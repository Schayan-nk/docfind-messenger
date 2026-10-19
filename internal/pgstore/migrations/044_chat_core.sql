-- Chat module, phase 1: people, chats, members, messages and the update log.
--
-- Idempotent (IF NOT EXISTS throughout) so a half-applied run can be re-run, and
-- reversible: migrations/down/044_chat_core.down.sql drops exactly these objects.
--
-- user ids are DocFind usernames (users.username is the primary key of the
-- account table). ON UPDATE CASCADE follows a rename; a deleted account keeps
-- its messages (sender_id goes NULL) but loses its memberships and profile.

CREATE TABLE IF NOT EXISTS chat_users_ext (
    user_id        TEXT PRIMARY KEY REFERENCES users(username) ON UPDATE CASCADE ON DELETE CASCADE,
    -- Optional public handle: 5-32 Latin letters, digits, underscore (checked in Go too).
    username       TEXT UNIQUE CHECK (username IS NULL OR username ~ '^[A-Za-z0-9_]{5,32}$'),
    bio            TEXT        NOT NULL DEFAULT '',
    avatar_file_id BIGINT,
    last_seen_at   TIMESTAMPTZ,
    privacy        JSONB       NOT NULL DEFAULT '{}'::jsonb,
    created_at     TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE TABLE IF NOT EXISTS chats (
    id                  BIGSERIAL PRIMARY KEY,
    type                TEXT NOT NULL CHECK (type IN ('private', 'group', 'supergroup', 'channel', 'saved', 'secret')),
    title               TEXT NOT NULL DEFAULT '',
    about               TEXT NOT NULL DEFAULT '',
    avatar_file_id      BIGINT,
    created_by          TEXT REFERENCES users(username) ON UPDATE CASCADE ON DELETE SET NULL,
    settings            JSONB NOT NULL DEFAULT '{}'::jsonb,
    is_forum            BOOLEAN NOT NULL DEFAULT FALSE,
    slow_mode_seconds   INTEGER NOT NULL DEFAULT 0,
    auto_delete_seconds INTEGER NOT NULL DEFAULT 0,
    -- One private chat per pair, one Saved Messages per person: the sorted pair
    -- ("a|b") or "saved|a". NULL for every other type.
    pair_key            TEXT UNIQUE,
    -- The chat's update counter (Telegram's pts). Every change to the chat takes
    -- the next value, inside the transaction that makes it.
    pts                 BIGINT NOT NULL DEFAULT 0,
    last_message_at     TIMESTAMPTZ,
    created_at          TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE INDEX IF NOT EXISTS chats_created_by_idx ON chats (created_by);
CREATE INDEX IF NOT EXISTS chats_last_message_idx ON chats (last_message_at DESC);

CREATE TABLE IF NOT EXISTS chat_members (
    chat_id       BIGINT NOT NULL REFERENCES chats(id) ON DELETE CASCADE,
    user_id       TEXT   NOT NULL REFERENCES users(username) ON UPDATE CASCADE ON DELETE CASCADE,
    role          TEXT   NOT NULL DEFAULT 'member' CHECK (role IN ('owner', 'admin', 'member', 'restricted', 'banned', 'left')),
    rights        JSONB  NOT NULL DEFAULT '{}'::jsonb,
    custom_title  TEXT   NOT NULL DEFAULT '',
    joined_at     TIMESTAMPTZ NOT NULL DEFAULT now(),
    muted_until   TIMESTAMPTZ,
    folder_ids    BIGINT[] NOT NULL DEFAULT '{}',
    pinned_order  INTEGER,
    archived      BOOLEAN NOT NULL DEFAULT FALSE,
    last_read_pts BIGINT  NOT NULL DEFAULT 0,
    last_read_at  TIMESTAMPTZ,
    PRIMARY KEY (chat_id, user_id)
);
CREATE INDEX IF NOT EXISTS chat_members_user_idx ON chat_members (user_id);

CREATE TABLE IF NOT EXISTS chat_messages (
    id                    BIGSERIAL PRIMARY KEY,
    chat_id               BIGINT NOT NULL REFERENCES chats(id) ON DELETE CASCADE,
    topic_id              BIGINT,
    pts                   BIGINT NOT NULL,
    sender_id             TEXT REFERENCES users(username) ON UPDATE CASCADE ON DELETE SET NULL,
    sender_chat_id        BIGINT,
    -- The client's UUIDv7 for this message: a resend after a lost answer finds
    -- the row it already made instead of making a second one.
    client_msg_id         UUID,
    kind                  TEXT NOT NULL DEFAULT 'text',
    text                  TEXT NOT NULL DEFAULT '',
    entities              JSONB NOT NULL DEFAULT '[]'::jsonb,
    reply_to_id           BIGINT,
    quote                 JSONB,
    fwd_from              JSONB,
    media_id              BIGINT,
    edited_at             TIMESTAMPTZ,
    deleted_at            TIMESTAMPTZ,
    silent                BOOLEAN NOT NULL DEFAULT FALSE,
    scheduled_for         TIMESTAMPTZ,
    self_destruct_seconds INTEGER,
    expires_at            TIMESTAMPTZ,
    views                 INTEGER NOT NULL DEFAULT 0,
    created_at            TIMESTAMPTZ NOT NULL DEFAULT now(),
    UNIQUE (chat_id, pts)
);
CREATE UNIQUE INDEX IF NOT EXISTS chat_messages_dedupe_idx ON chat_messages (chat_id, sender_id, client_msg_id) WHERE client_msg_id IS NOT NULL;
CREATE INDEX IF NOT EXISTS chat_messages_sender_idx ON chat_messages (sender_id);
CREATE INDEX IF NOT EXISTS chat_messages_chat_id_desc_idx ON chat_messages (chat_id, id DESC);
CREATE INDEX IF NOT EXISTS chat_messages_reply_idx ON chat_messages (reply_to_id) WHERE reply_to_id IS NOT NULL;

-- The getDifference log: one row per change, in pts order. A reconnecting
-- device asks for everything after the last pts it saw in each chat.
CREATE TABLE IF NOT EXISTS chat_updates (
    chat_id    BIGINT NOT NULL REFERENCES chats(id) ON DELETE CASCADE,
    pts        BIGINT NOT NULL,
    kind       TEXT   NOT NULL,
    payload    JSONB  NOT NULL DEFAULT '{}'::jsonb,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    PRIMARY KEY (chat_id, pts)
);

CREATE TABLE IF NOT EXISTS chat_drafts (
    user_id     TEXT   NOT NULL REFERENCES users(username) ON UPDATE CASCADE ON DELETE CASCADE,
    chat_id     BIGINT NOT NULL REFERENCES chats(id) ON DELETE CASCADE,
    topic_id    BIGINT NOT NULL DEFAULT 0,
    text        TEXT   NOT NULL DEFAULT '',
    entities    JSONB  NOT NULL DEFAULT '[]'::jsonb,
    reply_to_id BIGINT,
    updated_at  TIMESTAMPTZ NOT NULL DEFAULT now(),
    PRIMARY KEY (user_id, chat_id, topic_id)
);
CREATE INDEX IF NOT EXISTS chat_drafts_chat_idx ON chat_drafts (chat_id);

CREATE TABLE IF NOT EXISTS chat_blocks (
    user_id         TEXT NOT NULL REFERENCES users(username) ON UPDATE CASCADE ON DELETE CASCADE,
    blocked_user_id TEXT NOT NULL REFERENCES users(username) ON UPDATE CASCADE ON DELETE CASCADE,
    created_at      TIMESTAMPTZ NOT NULL DEFAULT now(),
    PRIMARY KEY (user_id, blocked_user_id)
);
CREATE INDEX IF NOT EXISTS chat_blocks_blocked_idx ON chat_blocks (blocked_user_id);

CREATE TABLE IF NOT EXISTS chat_contacts (
    user_id         TEXT NOT NULL REFERENCES users(username) ON UPDATE CASCADE ON DELETE CASCADE,
    contact_user_id TEXT NOT NULL REFERENCES users(username) ON UPDATE CASCADE ON DELETE CASCADE,
    display_name    TEXT NOT NULL DEFAULT '',
    PRIMARY KEY (user_id, contact_user_id)
);
CREATE INDEX IF NOT EXISTS chat_contacts_contact_idx ON chat_contacts (contact_user_id);
