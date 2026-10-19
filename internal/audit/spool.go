package audit

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"os"
	"sync"
	"sync/atomic"
	"time"
)

// Spool keeps the audit events the database could not take, on this
// machine's disk, until it can.
//
// The application log already holds a copy of every event, so nothing was
// ever lost outright. But the log is not the audit trail: the table is what
// an administrator searches at /security, and what the application cannot
// edit. An outage used to leave a hole in it exactly where somebody would
// look -- "who changed the permissions while the database was down?" -- with
// the answer only in a log file on the server. Now the events of an outage
// wait here and go into the table when it is back, each at most once: every
// event carries its own id, and the table ignores an id it already has
// (migration 019), so a send whose answer was lost and is sent again does not
// become two rows.
//
// One JSON line per event, appended and synced before Append returns, so a
// crash loses nothing that was spooled. Bounded (spoolMax): an outage long
// enough to fill it keeps its newest events in the log only, and says so.
type Spool struct {
	path     string
	mu       sync.Mutex // one writer, and replay's rename, at a time
	pending  atomic.Bool
	replayMu sync.Mutex
}

// spoolMax bounds the file: tens of thousands of events, far more than a
// working day's outage produces.
const spoolMax = 32 << 20

// ErrSpoolFull is returned by Append when the spool is at its bound.
var ErrSpoolFull = errors.New("audit: the spool is full")

// OpenSpool returns the spool at path, noting whether it holds events from
// before a restart.
func OpenSpool(path string) *Spool {
	sp := &Spool{path: path}
	if fi, err := os.Stat(path); err == nil && fi.Size() > 0 {
		sp.pending.Store(true)
	}
	return sp
}

// Pending reports whether events are waiting.
func (sp *Spool) Pending() bool { return sp != nil && sp.pending.Load() }

// Append adds one event.
func (sp *Spool) Append(e Event) error {
	line, err := json.Marshal(e)
	if err != nil {
		return err
	}
	sp.mu.Lock()
	defer sp.mu.Unlock()
	if fi, err := os.Stat(sp.path); err == nil && fi.Size()+int64(len(line)) > spoolMax {
		return ErrSpoolFull
	}
	f, err := os.OpenFile(sp.path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600)
	if err != nil {
		return err
	}
	_, werr := f.Write(append(line, '\n'))
	serr := f.Sync()
	cerr := f.Close()
	if err := errors.Join(werr, serr, cerr); err != nil {
		return err
	}
	sp.pending.Store(true)
	return nil
}

// Replay sends the waiting events to sink, oldest first, and keeps the ones
// it refuses. Events appended while it runs wait for the next replay. One
// replay at a time; a second caller returns at once.
func (sp *Spool) Replay(ctx context.Context, sink Sink) (sent, kept int, err error) {
	if sp == nil || sink == nil || !sp.replayMu.TryLock() {
		return 0, 0, nil
	}
	defer sp.replayMu.Unlock()

	// Take the file away from Append first, so what is read is fixed.
	sp.mu.Lock()
	work := sp.path + ".replaying"
	if _, err := os.Stat(work); errors.Is(err, os.ErrNotExist) {
		if err := os.Rename(sp.path, work); err != nil {
			sp.mu.Unlock()
			if errors.Is(err, os.ErrNotExist) {
				sp.pending.Store(false)
				return 0, 0, nil
			}
			return 0, 0, err
		}
	}
	// (A .replaying file already there is one a crash interrupted: it is
	// finished first, and the live spool waits for the next replay.)
	sp.pending.Store(false)
	sp.mu.Unlock()

	raw, err := os.ReadFile(work)
	if err != nil {
		sp.pending.Store(true)
		return 0, 0, err
	}
	var failed []Event
	sc := bufio.NewScanner(bytes.NewReader(raw))
	sc.Buffer(make([]byte, 0, 64<<10), 4<<20)
	for sc.Scan() {
		var e Event
		if json.Unmarshal(sc.Bytes(), &e) != nil {
			continue // a torn last line from a crash mid-write
		}
		if len(failed) > 0 || ctx.Err() != nil {
			failed = append(failed, e) // keep the order: nothing jumps a failure
			continue
		}
		sctx, cancel := context.WithTimeout(ctx, 5*time.Second)
		rerr := sink.Record(sctx, e)
		cancel()
		if rerr != nil {
			err = rerr
			failed = append(failed, e)
			continue
		}
		sent++
	}
	for _, e := range failed {
		if aerr := sp.Append(e); aerr != nil {
			return sent, len(failed), errors.Join(err, aerr) // the .replaying file stays, and is retried
		}
	}
	os.Remove(work)
	if _, serr := os.Stat(sp.path); serr == nil {
		sp.pending.Store(true)
	}
	return sent, len(failed), err
}
