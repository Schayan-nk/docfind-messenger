package chat

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"time"

	"github.com/Schayan-nk/docfind-messenger/internal/auth"
)

// Groups, supergroups and channels (SPEC 6.2, 6.7). Every operation loads the
// chat and the caller's place in it, refuses an outsider exactly as an
// unknown chat, asks can(), and then acts, logs (the 48-hour admin log),
// posts a service message and tells the devices concerned.

var (
	ErrOwnerLeaves = errors.New("chat: the owner must hand the chat over first")
	ErrPrivacy     = errors.New("chat: their privacy settings do not allow it")
	ErrFull        = errors.New("chat: the chat is full")
	ErrSlowMode    = errors.New("chat: slow mode")
	ErrNewLinks    = errors.New("chat: new members cannot send links yet")
	ErrClosed      = errors.New("chat: the topic is closed")
	ErrPassword    = errors.New("chat: wrong password")
)

// groupCtx is the chat and the caller's membership, or ErrNotMember.
func (s *Service) groupCtx(ctx context.Context, chatID int64, user string) (*ChatInfo, *Member, error) {
	info, err := s.Store.ChatInfo(ctx, chatID)
	if err != nil {
		return nil, nil, ErrNotMember
	}
	m, err := s.Store.MemberOf(ctx, chatID, user)
	if err != nil {
		return nil, nil, err
	}
	if m == nil || m.Role == "left" || m.Role == "banned" {
		return nil, nil, ErrNotMember
	}
	if !isGroupish(info.Type) {
		return nil, nil, ErrForbidden
	}
	return info, m, nil
}

// service posts a service message ("X added Y") and delivers it. The names
// of the people it is about go with it, as they were at the time.
func (s *Service) service(ctx context.Context, chatID int64, actor string, topic int64, payload map[string]any) {
	if users, ok := payload["users"].([]string); ok {
		names := make([]string, len(users))
		for i, u := range users {
			names[i] = s.displayName(u)
		}
		payload["names"] = names
	}
	b, _ := json.Marshal(payload)
	members, _ := s.Store.Members(ctx, chatID)
	if _, up, created, err := s.Store.Send(ctx, chatID, actor, uuidV7(), Outgoing{Kind: "service", Service: b, Topic: topic, Silent: true, FromName: s.displayName(actor)}); err == nil && created {
		s.deliver(members, "update", up, nil)
	}
}

// displayName is how a colleague is shown: their display name, else their
// username.
func (s *Service) displayName(u string) string {
	if x, err := s.users.Get(u); err == nil && x.DisplayName != "" {
		return x.DisplayName
	}
	return u
}

// hasPhoto: the colleague has a DocFind picture.
func (s *Service) hasPhoto(u string) bool {
	x, err := s.users.Get(u)
	return err == nil && x.Avatar != ""
}

// tell sends people a "chat" event: the chat appeared, changed or went away
// for them, so their lists refresh.
func (s *Service) tell(users []string, chatID int64, removed bool) {
	s.deliver(users, "chat", map[string]any{"chat": chatID, "removed": removed}, nil)
}

// CreateReq is a new group or channel.
type CreateReq struct {
	Type    string   `json:"type"` // group | supergroup | channel
	Title   string   `json:"title"`
	About   string   `json:"about"`
	Members []string `json:"members"`
	Forum   bool     `json:"forum"`
}

// CreateGroup makes a group or channel. People whose privacy does not let the
// creator add them are left out and named in the answer (the creator can send
// them the invite link instead).
func (s *Service) CreateGroup(ctx context.Context, user string, req CreateReq) (int64, []string, error) {
	if !isGroupish(req.Type) {
		return 0, nil, ErrForbidden
	}
	title := strings.TrimSpace(CleanText(req.Title))
	if title == "" || UTF16Len(title) > 128 || UTF16Len(req.About) > 255 {
		return 0, nil, ErrEmpty
	}
	if req.Forum && req.Type != "supergroup" {
		req.Forum = false
	}
	add, refused := s.addable(ctx, user, req.Members)
	if len(add)+1 > memberLimit(req.Type) {
		return 0, nil, ErrFull
	}
	id, err := s.Store.CreateChat(ctx, req.Type, title, strings.TrimSpace(CleanText(req.About)), user, req.Forum, add)
	if err != nil {
		return 0, nil, err
	}
	if _, err := s.Store.CreateInvite(ctx, id, user, "", nil, 0, false, true); err != nil {
		return 0, nil, err
	}
	s.Store.Log(ctx, id, user, "create", map[string]any{"type": req.Type, "title": title})
	s.service(ctx, id, user, 0, map[string]any{"action": "create", "title": title})
	if len(add) > 0 {
		s.service(ctx, id, user, 0, map[string]any{"action": "add", "users": add})
	}
	s.tell(append(add, user), id, false)
	return id, refused, nil
}

