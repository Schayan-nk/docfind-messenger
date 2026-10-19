package find

import (
	"context"

	"github.com/Schayan-nk/docfind-messenger/internal/audit"
)

// auditChange records one administrative change.
func (s *Server) auditChange(actor, action, target string, detail map[string]any) {
	if s.audit == nil {
		return
	}
	s.audit.Record(context.Background(), audit.Event{
		Actor: actor, Action: action, Target: target, Result: "ok", Detail: detail,
	})
}
