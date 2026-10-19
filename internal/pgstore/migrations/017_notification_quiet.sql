-- Quiet hours: a notification queued inside the recipient's quiet hours
-- still goes to every device and still appears in the in-app list, but the
-- device shows it without sound or vibration (internal/find/notifytopics.go,
-- quieten).
--
-- A column of its own rather than a lower urgency: urgency is how much the
-- event matters, and the notification centre ranks by it. A case that
-- arrived at 23:00 is as important as one that arrived at noon; it only
-- should not wake anybody.
ALTER TABLE notifications ADD COLUMN IF NOT EXISTS quiet BOOLEAN NOT NULL DEFAULT false;
