package auth

import (
	"strings"
	"testing"
)

func TestAMenuArrangementIsChecked(t *testing.T) {
	good := []MenuNode{
		{Href: "/"},
		{Name: "  کارهای  من ", Icon: "star", Items: []string{"/orders", "/dashboard#/warehouse"}},
		{Name: "خالی"},
		{Href: "/inbox"},
	}
	got, err := CleanMenu(good)
	if err != nil {
		t.Fatal(err)
	}
	if got[1].Name != "کارهای من" || got[1].Icon != "star" || len(got[1].Items) != 2 || got[2].Items == nil {
		t.Errorf("normalised: %+v", got)
	}

	for name, bad := range map[string][]MenuNode{
		"a page twice":        {{Href: "/orders"}, {Name: "g", Items: []string{"/orders"}}},
		"another site":        {{Href: "https://evil.example/"}},
		"protocol-relative":   {{Href: "//evil.example/"}},
		"a script":            {{Href: "javascript:alert(1)"}},
		"a quote in the path": {{Href: `/x" onmouseover="y`}},
		"both page and group": {{Href: "/orders", Name: "g"}},
		"neither":             {{}},
		"a name too long":     {{Name: strings.Repeat("ن", 41)}},
		"too many places":     make([]MenuNode, MaxMenuNodes+1),
	} {
		if _, err := CleanMenu(bad); err == nil {
			t.Errorf("%s was accepted", name)
		}
	}

	// A name carries no markup: the characters that could open a tag or an
	// attribute are dropped, and an icon that is not a sprite name is too.
	got, err = CleanMenu([]MenuNode{{Name: `<img src=x onerror="a">گروه`, Icon: "x\"><script>"}})
	if err != nil {
		t.Fatal(err)
	}
	if strings.ContainsAny(got[0].Name, `<>"'&`+"`") || got[0].Icon != "" {
		t.Errorf("markup kept: %+v", got[0])
	}
}

// Entries taken out with − are pages of this application, kept once each, kept
// through an appearance save and an arrangement save, and all back with nil.
func TestEntriesTakenOutOfTheMenu(t *testing.T) {
	s := newStore(t)
	if _, err := s.Create("alice", "Alice", "a-good-password", RoleViewer); err != nil {
		t.Fatal(err)
	}
	p, err := s.SetMenuHidden("alice", []string{" /couriers ", "/downloads", "/couriers"})
	if err != nil {
		t.Fatal(err)
	}
	if len(p.Hidden) != 2 || p.Hidden[0] != "/couriers" || p.Hidden[1] != "/downloads" {
		t.Errorf("stored %q, want [/couriers /downloads]", p.Hidden)
	}
	for _, bad := range [][]string{{"https://evil.example"}, {"//evil.example"}, {"/x\"onmouseover"}, {"javascript:alert(1)"}} {
		if _, err := s.SetMenuHidden("alice", bad); err == nil {
			t.Errorf("%q was accepted", bad)
		}
	}
	if _, err := s.SetPrefs("alice", Prefs{FontScale: 110}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.SetMenu("alice", []MenuNode{{Href: "/orders"}}, nil); err != nil {
		t.Fatal(err)
	}
	if u, _ := s.Get("alice"); len(u.Prefs.Hidden) != 2 {
		t.Errorf("an appearance or arrangement save lost the entries taken out: %+v", u.Prefs)
	}
	if _, err := s.SetMenuHidden("alice", nil); err != nil {
		t.Fatal(err)
	}
	if u, _ := s.Get("alice"); u.Prefs.Hidden != nil {
		t.Errorf("nil should bring every entry back, kept %q", u.Prefs.Hidden)
	}
	if _, err := s.SetMenuHidden("nobody", []string{"/"}); err == nil {
		t.Error("an unknown account was accepted")
	}
}
