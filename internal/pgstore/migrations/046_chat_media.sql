-- Chat module, phase 3: media and files, resumable uploads, link previews,
-- albums and DocFind document cards. Idempotent; reversed by
-- migrations/down/046_chat_media.down.sql.

-- One row per uploaded file. The bytes live in the chat media folder under
-- their SHA-256, never under the name the sender gave them.
CREATE TABLE IF NOT EXISTS chat_media (
    id            BIGSERIAL PRIMARY KEY,
    sha256        TEXT    NOT NULL,
    mime          TEXT    NOT NULL,
    size          BIGINT  NOT NULL,
    kind          TEXT    NOT NULL,           -- photo | video | round | voice | file | preview
    width         INTEGER,
    height        INTEGER,
    duration_ms   INTEGER,
    waveform      BYTEA,                      -- voice: up to 100 levels, 0..31
    thumb_sha256  TEXT,                       -- a small JPEG: photo thumbnail or video poster
    mini          BYTEA,                      -- a tiny JPEG for the blur-up, sent inline
    original_name TEXT    NOT NULL DEFAULT '',
    uploaded_by   TEXT REFERENCES users(username) ON UPDATE CASCADE ON DELETE SET NULL,
    ocr_text      TEXT,
    created_at    TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE INDEX IF NOT EXISTS chat_media_sha_idx ON chat_media (sha256);
CREATE INDEX IF NOT EXISTS chat_media_uploader_idx ON chat_media (uploaded_by);

-- An upload in progress, in chunks; resumable after a lost connection.
CREATE TABLE IF NOT EXISTS chat_uploads (
    id          UUID   PRIMARY KEY,
    user_id     TEXT   NOT NULL REFERENCES users(username) ON UPDATE CASCADE ON DELETE CASCADE,
    name        TEXT   NOT NULL DEFAULT '',
    size        BIGINT NOT NULL,
    received    BIGINT NOT NULL DEFAULT 0,
    created_at  TIMESTAMPTZ NOT NULL DEFAULT now(),
    expires_at  TIMESTAMPTZ NOT NULL
);
CREATE INDEX IF NOT EXISTS chat_uploads_user_idx ON chat_uploads (user_id);
CREATE INDEX IF NOT EXISTS chat_uploads_expiry_idx ON chat_uploads (expires_at);

-- Link previews fetched by the server, kept a week per address.
CREATE TABLE IF NOT EXISTS chat_link_previews (
    url        TEXT PRIMARY KEY,
    preview    JSONB NOT NULL,
    fetched_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

ALTER TABLE chat_messages ADD COLUMN IF NOT EXISTS album_cid UUID;
ALTER TABLE chat_messages ADD COLUMN IF NOT EXISTS preview   JSONB;
ALTER TABLE chat_messages ADD COLUMN IF NOT EXISTS doc_ref   TEXT;
CREATE INDEX IF NOT EXISTS chat_messages_media_idx ON chat_messages (media_id) WHERE media_id IS NOT NULL;
