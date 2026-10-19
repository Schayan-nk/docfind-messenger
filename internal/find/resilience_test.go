package find

import (
	"encoding/json"
	"testing"

	"github.com/Schayan-nk/docfind-messenger/internal/dependencies"
)

func resilienceState(t *testing.T, s *Server, token, method, path, body string) (int, []dependencies.Simulation) {
	t.Helper()
	rec := asUser(t, s, method, path, token, body)
	var got struct {
		Simulations []dependencies.Simulation `json:"simulations"`
	}
	json.Unmarshal(rec.Body.Bytes(), &got)
	return rec.Code, got.Simulations
}
