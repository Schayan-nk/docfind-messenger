package chat

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
)

// Security check 2 against a real database (skipped without DOCFIND_PG_*):
// a file opens only for its uploader and for members of a chat it was sent in.
func TestMediaOpensOnlyForItsChat(t *testing.T) {
	s, users := testStore(t)
	ctx := context.Background()
	u := users(3)
	svc := New(s, fakeUsers(u), nil)
	m := &Media{Kind: "file", Mime: "application/pdf", Size: 10, Name: "a.pdf", sha: strings.Repeat("a", 64)}
	id, err := s.InsertMedia(ctx, m, u[0], false)
	if err != nil {
		t.Fatal(err)
	}
	may := func(who string) bool {
		ok, err := s.MayReadMedia(ctx, who, id)
		if err != nil {
			t.Fatal(err)
		}
		return ok
	}
	if !may(u[0]) || may(u[1]) || may(u[2]) {
		t.Fatalf("before sending: uploader %v, others %v %v", may(u[0]), may(u[1]), may(u[2]))
	}
	chatID, _ := s.OpenPrivate(ctx, u[0], u[1])
	// Nobody sends a file somebody else uploaded.
	if _, err := svc.SendText(ctx, u[1], SendReq{Chat: chatID, CID: "01928c5e-7a3b-7c2d-8e9f-0a1b2c3d4ea0", Media: id}, nil); err != ErrForbidden {
		t.Fatalf("a file was sent by someone who did not upload it: %v", err)
	}
	msgs, err := svc.SendText(ctx, u[0], SendReq{Chat: chatID, CID: "01928c5e-7a3b-7c2d-8e9f-0a1b2c3d4ea1", Media: id}, nil)
	if err != nil || len(msgs) != 1 || msgs[0].Media == nil || msgs[0].Kind != "file" {
		t.Fatalf("sending the file: %v %+v", err, msgs)
	}
	if !may(u[1]) || may(u[2]) {
		t.Fatalf("after sending: member %v, outsider %v", may(u[1]), may(u[2]))
	}
	// Deleted for everyone: the file closes for the other side.
	if err := svc.Delete(ctx, u[0], chatID, []int64{msgs[0].ID}, true, nil); err != nil {
		t.Fatal(err)
	}
	if may(u[1]) {
		t.Fatal("a deleted message's file still opens")
	}
	// Uploads are their owner's: nobody else resumes or finishes them.
	if err := s.CreateUpload(ctx, u[0], uuidV7(), "x.pdf", 100); err != nil {
		t.Fatal(err)
	}
	up := uuidV7()
	_ = s.CreateUpload(ctx, u[0], up, "y.pdf", 100)
	if _, err := s.UploadOf(ctx, u[1], up); err != ErrNotMember {
		t.Fatalf("someone else's upload was found: %v", err)
	}
}

// A photo reaches the other person's device, live and on catching up, with
// its file's details: without them the page drew nothing at all (6.7.1).
func TestAPhotoArrivesWithItsFile(t *testing.T) {
	s, users := testStore(t)
	ctx := context.Background()
	u := users(2)
	svc := New(s, fakeUsers(u), nil)
	id, err := s.InsertMedia(ctx, &Media{Kind: "photo", Mime: "image/jpeg", Size: 10, Width: 640, Height: 480, sha: strings.Repeat("b", 64)}, u[0], false)
	if err != nil {
		t.Fatal(err)
	}
	chatID, _ := s.OpenPrivate(ctx, u[0], u[1])
	other := device(svc, u[1])
	if _, err := svc.SendText(ctx, u[0], SendReq{Chat: chatID, CID: uuidV7(), Media: id}, nil); err != nil {
		t.Fatal(err)
	}
	var up struct {
		Kind string `json:"kind"`
		D    struct {
			Kind  string `json:"kind"`
			Media *struct {
				ID int64 `json:"id"`
			} `json:"media"`
		} `json:"d"`
	}
	if err := json.Unmarshal(expect(t, other, "update"), &up); err != nil || up.D.Media == nil || up.D.Media.ID != id {
		t.Fatalf("the live update carried no file: %+v %v", up, err)
	}
	ups, _, err := s.Difference(ctx, chatID, 0, 50)
	if err != nil || len(ups) == 0 || !strings.Contains(string(ups[len(ups)-1].Payload), `"media"`) {
		t.Fatalf("catching up, the photo has no file: %v", err)
	}
}
