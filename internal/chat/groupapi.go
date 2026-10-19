package chat

import (
	"encoding/json"
	"net/http"
	"strconv"
	"time"
)

// The REST side of groups and channels (SPEC 6.7). Each handler starts with
// groupCtx (or the service call that does), so an outsider learns nothing,
// not even that a chat exists.

func decode(w http.ResponseWriter, r *http.Request, v any) bool {
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 64<<10)).Decode(v); err != nil {
		writeErr(w, ErrForbidden)
		return false
	}
	return true
}

func pathID(r *http.Request, name string) int64 {
	v, _ := strconv.ParseInt(r.PathValue(name), 10, 64)
	return v
}

// CreateGroup is POST /api/chat/groups.
func (a *API) CreateGroup(w http.ResponseWriter, r *http.Request) {
	var req CreateReq
	if !decode(w, r, &req) {
		return
	}
	id, refused, err := a.Svc.CreateGroup(r.Context(), a.User(r), req)
	if err != nil {
		writeErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"chat": id, "refused": refused})
}

// GroupInfo is GET /api/chat/chats/{id}/info: the chat, the reader's own
// place and what they may do there.
func (a *API) GroupInfo(w http.ResponseWriter, r *http.Request) {
	ctx, me := r.Context(), a.User(r)
	info, m, err := a.Svc.groupCtx(ctx, pathID(r, "id"), me)
	if err != nil {
		writeErr(w, err)
		return
	}
	now := a.Svc.now()
	mayList := map[string]bool{}
	for k, act := range map[string]Action{"send": ActSend, "media": ActSendMedia, "invite": ActInvite, "pin": ActPin, "info": ActChangeInfo,
		"topics": ActManageTopics, "delete": ActDeleteOthers, "ban": ActBan, "admins": ActAddAdmins, "calls": ActManageCalls} {
		mayList[k] = can(info.Type, info.Settings, m, act, now)
	}
	out := map[string]any{"chat": info, "me": m, "may": mayList}
	if mayList["invite"] {
		if list, err := a.Svc.Store.Invites(ctx, info.ID); err == nil {
			for _, inv := range list {
				if inv.Primary && !inv.Revoked {
					out["link"] = inv.Token
				}
			}
		}
	}
	writeJSON(w, http.StatusOK, out)
}

// GroupEdit is POST /api/chat/chats/{id}/info.
func (a *API) GroupEdit(w http.ResponseWriter, r *http.Request) {
	var req InfoReq
	if !decode(w, r, &req) {
		return
	}
	info, err := a.Svc.EditChat(r.Context(), a.User(r), pathID(r, "id"), req)
	if err != nil {
		writeErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"chat": info})
}

// GroupMembers is GET /api/chat/chats/{id}/members?q=&offset=, with names.
func (a *API) GroupMembers(w http.ResponseWriter, r *http.Request) {
	ctx, me := r.Context(), a.User(r)
	id := pathID(r, "id")
	info, m, err := a.Svc.groupCtx(ctx, id, me)
	if err != nil {
		writeErr(w, err)
		return
	}
	if info.Type == "channel" && m.Role != "owner" && m.Role != "admin" {
		writeErr(w, ErrForbidden) // subscribers do not see each other
		return
	}
	off, _ := strconv.Atoi(r.URL.Query().Get("offset"))
	list, err := a.Svc.Store.MemberList(ctx, id, r.URL.Query().Get("q"), 200, off)
	if err != nil {
		writeErr(w, err)
		return
	}
	for _, x := range list {
		if u, err := a.Svc.users.Get(x.User); err == nil {
			x.Name, x.Photo = u.DisplayName, u.Avatar != ""
		}
		if x.Name == "" {
			x.Name = x.User
		}
	}
	writeJSON(w, http.StatusOK, map[string]any{"members": list})
}

// GroupAdd is POST /api/chat/chats/{id}/members {"users":[...]}.
func (a *API) GroupAdd(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Users []string `json:"users"`
	}
	if !decode(w, r, &body) {
		return
	}
	done, refused, err := a.Svc.AddMembers(r.Context(), a.User(r), pathID(r, "id"), body.Users)
	if err != nil {
		writeErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"added": done, "refused": refused})
}

