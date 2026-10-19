-- Chat module, phase 2: pins, reactions, "delete for me", mentions, and the
-- per-chat reaction allow-list. Idempotent; reversed by
-- migrations/down/045_chat_actions.down.sql.

CREATE TABLE IF NOT EXISTS chat_pins (
    chat_id    BIGINT NOT NULL REFERENCES chats(id) ON DELETE CASCADE,
    message_id BIGINT NOT NULL REFERENCES chat_messages(id) ON DELETE CASCADE,
    pinned_by  TEXT REFERENCES users(username) ON UPDATE CASCADE ON DELETE SET NULL,
    pinned_at  TIMESTAMPTZ NOT NULL DEFAULT now(),
    PRIMARY KEY (chat_id, message_id)
);
CREATE INDEX IF NOT EXISTS chat_pins_message_idx ON chat_pins (message_id);

CREATE TABLE IF NOT EXISTS chat_reactions (
    message_id BIGINT NOT NULL REFERENCES chat_messages(id) ON DELETE CASCADE,
    user_id    TEXT   NOT NULL REFERENCES users(username) ON UPDATE CASCADE ON DELETE CASCADE,
    emoji      TEXT   NOT NULL CHECK (char_length(emoji) BETWEEN 1 AND 16),
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    PRIMARY KEY (message_id, user_id, emoji)
);
CREATE INDEX IF NOT EXISTS chat_reactions_user_idx ON chat_reactions (user_id);

-- "Delete for me": the message stays for everyone else.
CREATE TABLE IF NOT EXISTS chat_hidden (
    user_id    TEXT   NOT NULL REFERENCES users(username) ON UPDATE CASCADE ON DELETE CASCADE,
    message_id BIGINT NOT NULL REFERENCES chat_messages(id) ON DELETE CASCADE,
    PRIMARY KEY (user_id, message_id)
);
CREATE INDEX IF NOT EXISTS chat_hidden_message_idx ON chat_hidden (message_id);

-- Who a message mentions: the mention badge and "jump to next mention".
CREATE TABLE IF NOT EXISTS chat_mentions (
    message_id BIGINT NOT NULL REFERENCES chat_messages(id) ON DELETE CASCADE,
    chat_id    BIGINT NOT NULL REFERENCES chats(id) ON DELETE CASCADE,
    user_id    TEXT   NOT NULL REFERENCES users(username) ON UPDATE CASCADE ON DELETE CASCADE,
    pts        BIGINT NOT NULL,
    PRIMARY KEY (message_id, user_id)
);
CREATE INDEX IF NOT EXISTS chat_mentions_user_chat_idx ON chat_mentions (user_id, chat_id, pts);
