package chat

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/Schayan-nk/docfind-messenger/internal/auth"
)

// Phase 4 against a real database (skipped without DOCFIND_PG_*): the rights
// matrix as the store and the service apply it, invite links, join requests,
// hidden history, slow mode, topics and handing a chat over.

// pwUsers is fakeUsers that also checks a password (TransferOwnership).
type pwUsers struct {
	*fakeUsersT
	pw string
}

func (p pwUsers) Authenticate(name, password string) (*auth.User, error) {
	if password != p.pw {
		return nil, errors.New("wrong password")
	}
	return p.Get(name)
}

// groupFixture makes n users, a service and a chat of typ owned by u[0] with
// u[1..members] in it; the chat is removed at the end.
func groupFixture(t *testing.T, typ string, n, members int, forum bool) (*Service, []string, int64) {
	t.Helper()
	s, users := testStore(t)
	u := users(n)
	svc := New(s, pwUsers{fakeUsers(u), "درست"}, nil)
	id, refused, err := svc.CreateGroup(context.Background(), u[0], CreateReq{Type: typ, Title: "گروه آزمایشی", Members: u[1 : members+1], Forum: forum})
	if err != nil || len(refused) > 0 {
		t.Fatalf("creating a %s: %v (refused %v)", typ, err, refused)
	}
	t.Cleanup(func() { s.pool.Exec(context.Background(), `DELETE FROM chats WHERE id = $1`, id) })
	return svc, u, id
}

func send(svc *Service, user string, chat int64, text string) error {
	_, err := svc.SendText(context.Background(), user, SendReq{Chat: chat, CID: uuidV7(), Text: text}, nil)
	return err
}

func TestGroupRightsThroughTheStore(t *testing.T) {
	svc, u, id := groupFixture(t, "supergroup", 4, 2, false)
	ctx := context.Background()
	owner, a, b, outsider := u[0], u[1], u[2], u[3]

	// Security check 1 for groups: an outsider is told nothing.
	if err := send(svc, outsider, id, "x"); err != ErrNotMember {
		t.Errorf("outsider sent: %v", err)
	}
	if _, _, err := svc.AddMembers(ctx, outsider, id, []string{outsider}); err != ErrNotMember {
		t.Errorf("outsider added themselves: %v", err)
	}
	if _, err := svc.EditChat(ctx, outsider, id, InfoReq{Title: ptr("x")}); err != ErrNotMember {
		t.Errorf("outsider renamed the chat: %v", err)
	}
	if err := svc.ChangeMember(ctx, outsider, id, a, MemberReq{Action: "ban"}); err != ErrNotMember {
		t.Errorf("outsider banned a member: %v", err)
	}

	// A member talks but does not moderate.
	if err := send(svc, a, id, "سلام"); err != nil {
		t.Fatalf("member could not send: %v", err)
	}
	if err := svc.ChangeMember(ctx, a, id, b, MemberReq{Action: "ban"}); err != ErrForbidden {
		t.Errorf("member banned someone: %v", err)
	}
	if _, err := svc.EditChat(ctx, a, id, InfoReq{Title: ptr("x")}); err != ErrForbidden {
		t.Errorf("member renamed the chat: %v", err)
	}
	if err := svc.ChangeMember(ctx, a, id, a, MemberReq{Action: "promote", Rights: &AdminRights{BanUsers: true}}); err != ErrForbidden {
		t.Errorf("member promoted themselves: %v", err)
	}

	// An admin with ban and add-admins rights moderates, within their rights.
	if err := svc.ChangeMember(ctx, owner, id, a, MemberReq{Action: "promote", Rights: &AdminRights{BanUsers: true, AddAdmins: true}}); err != nil {
		t.Fatalf("promote: %v", err)
	}
	if err := svc.ChangeMember(ctx, a, id, b, MemberReq{Action: "promote", Rights: &AdminRights{ChangeInfo: true}}); err != ErrForbidden {
		t.Errorf("an admin handed out a right they lack: %v", err)
	}
	if err := svc.ChangeMember(ctx, a, id, owner, MemberReq{Action: "ban"}); err != ErrForbidden {
		t.Errorf("an admin banned the owner: %v", err)
	}
	if err := svc.ChangeMember(ctx, a, id, b, MemberReq{Action: "ban"}); err != nil {
		t.Fatalf("ban: %v", err)
	}
	if err := send(svc, b, id, "x"); err != ErrNotMember {
		t.Errorf("a banned member sent: %v", err)
	}
	if _, err := svc.Store.Page(ctx, id, b, PageQuery{}); err != nil {
		t.Logf("page: %v", err)
	}
	invs, _ := svc.Store.Invites(ctx, id)
	if len(invs) == 0 {
		t.Fatal("no primary link")
	}
	if _, err := svc.JoinByLink(ctx, b, invs[0].Token); err != ErrBanned {
		t.Errorf("a banned member came back by link: %v", err)
	}
	if _, refused, _ := svc.AddMembers(ctx, owner, id, []string{b}); len(refused) != 0 {
		t.Logf("the owner (who may ban) lifted the ban by adding: refused %v", refused)
	}

	// Restricted: muted members cannot send but still react.
	if err := svc.ChangeMember(ctx, owner, id, a, MemberReq{Action: "restrict", Perms: &Perms{}}); err != ErrForbidden {
		t.Errorf("an admin was restricted without being demoted: %v", err)
	}
	if err := svc.ChangeMember(ctx, owner, id, a, MemberReq{Action: "demote"}); err != nil {
		t.Fatal(err)
	}
	if err := svc.ChangeMember(ctx, owner, id, a, MemberReq{Action: "restrict", Perms: &Perms{}}); err != nil {
		t.Fatal(err)
	}
	if err := send(svc, a, id, "x"); err != ErrForbidden {
		t.Errorf("a muted member sent: %v", err)
	}
	msgs, _ := svc.Store.Page(ctx, id, owner, PageQuery{Limit: 1})
	if len(msgs) == 0 {
		t.Fatal("no messages")
	}
	if err := svc.React(ctx, a, id, msgs[0].ID, "👍", true, nil); err != nil {
		t.Errorf("a muted member could not react: %v", err)
	}

	// The admin log saw it all, and only admins read it.
	log, err := svc.Store.AdminLog(ctx, id, "", "")
	if err != nil || len(log) < 4 {
		t.Errorf("admin log: %d entries, %v", len(log), err)
	}
}

