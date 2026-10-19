-- Messenger phase 5: chat folders, a person's own settings for each chat
-- (notify only for mentions, marked unread, reactions seen, history cleared),
-- and search (docs/chat/SPEC.md 6.5, 6.6).
--
-- Idempotent and reversible: migrations/down/048_chat_lists_search.down.sql.

-- Folders: a person's own tabs over the chat list. The rules are evaluated on
-- the page from the chat list (types, contacts, muted, read, archived); the
-- chosen and excluded chats are kept here.
CREATE TABLE IF NOT EXISTS chat_folders (
    id            BIGSERIAL PRIMARY KEY,
    user_id       TEXT NOT NULL REFERENCES users(username) ON UPDATE CASCADE ON DELETE CASCADE,
    title         TEXT NOT NULL,
    icon          TEXT NOT NULL DEFAULT '',
    include_rules JSONB NOT NULL DEFAULT '{}'::jsonb,
    exclude_rules JSONB NOT NULL DEFAULT '{}'::jsonb,
    include_chats BIGINT[] NOT NULL DEFAULT '{}',
    exclude_chats BIGINT[] NOT NULL DEFAULT '{}',
    pinned_chats  BIGINT[] NOT NULL DEFAULT '{}',
    position      INTEGER NOT NULL DEFAULT 0,
    share_token   TEXT UNIQUE,
    created_at    TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE INDEX IF NOT EXISTS chat_folders_user_idx ON chat_folders (user_id, position);

ALTER TABLE chat_members ADD COLUMN IF NOT EXISTS mentions_only     BOOLEAN NOT NULL DEFAULT FALSE;
ALTER TABLE chat_members ADD COLUMN IF NOT EXISTS marked_unread     BOOLEAN NOT NULL DEFAULT FALSE;
ALTER TABLE chat_members ADD COLUMN IF NOT EXISTS reactions_seen_at TIMESTAMPTZ;
-- Messages up to here are gone for this person ("delete chat" for me).
ALTER TABLE chat_members ADD COLUMN IF NOT EXISTS cleared_pts       BIGINT NOT NULL DEFAULT 0;
CREATE INDEX IF NOT EXISTS chat_members_user_pinned_idx ON chat_members (user_id) WHERE pinned_order IS NOT NULL;

-- Persian normalisation for search (SPEC 5.2): Arabic yeh, alef maksura, yeh
-- with hamza and kaf become Persian; teh marbuta and heh with yeh become heh; hamza forms of
-- alef become alef; Arabic and Persian digits become ASCII; Persian and
-- typographic punctuation becomes a space; tatweel, zero-width characters,
-- direction marks and harakat go. internal/chat/normalize.go does the same.
CREATE OR REPLACE FUNCTION chat_norm(t TEXT) RETURNS TEXT
    LANGUAGE sql IMMUTABLE PARALLEL SAFE STRICT
    AS $fn$ SELECT translate($1, 'يىئكةۀأإٱ٠١٢٣٤٥٦٧٨٩۰۱۲۳۴۵۶۷۸۹،؛؟٪٫٬۔«»…–—‘’“”  •·ـ​‌‍‎‏﻿ًٌٍَُِّْٰٕٖٜٟٓٔٗ٘ٙٚٛٝٞ', 'یییکههااا01234567890123456789                    ') $fn$;

-- The full-text parser decides what a letter is from the database's locale:
-- under the C locale no Persian letter is one, and Persian words vanish from
-- the index. So every character outside ASCII is spelled as 'z' and its four
-- hex digits ('y' and six above U+FFFF; lpad would cut) before parsing: the parser sees plain ASCII words, a word's
-- prefix is still a prefix, and the result does not depend on the server.
-- Lower case is left to to_tsvector/to_tsquery. normalize.go's Fold matches.
CREATE OR REPLACE FUNCTION chat_fold(t TEXT) RETURNS TEXT
    LANGUAGE sql IMMUTABLE PARALLEL SAFE STRICT
    AS $fn$ SELECT coalesce(string_agg(CASE WHEN ascii(c) < 128 THEN c
                                         WHEN ascii(c) < 65536 THEN 'z' || lpad(to_hex(ascii(c)), 4, '0')
                                         ELSE 'y' || lpad(to_hex(ascii(c)), 6, '0') END, ''), '')
            FROM regexp_split_to_table($1, '') AS c $fn$;

ALTER TABLE chat_messages ADD COLUMN IF NOT EXISTS search tsvector
    GENERATED ALWAYS AS (to_tsvector('simple'::regconfig, chat_fold(chat_norm(text)))) STORED;
CREATE INDEX IF NOT EXISTS chat_messages_search_idx ON chat_messages USING gin (search);
-- Links: messages whose entities hold a url or a text link.
CREATE INDEX IF NOT EXISTS chat_messages_entities_idx ON chat_messages USING gin (entities jsonb_path_ops);

-- A file's name (dots, dashes and underscores split it into words) and the
-- text read from it (OCR, PDF text).
ALTER TABLE chat_media ADD COLUMN IF NOT EXISTS search tsvector
    GENERATED ALWAYS AS (to_tsvector('simple'::regconfig,
        chat_fold(chat_norm(regexp_replace(original_name, '[._-]+', ' ', 'g') || ' ' || coalesce(ocr_text, ''))))) STORED;
CREATE INDEX IF NOT EXISTS chat_media_search_idx ON chat_media USING gin (search);
