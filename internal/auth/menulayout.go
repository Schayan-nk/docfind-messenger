package auth

import (
	"fmt"
	"regexp"
	"strings"
)

// A person's own arrangement of the menu (asked for on 1405/07/08): the
// entries in the order they want, and groups of their own -- made, renamed,
// filled, emptied and deleted by them, for them.
//
// ONLY AN ARRANGEMENT. Which entries exist, and whether this person may open
// them, is still the menu's (nav.js GROUPS, filtered by permission): an href
// here that is not a menu entry, or one this account may not open, is simply
// not drawn. Kept on the account, like the shortcuts, so it follows the
// person to every machine.
//
// PAGES OF THEIR OWN (asked for on 1405/07/09). A person can also add a page
// of this application the menu does not list -- one report, one order, a
// customer's page -- under a name of their own (MenuPage), and place it like
// any entry. It is still only a link: nav.js draws it where the account may
// open the page it is on (a report's own permission for a report), and the
// page itself decides what it shows.

// MenuNode is one top-level place in the menu: an entry (Href), or a group
// (Name, an optional Icon, and Items -- its entries' hrefs, in order).
type MenuNode struct {
	Href  string   `json:"href,omitempty"`
	Name  string   `json:"name,omitempty"`
	Icon  string   `json:"icon,omitempty"`
	Items []string `json:"items,omitempty"`
}

// MenuPage is a page a person added to their own menu, and its name there.
type MenuPage struct {
	Href string `json:"href"`
	Name string `json:"name"`
}

// The bounds: the menu has about thirty entries, so these leave room for any
// arrangement of them and none for a menu used as storage.
const (
	MaxMenuNodes = 60
	MaxMenuItems = 120
	MaxMenuPages = 30
)

var menuIcon = regexp.MustCompile(`^[a-z0-9-]{1,32}$`)

// menuHref is a page of this application: a path, never another site or a
// script -- the same rule as a shortcut's destination.
func menuHref(h string) bool {
	return strings.HasPrefix(h, "/") && !strings.HasPrefix(h, "//") &&
		!strings.ContainsAny(h, "\\\r\n\t\"'<> ") && len(h) <= 200
}

// CleanMenu checks an arrangement and returns it normalised, or says what is
// wrong with it. Nil or empty is the menu as it comes.
func CleanMenu(in []MenuNode) ([]MenuNode, error) {
	if len(in) > MaxMenuNodes {
		return nil, fmt.Errorf("at most %d places", MaxMenuNodes)
	}
	seen := map[string]bool{}
	entry := func(h string) (string, error) {
		h = strings.TrimSpace(h)
		if !menuHref(h) {
			return "", fmt.Errorf("%q is not a page of this application", h)
		}
		if seen[h] {
			return "", fmt.Errorf("%q is in the menu twice", h)
		}
		seen[h] = true
		return h, nil
	}
	out := make([]MenuNode, 0, len(in))
	for _, n := range in {
		// The menu is drawn as markup, so a name carries none: the characters
		// that could open a tag or an attribute are dropped, not escaped, and
		// the name reads the same in the drawer, the dock and the search.
		name := cleanName(n.Name) // sites.go
		switch {
		case name == "" && len(n.Items) == 0:
			h, err := entry(n.Href)
			if err != nil {
				return nil, err
			}
			out = append(out, MenuNode{Href: h})
		case name != "" && n.Href == "":
			if r := []rune(name); len(r) > 40 {
				return nil, fmt.Errorf("a group's name is at most 40 letters")
			}
			g := MenuNode{Name: name, Items: []string{}}
			if icon := strings.TrimSpace(n.Icon); menuIcon.MatchString(icon) {
				g.Icon = icon
			}
			for _, it := range n.Items {
				h, err := entry(it)
				if err != nil {
					return nil, err
				}
				g.Items = append(g.Items, h)
			}
			out = append(out, g)
		default:
			return nil, fmt.Errorf("a place is either a page or a named group")
		}
	}
	if len(seen) > MaxMenuItems {
		return nil, fmt.Errorf("at most %d pages", MaxMenuItems)
	}
	return out, nil
}

