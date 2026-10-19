-- Reverses 050_chat_calls.sql: every call and its participants (the call
-- history; the lines the calls left in chats stay).
DROP TABLE IF EXISTS chat_call_participants;
DROP TABLE IF EXISTS chat_calls;
