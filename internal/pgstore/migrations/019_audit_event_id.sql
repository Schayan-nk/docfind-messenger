-- Every audit event now carries its own id (audit.Event.ID), given when it is
-- recorded.
--
-- WHY. Events the database could not take during an outage wait in a spool on
-- the server's disk and are written here when it is back (audit/spool.go). A
-- write whose answer was lost -- a timeout after the row went in -- is sent
-- again, and without an id of its own it would become a second row: an audit
-- trail that says somebody did a thing twice when they did it once. The
-- unique index makes the second copy a no-op (INSERT ... ON CONFLICT DO
-- NOTHING).
--
-- ONLY ADDS. Nullable, with no default, so no existing row is rewritten and
-- the append-only guard (009) is not touched; the rows from before this have
-- no id and need none. 5.23.0 runs with it present.

ALTER TABLE audit_events ADD COLUMN IF NOT EXISTS event_id TEXT;

CREATE UNIQUE INDEX IF NOT EXISTS audit_events_event_id
    ON audit_events (event_id) WHERE event_id IS NOT NULL;
