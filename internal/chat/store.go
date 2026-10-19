package chat

import (
	"context"
	"encoding/json"
	"errors"
	"sort"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
)

// Store is the chat data in PostgreSQL (migration 044_chat_core.sql).
type Store struct {
	pool *pgxpool.Pool
}

// NewStore wraps the server's pool.
func NewStore(pool *pgxpool.Pool) *Store { return &Store{pool: pool} }

// Ping is the -check-chat probe for the chat tables.
func (s *Store) Ping(ctx context.Context) error {
	var n int
	return s.pool.QueryRow(ctx, `SELECT count(*) FROM chats WHERE false`).Scan(&n)
}

// EnsureUser makes the profile row a person needs before anything refers to it.
func (s *Store) EnsureUser(ctx context.Context, user string) error {
	_, err := s.pool.Exec(ctx, `INSERT INTO chat_users_ext (user_id) VALUES ($1) ON CONFLICT (user_id) DO NOTHING`, user)
	return err
}

func pairKey(a, b string) string {
	if a == b {
		return "saved|" + a
	}
	p := []string{a, b}
	sort.Strings(p)
	return p[0] + "|" + p[1]
}

// OpenPrivate returns the private chat between a and b, making it on first
// use. a == b is the person's Saved Messages.
func (s *Store) OpenPrivate(ctx context.Context, a, b string) (int64, error) {
	typ := "private"
	if a == b {
		typ = "saved"
	}
	key := pairKey(a, b)
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return 0, err
	}
	defer tx.Rollback(ctx)
	var id int64
	err = tx.QueryRow(ctx, `
		INSERT INTO chats (type, created_by, pair_key) VALUES ($1, $2, $3)
		ON CONFLICT (pair_key) DO UPDATE SET pair_key = EXCLUDED.pair_key
		RETURNING id`, typ, a, key).Scan(&id)
	if err != nil {
		return 0, err
	}
	for _, u := range uniq(a, b) {
		if _, err := tx.Exec(ctx, `INSERT INTO chat_users_ext (user_id) VALUES ($1) ON CONFLICT DO NOTHING`, u); err != nil {
			return 0, err
		}
		if _, err := tx.Exec(ctx, `
			INSERT INTO chat_members (chat_id, user_id, role) VALUES ($1, $2, 'member')
			ON CONFLICT (chat_id, user_id) DO NOTHING`, id, u); err != nil {
			return 0, err
		}
	}
	return id, tx.Commit(ctx)
}

func uniq(a, b string) []string {
	if a == b {
		return []string{a}
	}
	return []string{a, b}
}

// Role is user's role in chatID, or ErrNotMember. "left" and "banned" count
// as not a member for every read; callers that need the distinction get it.
func (s *Store) Role(ctx context.Context, chatID int64, user string) (string, error) {
	var role string
	err := s.pool.QueryRow(ctx, `SELECT role FROM chat_members WHERE chat_id = $1 AND user_id = $2`, chatID, user).Scan(&role)
	if errors.Is(err, pgx.ErrNoRows) || role == "left" || role == "banned" {
		return role, ErrNotMember
	}
	return role, err
}

// Members lists the people who receive a chat's events.
func (s *Store) Members(ctx context.Context, chatID int64) ([]string, error) {
	rows, err := s.pool.Query(ctx, `SELECT user_id FROM chat_members WHERE chat_id = $1 AND role NOT IN ('left', 'banned')`, chatID)
	if err != nil {
		return nil, err
	}
	return pgx.CollectRows(rows, pgx.RowTo[string])
}

// ChatType returns the chat's type and, for a private chat, the other member.
func (s *Store) ChatType(ctx context.Context, chatID int64, me string) (typ, peer string, err error) {
	err = s.pool.QueryRow(ctx, `
		SELECT c.type, COALESCE((SELECT user_id FROM chat_members m WHERE m.chat_id = c.id AND m.user_id <> $2 LIMIT 1), '')
		FROM chats c WHERE c.id = $1`, chatID, me).Scan(&typ, &peer)
	return typ, peer, err
}

