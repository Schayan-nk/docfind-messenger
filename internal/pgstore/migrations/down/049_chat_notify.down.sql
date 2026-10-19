-- Reverses 049_chat_notify.sql: the notification switches and a chat's sound.
ALTER TABLE chat_members   DROP COLUMN IF EXISTS sound;
ALTER TABLE chat_users_ext DROP COLUMN IF EXISTS notify;