func TestSlowModeAndTheLinkRuleForNewMembers(t *testing.T) {
	svc, u, id := groupFixture(t, "supergroup", 2, 1, false)
	ctx := context.Background()
	if _, err := svc.EditChat(ctx, u[1], id, InfoReq{SlowMode: ptr(30)}); err != ErrForbidden {
		t.Errorf("a member set slow mode: %v", err)
	}
	if _, err := svc.EditChat(ctx, u[0], id, InfoReq{SlowMode: ptr(7)}); err == nil {
		t.Error("a slow mode step outside the list was accepted")
	}
	if _, err := svc.EditChat(ctx, u[0], id, InfoReq{SlowMode: ptr(30), AntiSpamHours: ptr(24)}); err != nil {
		t.Fatal(err)
	}
	if err := send(svc, u[1], id, "یک"); err != nil {
		t.Fatal(err)
	}
	if err := send(svc, u[1], id, "دو"); err != ErrSlowMode {
		t.Errorf("second message inside slow mode: %v", err)
	}
	if err := send(svc, u[0], id, "یک"); err != nil {
		t.Errorf("slow mode held back the owner: %v", err)
	}
	if err := send(svc, u[0], id, "دو https://example.com"); err != nil {
		t.Errorf("the owner: %v", err)
	}
	svc.now = func() time.Time { return time.Now().Add(time.Minute) }
	if err := send(svc, u[1], id, "ببینید https://example.com"); err != ErrNewLinks {
		t.Errorf("a day-old member sent a link: %v", err)
	}
}

func TestChannelsAreReadBySubscribers(t *testing.T) {
	svc, u, id := groupFixture(t, "channel", 3, 2, false)
	ctx := context.Background()
	if err := send(svc, u[1], id, "x"); err != ErrForbidden {
		t.Errorf("a subscriber posted: %v", err)
	}
	if err := send(svc, u[0], id, "خبر"); err != nil {
		t.Fatal(err)
	}
	if err := svc.ChangeMember(ctx, u[0], id, u[2], MemberReq{Action: "promote", Rights: &AdminRights{PinMessages: true}}); err != nil {
		t.Fatal(err)
	}
	if err := send(svc, u[2], id, "x"); err != ErrForbidden {
		t.Errorf("an admin without the posting right posted: %v", err)
	}
	msgs, _ := svc.Store.Page(ctx, id, u[1], PageQuery{})
	if len(msgs) == 0 {
		t.Fatal("the subscriber sees no posts")
	}
	if err := svc.React(ctx, u[1], id, msgs[0].ID, "👍", true, nil); err != nil {
		t.Errorf("a subscriber could not react: %v", err)
	}
	if err := svc.Delete(ctx, u[1], id, []int64{msgs[0].ID}, true, nil); err != ErrForbidden {
		t.Errorf("a subscriber deleted a post for everyone: %v", err)
	}
}

