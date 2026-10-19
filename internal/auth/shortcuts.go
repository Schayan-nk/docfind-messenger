package auth

// A person's own keyboard shortcuts to the pages they use (asked for on
// 1405/07/08): "Alt+1 opens the orders", "g then o opens the orders".
//
// Kept on the account, like the other preferences, so they follow the person
// to every machine. Two forms and nothing else, both read by the key's
// POSITION in the browser (so they work on a Persian keyboard as well):
//
//	alt+<a-z or 0-9>   pressed together
//	g <a-z>            g, then the letter -- the form the built-in shortcuts
//	                   use, minus the letters they already take
//
// Ctrl combinations are left alone: the browser owns most of them, and the
// one docfind uses (Ctrl+K) must not be taken over.

// Shortcut is one key sequence and where it goes.
type Shortcut struct {
	Keys  string `json:"keys"`
	Href  string `json:"href"`
	Label string `json:"label,omitempty"`
}
