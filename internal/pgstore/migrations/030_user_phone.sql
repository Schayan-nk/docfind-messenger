-- Every account gets a mobile number (asked for on 1405/07/08): where the SMS
-- panel sends a person's notifications and, when they choose it, the code of
-- the second step of signing in. Stored as 09xxxxxxxxx (auth.SetPhone
-- normalises what is typed). Empty until somebody enters one.
-- Only ADDS; 5.30.x runs with it present.
ALTER TABLE users ADD COLUMN IF NOT EXISTS phone TEXT NOT NULL DEFAULT '';
