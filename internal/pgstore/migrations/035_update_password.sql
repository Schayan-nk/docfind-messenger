-- Accounts that must replace their password at the next sign-in: until the
-- person changes it the server answers nothing but who they are, the change
-- itself and signing out (auth.Guard.Authenticate).
--
-- With them, what is recorded of each person: the personnel code, the second
-- email address, and whether a signature image is on file beside the avatars
-- (its extension). Only ADDS.
ALTER TABLE users ADD COLUMN IF NOT EXISTS update_password BOOLEAN NOT NULL DEFAULT FALSE;
ALTER TABLE users ADD COLUMN IF NOT EXISTS personnel_code TEXT NOT NULL DEFAULT '';
ALTER TABLE users ADD COLUMN IF NOT EXISTS second_email TEXT NOT NULL DEFAULT '';
ALTER TABLE users ADD COLUMN IF NOT EXISTS signature_ext TEXT NOT NULL DEFAULT '';
-- When the password was last set, for the policy's maximum age (auth.Policy);
-- NULL for accounts from before, which are judged from created_at.
ALTER TABLE users ADD COLUMN IF NOT EXISTS password_changed_at TIMESTAMPTZ;
