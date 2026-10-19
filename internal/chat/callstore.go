package chat

import (
	"context"
	"time"

	"github.com/jackc/pgx/v5"
)

// The calls' rows (migration 050). Only who called whom, when, for how long
// and how it ended; the media never reaches the server.

// CallPeer is the other person in a call.
type CallPeer struct {
	ID    string `json:"id"`
	Name  string `json:"name"`
	Photo bool   `json:"photo,omitempty"`
}

// CallEntry is one call in a person's call list.
type CallEntry struct {
	ID       int64     `json:"id"`
	Chat     int64     `json:"chat"`
	Peer     CallPeer  `json:"peer"`
	Outgoing bool      `json:"outgoing"`
	Video    bool      `json:"video"`
	Started  time.Time `json:"started_at"`
	Duration int       `json:"duration"` // seconds connected
	Reason   string    `json:"reason"`   // how it ended
	Missed   bool      `json:"missed"`   // an incoming call never answered
	Rated    bool      `json:"rated"`
}

// CallCreate records a call from caller to callee in chat.
func (s *Store) CallCreate(ctx context.Context, chatID int64, caller, callee string, video bool) (int64, time.Time, error) {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return 0, time.Time{}, err
	}
	defer tx.Rollback(ctx)
	var id int64
	var at time.Time
	if err := tx.QueryRow(ctx, `INSERT INTO chat_calls (type, chat_id, started_by, is_video) VALUES ('p2p', $1, $2, $3)
		RETURNING id, started_at`, chatID, caller, video).Scan(&id, &at); err != nil {
		return 0, time.Time{}, err
	}
	if _, err := tx.Exec(ctx, `INSERT INTO chat_call_participants (call_id, user_id, role, joined_at, video_on)
		VALUES ($1, $2, 'caller', $3, $4), ($1, $5, 'callee', NULL, false)`, id, caller, at, video, callee); err != nil {
		return 0, time.Time{}, err
	}
	return id, at, tx.Commit(ctx)
}

// CallAnswered notes when callee picked up.
func (s *Store) CallAnswered(ctx context.Context, id int64, callee string, at time.Time) error {
	if _, err := s.pool.Exec(ctx, `UPDATE chat_calls SET answered_at = $2 WHERE id = $1 AND answered_at IS NULL AND ended_at IS NULL`, id, at); err != nil {
		return err
	}
	_, err := s.pool.Exec(ctx, `UPDATE chat_call_participants SET joined_at = $3 WHERE call_id = $1 AND user_id = $2
		AND EXISTS (SELECT 1 FROM chat_calls WHERE id = $1 AND ended_at IS NULL)`, id, callee, at)
	return err
}

// CallConnected notes when media first flowed (the duration counts from it).
func (s *Store) CallConnected(ctx context.Context, id int64, at time.Time) error {
	_, err := s.pool.Exec(ctx, `UPDATE chat_calls SET connected_at = $2 WHERE id = $1 AND connected_at IS NULL`, id, at)
	return err
}

// CallVideo notes that a voice call became a video call.
func (s *Store) CallVideo(ctx context.Context, id int64) error {
	_, err := s.pool.Exec(ctx, `UPDATE chat_calls SET is_video = true WHERE id = $1`, id)
	return err
}

// CallEnded closes a call and everyone's part in it.
func (s *Store) CallEnded(ctx context.Context, id int64, reason string, at time.Time) error {
	if _, err := s.pool.Exec(ctx, `UPDATE chat_calls SET ended_at = $2, end_reason = $3 WHERE id = $1 AND ended_at IS NULL`, id, at, reason); err != nil {
		return err
	}
	_, err := s.pool.Exec(ctx, `UPDATE chat_call_participants SET left_at = $2 WHERE call_id = $1 AND joined_at IS NOT NULL AND left_at IS NULL`, id, at)
	return err
}

// CloseOpenCalls ends the calls a stopped server left open (at start-up:
// nobody can still be in them).
func (s *Store) CloseOpenCalls(ctx context.Context, at time.Time) (int64, error) {
	tag, err := s.pool.Exec(ctx, `UPDATE chat_calls SET ended_at = $1, end_reason = 'failed' WHERE ended_at IS NULL`, at)
	if err != nil {
		return 0, err
	}
	_, err = s.pool.Exec(ctx, `UPDATE chat_call_participants SET left_at = $1 WHERE joined_at IS NOT NULL AND left_at IS NULL`, at)
	return tag.RowsAffected(), err
}

