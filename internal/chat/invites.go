package chat

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"errors"
	"time"

	"github.com/jackc/pgx/v5"
)

// Invite links and join requests (SPEC 6.7).

var (
	ErrLinkGone = errors.New("chat: the invite link is no longer valid")
	ErrBanned   = errors.New("chat: banned from this chat")
	ErrPending  = errors.New("chat: join request sent")
)

// Invite is one invite link.
type Invite struct {
	ID        int64      `json:"id"`
	Token     string     `json:"token"`
	Name      string     `json:"name,omitempty"`
	By        string     `json:"by,omitempty"`
	Expires   *time.Time `json:"expires,omitempty"`
	Limit     int        `json:"limit,omitempty"`
	Used      int        `json:"used"`
	Approval  bool       `json:"approval,omitempty"`
	Primary   bool       `json:"primary,omitempty"`
	Revoked   bool       `json:"revoked,omitempty"`
	CreatedAt time.Time  `json:"created_at"`
	ChatID    int64      `json:"-"`
}

func newToken() string {
	b := make([]byte, 16)
	_, _ = rand.Read(b)
	return base64.RawURLEncoding.EncodeToString(b)
}

// CreateInvite makes a link; a new primary link revokes the old one.
func (s *Store) CreateInvite(ctx context.Context, chatID int64, by, name string, expires *time.Time, limit int, approval, primary bool) (*Invite, error) {
	if primary {
		if _, err := s.pool.Exec(ctx, `UPDATE chat_invite_links SET revoked = true WHERE chat_id = $1 AND is_primary`, chatID); err != nil {
			return nil, err
		}
	}
	inv := &Invite{Token: newToken(), Name: name, By: by, Expires: expires, Limit: limit, Approval: approval, Primary: primary, ChatID: chatID}
	err := s.pool.QueryRow(ctx, `INSERT INTO chat_invite_links (chat_id, token, name, created_by, expires_at, usage_limit, requires_approval, is_primary)
		VALUES ($1, $2, $3, NULLIF($4, ''), $5, $6, $7, $8) RETURNING id, created_at`,
		chatID, inv.Token, name, by, expires, limit, approval, primary).Scan(&inv.ID, &inv.CreatedAt)
	return inv, err
}

const inviteCols = `id, token, name, COALESCE(created_by, ''), expires_at, usage_limit, usage_count, requires_approval, is_primary, revoked, created_at, chat_id`

func scanInvite(r pgx.Row) (*Invite, error) {
	i := &Invite{}
	err := r.Scan(&i.ID, &i.Token, &i.Name, &i.By, &i.Expires, &i.Limit, &i.Used, &i.Approval, &i.Primary, &i.Revoked, &i.CreatedAt, &i.ChatID)
	return i, err
}

// Invites lists a chat's links, newest first.
func (s *Store) Invites(ctx context.Context, chatID int64) ([]*Invite, error) {
	rows, err := s.pool.Query(ctx, `SELECT `+inviteCols+` FROM chat_invite_links WHERE chat_id = $1 ORDER BY is_primary DESC, id DESC LIMIT 200`, chatID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []*Invite
	for rows.Next() {
		i, err := scanInvite(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, i)
	}
	return out, rows.Err()
}

// InviteByToken finds a link, valid or not.
func (s *Store) InviteByToken(ctx context.Context, token string) (*Invite, error) {
	i, err := scanInvite(s.pool.QueryRow(ctx, `SELECT `+inviteCols+` FROM chat_invite_links WHERE token = $1`, token))
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrLinkGone
	}
	return i, err
}

// RevokeInvite stops a link working.
func (s *Store) RevokeInvite(ctx context.Context, chatID, id int64) error {
	_, err := s.pool.Exec(ctx, `UPDATE chat_invite_links SET revoked = true WHERE chat_id = $1 AND id = $2`, chatID, id)
	return err
}

// JoinedVia lists who joined through a link.
func (s *Store) JoinedVia(ctx context.Context, chatID, link int64) ([]string, error) {
	rows, err := s.pool.Query(ctx, `SELECT user_id FROM chat_members WHERE chat_id = $1 AND invite_id = $2 ORDER BY joined_at DESC LIMIT 500`, chatID, link)
	if err != nil {
		return nil, err
	}
	return pgx.CollectRows(rows, pgx.RowTo[string])
}

