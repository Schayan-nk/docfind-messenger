package chat

import (
	"context"
	"encoding/json"
	"errors"
	"time"

	"github.com/jackc/pgx/v5"
)

// Groups, supergroups and channels in PostgreSQL (migration 047).

// ChatInfo is a group's or channel's own data.
type ChatInfo struct {
	ID       int64    `json:"id"`
	Type     string   `json:"type"`
	Title    string   `json:"title"`
	About    string   `json:"about,omitempty"`
	Forum    bool     `json:"forum,omitempty"`
	Slow     int      `json:"slow_mode,omitempty"`
	Settings Settings `json:"settings"`
	Members  int      `json:"members"`
	Owner    string   `json:"owner,omitempty"`
	PTS      int64    `json:"-"`
}

// CreateChat makes a group, supergroup or channel with its owner and first
// members, in one transaction.
func (s *Store) CreateChat(ctx context.Context, typ, title, about, owner string, forum bool, members []string) (int64, error) {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return 0, err
	}
	defer tx.Rollback(ctx)
	var id int64
	if err := tx.QueryRow(ctx, `INSERT INTO chats (type, title, about, created_by, is_forum, last_message_at) VALUES ($1, $2, $3, $4, $5, now()) RETURNING id`,
		typ, title, about, owner, forum).Scan(&id); err != nil {
		return 0, err
	}
	if err := addMemberTx(ctx, tx, id, owner, "owner", "", 0); err != nil {
		return 0, err
	}
	for _, u := range members {
		if u == owner {
			continue
		}
		if err := addMemberTx(ctx, tx, id, u, "member", owner, 0); err != nil {
			return 0, err
		}
	}
	return id, tx.Commit(ctx)
}

// addMemberTx adds (or brings back) a member. Someone who left or whose ban
// ended comes back as a plain member, seeing history from now when the chat
// hides it.
func addMemberTx(ctx context.Context, tx pgx.Tx, chatID int64, user, role, by string, invite int64) error {
	if _, err := tx.Exec(ctx, `INSERT INTO chat_users_ext (user_id) VALUES ($1) ON CONFLICT DO NOTHING`, user); err != nil {
		return err
	}
	var inv any
	if invite > 0 {
		inv = invite
	}
	_, err := tx.Exec(ctx, `
		INSERT INTO chat_members (chat_id, user_id, role, added_by, invite_id, joined_pts, last_read_pts)
		VALUES ($1, $2, $3, NULLIF($4, ''), $5, (SELECT pts FROM chats WHERE id = $1), (SELECT pts FROM chats WHERE id = $1))
		ON CONFLICT (chat_id, user_id) DO UPDATE SET role = EXCLUDED.role, added_by = EXCLUDED.added_by,
			invite_id = EXCLUDED.invite_id, joined_at = now(), joined_pts = EXCLUDED.joined_pts,
			last_read_pts = GREATEST(chat_members.last_read_pts, EXCLUDED.last_read_pts), rights = '{}', restricted_until = NULL
		WHERE chat_members.role IN ('left', 'banned')`, chatID, user, role, by, inv)
	return err
}

// AddMember adds one person (role "member") to a group or channel; through
// invite, only while that link is still good.
func (s *Store) AddMember(ctx context.Context, chatID int64, user, by string, invite int64) error {
	return s.addMember(ctx, chatID, user, by, invite, true)
}

// AddApproved adds someone an admin let in from a join request: the link is
// recorded and counted, but the admin's decision stands even if the link has
// filled up or expired since the request.
func (s *Store) AddApproved(ctx context.Context, chatID int64, user, by string, invite int64) error {
	return s.addMember(ctx, chatID, user, by, invite, false)
}