// CleanMenuPages checks a person's own pages: each a page of this application
// (the entries' rule), once, with a name.
func CleanMenuPages(in []MenuPage) ([]MenuPage, error) {
	if len(in) > MaxMenuPages {
		return nil, fmt.Errorf("at most %d pages of your own", MaxMenuPages)
	}
	seen := map[string]bool{}
	out := make([]MenuPage, 0, len(in))
	for _, p := range in {
		h := strings.TrimSpace(p.Href)
		if !menuHref(h) {
			return nil, fmt.Errorf("%q is not a page of this application", h)
		}
		if seen[h] {
			return nil, fmt.Errorf("%q is in the menu twice", h)
		}
		seen[h] = true
		name := cleanName(p.Name)
		if name == "" {
			return nil, fmt.Errorf("a page of your own needs a name")
		}
		if len([]rune(name)) > 40 {
			return nil, fmt.Errorf("a page's name is at most 40 letters")
		}
		out = append(out, MenuPage{Href: h, Name: name})
	}
	return out, nil
}

// ENTRIES TAKEN OUT (asked for on 1405/07/20: "+ and - beside nav items where
// user can directly change his nav menu links"). A person can press − beside
// an entry to take it out of their own menu and + to put it back. Kept apart
// from the arrangement because the arrangement deliberately never hides
// anything -- an entry it does not mention keeps its place, so a page added
// later is never lost to an old arrangement -- while this list is exactly the
// entries this person chose not to see. Still only theirs, and still not a
// permission: an entry taken out is one press from coming back.

// CleanMenuHidden checks the entries a person took out: each a page of this
// application, once each, within the menu's bound.
func CleanMenuHidden(in []string) ([]string, error) {
	if len(in) > MaxMenuItems {
		return nil, fmt.Errorf("at most %d pages", MaxMenuItems)
	}
	seen := map[string]bool{}
	out := make([]string, 0, len(in))
	for _, h := range in {
		h = strings.TrimSpace(h)
		if !menuHref(h) {
			return nil, fmt.Errorf("%q is not a page of this application", h)
		}
		if seen[h] {
			continue
		}
		seen[h] = true
		out = append(out, h)
	}
	return out, nil
}

// SetMenuHidden replaces the entries a person took out of their menu; nil or
// empty brings every one back.
func (s *Store) SetMenuHidden(username string, hidden []string) (Prefs, error) {
	clean, err := CleanMenuHidden(hidden)
	if err != nil {
		return Prefs{}, err
	}
	if len(clean) == 0 {
		clean = nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	u, ok := s.users[normalizeName(username)]
	if !ok {
		return Prefs{}, ErrNoSuchUser
	}
	before := u.Prefs.Hidden
	u.Prefs.Hidden = clean
	if err := s.save(); err != nil {
		u.Prefs.Hidden = before
		return Prefs{}, err
	}
	return u.Prefs, nil
}

// SetMenu replaces a person's arrangement and their own pages, together;
// nil or empty puts the menu back as it comes.
func (s *Store) SetMenu(username string, menu []MenuNode, pages []MenuPage) (Prefs, error) {
	clean, err := CleanMenu(menu)
	if err != nil {
		return Prefs{}, err
	}
	own, err := CleanMenuPages(pages)
	if err != nil {
		return Prefs{}, err
	}
	if len(clean) == 0 {
		clean = nil
	}
	if len(own) == 0 {
		own = nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	u, ok := s.users[normalizeName(username)]
	if !ok {
		return Prefs{}, ErrNoSuchUser
	}
	before, beforePages := u.Prefs.Menu, u.Prefs.Pages
	u.Prefs.Menu, u.Prefs.Pages = clean, own
	if err := s.save(); err != nil {
		u.Prefs.Menu, u.Prefs.Pages = before, beforePages
		return Prefs{}, err
	}
	return u.Prefs, nil
}
