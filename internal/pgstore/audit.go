package pgstore

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/Schayan-nk/docfind-messenger/internal/audit"
)

// The audit trail's table. See migrations/009_audit.sql for why it exists and
// what keeps it append-only; internal/audit for what gets recorded.

// Audit stores audit events. It satisfies audit.Sink.
type Audit struct{ db *DB }

// NewAudit returns the store over db.
func NewAudit(db *DB) *Audit { return &Audit{db: db} }

var _ audit.Sink = (*Audit)(nil)

// ErrNoDatabase is returned by a store over a database that never opened.
var ErrNoDatabase = errors.New("pgstore: PostgreSQL is not connected")

// Record appends one event.
//
// A store over a database that never opened -- PostgreSQL down when docfind
// started, which is the emergency sign-in's whole situation -- answers with
// an error, which the recorder turns into a line in the application log.
// It used to reach pgx with a nil pool, and pgx panics on one: every audited
// request, sign-in first, dropped its connection without an answer.
func (a *Audit) Record(ctx context.Context, e audit.Event) error {
	if a == nil || a.db.Pool() == nil {
		return ErrNoDatabase
	}
	var detail []byte
	if len(e.Detail) > 0 {
		b, err := json.Marshal(e.Detail)
		if err != nil {
			return fmt.Errorf("pgstore: audit detail: %w", err)
		}
		detail = b
	}
	at := e.At
	if at.IsZero() {
		at = time.Now()
	}
	// An id the table already holds is the same event sent again -- replayed
	// from the spool after an answer that was lost (migration 019) -- and is
	// not a second row.
	var eventID *string
	if e.ID != "" {
		eventID = &e.ID
	}
	_, err := a.db.Pool().Exec(ctx, `
		INSERT INTO audit_events (at, actor, action, target, result, status, ip, user_agent, request_id, detail, event_id)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11)
		ON CONFLICT (event_id) WHERE event_id IS NOT NULL DO NOTHING`,
		at, e.Actor, e.Action, e.Target, e.Result, e.Status, e.IP, e.UserAgent, e.RequestID, detail, eventID)
	if err != nil {
		return fmt.Errorf("pgstore: recording audit event: %w", err)
	}
	return nil
}

// AuditQuery narrows a listing. Every field is optional.
type AuditQuery struct {
	Actor  string    // exact username
	Target string    // a prefix: "doc:Contracts/" finds everything under that folder
	Action string    // exact, e.g. "DELETE /api/users"
	Result string    // ok | denied | rejected | error
	Since  time.Time // inclusive
	Until  time.Time // exclusive
	Before int64     // only events with a smaller id -- the next page
	Limit  int       // at most 500; 100 when unset
}

// AuditRow is one stored event with its id, for paging.
type AuditRow struct {
	ID int64 `json:"id"`
	audit.Event
}

// List returns events newest first.
func (a *Audit) List(ctx context.Context, q AuditQuery) ([]AuditRow, error) {
	ctx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	var (
		where []string
		args  []any
	)
	add := func(cond string, v any) {
		args = append(args, v)
		where = append(where, fmt.Sprintf(cond, len(args)))
	}
	if q.Actor != "" {
		add("actor = $%d", q.Actor)
	}
	if q.Target != "" {
		// A prefix, with LIKE's own wildcards taken literally: a document
		// called "50%_final.docx" is a name, not a pattern.
		add(`target LIKE $%d || '%%' ESCAPE '\'`, likeEscape(q.Target))
	}
	if q.Action != "" {
		add("action = $%d", q.Action)
	}
	if q.Result != "" {
		add("result = $%d", q.Result)
	}
	if !q.Since.IsZero() {
		add("at >= $%d", q.Since)
	}
	if !q.Until.IsZero() {
		add("at < $%d", q.Until)
	}
	if q.Before > 0 {
		add("id < $%d", q.Before)
	}
	limit := q.Limit
	if limit <= 0 {
		limit = 100
	}
	limit = min(limit, 500)
	sql := `SELECT id, at, actor, action, target, result, status, ip, user_agent, request_id, detail
	          FROM audit_events`
	if len(where) > 0 {
		sql += " WHERE " + strings.Join(where, " AND ")
	}
	args = append(args, limit)
	sql += fmt.Sprintf(" ORDER BY id DESC LIMIT $%d", len(args))

	rows, err := a.db.Pool().Query(ctx, sql, args...)
	if err != nil {
		return nil, fmt.Errorf("pgstore: listing audit events: %w", err)
	}
	defer rows.Close()
	out := []AuditRow{}
	for rows.Next() {
		var (
			r      AuditRow
			detail []byte
		)
		if err := rows.Scan(&r.ID, &r.At, &r.Actor, &r.Action, &r.Target, &r.Result, &r.Status,
			&r.IP, &r.UserAgent, &r.RequestID, &detail); err != nil {
			return nil, fmt.Errorf("pgstore: reading audit events: %w", err)
		}
		if len(detail) > 0 {
			_ = json.Unmarshal(detail, &r.Detail)
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

// likeEscape makes s match itself literally inside a LIKE pattern.
func likeEscape(s string) string {
	r := strings.NewReplacer(`\`, `\\`, `%`, `\%`, `_`, `\_`)
	return r.Replace(s)
}