// addable splits people into those who may be added by user and those whose
// "who may add me to groups" setting refuses (SPEC 6.1).
func (s *Service) addable(ctx context.Context, by string, users []string) (ok, refused []string) {
	seen := map[string]bool{by: true}
	var names []string
	for _, u := range users {
		x, err := s.users.Get(strings.TrimSpace(u))
		if err != nil || x.Disabled || seen[x.Username] {
			continue
		}
		seen[x.Username] = true
		names = append(names, x.Username)
	}
	profiles, _ := s.Store.Profiles(ctx, names)
	for _, u := range names {
		contact, _ := s.Store.IsContact(ctx, u, by)
		if profiles[u].Privacy.Invites.Allows(by, contact) {
			ok = append(ok, u)
		} else {
			refused = append(refused, u)
		}
	}
	return ok, refused
}

// AddMembers adds people to a group or channel.
func (s *Service) AddMembers(ctx context.Context, actor string, chatID int64, users []string) ([]string, []string, error) {
	info, me, err := s.groupCtx(ctx, chatID, actor)
	if err != nil {
		return nil, nil, err
	}
	if !can(info.Type, info.Settings, me, ActInvite, s.now()) {
		return nil, nil, ErrForbidden
	}
	add, refused := s.addable(ctx, actor, users)
	if info.Members+len(add) > memberLimit(info.Type) {
		return nil, nil, ErrFull
	}
	var done []string
	for _, u := range add {
		m, _ := s.Store.MemberOf(ctx, chatID, u)
		if m != nil && m.Role == "banned" && (m.Until == nil || m.Until.After(s.now())) && !can(info.Type, info.Settings, me, ActBan, s.now()) {
			refused = append(refused, u) // a ban is lifted by someone who may ban
			continue
		}
		if m != nil && m.Role != "left" && m.Role != "banned" {
			continue
		}
		if err := s.Store.AddMember(ctx, chatID, u, actor, 0); err != nil {
			return done, refused, err
		}
		done = append(done, u)
	}
	if len(done) > 0 {
		s.Store.Log(ctx, chatID, actor, "add", map[string]any{"users": done})
		s.service(ctx, chatID, actor, 0, map[string]any{"action": "add", "users": done})
		s.tell(done, chatID, false)
	}
	return done, refused, nil
}

// Leave takes the caller out of a group or channel. The owner hands it over
// first, unless they are the last one in it.
func (s *Service) Leave(ctx context.Context, user string, chatID int64) error {
	info, me, err := s.groupCtx(ctx, chatID, user)
	if err != nil {
		return err
	}
	if me.Role == "owner" && info.Members > 1 {
		return ErrOwnerLeaves
	}
	if err := s.Store.SetMember(ctx, chatID, user, "left", nil, "", nil); err != nil {
		return err
	}
	s.service(ctx, chatID, user, 0, map[string]any{"action": "leave"})
	s.tell([]string{user}, chatID, true)
	return nil
}

// MemberReq is one change to someone's place in a chat.
type MemberReq struct {
	Action string       `json:"action"` // ban | unban | kick | restrict | promote | demote
	Rights *AdminRights `json:"rights"`
	Perms  *Perms       `json:"perms"`
	Until  *time.Time   `json:"until"`
	Title  string       `json:"title"`
}

