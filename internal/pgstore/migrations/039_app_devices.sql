-- The messenger's Android app as a place notifications reach (from docfind's
-- 039, 2026-10-19). A WebView has no Push API, so the app asks for what is new
-- (POST /api/app/poll) and shows it as the phone's own notification. A row per
-- installation; nothing is queued for it -- the notifications table already
-- holds every notification, and the app reads after the last one it saw.
CREATE TABLE IF NOT EXISTS app_devices (
    device_id    TEXT        PRIMARY KEY,
    username     TEXT        NOT NULL,
    label        TEXT        NOT NULL DEFAULT '',
    user_agent   TEXT        NOT NULL DEFAULT '',
    created_at   TIMESTAMPTZ NOT NULL DEFAULT now(),
    last_seen_at TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE INDEX IF NOT EXISTS app_devices_user ON app_devices (username);
