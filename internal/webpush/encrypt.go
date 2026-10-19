// Package webpush sends Web Push messages: RFC 8030 delivery, RFC 8291
// payload encryption and RFC 8292 (VAPID) sender identification.
//
// WHY WEB PUSH. A notification raised by a page exists only while the page
// does. Web Push inverts that: the server hands a message to the push service
// run by the device's platform -- Apple's for an iPhone, Microsoft's for Edge,
// Google's for Chrome -- and that service delivers it to the device, which
// wakes the installed app's service worker (or, on Safari 18.4 and later,
// shows it with no code running at all). Nothing of docfind has to be open.
//
// STANDARD LIBRARY ONLY. The whole protocol is ECDH on P-256, HKDF-SHA-256,
// AES-128-GCM and an ES256 signature, all in Go's standard library, so this
// adds no dependency to a binary whose release check has to clear every one.
//
// THE PAYLOAD IS END-TO-END ENCRYPTED to the device. The push service carries
// ciphertext it cannot read; only the browser that subscribed holds the key.
package webpush

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/ecdh"
	"crypto/hkdf"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/binary"
	"errors"
	"fmt"
)

// recordSize is the RFC 8188 record size. One record holds the whole message:
// every push service caps a payload well below 4096 bytes.
const recordSize = 4096

// MaxPayload is the largest plaintext this package will send.
//
// 4096 is the ceiling the push services agree on for the ENCRYPTED body; the
// header (86 bytes), the padding delimiter and the GCM tag (16) come out of
// it. A little under the arithmetic so a service that counts differently
// does not reject a message sitting exactly on the line.
const MaxPayload = 3900

// ErrPayloadTooLarge means the message would be refused by every push service.
var ErrPayloadTooLarge = errors.New("webpush: payload too large")

// b64 is the unpadded URL-safe alphabet every Web Push value is written in.
var b64 = base64.RawURLEncoding

// decodeB64 accepts the unpadded form browsers send, and tolerates padding,
// which some libraries add.
func decodeB64(s string) ([]byte, error) {
	for len(s)%4 != 0 && len(s) > 0 && s[len(s)-1] == '=' {
		s = s[:len(s)-1]
	}
	if b, err := b64.DecodeString(s); err == nil {
		return b, nil
	}
	return base64.URLEncoding.DecodeString(s)
}

// encrypt produces the aes128gcm body for one subscription (RFC 8291 s.3.4).
//
// salt and asPriv are parameters so the test can reproduce the RFC's worked
// example byte for byte; Encrypt below draws both fresh for every message,
// which is what makes two identical notifications look different on the wire.
func encrypt(plaintext, uaPublic, authSecret, salt []byte, asPriv *ecdh.PrivateKey) ([]byte, error) {
	if len(plaintext) > MaxPayload {
		return nil, ErrPayloadTooLarge
	}
	if len(authSecret) != 16 {
		return nil, fmt.Errorf("webpush: auth secret is %d bytes, want 16", len(authSecret))
	}
	if len(salt) != 16 {
		return nil, fmt.Errorf("webpush: salt is %d bytes, want 16", len(salt))
	}
	uaKey, err := ecdh.P256().NewPublicKey(uaPublic)
	if err != nil {
		return nil, fmt.Errorf("webpush: the subscription's public key is not a P-256 point: %w", err)
	}
	asPublic := asPriv.PublicKey().Bytes()

	ecdhSecret, err := asPriv.ECDH(uaKey)
	if err != nil {
		return nil, fmt.Errorf("webpush: key agreement failed: %w", err)
	}

	// Combine the ECDH secret with the subscription's auth secret.
	//   PRK_key  = HKDF-Extract(salt=auth_secret, IKM=ecdh_secret)
	//   key_info = "WebPush: info" || 0x00 || ua_public || as_public
	//   IKM      = HKDF-Expand(PRK_key, key_info, 32)
	prkKey, err := hkdf.Extract(sha256.New, ecdhSecret, authSecret)
	if err != nil {
		return nil, err
	}
	keyInfo := make([]byte, 0, 14+65+65)
	keyInfo = append(keyInfo, "WebPush: info\x00"...)
	keyInfo = append(keyInfo, uaPublic...)
	keyInfo = append(keyInfo, asPublic...)
	ikm, err := hkdf.Expand(sha256.New, prkKey, string(keyInfo), 32)
	if err != nil {
		return nil, err
	}

	// Derive the content key and nonce (RFC 8188 s.2.2, 2.3).
	prk, err := hkdf.Extract(sha256.New, ikm, salt)
	if err != nil {
		return nil, err
	}
	cek, err := hkdf.Expand(sha256.New, prk, "Content-Encoding: aes128gcm\x00", 16)
	if err != nil {
		return nil, err
	}
	nonce, err := hkdf.Expand(sha256.New, prk, "Content-Encoding: nonce\x00", 12)
	if err != nil {
		return nil, err
	}

	block, err := aes.NewCipher(cek)
	if err != nil {
		return nil, err
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return nil, err
	}
	// 0x02 marks the last (and only) record; no further padding. The
	// notification's length is not a secret worth the bytes: its existence
	// already says "something happened on your desk".
	padded := make([]byte, 0, len(plaintext)+1)
	padded = append(padded, plaintext...)
	padded = append(padded, 0x02)
	ciphertext := gcm.Seal(nil, nonce, padded, nil)

	// header = salt(16) || rs(4) || idlen(1) || keyid(as_public, 65)
	body := make([]byte, 0, 16+4+1+len(asPublic)+len(ciphertext))
	body = append(body, salt...)
	body = binary.BigEndian.AppendUint32(body, recordSize)
	body = append(body, byte(len(asPublic)))
	body = append(body, asPublic...)
	body = append(body, ciphertext...)
	return body, nil
}

// Encrypt encrypts plaintext for a subscription, with a fresh salt and a fresh
// ephemeral key.
func Encrypt(plaintext []byte, p256dh, auth string) ([]byte, error) {
	uaPublic, err := decodeB64(p256dh)
	if err != nil {
		return nil, fmt.Errorf("webpush: p256dh is not base64url: %w", err)
	}
	authSecret, err := decodeB64(auth)
	if err != nil {
		return nil, fmt.Errorf("webpush: auth is not base64url: %w", err)
	}
	salt := make([]byte, 16)
	if _, err := rand.Read(salt); err != nil {
		return nil, err
	}
	asPriv, err := ecdh.P256().GenerateKey(rand.Reader)
	if err != nil {
		return nil, err
	}
	return encrypt(plaintext, uaPublic, authSecret, salt, asPriv)
}

// ValidateKeys checks a subscription's keys before they are stored.
//
// Done at subscription time rather than at the first send, so a malformed
// subscription is refused to the browser that sent it -- which can say so --
// instead of failing silently in a background job hours later.
func ValidateKeys(p256dh, auth string) error {
	pub, err := decodeB64(p256dh)
	if err != nil {
		return fmt.Errorf("p256dh is not base64url")
	}
	if _, err := ecdh.P256().NewPublicKey(pub); err != nil {
		return fmt.Errorf("p256dh is not an uncompressed P-256 public key")
	}
	a, err := decodeB64(auth)
	if err != nil || len(a) != 16 {
		return fmt.Errorf("auth must be 16 bytes of base64url")
	}
	return nil
}
