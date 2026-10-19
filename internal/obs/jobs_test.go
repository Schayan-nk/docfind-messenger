package obs

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"
)

func tracked() (context.Context, *Request) {
	rq := NewRequest()
	return With(context.Background(), rq), rq
}

// A query method that calls another query method is ONE wait, not two.
func TestNestedCallsAreNotCountedTwice(t *testing.T) {
	ctx, rq := tracked()
	outer := Track(ctx, DB)
	inner := Track(ctx, DB)
	time.Sleep(20 * time.Millisecond)
	inner()
	outer()
	d, n := rq.Spent(DB)
	if n != 2 {
		t.Errorf("counted %d calls, want 2", n)
	}
	if d > 35*time.Millisecond {
		t.Errorf("nested calls reported %v; the wait was ~20ms and must not be doubled", d)
	}
}

// Three queries in parallel make the request wait once, not three times.
func TestParallelCallsReportTheWallTime(t *testing.T) {
	ctx, rq := tracked()
	var wg sync.WaitGroup
	for range 3 {
		wg.Go(func() {
			defer Track(ctx, DB)()
			time.Sleep(20 * time.Millisecond)
		})
	}
	wg.Wait()
	d, n := rq.Spent(DB)
	if n != 3 {
		t.Errorf("counted %d calls, want 3", n)
	}
	if d < 15*time.Millisecond || d > 45*time.Millisecond {
		t.Errorf("three parallel 20ms calls reported %v, want about 20ms of wall time", d)
	}
}

// Sequential calls do add up.
func TestSequentialCallsAddUp(t *testing.T) {
	ctx, rq := tracked()
	for range 2 {
		stop := Track(ctx, AI)
		time.Sleep(10 * time.Millisecond)
		stop()
	}
	if d, _ := rq.Spent(AI); d < 18*time.Millisecond {
		t.Errorf("two sequential 10ms calls reported %v", d)
	}
}

// Stopping twice must not unbalance the in-flight count.
func TestAStopFunctionIsIdempotent(t *testing.T) {
	ctx, rq := tracked()
	stop := Track(ctx, DB)
	stop()
	stop()
	later := Track(ctx, DB)
	time.Sleep(5 * time.Millisecond)
	later()
	if d, n := rq.Spent(DB); n != 2 || d <= 0 {
		t.Errorf("after a double stop: %v over %d calls", d, n)
	}
}

func TestJobsRecordRunsFailuresAndTheLastGoodRun(t *testing.T) {
	j := &Jobs{}
	j.Start("sync")(12, nil)
	j.Start("sync")(0, errors.New("mysql: connection refused, password=hunter2hunter"))
	snap := j.Snapshot()
	if len(snap) != 1 {
		t.Fatalf("jobs = %+v", snap)
	}
	s := snap[0]
	if s.Runs != 2 || s.Failures != 1 || s.LastItems != 0 || s.LastOK.IsZero() || s.Running {
		t.Errorf("stats = %+v", s)
	}
	if s.LastError == "" {
		t.Error("the failure's error was not kept")
	}
	// A credential in an error must not survive into the job list.
	if contains(s.LastError, "hunter2hunter") {
		t.Errorf("a password reached the job list: %q", s.LastError)
	}
}

func TestAJobInProgressIsMarkedRunning(t *testing.T) {
	j := &Jobs{}
	done := j.Start("rescan")
	if !j.Snapshot()[0].Running {
		t.Error("a started job is not marked running")
	}
	done(1, nil)
	if j.Snapshot()[0].Running {
		t.Error("a finished job is still marked running")
	}
}

func contains(s, sub string) bool {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return true
		}
	}
	return false
}
