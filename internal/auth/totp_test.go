package auth

import (
	"strings"
	"testing"
	"time"
)

func TestRecoveryCodesAreSingleUseAndStoredHashed(t *testing.T) {
	codes, hashes, err := NewRecoveryCodes(RecoveryCodeCount)
	if err != nil || len(codes) != RecoveryCodeCount || len(hashes) != RecoveryCodeCount {
		t.Fatalf("%d codes, %d hashes, %v", len(codes), len(hashes), err)
	}
	seen := map[string]bool{}
	for i, c := range codes {
		if len(c) != 11 || c[5] != '-' || strings.ContainsAny(c, "01ILO") {
			t.Errorf("code %q is not xxxxx-xxxxx from the unambiguous alphabet", c)
		}
		if seen[c] {
			t.Errorf("code %q issued twice", c)
		}
		seen[c] = true
		if strings.Contains(hashes[i], strings.ReplaceAll(c, "-", "")) {
			t.Error("a stored hash contains its code")
		}
	}
	// Typed carelessly -- lower case, no dash, spaces -- it still counts.
	typed := " " + strings.ToLower(strings.ReplaceAll(codes[3], "-", "")) + " "
	rest, ok := useRecoveryCode(hashes, typed)
	if !ok || len(rest) != RecoveryCodeCount-1 {
		t.Fatalf("a valid code was refused (ok=%v, %d left)", ok, len(rest))
	}
	if _, ok := useRecoveryCode(rest, codes[3]); ok {
		t.Error("a spent recovery code worked again")
	}
	if _, ok := useRecoveryCode(rest, "AAAAA-AAAAA"); ok {
		t.Error("a made-up code was accepted")
	}
}

func TestTheSetupAddressIsWhatAppsRead(t *testing.T) {
	uri := TOTPURI("docfind", "r.ahmadi", "JBSWY3DPEHPK3PXP")
	for _, want := range []string{"otpauth://totp/docfind:r.ahmadi?", "secret=JBSWY3DPEHPK3PXP", "issuer=docfind"} {
		if !strings.Contains(uri, want) {
			t.Errorf("%s lacks %q", uri, want)
		}
	}
}

// A challenge is not a session and a session is not a challenge.
func TestAChallengeIsNeverASession(t *testing.T) {
	s := NewSigner([]byte("test-signing-key-32-bytes-long!!"), time.Hour)
	ch, _, err := s.SignChallenge("ali", 3)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.Verify(ch); err == nil {
		t.Error("a second-step challenge was accepted as a session")
	}
	c, err := s.VerifyChallenge(ch)
	if err != nil || c.Subject != "ali" || c.Version != 3 || c.SID == "" {
		t.Errorf("the challenge did not verify as one: %+v, %v", c, err)
	}
	sess, _, _ := s.SignSession("ali", 3, "sid")
	if _, err := s.VerifyChallenge(sess); err == nil {
		t.Error("a session was accepted as a second-step challenge")
	}
}
