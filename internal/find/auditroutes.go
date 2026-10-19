package find

import (
	"context"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/Schayan-nk/docfind-messenger/internal/audit"
	"github.com/Schayan-nk/docfind-messenger/internal/pgstore"
)

// The audit trail's reading side. Recording happens in audit.Middleware for
// every state-changing request; handlers add only the target, with
// audit.Note.

// auditStore is the part of pgstore.Audit the routes use.
type auditStore interface {
	audit.Sink
	List(ctx context.Context, q pgstore.AuditQuery) ([]pgstore.AuditRow, error)
}

// UseAudit stores the trail in st as well as in the log.
func (s *Server) UseAudit(st auditStore) {
	s.auditStore = st
	s.audit.UseSink(st)
}

// UseAuditSpool keeps the events the database refuses in the file at path
// until it takes them (audit/spool.go), and looks for it coming back every
// minute: an outage leaves no hole in the trail an administrator searches.
func (s *Server) UseAuditSpool(ctx context.Context, path string) {
	s.audit.UseSpool(audit.OpenSpool(path))
	s.audit.StartSpoolReplay(ctx, time.Minute)
}

// handleAuditList answers GET /api/audit.
//
//	?actor=sadeghi  ?target=doc:Contracts/  ?action=DELETE%20/api/users
//	?result=denied   ?since=2026-09-01  ?until=2026-09-23  ?before=<id>  ?limit=
//
// Dates are days in the server's calendar; until is exclusive. The page walks
// back with ?before= set to the last id it was given.
func (s *Server) handleAuditList(w http.ResponseWriter, r *http.Request) {
	if s.auditStore == nil {
		writeJSONError(w, http.StatusServiceUnavailable,
			`{"error":"گزارش رویدادها به پایگاه داده نیاز دارد و پایگاه داده تنظیم نشده است."}`)
		return
	}
	q := r.URL.Query()
	aq := pgstore.AuditQuery{
		Actor:  strings.TrimSpace(q.Get("actor")),
		Target: strings.TrimSpace(q.Get("target")),
		Action: strings.TrimSpace(q.Get("action")),
		Result: strings.TrimSpace(q.Get("result")),
	}
	if v := strings.TrimSpace(q.Get("since")); v != "" {
		t, err := time.ParseInLocation("2006-01-02", v, time.Local)
		if err != nil {
			writeJSONError(w, http.StatusBadRequest, `{"error":"since must be a date: 2026-09-01"}`)
			return
		}
		aq.Since = t
	}
	if v := strings.TrimSpace(q.Get("until")); v != "" {
		t, err := time.ParseInLocation("2006-01-02", v, time.Local)
		if err != nil {
			writeJSONError(w, http.StatusBadRequest, `{"error":"until must be a date: 2026-09-23"}`)
			return
		}
		aq.Until = t
	}
	if v := strings.TrimSpace(q.Get("before")); v != "" {
		n, err := strconv.ParseInt(v, 10, 64)
		if err != nil || n <= 0 {
			writeJSONError(w, http.StatusBadRequest, `{"error":"before must be an event id"}`)
			return
		}
		aq.Before = n
	}
	if v := strings.TrimSpace(q.Get("limit")); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 {
			aq.Limit = n
		}
	}
	rows, err := s.auditStore.List(r.Context(), aq)
	if err != nil {
		logReportError("audit list", err)
		writeJSONError(w, http.StatusBadGateway, `{"error":"گزارش رویدادها خوانده نشد."}`)
		return
	}
	// Reading the trail is itself not recorded -- it is a GET -- but who
	// looked is in the request log.
	writeJSON(w, map[string]any{"events": rows})
}
