package find

import (
	"net/http"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/Schayan-nk/docfind-messenger/internal/dependencies"
)

// PostgreSQL's three failure levels.
//
//	1  up: everything works.
//	2  briefly down: docfind runs from what it holds locally -- the document
//	   index, the report snapshots, the accounts snapshot, the mailbox -- and
//	   refuses the changes it could not record safely: accounts and
//	   permissions, report definitions, settings, anything that deletes.
//	3  down for longer than DOCFIND_PG_OFFLINE_AFTER (10 minutes): the same,
//	   announced on every page as EMERGENCY OFFLINE MODE, because by then it
//	   is not a blip anybody can wait out.
//
// Each move between levels is an audit event and a log line, written to the
// application log -- which survives the database being down -- as well as to
// the audit table when there is one to write to.

type pgLevel struct {
	mu    sync.Mutex
	level int
	since time.Time
}

// PGLevel is the current level and since when.
func (s *Server) PGLevel() (int, time.Time) {
	s.pg.mu.Lock()
	defer s.pg.mu.Unlock()
	if s.pg.level == 0 {
		return 1, time.Time{}
	}
	return s.pg.level, s.pg.since
}

// refusedWhileDown is what cannot be done while PostgreSQL is away: every
// change to accounts and permissions, and anything that deletes. Reads and
// searches go on.
var refusedWhileDown = []struct{ method, prefix string }{
	{"", "/api/users"},
	{"", "/api/permissions"},
	{"DELETE", "/"},
}

func refusedAtLevel(level int, r *http.Request) bool {
	if level < 2 || r.Method == http.MethodGet || r.Method == http.MethodHead {
		return false
	}
	for _, rule := range refusedWhileDown {
		if (rule.method == "" || rule.method == r.Method) && strings.HasPrefix(r.URL.Path, rule.prefix) {
			return true
		}
	}
	return false
}

// degradedGuard refuses, with a reason, the changes PostgreSQL's absence
// makes unsafe.
func (s *Server) degradedGuard(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		level, _ := s.PGLevel()
		if refusedAtLevel(level, r) {
			writeJSONError(w, http.StatusServiceUnavailable,
				`{"error":"پایگاه‌داده در دسترس نیست؛ این تغییر تا برگشتن آن ممکن نیست.","pg_level":`+strconv.Itoa(level)+`}`)
			return
		}
		next.ServeHTTP(w, r)
	})
}

// handleDBState is what every page asks, once a minute, to decide on a banner.
//
// With it, the status bar's other half: every dependency that is not
// answering, what people get instead, and which capabilities are off because
// of it -- "reports from the saved copy", "no new mail" -- so a person knows
// what they can and cannot do right now without opening the health page.
func (s *Server) handleDBState(w http.ResponseWriter, r *http.Request) {
	level, since := s.PGLevel()
	type down struct {
		Name     string   `json:"name"`
		Label    string   `json:"label"`
		State    string   `json:"state"`
		Fallback string   `json:"fallback,omitempty"`
		Lost     []string `json:"lost,omitempty"`
	}
	var degraded []down
	off := map[string]bool{}
	if s.deps != nil {
		for _, d := range s.deps.All() {
			if d.State != dependencies.Stale && d.State != dependencies.Offline {
				continue
			}
			degraded = append(degraded, down{d.Name, d.Label, string(d.State), d.Fallback, d.Downgrade})
			for _, c := range d.Downgrade {
				off[c] = true
			}
		}
	}
	lost := make([]string, 0, len(off))
	for c := range off {
		lost = append(lost, c)
	}
	slices.Sort(lost)
	writeJSON(w, map[string]any{"pg_level": level, "pg_since": since, "degraded": degraded, "capabilities_off": lost})
}
