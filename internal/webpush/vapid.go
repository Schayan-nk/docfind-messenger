package webpush

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"strings"
	"time"
)

// VAPID (RFC 8292): how a push service knows which server is sending.
//
// Each browser subscription is bound to one public key at the moment it is
// made. Every message after that must carry a JWT signed by the matching
// private key, or the push service refuses it. So the key pair is generated
// ONCE and kept for the life of the installation -- a new pair orphans every
// device that ever subscribed, and each one has to subscribe again by hand.

// Keys is the application server's VAPID key pair.
type Keys struct {
	priv *ecdsa.PrivateKey
}

// GenerateKeys makes a new P-256 key pair.
func GenerateKeys() (*Keys, error) {
	priv, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return nil, err
	}
	return &Keys{priv: priv}, nil
}

// ParseKeys restores a key pair from PrivateB64's output.
func ParseKeys(privateB64 string) (*Keys, error) {
	d, err := decodeB64(strings.TrimSpace(privateB64))
	if err != nil || len(d) != 32 {
		return nil, errors.New("webpush: the stored VAPID private key is not 32 bytes of base64url")
	}
	priv, err := ecdsa.ParseRawPrivateKey(elliptic.P256(), d)
	if err != nil {
		return nil, fmt.Errorf("webpush: the stored VAPID private key is invalid: %w", err)
	}
	return &Keys{priv: priv}, nil
}

// PrivateB64 is the private scalar, for storage. It is a secret: it goes into
// the encrypted secrets store and nowhere else.
func (k *Keys) PrivateB64() string {
	b, err := k.priv.Bytes()
	if err != nil {
		return ""
	}
	return b64.EncodeToString(b)
}

// PublicB64 is the uncompressed public point, which the page hands to
// PushManager.subscribe as applicationServerKey. Public by design.
func (k *Keys) PublicB64() string {
	b, err := k.priv.PublicKey.Bytes()
	if err != nil {
		return ""
	}
	return b64.EncodeToString(b)
}

// ValidSubject reports whether s is acceptable as the VAPID "sub" claim.
//
// APPLE ENFORCES THIS AND NOBODY ELSE DOES. RFC 8292 requires a mailto: or
// https: URI; web.push.apple.com answers 403 BadJwtToken for anything else --
// including a bare address, a mailto: at localhost, or a reserved TLD such as
// .invalid -- while Microsoft's and Google's services accept the same message.
// The symptom is notifications working on every Windows machine and on no
// iPhone, which is exactly the failure worth refusing at start-up.
func ValidSubject(s string) error {
	u, err := url.Parse(strings.TrimSpace(s))
	if err != nil {
		return fmt.Errorf("not a URI")
	}
	var host string
	switch u.Scheme {
	case "mailto":
		at := strings.LastIndexByte(u.Opaque, '@')
		if at <= 0 || at == len(u.Opaque)-1 {
			return fmt.Errorf("mailto: needs a full address")
		}
		host = u.Opaque[at+1:]
	case "https":
		host = u.Hostname()
	default:
		return fmt.Errorf("must start with mailto: or https:")
	}
	host = strings.ToLower(host)
	if !strings.Contains(host, ".") || host == "localhost" ||
		strings.HasSuffix(host, ".localhost") || strings.HasSuffix(host, ".invalid") ||
		strings.HasSuffix(host, ".local") || strings.HasSuffix(host, ".test") ||
		strings.HasSuffix(host, ".example") {
		return fmt.Errorf("%q is not a public domain; Apple refuses it", host)
	}
	return nil
}

// jwtLifetime is how long a signed VAPID token is valid. RFC 8292 caps it at
// 24 hours; well under that so a clock a few minutes out cannot push it over.
const jwtLifetime = 12 * time.Hour

// authorization builds the Authorization header for one endpoint.
//
// The audience is the push service's ORIGIN, not the endpoint: a token signed
// for https://web.push.apple.com is valid for every Apple endpoint, which is
// what lets one be cached per service rather than signed per message.
func (k *Keys) authorization(endpoint, subject string, now time.Time) (string, error) {
	u, err := url.Parse(endpoint)
	if err != nil {
		return "", err
	}
	aud := u.Scheme + "://" + u.Host

	header := b64.EncodeToString([]byte(`{"typ":"JWT","alg":"ES256"}`))
	claims, err := json.Marshal(map[string]any{
		"aud": aud,
		"exp": now.Add(jwtLifetime).Unix(),
		"sub": subject,
	})
	if err != nil {
		return "", err
	}
	signingInput := header + "." + b64.EncodeToString(claims)

	digest := sha256.Sum256([]byte(signingInput))
	r, s, err := ecdsa.Sign(rand.Reader, k.priv, digest[:])
	if err != nil {
		return "", err
	}
	// JWS ES256 is the raw 64-byte r||s, each left-padded to 32 bytes -- NOT
	// the ASN.1 DER that ecdsa.SignASN1 produces. A DER signature is the
	// single most common reason a hand-written VAPID sender gets 401 from
	// every service.
	sig := make([]byte, 64)
	r.FillBytes(sig[:32])
	s.FillBytes(sig[32:])
	token := signingInput + "." + b64.EncodeToString(sig)

	return "vapid t=" + token + ", k=" + k.PublicB64(), nil
}