// CallRate keeps user's rating of an ended call they were in. ok is false
// when there is no such call for them, or it has not ended.
func (s *Store) CallRate(ctx context.Context, id int64, user string, stars int, note string) (bool, error) {
	tag, err := s.pool.Exec(ctx, `UPDATE chat_call_participants p SET rating = $3, rating_note = $4
		FROM chat_calls c WHERE c.id = p.call_id AND p.call_id = $1 AND p.user_id = $2 AND c.ended_at IS NOT NULL`, id, user, stars, note)
	return err == nil && tag.RowsAffected() == 1, err
}

// CallHide removes a call from user's list (not from the other person's).
func (s *Store) CallHide(ctx context.Context, id int64, user string) error {
	_, err := s.pool.Exec(ctx, `UPDATE chat_call_participants SET hidden = true WHERE call_id = $1 AND user_id = $2`, id, user)
	return err
}

// Calls is user's call list, newest first, before call id before (0: from
// the newest).
func (s *Store) Calls(ctx context.Context, user string, before int64, limit int) ([]CallEntry, error) {
	rows, err := s.pool.Query(ctx, `
		SELECT c.id, COALESCE(c.chat_id, 0), COALESCE(o.user_id, ''), me.role = 'caller', c.is_video, c.started_at,
		       COALESCE(EXTRACT(EPOCH FROM (c.ended_at - c.connected_at))::int, 0), COALESCE(c.end_reason, ''),
		       me.rating IS NOT NULL
		FROM chat_call_participants me
		JOIN chat_calls c ON c.id = me.call_id
		LEFT JOIN chat_call_participants o ON o.call_id = c.id AND o.user_id <> me.user_id
		WHERE me.user_id = $1 AND NOT me.hidden AND c.type = 'p2p' AND c.ended_at IS NOT NULL AND ($2 = 0 OR c.id < $2)
		ORDER BY c.id DESC LIMIT $3`, user, before, limit)
	if err != nil {
		return nil, err
	}
	return pgx.CollectRows(rows, func(r pgx.CollectableRow) (CallEntry, error) {
		var e CallEntry
		err := r.Scan(&e.ID, &e.Chat, &e.Peer.ID, &e.Outgoing, &e.Video, &e.Started, &e.Duration, &e.Reason, &e.Rated)
		e.Missed = !e.Outgoing && missedReason(e.Reason)
		return e, err
	})
}

// CallOf is the call id's chat and its two people, for checks.
func (s *Store) CallOf(ctx context.Context, id int64) (chatID int64, people []string, err error) {
	rows, err := s.pool.Query(ctx, `SELECT COALESCE(c.chat_id, 0), p.user_id FROM chat_calls c
		JOIN chat_call_participants p ON p.call_id = c.id WHERE c.id = $1`, id)
	if err != nil {
		return 0, nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var u string
		if err := rows.Scan(&chatID, &u); err != nil {
			return 0, nil, err
		}
		people = append(people, u)
	}
	if err := rows.Err(); err != nil {
		return 0, nil, err
	}
	if len(people) == 0 {
		return 0, nil, ErrNoCall
	}
	return chatID, people, nil
}

// GroupCallCreate records a group call in chatID started by user (it is
// "connected" from the start: its duration is the room's).
func (s *Store) GroupCallCreate(ctx context.Context, chatID int64, by string) (int64, time.Time, error) {
	var id int64
	var at time.Time
	err := s.pool.QueryRow(ctx, `INSERT INTO chat_calls (type, chat_id, started_by, connected_at) VALUES ('group', $1, $2, now())
		RETURNING id, started_at`, chatID, by).Scan(&id, &at)
	return id, at, err
}

// GroupCallJoin records user in group call id (again, after leaving).
func (s *Store) GroupCallJoin(ctx context.Context, id int64, user string, video bool, at time.Time) error {
	_, err := s.pool.Exec(ctx, `INSERT INTO chat_call_participants (call_id, user_id, role, joined_at, video_on)
		VALUES ($1, $2, 'member', $3, $4)
		ON CONFLICT (call_id, user_id) DO UPDATE SET left_at = NULL, video_on = chat_call_participants.video_on OR $4`, id, user, at, video)
	return err
}

// GroupCallLeave records that user left group call id.
func (s *Store) GroupCallLeave(ctx context.Context, id int64, user string, at time.Time) error {
	_, err := s.pool.Exec(ctx, `UPDATE chat_call_participants SET left_at = $3 WHERE call_id = $1 AND user_id = $2 AND left_at IS NULL`, id, user, at)
	return err
}

// missedReason: the ways a call ends without the person called answering.
func missedReason(r string) bool { return r == "missed" || r == "cancelled" || r == "busy" }
