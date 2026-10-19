// Package secretbox seals small secrets with AES-GCM under a key kept in a
// file beside the data (DPAPI-wrapped on Windows).
package secretbox

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"encoding/base64"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sync"
)

// Keeping connection strings off the disk in the clear.
//
// A data source is a database password. The report builder exists so an
// administrator can point it at a server, which means this application now
// stores credentials for databases it does not own -- and the honest place to
// start is that a JSON file of production passwords beside the binary is not
// acceptable, whatever the file permissions say.
//
// So: AES-256-GCM over each connection string, with a key kept in a file of
// its own beside the data.
//
// What that does and does not buy, stated plainly, because encryption at rest
// is easy to oversell:
//
//   - It does buy: a backup, a copied folder, a support bundle or a stray
//     `type datasources.json` no longer hands over the passwords. Those are
//     the realistic ways a secret escapes a server, and all of them move the
//     data without the key.
//   - On Windows it buys more: the key file is wrapped with DPAPI, tied to
//     this machine, so the key is useless anywhere else even if both files
//     travel together.
//   - It does NOT buy protection from somebody who is already running code as
//     this service on this machine. They can read the key and the data, in
//     that order. Nothing a program can do to a secret it must later use
//     changes that, and claiming otherwise would be the dishonest part.
//
// The alternative -- never storing the string, and asking for it on every use
// -- was considered and rejected: a report runs on a schedule and from a
// phone, with nobody there to type a password.

// secretBox seals and opens short strings with one key.
type secretBox struct {
	mu   sync.Mutex
	aead cipher.AEAD
}

// secretVersion prefixes every sealed value.
//
// One byte, so the format can change later without having to guess what an
// old value was: a reader that meets a version it does not know says so
// instead of decrypting rubbish.
const secretVersion = 1

// openSecretBox loads the key beside path, creating it on first use.
//
// keyPath is derived from the data file rather than configured, because the
// two belong together: a key in one place and data in another is a way to
// lose the key and, with it, every stored connection.
func openSecretBox(dataPath string) (*secretBox, error) {
	keyPath := dataPath + ".key"
	key, err := loadOrCreateKey(keyPath)
	if err != nil {
		return nil, err
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, fmt.Errorf("reports: key is unusable: %w", err)
	}
	aead, err := cipher.NewGCM(block)
	if err != nil {
		return nil, fmt.Errorf("reports: cipher: %w", err)
	}
	return &secretBox{aead: aead}, nil
}

func loadOrCreateKey(path string) ([]byte, error) {
	raw, err := os.ReadFile(path)
	if err == nil {
		key, kerr := unprotectKey(raw)
		if kerr != nil {
			return nil, fmt.Errorf("reports: the key file %s cannot be read: %w\n"+
				"this usually means it was copied from another machine, where it does not work; "+
				"delete it and re-enter the connection strings", path, kerr)
		}
		if len(key) != 32 {
			return nil, fmt.Errorf("reports: the key in %s is %d bytes, not 32", path, len(key))
		}
		return key, nil
	}
	if !errors.Is(err, os.ErrNotExist) {
		return nil, fmt.Errorf("reports: reading %s: %w", path, err)
	}

	key := make([]byte, 32)
	if _, err := io.ReadFull(rand.Reader, key); err != nil {
		return nil, fmt.Errorf("reports: generating a key: %w", err)
	}
	wrapped, err := protectKey(key)
	if err != nil {
		return nil, err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return nil, fmt.Errorf("reports: %w", err)
	}
	// 0600 rather than 0644, and written with O_EXCL so two processes starting
	// at once cannot each write a key and leave one of them unable to read
	// what the other sealed.
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		if errors.Is(err, os.ErrExist) {
			// Somebody won the race; use theirs.
			return loadOrCreateKey(path)
		}
		return nil, fmt.Errorf("reports: creating %s: %w", path, err)
	}
	if _, err := f.Write(wrapped); err != nil {
		f.Close()
		return nil, fmt.Errorf("reports: writing %s: %w", path, err)
	}
	if err := f.Close(); err != nil {
		return nil, fmt.Errorf("reports: closing %s: %w", path, err)
	}
	return key, nil
}

