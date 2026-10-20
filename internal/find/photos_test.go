package find

import (
	"bytes"
	"encoding/json"
	"image"
	"image/color"
	"image/jpeg"
	"image/png"
	"net/http"
	"os"
	"strings"
	"testing"
)

// Several pictures per account (photos.go), through the routes as a page uses
// them.

func picturesServer(t *testing.T) (*Server, string, string) {
	t.Helper()
	s, boss, reader, _ := accountsTestServer(t)
	s.avatarDir = tempDir(t)
	return s, boss, reader
}

// testImage is a w x h picture in one colour, encoded as kind.
func testImage(t *testing.T, w, h int, c color.RGBA, kind string) string {
	t.Helper()
	img := image.NewRGBA(image.Rect(0, 0, w, h))
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			img.SetRGBA(x, y, c)
		}
	}
	var b bytes.Buffer
	var err error
	if kind == "png" {
		err = png.Encode(&b, img)
	} else {
		err = jpeg.Encode(&b, img, &jpeg.Options{Quality: 90})
	}
	if err != nil {
		t.Fatal(err)
	}
	return b.String()
}

type photoList struct {
	Photos []struct {
		ID string `json:"id"`
	} `json:"photos"`
}

func listPhotos(t *testing.T, s *Server, token, who string) []string {
	t.Helper()
	rec := asUser(t, s, "GET", "/api/account/photos?u="+who, token, "")
	if rec.Code != http.StatusOK {
		t.Fatalf("listing %s's pictures: %d %s", who, rec.Code, rec.Body.String())
	}
	var l photoList
	if err := json.Unmarshal(rec.Body.Bytes(), &l); err != nil {
		t.Fatal(err)
	}
	var ids []string
	for _, p := range l.Photos {
		ids = append(ids, p.ID)
	}
	return ids
}

func upload(t *testing.T, s *Server, token, body string) string {
	t.Helper()
	rec := asUser(t, s, "POST", "/api/account/photo", token, body)
	if rec.Code != http.StatusOK {
		t.Fatalf("upload: %d %s", rec.Code, rec.Body.String())
	}
	var a struct {
		Photo struct {
			ID string `json:"id"`
		} `json:"photo"`
	}
	json.Unmarshal(rec.Body.Bytes(), &a)
	if !galleryID.MatchString(a.Photo.ID) {
		t.Fatalf("upload answered %s", rec.Body.String())
	}
	return a.Photo.ID
}

// decoded is the picture a route answered with, and its size.
func decoded(t *testing.T, s *Server, token, target string) (color.Color, int) {
	t.Helper()
	rec := asUser(t, s, "GET", target, token, "")
	if rec.Code != http.StatusOK {
		t.Fatalf("GET %s: %d", target, rec.Code)
	}
	img, kind, err := image.Decode(bytes.NewReader(rec.Body.Bytes()))
	if err != nil || kind != "jpeg" {
		t.Fatalf("GET %s: %s, %v", target, kind, err)
	}
	b := img.Bounds()
	if b.Dx() != b.Dy() {
		t.Fatalf("GET %s: %dx%d, want a square", target, b.Dx(), b.Dy())
	}
	return img.At(b.Dx()/2, b.Dy()/2), b.Dx()
}

func reddish(c color.Color) bool { r, g, _, _ := c.RGBA(); return r > 0xC000 && g < 0x4000 }
func bluish(c color.Color) bool  { _, g, b, _ := c.RGBA(); return b > 0xC000 && g < 0x4000 }

