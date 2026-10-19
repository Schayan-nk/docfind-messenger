package dependencies

import (
	"context"
	"errors"
	"time"
)

// clock is a settable time for the breaker's cooldowns.
type clock struct{ t time.Time }

func (c *clock) now() time.Time      { return c.t }
func (c *clock) add(d time.Duration) { c.t = c.t.Add(d) }

var errDown = errors.New("connection refused")

func fail(context.Context) error { return errDown }
func ok(context.Context) error   { return nil }
