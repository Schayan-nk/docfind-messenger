-- Accounts, preferences and pictures.
--
-- THE USERNAME IS THE KEY, not a surrogate integer. It already is the key
-- everywhere else in this application: the accounts file, the JWT subject and
-- the avatar filename are all keyed on it. Adding an id would create a second identity
-- to keep in step with the first, and the first is the one every other system
-- already agrees on.
CREATE TABLE IF NOT EXISTS users (
    username       TEXT PRIMARY KEY,
    display_name   TEXT        NOT NULL DEFAULT '',
    -- New with the database. Accounts have never carried an address, so every
    -- imported row has an empty string here and nothing depends on it being
    -- set.
    email          TEXT        NOT NULL DEFAULT '',
    -- PBKDF2-HMAC-SHA256, 600,000 iterations, scheme tag "pbkdf2-sha256".
    -- Stored exactly as internal/auth/password.go writes it: this column is
    -- opaque to the database and re-deriving anything here would lock the
    -- account out.
    password_hash  TEXT        NOT NULL,
    role           TEXT        NOT NULL,
    disabled       BOOLEAN     NOT NULL DEFAULT FALSE,
    created_at     TIMESTAMPTZ NOT NULL DEFAULT now(),
    -- Nullable, because "never signed in" is a real state and 0001-01-01 is a
    -- date pretending to be one.
    last_login     TIMESTAMPTZ,
    -- What makes an issued token revocable: every token carries the version
    -- current when it was minted, so bumping this invalidates all of them at
    -- once. Without it, disabling an account would leave that person signed
    -- in until their token expired on its own.
    token_version  INTEGER     NOT NULL DEFAULT 0,
    avatar_ext     TEXT        NOT NULL DEFAULT '',
    english_name   TEXT        NOT NULL DEFAULT ''
);

-- Sign-in is case-insensitive, so two rows differing only in case would be a
-- second account nobody can reach and whose password still works.
CREATE UNIQUE INDEX IF NOT EXISTS users_username_lower ON users (lower(username));

-- JSONB rather than three columns. There are exactly three preferences today
-- -- font scale, density, reduced motion -- and adding a fourth should be a
-- line of Go, not a migration and a deploy.
CREATE TABLE IF NOT EXISTS user_prefs (
    username TEXT  PRIMARY KEY REFERENCES users(username) ON DELETE CASCADE,
    prefs    JSONB NOT NULL DEFAULT '{}'::jsonb
);

-- The bytes, not a path, and that was asked for explicitly. The picture is
-- part of the account, so restoring the database restores the faces too rather
-- than leaving a staff list of broken images pointing at files that were never
-- in the backup.
CREATE TABLE IF NOT EXISTS avatars (
    username     TEXT        PRIMARY KEY REFERENCES users(username) ON DELETE CASCADE,
    bytes        BYTEA       NOT NULL,
    content_type TEXT        NOT NULL,
    updated_at   TIMESTAMPTZ NOT NULL DEFAULT now()
);

-- Who changed what. Permission and credential changes are precisely the events
-- worth a trail, and a trail added later cannot answer a question about last
-- month. Nothing writes to this yet -- its first writer is the permission work
-- -- but the table costs nothing empty and cannot be backfilled.
CREATE TABLE IF NOT EXISTS audit (
    id     BIGSERIAL   PRIMARY KEY,
    at     TIMESTAMPTZ NOT NULL DEFAULT now(),
    actor  TEXT        NOT NULL,
    action TEXT        NOT NULL,
    target TEXT        NOT NULL DEFAULT '',
    detail TEXT        NOT NULL DEFAULT ''
);

-- Newest first is how an audit trail is read.
CREATE INDEX IF NOT EXISTS audit_at ON audit (at DESC);
