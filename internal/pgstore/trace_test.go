package pgstore

import (
	"bufio"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestCompactSQLFoldsWhitespaceAndCaps(t *testing.T) {
	got := compactSQL("select  *\nfrom   documents\n  where id = $1", 100)
	if got != "select * from documents where id = $1" {
		t.Errorf("whitespace not folded: %q", got)
	}
	got = compactSQL(strings.Repeat("x", 250), 200)
	if len([]rune(got)) != 201 {
		t.Errorf("not capped to 200: got %d runes", len([]rune(got)))
	}
}

func TestSlowQueryThresholdDefaultsTo500ms(t *testing.T) {
	t.Setenv("DOCFIND_SLOW_QUERY_MS", "")
	if got := slowQueryThreshold(); got != 500*time.Millisecond {
		t.Errorf("default threshold = %v, want 500ms", got)
	}
	t.Setenv("DOCFIND_SLOW_QUERY_MS", "50")
	if got := slowQueryThreshold(); got != 50*time.Millisecond {
		t.Errorf("DOCFIND_SLOW_QUERY_MS=50 gave %v, want 50ms", got)
	}
}

func findSlowQueryLines(t *testing.T, dir string) []map[string]any {
	t.Helper()
	var found []map[string]any
	_ = filepath.WalkDir(dir, func(path string, d os.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		f, err := os.Open(path)
		if err != nil {
			return err
		}
		defer f.Close()
		sc := bufio.NewScanner(f)
		for sc.Scan() {
			if !strings.Contains(sc.Text(), "slow_query") {
				continue
			}
			var line map[string]any
			if err := json.Unmarshal(sc.Bytes(), &line); err == nil {
				found = append(found, line)
			}
		}
		return nil
	})
	return found
}
