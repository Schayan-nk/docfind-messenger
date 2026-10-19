-- Reverses 048_chat_lists_search.sql: folders, the per-person chat settings
-- it added, and the search columns, indexes and function.
DROP INDEX IF EXISTS chat_media_search_idx;
ALTER TABLE chat_media DROP COLUMN IF EXISTS search;
DROP INDEX IF EXISTS chat_messages_entities_idx;
DROP INDEX IF EXISTS chat_messages_search_idx;
ALTER TABLE chat_messages DROP COLUMN IF EXISTS search;
DROP FUNCTION IF EXISTS chat_fold(TEXT);
DROP FUNCTION IF EXISTS chat_norm(TEXT);
DROP INDEX IF EXISTS chat_members_user_pinned_idx;
ALTER TABLE chat_members DROP COLUMN IF EXISTS cleared_pts;
ALTER TABLE chat_members DROP COLUMN IF EXISTS reactions_seen_at;
ALTER TABLE chat_members DROP COLUMN IF EXISTS marked_unread;
ALTER TABLE chat_members DROP COLUMN IF EXISTS mentions_only;
DROP TABLE IF EXISTS chat_folders;
