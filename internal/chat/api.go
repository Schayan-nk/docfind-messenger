package chat

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"slices"
	"sort"
	"strconv"
	"strings"
)

// API is the REST half of the messenger, mounted under /api/chat/. The socket
// carries real-time events; these carry pages, lists and settings. User says
// who is asking: DocFind's auth middleware has already run.
type API struct {
	Svc  *Service
	User func(r *http.Request) string
}

type ctxKey int

const dirAdminKey ctxKey = iota

// WithDirectoryAdmin marks a request as coming from someone the find layer has
// confirmed may see every colleague's full contact details (an administrator).
// The chat package cannot evaluate docfind permissions itself, so the find
// wrapper sets this before calling Card.
func WithDirectoryAdmin(ctx context.Context) context.Context {
	return context.WithValue(ctx, dirAdminKey, true)
}

func directoryAdmin(ctx context.Context) bool {
	v, _ := ctx.Value(dirAdminKey).(bool)
	return v
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

func writeErr(w http.ResponseWriter, err error) {
	code, text := errorText(err)
	status := http.StatusInternalServerError
	switch code {
	case "not_member", "no_call":
		status = http.StatusNotFound // never confirm that a chat someone is not in exists
	case "forbidden", "blocked", "call_privacy":
		status = http.StatusForbidden
	case "empty", "bad_id", "no_user":
		status = http.StatusBadRequest
	case "rate":
		status = http.StatusTooManyRequests
	}
	writeJSON(w, status, map[string]string{"error": text, "code": code})
}

// Chats is GET /api/chat/chats.
func (a *API) Chats(w http.ResponseWriter, r *http.Request) {
	me := a.User(r)
	ctx := r.Context()
	list, err := a.Svc.Store.Chats(ctx, me)
	if err != nil {
		writeErr(w, err)
		return
	}
	a.dress(ctx, me, list)
	writeJSON(w, http.StatusOK, map[string]any{"chats": list})
}

// dress fills in who each private chat is with, and its title.
func (a *API) dress(ctx context.Context, me string, list []*Chat) {
	ids := []string{me}
	for _, c := range list {
		if c.Peer != nil {
			ids = append(ids, c.Peer.ID)
		}
	}
	profiles, _ := a.Svc.Store.Profiles(ctx, ids)
	for _, c := range list {
		switch c.Type {
		case "saved":
			c.Title = "پیام‌های ذخیره‌شده"
			c.Peer = nil
		case "private", "secret":
			if c.Peer != nil {
				c.Peer = a.Svc.person(ctx, me, c.Peer.ID, profiles)
				c.Title = c.Peer.Name
			}
		default:
			c.Peer = nil // groups and channels have members, not a peer
			if c.Last != nil && c.Last.Sender != "" && c.Type != "channel" {
				c.Last.FromName = a.Svc.displayName(c.Last.Sender)
			}
		}
	}
}

// Open is POST /api/chat/open {"user":"name"}: the private chat with that
// person (made on first use), or Saved Messages when the name is your own.
func (a *API) Open(w http.ResponseWriter, r *http.Request) {
	me := a.User(r)
	var body struct {
		User string `json:"user"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 4<<10)).Decode(&body); err != nil {
		writeErr(w, ErrNoUser)
		return
	}
	target, err := a.Svc.users.Get(strings.TrimSpace(body.User))
	if err != nil || target.Disabled {
		writeErr(w, ErrNoUser)
		return
	}
	ctx := r.Context()
	if target.Username != me {
		if b, err := a.Svc.Store.Blocked(ctx, me, target.Username); err == nil && b {
			writeErr(w, ErrBlocked)
			return
		}
	}
	id, err := a.Svc.Store.OpenPrivate(ctx, me, target.Username)
	if err != nil {
		writeErr(w, err)
		return
	}
	list, err := a.Svc.Store.Chats(ctx, me)
	if err != nil {
		writeErr(w, err)
		return
	}
	for _, c := range list {
		if c.ID == id {
			a.dress(ctx, me, []*Chat{c})
			writeJSON(w, http.StatusOK, map[string]any{"chat": c})
			return
		}
	}
	writeErr(w, errors.New("chat vanished"))
}

// Messages is GET /api/chat/chats/{id}/messages?before=PTS&limit=N.
func (a *API) Messages(w http.ResponseWriter, r *http.Request) {
	me := a.User(r)
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil || id <= 0 {
		writeErr(w, ErrNotMember)
		return
	}
	ctx := r.Context()
	if _, err := a.Svc.Store.Role(ctx, id, me); err != nil {
		writeErr(w, ErrNotMember)
		return
	}
	q := r.URL.Query()
	before, _ := strconv.ParseInt(q.Get("before"), 10, 64)
	limit, _ := strconv.Atoi(q.Get("limit"))
	pq := PageQuery{Before: before, Limit: limit, From: a.Svc.visibleFrom(ctx, id, me)}
	if t := q.Get("topic"); t != "" {
		pq.ByTopic = true
		pq.Topic, _ = strconv.ParseInt(t, 10, 64)
	}
	msgs, err := a.Svc.Store.Page(ctx, id, me, pq)
	if err != nil {
		writeErr(w, err)
		return
	}
	a.Svc.dressDocs(me, msgs)
	if typ, _, err := a.Svc.Store.ChatType(ctx, id, me); err == nil && isGroupish(typ) {
		names := map[string]string{}
		for _, m := range msgs {
			if m.Sender == "" {
				continue
			}
			if _, ok := names[m.Sender]; !ok {
				names[m.Sender] = a.Svc.displayName(m.Sender)
			}
			m.FromName = names[m.Sender]
		}
		if typ == "channel" {
			a.Svc.Store.Views(ctx, id, msgs)
		}
	}
	writeJSON(w, http.StatusOK, map[string]any{"messages": msgs})
}

// People is GET /api/chat/people?q=...: colleagues to start a chat with.
// Matching is on the normalised display name and the username, so Arabic and
// Persian letter forms and digits find each other.
func (a *API) People(w http.ResponseWriter, r *http.Request) {
	me := a.User(r)
	q := Normalize(strings.TrimSpace(r.URL.Query().Get("q")))
	type row struct {
		ID    string `json:"id"`
		Name  string `json:"name"`
		Photo bool   `json:"photo,omitempty"`
	}
	var out []row
	for _, u := range a.Svc.users.List() {
		if u.Disabled || u.Username == me {
			continue
		}
		name := u.DisplayName
		if name == "" {
			name = u.Username
		}
		if q != "" && !strings.Contains(Normalize(name), q) && !strings.Contains(strings.ToLower(u.Username), q) {
			continue
		}
		out = append(out, row{u.Username, name, u.Avatar != ""})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	if len(out) > 50 {
		out = out[:50]
	}
	writeJSON(w, http.StatusOK, map[string]any{"people": out})
}

// Card is GET /api/chat/people/{user}/card: a colleague's contact details as
// DocFind has them from the office-automation directory (staff.go): the
// personnel code, the e-mail addresses, the mobile. What a colleague is told
// about a colleague; nothing that signs anybody in.
func (a *API) Card(w http.ResponseWriter, r *http.Request) {
	u, err := a.Svc.users.Get(r.PathValue("user"))
	if err != nil {
		writeErr(w, ErrNoUser)
		return
	}
	name := u.DisplayName
	if name == "" {
		name = u.Username
	}
	card := map[string]any{
		"id": u.Username, "name": name, "english_name": u.EnglishName,
		"photo": u.Avatar != "", "disabled": u.Disabled,
	}
	// Personnel code, e-mail addresses and mobile are personal data. On a LAN an
	// internal directory open to every colleague was fine; once this app is on
	// the public internet, "any one signed-in account" includes a compromised or
	// low-trust one, so these fields go only to the person themselves, to a
	// colleague they share a chat with or who is their contact, or to a
	// directory administrator. Name, English name and photo stay open so the
	// messenger's people list still works.
	me := a.User(r)
	full := directoryAdmin(r.Context()) || strings.EqualFold(me, u.Username)
	if !full {
		if ok, _ := a.Svc.Store.SharesChat(r.Context(), me, u.Username); ok {
			full = true
		} else if ok, _ := a.Svc.Store.IsContact(r.Context(), me, u.Username); ok {
			full = true
		}
	}
	if full {
		var emails []string
		for _, e := range []string{u.Email, u.SecondEmail} {
			if e = strings.TrimSpace(e); e != "" && !slices.Contains(emails, e) {
				emails = append(emails, e)
			}
		}
		card["personnel_code"] = u.PersonnelCode
		card["emails"] = emails
		card["phone"] = u.Phone
	}
	writeJSON(w, http.StatusOK, map[string]any{"card": card})
}

// Block is POST /api/chat/block {"user":"name","on":true}.
func (a *API) Block(w http.ResponseWriter, r *http.Request) {
	me := a.User(r)
	var body struct {
		User string `json:"user"`
		On   bool   `json:"on"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 4<<10)).Decode(&body); err != nil {
		writeErr(w, ErrNoUser)
		return
	}
	target, err := a.Svc.users.Get(body.User)
	if err != nil || target.Username == me {
		writeErr(w, ErrNoUser)
		return
	}
	if err := a.Svc.Store.SetBlock(r.Context(), me, target.Username, body.On); err != nil {
		writeErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]bool{"blocked": body.On})
}

