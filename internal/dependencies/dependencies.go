// Package dependencies keeps one record per thing this server depends on --
// PostgreSQL, the databases it reads, the mail and SMS providers, the
// network -- and decides, for each call, whether it is worth making.
//
// Every dependency has a state a person can read (live, degraded, stale,
// offline), the facts behind it (last success, last attempt, latency, what
// kind of error), and a circuit breaker: after a few failures in a row calls
// stop being made for a cooldown that doubles on each failed retry, up to a
// cap; then one probe is let through, and its answer closes the breaker or
// opens it again. A request somebody is waiting for therefore fails in
// microseconds while a dependency is known to be down, and goes to the cache
// (Fetch) instead of hanging for its full timeout.
package dependencies

import (
	"context"
	"errors"
	"slices"
	"sync"
	"time"
)

// State is what a person is told about a dependency.
type State string

const (
	Live     State = "live"     // answering, and quickly
	Degraded State = "degraded" // answering, but slowly or after recent failures
	Stale    State = "stale"    // not answering; what it last said is still recent
	Offline  State = "offline"  // not answering, and nothing recent to show instead
	Unknown  State = "unknown"  // not asked yet
)

// Spec declares a dependency: how to ask whether it is there, how long a
// person may wait for it, and what they get instead when it is not.
type Spec struct {
	Name  string // stable id: "postgres", "reports", ...
	Label string // what the status bar calls it
	// Check asks the dependency whether it is there. It is also the probe a
	// half-open breaker lets through.
	Check func(ctx context.Context) error
	// Interval between background checks while healthy. Default 1 minute.
	Interval time.Duration
	// MaxWait is the most a user-facing call waits. Default 3 seconds.
	MaxWait time.Duration
	// SlowAfter: a success slower than this is "degraded". Default MaxWait/2.
	SlowAfter time.Duration
	// MaxStale: this long without a success and "stale" becomes "offline".
	// Default 1 hour.
	MaxStale time.Duration
	// FailThreshold consecutive failures open the breaker. Default 3.
	FailThreshold int
	// Cooldown is the first open period; each failed probe doubles it up to
	// MaxCooldown. Defaults 10 seconds and 5 minutes.
	Cooldown, MaxCooldown time.Duration
	// Fallback says what people get while it is down, for the status page.
	Fallback string
	// Downgrade names the capabilities lost while it is down.
	Downgrade []string
}

// Breaker positions.
const (
	Closed = "closed"
)

// ErrOpen is returned, without calling anything, while a breaker is open.
var ErrOpen = errors.New("dependencies: circuit open")

// Status is one dependency as the status page and the health checks see it.
type Status struct {
	Name        string    `json:"name"`
	Label       string    `json:"label"`
	State       State     `json:"state"`
	Breaker     string    `json:"breaker"`
	LastSuccess time.Time `json:"last_success"`
	LastAttempt time.Time `json:"last_attempt"`
	LatencyMs   int64     `json:"latency_ms"`
	ErrorClass  string    `json:"error_class,omitempty"`
	Error       string    `json:"error,omitempty"`
	// DataAgeSec is how old the newest answer is; -1 when there has been none.
	DataAgeSec int64     `json:"data_age_sec"`
	Failures   int       `json:"failures"`
	OpenUntil  time.Time `json:"open_until,omitempty"`
	Fallback   string    `json:"fallback,omitempty"`
	Downgrade  []string  `json:"downgrade,omitempty"`
}

type dep struct {
	spec        Spec
	lastSuccess time.Time
	lastAttempt time.Time
	latency     time.Duration
	err         error
	failures    int
	breaker     string
	openUntil   time.Time
}

// Registry is every dependency.
type Registry struct {
	mu    sync.Mutex
	deps  map[string]*dep
	order []string
	now   func() time.Time
	sims  map[string]Simulation // resilience test mode (simulate.go)
}

// All is every dependency's record, in registration order.
func (r *Registry) All() []Status {
	r.mu.Lock()
	defer r.mu.Unlock()
	out := make([]Status, 0, len(r.order))
	for _, n := range r.order {
		out = append(out, r.status(r.deps[n]))
	}
	return out
}

func (r *Registry) status(d *dep) Status {
	now := r.now()
	s := Status{
		Name: d.spec.Name, Label: d.spec.Label, Breaker: d.breaker,
		LastSuccess: d.lastSuccess, LastAttempt: d.lastAttempt, LatencyMs: d.latency.Milliseconds(),
		Failures: d.failures, Fallback: d.spec.Fallback, Downgrade: slices.Clone(d.spec.Downgrade),
		DataAgeSec: -1,
	}
	if !d.lastSuccess.IsZero() {
		s.DataAgeSec = int64(now.Sub(d.lastSuccess).Seconds())
	}
	if d.breaker != Closed {
		s.OpenUntil = d.openUntil
	}
	if d.err != nil {
		s.Error, s.ErrorClass = d.err.Error(), Classify(d.err)
	}
	switch {
	case d.lastAttempt.IsZero():
		s.State = Unknown
	case d.err == nil && d.latency > d.spec.SlowAfter:
		s.State = Degraded
	case d.err == nil:
		s.State = Live
	case d.breaker == Closed:
		// Failing, but not yet often enough to stop trying.
		s.State = Degraded
	case !d.lastSuccess.IsZero() && now.Sub(d.lastSuccess) <= d.spec.MaxStale:
		s.State = Stale
	default:
		s.State = Offline
	}
	// A rehearsal says so: everything downstream -- the banner, the status
	// line, the monitoring page, the Telegram alert -- sees the outage the
	// fallbacks are being tested against, and the words that it is a test.
	if sim, ok := r.simulated(d.spec.Name); ok {
		s.State, s.Breaker, s.OpenUntil = Offline, "simulated", sim.Until
		s.Error, s.ErrorClass = "آزمایش تاب‌آوری: قطعی شبیه‌سازی‌شده تا "+sim.Until.Format("15:04"), "simulated"
	}
	return s
}