func (s *Store) addMember(ctx context.Context, chatID int64, user, by string, invite int64, strict bool) error {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	if err := addMemberTx(ctx, tx, chatID, user, "member", by, invite); err != nil {
		return err
	}
	if invite > 0 {
		// Counted only while the link still has room, in the same transaction:
		// two people racing for the last use of a link cannot both get in.
		tag, err := tx.Exec(ctx, `UPDATE chat_invite_links SET usage_count = usage_count + 1
			WHERE id = $1 AND ($2 = false OR (NOT revoked AND (usage_limit = 0 OR usage_count < usage_limit)
			  AND (expires_at IS NULL OR expires_at > now())))`, invite, strict)
		if err != nil {
			return err
		}
		if strict && tag.RowsAffected() == 0 {
			return ErrLinkGone
		}
	}
	return tx.Commit(ctx)
}

// MemberOf is one person's full place in a chat (nil when never a member).
func (s *Store) MemberOf(ctx context.Context, chatID int64, user string) (*Member, error) {
	m := &Member{User: user}
	var rights []byte
	err := s.pool.QueryRow(ctx, `SELECT role, rights, custom_title, joined_at, joined_pts, restricted_until, COALESCE(added_by, '')
		FROM chat_members WHERE chat_id = $1 AND user_id = $2`, chatID, user).
		Scan(&m.Role, &rights, &m.Title, &m.JoinedAt, &m.JoinedPTS, &m.Until, &m.AddedBy)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	readRights(m, rights)
	return m, nil
}

// rights holds an admin's rights, or a restricted member's permissions.
func readRights(m *Member, b []byte) {
	if len(b) == 0 {
		return
	}
	if m.Role == "admin" {
		_ = json.Unmarshal(b, &m.Admin)
	}
	if m.Role == "restricted" {
		var p Perms
		if json.Unmarshal(b, &p) == nil {
			m.Restrict = &p
		}
	}
}

// MemberList is a page of a chat's members (admins first), optionally only
// those whose username starts with or contains q.
func (s *Store) MemberList(ctx context.Context, chatID int64, q string, limit, offset int) ([]*Member, error) {
	if limit <= 0 || limit > 200 {
		limit = 100
	}
	rows, err := s.pool.Query(ctx, `
		SELECT user_id, role, rights, custom_title, joined_at, joined_pts, restricted_until, COALESCE(added_by, '')
		FROM chat_members WHERE chat_id = $1 AND role NOT IN ('left') AND ($2 = '' OR user_id ILIKE '%' || $2 || '%')
		ORDER BY CASE role WHEN 'owner' THEN 0 WHEN 'admin' THEN 1 WHEN 'member' THEN 2 WHEN 'restricted' THEN 3 ELSE 4 END, joined_at
		LIMIT $3 OFFSET $4`, chatID, q, limit, offset)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []*Member
	for rows.Next() {
		m := &Member{}
		var rights []byte
		if err := rows.Scan(&m.User, &m.Role, &rights, &m.Title, &m.JoinedAt, &m.JoinedPTS, &m.Until, &m.AddedBy); err != nil {
			return nil, err
		}
		readRights(m, rights)
		out = append(out, m)
	}
	return out, rows.Err()
}

// SetMember changes a member's role and rights (admin rights, or a
// restricted member's permissions) and, for bans and restrictions, until when.
func (s *Store) SetMember(ctx context.Context, chatID int64, user, role string, rights any, title string, until *time.Time) error {
	b, err := json.Marshal(rights)
	if err != nil {
		return err
	}
	if rights == nil {
		b = []byte("{}")
	}
	t, err := s.pool.Exec(ctx, `UPDATE chat_members SET role = $3, rights = $4, custom_title = $5, restricted_until = $6
		WHERE chat_id = $1 AND user_id = $2`, chatID, user, role, b, title, until)
	if err == nil && t.RowsAffected() == 0 {
		return ErrNoUser
	}
	return err
}

// ChatInfo loads a chat's own data and its member count.
func (s *Store) ChatInfo(ctx context.Context, chatID int64) (*ChatInfo, error) {
	c := &ChatInfo{ID: chatID}
	var set []byte
	err := s.pool.QueryRow(ctx, `
		SELECT type, title, about, is_forum, slow_mode_seconds, settings, pts,
		       (SELECT count(*) FROM chat_members m WHERE m.chat_id = c.id AND m.role NOT IN ('left', 'banned')),
		       COALESCE((SELECT user_id FROM chat_members m WHERE m.chat_id = c.id AND m.role = 'owner' LIMIT 1), '')
		FROM chats c WHERE id = $1`, chatID).Scan(&c.Type, &c.Title, &c.About, &c.Forum, &c.Slow, &set, &c.PTS, &c.Members, &c.Owner)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrNotMember
	}
	c.Settings = parseSettings(set)
	return c, err
}

