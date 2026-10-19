-- Reverses 045_chat_actions.sql: drops pins, reactions, per-person hiding and
-- mentions. Messages themselves are untouched.
DROP TABLE IF EXISTS chat_mentions;
DROP TABLE IF EXISTS chat_hidden;
DROP TABLE IF EXISTS chat_reactions;
DROP TABLE IF EXISTS chat_pins;
DELETE FROM schema_migrations WHERE version = 45;