// ChangeMember bans, removes, restricts, promotes or demotes someone. The
// owner is never the target; an admin is handled only by the owner or by an
// admin who may add admins and holds every right the target holds.
func (s *Service) ChangeMember(ctx context.Context, actor string, chatID int64, target string, req MemberReq) error {
	info, me, err := s.groupCtx(ctx, chatID, actor)
	if err != nil {
		return err
	}
	them, err := s.Store.MemberOf(ctx, chatID, target)
	if err != nil {
		return err
	}
	if them == nil || target == actor || them.Role == "owner" {
		return ErrForbidden
	}
	now := s.now()
	if them.Role == "admin" && me.Role != "owner" && !(can(info.Type, info.Settings, me, ActAddAdmins, now) && them.Admin.within(me.rights())) {
		return ErrForbidden
	}
	title := strings.TrimSpace(CleanText(req.Title))
	if UTF16Len(title) > 16 {
		title = ""
	}
	var role string
	var rights any
	switch req.Action {
	case "ban", "kick":
		if !can(info.Type, info.Settings, me, ActBan, now) {
			return ErrForbidden
		}
		role = "banned"
		if req.Action == "kick" {
			role = "left"
		}
	case "unban":
		if !can(info.Type, info.Settings, me, ActBan, now) || them.Role != "banned" {
			return ErrForbidden
		}
		role = "left" // may be added again or join by link
	case "restrict":
		if !can(info.Type, info.Settings, me, ActBan, now) || req.Perms == nil || them.Role == "admin" {
			return ErrForbidden
		}
		role, rights = "restricted", req.Perms
	case "promote":
		if !can(info.Type, info.Settings, me, ActAddAdmins, now) || req.Rights == nil || !req.Rights.within(me.rights()) || them.Role == "left" || them.Role == "banned" {
			return ErrForbidden
		}
		role, rights = "admin", req.Rights
	case "demote":
		if them.Role != "admin" {
			return ErrForbidden
		}
		role = "member"
	default:
		return ErrForbidden
	}
	until := req.Until
	if role != "banned" && role != "restricted" {
		until = nil
	}
	if err := s.Store.SetMember(ctx, chatID, target, role, rights, title, until); err != nil {
		return err
	}
	s.Store.Log(ctx, chatID, actor, req.Action, map[string]any{"user": target, "rights": rights, "until": until, "title": title})
	switch req.Action {
	case "ban", "kick":
		s.service(ctx, chatID, actor, 0, map[string]any{"action": "remove", "users": []string{target}})
		s.tell([]string{target}, chatID, true)
	case "promote", "demote", "restrict", "unban":
		members, _ := s.Store.Members(ctx, chatID)
		s.deliver(members, "member", map[string]any{"chat": chatID, "user": target, "role": role}, nil)
	}
	return nil
}

// TransferOwnership hands a chat to another member, after the owner confirms
// with their password (SPEC 6.7). The old owner stays an admin.
func (s *Service) TransferOwnership(ctx context.Context, owner string, chatID int64, target, password string) error {
	_, me, err := s.groupCtx(ctx, chatID, owner)
	if err != nil {
		return err
	}
	if me.Role != "owner" {
		return ErrForbidden
	}
	pc, ok := s.users.(interface {
		Authenticate(username, password string) (*auth.User, error)
	})
	if !ok {
		return ErrForbidden
	}
	if _, err := pc.Authenticate(owner, password); err != nil {
		return ErrPassword
	}
	them, err := s.Store.MemberOf(ctx, chatID, target)
	if err != nil || them == nil || them.Role == "left" || them.Role == "banned" {
		return ErrNoUser
	}
	if err := s.Store.SetMember(ctx, chatID, target, "owner", nil, them.Title, nil); err != nil {
		return err
	}
	if err := s.Store.SetMember(ctx, chatID, owner, "admin", allRights, me.Title, nil); err != nil {
		return err
	}
	s.Store.Log(ctx, chatID, owner, "transfer", map[string]any{"user": target})
	s.service(ctx, chatID, owner, 0, map[string]any{"action": "owner", "users": []string{target}})
	return nil
}

// InfoReq changes a group's own data and settings; absent fields stay.
type InfoReq struct {
	Title         *string   `json:"title"`
	About         *string   `json:"about"`
	Forum         *bool     `json:"forum"`
	SlowMode      *int      `json:"slow_mode"`
	Perms         *Perms    `json:"perms"`
	Reactions     *[]string `json:"reactions"`
	NoReactions   *bool     `json:"no_reactions"`
	HiddenHistory *bool     `json:"hidden_history"`
	Signatures    *bool     `json:"signatures"`
	AntiSpamHours *int      `json:"antispam_hours"`
}

