-- Settings an administrator changes on a page rather than in the .env: the
-- monthly report's recipients and schedule first (internal/find/
-- monthlyreport.go). One row per setting, its value a JSON document, with who
-- last changed it and when. Only ADDS; 5.27.x runs with it present.
CREATE TABLE IF NOT EXISTS app_settings (
    key        TEXT        PRIMARY KEY,
    value      JSONB       NOT NULL,
    updated_by TEXT        NOT NULL DEFAULT '',
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now()
);
