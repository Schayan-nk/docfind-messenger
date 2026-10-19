package chat

import "time"

// Privacy is one person's settings (chat_users_ext.privacy). Each rule is
// "everybody" (the default when empty), "contacts" or "nobody", with
// Always/Never lists that override it for named people.
type Privacy struct {
	LastSeen Rule `json:"last_seen"`
	Photo    Rule `json:"photo"`
	Bio      Rule `json:"bio"`
	Forwards Rule `json:"forwards"`
	Calls    Rule `json:"calls"`
	Invites  Rule `json:"invites"`
	Voice    Rule `json:"voice"`
}

// Rule is one privacy setting.
type Rule struct {
	Mode   string   `json:"mode,omitempty"` // everybody | contacts | nobody
	Always []string `json:"always,omitempty"`
	Never  []string `json:"never,omitempty"`
}

// Allows decides whether viewer may see (or do) what r guards. isContact says
// whether the owner of the rule has viewer as a contact.
func (r Rule) Allows(viewer string, isContact bool) bool {
	for _, n := range r.Never {
		if n == viewer {
			return false
		}
	}
	for _, a := range r.Always {
		if a == viewer {
			return true
		}
	}
	switch r.Mode {
	case "nobody":
		return false
	case "contacts":
		return isContact
	}
	return true
}

// Shares reports whether r shows anything to anybody: someone who hides their
// own last seen loses the right to see everyone else's (Telegram's rule).
func (r Rule) Shares() bool { return r.Mode != "nobody" || len(r.Always) > 0 }

// presenceFor fills in what viewer may know about target's presence.
//
// online is shown whenever the exact time would be; otherwise the page gets a
// bucket ("recently", within a week, within a month), never the time.
func presenceFor(p *Person, viewer string, targetRule Rule, viewerRule Rule, viewerIsTargetsContact, online bool, lastSeen *time.Time, now time.Time) {
	exact := viewer == p.ID || (targetRule.Allows(viewer, viewerIsTargetsContact) && viewerRule.Shares())
	if exact {
		p.Online = online
		if lastSeen != nil && !online {
			p.LastSeen = lastSeen.UTC().Format(time.RFC3339)
		}
		return
	}
	p.Online = false
	switch {
	case online || (lastSeen != nil && now.Sub(*lastSeen) < 72*time.Hour):
		p.Seen = "recently"
	case lastSeen != nil && now.Sub(*lastSeen) < 7*24*time.Hour:
		p.Seen = "week"
	case lastSeen != nil && now.Sub(*lastSeen) < 30*24*time.Hour:
		p.Seen = "month"
	default:
		p.Seen = "long"
	}
}
