package chat

import (
	"context"
	"errors"
	"time"

	"github.com/jackc/pgx/v5"
)

// A person's own handling of each chat in their list (SPEC 6.5): pinned,
// archived, muted (until a time, or for good), notified only for mentions,
// marked unread; and clearing a chat's history. Nobody else sees any of it.

var ErrPinLimit = errors.New("chat: too many pinned chats")

// Pin limits: Telegram's five in the main list; more in the archive.
const (
	maxPinnedMain    = 5
	maxPinnedArchive = 20
)

// MuteForever is how "muted for good" is kept: a time no one will reach.
var MuteForever = time.Date(9999, 12, 31, 0, 0, 0, 0, time.UTC)

// Prefs is one person's settings for one chat.
type Prefs struct {
	Chat         int64      `json:"chat"`
	Pinned       int        `json:"pinned"` // order among pinned chats; 0 when not pinned
	Archived     bool       `json:"archived"`
	MutedUntil   *time.Time `json:"muted_until"`
	MentionsOnly bool       `json:"mentions_only"`
	MarkedUnread bool       `json:"marked_unread"`
	Sound        string     `json:"sound"` // in-app sound for this chat; "" is the default
}

// PrefsReq changes some of them; a nil field stays as it is.
type PrefsReq struct {
	Pinned       *bool      `json:"pinned"`
	Archived     *bool      `json:"archived"`
	Muted        *bool      `json:"muted"`       // false unmutes; true with no time mutes for good
	MutedUntil   *time.Time `json:"muted_until"` // with muted true: until then
	MentionsOnly *bool      `json:"mentions_only"`
	MarkedUnread *bool      `json:"marked_unread"`
	Sound        *string    `json:"sound"`
}

// SetPrefs applies req to user's membership of chatID, in one transaction.
func (s *Store) SetPrefs(ctx context.Context, user string, chatID int64, req PrefsReq, now time.Time) (*Prefs, error) {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback(ctx)
	p := &Prefs{Chat: chatID}
	var pinned *int
	err = tx.QueryRow(ctx, `SELECT pinned_order, archived, muted_until, mentions_only, marked_unread, sound FROM chat_members
		WHERE chat_id = $1 AND user_id = $2 AND role NOT IN ('left', 'banned') FOR UPDATE`, chatID, user).
		Scan(&pinned, &p.Archived, &p.MutedUntil, &p.MentionsOnly, &p.MarkedUnread, &p.Sound)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrNotMember
	}
	if err != nil {
		return nil, err
	}
	if req.Archived != nil && *req.Archived != p.Archived {
		p.Archived = *req.Archived
		pinned = nil // a chat moving between the list and the archive leaves its pins behind
	}
	if req.Pinned != nil {
		switch {
		case !*req.Pinned:
			pinned = nil
		case pinned == nil:
			limit := maxPinnedMain
			if p.Archived {
				limit = maxPinnedArchive
			}
			var n, top int
			if err := tx.QueryRow(ctx, `SELECT count(*), COALESCE(max(pinned_order), 0) FROM chat_members
				WHERE user_id = $1 AND pinned_order IS NOT NULL AND archived = $2 AND role NOT IN ('left', 'banned')`, user, p.Archived).Scan(&n, &top); err != nil {
				return nil, err
			}
			if n >= limit {
				return nil, ErrPinLimit
			}
			top++
			pinned = &top
		}
	}
	if req.Muted != nil {
		switch {
		case !*req.Muted:
			p.MutedUntil = nil
		case req.MutedUntil == nil || req.MutedUntil.After(MuteForever):
			t := MuteForever
			p.MutedUntil = &t
		case req.MutedUntil.After(now):
			t := req.MutedUntil.UTC()
			p.MutedUntil = &t
		default:
			return nil, ErrForbidden
		}
	}
	if req.MentionsOnly != nil {
		p.MentionsOnly = *req.MentionsOnly
	}
	if req.MarkedUnread != nil {
		p.MarkedUnread = *req.MarkedUnread
	}
	if req.Sound != nil {
		if !chatSounds[*req.Sound] {
			return nil, ErrForbidden
		}
		p.Sound = *req.Sound
	}
	if _, err := tx.Exec(ctx, `UPDATE chat_members SET pinned_order = $3, archived = $4, muted_until = $5, mentions_only = $6, marked_unread = $7, sound = $8
		WHERE chat_id = $1 AND user_id = $2`, chatID, user, pinned, p.Archived, p.MutedUntil, p.MentionsOnly, p.MarkedUnread, p.Sound); err != nil {
		return nil, err
	}
	if pinned != nil {
		p.Pinned = *pinned
	}
	return p, tx.Commit(ctx)
}

