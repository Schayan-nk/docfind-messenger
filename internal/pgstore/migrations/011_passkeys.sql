-- Passkeys, per account.
--
-- Columns on users for the same reason as the second factor (010): an account
-- is saved as a whole and snapshotted as a whole, and emergency sign-in must
-- see exactly what the database saw.
--
-- passkeys is a JSON array of {id, public_key, alg, sign_count, name,
-- created_at, last_used}: public keys and counters, nothing secret. NULL is
-- "none". passkey_handle is the opaque id browsers store with an account's
-- passkeys -- random, not the user name -- and '' until the first is added.
ALTER TABLE users ADD COLUMN IF NOT EXISTS passkeys jsonb;
ALTER TABLE users ADD COLUMN IF NOT EXISTS passkey_handle text NOT NULL DEFAULT '';
