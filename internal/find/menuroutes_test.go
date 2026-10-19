package find

import (
	"net/http"
	"strings"
	"testing"
)

// One's own arrangement of the menu: saved on the account, kept by a later
// appearance save, put back as it comes by an empty one, and refused in the
// page's language when it would name a page twice or lead off the application.
func TestOwnMenuArrangementIsSavedAndChecked(t *testing.T) {
	s, _, viewer, _ := accountsTestServer(t)
	rec := asUser(t, s, "POST", "/api/account/menu", viewer,
		`{"menu":[{"name":"کارهای من","items":["/orders","/inbox"]},{"href":"/"}]}`)
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), `"name":"کارهای من"`) {
		t.Fatalf("saving: %d %s", rec.Code, rec.Body.String())
	}
	if rec := asUser(t, s, "POST", "/api/account/prefs", viewer, `{"font_scale":110}`); rec.Code != http.StatusOK {
		t.Fatalf("appearance: %d %s", rec.Code, rec.Body.String())
	}
	if u, _ := s.guard.Users.Get("reader"); len(u.Prefs.Menu) != 2 || u.Prefs.Menu[0].Items[1] != "/inbox" {
		t.Errorf("after the appearance save: %+v", u.Prefs.Menu)
	}
	for body, want := range map[string]string{
		`{"menu":[{"href":"/orders"},{"href":"/orders"}]}`: "دو بار",
		`{"menu":[{"href":"https://evil.example"}]}`:       "همین سامانه",
	} {
		rec := asUser(t, s, "POST", "/api/account/menu", viewer, body)
		if rec.Code != http.StatusBadRequest || !strings.Contains(rec.Body.String(), want) {
			t.Errorf("%s answered %d %s", body, rec.Code, rec.Body.String())
		}
	}
	if rec := asUser(t, s, "POST", "/api/account/menu", viewer, `{"menu":[]}`); rec.Code != http.StatusOK {
		t.Fatalf("reset: %d %s", rec.Code, rec.Body.String())
	}
	if u, _ := s.guard.Users.Get("reader"); u.Prefs.Menu != nil {
		t.Errorf("reset kept %+v", u.Prefs.Menu)
	}

	// A page of one's own goes with the arrangement, and needs a name.
	rec = asUser(t, s, "POST", "/api/account/menu", viewer,
		`{"menu":[{"href":"/messages?c=1404-12"}],"pages":[{"href":"/messages?c=1404-12","name":"سفارش ۱۲"}]}`)
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), `"name":"سفارش ۱۲"`) {
		t.Fatalf("a page of one's own: %d %s", rec.Code, rec.Body.String())
	}
	if u, _ := s.guard.Users.Get("reader"); len(u.Prefs.Pages) != 1 || u.Prefs.Pages[0].Href != "/messages?c=1404-12" {
		t.Errorf("pages: %+v", u.Prefs.Pages)
	}
	if rec := asUser(t, s, "POST", "/api/account/menu", viewer, `{"menu":[],"pages":[{"href":"/orders"}]}`); rec.Code != http.StatusBadRequest ||
		!strings.Contains(rec.Body.String(), "نامی می‌خواهد") {
		t.Errorf("a page without a name: %d %s", rec.Code, rec.Body.String())
	}
}

// − and + beside the menu's entries: the entries taken out are the caller's own,
// saved on the account, kept by an appearance save, refused when one is not a
// page of this application, and all back with an empty list.
func TestMenuEntriesTakenOutAndPutBack(t *testing.T) {
	s, _, viewer, _ := accountsTestServer(t)
	rec := asUser(t, s, "POST", "/api/account/menu/hidden", viewer, `{"hidden":["/couriers","/downloads"]}`)
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), `"/downloads"`) {
		t.Fatalf("taking out: %d %s", rec.Code, rec.Body.String())
	}
	if rec := asUser(t, s, "POST", "/api/account/prefs", viewer, `{"font_scale":110}`); rec.Code != http.StatusOK {
		t.Fatalf("appearance: %d %s", rec.Code, rec.Body.String())
	}
	if u, _ := s.guard.Users.Get("reader"); len(u.Prefs.Hidden) != 2 {
		t.Errorf("after the appearance save: %+v", u.Prefs.Hidden)
	}
	if rec := asUser(t, s, "POST", "/api/account/menu/hidden", viewer, `{"hidden":["https://evil.example"]}`); rec.Code != http.StatusBadRequest ||
		!strings.Contains(rec.Body.String(), "همین سامانه") {
		t.Errorf("an outside address: %d %s", rec.Code, rec.Body.String())
	}
	rec = asUser(t, s, "POST", "/api/account/menu/hidden", viewer, `{"hidden":[]}`)
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), `"hidden":[]`) {
		t.Fatalf("putting all back: %d %s", rec.Code, rec.Body.String())
	}
	if u, _ := s.guard.Users.Get("reader"); u.Prefs.Hidden != nil {
		t.Errorf("kept %q", u.Prefs.Hidden)
	}
}
