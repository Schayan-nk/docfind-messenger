package auth

import (
	"errors"
	"slices"
	"testing"
	"time"
)

func mutedStore(t *testing.T) (*Store, string) {
	t.Helper()
	path := tempDir(t) + "/users.json"
	s, err := OpenStore(path)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.Create("reader", "خواننده", "a-long-enough-password", RoleViewer); err != nil {
		t.Fatal(err)
	}
	return s, path
}

// The list is kept tidy whatever the request sent, and survives a restart.
func TestSetMutedCleansAndPersists(t *testing.T) {
	s, path := mutedStore(t)
	got, err := s.SetMuted("reader", []string{" Meeting-New ", "case", "case", "", "bad name", "x;drop"})
	if err != nil {
		t.Fatal(err)
	}
	if want := []string{"case", "meeting-new"}; !slices.Equal(got.Muted, want) {
		t.Fatalf("Muted = %q, want %q", got.Muted, want)
	}
	again, err := OpenStore(path)
	if err != nil {
		t.Fatal(err)
	}
	u, _ := again.Get("reader")
	if !u.Prefs.MutedTopic("case") || !u.Prefs.MutedTopic("meeting-new") || u.Prefs.MutedTopic("meeting-cancel") {
		t.Errorf("after reopening, Muted = %q", u.Prefs.Muted)
	}
	// Switching everything back on stores nothing at all.
	if got, _ := s.SetMuted("reader", nil); got.Muted != nil {
		t.Errorf("empty list stored as %q", got.Muted)
	}
}

// Saving the appearance form must not switch every notification back on.
func TestSetPrefsKeepsMutedTopics(t *testing.T) {
	s, _ := mutedStore(t)
	if _, err := s.SetMuted("reader", []string{"case"}); err != nil {
		t.Fatal(err)
	}
	saved, err := s.SetPrefs("reader", Prefs{FontScale: 110, Muted: nil})
	if err != nil {
		t.Fatal(err)
	}
	if !saved.MutedTopic("case") {
		t.Errorf("SetPrefs dropped the muted topics: %q", saved.Muted)
	}
	// Nor can the appearance form set them.
	saved, _ = s.SetPrefs("reader", Prefs{Muted: []string{"meeting-new"}})
	if saved.MutedTopic("meeting-new") {
		t.Error("SetPrefs changed the muted topics")
	}
}

// A copy handed out is a copy: changing its slice changes nothing stored.
func TestMutedTopicsAreCopiedOut(t *testing.T) {
	s, _ := mutedStore(t)
	s.SetMuted("reader", []string{"case"})
	u, _ := s.Get("reader")
	u.Prefs.Muted[0] = "meeting-new"
	again, _ := s.Get("reader")
	if !again.Prefs.MutedTopic("case") {
		t.Errorf("stored list changed through a copy: %q", again.Prefs.Muted)
	}
}

// Quiet hours wrap midnight, count the start hour and not the end one, and
// equal hours are none.
func TestQuietHours(t *testing.T) {
	at := func(h int) time.Time { return time.Date(2026, 9, 24, h, 30, 0, 0, time.Local) }
	night := Prefs{QuietFrom: 22, QuietTo: 7}
	for h, want := range map[int]bool{21: false, 22: true, 23: true, 0: true, 6: true, 7: false, 12: false} {
		if got := night.Quiet(at(h)); got != want {
			t.Errorf("22-7 at %d:30 = %v, want %v", h, got, want)
		}
	}
	lunch := Prefs{QuietFrom: 13, QuietTo: 14}
	if !lunch.Quiet(at(13)) || lunch.Quiet(at(14)) || lunch.Quiet(at(12)) {
		t.Error("13-14 should be quiet at 13:30 only")
	}
	if (Prefs{QuietFrom: 5, QuietTo: 5}).Quiet(at(5)) || (Prefs{}).Quiet(at(0)) {
		t.Error("equal hours must mean no quiet hours")
	}
}

// SetQuiet validates, persists, and survives an appearance save.
func TestSetQuietPersistsAndSurvivesSetPrefs(t *testing.T) {
	s, path := mutedStore(t)
	if _, err := s.SetQuiet("reader", 24, 7); !errors.Is(err, ErrQuietHours) {
		t.Fatalf("hour 24: err = %v, want ErrQuietHours", err)
	}
	if _, err := s.SetQuiet("reader", 22, -1); !errors.Is(err, ErrQuietHours) {
		t.Fatalf("hour -1: err = %v, want ErrQuietHours", err)
	}
	if _, err := s.SetQuiet("reader", 22, 7); err != nil {
		t.Fatal(err)
	}
	if _, err := s.SetPrefs("reader", Prefs{FontScale: 110}); err != nil {
		t.Fatal(err)
	}
	again, err := OpenStore(path)
	if err != nil {
		t.Fatal(err)
	}
	u, _ := again.Get("reader")
	if u.Prefs.QuietFrom != 22 || u.Prefs.QuietTo != 7 || u.Prefs.FontScale != 110 {
		t.Errorf("after an appearance save and a reopen, prefs = %+v", u.Prefs)
	}
	// Equal hours switch them off and store nothing.
	got, err := s.SetQuiet("reader", 9, 9)
	if err != nil || got.QuietFrom != 0 || got.QuietTo != 0 {
		t.Errorf("9-9: %+v, %v", got, err)
	}
	// A hand-edited file with nonsense hours is read as none.
	if p := (Prefs{QuietFrom: 30, QuietTo: 7}).Normalised(); p.QuietFrom != 0 || p.QuietTo != 0 {
		t.Errorf("Normalised kept bad hours: %+v", p)
	}
}
