package chat

import (
	"context"
	"strings"
	"testing"
	"time"
)

// Phase 5 against a real database (skipped without DOCFIND_PG_*): search and
// its normalisation, a person's settings for each chat, folders, clearing.

// The index is normalised by SQL and the query by Go: they must agree.
func TestNormalizeMatchesSQL(t *testing.T) {
	s, _ := testStore(t)
	ctx := context.Background()
	for _, in := range []string{
		"كتاب", "علي و ولي", "می‌خواهم", "مدرســـه", "۱۴۰۳/۰۷/۲۱ و ٢٠٢٦", "قراردادِ فولادْ", "مسئول",
		"«سلام»، دنیا؛ چطوری؟", "ۀ و ة", "أحمد إسلام ٱ", "Report_1403.PDF", "B2B – ERP -- 50٪", "😀 ok",
	} {
		var norm, fold string
		if err := s.pool.QueryRow(ctx, `SELECT lower(chat_norm($1)), chat_fold(lower(chat_norm($1)))`, in).Scan(&norm, &fold); err != nil {
			t.Fatal(err)
		}
		if norm != Normalize(in) {
			t.Errorf("chat_norm(%q) = %q, Normalize = %q", in, norm, Normalize(in))
		}
		if fold != Fold(Normalize(in)) {
			t.Errorf("chat_fold(%q) = %q, Fold = %q", in, fold, Fold(Normalize(in)))
		}
	}
}

func TestSearchQueryIsOnlyWords(t *testing.T) {
	for in, want := range map[string]string{
		"":                        "",
		"  ":                      "",
		"foo & bar | !baz:*":      "foo:* & bar:* & baz:*",
		"قرارداد":                 Fold("قرارداد") + ":*",
		"'); DROP TABLE x; --":    "drop:* & table:* & x:*",
		"a b c d e f g h i j k l": "a:* & b:* & c:* & d:* & e:* & f:* & g:* & h:*",
	} {
		if got := searchQuery(in); got != want {
			t.Errorf("searchQuery(%q) = %q, want %q", in, got, want)
		}
	}
}

func found(t *testing.T, svc *Service, user string, req SearchReq) []string {
	t.Helper()
	msgs, err := svc.Store.Search(context.Background(), user, req)
	if err != nil {
		t.Fatalf("search %+v: %v", req, err)
	}
	var out []string
	for _, m := range msgs {
		out = append(out, m.Text)
	}
	return out
}

func has(list []string, text string) bool {
	for _, x := range list {
		if x == text {
			return true
		}
	}
	return false
}

// Security (SPEC 6.6): a search never returns a message the person may not
// read: another chat's, a hidden history's, a cleared one, one deleted for
// everyone or for themselves.
func TestSearchNeverLeavesYourChats(t *testing.T) {
	s, users := testStore(t)
	ctx := context.Background()
	u := users(3)
	svc := New(s, fakeUsers(u), nil)
	ab, _ := s.OpenPrivate(ctx, u[0], u[1])
	cb, _ := s.OpenPrivate(ctx, u[2], u[1])
	mine := "قرارداد فولاد نمونه ۱۲۳۴۵۶"
	theirs := "قرارداد سیمان تهران"
	for _, x := range []struct {
		who  string
		chat int64
		text string
	}{{u[0], ab, mine}, {u[2], cb, theirs}, {u[0], ab, "می‌خواهم کتاب را ببینم https://example.com/x"}} {
		if err := send(svc, x.who, x.chat, x.text); err != nil {
			t.Fatal(err)
		}
	}
	got := found(t, svc, u[0], SearchReq{Q: "قرارداد"})
	if !has(got, mine) || has(got, theirs) {
		t.Fatalf("u0 searching «قرارداد»: %q", got)
	}
	if got := found(t, svc, u[1], SearchReq{Q: "قرارداد"}); !has(got, mine) || !has(got, theirs) {
		t.Errorf("u1 is in both chats: %q", got)
	}
	if _, err := svc.Search(ctx, u[0], SearchReq{Q: "قرارداد", Chat: cb}); err != ErrNotMember {
		t.Errorf("searching inside someone else's chat: %v", err)
	}
	if got := found(t, svc, u[0], SearchReq{Q: "قرارداد", Chat: cb}); len(got) != 0 {
		t.Errorf("the store itself found %q in a chat u0 is not in", got)
	}
	// Normalisation and prefixes: Arabic kaf, the zero-width joiner, digits, a word's start.
	for _, q := range []string{"كتاب", "میخواهم", "123456", "۱۲۳", "فولا", "نمونه قرار"} {
		if got := found(t, svc, u[0], SearchReq{Q: q}); len(got) == 0 {
			t.Errorf("«%s» found nothing", q)
		}
	}
	if got := found(t, svc, u[0], SearchReq{Kind: "links", Chat: ab}); len(got) != 1 || !strings.Contains(got[0], "https://") {
		t.Errorf("links in the chat: %q", got)
	}
	// Deleted for me, then for everyone, then a cleared history.
	msgs, _ := s.Page(ctx, ab, u[0], PageQuery{})
	var mineID int64
	for _, m := range msgs {
		if m.Text == mine {
			mineID = m.ID
		}
	}
	if err := svc.Delete(ctx, u[0], ab, []int64{mineID}, false, nil); err != nil {
		t.Fatal(err)
	}
	if has(found(t, svc, u[0], SearchReq{Q: "فولاد"}), mine) {
		t.Error("a message deleted for me is still found by me")
	}
	if !has(found(t, svc, u[1], SearchReq{Q: "فولاد"}), mine) {
		t.Error("deleting for myself hid it from the other side")
	}
	if err := svc.Delete(ctx, u[1], ab, []int64{mineID}, true, nil); err != nil {
		t.Fatal(err)
	}
	if got := found(t, svc, u[1], SearchReq{Q: "فولاد"}); len(got) != 0 {
		t.Errorf("deleted for everyone, still found: %q", got)
	}
	if err := svc.ClearChat(ctx, u[1], cb, false); err != nil {
		t.Fatal(err)
	}
	if has(found(t, svc, u[1], SearchReq{Q: "سیمان"}), theirs) {
		t.Error("a cleared history is still searchable")
	}
	if !has(found(t, svc, u[2], SearchReq{Q: "سیمان"}), theirs) {
		t.Error("clearing for me cleared the other side")
	}
}

