package chat

import (
	"bytes"
	"context"
	"image"
	"image/color"
	"image/jpeg"
	"strings"
	"testing"
)

// Security check 6: an executable renamed to .jpg is refused, whatever it is
// sent as. Programs and scripts are refused by their first bytes and by name.
func TestExecutablesAreRefused(t *testing.T) {
	cases := []struct {
		name string
		head []byte
		kind string
	}{
		{"photo.jpg", []byte("MZ\x90\x00\x03\x00\x00\x00"), "photo"},
		{"photo.jpg", []byte("MZ\x90\x00\x03\x00\x00\x00"), "file"},
		{"report.pdf", []byte("\x7fELF\x02\x01\x01"), "file"},
		{"run.txt", []byte("#!/bin/sh\nrm -rf /\n"), "file"},
		{"invoice.pdf.bat", []byte("@echo off\r\n"), "file"},
		{"tool.PS1", []byte("Write-Host hi"), "file"},
		{"x.jar", []byte("PK\x03\x04"), "file"},
		{"mac", []byte{0xCF, 0xFA, 0xED, 0xFE, 0, 0}, "file"},
	}
	for _, c := range cases {
		if _, _, err := classify(c.head, c.name, c.kind, false, false); err != ErrBadMedia {
			t.Errorf("%s as %s was accepted", c.name, c.kind)
		}
	}
	if _, _, err := classify([]byte("MZ\x90"), "tool.exe", "file", false, true); err != nil {
		t.Errorf("an administrator's allowance was ignored: %v", err)
	}
}

func TestAFileMustBeWhatItIsSentAs(t *testing.T) {
	jpg := []byte("\xff\xd8\xff\xe0\x00\x10JFIF\x00")
	if mime, kind, err := classify(jpg, "a.jpg", "photo", false, false); err != nil || mime != "image/jpeg" || kind != "photo" {
		t.Errorf("a JPEG photo: %s %s %v", mime, kind, err)
	}
	if _, _, err := classify([]byte("%PDF-1.7\n"), "a.jpg", "photo", false, false); err != ErrBadMedia {
		t.Errorf("a PDF was taken as a photo")
	}
	if _, kind, err := classify(jpg, "a.jpg", "photo", true, false); err != nil || kind != "file" {
		t.Errorf("a photo sent as a file: %s %v", kind, err)
	}
	if mime, _, err := classify([]byte("\x1aE\xdf\xa3\x9fB\x86\x81\x01"), "v.webm", "voice", false, false); err != nil || mime != "audio/webm" {
		t.Errorf("a WebM voice message: %s %v", mime, err)
	}
	if mime, _, err := classify([]byte("<!DOCTYPE html><html><script>alert(1)</script>"), "page.html", "file", false, false); err != nil || mime != "application/octet-stream" {
		t.Errorf("an HTML file would be served as a page: %s %v", mime, err)
	}
	if _, _, err := classify([]byte("hello"), "a.txt", "sticker", false, false); err != ErrBadMedia {
		t.Errorf("an unknown kind was taken")
	}
}

// Security check 7: a file name is shown, never used as a path.
func TestUploadNamesAreNotPaths(t *testing.T) {
	for in, want := range map[string]string{
		`..\..\windows\system32\evil.dll`: "evil.dll",
		"../../etc/passwd":                "passwd",
		"a\x00b\x07c.txt":                 "abc.txt",
		"  گزارش ماهانه.pdf ":             "گزارش ماهانه.pdf",
	} {
		if got := cleanName(in); got != want {
			t.Errorf("cleanName(%q) = %q, want %q", in, got, want)
		}
	}
	m := &MediaFolder{fallback: `D:\chat-media`}
	if p := m.pathFor(strings.Repeat("ab", 32)); !strings.HasPrefix(p, `D:\chat-media`) || strings.Contains(p, "..") {
		t.Errorf("content path %s", p)
	}
}

func TestPhotosLoseTheirLocation(t *testing.T) {
	img := image.NewRGBA(image.Rect(0, 0, 40, 30))
	for x := 0; x < 40; x++ {
		img.Set(x, x%30, color.RGBA{200, 10, 10, 255})
	}
	var buf bytes.Buffer
	if err := jpeg.Encode(&buf, img, nil); err != nil {
		t.Fatal(err)
	}
	src := buf.Bytes()
	body := []byte("Exif\x00\x00GPS 35.7N 51.4E")
	exif := append([]byte{0xFF, 0xE1, byte((len(body) + 2) >> 8), byte(len(body) + 2)}, body...)
	with := append(append(append([]byte{}, src[:2]...), exif...), src[2:]...)
	out, ok := stripJPEGMeta(with)
	if !ok || bytes.Contains(out, []byte("GPS")) {
		t.Fatalf("the location survived: ok=%v", ok)
	}
	if _, err := jpeg.Decode(bytes.NewReader(out)); err != nil {
		t.Fatalf("the stripped photo no longer decodes: %v", err)
	}
	if got := shrink(img, 20).Bounds(); got.Dx() != 20 || got.Dy() != 15 {
		t.Errorf("thumbnail size %v", got)
	}
}

// Security check 8: link previews never reach this machine, the office
// network or a cloud metadata address.
func TestLinkPreviewsStayOnThePublicInternet(t *testing.T) {
	s := &Service{}
	for _, u := range []string{
		"http://127.0.0.1:8300/api/chat/chats", "http://localhost/", "http://[::1]/", "http://10.0.0.5/",
		"http://192.168.77.13/", "http://172.16.0.1/", "http://169.254.169.254/latest/meta-data/",
		"http://0.0.0.0/", "file:///C:/Windows/win.ini", "gopher://example.com/",
	} {
		if p, err := s.fetchPreview(context.Background(), u); err == nil {
			t.Errorf("%s was fetched: %+v", u, p)
		}
	}
}

func TestPreviewTagsAreRead(t *testing.T) {
	page := []byte(`<html><head><meta charset="utf-8"><title>Fallback</title>
<meta property="og:title" content="Acme &amp; Co">
<meta property='og:description' content='شرکت نمونه'>
<meta property="og:site_name" content="example.ir">
<meta property="og:image" content="/logo.png"></head></html>`)
	p := parsePreview(page, "")
	if p.Title != "Acme & Co" || p.Desc != "شرکت نمونه" || p.Site != "example.ir" || p.imageURL != "/logo.png" {
		t.Fatalf("%+v", p)
	}
	// An older Persian site in Windows-1256: "سلام" is D3 E1 C7 E3 there.
	p = parsePreview([]byte("<html><head><title>\xd3\xe1\xc7\xe3</title></head></html>"), "windows-1256")
	if p.Title != "سلام" {
		t.Fatalf("windows-1256 title: %q", p.Title)
	}
	if got := firstLink("ببینید chat.example.ir/x", []Entity{{Type: "url", Offset: 7, Length: 21}}); got != "https://chat.example.ir/x" {
		t.Errorf("firstLink %q", got)
	}
}
