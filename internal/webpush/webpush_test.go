package webpush

import (
	"bytes"
	"context"
	"crypto/aes"
	"crypto/cipher"
	"crypto/ecdh"
	"crypto/hkdf"
	"crypto/rand"
	"crypto/sha256"
	"encoding/binary"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func mustB64(t *testing.T, s string) []byte {
	t.Helper()
	b, err := decodeB64(s)
	if err != nil {
		t.Fatalf("bad base64url in the test itself: %q: %v", s, err)
	}
	return b
}

// decrypt is the RECEIVING side of RFC 8291, written independently of
// encrypt. A test that only compared encrypt with itself would prove nothing;
// this is what a browser does with the body.
func decrypt(body []byte, uaPriv *ecdh.PrivateKey, authSecret []byte) ([]byte, error) {
	if len(body) < 21 {
		return nil, errors.New("short body")
	}
	salt := body[:16]
	rs := binary.BigEndian.Uint32(body[16:20])
	idlen := int(body[20])
	if rs != recordSize || len(body) < 21+idlen {
		return nil, errors.New("bad header")
	}
	asPublic := body[21 : 21+idlen]
	ciphertext := body[21+idlen:]

	asKey, err := ecdh.P256().NewPublicKey(asPublic)
	if err != nil {
		return nil, err
	}
	secret, err := uaPriv.ECDH(asKey)
	if err != nil {
		return nil, err
	}
	uaPublic := uaPriv.PublicKey().Bytes()
	info := append(append([]byte("WebPush: info\x00"), uaPublic...), asPublic...)
	prkKey, _ := hkdf.Extract(sha256.New, secret, authSecret)
	ikm, _ := hkdf.Expand(sha256.New, prkKey, string(info), 32)
	prk, _ := hkdf.Extract(sha256.New, ikm, salt)
	cek, _ := hkdf.Expand(sha256.New, prk, "Content-Encoding: aes128gcm\x00", 16)
	nonce, _ := hkdf.Expand(sha256.New, prk, "Content-Encoding: nonce\x00", 12)
	block, _ := aes.NewCipher(cek)
	gcm, _ := cipher.NewGCM(block)
	plain, err := gcm.Open(nil, nonce, ciphertext, nil)
	if err != nil {
		return nil, err
	}
	// Strip padding: trailing zeros, then the 0x02 last-record delimiter.
	i := len(plain) - 1
	for i >= 0 && plain[i] == 0 {
		i--
	}
	if i < 0 || plain[i] != 0x02 {
		return nil, errors.New("no last-record delimiter")
	}
	return plain[:i], nil
}

// THE RFC'S OWN WORKED EXAMPLE (RFC 8291 Appendix A).
//
// Fixed keys and a fixed salt, and the exact bytes a correct implementation
// must produce. Passing this is the difference between "the code runs" and
// "a push service will accept what the code sends".
func TestEncryptionMatchesTheRFC8291Example(t *testing.T) {
	plaintext := []byte("When I grow up, I want to be a watermelon")
	asPrivD := mustB64(t, "yfWPiYE-n46HLnH0KqZOF1fJJU3MYrct3AELtAQ-oRw")
	uaPublic := mustB64(t, "BCVxsr7N_eNgVRqvHtD0zTZsEc6-VV-JvLexhqUzORcxaOzi6-AYWXvTBHm4bjyPjs7Vd8pZGH6SRpkNtoIAiw4")
	uaPrivD := mustB64(t, "q1dXpw3UpT5VOmu_cf_v6ih07Aems3njxI-JWgLcM94")
	salt := mustB64(t, "DGv6ra1nlYgDCS1FRnbzlw")
	authSecret := mustB64(t, "BTBZMqHH6r4Tts7J_aSIgg")
	wantHeader := mustB64(t, "DGv6ra1nlYgDCS1FRnbzlwAAEABBBP4z9KsN6nGRTbVYI_c7VJSPQTBtkgcy27mlmlMoZIIgDll6e3vCYLocInmYWAmS6TlzAC8wEqKK6PBru3jl7A8")
	// One character of this value was mis-copied on the way in ('Z' where the
	// RFC has 'r', after "QUrdi"). Settled by decryption rather than by
	// trusting either copy: under the RFC's own keys the 'Z' form FAILS GCM
	// authentication, and this form authenticates and decrypts to the RFC's
	// plaintext. A GCM tag covers every ciphertext byte, so only one of the
	// two can be genuine.
	wantCiphertext := mustB64(t, "8pfeW0KbunFT06SuDKoJH9Ql87S1QUrdirN6GcG7sFz1y1sqLgVi1VhjVkHsUoEsbI_0LpXMuGvnzQ")

	asPriv, err := ecdh.P256().NewPrivateKey(asPrivD)
	if err != nil {
		t.Fatal(err)
	}
	// The RFC's application-server public key, as a check on the key the
	// test built from its private half.
	if got := b64.EncodeToString(asPriv.PublicKey().Bytes()); got !=
		"BP4z9KsN6nGRTbVYI_c7VJSPQTBtkgcy27mlmlMoZIIgDll6e3vCYLocInmYWAmS6TlzAC8wEqKK6PBru3jl7A8" {
		t.Fatalf("as_public = %s; the test vector was transcribed wrongly", got)
	}

	body, err := encrypt(plaintext, uaPublic, authSecret, salt, asPriv)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(body[:len(wantHeader)], wantHeader) {
		t.Errorf("header differs from the RFC:\n got  %x\n want %x", body[:len(wantHeader)], wantHeader)
	}
	if !bytes.Equal(body[len(wantHeader):], wantCiphertext) {
		t.Errorf("ciphertext differs from the RFC:\n got  %s\n want %s",
			b64.EncodeToString(body[len(wantHeader):]), b64.EncodeToString(wantCiphertext))
	}

	// And the receiving side, independently, recovers the text.
	uaPriv, err := ecdh.P256().NewPrivateKey(uaPrivD)
	if err != nil {
		t.Fatal(err)
	}
	got, err := decrypt(body, uaPriv, authSecret)
	if err != nil {
		t.Fatalf("the user agent could not decrypt: %v", err)
	}
	if string(got) != string(plaintext) {
		t.Errorf("decrypted %q, want %q", got, plaintext)
	}
}

// A browser's subscription keys, generated as a browser would.
func browser(t *testing.T) (*ecdh.PrivateKey, []byte, string, string) {
	t.Helper()
	priv, err := ecdh.P256().GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	auth := make([]byte, 16)
	rand.Read(auth)
	return priv, auth, b64.EncodeToString(priv.PublicKey().Bytes()), b64.EncodeToString(auth)
}

func TestEveryMessageIsFreshlyKeyedAndStillDecrypts(t *testing.T) {
	priv, auth, p256dh, authB64 := browser(t)
	msg := []byte(`{"web_push":8030,"notification":{"title":"کار جدید در کارتابل","navigate":"/"}}`)
	a, err := Encrypt(msg, p256dh, authB64)
	if err != nil {
		t.Fatal(err)
	}
	b, _ := Encrypt(msg, p256dh, authB64)
	if bytes.Equal(a, b) {
		t.Error("two encryptions of the same message are identical; the salt or key is not fresh")
	}
	for _, body := range [][]byte{a, b} {
		got, err := decrypt(body, priv, auth)
		if err != nil || !bytes.Equal(got, msg) {
			t.Errorf("round trip failed: %v, %q", err, got)
		}
	}
}

func TestAnOversizedPayloadIsRefusedNotTruncated(t *testing.T) {
	_, _, p256dh, auth := browser(t)
	if _, err := Encrypt(make([]byte, MaxPayload+1), p256dh, auth); !errors.Is(err, ErrPayloadTooLarge) {
		t.Errorf("got %v, want ErrPayloadTooLarge", err)
	}
}

func TestMalformedSubscriptionKeysAreRefusedAtTheDoor(t *testing.T) {
	_, _, p256dh, auth := browser(t)
	if err := ValidateKeys(p256dh, auth); err != nil {
		t.Errorf("a real browser's keys were refused: %v", err)
	}
	for _, c := range [][2]string{
		{"not-base64!!", auth},
		{b64.EncodeToString(make([]byte, 65)), auth}, // all zeros: not on the curve
		{p256dh, b64.EncodeToString(make([]byte, 8))},
		{p256dh, ""},
	} {
		if ValidateKeys(c[0], c[1]) == nil {
			t.Errorf("accepted p256dh=%q auth=%q", c[0], c[1])
		}
	}
}

// ---------------------------------------------------------------- VAPID

func TestTheVAPIDKeyPairSurvivesStorage(t *testing.T) {
	k, err := GenerateKeys()
	if err != nil {
		t.Fatal(err)
	}
	back, err := ParseKeys(k.PrivateB64())
	if err != nil {
		t.Fatal(err)
	}
	if back.PublicB64() != k.PublicB64() {
		t.Error("a stored and restored key has a different public half -- every device would have to resubscribe")
	}
	if pub := mustB64(t, k.PublicB64()); len(pub) != 65 || pub[0] != 0x04 {
		t.Errorf("the public key is not a 65-byte uncompressed point: %d bytes", len(pub))
	}
	if _, err := ParseKeys("garbage"); err == nil {
		t.Error("a garbage private key was accepted")
	}
}

// Apple refuses what the others accept -- tested against the shapes that
// broke other projects' iOS delivery.
func TestTheSubjectIsOneAppleWillAccept(t *testing.T) {
	for _, ok := range []string{"mailto:docfind@example.com", "https://app.example.ir", "mailto:it@example.org"} {
		if err := ValidSubject(ok); err != nil {
			t.Errorf("%q refused: %v", ok, err)
		}
	}
	for _, bad := range []string{
		"docfind@example.com", "mailto:admin@localhost", "mailto:x@gateway.invalid",
		"http://docfind.example.ir", "https://localhost", "mailto:", "mailto:@example.com",
		"https://docfind.local", "",
	} {
		if ValidSubject(bad) == nil {
			t.Errorf("%q accepted; Apple would answer 403 BadJwtToken", bad)
		}
	}
}

// ---------------------------------------------------------------- endpoints

func TestOnlyRealPushServicesAreReachable(t *testing.T) {
	var s Sender
	for _, ok := range []string{
		"https://web.push.apple.com/QGuQyavXutnMH-b8nf1eZ29B",
		"https://fcm.googleapis.com/fcm/send/abc:def",
		"https://sg2p.notify.windows.com/w/?token=AQE",
		"https://db5p.notify.windows.com/w/?token=x",
		"https://updates.push.services.mozilla.com/wpush/v2/gAAA",
	} {
		if err := s.AllowedEndpoint(ok); err != nil {
			t.Errorf("%s refused: %v", ok, err)
		}
	}
	for _, bad := range []string{
		"http://web.push.apple.com/x",           // not https
		"https://192.168.77.4/api/whatever",     // the office network
		"https://127.0.0.1:5432/",               // this server's database
		"https://evilnotify.windows.com/x",      // suffix without the dot
		"https://notify.windows.com.evil.com/x", // lookalike
		"https://web.push.apple.com:8443/x",     // odd port
		"https://user:pw@fcm.googleapis.com/x",  // credentials
		"https://example.com/push",
		"javascript:alert(1)",
	} {
		if s.AllowedEndpoint(bad) == nil {
			t.Errorf("%s accepted; the server could be made to POST anywhere", bad)
		}
	}
	// A deployment can add a host, and only that host.
	s.Hosts = []string{"push.example.org"}
	if s.AllowedEndpoint("https://push.example.org/x") != nil {
		t.Error("a configured extra host was refused")
	}
}

// ---------------------------------------------------------------- sending

func TestSendDeliversAnEncryptedMessageWithTheRightHeaders(t *testing.T) {
	priv, auth, p256dh, authB64 := browser(t)
	k, _ := GenerateKeys()
	var gotBody []byte
	var gotHeader http.Header
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotHeader = r.Header.Clone()
		gotBody, _ = io.ReadAll(r.Body)
		w.WriteHeader(http.StatusCreated)
	}))
	defer srv.Close()

	s := &Sender{Keys: k, Subject: "mailto:docfind@example.com", Client: srv.Client(), allowAnyEndpoint: true}
	payload := []byte(`{"title":"test"}`)
	res := s.Send(context.Background(), Subscription{Endpoint: srv.URL + "/push/1", P256dh: p256dh, Auth: authB64},
		Message{Payload: payload, TTL: time.Hour, Urgency: "high", Topic: "case-1234/ok?"})

	if res.Outcome != Delivered || res.Status != 201 {
		t.Fatalf("result = %+v, want delivered 201", res)
	}
	for k, want := range map[string]string{
		"Ttl": "3600", "Content-Encoding": "aes128gcm", "Urgency": "high",
		"Topic": "case-1234ok", // sanitised to the URL-safe alphabet
	} {
		if got := gotHeader.Get(k); got != want {
			t.Errorf("%s = %q, want %q", k, got, want)
		}
	}
	if !strings.HasPrefix(gotHeader.Get("Authorization"), "vapid t=") {
		t.Errorf("Authorization = %q", gotHeader.Get("Authorization"))
	}
	plain, err := decrypt(gotBody, priv, auth)
	if err != nil || string(plain) != string(payload) {
		t.Errorf("the device could not read what was sent: %v %q", err, plain)
	}
}