// OrderPins puts user's pinned chats in the order given (chats not pinned, or
// not theirs, are ignored).
func (s *Store) OrderPins(ctx context.Context, user string, chats []int64) error {
	if len(chats) > maxPinnedMain+maxPinnedArchive {
		return ErrForbidden
	}
	_, err := s.pool.Exec(ctx, `UPDATE chat_members m SET pinned_order = o.n
		FROM unnest($2::bigint[]) WITH ORDINALITY AS o(chat_id, n)
		WHERE m.user_id = $1 AND m.chat_id = o.chat_id AND m.pinned_order IS NOT NULL`, user, chats)
	return err
}

// VisibleFrom is the pts a reader's view of a chat starts after: where they
// cleared it, or where they joined a supergroup that hides its history from
// newcomers (admins see it all).
func (s *Store) VisibleFrom(ctx context.Context, chatID int64, user string) int64 {
	var from int64
	_ = s.pool.QueryRow(ctx, `SELECT GREATEST(m.cleared_pts,
			CASE WHEN COALESCE((c.settings->>'hidden_history')::boolean, false) AND m.role NOT IN ('owner', 'admin') THEN m.joined_pts ELSE 0 END)
		FROM chat_members m JOIN chats c ON c.id = m.chat_id WHERE m.chat_id = $1 AND m.user_id = $2`, chatID, user).Scan(&from)
	return from
}

// clearFor hides everything up to the chat's current pts from users.
func clearFor(ctx context.Context, tx pgx.Tx, chatID int64, users []string) error {
	_, err := tx.Exec(ctx, `UPDATE chat_members m SET cleared_pts = c.pts, last_read_pts = GREATEST(m.last_read_pts, c.pts), marked_unread = false
		FROM chats c WHERE c.id = m.chat_id AND m.chat_id = $1 AND m.user_id = ANY($2)`, chatID, users)
	return err
}

// ---------------------------------------------------------------- service

// SetPrefs changes user's settings for a chat and tells their other devices.
func (s *Service) SetPrefs(ctx context.Context, user string, chatID int64, req PrefsReq) (*Prefs, error) {
	p, err := s.Store.SetPrefs(ctx, user, chatID, req, s.now())
	if err != nil {
		return nil, err
	}
	s.deliver([]string{user}, "prefs", p, nil)
	return p, nil
}

// ClearChat deletes a chat's history for user, or in a private chat (and
// Saved Messages) for both sides when forAll. In a group only the reader's
// own view is cleared; leaving is separate.
func (s *Service) ClearChat(ctx context.Context, user string, chatID int64, forAll bool) error {
	if _, err := s.Store.Role(ctx, chatID, user); err != nil {
		return err
	}
	typ, _, err := s.Store.ChatType(ctx, chatID, user)
	if err != nil {
		return err
	}
	if typ == "secret" || (forAll && typ != "private" && typ != "saved") {
		return ErrForbidden
	}
	members, err := s.Store.Members(ctx, chatID)
	if err != nil {
		return err
	}
	tx, err := s.Store.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	who := []string{user}
	if forAll {
		if _, err := tx.Exec(ctx, `UPDATE chat_messages SET deleted_at = now(), text = '' WHERE chat_id = $1 AND deleted_at IS NULL`, chatID); err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `DELETE FROM chat_pins WHERE chat_id = $1`, chatID); err != nil {
			return err
		}
		who = members
	}
	if err := clearFor(ctx, tx, chatID, who); err != nil {
		return err
	}
	if err := tx.Commit(ctx); err != nil {
		return err
	}
	if typ == "private" { // it leaves their lists until a new message
		s.deliver(who, "chat_cleared", map[string]any{"chat": chatID}, nil)
	} else {
		s.deliver(who, "reset", map[string]any{"chat": chatID}, nil)
	}
	return nil
}
