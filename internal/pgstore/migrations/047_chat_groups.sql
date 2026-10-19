-- Chat module, phase 4: groups, supergroups, forum topics, channels, admin
-- rights, invite links, join requests and the admin log. Idempotent;
-- reversed by migrations/down/047_chat_groups.down.sql.

-- Forum topics (a supergroup with is_forum). Topic 0 is "General" and has no
-- row; every other topic is one row here.
CREATE TABLE IF NOT EXISTS chat_topics (
    id              BIGSERIAL PRIMARY KEY,
    chat_id         BIGINT  NOT NULL REFERENCES chats(id) ON DELETE CASCADE,
    title           TEXT    NOT NULL,
    icon            TEXT    NOT NULL DEFAULT '',
    color           INTEGER NOT NULL DEFAULT 0,
    closed          BOOLEAN NOT NULL DEFAULT FALSE,
    pinned          BOOLEAN NOT NULL DEFAULT FALSE,
    created_by      TEXT REFERENCES users(username) ON UPDATE CASCADE ON DELETE SET NULL,
    created_at      TIMESTAMPTZ NOT NULL DEFAULT now(),
    last_message_at TIMESTAMPTZ
);
CREATE INDEX IF NOT EXISTS chat_topics_chat_idx ON chat_topics (chat_id);

-- Where each member has read each topic to (the chat-wide position stays in
-- chat_members for chats without topics).
CREATE TABLE IF NOT EXISTS chat_topic_reads (
    chat_id       BIGINT NOT NULL REFERENCES chats(id) ON DELETE CASCADE,
    topic_id      BIGINT NOT NULL,
    user_id       TEXT   NOT NULL REFERENCES users(username) ON UPDATE CASCADE ON DELETE CASCADE,
    last_read_pts BIGINT NOT NULL DEFAULT 0,
    PRIMARY KEY (chat_id, topic_id, user_id)
);

CREATE TABLE IF NOT EXISTS chat_invite_links (
    id                BIGSERIAL PRIMARY KEY,
    chat_id           BIGINT  NOT NULL REFERENCES chats(id) ON DELETE CASCADE,
    token             TEXT    NOT NULL UNIQUE,
    name              TEXT    NOT NULL DEFAULT '',
    created_by        TEXT REFERENCES users(username) ON UPDATE CASCADE ON DELETE SET NULL,
    expires_at        TIMESTAMPTZ,
    usage_limit       INTEGER NOT NULL DEFAULT 0,
    usage_count       INTEGER NOT NULL DEFAULT 0,
    requires_approval BOOLEAN NOT NULL DEFAULT FALSE,
    is_primary        BOOLEAN NOT NULL DEFAULT FALSE,
    revoked           BOOLEAN NOT NULL DEFAULT FALSE,
    created_at        TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE INDEX IF NOT EXISTS chat_invite_links_chat_idx ON chat_invite_links (chat_id);

CREATE TABLE IF NOT EXISTS chat_join_requests (
    chat_id    BIGINT NOT NULL REFERENCES chats(id) ON DELETE CASCADE,
    user_id    TEXT   NOT NULL REFERENCES users(username) ON UPDATE CASCADE ON DELETE CASCADE,
    link_id    BIGINT REFERENCES chat_invite_links(id) ON DELETE SET NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    status     TEXT NOT NULL DEFAULT 'pending' CHECK (status IN ('pending', 'approved', 'declined')),
    PRIMARY KEY (chat_id, user_id)
);

-- Recent actions, kept and shown for 48 hours.
CREATE TABLE IF NOT EXISTS chat_admin_log (
    id         BIGSERIAL PRIMARY KEY,
    chat_id    BIGINT NOT NULL REFERENCES chats(id) ON DELETE CASCADE,
    actor_id   TEXT REFERENCES users(username) ON UPDATE CASCADE ON DELETE SET NULL,
    action     TEXT NOT NULL,
    payload    JSONB NOT NULL DEFAULT '{}'::jsonb,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE INDEX IF NOT EXISTS chat_admin_log_chat_idx ON chat_admin_log (chat_id, created_at);

-- joined_pts: what a member of a supergroup with hidden history may see from;
-- invite_id: the link they joined through; restricted_until: when a
-- restriction or a ban ends (NULL: never).
ALTER TABLE chat_members ADD COLUMN IF NOT EXISTS joined_pts       BIGINT NOT NULL DEFAULT 0;
ALTER TABLE chat_members ADD COLUMN IF NOT EXISTS invite_id        BIGINT;
ALTER TABLE chat_members ADD COLUMN IF NOT EXISTS restricted_until TIMESTAMPTZ;
ALTER TABLE chat_members ADD COLUMN IF NOT EXISTS added_by         TEXT;

-- Service messages ("X added Y") carry what happened, drawn by the page.
ALTER TABLE chat_messages ADD COLUMN IF NOT EXISTS service JSONB;
CREATE INDEX IF NOT EXISTS chat_messages_topic_idx ON chat_messages (chat_id, topic_id, pts) WHERE topic_id IS NOT NULL;
CREATE INDEX IF NOT EXISTS chat_messages_sender_time_idx ON chat_messages (chat_id, sender_id, created_at);
