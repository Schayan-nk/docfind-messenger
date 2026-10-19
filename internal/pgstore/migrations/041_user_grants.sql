-- Each account's permission overrides, auth.User.Grants: {"page:orders":
-- "allow", "report:x": "deny", ...}. They were never stored here (1405/07/18,
-- the user: "based on each update users' permissions get reset"): Accounts
-- wrote and read every other field, so the grants lived in memory and in the
-- snapshot only, and every restart -- every update -- handed everybody back
-- their role's default. NULL is no overrides. The first start with this
-- column copies the grants in force from the signed snapshot
-- (cmd/docfind/accountsetup.go), so the ones set since the last restart are
-- not lost too. Only ADDS; 5.62.x runs with it present.
ALTER TABLE users ADD COLUMN IF NOT EXISTS grants JSONB;