func TestSearchFilesAndHiddenHistory(t *testing.T) {
	svc, u, id := groupFixture(t, "supergroup", 3, 1, false)
	ctx := context.Background()
	s := svc.Store
	m := &Media{Kind: "file", Mime: "application/pdf", Size: 10, Name: "پیش‌فاکتور_ORD-1001.pdf", sha: strings.Repeat("b", 64)}
	mid, err := s.InsertMedia(ctx, m, u[0], false)
	if err != nil {
		t.Fatal(err)
	}
	if err := s.SetOCR(ctx, mid, "مبلغ کل: ۱۲٬۵۰۰ یورو، تحویل بندرعباس"); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.SendText(ctx, u[0], SendReq{Chat: id, CID: uuidV7(), Media: mid}, nil); err != nil {
		t.Fatal(err)
	}
	for _, q := range []string{"بندرعباس", "یورو", "1001", "پیشفاکتور"} {
		if got := found(t, svc, u[1], SearchReq{Q: q}); len(got) != 1 {
			t.Errorf("«%s» in a file's name or text: %d results", q, len(got))
		}
	}
	if got := found(t, svc, u[1], SearchReq{Kind: "files", Chat: id}); len(got) != 1 {
		t.Errorf("the chat's files: %d", len(got))
	}
	// A newcomer to a supergroup that hides its history finds nothing from before.
	if _, err := svc.EditChat(ctx, u[0], id, InfoReq{HiddenHistory: ptr(true)}); err != nil {
		t.Fatal(err)
	}
	invs, _ := s.Invites(ctx, id)
	if _, err := svc.JoinByLink(ctx, u[2], invs[0].Token); err != nil {
		t.Fatal(err)
	}
	if got := found(t, svc, u[2], SearchReq{Q: "بندرعباس"}); len(got) != 0 {
		t.Errorf("a newcomer found hidden history: %q", got)
	}
}