// usable: not revoked, not expired, not used up.
func (i *Invite) usable(now time.Time) bool {
	return !i.Revoked && (i.Expires == nil || i.Expires.After(now)) && (i.Limit == 0 || i.Used < i.Limit)
}

// JoinRequest is someone waiting to be let in.
type JoinRequest struct {
	User string    `json:"user"`
	Link int64     `json:"link,omitempty"`
	At   time.Time `json:"at"`
	Name string    `json:"name,omitempty"`
}

// ------------------------------------------------------------- service

// InvitePreview is what a link shows before joining: never the messages.
func (s *Service) InvitePreview(ctx context.Context, token string) (map[string]any, error) {
	inv, err := s.Store.InviteByToken(ctx, token)
	if err != nil || !inv.usable(s.now()) {
		return nil, ErrLinkGone
	}
	info, err := s.Store.ChatInfo(ctx, inv.ChatID)
	if err != nil || !isGroupish(info.Type) {
		return nil, ErrLinkGone
	}
	return map[string]any{"title": info.Title, "type": info.Type, "about": info.About, "members": info.Members, "approval": inv.Approval}, nil
}

// JoinByLink lets user in through a link, or files a join request when the
// link asks for approval. A ban that has not ended keeps them out.
func (s *Service) JoinByLink(ctx context.Context, user, token string) (int64, error) {
	if !s.joinLimit.allow(user) {
		return 0, ErrRate
	}
	inv, err := s.Store.InviteByToken(ctx, token)
	if err != nil || !inv.usable(s.now()) {
		return 0, ErrLinkGone
	}
	info, err := s.Store.ChatInfo(ctx, inv.ChatID)
	if err != nil || !isGroupish(info.Type) {
		return 0, ErrLinkGone
	}
	m, err := s.Store.MemberOf(ctx, inv.ChatID, user)
	if err != nil {
		return 0, err
	}
	if m != nil && m.Role == "banned" && (m.Until == nil || m.Until.After(s.now())) {
		return 0, ErrBanned
	}
	if m != nil && m.Role != "left" && m.Role != "banned" {
		return inv.ChatID, nil // already in
	}
	if info.Members >= memberLimit(info.Type) {
		return 0, ErrFull
	}
	if inv.Approval {
		_, err := s.Store.pool.Exec(ctx, `INSERT INTO chat_join_requests (chat_id, user_id, link_id) VALUES ($1, $2, $3)
			ON CONFLICT (chat_id, user_id) DO UPDATE SET status = 'pending', link_id = EXCLUDED.link_id, created_at = now()`, inv.ChatID, user, inv.ID)
		if err != nil {
			return 0, err
		}
		s.deliver(s.admins(ctx, inv.ChatID, info, ActInvite), "join_request", map[string]any{"chat": inv.ChatID, "user": user}, nil)
		return 0, ErrPending
	}
	if err := s.Store.AddMember(ctx, inv.ChatID, user, "", inv.ID); err != nil {
		return 0, err
	}
	s.service(ctx, inv.ChatID, user, 0, map[string]any{"action": "join"})
	s.tell([]string{user}, inv.ChatID, false)
	return inv.ChatID, nil
}

// admins are the members who may do act (who hears about join requests).
func (s *Service) admins(ctx context.Context, chatID int64, info *ChatInfo, act Action) []string {
	list, _ := s.Store.MemberList(ctx, chatID, "", 200, 0)
	var out []string
	for _, m := range list {
		if (m.Role == "owner" || m.Role == "admin") && can(info.Type, info.Settings, m, act, s.now()) {
			out = append(out, m.User)
		}
	}
	return out
}