// GroupMember is POST /api/chat/chats/{id}/members/{user}.
func (a *API) GroupMember(w http.ResponseWriter, r *http.Request) {
	var req MemberReq
	if !decode(w, r, &req) {
		return
	}
	if err := a.Svc.ChangeMember(r.Context(), a.User(r), pathID(r, "id"), r.PathValue("user"), req); err != nil {
		writeErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}

// GroupLeave is POST /api/chat/chats/{id}/leave.
func (a *API) GroupLeave(w http.ResponseWriter, r *http.Request) {
	if err := a.Svc.Leave(r.Context(), a.User(r), pathID(r, "id")); err != nil {
		writeErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}

// GroupTransfer is POST /api/chat/chats/{id}/transfer {"user","password"}.
func (a *API) GroupTransfer(w http.ResponseWriter, r *http.Request) {
	var body struct {
		User     string `json:"user"`
		Password string `json:"password"`
	}
	if !decode(w, r, &body) {
		return
	}
	if err := a.Svc.TransferOwnership(r.Context(), a.User(r), pathID(r, "id"), body.User, body.Password); err != nil {
		writeErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}

// GroupDelete is POST /api/chat/chats/{id}/delete {"title": the chat's title}:
// typing the title is the confirmation.
func (a *API) GroupDelete(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Title string `json:"title"`
	}
	if !decode(w, r, &body) {
		return
	}
	ctx, id := r.Context(), pathID(r, "id")
	info, _, err := a.Svc.groupCtx(ctx, id, a.User(r))
	if err != nil {
		writeErr(w, err)
		return
	}
	if body.Title != info.Title {
		writeErr(w, ErrForbidden)
		return
	}
	if err := a.Svc.DeleteChat(ctx, a.User(r), id); err != nil {
		writeErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}

// GroupInvites is GET (list) and POST (make) /api/chat/chats/{id}/invites.
func (a *API) GroupInvites(w http.ResponseWriter, r *http.Request) {
	ctx, me, id := r.Context(), a.User(r), pathID(r, "id")
	if r.Method == http.MethodGet {
		list, err := a.Svc.ListInvites(ctx, me, id)
		if err != nil {
			writeErr(w, err)
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"invites": list})
		return
	}
	var body struct {
		Name     string     `json:"name"`
		Expires  *time.Time `json:"expires"`
		Limit    int        `json:"limit"`
		Approval bool       `json:"approval"`
	}
	if !decode(w, r, &body) {
		return
	}
	inv, err := a.Svc.MakeInvite(ctx, me, id, body.Name, body.Expires, body.Limit, body.Approval)
	if err != nil {
		writeErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"invite": inv})
}

// GroupInviteRevoke is POST /api/chat/chats/{id}/invites/{link}/revoke.
func (a *API) GroupInviteRevoke(w http.ResponseWriter, r *http.Request) {
	if err := a.Svc.DropInvite(r.Context(), a.User(r), pathID(r, "id"), pathID(r, "link")); err != nil {
		writeErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}

// GroupInviteJoined is GET /api/chat/chats/{id}/invites/{link}/joined.
func (a *API) GroupInviteJoined(w http.ResponseWriter, r *http.Request) {
	ctx, id := r.Context(), pathID(r, "id")
	if _, err := a.Svc.ListInvites(ctx, a.User(r), id); err != nil {
		writeErr(w, err)
		return
	}
	users, err := a.Svc.Store.JoinedVia(ctx, id, pathID(r, "link"))
	if err != nil {
		writeErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"users": users})
}

// InviteInfo is GET /api/chat/invite/{token}: what the link leads to.
func (a *API) InviteInfo(w http.ResponseWriter, r *http.Request) {
	p, err := a.Svc.InvitePreview(r.Context(), r.PathValue("token"))
	if err != nil {
		writeErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, p)
}

// InviteJoin is POST /api/chat/invite/{token}/join.
func (a *API) InviteJoin(w http.ResponseWriter, r *http.Request) {
	id, err := a.Svc.JoinByLink(r.Context(), a.User(r), r.PathValue("token"))
	if err == ErrPending {
		writeJSON(w, http.StatusAccepted, map[string]any{"pending": true, "text": "درخواست عضویت فرستاده شد؛ پس از تأیید مدیر عضو می‌شوید."})
		return
	}
	if err != nil {
		writeErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"chat": id})
}

