package chat

import (
	"context"
	"time"

	"github.com/jackc/pgx/v5"
)

// Polls in PostgreSQL (migration 061). The rules are in polls.go.

// CreatePoll stores a poll and its options; the message comes after.
func (s *Store) CreatePoll(ctx context.Context, chatID int64, user string, d PollDef, correct *int, explanation string) (int64, error) {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return 0, err
	}
	defer tx.Rollback(ctx)
	var id int64
	err = tx.QueryRow(ctx, `
		INSERT INTO chat_polls (chat_id, created_by, question, anonymous, multiple, quiz, correct, explanation, closes_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9) RETURNING id`,
		chatID, user, d.Question, d.Anonymous, d.Multiple, d.Quiz, correct, explanation, d.CloseAt).Scan(&id)
	if err != nil {
		return 0, err
	}
	for i, o := range d.Options {
		if _, err := tx.Exec(ctx, `INSERT INTO chat_poll_options (poll_id, idx, text) VALUES ($1, $2, $3)`, id, i, o); err != nil {
			return 0, err
		}
	}
	return id, tx.Commit(ctx)
}

// DropPoll removes a poll no message points at.
func (s *Store) DropPoll(ctx context.Context, id int64) {
	_, _ = s.pool.Exec(ctx, `DELETE FROM chat_polls WHERE id = $1 AND message_id IS NULL`, id)
}

// LinkPoll ties a poll to its message.
func (s *Store) LinkPoll(ctx context.Context, id, msgID int64) error {
	_, err := s.pool.Exec(ctx, `UPDATE chat_polls SET message_id = $2 WHERE id = $1`, id, msgID)
	return err
}

const pollCols = `p.id, p.chat_id, COALESCE(p.message_id, 0), COALESCE(p.created_by, ''), p.anonymous, p.multiple, p.quiz, p.correct, p.explanation, p.closes_at, p.closed_at,
	(SELECT count(*) FROM chat_poll_options o WHERE o.poll_id = p.id)`

type rowScanner interface{ Scan(dest ...any) error }

func scanPoll(r rowScanner) (*pollRow, error) {
	p := &pollRow{}
	var correct *int16
	err := r.Scan(&p.ID, &p.ChatID, &p.MsgID, &p.Creator, &p.Anonymous, &p.Multiple, &p.Quiz, &correct, &p.Explanation, &p.ClosesAt, &p.ClosedAt, &p.NOpts)
	if correct != nil {
		c := int(*correct)
		p.Correct = &c
	}
	return p, err
}

// Poll reads one poll.
func (s *Store) Poll(ctx context.Context, id int64) (*pollRow, error) {
	return scanPoll(s.pool.QueryRow(ctx, `SELECT `+pollCols+` FROM chat_polls p WHERE p.id = $1`, id))
}

// Vote replaces user's choice and logs a "poll" update. The update carries
// only how many have voted: each reader fetches what they may see.
func (s *Store) Vote(ctx context.Context, p *pollRow, user string, opts []int) (*Update, error) {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback(ctx)
	// Lock the poll row: a quiz answer is final, and two votes at once must not both count.
	var closed *time.Time
	if err := tx.QueryRow(ctx, `SELECT closed_at FROM chat_polls WHERE id = $1 FOR UPDATE`, p.ID).Scan(&closed); err != nil {
		return nil, err
	}
	if closed != nil {
		return nil, ErrPollClosed
	}
	if p.Quiz {
		var n int
		if err := tx.QueryRow(ctx, `SELECT count(*) FROM chat_poll_votes WHERE poll_id = $1 AND user_id = $2`, p.ID, user).Scan(&n); err != nil {
			return nil, err
		}
		if n > 0 {
			return nil, ErrBadVote
		}
	}
	if _, err := tx.Exec(ctx, `DELETE FROM chat_poll_votes WHERE poll_id = $1 AND user_id = $2`, p.ID, user); err != nil {
		return nil, err
	}
	for _, o := range opts {
		if _, err := tx.Exec(ctx, `INSERT INTO chat_poll_votes (poll_id, user_id, idx) VALUES ($1, $2, $3)`, p.ID, user, o); err != nil {
			return nil, err
		}
	}
	up, err := pollUpdate(ctx, tx, p, false)
	if err != nil {
		return nil, err
	}
	return up, tx.Commit(ctx)
}