var slowSteps = map[int]bool{0: true, 10: true, 30: true, 60: true, 300: true, 900: true, 3600: true}

// EditChat applies InfoReq: the title, description and settings need the
// "change info" right; permissions, slow mode and anti-spam the "ban" right.
func (s *Service) EditChat(ctx context.Context, actor string, chatID int64, req InfoReq) (*ChatInfo, error) {
	info, me, err := s.groupCtx(ctx, chatID, actor)
	if err != nil {
		return nil, err
	}
	now := s.now()
	mayInfo := can(info.Type, info.Settings, me, ActChangeInfo, now)
	mayBan := can(info.Type, info.Settings, me, ActBan, now)
	need := func(ok bool) error {
		if !ok {
			return ErrForbidden
		}
		return nil
	}
	oldTitle := info.Title
	if req.Title != nil {
		if err := need(mayInfo); err != nil {
			return nil, err
		}
		t := strings.TrimSpace(CleanText(*req.Title))
		if t == "" || UTF16Len(t) > 128 {
			return nil, ErrEmpty
		}
		info.Title = t
	}
	if req.About != nil {
		if err := need(mayInfo); err != nil {
			return nil, err
		}
		if UTF16Len(*req.About) > 255 {
			return nil, ErrTooLong
		}
		info.About = strings.TrimSpace(CleanText(*req.About))
	}
	if req.Forum != nil {
		if err := need(mayInfo && info.Type != "channel"); err != nil {
			return nil, err
		}
		if *req.Forum && info.Type == "group" {
			info.Type = "supergroup" // topics need a supergroup, as in Telegram
		}
		info.Forum = *req.Forum
	}
	if req.SlowMode != nil {
		if err := need(mayBan && slowSteps[*req.SlowMode]); err != nil {
			return nil, err
		}
		info.Slow = *req.SlowMode
	}
	if req.Perms != nil {
		if err := need(mayBan); err != nil {
			return nil, err
		}
		info.Settings.Perms = req.Perms
	}
	if req.AntiSpamHours != nil {
		if err := need(mayBan && *req.AntiSpamHours >= 0 && *req.AntiSpamHours <= 24*30); err != nil {
			return nil, err
		}
		info.Settings.AntiSpamHours = *req.AntiSpamHours
	}
	if req.Reactions != nil || req.NoReactions != nil || req.HiddenHistory != nil || req.Signatures != nil {
		if err := need(mayInfo); err != nil {
			return nil, err
		}
		if req.Reactions != nil {
			var keep []string
			for _, e := range *req.Reactions {
				if validReaction(e) {
					keep = append(keep, e)
				}
			}
			info.Settings.Reactions = keep
		}
		if req.NoReactions != nil {
			info.Settings.NoReactions = *req.NoReactions
		}
		if req.HiddenHistory != nil {
			info.Settings.HiddenHistory = *req.HiddenHistory && info.Type == "supergroup"
		}
		if req.Signatures != nil {
			info.Settings.Signatures = *req.Signatures && info.Type == "channel"
		}
	}
	if err := s.Store.UpdateChat(ctx, info); err != nil {
		return nil, err
	}
	if info.Type != "" {
		_, _ = s.Store.pool.Exec(ctx, `UPDATE chats SET type = $2 WHERE id = $1 AND type = 'group'`, chatID, info.Type)
	}
	s.Store.Log(ctx, chatID, actor, "edit", req)
	if info.Title != oldTitle {
		s.service(ctx, chatID, actor, 0, map[string]any{"action": "title", "title": info.Title})
	}
	members, _ := s.Store.Members(ctx, chatID)
	s.deliver(members, "info", map[string]any{"chat": chatID}, nil)
	return info, nil
}

// DeleteChat removes a group or channel for everybody: the owner only.
func (s *Service) DeleteChat(ctx context.Context, owner string, chatID int64) error {
	_, me, err := s.groupCtx(ctx, chatID, owner)
	if err != nil {
		return err
	}
	if me.Role != "owner" {
		return ErrForbidden
	}
	members, _ := s.Store.Members(ctx, chatID)
	if _, err := s.Store.pool.Exec(ctx, `DELETE FROM chats WHERE id = $1`, chatID); err != nil {
		return err
	}
	s.tell(members, chatID, true)
	return nil
}
