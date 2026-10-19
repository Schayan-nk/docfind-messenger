package audit

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"
)

type fakeSink struct {
	mu   sync.Mutex
	down bool
	got  []Event
	ids  map[string]bool
}

func (f *fakeSink) Record(_ context.Context, e Event) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.down {
		return errors.New("connection refused")
	}
	if f.ids == nil {
		f.ids = map[string]bool{}
	}
	if !f.ids[e.ID] { // the table's unique id: a repeat is not a second row
		f.ids[e.ID] = true
		f.got = append(f.got, e)
	}
	return nil
}

func (f *fakeSink) set(down bool) { f.mu.Lock(); f.down = down; f.mu.Unlock() }
func (f *fakeSink) n() int        { f.mu.Lock(); defer f.mu.Unlock(); return len(f.got) }

// Events the database refuses wait on disk, survive a restart, and go in --
// each once, oldest first -- when it is back.
func TestSpoolKeepsWhatTheDatabaseRefused(t *testing.T) {
	path := filepath.Join(t.TempDir(), "audit-spool.jsonl")
	sink := &fakeSink{down: true}
	r := NewRecorder()
	r.UseSink(sink)
	r.UseSpool(OpenSpool(path))
	ctx := context.Background()

	for _, a := range []string{"login", "POST /api/users", "DELETE /api/docs"} {
		r.Record(ctx, Event{Actor: "boss", Action: a, Result: "ok"})
	}
	if sink.n() != 0 {
		t.Fatal("the sink took events while down")
	}

	// A restart: a new spool on the same file still knows they are waiting.
	sp := OpenSpool(path)
	if !sp.Pending() {
		t.Fatal("the spooled events were forgotten across a restart")
	}
	r.UseSpool(sp)

	sink.set(false)
	sent, kept, err := r.ReplaySpool(ctx)
	if err != nil || sent != 3 || kept != 0 || sink.n() != 3 {
		t.Fatalf("replay: sent %d kept %d err %v; table has %d", sent, kept, err, sink.n())
	}
	if sink.got[0].Action != "login" || sink.got[2].Action != "DELETE /api/docs" || sink.got[0].ID == "" {
		t.Errorf("order or ids lost: %+v", sink.got)
	}
	if sp.Pending() {
		t.Error("still pending after everything went in")
	}
	if _, err := os.Stat(path); !errors.Is(err, os.ErrNotExist) {
		t.Error("the spool file is still there")
	}
	// Replaying the same events again -- as after a lost answer -- adds nothing.
	for _, e := range sink.got {
		sp.Append(e)
	}
	r.ReplaySpool(ctx)
	if sink.n() != 3 {
		t.Errorf("a replayed event became a second row: %d", sink.n())
	}
}

// A database that fails half-way keeps the rest, in order, for next time.
func TestSpoolKeepsTheRestWhenReplayFails(t *testing.T) {
	path := filepath.Join(t.TempDir(), "spool.jsonl")
	sp := OpenSpool(path)
	for i := 0; i < 4; i++ {
		sp.Append(Event{ID: string(rune('a' + i)), Action: "x", At: time.Now()})
	}
	sink := &failAfter{n: 2}
	sent, kept, err := sp.Replay(context.Background(), sink)
	if sent != 2 || kept != 2 || err == nil || !sp.Pending() {
		t.Fatalf("sent %d kept %d err %v pending %v", sent, kept, err, sp.Pending())
	}
	ok := &fakeSink{}
	if sent, _, _ := sp.Replay(context.Background(), ok); sent != 2 || ok.got[0].ID != "c" {
		t.Errorf("the rest: sent %d, first %+v", sent, ok.got)
	}
}

// A database that takes an event again is back: the spool follows by itself.
func TestRecordReplaysOnceTheDatabaseIsBack(t *testing.T) {
	sink := &fakeSink{down: true}
	r := NewRecorder()
	r.UseSink(sink)
	r.UseSpool(OpenSpool(filepath.Join(t.TempDir(), "s.jsonl")))
	r.Record(context.Background(), Event{Action: "during the outage", Result: "ok"})
	sink.set(false)
	r.Record(context.Background(), Event{Action: "after", Result: "ok"})
	deadline := time.Now().Add(3 * time.Second)
	for sink.n() < 2 && time.Now().Before(deadline) {
		time.Sleep(20 * time.Millisecond)
	}
	if sink.n() != 2 {
		t.Errorf("the outage's event did not follow: %d in the table", sink.n())
	}
}

type failAfter struct{ n int }

func (f *failAfter) Record(context.Context, Event) error {
	if f.n == 0 {
		return errors.New("down again")
	}
	f.n--
	return nil
}