// PrivacyGet is GET /api/chat/privacy; PrivacySet is POST with the same shape.
func (a *API) PrivacyGet(w http.ResponseWriter, r *http.Request) {
	me := a.User(r)
	p, err := a.Svc.Store.Profiles(r.Context(), []string{me})
	if err != nil {
		writeErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, p[me].Privacy)
}

func (a *API) PrivacySet(w http.ResponseWriter, r *http.Request) {
	me := a.User(r)
	var p Privacy
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 32<<10)).Decode(&p); err != nil {
		writeErr(w, ErrForbidden)
		return
	}
	for _, rule := range []*Rule{&p.LastSeen, &p.Photo, &p.Bio, &p.Forwards, &p.Calls, &p.Invites, &p.Voice} {
		switch rule.Mode {
		case "", "everybody", "contacts", "nobody":
		default:
			rule.Mode = "everybody"
		}
		rule.Always, rule.Never = known(a.Svc.users, rule.Always), known(a.Svc.users, rule.Never)
	}
	if err := a.Svc.Store.SetPrivacy(r.Context(), me, p); err != nil {
		writeErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, p)
}

// known keeps only real usernames, at most 500 of them.
func known(users Users, list []string) []string {
	var out []string
	for _, n := range list {
		if u, err := users.Get(n); err == nil {
			out = append(out, u.Username)
		}
		if len(out) == 500 {
			break
		}
	}
	return out
}
