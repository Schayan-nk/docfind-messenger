package chat

import (
	"testing"
	"time"
)

// The rights matrix (SPEC 6.7) without a database: can is the one place every
// group, supergroup and channel rule is decided.

var actNames = map[Action]string{
	ActSend: "send", ActSendMedia: "media", ActSendPoll: "poll", ActPreview: "preview", ActInvite: "invite",
	ActPin: "pin", ActChangeInfo: "info", ActManageTopics: "topics", ActDeleteOthers: "delete others",
	ActBan: "ban", ActAddAdmins: "add admins", ActEditOthers: "edit others", ActManageCalls: "calls", ActReact: "react",
}

func TestCanMatrix(t *testing.T) {
	now := time.Date(2026, 10, 13, 12, 0, 0, 0, time.UTC)
	later, earlier := now.Add(time.Hour), now.Add(-time.Hour)
	mute := &Perms{} // a restriction that takes everything away
	pinOnly := AdminRights{PinMessages: true}
	poster := AdminRights{PostMessages: true}
	type row struct {
		typ  string
		m    *Member
		set  Settings
		yes  []Action // everything else must be refused
		name string
	}
	all := []Action{ActSend, ActSendMedia, ActSendPoll, ActPreview, ActInvite, ActPin, ActChangeInfo,
		ActManageTopics, ActDeleteOthers, ActBan, ActAddAdmins, ActEditOthers, ActManageCalls, ActReact}
	ownerYes := []Action{ActSend, ActSendMedia, ActSendPoll, ActPreview, ActInvite, ActPin, ActChangeInfo,
		ActManageTopics, ActDeleteOthers, ActBan, ActAddAdmins, ActEditOthers, ActManageCalls, ActReact}
	memberYes := []Action{ActSend, ActSendMedia, ActSendPoll, ActPreview, ActInvite, ActReact}
	strict := Settings{Perms: &Perms{SendMessages: true}} // text only
	rows := []row{
		{"group", &Member{Role: "owner"}, Settings{}, ownerYes, "group owner"},
		{"supergroup", &Member{Role: "owner"}, Settings{}, ownerYes, "supergroup owner"},
		{"channel", &Member{Role: "owner"}, Settings{}, ownerYes, "channel owner"},
		{"group", &Member{Role: "member"}, Settings{}, memberYes, "group member, default permissions"},
		{"supergroup", &Member{Role: "member"}, strict, []Action{ActSend, ActReact}, "member under text-only permissions"},
		{"supergroup", &Member{Role: "restricted", Restrict: mute, Until: &later}, Settings{}, []Action{ActReact}, "muted until later"},
		{"supergroup", &Member{Role: "restricted", Restrict: mute, Until: &earlier}, Settings{}, memberYes, "mute that has run out"},
		{"supergroup", &Member{Role: "restricted", Restrict: mute}, Settings{}, []Action{ActReact}, "muted for good"},
		{"supergroup", &Member{Role: "restricted", Restrict: &Perms{SendMessages: true, SendMedia: true, PinMessages: true}}, Settings{},
			[]Action{ActSend, ActSendMedia, ActReact}, "a restriction never grants what the chat withholds"},
		{"channel", &Member{Role: "member"}, Settings{}, []Action{ActReact}, "channel subscriber"},
		{"channel", &Member{Role: "member"}, Settings{Perms: &Perms{SendMessages: true, PinMessages: true, ChangeInfo: true}}, []Action{ActReact}, "channel subscriber, generous defaults"},
		{"channel", &Member{Role: "admin", Admin: pinOnly}, Settings{}, []Action{ActPin, ActReact}, "channel admin without posting"},
		{"channel", &Member{Role: "admin", Admin: poster}, Settings{}, []Action{ActSend, ActSendMedia, ActSendPoll, ActPreview, ActReact}, "channel poster"},
		{"supergroup", &Member{Role: "admin"}, Settings{}, []Action{ActSend, ActSendMedia, ActSendPoll, ActPreview, ActReact}, "admin with no rights"},
		{"supergroup", &Member{Role: "admin"}, Settings{Perms: &Perms{PinMessages: true}}, []Action{ActSend, ActSendMedia, ActSendPoll, ActPreview, ActPin, ActReact}, "admin pins where members may"},
		{"supergroup", &Member{Role: "admin", Admin: AdminRights{BanUsers: true, DeleteMessages: true}}, Settings{},
			[]Action{ActSend, ActSendMedia, ActSendPoll, ActPreview, ActBan, ActDeleteOthers, ActReact}, "moderator"},
		{"group", &Member{Role: "banned"}, Settings{}, nil, "banned"},
		{"group", &Member{Role: "left"}, Settings{}, nil, "left"},
		{"channel", &Member{Role: "banned", Admin: allRights}, Settings{}, nil, "banned with stale rights"},
		{"group", &Member{Role: "member", Admin: allRights}, Settings{}, memberYes, "member with stale admin rights"},
		{"group", nil, Settings{}, nil, "not a member"},
	}
	for _, r := range rows {
		want := map[Action]bool{}
		for _, a := range r.yes {
			want[a] = true
		}
		for _, a := range all {
			if got := can(r.typ, r.set, r.m, a, now); got != want[a] {
				t.Errorf("%s: %s = %v, want %v", r.name, actNames[a], got, want[a])
			}
		}
	}
}

func TestAdminCannotHandOutRightsTheyLack(t *testing.T) {
	have := AdminRights{BanUsers: true, PinMessages: true, AddAdmins: true}
	if !(AdminRights{BanUsers: true}).within(have) {
		t.Error("a subset was refused")
	}
	if (AdminRights{BanUsers: true, ChangeInfo: true}).within(have) {
		t.Error("an admin could grant change_info without holding it")
	}
	if !(AdminRights{}).within(AdminRights{}) {
		t.Error("no rights must fit within no rights")
	}
}

func TestChatReactionList(t *testing.T) {
	if !(Settings{}).allowsReaction("👍") || (Settings{}).allowsReaction("🍆") {
		t.Error("the default list")
	}
	narrow := Settings{Reactions: []string{"👍", "🍆"}}
	if !narrow.allowsReaction("👍") || narrow.allowsReaction("❤️") {
		t.Error("a chat's own list")
	}
	if narrow.allowsReaction("🍆") {
		t.Error("a chat's list let in a reaction outside the company set")
	}
	if (Settings{NoReactions: true}).allowsReaction("👍") {
		t.Error("reactions switched off")
	}
}
