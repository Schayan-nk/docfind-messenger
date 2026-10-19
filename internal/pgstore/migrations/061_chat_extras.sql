-- Chat module, phase 11: polls, scheduled messages, locations, sticker and GIF
-- packs. Idempotent (IF NOT EXISTS throughout); the down script is
-- migrations/down/061_chat_extras.down.sql.

-- What a message of kind poll, location, sticker or gif carries besides its
-- text: the poll's definition, the point, the pack item. NULL for other kinds.
ALTER TABLE chat_messages ADD COLUMN IF NOT EXISTS extra JSONB;

CREATE TABLE IF NOT EXISTS chat_polls (
    id          BIGSERIAL PRIMARY KEY,
    chat_id     BIGINT  NOT NULL REFERENCES chats(id) ON DELETE CASCADE,
    message_id  BIGINT  REFERENCES chat_messages(id) ON DELETE CASCADE,
    created_by  TEXT    REFERENCES users(username) ON UPDATE CASCADE ON DELETE SET NULL,
    question    TEXT    NOT NULL,
    anonymous   BOOLEAN NOT NULL DEFAULT TRUE,
    multiple    BOOLEAN NOT NULL DEFAULT FALSE,
    quiz        BOOLEAN NOT NULL DEFAULT FALSE,
    correct     SMALLINT,          -- quiz only: the right option
    explanation TEXT    NOT NULL DEFAULT '',
    closes_at   TIMESTAMPTZ,       -- closes by itself at this time
    closed_at   TIMESTAMPTZ,
    created_at  TIMESTAMPTZ NOT NULL DEFAULT now(),
    CHECK (NOT quiz OR (correct IS NOT NULL AND NOT multiple))
);
CREATE INDEX IF NOT EXISTS chat_polls_chat_idx ON chat_polls (chat_id);
CREATE INDEX IF NOT EXISTS chat_polls_message_idx ON chat_polls (message_id);
CREATE INDEX IF NOT EXISTS chat_polls_created_by_idx ON chat_polls (created_by);
CREATE INDEX IF NOT EXISTS chat_polls_closing_idx ON chat_polls (closes_at) WHERE closed_at IS NULL AND closes_at IS NOT NULL;

CREATE TABLE IF NOT EXISTS chat_poll_options (
    poll_id BIGINT   NOT NULL REFERENCES chat_polls(id) ON DELETE CASCADE,
    idx     SMALLINT NOT NULL CHECK (idx BETWEEN 0 AND 9),
    text    TEXT     NOT NULL,
    PRIMARY KEY (poll_id, idx)
);

CREATE TABLE IF NOT EXISTS chat_poll_votes (
    poll_id  BIGINT   NOT NULL REFERENCES chat_polls(id) ON DELETE CASCADE,
    user_id  TEXT     NOT NULL REFERENCES users(username) ON UPDATE CASCADE ON DELETE CASCADE,
    idx      SMALLINT NOT NULL,
    voted_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    PRIMARY KEY (poll_id, user_id, idx)
);
CREATE INDEX IF NOT EXISTS chat_poll_votes_user_idx ON chat_poll_votes (user_id);

-- A message waiting for its time (or for the other person to come online).
-- It becomes an ordinary message, through the ordinary checks, when it is sent.
CREATE TABLE IF NOT EXISTS chat_scheduled (
    id            BIGSERIAL PRIMARY KEY,
    chat_id       BIGINT  NOT NULL REFERENCES chats(id) ON DELETE CASCADE,
    sender        TEXT    NOT NULL REFERENCES users(username) ON UPDATE CASCADE ON DELETE CASCADE,
    client_msg_id UUID    NOT NULL,
    topic_id      BIGINT  NOT NULL DEFAULT 0,
    text          TEXT    NOT NULL,
    entities      JSONB   NOT NULL DEFAULT '[]'::jsonb,
    reply_to_id   BIGINT,
    silent        BOOLEAN NOT NULL DEFAULT FALSE,
    send_at       TIMESTAMPTZ,
    when_online   BOOLEAN NOT NULL DEFAULT FALSE,
    created_at    TIMESTAMPTZ NOT NULL DEFAULT now(),
    UNIQUE (sender, client_msg_id),
    CHECK (when_online OR send_at IS NOT NULL)
);
CREATE INDEX IF NOT EXISTS chat_scheduled_due_idx ON chat_scheduled (send_at) WHERE NOT when_online;
CREATE INDEX IF NOT EXISTS chat_scheduled_online_idx ON chat_scheduled (chat_id) WHERE when_online;
CREATE INDEX IF NOT EXISTS chat_scheduled_chat_sender_idx ON chat_scheduled (chat_id, sender);

-- Sticker and GIF packs, managed by DocFind administrators and open to every
-- signed-in person. Files are ordinary chat_media rows, already checked.
CREATE TABLE IF NOT EXISTS chat_packs (
    id         BIGSERIAL PRIMARY KEY,
    kind       TEXT NOT NULL CHECK (kind IN ('sticker', 'gif')),
    title      TEXT NOT NULL,
    created_by TEXT REFERENCES users(username) ON UPDATE CASCADE ON DELETE SET NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE INDEX IF NOT EXISTS chat_packs_created_by_idx ON chat_packs (created_by);

CREATE TABLE IF NOT EXISTS chat_pack_items (
    id         BIGSERIAL PRIMARY KEY,
    pack_id    BIGINT NOT NULL REFERENCES chat_packs(id) ON DELETE CASCADE,
    media_id   BIGINT NOT NULL REFERENCES chat_media(id),
    format     TEXT   NOT NULL CHECK (format IN ('webp', 'tgs', 'webm', 'gif', 'mp4')),
    emoji      TEXT   NOT NULL DEFAULT '',
    keywords   TEXT   NOT NULL DEFAULT '', -- as typed, Persian or English
    search     TEXT   NOT NULL DEFAULT '', -- normalised keywords and emoji, for the GIF search
    width      INTEGER,
    height     INTEGER,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE INDEX IF NOT EXISTS chat_pack_items_pack_idx ON chat_pack_items (pack_id);
CREATE INDEX IF NOT EXISTS chat_pack_items_media_idx ON chat_pack_items (media_id);
