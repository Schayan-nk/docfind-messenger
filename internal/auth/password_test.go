package auth

import (
	"strings"
	"testing"
)

func TestHashAndVerifyRoundTrip(t *testing.T) {
	const plain = "correct horse battery staple"
	encoded, err := HashPassword(plain)
	if err != nil {
		t.Fatalf("HashPassword: %v", err)
	}

	ok, err := VerifyPassword(encoded, plain)
	if err != nil {
		t.Fatalf("VerifyPassword: %v", err)
	}
	if !ok {
		t.Error("the correct password did not verify")
	}

	ok, err = VerifyPassword(encoded, "correct horse battery stapl")
	if err != nil {
		t.Fatalf("VerifyPassword: %v", err)
	}
	if ok {
		t.Error("a wrong password verified")
	}
}

// The stored form must never contain the password, and must carry its own
// parameters so the cost can be raised later without locking anyone out.
func TestHashFormatIsSelfDescribing(t *testing.T) {
	const plain = "hunter2"
	encoded, err := HashPassword(plain)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(encoded, plain) {
		t.Fatalf("the plaintext password appears in the stored hash: %q", encoded)
	}
	parts := strings.Split(encoded, "$")
	if len(parts) != 4 {
		t.Fatalf("expected 4 fields, got %d: %q", len(parts), encoded)
	}
	if parts[0] != hashScheme {
		t.Errorf("scheme = %q, want %q", parts[0], hashScheme)
	}
}

// Two accounts with the same password must not share a hash, or one cracked
// password would reveal every account that reused it.
func TestSaltsAreUnique(t *testing.T) {
	a, err := HashPassword("same password")
	if err != nil {
		t.Fatal(err)
	}
	b, err := HashPassword("same password")
	if err != nil {
		t.Fatal(err)
	}
	if a == b {
		t.Error("hashing the same password twice produced identical output")
	}
}

// A hash that cannot be parsed must fail closed. Returning "no match" here
// rather than an error would be survivable, but returning "match" for a
// corrupt record would be an authentication bypass.
func TestMalformedHashNeverMatches(t *testing.T) {
	for _, bad := range []string{
		"",
		"garbage",
		"pbkdf2-sha256$notanumber$c2FsdA$aGFzaA",
		"pbkdf2-sha256$600000$!!!notbase64!!!$aGFzaA",
		"scrypt$600000$c2FsdA$aGFzaA", // right shape, unsupported scheme
		"pbkdf2-sha256$0$c2FsdA$aGFzaA",
	} {
		ok, err := VerifyPassword(bad, "anything")
		if ok {
			t.Errorf("malformed hash %q reported a match", bad)
		}
		if err == nil {
			t.Errorf("malformed hash %q returned no error", bad)
		}
	}
}

func TestVerifyUnknownUserDoesNotPanic(t *testing.T) {
	// Its only job is to burn comparable work on the "no such user" path so
	// timing does not reveal which usernames exist.
	VerifyUnknownUser("whatever")
}
