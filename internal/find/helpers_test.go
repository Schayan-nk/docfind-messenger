package find

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Schayan-nk/docfind-messenger/internal/auth"
	"github.com/Schayan-nk/docfind-messenger/internal/pgstore"
)

// Helpers shared by this package's tests.

func asUser(t *testing.T, s *Server, method, target, token, body string) *httptest.ResponseRecorder {
	t.Helper()
	var r *http.Request
	if body == "" {
		r = httptest.NewRequest(method, target, nil)
	} else {
		r = httptest.NewRequest(method, target, strings.NewReader(body))
		r.Header.Set("Content-Type", "application/json")
	}
	if token != "" {
		r.AddCookie(&http.Cookie{Name: auth.SessionCookie, Value: token})
	}
	// The mutating routes check the origin of a state-changing request, the
	// same as every other POST in this application.
	r.Header.Set("Origin", "http://example.test")
	r.Host = "example.test"
	rec := httptest.NewRecorder()
	s.Handler().ServeHTTP(rec, r)
	return rec
}

func getAsset(t *testing.T, s *Server, path, acceptEncoding, ifNoneMatch string) *httptest.ResponseRecorder {
	t.Helper()
	r := httptest.NewRequest("GET", path, nil)
	if acceptEncoding != "" {
		r.Header.Set("Accept-Encoding", acceptEncoding)
	}
	if ifNoneMatch != "" {
		r.Header.Set("If-None-Match", ifNoneMatch)
	}
	rec := httptest.NewRecorder()
	s.Handler().ServeHTTP(rec, r)
	return rec
}

// A temporary directory whose cleanup does not fail the test on Windows.
//
// t.TempDir() removes its directory when the test ends and FAILS the test if
// it cannot. On Windows a file written moments earlier can still be held
// briefly by something outside this process -- a scanner, the search indexer --
// and RemoveAll then reports "The directory is not empty".
//
// Measured on this machine before changing anything: four full-suite runs out
// of eight failed that way, in four DIFFERENT tests across three packages,
// with every assertion in every one of them passing. The message is always
// "testing.go:1369: TempDir RemoveAll cleanup", never an assertion.
//
// Worth fixing rather than re-running, because a suite that fails half its
// runs for a reason unrelated to the code teaches everybody to re-run it and
// stop reading the output -- and then the failure that IS real gets the same
// shrug. This package's tests are the ones that verify the report builder's
// routes, so it is the package where that mattered most.
//
// Retried, and a residue is LOGGED rather than failed. Logged rather than
// ignored on purpose: a genuine leaked file handle would keep the directory
// undeletable on every retry of every run, and that log line is what would
// say so. A scanner holding a file appears once and is gone on the retry.
//
// The same helper exists in internal/reports. Not shared, because a test
// helper cannot cross a package boundary without becoming exported API that
// ships in the binary, and forty lines of duplication is the cheaper of the
// two.
func tempDir(t *testing.T) string {
	t.Helper()
	dir, err := os.MkdirTemp("", "find-"+safeTestName(t.Name())+"-")
	if err != nil {
		t.Fatalf("temporary directory: %v", err)
	}
	t.Cleanup(func() {
		for i := 0; ; i++ {
			if err := os.RemoveAll(dir); err == nil {
				return
			} else if i == 3 {
				t.Logf("could not remove %s: %v -- harmless if this is the only "+
					"run it happens on; a file handle this package failed to "+
					"close would do it every time", dir, err)
				return
			}
			time.Sleep(80 * time.Millisecond)
		}
	})
	return dir
}

func writeDoc(t *testing.T, root, rel string, body []byte) {
	t.Helper()
	full := filepath.Join(root, filepath.FromSlash(rel))
	if err := os.MkdirAll(filepath.Dir(full), 0o750); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(full, body, 0o644); err != nil {
		t.Fatal(err)
	}
}

// buildArchive writes a small archive of real .docx files.
func buildArchive(t *testing.T) string {
	t.Helper()
	root := tempDir(t)

	docs := []struct {
		rel  string
		body string
	}{
		{filepath.Join("111", "222", "Contract_111222.docx"), "قرارداد شماره 111222 کنتاکتور سه فاز"},
		{filepath.Join("111", "222", "Pi_NO_111222.docx"), "Proforma invoice for valve VX-100"},
		{filepath.Join("333", "444", "Anfrage_333444.docx"), "Anfrage Float Level Switch Sample"},
	}
	for _, d := range docs {
		full := filepath.Join(root, d.rel)
		if err := os.MkdirAll(filepath.Dir(full), 0o750); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(full, minimalDocx(t, d.body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return root
}

// safeTestName makes a test's name usable in a filename.
//
// A subtest's name carries its parent and its case:
// "TestFirstAdminStillWorksOverLoopback/[::1]:51000". Both the slash and the
// colon are illegal in a Windows path, and MkdirTemp refuses a pattern
// containing a separator on every platform -- which turned a helper meant to
// stop spurious failures into eight real ones the first time it ran under a
// table-driven test. The name is kept rather than dropped because it is what
// identifies the directory in the log line above.
func safeTestName(name string) string {
	var b strings.Builder
	b.Grow(len(name))
	for _, r := range name {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9', r == '_', r == '-':
			b.WriteRune(r)
		default:
			b.WriteByte('_')
		}
	}
	return b.String()
}

func readPage(t *testing.T, name string) string {
	t.Helper()
	b, err := os.ReadFile(name)
	if err != nil {
		t.Fatalf("reading %s: %v", name, err)
	}
	return string(b)
}

// memSettings is the settings table without a database.
type memSettings struct {
	mu   sync.Mutex
	rows map[string][]byte
}

func (m *memSettings) Get(_ context.Context, key string, v any) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	raw, ok := m.rows[key]
	if !ok {
		return pgstore.ErrNoSetting
	}
	return json.Unmarshal(raw, v)
}

func (m *memSettings) Set(_ context.Context, key string, v any, _ string) error {
	raw, err := json.Marshal(v)
	m.mu.Lock()
	defer m.mu.Unlock()
	m.rows[key] = raw
	return err
}

// signFor mints a token for an account.
//
// The version is read from the stored user rather than passed as zero: a
// token carrying the wrong version is refused, which is the revocation
// mechanism doing its job and would read here as a broken test.
func signFor(t *testing.T, signer *auth.Signer, users *auth.Store, name string) string {
	t.Helper()
	u, err := users.Get(name)
	if err != nil {
		t.Fatalf("no such account %s: %v", name, err)
	}
	tok, _, err := signer.Sign(name, u.TokenVersion)
	if err != nil {
		t.Fatalf("signing for %s: %v", name, err)
	}
	return tok
}

// identityBox seals nothing; the mailbox tests are about hosts, not secrets.
type identityBox struct{}