// GroupRequests is GET (list) and POST {"user" or "", "approve"} /requests.
func (a *API) GroupRequests(w http.ResponseWriter, r *http.Request) {
	ctx, me, id := r.Context(), a.User(r), pathID(r, "id")
	if r.Method == http.MethodGet {
		list, err := a.Svc.JoinRequests(ctx, me, id)
		if err != nil {
			writeErr(w, err)
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"requests": list})
		return
	}
	var body struct {
		User    string `json:"user"`
		Approve bool   `json:"approve"`
	}
	if !decode(w, r, &body) {
		return
	}
	n, err := a.Svc.DecideRequests(ctx, me, id, body.User, body.Approve)
	if err != nil {
		writeErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]int{"done": n})
}

// GroupLog is GET /api/chat/chats/{id}/log?action=&actor= (admins).
func (a *API) GroupLog(w http.ResponseWriter, r *http.Request) {
	ctx, me, id := r.Context(), a.User(r), pathID(r, "id")
	_, m, err := a.Svc.groupCtx(ctx, id, me)
	if err != nil {
		writeErr(w, err)
		return
	}
	if m.Role != "owner" && m.Role != "admin" {
		writeErr(w, ErrForbidden)
		return
	}
	list, err := a.Svc.Store.AdminLog(ctx, id, r.URL.Query().Get("action"), r.URL.Query().Get("actor"))
	if err != nil {
		writeErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"log": list})
}

// GroupTopics is GET (list) and POST (make) /api/chat/chats/{id}/topics.
func (a *API) GroupTopics(w http.ResponseWriter, r *http.Request) {
	ctx, me, id := r.Context(), a.User(r), pathID(r, "id")
	if r.Method == http.MethodGet {
		info, _, err := a.Svc.groupCtx(ctx, id, me)
		if err != nil {
			writeErr(w, err)
			return
		}
		if !info.Forum {
			writeJSON(w, http.StatusOK, map[string]any{"topics": []Topic{}})
			return
		}
		list, err := a.Svc.Store.Topics(ctx, id, me)
		if err != nil {
			writeErr(w, err)
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"topics": list})
		return
	}
	var req TopicReq
	if !decode(w, r, &req) {
		return
	}
	t, err := a.Svc.CreateTopic(ctx, me, id, req)
	if err != nil {
		writeErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"topic": t})
}

// GroupTopic is POST /api/chat/chats/{id}/topics/{topic}.
func (a *API) GroupTopic(w http.ResponseWriter, r *http.Request) {
	var req TopicReq
	if !decode(w, r, &req) {
		return
	}
	t, err := a.Svc.EditTopic(r.Context(), a.User(r), pathID(r, "id"), pathID(r, "topic"), req)
	if err != nil {
		writeErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"topic": t})
}

// MessageReaders is GET /api/chat/chats/{id}/messages/{msg}/readers: who has
// read a message, in groups of up to 100 members, for a week (SPEC 6.3).
func (a *API) MessageReaders(w http.ResponseWriter, r *http.Request) {
	ctx, me, id := r.Context(), a.User(r), pathID(r, "id")
	info, _, err := a.Svc.groupCtx(ctx, id, me)
	if err != nil {
		writeErr(w, err)
		return
	}
	msgs, err := a.Svc.Store.ByIDs(ctx, id, me, []int64{pathID(r, "msg")})
	if err != nil || len(msgs) != 1 {
		writeErr(w, ErrNotMember)
		return
	}
	m := msgs[0]
	if info.Type == "channel" || info.Members > 100 || time.Since(m.Created) > 7*24*time.Hour {
		writeJSON(w, http.StatusOK, map[string]any{"readers": []any{}, "hidden": true})
		return
	}
	users, err := a.Svc.Store.Readers(ctx, id, m.PTS, m.Sender)
	if err != nil {
		writeErr(w, err)
		return
	}
	type who struct {
		ID   string `json:"id"`
		Name string `json:"name"`
	}
	out := make([]who, 0, len(users))
	for _, u := range users {
		name := u
		if x, err := a.Svc.users.Get(u); err == nil && x.DisplayName != "" {
			name = x.DisplayName
		}
		out = append(out, who{u, name})
	}
	writeJSON(w, http.StatusOK, map[string]any{"readers": out})
}
