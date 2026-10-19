package pgstore

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"testing"
	"time"

	"github.com/Schayan-nk/docfind-messenger/internal/audit"
)

// A store over a database that never opened answers with an error instead of
// panicking inside pgx. It is what docfind holds when PostgreSQL was down at
// start-up, and the panic took every audited request down with it -- sign-in
// first, which is the one the emergency path exists to keep working.
func TestTheAuditStoreOverNoDatabaseFailsPolitely(t *testing.T) {
	err := NewAudit(nil).Record(context.Background(), audit.Event{Action: "auth.login"})
	if !errors.Is(err, ErrNoDatabase) {
		t.Fatalf("got %v, want ErrNoDatabase", err)
	}
}

// The audit table against a real server.
//
// The table refuses to lose recent rows, which is the point of it -- and
// which means a test that wrote "now" rows would leave them in the live trail
// for a year. So the rows this test keeps are dated 400 days ago (old enough
// for the cleanup to be allowed to delete them), and every check that needs a
// RECENT row runs inside a transaction that is rolled back.
func auditStore(t *testing.T) (*Audit, string) {
	t.Helper()
	db := testDB(t)
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	if _, err := db.Migrate(ctx); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	b := make([]byte, 6)
	rand.Read(b)
	actor := "test-audit-" + hex.EncodeToString(b)
	t.Cleanup(func() {
		db.Pool().Exec(context.Background(), `DELETE FROM audit_events WHERE actor = $1`, actor)
	})
	return NewAudit(db), actor
}

func TestTheAuditTrailRecordsAndLists(t *testing.T) {
	a, actor := auditStore(t)
	ctx := context.Background()
	old := time.Now().AddDate(0, 0, -400)
	for i, e := range []audit.Event{
		{At: old, Actor: actor, Action: "POST /api/docs/rename", Target: "doc:Contracts/50%_draft.docx",
			Result: "ok", Status: 200, Detail: map[string]any{"to": "final.docx"}},
		{At: old.Add(time.Minute), Actor: actor, Action: "DELETE /api/users", Target: "user:x",
			Result: "denied", Status: 403},
		{At: old.Add(2 * time.Minute), Actor: actor, Action: "POST /api/docs/delete", Target: "doc:Contracts2/y.pdf",
			Result: "ok", Status: 200},
	} {
		if err := a.Record(ctx, e); err != nil {
			t.Fatalf("record %d: %v", i, err)
		}
	}

	all, err := a.List(ctx, AuditQuery{Actor: actor})
	if err != nil {
		t.Fatal(err)
	}
	if len(all) != 3 || all[0].Action != "POST /api/docs/delete" {
		t.Fatalf("newest first, three rows: %+v", all)
	}
	if all[2].Detail["to"] != "final.docx" {
		t.Errorf("detail did not round-trip: %+v", all[2].Detail)
	}

	// The target is a PREFIX, with LIKE's wildcards taken literally. Were "_"
	// a wildcard, "doc:Contracts_" would match both "Contracts/" and
	// "Contracts2/"; taken literally it matches neither.
	got, err := a.List(ctx, AuditQuery{Actor: actor, Target: "doc:Contracts_"})
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 0 {
		t.Errorf("an underscore acted as a wildcard: %d rows", len(got))
	}
	if got, _ = a.List(ctx, AuditQuery{Actor: actor, Target: "doc:Contracts/"}); len(got) != 1 {
		t.Errorf("a folder prefix matched %d rows, want 1", len(got))
	}
	got, _ = a.List(ctx, AuditQuery{Actor: actor, Result: "denied"})
	if len(got) != 1 || got[0].Status != 403 {
		t.Errorf("result filter: %+v", got)
	}
	// Paging: everything older than the newest.
	got, _ = a.List(ctx, AuditQuery{Actor: actor, Before: all[0].ID})
	if len(got) != 2 {
		t.Errorf("before=%d gave %d rows", all[0].ID, len(got))
	}
}

// The database itself refuses to rewrite history.
func TestTheAuditTrailCannotBeRewritten(t *testing.T) {
	a, actor := auditStore(t)
	ctx := context.Background()
	p := a.db.Pool()

	try := func(name, stmt string) {
		t.Helper()
		tx, err := p.Begin(ctx)
		if err != nil {
			t.Fatal(err)
		}
		defer tx.Rollback(ctx)
		if _, err := tx.Exec(ctx, `INSERT INTO audit_events (actor, action, result) VALUES ($1, 'test', 'ok')`,
			actor); err != nil {
			t.Fatal(err)
		}
		if _, err := tx.Exec(ctx, stmt, actor); err == nil {
			t.Errorf("%s was allowed", name)
		}
	}
	try("an UPDATE", `UPDATE audit_events SET result = 'hidden' WHERE actor = $1`)
	try("DELETE of a recent row", `DELETE FROM audit_events WHERE actor = $1`)

	// TRUNCATE takes no parameter; the insert above it still needs one.
	tx, err := p.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback(ctx)
	if _, err := tx.Exec(ctx, `TRUNCATE audit_events`); err == nil {
		t.Error("TRUNCATE was allowed")
	}
}
