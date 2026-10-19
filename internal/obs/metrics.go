package obs

import (
	"slices"
	"sort"
	"sync"
	"time"
)

// Latency keeps recent response times per route, for p50, p95 and p99.
//
// THE LAST N SAMPLES, NOT A HISTOGRAM OF ALL TIME. A percentile over
// everything since start-up is dominated by the first hour -- the cold index,
// the empty caches -- and stops moving just when somebody wants to know
// whether today is slower than yesterday. The newest 512 requests per route
// answer "how is it now", which is the question.
//
// Exact rather than estimated: 512 durations sort in microseconds, and an
// estimate is one more thing to doubt when the number looks wrong.
//
// Nil-safe, so a server built without one records nothing and breaks nothing.
type Latency struct {
	mu     sync.Mutex
	window int
	routes map[string]*routeSamples
	since  time.Time
}

type routeSamples struct {
	ring  []time.Duration
	next  int
	full  bool
	count int64
	err4  int64
	err5  int64
	max   time.Duration
}

// maxRoutes bounds the label set. Labels are registered route patterns, of
// which there are about 140, plus one "(no route)" per method; the ceiling is
// there so that a future change which let a raw path through could not grow
// this without limit.
const maxRoutes = 1000

// NewLatency keeps window samples per route; 512 when window <= 0.
func NewLatency(window int) *Latency {
	if window <= 0 {
		window = 512
	}
	return &Latency{window: window, routes: map[string]*routeSamples{}, since: time.Now()}
}

// Observe records one finished request.
func (l *Latency) Observe(route string, status int, d time.Duration) {
	if l == nil {
		return
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	rs, ok := l.routes[route]
	if !ok {
		if len(l.routes) >= maxRoutes {
			route = "(other)"
			rs, ok = l.routes[route]
		}
		if !ok {
			rs = &routeSamples{ring: make([]time.Duration, l.window)}
			l.routes[route] = rs
		}
	}
	rs.ring[rs.next] = d
	rs.next++
	if rs.next == len(rs.ring) {
		rs.next, rs.full = 0, true
	}
	rs.count++
	switch {
	case status >= 500:
		rs.err5++
	case status >= 400:
		rs.err4++
	}
	if d > rs.max {
		rs.max = d
	}
}

// RouteStats is one route's figures.
type RouteStats struct {
	Route   string  `json:"route"`
	Count   int64   `json:"count"`   // since start
	Err4xx  int64   `json:"err_4xx"` // since start
	Err5xx  int64   `json:"err_5xx"` // since start
	Samples int     `json:"samples"` // in the window the percentiles use
	P50     float64 `json:"p50_ms"`
	P95     float64 `json:"p95_ms"`
	P99     float64 `json:"p99_ms"`
	Max     float64 `json:"max_ms"` // since start
}

// Snapshot returns every route's figures, slowest p95 first.
func (l *Latency) Snapshot() []RouteStats {
	if l == nil {
		return nil
	}
	// Copied under the lock, sorted outside it: sorting 140 windows of 512 is
	// the only part of this that takes any time, and requests should not
	// queue behind somebody opening the Security Center.
	type copied struct {
		stats   RouteStats
		samples []time.Duration
	}
	l.mu.Lock()
	all := make([]copied, 0, len(l.routes))
	for route, rs := range l.routes {
		n := rs.next
		if rs.full {
			n = len(rs.ring)
		}
		all = append(all, copied{
			stats: RouteStats{
				Route: route, Count: rs.count, Err4xx: rs.err4, Err5xx: rs.err5,
				Samples: n, Max: ms(rs.max),
			},
			samples: slices.Clone(rs.ring[:n]),
		})
	}
	l.mu.Unlock()

	out := make([]RouteStats, 0, len(all))
	for _, c := range all {
		out = append(out, fillPercentiles(c.stats, c.samples))
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].P95 != out[j].P95 {
			return out[i].P95 > out[j].P95
		}
		return out[i].Route < out[j].Route
	})
	return out
}

func fillPercentiles(rs RouteStats, samples []time.Duration) RouteStats {
	if len(samples) == 0 {
		rs.P50, rs.P95, rs.P99 = 0, 0, 0
		return rs
	}
	slices.Sort(samples)
	rs.P50 = ms(percentile(samples, 0.50))
	rs.P95 = ms(percentile(samples, 0.95))
	rs.P99 = ms(percentile(samples, 0.99))
	return rs
}

// percentile is the nearest-rank percentile of sorted samples.
//
// Nearest-rank rather than interpolated: every figure reported is a duration
// some real request actually took, which is the property wanted when the
// answer is going to be compared with a complaint.
func percentile(sorted []time.Duration, p float64) time.Duration {
	if len(sorted) == 0 {
		return 0
	}
	rank := int(p*float64(len(sorted))+0.999999) - 1
	rank = min(max(rank, 0), len(sorted)-1)
	return sorted[rank]
}

// Since is when recording began.
func (l *Latency) Since() time.Time {
	if l == nil {
		return time.Time{}
	}
	return l.since
}
