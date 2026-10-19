-- Messenger phase 6: notifications (docs/chat/SPEC.md 6.10). A person's own
-- switches for the messenger's notifications (private chats, groups,
-- channels, calls, the preview, in-app sound, vibration, the badge), and a
-- chat's own in-app sound. The notifications themselves travel through the
-- existing push queue (notifications, notification_outbox).
--
-- Idempotent and reversible: migrations/down/049_chat_notify.down.sql.
ALTER TABLE chat_users_ext ADD COLUMN IF NOT EXISTS notify JSONB NOT NULL DEFAULT '{}'::jsonb;
ALTER TABLE chat_members   ADD COLUMN IF NOT EXISTS sound  TEXT  NOT NULL DEFAULT '';