func pollUpdate(ctx context.Context, tx pgx.Tx, p *pollRow, closed bool) (*Update, error) {
	var total int
	if err := tx.QueryRow(ctx, `SELECT count(DISTINCT user_id) FROM chat_poll_votes WHERE poll_id = $1`, p.ID).Scan(&total); err != nil {
		return nil, err
	}
	pts, err := bump(ctx, tx, p.ChatID)
	if err != nil {
		return nil, err
	}
	return addUpdate(ctx, tx, p.ChatID, pts, "poll", map[string]any{"poll": p.ID, "msg": p.MsgID, "total": total, "closed": closed})
}

// ClosePoll closes a poll once; nil, nil when it was already closed.
func (s *Store) ClosePoll(ctx context.Context, p *pollRow) (*Update, error) {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback(ctx)
	tag, err := tx.Exec(ctx, `UPDATE chat_polls SET closed_at = now() WHERE id = $1 AND closed_at IS NULL`, p.ID)
	if err != nil || tag.RowsAffected() == 0 {
		return nil, err
	}
	up, err := pollUpdate(ctx, tx, p, true)
	if err != nil {
		return nil, err
	}
	return up, tx.Commit(ctx)
}

// DuePolls lists open polls whose closing time has passed.
func (s *Store) DuePolls(ctx context.Context, now time.Time, limit int) ([]*pollRow, error) {
	rows, err := s.pool.Query(ctx, `SELECT `+pollCols+` FROM chat_polls p
		WHERE p.closed_at IS NULL AND p.closes_at IS NOT NULL AND p.closes_at <= $1 AND p.message_id IS NOT NULL
		ORDER BY p.closes_at LIMIT $2`, now, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []*pollRow
	for rows.Next() {
		p, err := scanPoll(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, p)
	}
	return out, rows.Err()
}

// PollCounts returns the votes per option, how many people voted, and viewer's choice.
func (s *Store) PollCounts(ctx context.Context, p *pollRow, viewer string) ([]int, int, []int, error) {
	counts := make([]int, p.NOpts)
	rows, err := s.pool.Query(ctx, `SELECT idx, count(*), bool_or(user_id = $2) FROM chat_poll_votes WHERE poll_id = $1 GROUP BY idx`, p.ID, viewer)
	if err != nil {
		return nil, 0, nil, err
	}
	defer rows.Close()
	var mine []int
	for rows.Next() {
		var idx int16
		var n int
		var me bool
		if err := rows.Scan(&idx, &n, &me); err != nil {
			return nil, 0, nil, err
		}
		if int(idx) < len(counts) {
			counts[idx] = n
		}
		if me {
			mine = append(mine, int(idx))
		}
	}
	if err := rows.Err(); err != nil {
		return nil, 0, nil, err
	}
	var total int
	err = s.pool.QueryRow(ctx, `SELECT count(DISTINCT user_id) FROM chat_poll_votes WHERE poll_id = $1`, p.ID).Scan(&total)
	return counts, total, mine, err
}

// PollVoters lists, per option, who chose it (most recent first, at most limit).
func (s *Store) PollVoters(ctx context.Context, id int64, limit int) (map[int][]string, error) {
	rows, err := s.pool.Query(ctx, `
		SELECT idx, user_id FROM (
		  SELECT idx, user_id, row_number() OVER (PARTITION BY idx ORDER BY voted_at DESC) AS n
		  FROM chat_poll_votes WHERE poll_id = $1) v
		WHERE n <= $2 ORDER BY idx, n`, id, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[int][]string{}
	for rows.Next() {
		var idx int16
		var u string
		if err := rows.Scan(&idx, &u); err != nil {
			return nil, err
		}
		out[int(idx)] = append(out[int(idx)], u)
	}
	return out, rows.Err()
}
