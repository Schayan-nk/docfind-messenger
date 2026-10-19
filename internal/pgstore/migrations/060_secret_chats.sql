-- Chat module, phase 10: secret chats (docs/chat/SECRET_CHATS.md).
--
-- The server introduces two devices and carries sealed bodies between them.
-- It stores public keys only, and a body only until the receiving device
-- confirms it: secret_queue is a per-device mailbox, not a history.
--
-- Idempotent (IF NOT EXISTS throughout); migrations/down/060_secret_chats.down.sql
-- reverses it. The chat itself is a chats row of type 'secret' with its two
-- members, so every membership check of the module applies unchanged.

CREATE TABLE IF NOT EXISTS secret_chats (
    chat_id          BIGINT PRIMARY KEY REFERENCES chats(id) ON DELETE CASCADE,
    initiator        TEXT NOT NULL REFERENCES users(username) ON UPDATE CASCADE ON DELETE CASCADE,
    initiator_device TEXT NOT NULL CHECK (initiator_device ~ '^[0-9a-f]{32}$'),
    responder        TEXT NOT NULL REFERENCES users(username) ON UPDATE CASCADE ON DELETE CASCADE,
    -- NULL until one of the responder's devices accepts; from then on the
    -- chat lives on exactly these two devices.
    responder_device TEXT CHECK (responder_device IS NULL OR responder_device ~ '^[0-9a-f]{32}$'),
    alg              TEXT CHECK (alg IS NULL OR alg IN ('x25519', 'p256')),
    state            TEXT NOT NULL DEFAULT 'requested' CHECK (state IN ('requested', 'active', 'closed')),
    created_at       TIMESTAMPTZ NOT NULL DEFAULT now(),
    accepted_at      TIMESTAMPTZ,
    closed_at        TIMESTAMPTZ,
    CHECK (initiator <> responder)
);
CREATE INDEX IF NOT EXISTS secret_chats_initiator_idx ON secret_chats (initiator);
CREATE INDEX IF NOT EXISTS secret_chats_responder_idx ON secret_chats (responder);

-- Public keys only (SPEC 5.4). The inviting device offers P-256 and, where its
-- browser has it, X25519; the key the answer did not use is removed on accept.
CREATE TABLE IF NOT EXISTS secret_chat_keys (
    chat_id     BIGINT NOT NULL REFERENCES chats(id) ON DELETE CASCADE,
    device_id   TEXT   NOT NULL,
    alg         TEXT   NOT NULL CHECK (alg IN ('x25519', 'p256')),
    public_key  TEXT   NOT NULL, -- raw public key, base64url
    fingerprint TEXT   NOT NULL, -- hex SHA-256 of the raw key
    created_at  TIMESTAMPTZ NOT NULL DEFAULT now(),
    PRIMARY KEY (chat_id, device_id, alg)
);

-- One row per frame waiting for a device. to_device NULL is "any device of
-- to_user": an invitation that no device has accepted yet. body is a sealed
-- message (base64url) for kind 'msg', a small JSON object of public values
-- for the other kinds; it is never logged.
CREATE TABLE IF NOT EXISTS secret_queue (
    id            BIGSERIAL PRIMARY KEY,
    chat_id       BIGINT NOT NULL REFERENCES chats(id) ON DELETE CASCADE,
    to_user       TEXT   NOT NULL REFERENCES users(username) ON UPDATE CASCADE ON DELETE CASCADE,
    to_device     TEXT,
    from_user     TEXT   REFERENCES users(username) ON UPDATE CASCADE ON DELETE SET NULL,
    from_device   TEXT   NOT NULL DEFAULT '',
    kind          TEXT   NOT NULL CHECK (kind IN ('request', 'accept', 'msg', 'close')),
    client_msg_id UUID,
    body          TEXT   NOT NULL DEFAULT '',
    created_at    TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE INDEX IF NOT EXISTS secret_queue_to_idx ON secret_queue (to_user, to_device, id);
CREATE INDEX IF NOT EXISTS secret_queue_chat_idx ON secret_queue (chat_id);
CREATE INDEX IF NOT EXISTS secret_queue_from_idx ON secret_queue (from_user);
CREATE INDEX IF NOT EXISTS secret_queue_created_idx ON secret_queue (created_at);
CREATE UNIQUE INDEX IF NOT EXISTS secret_queue_dedupe_idx ON secret_queue (chat_id, from_user, client_msg_id) WHERE client_msg_id IS NOT NULL;