// Blocked reports whether either person has blocked the other.
func (s *Store) Blocked(ctx context.Context, a, b string) (bool, error) {
	var n int
	err := s.pool.QueryRow(ctx, `
		SELECT count(*) FROM chat_blocks
		WHERE (user_id = $1 AND blocked_user_id = $2) OR (user_id = $2 AND blocked_user_id = $1)`, a, b).Scan(&n)
	return n > 0, err
}

// SetBlock blocks or unblocks.
func (s *Store) SetBlock(ctx context.Context, user, target string, on bool) error {
	var err error
	if on {
		_, err = s.pool.Exec(ctx, `INSERT INTO chat_blocks (user_id, blocked_user_id) VALUES ($1, $2) ON CONFLICT DO NOTHING`, user, target)
	} else {
		_, err = s.pool.Exec(ctx, `DELETE FROM chat_blocks WHERE user_id = $1 AND blocked_user_id = $2`, user, target)
	}
	return err
}

// Outgoing is what a send carries besides its chat, sender and client id.
type Outgoing struct {
	Text     string
	Entities []Entity
	ReplyTo  int64           // a message of this chat (or of another, when Quote is its snapshot)
	Quote    json.RawMessage // the quoted part of the replied message, or a cross-chat snapshot
	Fwd      json.RawMessage // where a forwarded message came from
	Silent   bool
	Mention  []string // members this message mentions, already resolved

	Kind    string // "text" when empty; photo, video, round, voice, file, doc, service
	MediaID int64
	Album   string // the client's id for messages sent together
	DocRef  string
	Topic   int64           // forum topic; 0 is General
	Service json.RawMessage // a service message's payload
	// FromName is the sender's display name as of sending, so a group's
	// members see who wrote without looking each sender up.
	FromName string
	// Extra is what a poll, location, sticker or GIF message carries (phase 11).
	Extra json.RawMessage
}

// Send stores one text message and its update, in one transaction. A resend
// with the same client id returns the row already made, with created=false,
// so a retry after a lost answer never duplicates a message.
func (s *Store) Send(ctx context.Context, chatID int64, sender, clientID string, o Outgoing) (*Message, *Update, bool, error) {
	if m, err := s.byClientID(ctx, chatID, sender, clientID); err == nil {
		return m, nil, false, nil
	}
	entJSON, err := json.Marshal(nonNil(o.Entities))
	if err != nil {
		return nil, nil, false, err
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return nil, nil, false, err
	}
	defer tx.Rollback(ctx)
	var pts int64
	if err := tx.QueryRow(ctx, `UPDATE chats SET pts = pts + 1, last_message_at = now() WHERE id = $1 RETURNING pts`, chatID).Scan(&pts); err != nil {
		return nil, nil, false, err
	}
	kind := o.Kind
	if kind == "" {
		kind = "text"
	}
	m := &Message{ChatID: chatID, PTS: pts, Sender: sender, ClientID: clientID, Kind: kind, Text: o.Text, Silent: o.Silent,
		Entities: entJSON, Quote: o.Quote, Fwd: o.Fwd, Album: o.Album, DocRef: o.DocRef, Topic: o.Topic, Service: o.Service, Extra: o.Extra}
	if o.MediaID > 0 {
		mid := o.MediaID
		m.MediaID = &mid
	}
	if o.ReplyTo > 0 {
		r := o.ReplyTo
		m.ReplyTo = &r
	}
	err = tx.QueryRow(ctx, `
		INSERT INTO chat_messages (chat_id, pts, sender_id, client_msg_id, kind, text, entities, reply_to_id, quote, fwd_from, silent, media_id, album_cid, doc_ref, topic_id, service, extra)
		VALUES ($1, $2, $3, $4, $11, $5, $6, $7, $8, $9, $10, $12, NULLIF($13, '')::uuid, NULLIF($14, ''), NULLIF($15, 0), $16, $17) RETURNING id, created_at`,
		chatID, pts, sender, clientID, o.Text, entJSON, m.ReplyTo, nullJSON(o.Quote), nullJSON(o.Fwd), o.Silent, kind, m.MediaID, o.Album, o.DocRef, o.Topic, nullJSON(o.Service), nullJSON(o.Extra)).Scan(&m.ID, &m.Created)
	if err != nil {
		var pe *pgconn.PgError
		if errors.As(err, &pe) && pe.Code == "23505" { // a concurrent resend won the race
			tx.Rollback(ctx)
			m2, err2 := s.byClientID(ctx, chatID, sender, clientID)
			return m2, nil, false, err2
		}
		return nil, nil, false, err
	}
	if m.ReplyTo != nil {
		m.Reply = replyPreviews(ctx, tx, chatID, []int64{*m.ReplyTo})[*m.ReplyTo]
	}
	for _, u := range o.Mention {
		if u == sender {
			continue
		}
		if _, err := tx.Exec(ctx, `INSERT INTO chat_mentions (message_id, chat_id, user_id, pts) VALUES ($1, $2, $3, $4) ON CONFLICT DO NOTHING`, m.ID, chatID, u, pts); err != nil {
			return nil, nil, false, err
		}
	}
	m.FromName = o.FromName
	// The file's details travel with the message: the logged update is what
	// every other device draws (live, and on catching up after a reconnect),
	// and a photo or video without them drew nothing at all (6.7.1 and
	// before: the update was logged before the file was looked up).
	if m.MediaID != nil {
		if md, err := scanMedia(tx.QueryRow(ctx, `SELECT `+mediaCols+` FROM chat_media WHERE id = $1`, *m.MediaID)); err == nil {
			m.Media = md
		}
	}
	up, err := addUpdate(ctx, tx, chatID, pts, "message", m)
	if err != nil {
		return nil, nil, false, err
	}
	// An archived chat that is not muted comes back to the list on a new message.
	if _, err := tx.Exec(ctx, `UPDATE chat_members SET archived = false, pinned_order = NULL WHERE chat_id = $1 AND archived
		AND user_id IS DISTINCT FROM $2 AND (muted_until IS NULL OR muted_until <= now())`, chatID, sender); err != nil {
		return nil, nil, false, err
	}
	// The sender has read everything up to their own message.
	if _, err := tx.Exec(ctx, `UPDATE chat_members SET last_read_pts = GREATEST(last_read_pts, $3), last_read_at = now() WHERE chat_id = $1 AND user_id = $2`, chatID, sender, pts); err != nil {
		return nil, nil, false, err
	}
	return m, up, true, tx.Commit(ctx)
}

