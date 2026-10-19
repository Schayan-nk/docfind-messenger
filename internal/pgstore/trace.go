package pgstore

import (
	"context"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/Schayan-nk/docfind-messenger/internal/obs"
)

// queryTracer times every PostgreSQL query, in two ways.
//
//  1. It charges the time to the request that caused it (obs.Track), so the
//     access line for a slow page says how much of it was the database.
//  2. A query slower than the threshold is logged on its own, with its
//     STATEMENT TEXT and never its arguments. The text is this program's own
//     SQL; the arguments are where names, numbers and documents live. The
//     line is what somebody needs to run EXPLAIN (ANALYZE, BUFFERS) on.
//
// One hook in the pool's configuration, so every query in this package is
// covered -- including the ones added next month.
type queryTracer struct {
	slow time.Duration
}

type traceKey struct{}

type traceState struct {
	start time.Time
	stop  func()
	sql   string
}

// slowQueryThreshold is DOCFIND_SLOW_QUERY_MS, 500 ms by default.
func slowQueryThreshold() time.Duration {
	if n, err := strconv.Atoi(strings.TrimSpace(os.Getenv("DOCFIND_SLOW_QUERY_MS"))); err == nil && n > 0 {
		return time.Duration(n) * time.Millisecond
	}
	return 500 * time.Millisecond
}

func (t queryTracer) TraceQueryStart(ctx context.Context, _ *pgx.Conn, data pgx.TraceQueryStartData) context.Context {
	return context.WithValue(ctx, traceKey{}, &traceState{
		start: time.Now(), stop: obs.Track(ctx, obs.DB), sql: data.SQL,
	})
}

func (t queryTracer) TraceQueryEnd(ctx context.Context, _ *pgx.Conn, data pgx.TraceQueryEndData) {
	st, ok := ctx.Value(traceKey{}).(*traceState)
	if !ok {
		return
	}
	st.stop()
	d := time.Since(st.start)
	if t.slow <= 0 || d < t.slow {
		return
	}
	f := obs.Fields{
		"db":         "postgresql",
		"ms":         float64(d.Microseconds()) / 1000,
		"sql":        compactSQL(st.sql, 200),
		"request_id": obs.RequestID(ctx),
	}
	if data.Err != nil {
		f["error"] = data.Err.Error()
	}
	obs.Default().Write("slow_query", "warn", f)
}

// compactSQL folds a statement's whitespace so it fits on one log line.
func compactSQL(s string, n int) string {
	s = strings.Join(strings.Fields(s), " ")
	if len(s) > n {
		s = s[:n] + "…"
	}
	return s
}