func TestPinMuteArchiveMarkUnread(t *testing.T) {
	s, users := testStore(t)
	ctx := context.Background()
	u := users(8)
	svc := New(s, fakeUsers(u), nil)
	var chats []int64
	for i := 1; i < 8; i++ {
		id, _ := s.OpenPrivate(ctx, u[0], u[i])
		chats = append(chats, id)
	}
	on := true
	for i := 0; i < maxPinnedMain; i++ {
		if _, err := svc.SetPrefs(ctx, u[0], chats[i], PrefsReq{Pinned: &on}); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := svc.SetPrefs(ctx, u[0], chats[5], PrefsReq{Pinned: &on}); err != ErrPinLimit {
		t.Errorf("a sixth pin: %v", err)
	}
	if _, err := svc.SetPrefs(ctx, u[1], chats[2], PrefsReq{Pinned: &on}); err != ErrNotMember {
		t.Errorf("pinning someone else's chat: %v", err)
	}
	// Archiving a pinned chat unpins it; a new message brings it back unless muted.
	p, err := svc.SetPrefs(ctx, u[0], chats[0], PrefsReq{Archived: &on})
	if err != nil || !p.Archived || p.Pinned != 0 {
		t.Fatalf("archive: %+v %v", p, err)
	}
	if _, err := svc.SetPrefs(ctx, u[0], chats[1], PrefsReq{Archived: &on, Muted: &on}); err != nil {
		t.Fatal(err)
	}
	_ = send(svc, u[1], chats[0], "سلام")
	_ = send(svc, u[2], chats[1], "سلام")
	list, _ := s.Chats(ctx, u[0])
	for _, c := range list {
		switch c.ID {
		case chats[0]:
			if c.Archived {
				t.Error("an unmuted archived chat stayed archived on a new message")
			}
		case chats[1]:
			if !c.Archived || c.MutedUntil == nil || c.MutedUntil.Year() != 9999 {
				t.Errorf("a muted archived chat: archived %v, muted %v", c.Archived, c.MutedUntil)
			}
		}
	}
	past := time.Now().Add(-time.Hour)
	if _, err := svc.SetPrefs(ctx, u[0], chats[2], PrefsReq{Muted: &on, MutedUntil: &past}); err != ErrForbidden {
		t.Errorf("muting until the past: %v", err)
	}
	// Marked unread clears when the chat is read.
	if _, err := svc.SetPrefs(ctx, u[0], chats[3], PrefsReq{MarkedUnread: &on}); err != nil {
		t.Fatal(err)
	}
	if err := svc.MarkRead(ctx, u[0], chats[3], 1<<40, nil); err != nil {
		t.Fatal(err)
	}
	list, _ = s.Chats(ctx, u[0])
	for _, c := range list {
		if c.ID == chats[3] && c.MarkedUnread {
			t.Error("reading did not clear «marked unread»")
		}
	}
}

func TestClearingAPrivateChat(t *testing.T) {
	s, users := testStore(t)
	ctx := context.Background()
	u := users(2)
	svc := New(s, fakeUsers(u), nil)
	id, _ := s.OpenPrivate(ctx, u[0], u[1])
	_ = send(svc, u[0], id, "یک")
	if err := svc.ClearChat(ctx, u[0], id, false); err != nil {
		t.Fatal(err)
	}
	inList := func(user string) bool {
		list, _ := s.Chats(ctx, user)
		for _, c := range list {
			if c.ID == id {
				return true
			}
		}
		return false
	}
	if inList(u[0]) || !inList(u[1]) {
		t.Errorf("after clearing for me: in my list %v, in theirs %v", inList(u[0]), inList(u[1]))
	}
	if from := s.VisibleFrom(ctx, id, u[0]); from == 0 {
		t.Error("my view of the chat does not start after the cleared part")
	}
	_ = send(svc, u[1], id, "دو")
	if !inList(u[0]) {
		t.Error("a new message did not bring the chat back")
	}
	if err := svc.ClearChat(ctx, u[1], id, true); err != nil {
		t.Fatal(err)
	}
	if got, _ := s.Page(ctx, id, u[0], PageQuery{From: s.VisibleFrom(ctx, id, u[0])}); len(got) != 0 {
		t.Errorf("cleared for both, the other side still reads %d messages", len(got))
	}
	gid, _, _ := svc.CreateGroup(ctx, u[0], CreateReq{Type: "group", Title: "گ", Members: []string{u[1]}})
	t.Cleanup(func() { s.pool.Exec(context.Background(), `DELETE FROM chats WHERE id = $1`, gid) })
	if err := svc.ClearChat(ctx, u[0], gid, true); err != ErrForbidden {
		t.Errorf("clearing a group for everyone: %v", err)
	}
}

func TestFoldersAndSharing(t *testing.T) {
	svc, u, gid := groupFixture(t, "supergroup", 3, 1, false)
	ctx := context.Background()
	s := svc.Store
	other, _ := s.OpenPrivate(ctx, u[1], u[2]) // not u[0]'s
	f, err := s.SaveFolder(ctx, u[0], &Folder{Title: "کار", IncludeChats: []int64{gid, other, gid}, Include: FolderRules{Groups: true}})
	if err != nil {
		t.Fatal(err)
	}
	if len(f.IncludeChats) != 1 || f.IncludeChats[0] != gid {
		t.Errorf("a folder kept chats that are not its owner's: %v", f.IncludeChats)
	}
	if _, err := s.SaveFolder(ctx, u[1], &Folder{ID: f.ID, Title: "ربوده"}); err != ErrNotMember {
		t.Errorf("changing someone else's folder: %v", err)
	}
	if err := s.DeleteFolder(ctx, u[1], f.ID); err != ErrNotMember {
		t.Errorf("deleting someone else's folder: %v", err)
	}
	tok, err := s.ShareFolder(ctx, u[0], f.ID)
	if err != nil || tok == "" {
		t.Fatal(err)
	}
	if again, _ := s.ShareFolder(ctx, u[0], f.ID); again != tok {
		t.Error("sharing twice made a second link")
	}
	p, err := svc.FolderPreview(ctx, tok)
	if err != nil || len(p["chats"].([]SharedChat)) != 1 {
		t.Fatalf("preview: %v %v", p, err)
	}
	nf, joined, err := svc.JoinFolder(ctx, u[2], tok)
	if err != nil || joined != 1 || len(nf.IncludeChats) != 1 {
		t.Fatalf("join: %+v %d %v", nf, joined, err)
	}
	if m, _ := s.MemberOf(ctx, gid, u[2]); m == nil || m.Role != "member" {
		t.Error("joining the folder did not join its chat")
	}
	if _, _, err := svc.JoinFolder(ctx, u[2], "no-such-folder-link"); err != ErrLinkGone {
		t.Errorf("an unknown folder link: %v", err)
	}
	t.Cleanup(func() { s.pool.Exec(context.Background(), `DELETE FROM chat_folders WHERE user_id = ANY($1)`, u) })
}
