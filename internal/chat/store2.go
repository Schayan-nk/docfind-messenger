package chat

import (
	"context"
	"encoding/json"
	"errors"
	"time"

	"github.com/jackc/pgx/v5"
)

// Phase 2 changes to existing messages (migration 045). Every one takes the
// chat's next pts and logs an update in the same transaction, like a send, so
// a device that was offline replays it in order.

// bump takes the chat's next pts inside tx.
func bump(ctx context.Context, tx pgx.Tx, chatID int64) (int64, error) {
	var pts int64
	err := tx.QueryRow(ctx, `UPDATE chats SET pts = pts + 1 WHERE id = $1 RETURNING pts`, chatID).Scan(&pts)
	return pts, err
}

// msgOwner returns a message's chat and sender, or ErrNotMember when it does
// not exist (an unknown id must look exactly like a forbidden one).
func (s *Store) msgOwner(ctx context.Context, msgID int64) (chatID int64, sender string, deleted bool, err error) {
	err = s.pool.QueryRow(ctx, `SELECT chat_id, COALESCE(sender_id, ''), deleted_at IS NOT NULL FROM chat_messages WHERE id = $1`, msgID).Scan(&chatID, &sender, &deleted)
	if errors.Is(err, pgx.ErrNoRows) {
		return 0, "", false, ErrNotMember
	}
	return chatID, sender, deleted, err
}

