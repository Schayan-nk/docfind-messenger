package find

import (
	"net/http"
	"runtime"
	"time"

	"github.com/Schayan-nk/docfind-messenger/internal/obs"
)

// What the server is doing, for an administrator: the answer to "why is it
// slow" that did not exist while stdout was thrown away.
//
//   routes  -- every route's p50/p95/p99 over its newest 512 requests, slowest
//              p95 first, with 4xx/5xx counts since start.
//   jobs    -- every background job's last run, duration, items, error, and
//              runs and failures since start. A job whose last_ok keeps getting
//              older has stopped, which is the one failure a job cannot report
//              about itself.
//   runtime -- goroutines, memory, garbage collection and uptime, so a leak
//              shows as a number that only ever goes up.
//
// Read-only and figures only: no paths, no users, no query text. The request
// log has the detail; this is the summary a person reads first.

var processStart = time.Now()

func (s *Server) handleMetrics(w http.ResponseWriter, r *http.Request) {
	var ms runtime.MemStats
	runtime.ReadMemStats(&ms)
	writeJSON(w, map[string]any{
		"since":  s.latency.Since(),
		"routes": s.latency.Snapshot(),
		"jobs":   obs.DefaultJobs().Snapshot(),
		"runtime": map[string]any{
			"version":       buildVersion,
			"sha256":        binaryHash,
			"uptime_s":      int64(time.Since(processStart).Seconds()),
			"goroutines":    runtime.NumGoroutine(),
			"heap_mb":       float64(ms.HeapAlloc) / (1 << 20),
			"sys_mb":        float64(ms.Sys) / (1 << 20),
			"gc_cycles":     ms.NumGC,
			"gc_pause_ms":   float64(ms.PauseTotalNs) / 1e6,
			"go":            runtime.Version(),
			"cpus":          runtime.NumCPU(),
			"log_directory": obs.Default().Dir(),
		},
	})
}
