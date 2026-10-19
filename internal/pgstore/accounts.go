package pgstore

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/Schayan-nk/docfind-messenger/internal/auth"
)

// Accounts stores accounts in PostgreSQL.
//
// It implements auth.Backend, so auth.Store does not know it exists: the
// fifteen account methods still mutate an in-memory map and then persist, and
// this is what persisting means once a database is configured. See
// internal/auth/backend.go for why it is shaped that way.
//
// THE DEPENDENCY RUNS THIS WAY ROUND. This package imports auth because auth
// defines what an account IS and this only knows how to store one. auth must
// never import pgstore; main is the only place that knows about both.
type Accounts struct {
	db *DB
}

// NewAccounts returns an auth.Backend over db.
func NewAccounts(db *DB) *Accounts { return &Accounts{db: db} }

// opTimeout bounds one account read or write.
//
// Generous for a database on the same box and far short of forever: a wedged
// database must not hold a request open until the browser gives up.
const opTimeout = 15 * time.Second

// healthTimeout is shorter, because Healthy is asked while somebody waits for
// a page to render.
const healthTimeout = 2 * time.Second

func (a *Accounts) Describe() string {
	return "پایگاه دادهٔ PostgreSQL (" + a.db.Redacted() + ")"
}

// Healthy reports whether the database is answering right now.
//
// ASKED, NOT REMEMBERED. This decides whether the application offers a write
// control at all, so a cached yes from a minute ago is worse than no answer:
// it produces a button that fails when pressed.
func (a *Accounts) Healthy() bool {
	ctx, cancel := context.WithTimeout(context.Background(), healthTimeout)
	defer cancel()
	return a.db.Ping(ctx) == nil
}

// Load returns every account with its preferences attached.
//
// ONE QUERY WITH A JOIN, not a query per account. This runs at start-up where
// a little slowness would be forgiven, but N+1 at start-up is still N+1 and
// the join costs nothing to write.
func (a *Accounts) Load() ([]*auth.User, error) {
	ctx, cancel := context.WithTimeout(context.Background(), opTimeout)
	defer cancel()

	rows, err := a.db.Pool().Query(ctx, `
		SELECT u.username, u.display_name, u.email, u.phone, u.password_hash, u.role,
		       u.disabled, u.created_at, u.last_login, u.token_version,
		       u.avatar_ext, u.english_name, COALESCE(p.prefs, '{}'::jsonb), u.second_factor,
		       u.passkeys, u.passkey_handle, u.update_password,
		       u.personnel_code, u.second_email, u.signature_ext, u.password_changed_at,
		       u.grants
		  FROM users u
		  LEFT JOIN user_prefs p ON p.username = u.username
		 ORDER BY u.username`)
	if err != nil {
		return nil, fmt.Errorf("pgstore: reading accounts: %w", err)
	}
	defer rows.Close()

	var out []*auth.User
	for rows.Next() {
		u := &auth.User{}
		// A pointer, because the column is nullable: "never signed in" is a
		// real state and 0001-01-01 is a date pretending to be one.
		var lastLogin, changedAt *time.Time
		var role string
		var prefsRaw, factorRaw, passkeysRaw, grantsRaw []byte
		if err := rows.Scan(&u.Username, &u.DisplayName, &u.Email, &u.Phone,
			&u.PasswordHash, &role, &u.Disabled, &u.CreatedAt, &lastLogin,
			&u.TokenVersion, &u.Avatar, &u.EnglishName, &prefsRaw, &factorRaw,
			&passkeysRaw, &u.PasskeyHandle, &u.MustChangePassword,
			&u.PersonnelCode, &u.SecondEmail, &u.Signature, &changedAt,
			&grantsRaw); err != nil {
			return nil, fmt.Errorf("pgstore: reading an account: %w", err)
		}
		// Grants that cannot be read FAIL the load, like a second factor: a
		// deny dropped on the way in is access somebody was refused.
		if len(grantsRaw) > 0 {
			if err := json.Unmarshal(grantsRaw, &u.Grants); err != nil {
				return nil, fmt.Errorf("pgstore: reading %s's permissions: %w", u.Username, err)
			}
			if len(u.Grants) == 0 {
				u.Grants = nil
			}
		}
		if len(passkeysRaw) > 0 {
			if err := json.Unmarshal(passkeysRaw, &u.Passkeys); err != nil {
				return nil, fmt.Errorf("pgstore: reading %s's passkeys: %w", u.Username, err)
			}
		}
		// Unlike the preferences, a second factor that cannot be read FAILS
		// the load: carrying on would sign that person in on a password alone.
		if len(factorRaw) > 0 {
			u.SecondFactor = &auth.SecondFactor{}
			if err := json.Unmarshal(factorRaw, u.SecondFactor); err != nil {
				return nil, fmt.Errorf("pgstore: reading %s's second factor: %w", u.Username, err)
			}
		}
		u.Role = auth.Role(role)
		if lastLogin != nil {
			u.LastLogin = *lastLogin
		}
		if changedAt != nil {
			u.PasswordChangedAt = *changedAt
		}
		// A malformed preferences document leaves the defaults rather than
		// failing the whole load. Nobody should be locked out of the
		// application by a bad font size.
		if len(prefsRaw) > 0 {
			_ = json.Unmarshal(prefsRaw, &u.Prefs)
		}
		out = append(out, u)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("pgstore: reading accounts: %w", err)
	}
	return out, nil
}

