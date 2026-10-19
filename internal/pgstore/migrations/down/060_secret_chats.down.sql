-- Reverses 060_secret_chats.sql. A secret chat without its keys and its
-- device binding is unusable, so the secret chats themselves go too (their
-- members, keys and waiting frames follow by cascade). Nothing else is touched.
DROP TABLE IF EXISTS secret_queue;
DROP TABLE IF EXISTS secret_chat_keys;
DROP TABLE IF EXISTS secret_chats;
DELETE FROM chats WHERE type = 'secret';
DELETE FROM schema_migrations WHERE version = 60;
