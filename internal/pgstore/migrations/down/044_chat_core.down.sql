-- Reverses 044_chat_core.sql. DESTROYS every chat, message and draft: run only
-- on purpose (docfind -migrate-down 044), never automatically.
DROP TABLE IF EXISTS chat_contacts;
DROP TABLE IF EXISTS chat_blocks;
DROP TABLE IF EXISTS chat_drafts;
DROP TABLE IF EXISTS chat_updates;
DROP TABLE IF EXISTS chat_messages;
DROP TABLE IF EXISTS chat_members;
DROP TABLE IF EXISTS chats;
DROP TABLE IF EXISTS chat_users_ext;
DELETE FROM schema_migrations WHERE version = 44;
