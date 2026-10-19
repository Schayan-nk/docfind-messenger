-- Reverses 046_chat_media.sql: drops media, uploads and link previews, and the
-- album, preview and document columns of messages. The files in the chat media
-- folder are not touched; remove them by hand if the folder is not wanted.
DROP INDEX IF EXISTS chat_messages_media_idx;
ALTER TABLE chat_messages DROP COLUMN IF EXISTS doc_ref;
ALTER TABLE chat_messages DROP COLUMN IF EXISTS preview;
ALTER TABLE chat_messages DROP COLUMN IF EXISTS album_cid;
DROP TABLE IF EXISTS chat_link_previews;
DROP TABLE IF EXISTS chat_uploads;
DROP TABLE IF EXISTS chat_media;
DELETE FROM schema_migrations WHERE version = 46;
