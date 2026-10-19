package chat

import (
	"strings"
	"testing"
	"time"
)

func TestUTF16Len(t *testing.T) {
	for _, tc := range []struct {
		in   string
		want int
	}{
		{"", 0}, {"abc", 3}, {"سلام", 4}, {"😀", 2}, {"a😀b", 4},
	} {
		if got := UTF16Len(tc.in); got != tc.want {
			t.Errorf("UTF16Len(%q) = %d, want %d", tc.in, got, tc.want)
		}
	}
}

func TestSplitTextKeepsEveryCharacterAndRespectsTheLimit(t *testing.T) {
	word := "کلمه "
	long := strings.Repeat(word, 2000) // 10,000 units
	parts := SplitText(long)
	if len(parts) < 3 {
		t.Fatalf("got %d parts, want at least 3", len(parts))
	}
	joined := 0
	for _, p := range parts {
		if n := UTF16Len(p); n > MaxText || n == 0 {
			t.Errorf("part of %d units", n)
		}
		if strings.HasPrefix(p, " ") || strings.HasSuffix(p, " ") {
			t.Errorf("part was not cut at a space: %q...", p[:10])
		}
		joined += len(strings.Fields(p))
	}
	if joined != 2000 {
		t.Errorf("words after split = %d, want 2000", joined)
	}
}

func TestSplitTextNeverBreaksASurrogatePair(t *testing.T) {
	s := strings.Repeat("😀", 3000) // 6,000 units, no spaces
	for _, p := range SplitText(s) {
		if UTF16Len(p) > MaxText {
			t.Fatalf("part over the limit")
		}
		if strings.ContainsRune(p, '�') {
			t.Fatalf("a surrogate pair was split")
		}
	}
}

func TestShortTextIsOnePart(t *testing.T) {
	if got := SplitText("سلام"); len(got) != 1 || got[0] != "سلام" {
		t.Fatalf("got %q", got)
	}
}

func TestNormalizeFoldsPersianVariants(t *testing.T) {
	for _, tc := range []struct{ a, b string }{
		{"كتاب", "کتاب"},
		{"يك", "یک"},
		{"می‌خواهم", "میخواهم"},
		{"ســلام", "سلام"},
		{"۱۲۳", "123"},
		{"١٢٣", "123"},
		{"DocFind", "docfind"},
	} {
		if Normalize(tc.a) != Normalize(tc.b) {
			t.Errorf("Normalize(%q)=%q, Normalize(%q)=%q", tc.a, Normalize(tc.a), tc.b, Normalize(tc.b))
		}
	}
}

func TestCleanTextDropsControlCharactersButKeepsLines(t *testing.T) {
	got := CleanText("خط اول\r\nخط دوم\x00\x07  \n")
	if got != "خط اول\nخط دوم" {
		t.Fatalf("got %q", got)
	}
}

func TestClientIDs(t *testing.T) {
	if !ValidClientID("01928c5e-7a3b-7c2d-8e9f-0a1b2c3d4e5f") {
		t.Error("a UUIDv7 was refused")
	}
	for _, bad := range []string{"", "1", "../../etc", "01928c5e7a3b7c2d8e9f0a1b2c3d4e5f", "<script>"} {
		if ValidClientID(bad) {
			t.Errorf("%q accepted", bad)
		}
	}
	a, b := derivedID("01928c5e-7a3b-7c2d-8e9f-0a1b2c3d4e5f", 1), derivedID("01928c5e-7a3b-7c2d-8e9f-0a1b2c3d4e5f", 1)
	if a != b || !ValidClientID(a) || a == derivedID("01928c5e-7a3b-7c2d-8e9f-0a1b2c3d4e5f", 2) {
		t.Errorf("derived ids: %s %s", a, b)
	}
}

func TestHandles(t *testing.T) {
	for in, want := range map[string]bool{"shayan": true, "abcd": false, "a_b_c_1": true, "شایان_k": false, strings.Repeat("a", 33): false} {
		if ValidHandle(in) != want {
			t.Errorf("ValidHandle(%q) != %v", in, want)
		}
	}
}

func TestLimiterAllowsBurstThenRefuses(t *testing.T) {
	l := newLimiter(20, 20)
	ok := 0
	for i := 0; i < 40; i++ {
		if l.allow("u") {
			ok++
		}
	}
	if ok < 20 || ok > 21 {
		t.Fatalf("allowed %d of 40 at once, want 20", ok)
	}
	if !l.allow("someone-else") {
		t.Fatal("one person's budget was shared with another")
	}
}

func TestPrivacyRules(t *testing.T) {
	now := time.Date(2026, 10, 13, 12, 0, 0, 0, time.UTC)
	hour := now.Add(-time.Hour)
	everybody, nobody := Rule{}, Rule{Mode: "nobody"}
	contacts := Rule{Mode: "contacts"}

	p := &Person{ID: "b"}
	presenceFor(p, "a", everybody, everybody, false, false, &hour, now)
	if p.LastSeen == "" || p.Seen != "" {
		t.Errorf("everybody: exact time expected, got %+v", p)
	}

	p = &Person{ID: "b"}
	presenceFor(p, "a", nobody, everybody, false, true, &hour, now)
	if p.Online || p.LastSeen != "" || p.Seen != "recently" {
		t.Errorf("nobody: only 'recently' expected, got %+v", p)
	}

	p = &Person{ID: "b"}
	presenceFor(p, "a", contacts, everybody, true, false, &hour, now)
	if p.LastSeen == "" {
		t.Errorf("contacts + is contact: exact expected, got %+v", p)
	}

	// A viewer who hides their own last seen cannot see anyone else's.
	p = &Person{ID: "b"}
	presenceFor(p, "a", everybody, nobody, false, false, &hour, now)
	if p.LastSeen != "" || p.Seen != "recently" {
		t.Errorf("reciprocal rule broken: %+v", p)
	}

	// Never and Always lists win over the mode.
	if (Rule{Mode: "everybody", Never: []string{"a"}}).Allows("a", true) {
		t.Error("Never list ignored")
	}
	if !(Rule{Mode: "nobody", Always: []string{"a"}}).Allows("a", false) {
		t.Error("Always list ignored")
	}
}