// JoinRequests lists pending requests, for admins with the invite right (a
// member who may invite still does not decide who gets in).
func (s *Service) JoinRequests(ctx context.Context, actor string, chatID int64) ([]JoinRequest, error) {
	info, me, err := s.groupCtx(ctx, chatID, actor)
	if err != nil {
		return nil, err
	}
	if (me.Role != "owner" && me.Role != "admin") || !can(info.Type, info.Settings, me, ActInvite, s.now()) {
		return nil, ErrForbidden
	}
	rows, err := s.Store.pool.Query(ctx, `SELECT user_id, COALESCE(link_id, 0), created_at FROM chat_join_requests WHERE chat_id = $1 AND status = 'pending' ORDER BY created_at LIMIT 500`, chatID)
	if err != nil {
		return nil, err
	}
	out, err := pgx.CollectRows(rows, func(r pgx.CollectableRow) (JoinRequest, error) {
		var j JoinRequest
		err := r.Scan(&j.User, &j.Link, &j.At)
		return j, err
	})
	for i := range out {
		if u, err := s.users.Get(out[i].User); err == nil {
			out[i].Name = u.DisplayName
		}
	}
	return out, err
}

// DecideRequests approves or declines one request, or every pending one when
// user is "".
func (s *Service) DecideRequests(ctx context.Context, actor string, chatID int64, user string, approve bool) (int, error) {
	pending, err := s.JoinRequests(ctx, actor, chatID)
	if err != nil {
		return 0, err
	}
	n := 0
	var joined []string
	for _, j := range pending {
		if user != "" && j.User != user {
			continue
		}
		status := "declined"
		if approve {
			if err := s.Store.AddApproved(ctx, chatID, j.User, actor, j.Link); err != nil {
				return n, err
			}
			status = "approved"
			joined = append(joined, j.User)
		}
		if _, err := s.Store.pool.Exec(ctx, `UPDATE chat_join_requests SET status = $3 WHERE chat_id = $1 AND user_id = $2`, chatID, j.User, status); err != nil {
			return n, err
		}
		n++
	}
	if len(joined) > 0 {
		s.Store.Log(ctx, chatID, actor, "approve", map[string]any{"users": joined})
		s.service(ctx, chatID, actor, 0, map[string]any{"action": "add", "users": joined})
		s.tell(joined, chatID, false)
	}
	return n, nil
}

// MakeInvite creates a link (anyone who may invite).
func (s *Service) MakeInvite(ctx context.Context, actor string, chatID int64, name string, expires *time.Time, limit int, approval bool) (*Invite, error) {
	info, me, err := s.groupCtx(ctx, chatID, actor)
	if err != nil {
		return nil, err
	}
	if !can(info.Type, info.Settings, me, ActInvite, s.now()) || limit < 0 || limit > 100000 || UTF16Len(name) > 32 {
		return nil, ErrForbidden
	}
	inv, err := s.Store.CreateInvite(ctx, chatID, actor, CleanText(name), expires, limit, approval, false)
	if err == nil {
		s.Store.Log(ctx, chatID, actor, "invite", map[string]any{"id": inv.ID, "name": inv.Name})
	}
	return inv, err
}

// ListInvites: everybody who may invite sees the links; others none.
func (s *Service) ListInvites(ctx context.Context, actor string, chatID int64) ([]*Invite, error) {
	info, me, err := s.groupCtx(ctx, chatID, actor)
	if err != nil {
		return nil, err
	}
	if !can(info.Type, info.Settings, me, ActInvite, s.now()) {
		return nil, ErrForbidden
	}
	return s.Store.Invites(ctx, chatID)
}

// DropInvite revokes a link: its creator, or an admin who may invite. The
// primary link is replaced by a new one.
func (s *Service) DropInvite(ctx context.Context, actor string, chatID, id int64) error {
	info, me, err := s.groupCtx(ctx, chatID, actor)
	if err != nil {
		return err
	}
	list, err := s.Store.Invites(ctx, chatID)
	if err != nil {
		return err
	}
	for _, inv := range list {
		if inv.ID != id {
			continue
		}
		if inv.By != actor && !((me.Role == "owner" || me.Role == "admin") && can(info.Type, info.Settings, me, ActInvite, s.now())) {
			return ErrForbidden
		}
		if inv.Primary {
			_, err = s.Store.CreateInvite(ctx, chatID, actor, "", nil, 0, false, true)
		} else {
			err = s.Store.RevokeInvite(ctx, chatID, id)
		}
		if err == nil {
			s.Store.Log(ctx, chatID, actor, "revoke", map[string]any{"id": id})
		}
		return err
	}
	return ErrNotMember
}
