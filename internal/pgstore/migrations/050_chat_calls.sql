-- Messenger phase 7: one-to-one calls (docs/chat/SPEC.md 6.8, 5.4 calls and
-- call_participants). A call is a row; each person in it is a participant row
-- (their part: when they joined and left, their rating). Group calls (phase 8)
-- use the same two tables with type 'group'. The media never touches the
-- server; only who called whom, when, for how long and how it ended.
--
-- Idempotent and reversible: migrations/down/050_chat_calls.down.sql.
CREATE TABLE IF NOT EXISTS chat_calls (
    id                  BIGSERIAL PRIMARY KEY,
    type                TEXT NOT NULL DEFAULT 'p2p' CHECK (type IN ('p2p', 'group')),
    chat_id             BIGINT REFERENCES chats(id) ON DELETE SET NULL,
    started_by          TEXT REFERENCES users(username) ON UPDATE CASCADE ON DELETE SET NULL,
    is_video            BOOLEAN NOT NULL DEFAULT false,
    started_at          TIMESTAMPTZ NOT NULL DEFAULT now(),
    answered_at         TIMESTAMPTZ,
    connected_at        TIMESTAMPTZ,
    ended_at            TIMESTAMPTZ,
    end_reason          TEXT,
    recording_media_id  BIGINT
);
CREATE INDEX IF NOT EXISTS chat_calls_chat_idx    ON chat_calls (chat_id, id DESC);
CREATE INDEX IF NOT EXISTS chat_calls_started_idx ON chat_calls (started_by);
-- A call left open by a stopped server is closed at start-up.
CREATE INDEX IF NOT EXISTS chat_calls_open_idx    ON chat_calls (id) WHERE ended_at IS NULL;

CREATE TABLE IF NOT EXISTS chat_call_participants (
    call_id         BIGINT NOT NULL REFERENCES chat_calls(id) ON DELETE CASCADE,
    user_id         TEXT   NOT NULL REFERENCES users(username) ON UPDATE CASCADE ON DELETE CASCADE,
    role            TEXT   NOT NULL DEFAULT 'callee' CHECK (role IN ('caller', 'callee', 'member')),
    joined_at       TIMESTAMPTZ,
    left_at         TIMESTAMPTZ,
    muted           BOOLEAN NOT NULL DEFAULT false,
    video_on        BOOLEAN NOT NULL DEFAULT false,
    hand_raised_at  TIMESTAMPTZ,
    rating          SMALLINT CHECK (rating BETWEEN 1 AND 5),
    rating_note     TEXT NOT NULL DEFAULT '',
    hidden          BOOLEAN NOT NULL DEFAULT false,
    PRIMARY KEY (call_id, user_id)
);
CREATE INDEX IF NOT EXISTS chat_call_participants_user_idx ON chat_call_participants (user_id, call_id DESC);