// Seal returns a base64 string safe to put in a JSON file.
func (b *secretBox) Seal(plain string) (string, error) {
	if plain == "" {
		return "", nil
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	nonce := make([]byte, b.aead.NonceSize())
	if _, err := io.ReadFull(rand.Reader, nonce); err != nil {
		return "", fmt.Errorf("reports: nonce: %w", err)
	}
	out := []byte{secretVersion}
	out = append(out, nonce...)
	out = b.aead.Seal(out, nonce, []byte(plain), []byte{secretVersion})
	return base64.StdEncoding.EncodeToString(out), nil
}

// Open reverses Seal.
func (b *secretBox) Open(sealed string) (string, error) {
	if sealed == "" {
		return "", nil
	}
	raw, err := base64.StdEncoding.DecodeString(sealed)
	if err != nil {
		return "", fmt.Errorf("reports: stored value is not valid base64: %w", err)
	}
	ns := b.aead.NonceSize()
	if len(raw) < 1+ns {
		return "", errors.New("reports: stored value is too short to be valid")
	}
	if raw[0] != secretVersion {
		return "", fmt.Errorf("reports: stored value has format version %d, which this build does not know", raw[0])
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	plain, err := b.aead.Open(nil, raw[1:1+ns], raw[1+ns:], []byte{secretVersion})
	if err != nil {
		// Deliberately not "wrong password" or anything resembling an oracle:
		// the only honest statement is that this key does not open this value.
		return "", errors.New("reports: this connection string cannot be decrypted with the current key")
	}
	return string(plain), nil
}

// ---------------------------------------------------------------- whole files
//
// The same key and the same construction, offered to the rest of the program
// for sealing a FILE rather than one connection string.
//
// This exists because the document index is written to disk holding the
// extracted text of every document in the archive -- supplier names, prices,
// quantities, contract terms -- and it was plain bytes. On Windows, which is
// where this runs, file permissions are close to no protection: Go's 0600 sets
// the read-only bit and nothing more, so "only the service account can read
// it" was never true there.
//
// Exported from here rather than copied into the package that needed it. The
// key underneath is DPAPI-wrapped on Windows, and security code that exists
// twice is security code that gets fixed once.

// FileBox seals and opens whole files with a key stored beside them.
type FileBox struct{ box *secretBox }

// OpenFileBox loads, or creates, the key beside dataPath.
//
// The key file is dataPath + ".key" -- the same convention the connection
// store uses, and for the same reason: a key kept somewhere else is a key that
// gets separated from the thing it opens.
func OpenFileBox(dataPath string) (*FileBox, error) {
	b, err := openSecretBox(dataPath)
	if err != nil {
		return nil, err
	}
	return &FileBox{box: b}, nil
}

// Seal encrypts a block of bytes.
//
// Raw rather than base64, unlike the string form above: this is for a file,
// which nothing needs to read as text, and base64 would add half again to
// something already tens of megabytes.
func (f *FileBox) Seal(plain []byte) ([]byte, error) {
	b := f.box
	b.mu.Lock()
	defer b.mu.Unlock()
	nonce := make([]byte, b.aead.NonceSize())
	if _, err := io.ReadFull(rand.Reader, nonce); err != nil {
		return nil, fmt.Errorf("reports: nonce: %w", err)
	}
	out := []byte{secretVersion}
	out = append(out, nonce...)
	return b.aead.Seal(out, nonce, plain, []byte{secretVersion}), nil
}

// Open reverses Seal.
//
// A failure here is not fatal for every caller, and the message does not
// pretend otherwise: the index this was written for is derived data, so a
// caller that cannot open it rebuilds it instead.
func (f *FileBox) Open(sealed []byte) ([]byte, error) {
	b := f.box
	ns := b.aead.NonceSize()
	if len(sealed) < 1+ns {
		return nil, errors.New("reports: sealed file is too short to be valid")
	}
	if sealed[0] != secretVersion {
		return nil, fmt.Errorf("reports: sealed file has format version %d, "+
			"which this build does not know", sealed[0])
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	plain, err := b.aead.Open(nil, sealed[1:1+ns], sealed[1+ns:], []byte{secretVersion})
	if err != nil {
		return nil, errors.New("reports: this file cannot be decrypted with the current key")
	}
	return plain, nil
}
