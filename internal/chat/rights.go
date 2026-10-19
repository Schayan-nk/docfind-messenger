package chat

import (
	"encoding/json"
	"time"
)

// Who may do what in a group, supergroup or channel (SPEC 6.7). One function,
// can, answers every question, so the socket, the REST API and the tests all
// ask the same thing.

// AdminRights are Telegram's administrator rights.
type AdminRights struct {
	ChangeInfo     bool `json:"change_info,omitempty"`
	DeleteMessages bool `json:"delete_messages,omitempty"`
	BanUsers       bool `json:"ban_users,omitempty"`
	InviteUsers    bool `json:"invite_users,omitempty"`
	PinMessages    bool `json:"pin_messages,omitempty"`
	ManageTopics   bool `json:"manage_topics,omitempty"`
	ManageCalls    bool `json:"manage_calls,omitempty"`
	PostMessages   bool `json:"post_messages,omitempty"` // channels
	EditMessages   bool `json:"edit_messages,omitempty"` // channels: other admins' posts
	AddAdmins      bool `json:"add_admins,omitempty"`
	Anonymous      bool `json:"anonymous,omitempty"`
}

// allRights is what the owner has.
var allRights = AdminRights{true, true, true, true, true, true, true, true, true, true, false}

// within reports whether every right in r is also in of: an admin cannot hand
// out a right they do not have.
func (r AdminRights) within(of AdminRights) bool {
	type p struct{ a, b bool }
	for _, x := range []p{{r.ChangeInfo, of.ChangeInfo}, {r.DeleteMessages, of.DeleteMessages}, {r.BanUsers, of.BanUsers},
		{r.InviteUsers, of.InviteUsers}, {r.PinMessages, of.PinMessages}, {r.ManageTopics, of.ManageTopics},
		{r.ManageCalls, of.ManageCalls}, {r.PostMessages, of.PostMessages}, {r.EditMessages, of.EditMessages},
		{r.AddAdmins, of.AddAdmins}, {r.Anonymous, of.Anonymous}} {
		if x.a && !x.b {
			return false
		}
	}
	return true
}

// Perms are what ordinary members may do (the chat's defaults), and what a
// restricted member is still allowed.
type Perms struct {
	SendMessages bool `json:"send_messages"`
	SendMedia    bool `json:"send_media"`
	SendPolls    bool `json:"send_polls"`
	AddPreviews  bool `json:"add_previews"`
	InviteUsers  bool `json:"invite_users"`
	PinMessages  bool `json:"pin_messages"`
	ChangeInfo   bool `json:"change_info"`
	ManageTopics bool `json:"manage_topics"`
}

// DefaultPerms suit a company group: members talk, share files and invite
// colleagues; pinning, renaming and topics are for admins.
var DefaultPerms = Perms{SendMessages: true, SendMedia: true, SendPolls: true, AddPreviews: true, InviteUsers: true}

func (p Perms) and(q Perms) Perms {
	return Perms{p.SendMessages && q.SendMessages, p.SendMedia && q.SendMedia, p.SendPolls && q.SendPolls,
		p.AddPreviews && q.AddPreviews, p.InviteUsers && q.InviteUsers, p.PinMessages && q.PinMessages,
		p.ChangeInfo && q.ChangeInfo, p.ManageTopics && q.ManageTopics}
}

// Settings is chats.settings.
type Settings struct {
	Perms         *Perms   `json:"perms,omitempty"`
	Reactions     []string `json:"reactions,omitempty"` // allowed reactions; empty: the default set
	NoReactions   bool     `json:"no_reactions,omitempty"`
	HiddenHistory bool     `json:"hidden_history,omitempty"` // supergroups: new members see from when they joined
	Signatures    bool     `json:"signatures,omitempty"`     // channels: posts show their author
	AntiSpamHours int      `json:"antispam_hours,omitempty"` // no links from members newer than this
	Linked        int64    `json:"linked,omitempty"`         // a channel's discussion group, and back
}

func (s Settings) perms() Perms {
	if s.Perms != nil {
		return *s.Perms
	}
	return DefaultPerms
}

