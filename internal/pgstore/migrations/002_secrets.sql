-- The credentials docfind uses to reach other systems.
--
-- ciphertext, and the column is named for what it holds rather than for what
-- it protects. Nothing here is readable without the key file beside the
-- database, and nothing here is a hash.
--
-- THE DISTINCTION MATTERS AND IS NOT A STYLE CHOICE. docfind must REPLAY these
-- credentials -- sign in to a mail server, to another system's API, to an AI
-- provider -- and a hash cannot be replayed. Hashing applies to exactly one
-- thing in this application: the users' own login passwords, which are only
-- ever verified against something somebody typed. Those live in
-- users.password_hash and never here.
--
-- What the encryption buys and does not buy is written out in
-- internal/reports/secret.go and is the same here: a copied database no longer
-- hands over the credentials, and somebody already running code as this
-- service can read the key and then the data. Nothing that stores a secret it
-- must later use can claim otherwise.
CREATE TABLE IF NOT EXISTS secrets (
    -- One of the closed set in internal/secrets. Closed so a typo cannot
    -- create a second credential that nothing reads -- the failure that looks
    -- like success, where the page saves and the provider still refuses every
    -- call.
    key        TEXT        PRIMARY KEY,
    ciphertext BYTEA       NOT NULL,
    updated_by TEXT        NOT NULL DEFAULT '',
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now()
);
