-- Reverses 047_chat_groups.sql: drops topics, invite links, join requests and
-- the admin log, and the columns phase 4 added. Groups and channels stay as
-- chats; their messages keep their topic_id values.
DROP INDEX IF EXISTS chat_messages_sender_time_idx;
DROP INDEX IF EXISTS chat_messages_topic_idx;
ALTER TABLE chat_messages DROP COLUMN IF EXISTS service;
ALTER TABLE chat_members DROP COLUMN IF EXISTS added_by;
ALTER TABLE chat_members DROP COLUMN IF EXISTS restricted_until;
ALTER TABLE chat_members DROP COLUMN IF EXISTS invite_id;
ALTER TABLE chat_members DROP COLUMN IF EXISTS joined_pts;
DROP TABLE IF EXISTS chat_admin_log;
DROP TABLE IF EXISTS chat_join_requests;
DROP TABLE IF EXISTS chat_invite_links;
DROP TABLE IF EXISTS chat_topic_reads;
DROP TABLE IF EXISTS chat_topics;
DELETE FROM schema_migrations WHERE version = 47;