func TestPicturesAreKeptNewestFirstAndTheNewestIsDrawnEverywhere(t *testing.T) {
	s, boss, _ := picturesServer(t)
	red := upload(t, s, boss, testImage(t, 1600, 1200, color.RGBA{220, 20, 20, 255}, "png"))
	blue := upload(t, s, boss, testImage(t, 90, 90, color.RGBA{20, 20, 220, 255}, "jpeg"))

	if ids := listPhotos(t, s, boss, "boss"); len(ids) != 2 || ids[0] != blue || ids[1] != red {
		t.Fatalf("pictures %v, want [%s %s]", ids, blue, red)
	}
	// What every list draws is the newest, small.
	if c, side := decoded(t, s, boss, "/api/account/photo?u=boss"); !bluish(c) || side != 90 {
		t.Errorf("the current picture is %v at %d px, want the blue one, not enlarged", c, side)
	}
	// The older one, sharp: cropped to a square and reduced to galleryFull.
	if c, side := decoded(t, s, boss, "/api/account/photo?u=boss&id="+red+"&size=full"); !reddish(c) || side != galleryFull {
		t.Errorf("the red picture, full: %v at %d px, want red at %d", c, side, galleryFull)
	}
	if _, side := decoded(t, s, boss, "/api/account/photo?u=boss&id="+red); side != gallerySmall {
		t.Errorf("the red picture, small: %d px, want %d", side, gallerySmall)
	}
	if u, _ := s.guard.Users.Get("boss"); u.Avatar != "jpg" {
		t.Errorf("the account says its picture is %q", u.Avatar)
	}

	// An older one made current moves to the front under a new id.
	rec := asUser(t, s, "POST", "/api/account/photo/current?id="+red, boss, "")
	if rec.Code != http.StatusOK {
		t.Fatalf("making it current: %d %s", rec.Code, rec.Body.String())
	}
	ids := listPhotos(t, s, boss, "boss")
	if len(ids) != 2 || ids[1] != blue || ids[0] == red {
		t.Fatalf("after making red current: %v", ids)
	}
	if c, _ := decoded(t, s, boss, "/api/account/photo?u=boss"); !reddish(c) {
		t.Error("the current picture did not follow")
	}

	// Letting the current one go makes the next newest current.
	if rec := asUser(t, s, "DELETE", "/api/account/photo?id="+ids[0], boss, ""); rec.Code != http.StatusOK {
		t.Fatalf("deleting one: %d %s", rec.Code, rec.Body.String())
	}
	if c, _ := decoded(t, s, boss, "/api/account/photo?u=boss"); !bluish(c) {
		t.Error("deleting the current picture did not bring back the previous one")
	}
	// With no id, every picture goes, as the route always meant.
	if rec := asUser(t, s, "DELETE", "/api/account/photo", boss, ""); rec.Code != http.StatusOK {
		t.Fatalf("deleting all: %d", rec.Code)
	}
	if ids := listPhotos(t, s, boss, "boss"); len(ids) != 0 {
		t.Errorf("after deleting all: %v", ids)
	}
	if rec := asUser(t, s, "GET", "/api/account/photo?u=boss", boss, ""); rec.Code != http.StatusNotFound {
		t.Errorf("no picture answered %d", rec.Code)
	}
	if u, _ := s.guard.Users.Get("boss"); u.Avatar != "" {
		t.Errorf("the account still claims a picture: %q", u.Avatar)
	}
}