// RestoreGrants writes each given account's permission overrides where the
// database holds none, and returns how many accounts it wrote. Once, on the
// start that adds the column (migration 041), from the snapshot: the grants
// set since the last restart were only there. An account the database already
// has grants for, or does not have at all, is left alone.
func RestoreGrants(ctx context.Context, db *DB, users []*auth.User) (int, error) {
	n := 0
	for _, u := range users {
		if u == nil || len(u.Grants) == 0 {
			continue
		}
		raw, err := json.Marshal(u.Grants)
		if err != nil {
			return n, fmt.Errorf("pgstore: encoding %s's permissions: %w", u.Username, err)
		}
		tag, err := db.Pool().Exec(ctx, `UPDATE users SET grants = $2 WHERE username = $1 AND grants IS NULL`, u.Username, raw)
		if err != nil {
			return n, fmt.Errorf("pgstore: restoring %s's permissions: %w", u.Username, err)
		}
		n += int(tag.RowsAffected())
	}
	return n, nil
}

// SetPhone writes one account's mobile number and nothing else; false when
// there is no such account. For `docfind -set-phone`, run while the server is
// stopped: a running server keeps every account in memory and writes them all
// back on its next save.
func (a *Accounts) SetPhone(ctx context.Context, username, phone string) (bool, error) {
	ctx, cancel := context.WithTimeout(ctx, opTimeout)
	defer cancel()
	tag, err := a.db.Pool().Exec(ctx, `UPDATE users SET phone = $2 WHERE lower(username) = lower($1)`, username, phone)
	if err != nil {
		return false, fmt.Errorf("pgstore: setting %s's phone: %w", username, err)
	}
	return tag.RowsAffected() > 0, nil
}

// SetGrant writes one permission override on one account -- "allow",
// "deny", or "" to clear it back to the role -- and nothing else; false when
// there is no such account. Like SetPhone, for a stopped server.
func (a *Accounts) SetGrant(ctx context.Context, username, perm, state string) (bool, error) {
	ctx, cancel := context.WithTimeout(ctx, opTimeout)
	defer cancel()
	q := `UPDATE users SET grants = COALESCE(grants, '{}'::jsonb) || jsonb_build_object($2::text, $3::text)
	       WHERE lower(username) = lower($1)`
	args := []any{username, perm, state}
	if state == "" {
		q = `UPDATE users SET grants = NULLIF(COALESCE(grants, '{}'::jsonb) - $2::text, '{}'::jsonb)
		      WHERE lower(username) = lower($1)`
		args = args[:2]
	}
	tag, err := a.db.Pool().Exec(ctx, q, args...)
	if err != nil {
		return false, fmt.Errorf("pgstore: setting %s's %s: %w", username, perm, err)
	}
	return tag.RowsAffected() > 0, nil
}