func TestEachStatusMapsToWhatTheQueueShouldDo(t *testing.T) {
	now := time.Now()
	for _, c := range []struct {
		status int
		want   Outcome
	}{
		{200, Delivered}, {201, Delivered}, {202, Delivered},
		{404, Gone}, {410, Gone},
		{401, Unauthorized}, {403, Unauthorized},
		{429, Retry}, {500, Retry}, {502, Retry}, {503, Retry},
		{400, Rejected}, {413, Rejected},
		{418, Retry},
	} {
		if got := classify(c.status, "", "", now).Outcome; got != c.want {
			t.Errorf("HTTP %d -> %v, want %v", c.status, got, c.want)
		}
	}
	if r := classify(429, "120", "", now); r.RetryAfter != 2*time.Minute {
		t.Errorf("Retry-After 120 -> %v", r.RetryAfter)
	}
	date := now.Add(5 * time.Minute).UTC().Format(http.TimeFormat)
	if r := classify(503, date, "", now); r.RetryAfter < 4*time.Minute || r.RetryAfter > 5*time.Minute {
		t.Errorf("Retry-After as a date -> %v", r.RetryAfter)
	}
	// Apple's one-word diagnosis survives into the reason.
	if r := classify(403, "", `{"reason":"BadJwtToken"}`, now); !strings.Contains(r.Reason, "BadJwtToken") {
		t.Errorf("reason = %q", r.Reason)
	}
}