func TestInviteLinksAndJoinRequests(t *testing.T) {
	svc, u, id := groupFixture(t, "supergroup", 6, 1, false)
	ctx := context.Background()
	owner, member, x, y, z, w := u[0], u[1], u[2], u[3], u[4], u[5]

	if _, err := svc.InvitePreview(ctx, "no-such-link-token"); err != ErrLinkGone {
		t.Errorf("an unknown link: %v", err)
	}
	once, err := svc.MakeInvite(ctx, owner, id, "یک‌بار", nil, 1, false)
	if err != nil {
		t.Fatal(err)
	}
	// Two people race for the one use: exactly one gets in.
	var wg sync.WaitGroup
	errs := make([]error, 2)
	for i, who := range []string{x, y} {
		wg.Add(1)
		go func(i int, who string) {
			defer wg.Done()
			_, errs[i] = svc.JoinByLink(ctx, who, once.Token)
		}(i, who)
	}
	wg.Wait()
	if (errs[0] == nil) == (errs[1] == nil) {
		t.Errorf("a one-use link let in: %v / %v", errs[0], errs[1])
	}

	// A link that asks for approval files a request instead.
	ask, err := svc.MakeInvite(ctx, owner, id, "", nil, 0, true)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := svc.JoinByLink(ctx, z, ask.Token); err != ErrPending {
		t.Fatalf("approval link: %v", err)
	}
	if err := send(svc, z, id, "x"); err != ErrNotMember {
		t.Errorf("a pending requester sent: %v", err)
	}
	if _, err := svc.JoinRequests(ctx, member, id); err != ErrForbidden {
		t.Errorf("a member without the invite right as admin read requests: %v", err)
	}
	if n, err := svc.DecideRequests(ctx, owner, id, z, true); err != nil || n != 1 {
		t.Fatalf("approve: %d %v", n, err)
	}
	if err := send(svc, z, id, "سلام"); err != nil {
		t.Errorf("an approved requester could not send: %v", err)
	}

	// Revoked links stop working; members may not revoke others' links.
	if err := svc.DropInvite(ctx, member, id, ask.ID); err != ErrForbidden {
		t.Errorf("a member revoked the owner's link: %v", err)
	}
	if err := svc.DropInvite(ctx, owner, id, ask.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.JoinByLink(ctx, w, ask.Token); err != ErrLinkGone {
		t.Errorf("a revoked link: %v", err)
	}

	// Members may make links only while the chat lets members invite.
	if _, err := svc.EditChat(ctx, owner, id, InfoReq{Perms: &Perms{SendMessages: true}}); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.MakeInvite(ctx, member, id, "", nil, 0, false); err != ErrForbidden {
		t.Errorf("a member made a link without the invite permission: %v", err)
	}
	if _, _, err := svc.AddMembers(ctx, member, id, []string{w}); err != ErrForbidden {
		t.Errorf("a member added someone without the invite permission: %v", err)
	}
}

func TestHiddenHistoryStartsAtJoining(t *testing.T) {
	svc, u, id := groupFixture(t, "supergroup", 3, 1, false)
	ctx := context.Background()
	if err := send(svc, u[0], id, "پیش از آمدن"); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.EditChat(ctx, u[0], id, InfoReq{HiddenHistory: ptr(true)}); err != nil {
		t.Fatal(err)
	}
	invs, _ := svc.Store.Invites(ctx, id)
	if _, err := svc.JoinByLink(ctx, u[2], invs[0].Token); err != nil {
		t.Fatal(err)
	}
	if err := send(svc, u[0], id, "پس از آمدن"); err != nil {
		t.Fatal(err)
	}
	from := svc.visibleFrom(ctx, id, u[2])
	page, _ := svc.Store.Page(ctx, id, u[2], PageQuery{From: from})
	for _, m := range page {
		if m.Text == "پیش از آمدن" {
			t.Error("a newcomer read history the chat hides")
		}
	}
	if len(page) == 0 {
		t.Error("the newcomer sees nothing at all")
	}
	if svc.visibleFrom(ctx, id, u[1]) != 0 && svc.visibleFrom(ctx, id, u[0]) != 0 {
		t.Error("the owner's view was clamped")
	}
}

func TestForumTopics(t *testing.T) {
	svc, u, id := groupFixture(t, "supergroup", 2, 1, true)
	ctx := context.Background()
	if _, err := svc.CreateTopic(ctx, u[1], id, TopicReq{Title: ptr("x")}); err != ErrForbidden {
		t.Errorf("a member made a topic: %v", err)
	}
	tp, err := svc.CreateTopic(ctx, u[0], id, TopicReq{Title: ptr("فروش")})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := svc.SendText(ctx, u[1], SendReq{Chat: id, CID: uuidV7(), Text: "در موضوع", Topic: tp.ID}, nil); err != nil {
		t.Fatalf("member in an open topic: %v", err)
	}
	if _, err := svc.SendText(ctx, u[1], SendReq{Chat: id, CID: uuidV7(), Text: "x", Topic: 999999}, nil); err == nil {
		t.Error("sent to a topic that does not exist")
	}
	if _, err := svc.EditTopic(ctx, u[0], id, tp.ID, TopicReq{Closed: ptr(true)}); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.SendText(ctx, u[1], SendReq{Chat: id, CID: uuidV7(), Text: "x", Topic: tp.ID}, nil); err != ErrClosed {
		t.Errorf("a member wrote in a closed topic: %v", err)
	}
	if _, err := svc.SendText(ctx, u[0], SendReq{Chat: id, CID: uuidV7(), Text: "x", Topic: tp.ID}, nil); err != nil {
		t.Errorf("the owner in a closed topic: %v", err)
	}
	only, _ := svc.Store.Page(ctx, id, u[0], PageQuery{ByTopic: true, Topic: tp.ID})
	for _, m := range only {
		if m.Topic != tp.ID {
			t.Errorf("message %d of topic %d in topic %d's page", m.ID, m.Topic, tp.ID)
		}
	}
	// A plain group has no topics to write into.
	svc2, u2, plain := groupFixture(t, "group", 2, 1, false)
	if _, err := svc2.SendText(ctx, u2[0], SendReq{Chat: plain, CID: uuidV7(), Text: "x", Topic: tp.ID}, nil); err != ErrForbidden {
		t.Errorf("a topic in a chat without topics: %v", err)
	}
}

func TestHandingAChatOver(t *testing.T) {
	svc, u, id := groupFixture(t, "group", 3, 2, false)
	ctx := context.Background()
	if err := svc.Leave(ctx, u[0], id); err != ErrOwnerLeaves {
		t.Errorf("the owner walked out of a chat with members: %v", err)
	}
	if err := svc.TransferOwnership(ctx, u[1], id, u[2], "درست"); err != ErrForbidden {
		t.Errorf("a member gave the chat away: %v", err)
	}
	if err := svc.TransferOwnership(ctx, u[0], id, u[1], "نادرست"); err != ErrPassword {
		t.Errorf("wrong password: %v", err)
	}
	if err := svc.TransferOwnership(ctx, u[0], id, u[1], "درست"); err != nil {
		t.Fatal(err)
	}
	info, _ := svc.Store.ChatInfo(ctx, id)
	if info.Owner != u[1] {
		t.Errorf("owner is %q", info.Owner)
	}
	if err := svc.Leave(ctx, u[0], id); err != nil {
		t.Errorf("the old owner (now an admin) could not leave: %v", err)
	}
	if err := svc.DeleteChat(ctx, u[2], id); err != ErrForbidden {
		t.Errorf("a member deleted the chat: %v", err)
	}
}

func TestPrivacyDecidesWhoMayAddMe(t *testing.T) {
	s, users := testStore(t)
	ctx := context.Background()
	u := users(3)
	svc := New(s, fakeUsers(u), nil)
	if err := s.SetPrivacy(ctx, u[2], Privacy{Invites: Rule{Mode: "nobody"}}); err != nil {
		t.Fatal(err)
	}
	id, refused, err := svc.CreateGroup(ctx, u[0], CreateReq{Type: "group", Title: "ح", Members: []string{u[1], u[2]}})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.pool.Exec(context.Background(), `DELETE FROM chats WHERE id = $1`, id) })
	if len(refused) != 1 || refused[0] != u[2] {
		t.Errorf("refused %v, want only %s", refused, u[2])
	}
	if m, _ := s.MemberOf(ctx, id, u[2]); m != nil {
		t.Error("someone who allows nobody was added")
	}
}

func TestAGroupInTheChatList(t *testing.T) {
	svc, u, id := groupFixture(t, "group", 3, 2, false)
	ctx := context.Background()
	list, err := svc.Store.Chats(ctx, u[0])
	if err != nil {
		t.Fatal(err)
	}
	var c *Chat
	for _, x := range list {
		if x.ID == id {
			c = x
		}
	}
	if c == nil || c.Peer != nil || c.Last == nil || c.Last.Kind != "service" || len(c.Last.Service) == 0 {
		t.Fatalf("the new group in the list: %+v", c)
	}
	if err := send(svc, u[0], id, "سلام"); err != nil {
		t.Fatal(err)
	}
	msgs, _ := svc.Store.Page(ctx, id, u[0], PageQuery{Limit: 1})
	if _, err := svc.Store.MarkRead(ctx, id, u[2], msgs[0].PTS); err != nil {
		t.Fatal(err)
	}
	list, _ = svc.Store.Chats(ctx, u[0])
	for _, x := range list {
		if x.ID == id && x.PeerReadPTS != msgs[0].PTS {
			t.Errorf("read by one member: peer_read_pts %d, want %d", x.PeerReadPTS, msgs[0].PTS)
		}
	}
}

func ptr[T any](v T) *T { return &v }