func parseSettings(b []byte) Settings {
	var s Settings
	_ = json.Unmarshal(b, &s)
	return s
}

// allowsReaction checks a reaction against the chat's own list.
func (s Settings) allowsReaction(e string) bool {
	if s.NoReactions {
		return false
	}
	if len(s.Reactions) == 0 {
		return validReaction(e)
	}
	for _, r := range s.Reactions {
		if r == e {
			return validReaction(e)
		}
	}
	return false
}

// Member is one person's place in a chat.
type Member struct {
	User      string      `json:"id"`
	Role      string      `json:"role"`
	Admin     AdminRights `json:"admin,omitempty"`
	Restrict  *Perms      `json:"restrict,omitempty"`
	Until     *time.Time  `json:"until,omitempty"`
	Title     string      `json:"title,omitempty"`
	JoinedAt  time.Time   `json:"joined_at"`
	JoinedPTS int64       `json:"-"`
	AddedBy   string      `json:"added_by,omitempty"`
	Name      string      `json:"name,omitempty"`
	Photo     bool        `json:"photo,omitempty"`
}

// rights is what an admin (or the owner) holds.
func (m *Member) rights() AdminRights {
	if m.Role == "owner" {
		return allRights
	}
	if m.Role == "admin" {
		return m.Admin
	}
	return AdminRights{}
}

// Action is one thing someone may or may not do in a chat.
type Action int

const (
	ActSend Action = iota
	ActSendMedia
	ActSendPoll
	ActPreview
	ActInvite
	ActPin
	ActChangeInfo
	ActManageTopics
	ActDeleteOthers
	ActBan
	ActAddAdmins
	ActEditOthers
	ActManageCalls
	ActReact
)

// can decides one action for one member of a group, supergroup or channel.
// Private chats, Saved Messages and secret chats keep their own rules.
func can(chatType string, set Settings, m *Member, a Action, now time.Time) bool {
	if m == nil {
		return false
	}
	switch m.Role {
	case "left", "banned":
		return false
	case "owner", "admin":
		r := m.rights()
		switch a {
		case ActSend, ActSendMedia, ActSendPoll, ActPreview:
			return chatType != "channel" || r.PostMessages
		case ActReact:
			return true
		case ActInvite:
			return r.InviteUsers
		case ActPin:
			return r.PinMessages || (chatType != "channel" && set.perms().PinMessages)
		case ActChangeInfo:
			return r.ChangeInfo
		case ActManageTopics:
			return r.ManageTopics
		case ActDeleteOthers:
			return r.DeleteMessages
		case ActBan:
			return r.BanUsers
		case ActAddAdmins:
			return r.AddAdmins
		case ActEditOthers:
			return r.EditMessages
		case ActManageCalls:
			return r.ManageCalls
		}
		return false
	}
	if a == ActReact {
		return true // subscribers and members react; the chat's list decides which emoji
	}
	if chatType == "channel" {
		return false // subscribers read
	}
	p := set.perms()
	if m.Role == "restricted" && m.Restrict != nil && (m.Until == nil || m.Until.After(now)) {
		p = p.and(*m.Restrict)
	}
	switch a {
	case ActSend:
		return p.SendMessages
	case ActSendMedia:
		return p.SendMessages && p.SendMedia
	case ActSendPoll:
		return p.SendMessages && p.SendPolls
	case ActPreview:
		return p.AddPreviews
	case ActInvite:
		return p.InviteUsers
	case ActPin:
		return p.PinMessages
	case ActChangeInfo:
		return p.ChangeInfo
	case ActManageTopics:
		return p.ManageTopics
	}
	return false
}

// isGroupish: the types whose rules are can's.
func isGroupish(t string) bool { return t == "group" || t == "supergroup" || t == "channel" }

// memberLimit is the most people a chat type takes (SPEC 6.2).
func memberLimit(t string) int {
	switch t {
	case "group":
		return 200
	case "supergroup", "channel":
		return 10000
	}
	return 2
}
