-- Notifications that reach a device when docfind is not open anywhere.
--
-- WHY THIS EXISTS. Until now every notification was raised by the page itself:
-- the home page polled for new work and called showNotification. A notification
-- raised by a page exists only while the page does, so on Windows and on an
-- iPhone nothing arrived until somebody opened the application -- which is the
-- moment a notification is least needed.
--
-- The server now decides what to announce and hands each message to the push
-- service run by the device's own platform (Apple, Microsoft, Google, Mozilla),
-- which delivers it with nothing of docfind running on the device.
--
-- THE SHAPE, AS ASKED FOR: a table of pending deliveries; a sender that retries
-- each one until the push service accepts it; and the row DELETED on
-- acceptance. What is added to that, and why each is needed:
--
--   * ONE ROW PER DEVICE, not per notification. A person with a phone and a
--     laptop has two subscriptions, and the phone being off must not hold back
--     -- or re-send to -- the laptop.
--   * A LEASE. A row is claimed by moving next_attempt_at forward before it is
--     sent. If the process dies mid-send, the lease expires and the row is
--     picked up again, so nothing is lost. The cost is that a crash at exactly
--     the wrong moment can send one message twice -- and the notification's
--     tag makes the second replace the first on the device.
--   * A LIMIT ON "UNTIL IT SUCCEEDS". Some answers mean it never will: 410 Gone
--     is a device that uninstalled the app, and retrying it for ever is a
--     queue that grows for ever. Those subscriptions are deleted; a message the
--     push service rejects outright, or one past its expiry, moves to
--     notification_dead where an administrator can see it.

-- A device that asked to be notified.
CREATE TABLE IF NOT EXISTS push_subscriptions (
    id              BIGSERIAL PRIMARY KEY,
    username        TEXT        NOT NULL,
    -- The push service URL the browser was given. UNIQUE: a browser that
    -- subscribes again gets the same endpoint back, and that must update the
    -- row rather than make the device receive everything twice.
    endpoint        TEXT        NOT NULL UNIQUE,
    -- The browser's public key and auth secret (RFC 8291). Public by design:
    -- they let the server ENCRYPT to the device, not read anything. The
    -- device's private key never leaves it.
    p256dh          TEXT        NOT NULL,
    auth            TEXT        NOT NULL,
    -- The address the device subscribed FROM, which is where pressing the
    -- notification takes it. Per device, because the same server can be
    -- reached under more than one name and a phone must be sent back to the
    -- one it actually uses. iOS needs this absolute to show the notification
    -- without running any script.
    origin          TEXT        NOT NULL DEFAULT '',
    -- What the person called this device, and what the browser said it was,
    -- so "which of my three subscriptions is the old phone" has an answer.
    label           TEXT        NOT NULL DEFAULT '',
    user_agent      TEXT        NOT NULL DEFAULT '',
    created_at      TIMESTAMPTZ NOT NULL DEFAULT now(),
    last_success_at TIMESTAMPTZ,
    last_failure_at TIMESTAMPTZ,
    last_status     INTEGER,
    last_error      TEXT        NOT NULL DEFAULT '',
    -- Consecutive failures. Reset by a success.
    failures        INTEGER     NOT NULL DEFAULT 0
);
CREATE INDEX IF NOT EXISTS push_subscriptions_user ON push_subscriptions (username);

-- What was announced to whom. KEPT after delivery: this is the history a
-- person sees in the application, and the record that stops the same event
-- being announced twice.
CREATE TABLE IF NOT EXISTS notifications (
    id          BIGSERIAL PRIMARY KEY,
    username    TEXT        NOT NULL,
    -- What kind of event: case, appointment, announcement, test.
    kind        TEXT        NOT NULL,
    -- The event's identity -- "case:156151:3" -- UNIQUE per person, so a
    -- watcher that sees the same case on every pass creates it once.
    dedupe_key  TEXT        NOT NULL,
    title       TEXT        NOT NULL,
    body        TEXT        NOT NULL DEFAULT '',
    url         TEXT        NOT NULL DEFAULT '/',
    -- The on-device tag: a newer notification with the same tag replaces the
    -- older one rather than stacking.
    tag         TEXT        NOT NULL DEFAULT '',
    -- "normal" or "high". High wakes a phone in battery saver.
    urgency     TEXT        NOT NULL DEFAULT 'normal',
    created_at  TIMESTAMPTZ NOT NULL DEFAULT now(),
    -- After this, an undelivered copy is not worth delivering: a meeting
    -- reminder that arrives after the meeting is noise.
    expires_at  TIMESTAMPTZ NOT NULL,
    read_at     TIMESTAMPTZ,
    UNIQUE (username, dedupe_key)
);
CREATE INDEX IF NOT EXISTS notifications_user_recent ON notifications (username, created_at DESC);

-- The deliveries still to make. A row here is a message not yet accepted by a
-- push service; it is deleted the moment one is.
CREATE TABLE IF NOT EXISTS notification_outbox (
    id              BIGSERIAL PRIMARY KEY,
    notification_id BIGINT      NOT NULL REFERENCES notifications (id) ON DELETE CASCADE,
    -- Deleting a subscription deletes what was queued for it: there is no
    -- device left to deliver to.
    subscription_id BIGINT      NOT NULL REFERENCES push_subscriptions (id) ON DELETE CASCADE,
    attempts        INTEGER     NOT NULL DEFAULT 0,
    -- Due now when created; pushed forward by the lease and by each backoff.
    next_attempt_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    last_status     INTEGER,
    last_error      TEXT        NOT NULL DEFAULT '',
    created_at      TIMESTAMPTZ NOT NULL DEFAULT now(),
    UNIQUE (notification_id, subscription_id)
);
-- The sender's one query: what is due.
CREATE INDEX IF NOT EXISTS notification_outbox_due ON notification_outbox (next_attempt_at);

-- Deliveries that will never succeed, kept so an administrator can see why.
-- Pruned after 30 days by the sender itself.
CREATE TABLE IF NOT EXISTS notification_dead (
    id              BIGSERIAL PRIMARY KEY,
    notification_id BIGINT,
    subscription_id BIGINT,
    username        TEXT        NOT NULL DEFAULT '',
    endpoint_host   TEXT        NOT NULL DEFAULT '',
    attempts        INTEGER     NOT NULL DEFAULT 0,
    last_status     INTEGER,
    reason          TEXT        NOT NULL DEFAULT '',
    died_at         TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE INDEX IF NOT EXISTS notification_dead_recent ON notification_dead (died_at DESC);

-- What each watcher has already seen for each person, so the server -- not
-- each browser's localStorage -- decides what is new.
--
-- Separate from notifications because "seen" and "announced" differ: the
-- first pass for a person records everything already waiting WITHOUT
-- announcing it. Otherwise enabling notifications would be greeted by a burst
-- of alerts for a week of work the person already knows about.
CREATE TABLE IF NOT EXISTS notification_seen (
    username   TEXT        NOT NULL,
    dedupe_key TEXT        NOT NULL,
    seen_at    TIMESTAMPTZ NOT NULL DEFAULT now(),
    PRIMARY KEY (username, dedupe_key)
);

-- Which watchers have made their first, silent pass for which person.
CREATE TABLE IF NOT EXISTS notification_primed (
    username  TEXT        NOT NULL,
    source    TEXT        NOT NULL,
    primed_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    PRIMARY KEY (username, source)
);