// Save persists every account, replacing what was there.
//
// THE WHOLE SET, matching what writing the accounts file has always meant --
// see the Backend comment in internal/auth. So it upserts everything given and
// deletes anything not given, in ONE transaction: a half-applied account list
// is a list somebody is locked out of.
//
// AN EMPTY SET IS REFUSED WHEN ACCOUNTS EXIST. Save's contract is "replace
// everything", so an empty slice arriving here -- from a bug above, a failed
// load mistaken for success, a filter that matched nothing -- would delete
// every account in the company and lock everybody out of the application, with
// no way back but restoring a backup. There is no legitimate path to it:
// auth.Store refuses to remove the last administrator, so the set never
// legitimately empties. An empty save into an EMPTY database is fine, because
// that is a fresh install with nothing to lose.
func (a *Accounts) Save(users []*auth.User) error {
	ctx, cancel := context.WithTimeout(context.Background(), opTimeout)
	defer cancel()

	if len(users) == 0 {
		var existing int
		if err := a.db.Pool().QueryRow(ctx, `SELECT count(*) FROM users`).Scan(&existing); err != nil {
			return fmt.Errorf("pgstore: counting accounts: %w", err)
		}
		if existing > 0 {
			return fmt.Errorf("pgstore: refusing to save an empty account list over %d existing account(s); "+
				"that would lock everybody out. This is a bug in the caller, not a state to store", existing)
		}
		return nil
	}

	tx, err := a.db.Pool().Begin(ctx)
	if err != nil {
		return fmt.Errorf("pgstore: %w", err)
	}
	defer tx.Rollback(ctx)

	keep := make([]string, 0, len(users))
	for _, u := range users {
		if u == nil || u.Username == "" {
			continue
		}
		keep = append(keep, u.Username)

		createdAt := u.CreatedAt
		if createdAt.IsZero() {
			createdAt = time.Now()
		}
		var lastLogin, changedAt *time.Time
		if !u.LastLogin.IsZero() {
			lastLogin = &u.LastLogin
		}
		if !u.PasswordChangedAt.IsZero() {
			changedAt = &u.PasswordChangedAt
		}
		// NULL for "password only", rather than a JSON null inside the column.
		var factor []byte
		if u.SecondFactor != nil {
			if factor, err = json.Marshal(u.SecondFactor); err != nil {
				return fmt.Errorf("pgstore: encoding %s's second factor: %w", u.Username, err)
			}
		}

		var passkeys []byte
		if len(u.Passkeys) > 0 {
			if passkeys, err = json.Marshal(u.Passkeys); err != nil {
				return fmt.Errorf("pgstore: encoding %s's passkeys: %w", u.Username, err)
			}
		}
		// NULL for "no overrides" (migration 041).
		var grants []byte
		if len(u.Grants) > 0 {
			if grants, err = json.Marshal(u.Grants); err != nil {
				return fmt.Errorf("pgstore: encoding %s's permissions: %w", u.Username, err)
			}
		}

		if _, err := tx.Exec(ctx, `
			INSERT INTO users (username, display_name, email, phone, password_hash, role,
			                   disabled, created_at, last_login, token_version,
			                   avatar_ext, english_name, second_factor, passkeys,
			                   passkey_handle, update_password, personnel_code,
			                   second_email, signature_ext, password_changed_at, grants)
			VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15,$16,$17,$18,$19,$20,$21)
			ON CONFLICT (username) DO UPDATE SET
			    grants          = EXCLUDED.grants,
			    update_password = EXCLUDED.update_password,
			    personnel_code = EXCLUDED.personnel_code,
			    second_email   = EXCLUDED.second_email,
			    signature_ext  = EXCLUDED.signature_ext,
			    password_changed_at = EXCLUDED.password_changed_at,
			    second_factor  = EXCLUDED.second_factor,
			    passkeys       = EXCLUDED.passkeys,
			    passkey_handle = EXCLUDED.passkey_handle,
			    display_name  = EXCLUDED.display_name,
			    email         = EXCLUDED.email,
			    phone         = EXCLUDED.phone,
			    password_hash = EXCLUDED.password_hash,
			    role          = EXCLUDED.role,
			    disabled      = EXCLUDED.disabled,
			    last_login    = EXCLUDED.last_login,
			    token_version = EXCLUDED.token_version,
			    avatar_ext    = EXCLUDED.avatar_ext,
			    english_name  = EXCLUDED.english_name`,
			u.Username, u.DisplayName, u.Email, u.Phone, u.PasswordHash, string(u.Role),
			u.Disabled, createdAt, lastLogin, u.TokenVersion,
			u.Avatar, u.EnglishName, factor, passkeys,
			u.PasskeyHandle, u.MustChangePassword, u.PersonnelCode,
			u.SecondEmail, u.Signature, changedAt, grants); err != nil {
			return fmt.Errorf("pgstore: writing account %s: %w", u.Username, err)
		}

		// created_at is deliberately NOT in the UPDATE above: it is when the
		// account was made, and an upsert should not rewrite history because
		// somebody changed a display name.

		prefs, err := json.Marshal(u.Prefs)
		if err != nil {
			return fmt.Errorf("pgstore: encoding preferences for %s: %w", u.Username, err)
		}
		if _, err := tx.Exec(ctx, `
			INSERT INTO user_prefs (username, prefs) VALUES ($1, $2)
			ON CONFLICT (username) DO UPDATE SET prefs = EXCLUDED.prefs`,
			u.Username, prefs); err != nil {
			return fmt.Errorf("pgstore: writing preferences for %s: %w", u.Username, err)
		}
	}

	// Anything not in the set goes. Preferences and pictures follow by the
	// cascade declared in the schema.
	if _, err := tx.Exec(ctx,
		`DELETE FROM users WHERE username <> ALL($1)`, keep); err != nil {
		return fmt.Errorf("pgstore: removing accounts no longer in the set: %w", err)
	}

	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("pgstore: committing the account list: %w", err)
	}
	return nil
}
