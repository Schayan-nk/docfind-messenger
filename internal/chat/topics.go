package chat

import (
	"context"
	"errors"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
)

// Forum topics (SPEC 6.2): a supergroup with topics has one thread per topic,
// each with its own unread count. Topic 0 is "General" and has no row.

// Topic is one thread of a forum.
type Topic struct {
	ID      int64      `json:"id"`
	Title   string     `json:"title"`
	Icon    string     `json:"icon,omitempty"`
	Color   int        `json:"color,omitempty"`
	Closed  bool       `json:"closed,omitempty"`
	Pinned  bool       `json:"pinned,omitempty"`
	By      string     `json:"by,omitempty"`
	Unread  int        `json:"unread"`
	LastAt  *time.Time `json:"last_at,omitempty"`
	LastPTS int64      `json:"last_pts,omitempty"`
}

// Topics lists a forum's topics as user sees them, General first, then
// pinned, then by last activity.
func (s *Store) Topics(ctx context.Context, chatID int64, user string) ([]Topic, error) {
	rows, err := s.pool.Query(ctx, `
		WITH t AS (
			SELECT 0::bigint AS id, 'عمومی' AS title, '' AS icon, 0 AS color, false AS closed, true AS pinned, '' AS by_user
			UNION ALL
			SELECT id, title, icon, color, closed, pinned, COALESCE(created_by, '') FROM chat_topics WHERE chat_id = $1
		)
		SELECT t.id, t.title, t.icon, t.color, t.closed, t.pinned, t.by_user,
		       (SELECT count(*) FROM chat_messages u
		          WHERE u.chat_id = $1 AND COALESCE(u.topic_id, 0) = t.id AND u.deleted_at IS NULL AND u.sender_id IS DISTINCT FROM $2
		            AND u.pts > GREATEST(m.last_read_pts, COALESCE((SELECT r.last_read_pts FROM chat_topic_reads r WHERE r.chat_id = $1 AND r.topic_id = t.id AND r.user_id = $2), 0))),
		       (SELECT max(created_at) FROM chat_messages u WHERE u.chat_id = $1 AND COALESCE(u.topic_id, 0) = t.id),
		       COALESCE((SELECT max(pts) FROM chat_messages u WHERE u.chat_id = $1 AND COALESCE(u.topic_id, 0) = t.id), 0)
		FROM t, chat_members m WHERE m.chat_id = $1 AND m.user_id = $2
		ORDER BY (t.id = 0) DESC, t.pinned DESC, 9 DESC NULLS LAST LIMIT 500`, chatID, user)
	if err != nil {
		return nil, err
	}
	return pgx.CollectRows(rows, func(r pgx.CollectableRow) (Topic, error) {
		var t Topic
		err := r.Scan(&t.ID, &t.Title, &t.Icon, &t.Color, &t.Closed, &t.Pinned, &t.By, &t.Unread, &t.LastAt, &t.LastPTS)
		return t, err
	})
}

// TopicOf loads one topic of a chat (ErrNotMember when it is not there).
func (s *Store) TopicOf(ctx context.Context, chatID, id int64) (*Topic, error) {
	if id == 0 {
		return &Topic{ID: 0, Title: "عمومی"}, nil
	}
	t := &Topic{}
	err := s.pool.QueryRow(ctx, `SELECT id, title, icon, color, closed, pinned, COALESCE(created_by, '') FROM chat_topics WHERE chat_id = $1 AND id = $2`, chatID, id).
		Scan(&t.ID, &t.Title, &t.Icon, &t.Color, &t.Closed, &t.Pinned, &t.By)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrNotMember
	}
	return t, err
}

// MarkTopicRead moves user's position in one topic forward.
func (s *Store) MarkTopicRead(ctx context.Context, chatID, topic int64, user string, pts int64) error {
	_, err := s.pool.Exec(ctx, `INSERT INTO chat_topic_reads (chat_id, topic_id, user_id, last_read_pts) VALUES ($1, $2, $3, $4)
		ON CONFLICT (chat_id, topic_id, user_id) DO UPDATE SET last_read_pts = GREATEST(chat_topic_reads.last_read_pts, EXCLUDED.last_read_pts)`, chatID, topic, user, pts)
	return err
}

