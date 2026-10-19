package find

import (
	"context"
)

// The periodic report by email: abandoned orders first -- the report the
// company cares most about -- then the business analysis of the week, month
// or season just ended, and the attendance of the same days, sent on a set
// day to the addresses an administrator gives on the settings page, from the
// office mailbox docfind already sends its sign-in codes from. Or sent now,
// for any range, to one address.
//
// Sent once a period and never twice: the period last sent is kept in the
// database, so a restart or a clock that ticks twice cannot send it again.
// Sent late rather than not at all: if the server was down on the day, the
// first check after it is back sends it.

type appSettingsStore interface {
	Get(ctx context.Context, key string, v any) error
	Set(ctx context.Context, key string, v any, by string) error
}

// ---------------------------------------------------------------- routes