// nullJSON stores an absent JSON value as SQL NULL.
func nullJSON(b json.RawMessage) any {
	if len(b) == 0 {
		return nil
	}
	return string(b)
}

func addUpdate(ctx context.Context, tx pgx.Tx, chatID, pts int64, kind string, payload any) (*Update, error) {
	b, err := json.Marshal(payload)
	if err != nil {
		return nil, err
	}
	u := &Update{ChatID: chatID, PTS: pts, Kind: kind, Payload: b}
	err = tx.QueryRow(ctx, `INSERT INTO chat_updates (chat_id, pts, kind, payload) VALUES ($1, $2, $3, $4) RETURNING created_at`, chatID, pts, kind, b).Scan(&u.At)
	return u, err
}

const msgCols = `id, chat_id, pts, COALESCE(sender_id, ''), COALESCE(client_msg_id::text, ''), kind, CASE WHEN deleted_at IS NULL THEN text ELSE '' END, entities, reply_to_id, edited_at, deleted_at IS NOT NULL, silent, created_at, quote, fwd_from, media_id, COALESCE(album_cid::text, ''), preview, COALESCE(doc_ref, ''), COALESCE(topic_id, 0), service, CASE WHEN deleted_at IS NULL THEN extra END`

func scanMsg(r pgx.Row) (*Message, error) {
	m := &Message{}
	var ent, quote, fwd, prev, svc, ext []byte
	err := r.Scan(&m.ID, &m.ChatID, &m.PTS, &m.Sender, &m.ClientID, &m.Kind, &m.Text, &ent, &m.ReplyTo, &m.Edited, &m.Deleted, &m.Silent, &m.Created, &quote, &fwd,
		&m.MediaID, &m.Album, &prev, &m.DocRef, &m.Topic, &svc, &ext)
	if len(ext) > 0 {
		m.Extra = ext
	}
	if len(svc) > 0 {
		m.Service = svc
	}
	if len(prev) > 0 {
		m.Preview = prev
	}
	m.Entities = ent
	if len(quote) > 0 {
		m.Quote = quote
	}
	if len(fwd) > 0 {
		m.Fwd = fwd
	}
	return m, err
}

