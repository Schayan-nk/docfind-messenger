package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// Configuration is the one place a deployment mistake turns into a security
// problem quietly: an empty API key, a world-readable output directory, a
// template path that points somewhere unexpected. Validate has to catch those
// at start-up, where someone is watching, rather than at request time.

// clearEnv removes every key this package reads, so one test cannot leak
// settings into the next.
func clearEnv(t *testing.T) {
	t.Helper()
	keys := []string{
		"PORT", "BIND_ADDRESS", "API_KEY", "TEMPLATE_DIRECTORY", "OUTPUT_DIRECTORY",
		"LOG_DIRECTORY", "IMAGE_DIRECTORY", "ARCHIVE_DIRECTORY", "ARCHIVE_KEY_SEGMENTS",
		"MAX_REQUEST_SIZE", "MAX_FILE_SIZE", "MAX_GRID_ROWS", "MAX_GRIDS",
		"RATE_LIMIT_RPS", "RATE_LIMIT_BURST", "TRUST_PROXY_HEADERS",
		"PLACEHOLDER_SYNTAX", "MISSING_PLACEHOLDER_MODE", "BIDI_WRAP",
		"LOG_LEVEL", "LOG_FORMAT", "LOG_FILE", "READ_TIMEOUT", "WRITE_TIMEOUT",
		"TLS_CERT_FILE", "TLS_KEY_FILE", "DOWNLOAD_BASE_URL", "TEMP_FILE_LIFETIME",
		"NUMBER_DECIMALS", "FORCE_FONTS", "AUTO_PARAGRAPH_RTL",
	}
	for _, k := range keys {
		t.Setenv(k, "")
		os.Unsetenv(k)
	}
}

// baseEnv points the required directories at a temporary tree.
func baseEnv(t *testing.T) string {
	t.Helper()
	dir := tempDir(t)
	for _, sub := range []string{"templates", "generated", "logs"} {
		if err := os.MkdirAll(filepath.Join(dir, sub), 0o750); err != nil {
			t.Fatal(err)
		}
	}
	t.Setenv("TEMPLATE_DIRECTORY", filepath.Join(dir, "templates"))
	t.Setenv("OUTPUT_DIRECTORY", filepath.Join(dir, "generated"))
	t.Setenv("LOG_DIRECTORY", filepath.Join(dir, "logs"))
	t.Setenv("API_KEY", "0123456789abcdef0123456789abcdef")
	return dir
}

// --- .env parsing -----------------------------------------------------------

func TestDotEnvParsing(t *testing.T) {
	clearEnv(t)
	dir := tempDir(t)
	env := filepath.Join(dir, ".env")

	content := strings.Join([]string{
		"# a comment",
		"",
		"PORT=9001",
		"  BIND_ADDRESS = 127.0.0.1  ",
		`API_KEY="0123456789abcdef0123456789abcdef"`,
		"LOG_FORMAT='text'",
		"EMPTY=",
		"WITH_EQUALS=a=b=c",
		"NOT A VALID LINE",
	}, "\n")
	if err := os.WriteFile(env, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := LoadDotEnv(env); err != nil {
		t.Fatalf("LoadDotEnv: %v", err)
	}

	checks := map[string]string{
		"PORT":         "9001",
		"BIND_ADDRESS": "127.0.0.1",
		"API_KEY":      "0123456789abcdef0123456789abcdef",
		"LOG_FORMAT":   "text",
		"WITH_EQUALS":  "a=b=c",
	}
	for k, want := range checks {
		if got := os.Getenv(k); got != want {
			t.Errorf("%s = %q, want %q", k, got, want)
		}
	}
	t.Cleanup(func() {
		for k := range checks {
			os.Unsetenv(k)
		}
		os.Unsetenv("EMPTY")
		os.Unsetenv("WITH_EQUALS")
	})
}

// The real environment wins: a .env is a convenience for development, not a way
// to override what an administrator set deliberately.
func TestDotEnvDoesNotOverrideRealEnvironment(t *testing.T) {
	clearEnv(t)
	dir := tempDir(t)
	env := filepath.Join(dir, ".env")
	if err := os.WriteFile(env, []byte("PORT=1111\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	t.Setenv("PORT", "2222")
	if err := LoadDotEnv(env); err != nil {
		t.Fatal(err)
	}
	if got := os.Getenv("PORT"); got != "2222" {
		t.Errorf("the .env file overrode the environment: PORT = %q", got)
	}
}
