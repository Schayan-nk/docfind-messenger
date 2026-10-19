package find

import (
	"crypto/ecdh"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"testing"
)

// subscriptionBody is what a real browser posts: a P-256 key and a 16-byte
// auth secret, at an endpoint of the given host.
func subscriptionBody(t *testing.T, endpoint string) string {
	t.Helper()
	k, err := ecdh.P256().GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	auth := make([]byte, 16)
	rand.Read(auth)
	b, _ := json.Marshal(map[string]any{
		"endpoint": endpoint,
		"keys": map[string]string{
			"p256dh": base64.RawURLEncoding.EncodeToString(k.PublicKey().Bytes()),
			"auth":   base64.RawURLEncoding.EncodeToString(auth),
		},
		"label": "iPhone",
	})
	return string(b)
}

// ---------------------------------------------------------------- watchers