func (s *Store) byClientID(ctx context.Context, chatID int64, sender, clientID string) (*Message, error) {
	return scanMsg(s.pool.QueryRow(ctx, `SELECT `+msgCols+` FROM chat_messages WHERE chat_id = $1 AND sender_id = $2 AND client_msg_id = $3`, chatID, sender, clientID))
}

// Difference returns a chat's updates after afterPTS, oldest first, at most
// limit of them; more=true means the caller should reload history instead.
func (s *Store) Difference(ctx context.Context, chatID, afterPTS int64, limit int) ([]*Update, bool, error) {
	rows, err := s.pool.Query(ctx, `SELECT chat_id, pts, kind, payload, created_at FROM chat_updates WHERE chat_id = $1 AND pts > $2 ORDER BY pts LIMIT $3`, chatID, afterPTS, limit+1)
	if err != nil {
		return nil, false, err
	}
	defer rows.Close()
	var out []*Update
	for rows.Next() {
		u := &Update{}
		if err := rows.Scan(&u.ChatID, &u.PTS, &u.Kind, &u.Payload, &u.At); err != nil {
			return nil, false, err
		}
		out = append(out, u)
	}
	if len(out) > limit {
		return out[:limit], true, rows.Err()
	}
	return out, false, rows.Err()
}

// MarkRead moves user's read position forward (never back). It logs an update
// only when the position moved, so other devices and the other side's ticks
// follow.
func (s *Store) MarkRead(ctx context.Context, chatID int64, user string, pts int64) (*Update, error) {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback(ctx)
	var before int64
	if err := tx.QueryRow(ctx, `UPDATE chat_members SET marked_unread = false, reactions_seen_at = now()
		WHERE chat_id = $1 AND user_id = $2 RETURNING last_read_pts`, chatID, user).Scan(&before); err != nil {
		return nil, err
	}
	var top int64
	if err := tx.QueryRow(ctx, `SELECT pts FROM chats WHERE id = $1`, chatID).Scan(&top); err != nil {
		return nil, err
	}
	if pts > top {
		pts = top
	}
	if pts <= before {
		return nil, tx.Commit(ctx)
	}
	if _, err := tx.Exec(ctx, `UPDATE chat_members SET last_read_pts = $3, last_read_at = now() WHERE chat_id = $1 AND user_id = $2`, chatID, user, pts); err != nil {
		return nil, err
	}
	var upts int64
	if err := tx.QueryRow(ctx, `UPDATE chats SET pts = pts + 1 WHERE id = $1 RETURNING pts`, chatID).Scan(&upts); err != nil {
		return nil, err
	}
	up, err := addUpdate(ctx, tx, chatID, upts, "read", map[string]any{"user": user, "pts": pts})
	if err != nil {
		return nil, err
	}
	return up, tx.Commit(ctx)
}

