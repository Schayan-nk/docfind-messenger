package secrets

import (
	"context"
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"io"
	"testing"
	"time"

	"github.com/Schayan-nk/docfind-messenger/internal/pgstore"
)

// testBox is the same AES-256-GCM construction reports.FileBox uses, with a
// throwaway key.
//
// A real box rather than an identity function, because the properties worth
// testing here are about encryption: that the stored bytes do not contain the
// value, and that a different key cannot read it. A no-op box would pass a
// test that asserts neither.
type testBox struct{ aead cipher.AEAD }

func newBox(t *testing.T) *testBox {
	t.Helper()
	key := make([]byte, 32)
	if _, err := io.ReadFull(rand.Reader, key); err != nil {
		t.Fatal(err)
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		t.Fatal(err)
	}
	aead, err := cipher.NewGCM(block)
	if err != nil {
		t.Fatal(err)
	}
	return &testBox{aead: aead}
}

func (b *testBox) Seal(plain []byte) ([]byte, error) {
	nonce := make([]byte, b.aead.NonceSize())
	if _, err := io.ReadFull(rand.Reader, nonce); err != nil {
		return nil, err
	}
	return b.aead.Seal(nonce, nonce, plain, nil), nil
}

func (b *testBox) Open(sealed []byte) ([]byte, error) {
	ns := b.aead.NonceSize()
	if len(sealed) < ns {
		return nil, io.ErrUnexpectedEOF
	}
	return b.aead.Open(nil, sealed[:ns], sealed[ns:], nil)
}

// testStore returns a store over the real database, plus a way to read the
// RAW stored bytes so the encryption can be checked against the database
// rather than against this package's own API.
func testStore(t *testing.T) (*Store, func(key string) []byte) {
	t.Helper()
	cfg := pgstore.FromEnv()
	if !cfg.Enabled() {
		t.Skip("no DOCFIND_PG_* configured; set them to run the credential tests")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	db, err := pgstore.Open(ctx, cfg)
	if err != nil {
		t.Fatalf("connecting to %s: %v", cfg.Redacted(), err)
	}
	t.Cleanup(db.Close)
	if _, err := db.Migrate(context.Background()); err != nil {
		t.Fatal(err)
	}
	clean := func() {
		db.Pool().Exec(context.Background(), `DELETE FROM secrets`)
	}
	clean()
	t.Cleanup(clean)

	raw := func(key string) []byte {
		var b []byte
		_ = db.Pool().QueryRow(context.Background(),
			`SELECT ciphertext FROM secrets WHERE key = $1`, key).Scan(&b)
		return b
	}
	return Open(db, newBox(t)), raw
}

// A key nothing knows about is refused. The list is closed so a typo cannot
// quietly create a second credential that nothing ever reads -- the failure
// that looks like success, where the page saves and the provider still refuses
// every call.
func TestAnUnknownKeyIsRefused(t *testing.T) {
	s, _ := testStore(t)
	for _, bad := range []string{"ai.openia", "mailbox ", "", "OA", "whatsapp"} {
		if err := s.Set(bad, "x", "me"); err == nil {
			t.Errorf("%q was accepted as a credential key", bad)
		}
		if _, err := s.Get(bad); err == nil {
			t.Errorf("Get(%q) did not complain", bad)
		}
	}
}

// Every key in Keys must be usable, or the list and the validation disagree
// and one of them is a lie.
func TestEveryDeclaredKeyWorks(t *testing.T) {
	s, _ := testStore(t)
	for _, k := range Keys {
		if err := s.Set(k, "value-for-"+k, "me"); err != nil {
			t.Errorf("declared key %q was refused by Set: %v", k, err)
			continue
		}
		got, err := s.Get(k)
		if err != nil {
			t.Errorf("declared key %q could not be read: %v", k, err)
			continue
		}
		if got != "value-for-"+k {
			t.Errorf("%q round-tripped to %q", k, got)
		}
	}
}

func errText(err error) string {
	if err == nil {
		return ""
	}
	return err.Error()
}
