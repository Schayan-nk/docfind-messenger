package auth

import (
	"strings"
)

// «سایت‌های پرکاربرد» (asked for on 1405/07/09): sites outside this
// application a person opens often, kept on their account and listed in their
// menu, each opening in a new tab. Added with the ＋ beside the heading,
// removed with the ✕ beside each one -- for that person alone, on every
// machine, like the shortcuts.
//
// The menu draws them, so a site is checked here, not trusted from the page:
// a web address (http or https, a host, no user name or password in it) and a
// name with nothing in it that could open a tag.

// Site is one of a person's sites: what the menu calls it, and where it goes.
type Site struct {
	Name string `json:"name"`
	URL  string `json:"url"`
}

// cleanName is a name the menu can draw as markup: the characters that could
// open a tag or an attribute dropped, not escaped, and the spaces collapsed.
func cleanName(s string) string {
	s = strings.Map(func(r rune) rune {
		if strings.ContainsRune("<>&\"'`", r) {
			return -1
		}
		return r
	}, s)
	return strings.TrimSpace(strings.Join(strings.Fields(s), " "))
}