// Chats is a person's chat list, most recent activity first.
func (s *Store) Chats(ctx context.Context, user string) ([]*Chat, error) {
	rows, err := s.pool.Query(ctx, `
		SELECT c.id, c.type, c.title, c.pts, m.last_read_pts, m.role, c.is_forum,
		       COALESCE(m.pinned_order, 0), m.archived, m.muted_until, m.mentions_only, m.marked_unread, m.sound,
		       (SELECT count(*) FROM chat_reactions r JOIN chat_messages x ON x.id = r.message_id
		            WHERE x.chat_id = c.id AND x.sender_id = $1 AND x.deleted_at IS NULL AND r.user_id <> $1
		              AND r.created_at > COALESCE(m.reactions_seen_at, m.joined_at)),
		       EXISTS (SELECT 1 FROM chat_contacts k WHERE k.user_id = $1 AND k.contact_user_id = peer.user_id),
		       COALESCE(peer.user_id, ''), COALESCE(peer.last_read_pts, 0),
		       (SELECT count(*) FROM chat_messages u WHERE u.chat_id = c.id AND u.pts > m.last_read_pts
		            AND u.deleted_at IS NULL AND u.sender_id IS DISTINCT FROM $1
		            AND NOT EXISTS (SELECT 1 FROM chat_hidden h WHERE h.user_id = $1 AND h.message_id = u.id)
		            AND (NOT c.is_forum OR u.pts > COALESCE((SELECT r.last_read_pts FROM chat_topic_reads r
		                 WHERE r.chat_id = c.id AND r.topic_id = COALESCE(u.topic_id, 0) AND r.user_id = $1), 0))),
		       (SELECT count(*) FROM chat_mentions mm JOIN chat_messages mx ON mx.id = mm.message_id AND mx.deleted_at IS NULL
		            WHERE mm.user_id = $1 AND mm.chat_id = c.id AND mm.pts > m.last_read_pts),
		       COALESCE(d.text, ''),
		       lm.id, lm.pts, COALESCE(lm.sender_id, ''), lm.kind, lm.text, lm.created_at, lm.service
		FROM chat_members m
		JOIN chats c ON c.id = m.chat_id
		-- The other side of a private chat; in a group, how far its members
		-- have read (a message is "read" once anyone has read it).
		LEFT JOIN LATERAL (SELECT CASE WHEN c.type IN ('private', 'secret') THEN min(p.user_id) END AS user_id, max(p.last_read_pts) AS last_read_pts
		    FROM chat_members p WHERE p.chat_id = c.id AND p.user_id <> $1 AND p.role NOT IN ('left', 'banned')) peer ON true
		LEFT JOIN LATERAL (SELECT id, pts, sender_id, kind, text, created_at, service FROM chat_messages x WHERE x.chat_id = c.id AND x.deleted_at IS NULL
		    AND x.pts > m.cleared_pts
		    AND NOT EXISTS (SELECT 1 FROM chat_hidden h WHERE h.user_id = $1 AND h.message_id = x.id) ORDER BY x.pts DESC LIMIT 1) lm ON true
		LEFT JOIN chat_drafts d ON d.user_id = $1 AND d.chat_id = c.id AND d.topic_id = 0
		WHERE m.user_id = $1 AND m.role NOT IN ('left', 'banned')
		  -- a private chat whose history I cleared leaves the list until a new message
		  AND NOT (c.type = 'private' AND m.cleared_pts > 0 AND lm.id IS NULL)
		ORDER BY COALESCE(c.last_message_at, c.created_at) DESC
		LIMIT 500`, user)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []*Chat
	for rows.Next() {
		c := &Chat{}
		var peer string
		var lmID, lmPTS *int64
		var lmSender, lmKind, lmText *string
		var lmAt *time.Time
		var lmService []byte
		if err := rows.Scan(&c.ID, &c.Type, &c.Title, &c.PTS, &c.ReadPTS, &c.Role, &c.Forum,
			&c.Pinned, &c.Archived, &c.MutedUntil, &c.MentionsOnly, &c.MarkedUnread, &c.Sound, &c.Reactions, &c.Contact,
			&peer, &c.PeerReadPTS, &c.Unread, &c.Mentions, &c.Draft,
			&lmID, &lmPTS, &lmSender, &lmKind, &lmText, &lmAt, &lmService); err != nil {
			return nil, err
		}
		if peer != "" {
			c.Peer = &Person{ID: peer}
		}
		if lmID != nil {
			c.Last = &Message{ID: *lmID, ChatID: c.ID, PTS: *lmPTS, Sender: *lmSender, Kind: *lmKind, Text: preview(*lmText), Created: *lmAt, Service: lmService}
		}
		out = append(out, c)
	}
	return out, rows.Err()
}

func preview(s string) string {
	s = strings.Join(strings.Fields(s), " ")
	if r := []rune(s); len(r) > 120 {
		return string(r[:120]) + "…"
	}
	return s
}