// UpdateChat writes a group's title, description, topics switch, slow mode
// and settings.
func (s *Store) UpdateChat(ctx context.Context, c *ChatInfo) error {
	b, err := json.Marshal(c.Settings)
	if err != nil {
		return err
	}
	_, err = s.pool.Exec(ctx, `UPDATE chats SET title = $2, about = $3, is_forum = $4, slow_mode_seconds = $5, settings = $6 WHERE id = $1`,
		c.ID, c.Title, c.About, c.Forum, c.Slow, b)
	return err
}

// LastSentAt is when user last wrote in a chat (slow mode).
func (s *Store) LastSentAt(ctx context.Context, chatID int64, user string) (time.Time, bool) {
	var t *time.Time
	_ = s.pool.QueryRow(ctx, `SELECT max(created_at) FROM chat_messages WHERE chat_id = $1 AND sender_id = $2 AND kind <> 'service'`, chatID, user).Scan(&t)
	if t == nil {
		return time.Time{}, false
	}
	return *t, true
}

// Log records an administrative action (the 48-hour "recent actions").
func (s *Store) Log(ctx context.Context, chatID int64, actor, action string, payload any) {
	b, _ := json.Marshal(payload)
	_, _ = s.pool.Exec(ctx, `INSERT INTO chat_admin_log (chat_id, actor_id, action, payload) VALUES ($1, NULLIF($2, ''), $3, $4)`, chatID, actor, action, b)
}

// LogEntry is one recent action.
type LogEntry struct {
	ID      int64           `json:"id"`
	Actor   string          `json:"actor"`
	Action  string          `json:"action"`
	Payload json.RawMessage `json:"d"`
	At      time.Time       `json:"at"`
}

// AdminLog lists the last 48 hours of actions, newest first, optionally of
// one kind or by one admin.
func (s *Store) AdminLog(ctx context.Context, chatID int64, action, actor string) ([]LogEntry, error) {
	rows, err := s.pool.Query(ctx, `
		SELECT id, COALESCE(actor_id, ''), action, payload, created_at FROM chat_admin_log
		WHERE chat_id = $1 AND created_at > now() - interval '48 hours' AND ($2 = '' OR action = $2) AND ($3 = '' OR actor_id = $3)
		ORDER BY id DESC LIMIT 500`, chatID, action, actor)
	if err != nil {
		return nil, err
	}
	return pgx.CollectRows(rows, func(r pgx.CollectableRow) (LogEntry, error) {
		var e LogEntry
		err := r.Scan(&e.ID, &e.Actor, &e.Action, &e.Payload, &e.At)
		return e, err
	})
}

// Readers are the members (other than the sender) who have read up to a
// message: "who read" in a group.
func (s *Store) Readers(ctx context.Context, chatID, pts int64, sender string) ([]string, error) {
	rows, err := s.pool.Query(ctx, `SELECT user_id FROM chat_members WHERE chat_id = $1 AND last_read_pts >= $2 AND user_id <> $3 AND role NOT IN ('left', 'banned') ORDER BY last_read_at DESC NULLS LAST LIMIT 100`, chatID, pts, sender)
	if err != nil {
		return nil, err
	}
	return pgx.CollectRows(rows, pgx.RowTo[string])
}

// Views counts, for a channel's posts, the subscribers who have read past
// each one.
func (s *Store) Views(ctx context.Context, chatID int64, list []*Message) {
	for _, m := range list {
		_ = s.pool.QueryRow(ctx, `SELECT count(*) FROM chat_members WHERE chat_id = $1 AND last_read_pts >= $2 AND role NOT IN ('left', 'banned')`, chatID, m.PTS).Scan(&m.Views)
	}
}