// Edit replaces the text of the sender's own message. No history is kept that
// anyone else can see (the brief: "keep no edit history visible to others").
// The mentions are worked out again: a name added by an edit notifies.
func (s *Store) Edit(ctx context.Context, msgID int64, user, text string, ents []Entity, mentions []string) (*Update, error) {
	chatID, sender, deleted, err := s.msgOwner(ctx, msgID)
	if err != nil {
		return nil, err
	}
	if deleted || sender != user {
		return nil, ErrForbidden
	}
	entJSON, err := json.Marshal(nonNil(ents))
	if err != nil {
		return nil, err
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback(ctx)
	pts, err := bump(ctx, tx, chatID)
	if err != nil {
		return nil, err
	}
	var edited time.Time
	var mpts int64
	if err := tx.QueryRow(ctx, `UPDATE chat_messages SET text = $2, entities = $3, edited_at = now() WHERE id = $1 RETURNING edited_at, pts`, msgID, text, entJSON).Scan(&edited, &mpts); err != nil {
		return nil, err
	}
	if _, err := tx.Exec(ctx, `DELETE FROM chat_mentions WHERE message_id = $1`, msgID); err != nil {
		return nil, err
	}
	for _, u := range mentions {
		if u == user {
			continue
		}
		if _, err := tx.Exec(ctx, `INSERT INTO chat_mentions (message_id, chat_id, user_id, pts) VALUES ($1, $2, $3, $4) ON CONFLICT DO NOTHING`, msgID, chatID, u, mpts); err != nil {
			return nil, err
		}
	}
	up, err := addUpdate(ctx, tx, chatID, pts, "edit", map[string]any{"id": msgID, "text": text, "entities": json.RawMessage(entJSON), "edited": edited})
	if err != nil {
		return nil, err
	}
	return up, tx.Commit(ctx)
}

func nonNil(e []Entity) []Entity {
	if e == nil {
		return []Entity{}
	}
	return e
}

// DeleteForAll removes messages for everybody. may decides, per message,
// whether user is allowed (the sender, or an admin with delete rights).
func (s *Store) DeleteForAll(ctx context.Context, chatID int64, ids []int64, may func(sender string) bool) (*Update, error) {
	if len(ids) == 0 || len(ids) > 100 {
		return nil, ErrForbidden
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback(ctx)
	rows, err := tx.Query(ctx, `SELECT id, COALESCE(sender_id, '') FROM chat_messages WHERE chat_id = $1 AND id = ANY($2) AND deleted_at IS NULL FOR UPDATE`, chatID, ids)
	if err != nil {
		return nil, err
	}
	var gone []int64
	for rows.Next() {
		var id int64
		var sender string
		if err := rows.Scan(&id, &sender); err != nil {
			rows.Close()
			return nil, err
		}
		if !may(sender) {
			rows.Close()
			return nil, ErrForbidden
		}
		gone = append(gone, id)
	}
	rows.Close()
	if len(gone) == 0 {
		return nil, tx.Commit(ctx)
	}
	// The text goes with the row's visibility: a deleted message keeps no
	// readable content in the database either.
	if _, err := tx.Exec(ctx, `UPDATE chat_messages SET deleted_at = now(), text = '', entities = '[]' WHERE id = ANY($1)`, gone); err != nil {
		return nil, err
	}
	if _, err := tx.Exec(ctx, `DELETE FROM chat_pins WHERE message_id = ANY($1)`, gone); err != nil {
		return nil, err
	}
	pts, err := bump(ctx, tx, chatID)
	if err != nil {
		return nil, err
	}
	up, err := addUpdate(ctx, tx, chatID, pts, "delete", map[string]any{"ids": gone})
	if err != nil {
		return nil, err
	}
	return up, tx.Commit(ctx)
}

// HideForMe is "delete for me": the messages vanish from user's view only.
func (s *Store) HideForMe(ctx context.Context, user string, chatID int64, ids []int64) error {
	if len(ids) == 0 || len(ids) > 100 {
		return ErrForbidden
	}
	_, err := s.pool.Exec(ctx, `
		INSERT INTO chat_hidden (user_id, message_id)
		SELECT $1, id FROM chat_messages WHERE chat_id = $2 AND id = ANY($3)
		ON CONFLICT DO NOTHING`, user, chatID, ids)
	return err
}

// Pin pins (on) or unpins one message.
func (s *Store) Pin(ctx context.Context, chatID, msgID int64, user string, on bool) (*Update, error) {
	c, _, deleted, err := s.msgOwner(ctx, msgID)
	if err != nil || c != chatID {
		return nil, ErrNotMember
	}
	if deleted && on {
		return nil, ErrForbidden
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback(ctx)
	var tag pgconnTag
	if on {
		tag, err = execTag(ctx, tx, `INSERT INTO chat_pins (chat_id, message_id, pinned_by) VALUES ($1, $2, $3) ON CONFLICT DO NOTHING`, chatID, msgID, user)
	} else {
		tag, err = execTag(ctx, tx, `DELETE FROM chat_pins WHERE chat_id = $1 AND message_id = $2`, chatID, msgID)
	}
	if err != nil || tag == 0 {
		return nil, err
	}
	pts, err := bump(ctx, tx, chatID)
	if err != nil {
		return nil, err
	}
	kind := "pin"
	if !on {
		kind = "unpin"
	}
	up, err := addUpdate(ctx, tx, chatID, pts, kind, map[string]any{"id": msgID, "by": user})
	if err != nil {
		return nil, err
	}
	return up, tx.Commit(ctx)
}

type pgconnTag = int64

func execTag(ctx context.Context, tx pgx.Tx, q string, args ...any) (int64, error) {
	t, err := tx.Exec(ctx, q, args...)
	return t.RowsAffected(), err
}

// Pins lists a chat's pinned message ids, newest pin first.
func (s *Store) Pins(ctx context.Context, chatID int64) ([]int64, error) {
	rows, err := s.pool.Query(ctx, `SELECT message_id FROM chat_pins WHERE chat_id = $1 ORDER BY pinned_at DESC LIMIT 100`, chatID)
	if err != nil {
		return nil, err
	}
	return pgx.CollectRows(rows, pgx.RowTo[int64])
}

// React adds (on) or removes user's reaction on a message, and logs the
// message's counts after the change. A person has one reaction per message, as
// in Telegram without Premium: a new one replaces the old.
func (s *Store) React(ctx context.Context, chatID, msgID int64, user, emoji string, on bool) (*Update, error) {
	c, _, deleted, err := s.msgOwner(ctx, msgID)
	if err != nil || c != chatID {
		return nil, ErrNotMember
	}
	if deleted {
		return nil, ErrForbidden
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback(ctx)
	var n int64
	if on {
		if _, err := tx.Exec(ctx, `DELETE FROM chat_reactions WHERE message_id = $1 AND user_id = $2 AND emoji <> $3`, msgID, user, emoji); err != nil {
			return nil, err
		}
		n, err = execTag(ctx, tx, `INSERT INTO chat_reactions (message_id, user_id, emoji) VALUES ($1, $2, $3) ON CONFLICT DO NOTHING`, msgID, user, emoji)
	} else {
		n, err = execTag(ctx, tx, `DELETE FROM chat_reactions WHERE message_id = $1 AND user_id = $2 AND emoji = $3`, msgID, user, emoji)
	}
	if err != nil || n == 0 {
		return nil, err
	}
	counts, err := reactionCounts(ctx, tx, msgID)
	if err != nil {
		return nil, err
	}
	pts, err := bump(ctx, tx, chatID)
	if err != nil {
		return nil, err
	}
	up, err := addUpdate(ctx, tx, chatID, pts, "react", map[string]any{"id": msgID, "user": user, "emoji": emoji, "on": on, "counts": counts})
	if err != nil {
		return nil, err
	}
	return up, tx.Commit(ctx)
}

// ReactionCount is one emoji's total on a message.
type ReactionCount struct {
	Emoji string `json:"emoji"`
	Count int    `json:"n"`
}

func reactionCounts(ctx context.Context, q interface {
	Query(context.Context, string, ...any) (pgx.Rows, error)
}, msgID int64) ([]ReactionCount, error) {
	rows, err := q.Query(ctx, `SELECT emoji, count(*) FROM chat_reactions WHERE message_id = $1 GROUP BY emoji ORDER BY min(created_at)`, msgID)
	if err != nil {
		return nil, err
	}
	return pgx.CollectRows(rows, func(r pgx.CollectableRow) (ReactionCount, error) {
		var c ReactionCount
		err := r.Scan(&c.Emoji, &c.Count)
		return c, err
	})
}

// Reactors lists who reacted with what on a message (the "who reacted" sheet).
func (s *Store) Reactors(ctx context.Context, msgID int64) (map[string][]string, error) {
	rows, err := s.pool.Query(ctx, `SELECT emoji, user_id FROM chat_reactions WHERE message_id = $1 ORDER BY created_at`, msgID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[string][]string{}
	for rows.Next() {
		var e, u string
		if err := rows.Scan(&e, &u); err != nil {
			return nil, err
		}
		out[e] = append(out[e], u)
	}
	return out, rows.Err()
}

// hasAttachment says whether a message carries a file or a document, so its
// caption may be emptied by an edit.
func (s *Store) hasAttachment(ctx context.Context, msgID int64) bool {
	var ok bool
	_ = s.pool.QueryRow(ctx, `SELECT media_id IS NOT NULL OR doc_ref IS NOT NULL FROM chat_messages WHERE id = $1`, msgID).Scan(&ok)
	return ok
}

// UsersByHandle resolves @handles (lower case, without the @) to usernames.
func (s *Store) UsersByHandle(ctx context.Context, handles []string) ([]string, error) {
	if len(handles) == 0 {
		return nil, nil
	}
	rows, err := s.pool.Query(ctx, `SELECT user_id FROM chat_users_ext WHERE lower(username) = ANY($1)`, handles)
	if err != nil {
		return nil, err
	}
	return pgx.CollectRows(rows, pgx.RowTo[string])
}

// UnreadMentions lists the pts of the messages in chatID that mention user
// and that user has not read yet, oldest first: "jump to next mention".
func (s *Store) UnreadMentions(ctx context.Context, chatID int64, user string) ([]int64, error) {
	rows, err := s.pool.Query(ctx, `
		SELECT mm.pts FROM chat_mentions mm
		JOIN chat_members m ON m.chat_id = mm.chat_id AND m.user_id = mm.user_id
		JOIN chat_messages x ON x.id = mm.message_id AND x.deleted_at IS NULL
		WHERE mm.chat_id = $1 AND mm.user_id = $2 AND mm.pts > m.last_read_pts
		ORDER BY mm.pts LIMIT 500`, chatID, user)
	if err != nil {
		return nil, err
	}
	return pgx.CollectRows(rows, pgx.RowTo[int64])
}

// MessageText returns a live message's chat, sender and text (for quotes).
func (s *Store) MessageText(ctx context.Context, msgID int64) (chatID int64, sender, text string, err error) {
	err = s.pool.QueryRow(ctx, `SELECT chat_id, COALESCE(sender_id, ''), text FROM chat_messages WHERE id = $1 AND deleted_at IS NULL`, msgID).Scan(&chatID, &sender, &text)
	if errors.Is(err, pgx.ErrNoRows) {
		return 0, "", "", ErrNotMember
	}
	return chatID, sender, text, err
}
