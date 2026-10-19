package chat

import (
	"context"
	"strconv"
	"strings"
	"time"
)

// Search (SPEC 6.6): messages, files (by name and the text read from them)
// and links, across a person's chats or inside one, with Persian
// normalisation (normalize.go). Every query starts from the person's own
// memberships, so a message from a chat they are not in, a history hidden
// from them, a cleared history or a message they deleted for themselves is
// never a candidate (security check: TestSearchNeverLeavesYourChats).

// SearchReq narrows a search.
type SearchReq struct {
	Q      string     // words; each must appear, as a prefix
	Chat   int64      // 0: every chat of the person
	From   string     // a sender
	Kind   string     // "", media, files, voice, links
	Since  *time.Time // sent at or after
	Until  *time.Time // sent before
	Before int64      // message id, for the next page
	Limit  int
}

var searchKinds = map[string]string{
	"media": `c.kind IN ('photo', 'video')`,
	"files": `c.kind IN ('file', 'doc')`,
	"voice": `c.kind IN ('voice', 'round')`,
	"links": `(c.entities @> '[{"type":"url"}]' OR c.entities @> '[{"type":"text_link"}]')`,
}

// Search finds messages user may read, newest first. With no words it needs
// a chat, a sender or a kind (the shared media of a chat, say).
func (s *Store) Search(ctx context.Context, user string, req SearchReq) ([]*Message, error) {
	limit := req.Limit
	if limit <= 0 || limit > 100 {
		limit = 50
	}
	where := []string{
		`m.user_id = $1`, `m.role NOT IN ('left', 'banned')`,
		`c.deleted_at IS NULL`, `c.kind <> 'service'`, `ch.type <> 'secret'`,
		`c.pts > m.cleared_pts`,
		`(NOT COALESCE((ch.settings->>'hidden_history')::boolean, false) OR m.role IN ('owner', 'admin') OR c.pts > m.joined_pts)`,
		`NOT EXISTS (SELECT 1 FROM chat_hidden h WHERE h.user_id = $1 AND h.message_id = c.id)`,
	}
	args := []any{user}
	arg := func(v any) string { args = append(args, v); return "$" + strconv.Itoa(len(args)) }
	if req.Chat > 0 {
		where = append(where, `c.chat_id = `+arg(req.Chat))
	}
	if req.From != "" {
		where = append(where, `c.sender_id = `+arg(req.From))
	}
	if k, ok := searchKinds[req.Kind]; ok {
		where = append(where, k)
	} else if req.Kind != "" {
		return nil, ErrForbidden
	}
	if req.Since != nil {
		where = append(where, `c.created_at >= `+arg(*req.Since))
	}
	if req.Until != nil {
		where = append(where, `c.created_at < `+arg(*req.Until))
	}
	if req.Before > 0 {
		where = append(where, `c.id < `+arg(req.Before))
	}
	base := `FROM chat_messages c JOIN chat_members m ON m.chat_id = c.chat_id JOIN chats ch ON ch.id = c.chat_id WHERE ` + strings.Join(where, " AND ")
	var hits string
	if tq := searchQuery(req.Q); tq != "" {
		q := `to_tsquery('simple', ` + arg(tq) + `)`
		// The message's words, or the words of the file it carries.
		hits = `SELECT c.id ` + base + ` AND c.search @@ ` + q +
			` UNION SELECT c.id ` + base + ` AND EXISTS (SELECT 1 FROM chat_media md WHERE md.id = c.media_id AND md.search @@ ` + q + `)`
	} else if req.Chat > 0 || req.From != "" || req.Kind != "" {
		hits = `SELECT c.id ` + base
	} else {
		return nil, nil
	}
	rows, err := s.pool.Query(ctx, `SELECT `+msgCols+` FROM chat_messages c
		WHERE c.id IN (SELECT id FROM (`+hits+`) h ORDER BY id DESC LIMIT `+arg(limit)+`)
		ORDER BY c.id DESC`, args...)
	if err != nil {
		return nil, err
	}
	out, err := collectMsgs(rows)
	if err != nil {
		return nil, err
	}
	// Replies, reactions, media and pins are dressed per chat.
	byChat := map[int64][]*Message{}
	for _, m := range out {
		byChat[m.ChatID] = append(byChat[m.ChatID], m)
	}
	for chatID, list := range byChat {
		if err := s.decorate(ctx, chatID, user, list); err != nil {
			return nil, err
		}
	}
	return out, nil
}

// Search is Store.Search with the people's names and the document cards the
// reader may see, and a limit of how often one person searches.
func (s *Service) Search(ctx context.Context, user string, req SearchReq) ([]*Message, error) {
	if !s.searchLimit.allow(user) {
		return nil, ErrRate
	}
	if req.Chat > 0 {
		if _, err := s.Store.Role(ctx, req.Chat, user); err != nil {
			return nil, err
		}
	}
	msgs, err := s.Store.Search(ctx, user, req)
	if err != nil {
		return nil, err
	}
	names := map[string]string{}
	for _, m := range msgs {
		if m.Sender == "" {
			continue
		}
		if _, ok := names[m.Sender]; !ok {
			names[m.Sender] = s.displayName(m.Sender)
		}
		m.FromName = names[m.Sender]
	}
	s.dressDocs(user, msgs)
	return msgs, nil
}