// A redirect is not followed: that would be a way round the allowlist.
func TestARedirectIsNotFollowed(t *testing.T) {
	_, _, p256dh, auth := browser(t)
	k, _ := GenerateKeys()
	followed := false
	target := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { followed = true }))
	defer target.Close()
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, target.URL, http.StatusTemporaryRedirect)
	}))
	defer srv.Close()
	s := &Sender{Keys: k, Subject: "mailto:a@example.com", Client: srv.Client(), allowAnyEndpoint: true}
	res := s.Send(context.Background(), Subscription{Endpoint: srv.URL, P256dh: p256dh, Auth: auth}, Message{Payload: []byte("x")})
	if followed {
		t.Error("the redirect was followed")
	}
	if res.Outcome == Delivered {
		t.Errorf("a redirect counted as delivered: %+v", res)
	}
}

func TestANetworkFailureIsRetriedNotDropped(t *testing.T) {
	_, _, p256dh, auth := browser(t)
	k, _ := GenerateKeys()
	srv := httptest.NewTLSServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
	url := srv.URL
	srv.Close() // nothing listening now
	s := &Sender{Keys: k, Subject: "mailto:a@example.com", allowAnyEndpoint: true,
		Client: &http.Client{Timeout: 2 * time.Second}}
	res := s.Send(context.Background(), Subscription{Endpoint: url, P256dh: p256dh, Auth: auth}, Message{Payload: []byte("x")})
	if res.Outcome != Retry {
		t.Errorf("an unreachable push service -> %v, want retry", res.Outcome)
	}
}

func TestTheAllowlistAppliesToRealSends(t *testing.T) {
	_, _, p256dh, auth := browser(t)
	k, _ := GenerateKeys()
	s := &Sender{Keys: k, Subject: "mailto:a@example.com"}
	res := s.Send(context.Background(), Subscription{Endpoint: "https://192.168.77.4/x", P256dh: p256dh, Auth: auth}, Message{Payload: []byte("x")})
	if res.Outcome != Rejected || !strings.Contains(res.Reason, "endpoint refused") {
		t.Errorf("an internal address was not refused: %+v", res)
	}
}
