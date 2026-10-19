-- Reverses 061_chat_extras.sql: polls, scheduled messages and packs go. The
-- messages that showed a poll, a location, a sticker or a GIF stay, as
-- messages without what they carried. Pack files stay in chat_media.
DROP TABLE IF EXISTS chat_pack_items;
DROP TABLE IF EXISTS chat_packs;
DROP TABLE IF EXISTS chat_scheduled;
DROP TABLE IF EXISTS chat_poll_votes;
DROP TABLE IF EXISTS chat_poll_options;
DROP TABLE IF EXISTS chat_polls;
ALTER TABLE chat_messages DROP COLUMN IF EXISTS extra;
DELETE FROM schema_migrations WHERE version = 61;
