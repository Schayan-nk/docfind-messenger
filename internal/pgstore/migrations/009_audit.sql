-- The audit trail: who did what to what, and whether it worked.
--
-- WHY A TABLE AND NOT ONLY THE LOG. The application log already carries every
-- audit event as a line of kind "audit", and that copy survives the database
-- being down. What the log cannot do is be searched by an administrator from
-- a page ("everything z.moradi changed last week", "who deleted the Nikan
-- folder"), or refuse to be edited. This table does both.
--
-- APPEND-ONLY, ENFORCED HERE rather than by the application's good manners:
-- the trigger below refuses every UPDATE, and refuses a DELETE of any row
-- younger than 365 days. docfind itself has no code path that changes a row,
-- and if one is ever written by mistake, or a compromised session tries to
-- tidy up after itself, the database says no. Rows older than a year may be
-- pruned, so the table does not grow for ever.
--
-- NEVER STORED: passwords, keys, tokens, session identifiers, request bodies,
-- document contents, AI prompts. A target NAMES an object ("user:sadeghi",
-- "doc:Contracts/x.docx"); detail holds names and counts.
CREATE TABLE IF NOT EXISTS audit_events (
    id         BIGSERIAL   PRIMARY KEY,
    at         TIMESTAMPTZ NOT NULL DEFAULT now(),
    actor      TEXT        NOT NULL DEFAULT '',
    action     TEXT        NOT NULL,
    target     TEXT        NOT NULL DEFAULT '',
    result     TEXT        NOT NULL,
    status     INTEGER     NOT NULL DEFAULT 0,
    ip         TEXT        NOT NULL DEFAULT '',
    user_agent TEXT        NOT NULL DEFAULT '',
    request_id TEXT        NOT NULL DEFAULT '',
    detail     JSONB
);

CREATE INDEX IF NOT EXISTS audit_events_at        ON audit_events (at DESC);
CREATE INDEX IF NOT EXISTS audit_events_actor_at  ON audit_events (actor, at DESC);
CREATE INDEX IF NOT EXISTS audit_events_target_at ON audit_events (target, at DESC);

CREATE OR REPLACE FUNCTION audit_events_guard() RETURNS trigger AS $$
BEGIN
    IF TG_OP = 'UPDATE' THEN
        RAISE EXCEPTION 'audit_events is append-only: rows cannot be changed';
    END IF;
    IF TG_OP = 'DELETE' AND OLD.at > now() - interval '365 days' THEN
        RAISE EXCEPTION 'audit_events keeps a year: row % is too recent to delete', OLD.id;
    END IF;
    RETURN OLD;
END;
$$ LANGUAGE plpgsql;

DROP TRIGGER IF EXISTS audit_events_guard ON audit_events;
CREATE TRIGGER audit_events_guard
    BEFORE UPDATE OR DELETE ON audit_events
    FOR EACH ROW EXECUTE FUNCTION audit_events_guard();

-- TRUNCATE skips row triggers, so it gets a refusal of its own.
CREATE OR REPLACE FUNCTION audit_events_no_truncate() RETURNS trigger AS $$
BEGIN
    RAISE EXCEPTION 'audit_events cannot be truncated';
END;
$$ LANGUAGE plpgsql;

DROP TRIGGER IF EXISTS audit_events_no_truncate ON audit_events;
CREATE TRIGGER audit_events_no_truncate
    BEFORE TRUNCATE ON audit_events
    FOR EACH STATEMENT EXECUTE FUNCTION audit_events_no_truncate();

-- THE LIMIT OF THIS, stated plainly: the docfind role owns the table, and an
-- owner can drop a trigger. These guards stop the application -- and anybody
-- who has only the application's reach, a stolen session included -- from
-- rewriting history. They do not stop somebody holding the database password
-- and a psql prompt; the nightly dump, kept elsewhere, is the answer to that.