// SaveDraft keeps (or clears, on empty text) a person's unsent text.
func (s *Store) SaveDraft(ctx context.Context, user string, chatID int64, text string) error {
	if strings.TrimSpace(text) == "" {
		_, err := s.pool.Exec(ctx, `DELETE FROM chat_drafts WHERE user_id = $1 AND chat_id = $2 AND topic_id = 0`, user, chatID)
		return err
	}
	_, err := s.pool.Exec(ctx, `
		INSERT INTO chat_drafts (user_id, chat_id, topic_id, text) VALUES ($1, $2, 0, $3)
		ON CONFLICT (user_id, chat_id, topic_id) DO UPDATE SET text = EXCLUDED.text, updated_at = now()`, user, chatID, text)
	return err
}

// Peers is everyone who shares a chat with user: who hears about user's
// presence changes.
func (s *Store) Peers(ctx context.Context, user string) ([]string, error) {
	rows, err := s.pool.Query(ctx, `
		SELECT DISTINCT o.user_id FROM chat_members me
		JOIN chat_members o ON o.chat_id = me.chat_id AND o.user_id <> me.user_id AND o.role NOT IN ('left', 'banned')
		WHERE me.user_id = $1 AND me.role NOT IN ('left', 'banned')`, user)
	if err != nil {
		return nil, err
	}
	return pgx.CollectRows(rows, pgx.RowTo[string])
}

// Profile is the stored part of a person: handle, last seen, privacy.
type Profile struct {
	Handle   string
	LastSeen *time.Time
	Privacy  Privacy
}

// Profiles loads the stored profile of each user (missing users get zero values).
func (s *Store) Profiles(ctx context.Context, users []string) (map[string]Profile, error) {
	out := map[string]Profile{}
	if len(users) == 0 {
		return out, nil
	}
	rows, err := s.pool.Query(ctx, `SELECT user_id, COALESCE(username, ''), last_seen_at, privacy FROM chat_users_ext WHERE user_id = ANY($1)`, users)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var id string
		var p Profile
		var priv []byte
		if err := rows.Scan(&id, &p.Handle, &p.LastSeen, &priv); err != nil {
			return nil, err
		}
		_ = json.Unmarshal(priv, &p.Privacy)
		out[id] = p
	}
	return out, rows.Err()
}

// TouchLastSeen records when user was last connected.
func (s *Store) TouchLastSeen(ctx context.Context, user string, at time.Time) error {
	_, err := s.pool.Exec(ctx, `
		INSERT INTO chat_users_ext (user_id, last_seen_at) VALUES ($1, $2)
		ON CONFLICT (user_id) DO UPDATE SET last_seen_at = EXCLUDED.last_seen_at`, user, at)
	return err
}

// SetPrivacy stores a person's privacy settings.
func (s *Store) SetPrivacy(ctx context.Context, user string, p Privacy) error {
	b, err := json.Marshal(p)
	if err != nil {
		return err
	}
	_, err = s.pool.Exec(ctx, `
		INSERT INTO chat_users_ext (user_id, privacy) VALUES ($1, $2)
		ON CONFLICT (user_id) DO UPDATE SET privacy = EXCLUDED.privacy`, user, b)
	return err
}

// IsContact reports whether owner has target in their contacts.
func (s *Store) IsContact(ctx context.Context, owner, target string) (bool, error) {
	var n int
	err := s.pool.QueryRow(ctx, `SELECT count(*) FROM chat_contacts WHERE user_id = $1 AND contact_user_id = $2`, owner, target).Scan(&n)
	return n > 0, err
}

// SharesChat reports whether a and b are both active members of some chat --
// the "colleague you share a chat with" relationship that gates a contact
// card's personal details.
func (s *Store) SharesChat(ctx context.Context, a, b string) (bool, error) {
	if strings.EqualFold(a, b) || a == "" || b == "" {
		return false, nil
	}
	var ok bool
	err := s.pool.QueryRow(ctx, `
		SELECT EXISTS (
			SELECT 1 FROM chat_members x
			JOIN chat_members y ON y.chat_id = x.chat_id
			WHERE x.user_id = $1 AND y.user_id = $2
			  AND x.role NOT IN ('left', 'banned') AND y.role NOT IN ('left', 'banned'))`, a, b).Scan(&ok)
	return ok, err
}
