package chat

import (
	"context"

	"github.com/jackc/pgx/v5"
)

// Reading pages of a chat: the rows a reader may see and what is shown with them.

// History is one page of a chat as viewer sees it, newest first: up to limit
// messages below beforePTS (0 = from the end), without the ones viewer
// deleted for themselves.
func (s *Store) History(ctx context.Context, chatID int64, viewer string, beforePTS int64, limit int) ([]*Message, error) {
	return s.Page(ctx, chatID, viewer, PageQuery{Before: beforePTS, Limit: limit})
}

// PageQuery narrows a page: From hides what came before (a newcomer to a
// supergroup with hidden history); ByTopic keeps one forum topic.
type PageQuery struct {
	Before  int64
	Limit   int
	From    int64
	ByTopic bool
	Topic   int64
}

// Page is History with PageQuery's limits.
func (s *Store) Page(ctx context.Context, chatID int64, viewer string, q PageQuery) ([]*Message, error) {
	limit, beforePTS := q.Limit, q.Before
	if limit <= 0 || limit > 100 {
		limit = 50
	}
	if beforePTS <= 0 {
		beforePTS = 1 << 62
	}
	rows, err := s.pool.Query(ctx, `SELECT `+msgCols+` FROM chat_messages c
		WHERE chat_id = $1 AND pts < $2 AND pts > $5 AND deleted_at IS NULL
		  AND ($6 = false OR COALESCE(topic_id, 0) = $7)
		  AND NOT EXISTS (SELECT 1 FROM chat_hidden h WHERE h.user_id = $4 AND h.message_id = c.id)
		ORDER BY pts DESC LIMIT $3`, chatID, beforePTS, limit, viewer, q.From, q.ByTopic, q.Topic)
	if err != nil {
		return nil, err
	}
	out, err := collectMsgs(rows)
	if err != nil {
		return nil, err
	}
	return out, s.decorate(ctx, chatID, viewer, out)
}

// ByIDs loads messages of one chat by id, oldest first, as viewer sees them.
func (s *Store) ByIDs(ctx context.Context, chatID int64, viewer string, ids []int64) ([]*Message, error) {
	if len(ids) == 0 || len(ids) > 100 {
		return nil, nil
	}
	rows, err := s.pool.Query(ctx, `SELECT `+msgCols+` FROM chat_messages c
		WHERE chat_id = $1 AND id = ANY($2) AND deleted_at IS NULL
		  AND NOT EXISTS (SELECT 1 FROM chat_hidden h WHERE h.user_id = $3 AND h.message_id = c.id)
		ORDER BY pts`, chatID, ids, viewer)
	if err != nil {
		return nil, err
	}
	out, err := collectMsgs(rows)
	if err != nil {
		return nil, err
	}
	return out, s.decorate(ctx, chatID, viewer, out)
}

func collectMsgs(rows pgx.Rows) ([]*Message, error) {
	defer rows.Close()
	var out []*Message
	for rows.Next() {
		m, err := scanMsg(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, m)
	}
	return out, rows.Err()
}

type querier interface {
	Query(context.Context, string, ...any) (pgx.Rows, error)
}

// replyPreviews loads the strip shown above each reply, for messages of chatID
// only: a reply never reveals a message of a chat the reader is not in.
func replyPreviews(ctx context.Context, q querier, chatID int64, ids []int64) map[int64]*ReplyPreview {
	out := map[int64]*ReplyPreview{}
	rows, err := q.Query(ctx, `SELECT id, pts, COALESCE(sender_id, ''), CASE WHEN deleted_at IS NULL THEN left(text, 200) ELSE '' END, deleted_at IS NOT NULL
		FROM chat_messages WHERE chat_id = $1 AND id = ANY($2)`, chatID, ids)
	if err != nil {
		return out
	}
	defer rows.Close()
	for rows.Next() {
		p := &ReplyPreview{}
		if rows.Scan(&p.ID, &p.PTS, &p.From, &p.Text, &p.Deleted) == nil {
			out[p.ID] = p
		}
	}
	return out
}

// decorate fills in what a page of messages shows besides the rows
// themselves: reply strips, reaction counts (and which are the viewer's), pins.
func (s *Store) decorate(ctx context.Context, chatID int64, viewer string, list []*Message) error {
	if len(list) == 0 {
		return nil
	}
	ids := make([]int64, 0, len(list))
	var replies []int64
	byID := map[int64]*Message{}
	for _, m := range list {
		ids = append(ids, m.ID)
		byID[m.ID] = m
		if m.ReplyTo != nil {
			replies = append(replies, *m.ReplyTo)
		}
	}
	var mids []int64
	for _, m := range list {
		if m.MediaID != nil {
			mids = append(mids, *m.MediaID)
		}
	}
	if len(mids) > 0 {
		media, err := s.MediaByIDs(ctx, mids)
		if err != nil {
			return err
		}
		for _, m := range list {
			if m.MediaID != nil {
				m.Media = media[*m.MediaID]
			}
		}
	}
	if len(replies) > 0 {
		pv := replyPreviews(ctx, s.pool, chatID, replies)
		for _, m := range list {
			if m.ReplyTo != nil {
				m.Reply = pv[*m.ReplyTo]
			}
		}
	}
	rows, err := s.pool.Query(ctx, `SELECT message_id, emoji, count(*), bool_or(user_id = $2) FROM chat_reactions
		WHERE message_id = ANY($1) GROUP BY message_id, emoji ORDER BY message_id, min(created_at)`, ids, viewer)
	if err != nil {
		return err
	}
	for rows.Next() {
		var id int64
		var c ReactionCount
		var mine bool
		if err := rows.Scan(&id, &c.Emoji, &c.Count, &mine); err != nil {
			rows.Close()
			return err
		}
		if m := byID[id]; m != nil {
			m.Reactions = append(m.Reactions, c)
			if mine {
				m.Mine = append(m.Mine, c.Emoji)
			}
		}
	}
	rows.Close()
	pins, err := s.pool.Query(ctx, `SELECT message_id FROM chat_pins WHERE chat_id = $1 AND message_id = ANY($2)`, chatID, ids)
	if err != nil {
		return err
	}
	defer pins.Close()
	for pins.Next() {
		var id int64
		if pins.Scan(&id) == nil && byID[id] != nil {
			byID[id].Pinned = true
		}
	}
	return pins.Err()
}

// visibleFrom is the pts a reader's view of a chat starts after (see
// Store.VisibleFrom: a cleared history, or a supergroup hiding its history
// from newcomers); history, sync and search all start there.
func (s *Service) visibleFrom(ctx context.Context, chatID int64, user string) int64 {
	return s.Store.VisibleFrom(ctx, chatID, user)
}