// A picture set before there was a gallery is its first entry, not lost.
func TestAPictureFromBeforeTheGalleryIsKept(t *testing.T) {
	s, boss, _ := picturesServer(t)
	if err := os.MkdirAll(s.avatarDir, 0o700); err != nil {
		t.Fatal(err)
	}
	old := testImage(t, 128, 128, color.RGBA{220, 20, 20, 255}, "png")
	if err := os.WriteFile(s.avatarPath("boss", "png"), []byte(old), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := s.guard.Users.SetAvatar("boss", "png"); err != nil {
		t.Fatal(err)
	}
	if ids := listPhotos(t, s, boss, "boss"); len(ids) != 1 {
		t.Fatalf("the earlier picture was not brought in: %v", ids)
	}
	upload(t, s, boss, testImage(t, 64, 64, color.RGBA{20, 20, 220, 255}, "jpeg"))
	ids := listPhotos(t, s, boss, "boss")
	if len(ids) != 2 {
		t.Fatalf("after a new one: %v, want the earlier one kept", ids)
	}
	if c, _ := decoded(t, s, boss, "/api/account/photo?u=boss&id="+ids[1]+"&size=full"); !reddish(c) {
		t.Error("the earlier picture changed")
	}
}

func TestOnlyTheNewestPicturesAreKept(t *testing.T) {
	s, boss, _ := picturesServer(t)
	pic := testImage(t, 32, 32, color.RGBA{20, 120, 20, 255}, "jpeg")
	var first string
	for i := 0; i < galleryMax+2; i++ {
		id := upload(t, s, boss, pic)
		if i == 0 {
			first = id
		}
	}
	ids := listPhotos(t, s, boss, "boss")
	if len(ids) != galleryMax {
		t.Fatalf("%d kept, want %d", len(ids), galleryMax)
	}
	for _, id := range ids {
		if id == first {
			t.Error("the oldest was kept past the limit")
		}
	}
}

// Anybody signed in sees a colleague's pictures; changing them is the
// colleague's own business or an administrator's.
func TestSomebodyElsesPicturesAreSeenButNotChanged(t *testing.T) {
	s, boss, reader := picturesServer(t)
	id := upload(t, s, boss, testImage(t, 40, 40, color.RGBA{20, 20, 220, 255}, "jpeg"))
	if ids := listPhotos(t, s, reader, "boss"); len(ids) != 1 {
		t.Fatalf("a colleague sees %v", ids)
	}
	for _, r := range []struct{ method, path string }{
		{"POST", "/api/account/photo/current?u=boss&id=" + id},
		{"DELETE", "/api/account/photo?u=boss&id=" + id},
		{"DELETE", "/api/account/photo?u=boss"},
		{"POST", "/api/account/photo?u=boss"},
	} {
		if rec := asUser(t, s, r.method, r.path, reader, "x"); rec.Code != http.StatusForbidden {
			t.Errorf("%s %s by a viewer answered %d, want 403", r.method, r.path, rec.Code)
		}
	}
	// The administrator may.
	if rec := asUser(t, s, "DELETE", "/api/account/photo?u=reader", boss, ""); rec.Code != http.StatusOK {
		t.Errorf("an administrator clearing a picture: %d", rec.Code)
	}
}

func TestWhatIsNotAPictureIsRefused(t *testing.T) {
	s, boss, _ := picturesServer(t)
	for name, body := range map[string]string{
		"svg":         `<svg xmlns="http://www.w3.org/2000/svg"><script>alert(1)</script></svg>`,
		"html":        "<html><body>hi</body></html>",
		"webp header": "RIFF\x00\x00\x00\x00WEBPVP8 " + strings.Repeat("\x00", 40),
		"bmp header":  "BM" + strings.Repeat("\x00", 60),
	} {
		if rec := asUser(t, s, "POST", "/api/account/photo", boss, body); rec.Code != http.StatusUnsupportedMediaType {
			t.Errorf("%s answered %d, want 415", name, rec.Code)
		}
	}
	if ids := listPhotos(t, s, boss, "boss"); len(ids) != 0 {
		t.Errorf("something was kept: %v", ids)
	}
	for _, p := range []string{
		"/api/account/photo?u=boss&id=../../users.json",
		"/api/account/photo?u=boss&id=1234",
		"/api/account/photo?u=boss&id=9999999999999&size=full",
	} {
		if rec := asUser(t, s, "GET", p, boss, ""); rec.Code != http.StatusNotFound {
			t.Errorf("GET %s answered %d, want 404", p, rec.Code)
		}
	}
	if rec := asUser(t, s, "POST", "/api/account/photo/current?id=../x", boss, ""); rec.Code != http.StatusNotFound {
		t.Errorf("making a bad id current answered %d", rec.Code)
	}
}