// TopicReq makes or changes a topic; absent fields stay.
type TopicReq struct {
	Title  *string `json:"title"`
	Icon   *string `json:"icon"`
	Color  *int    `json:"color"`
	Closed *bool   `json:"closed"`
	Pinned *bool   `json:"pinned"`
}

func topicTitle(p *string) (string, error) {
	if p == nil {
		return "", ErrEmpty
	}
	t := strings.TrimSpace(CleanText(*p))
	if t == "" || UTF16Len(t) > 128 {
		return "", ErrEmpty
	}
	return t, nil
}

// CreateTopic adds a topic to a forum.
func (s *Service) CreateTopic(ctx context.Context, actor string, chatID int64, req TopicReq) (*Topic, error) {
	info, me, err := s.groupCtx(ctx, chatID, actor)
	if err != nil {
		return nil, err
	}
	if !info.Forum || !can(info.Type, info.Settings, me, ActManageTopics, s.now()) {
		return nil, ErrForbidden
	}
	title, err := topicTitle(req.Title)
	if err != nil {
		return nil, err
	}
	icon, color := "", 0
	// No angle brackets: a topic icon is shared with every member of the chat,
	// and the client may render a "<"-prefixed icon as raw SVG, so markup here
	// would be stored XSS against the whole group. See folders.SaveFolder.
	if req.Icon != nil && len([]rune(*req.Icon)) <= 4 && !strings.ContainsAny(*req.Icon, "<>") {
		icon = *req.Icon
	}
	if req.Color != nil {
		color = *req.Color & 0xFFFFFF
	}
	t := &Topic{Title: title, Icon: icon, Color: color, By: actor}
	if err := s.Store.pool.QueryRow(ctx, `INSERT INTO chat_topics (chat_id, title, icon, color, created_by) VALUES ($1, $2, $3, $4, $5) RETURNING id`,
		chatID, title, icon, color, actor).Scan(&t.ID); err != nil {
		return nil, err
	}
	s.Store.Log(ctx, chatID, actor, "topic", map[string]any{"id": t.ID, "title": title})
	s.service(ctx, chatID, actor, t.ID, map[string]any{"action": "topic", "title": title, "icon": icon})
	members, _ := s.Store.Members(ctx, chatID)
	s.deliver(members, "topic", map[string]any{"chat": chatID, "topic": t}, nil)
	return t, nil
}

// EditTopic renames, closes, reopens or pins a topic. Its creator may change
// its title and icon; closing and pinning take the "manage topics" right.
func (s *Service) EditTopic(ctx context.Context, actor string, chatID, id int64, req TopicReq) (*Topic, error) {
	info, me, err := s.groupCtx(ctx, chatID, actor)
	if err != nil {
		return nil, err
	}
	t, err := s.Store.TopicOf(ctx, chatID, id)
	if err != nil || id == 0 {
		return nil, ErrForbidden
	}
	manage := can(info.Type, info.Settings, me, ActManageTopics, s.now()) && (me.Role == "owner" || me.Role == "admin")
	if (req.Closed != nil || req.Pinned != nil) && !manage {
		return nil, ErrForbidden
	}
	if (req.Title != nil || req.Icon != nil || req.Color != nil) && !manage && t.By != actor {
		return nil, ErrForbidden
	}
	if req.Title != nil {
		if t.Title, err = topicTitle(req.Title); err != nil {
			return nil, err
		}
	}
	if req.Icon != nil && len([]rune(*req.Icon)) <= 4 {
		t.Icon = *req.Icon
	}
	if req.Color != nil {
		t.Color = *req.Color & 0xFFFFFF
	}
	if req.Closed != nil {
		t.Closed = *req.Closed
	}
	if req.Pinned != nil {
		t.Pinned = *req.Pinned
	}
	if _, err := s.Store.pool.Exec(ctx, `UPDATE chat_topics SET title = $3, icon = $4, color = $5, closed = $6, pinned = $7 WHERE chat_id = $1 AND id = $2`,
		chatID, id, t.Title, t.Icon, t.Color, t.Closed, t.Pinned); err != nil {
		return nil, err
	}
	s.Store.Log(ctx, chatID, actor, "topic_edit", map[string]any{"id": id, "req": req})
	members, _ := s.Store.Members(ctx, chatID)
	s.deliver(members, "topic", map[string]any{"chat": chatID, "topic": t}, nil)
	return t, nil
}
