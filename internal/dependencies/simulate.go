package dependencies

import (
	"time"
)

// Resilience test mode: an administrator can make the application treat one
// dependency as down for a few minutes, to see -- before it happens for real
// -- that the reports fall back to their copy, the assistant to its rules,
// the inbox to its local store, and the banner says so.
//
// A simulated outage is refused exactly the way an open circuit breaker is:
// Do returns an OpenError without calling the dependency, and Allow says no.
// Nothing is really cut and nothing about the real breaker changes, so when
// the simulation ends -- on its own after its time, or when ended -- the
// dependency is exactly as it was.

// Simulation is one simulated outage in force.
type Simulation struct {
	Name  string    `json:"name"`
	Label string    `json:"label"`
	Until time.Time `json:"until"`
	By    string    `json:"by"`
}

// simulated reports a live simulation for name, dropping an expired one.
// Called with r.mu held.
func (r *Registry) simulated(name string) (Simulation, bool) {
	s, ok := r.sims[name]
	if !ok {
		return Simulation{}, false
	}
	if !r.now().Before(s.Until) {
		delete(r.sims, name)
		return Simulation{}, false
	}
	return s, true
}
