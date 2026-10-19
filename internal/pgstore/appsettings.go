package pgstore

import (
	"context"
	"encoding/json"
	"errors"
	"time"

	"github.com/jackc/pgx/v5"
)

// AppSettings is the settings administrators change on a page. See
// migrations/024_app_settings.sql.
type AppSettings struct{ db *DB }

func NewAppSettings(db *DB) *AppSettings { return &AppSettings{db: db} }

// ErrNoSetting is a setting nobody has saved yet.
var ErrNoSetting = errors.New("pgstore: no such setting")

const appSettingsTimeout = 10 * time.Second

// Get reads one setting into v.
func (s *AppSettings) Get(ctx context.Context, key string, v any) error {
	ctx, cancel := context.WithTimeout(ctx, appSettingsTimeout)
	defer cancel()
	var raw []byte
	err := s.db.Pool().QueryRow(ctx, `SELECT value FROM app_settings WHERE key = $1`, key).Scan(&raw)
	if errors.Is(err, pgx.ErrNoRows) {
		return ErrNoSetting
	}
	if err != nil {
		return err
	}
	return json.Unmarshal(raw, v)
}

// Set stores one setting, and who stored it.
func (s *AppSettings) Set(ctx context.Context, key string, v any, by string) error {
	raw, err := json.Marshal(v)
	if err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(ctx, appSettingsTimeout)
	defer cancel()
	_, err = s.db.Pool().Exec(ctx, `INSERT INTO app_settings (key, value, updated_by, updated_at)
		VALUES ($1, $2, $3, now())
		ON CONFLICT (key) DO UPDATE SET value = EXCLUDED.value, updated_by = EXCLUDED.updated_by, updated_at = now()`,
		key, raw, by)
	return err
}
