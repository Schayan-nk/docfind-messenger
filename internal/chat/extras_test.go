package chat

import (
	"bytes"
	"compress/gzip"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

// Phase 11 rules that need no database: polls, pack files (security checks
// 16 and 17), locations, schedule times, translation, forwarding.

func TestCleanPoll(t *testing.T) {
	ok := PollReq{Question: "ناهار کجا؟", Options: []string{"رستوران", "سفارش"}}
	if d, err := cleanPoll(ok); err != nil || len(d.Options) != 2 {
		t.Fatalf("a plain poll: %v", err)
	}
	quiz := PollReq{Question: "پایتخت آلمان؟", Options: []string{"برلین", "مونیخ", "هامبورگ"}, Quiz: true, Correct: 0, Explanation: "برلین"}
	if _, err := cleanPoll(quiz); err != nil {
		t.Fatalf("a quiz: %v", err)
	}
	bad := map[string]PollReq{
		"no question":        {Options: []string{"a", "b"}},
		"one option":         {Question: "q", Options: []string{"a"}},
		"eleven options":     {Question: "q", Options: strings.Split("a b c d e f g h i j k", " ")},
		"same option twice":  {Question: "q", Options: []string{"بله", " بله "}},
		"Arabic and Persian": {Question: "q", Options: []string{"كيك", "کیک"}},
		"empty option":       {Question: "q", Options: []string{"a", "  "}},
		"quiz out of range":  {Question: "q", Options: []string{"a", "b"}, Quiz: true, Correct: 2},
		"quiz multiple":      {Question: "q", Options: []string{"a", "b"}, Quiz: true, Multiple: true},
		"explain non-quiz":   {Question: "q", Options: []string{"a", "b"}, Explanation: "x"},
		"closes too soon":    {Question: "q", Options: []string{"a", "b"}, CloseIn: 2},
		"closes too late":    {Question: "q", Options: []string{"a", "b"}, CloseIn: 40 * 24 * 3600},
		"long option":        {Question: "q", Options: []string{"a", strings.Repeat("x", 101)}},
	}
	for name, r := range bad {
		if _, err := cleanPoll(r); !errors.Is(err, ErrBadPoll) {
			t.Errorf("%s: %v", name, err)
		}
	}
}

func gz(b []byte) []byte {
	var buf bytes.Buffer
	w := gzip.NewWriter(&buf)
	w.Write(b)
	w.Close()
	return buf.Bytes()
}

const lottieOK = `{"v":"5.7.4","fr":30,"ip":0,"op":60,"w":512,"h":512,"layers":[{"ty":4,"nm":"heart","ks":{"o":{"a":0,"k":100,"x":"var $bm_rt = 1;"}},"shapes":[]}]}`

func TestPackFilesAndLottie(t *testing.T) {
	// An expression is removed, and what is kept is plain JSON.
	f, mime, out, w, h, err := checkPackFile("sticker", gz([]byte(lottieOK)))
	if err != nil || f != "tgs" || mime != "application/json" || w != 512 || h != 512 {
		t.Fatalf("a good sticker: %v %s %s %dx%d", err, f, mime, w, h)
	}
	if bytes.Contains(out, []byte("$bm_rt")) || bytes.Contains(out, []byte(`"x"`)) {
		t.Fatalf("the expression survived: %s", out)
	}
	var back map[string]any
	if json.Unmarshal(out, &back) != nil {
		t.Fatal("the kept file is not JSON")
	}
	refused := map[string][]byte{
		"code in a name":     gz([]byte(strings.Replace(lottieOK, `"nm":"heart"`, `"nm":"eval(alert(1))"`, 1))),
		"thisComp in a name": gz([]byte(strings.Replace(lottieOK, `"nm":"heart"`, `"nm":"thisComp.layer(1)"`, 1))),
		"outside image":      gz([]byte(strings.Replace(lottieOK, `"layers"`, `"assets":[{"id":"i","u":"https://evil.example/","p":"x.png","e":0}],"layers"`, 1))),
		"outside link":       gz([]byte(strings.Replace(lottieOK, `"nm":"heart"`, `"nm":"see https://x.example"`, 1))),
		"too long":           gz([]byte(strings.Replace(lottieOK, `"op":60`, `"op":6000`, 1))),
		"too big a canvas":   gz([]byte(strings.Replace(lottieOK, `"w":512`, `"w":4096`, 1))),
		"no layers":          gz([]byte(`{"v":"5","fr":30,"ip":0,"op":30,"w":100,"h":100}`)),
		"not JSON":           gz([]byte("hello")),
		"too many layers":    gz([]byte(strings.Replace(lottieOK, `"layers":[`, `"layers":[`+strings.Repeat(`{"ty":3},`, 1001), 1))),
	}
	for name, b := range refused {
		if _, _, _, _, _, err := checkPackFile("sticker", b); err == nil {
			t.Errorf("%s: accepted", name)
		}
	}
	// A gzip bomb stops at the limit instead of filling memory.
	bomb := gz(append([]byte(`{"layers":[],"nm":"`), bytes.Repeat([]byte("a"), maxLottieRaw+10)...))
	if len(bomb) <= maxTGS {
		if _, _, _, _, _, err := checkPackFile("sticker", bomb); err == nil {
			t.Error("a file that unpacks past the limit was accepted")
		}
	}
	// Bytes, not names: an executable or a GIF is not a sticker; a WebP is not a GIF.
	webp := append([]byte("RIFF\x00\x00\x00\x00WEBPVP8X"), make([]byte, 20)...)
	cases := []struct {
		kind string
		b    []byte
		ok   bool
	}{
		{"sticker", []byte("MZ\x90\x00 this is a program"), false},
		{"sticker", webp, true},
		{"gif", webp, false},
		{"gif", append([]byte("GIF89a\x10\x00\x10\x00"), make([]byte, 10)...), true},
		{"sticker", append([]byte("GIF89a\x10\x00\x10\x00"), make([]byte, 10)...), false},
		{"sticker", append([]byte{0x1a, 0x45, 0xdf, 0xa3}, make([]byte, maxWebMSt)...), false},
		{"gif", append([]byte{0, 0, 0, 0x18}, []byte("ftypmp42xxxxxxxx")...), true},
		{"sticker", []byte(`<svg onload="alert(1)"></svg>`), false},
	}
	for i, c := range cases {
		if _, _, _, _, _, err := checkPackFile(c.kind, c.b); (err == nil) != c.ok {
			t.Errorf("case %d (%s): err=%v", i, c.kind, err)
		}
	}
}

func TestPlacesAndTimes(t *testing.T) {
	if p, err := cleanPlace(35.6891975, 51.3889736, 12.7); err != nil || p.Lat != 35.689198 || p.Lng != 51.388974 || p.Acc != 12 {
		t.Fatalf("Tehran: %+v %v", p, err)
	}
	for _, bad := range [][2]float64{{91, 0}, {0, 181}, {-91, 0}, {0, 0}} {
		if _, err := cleanPlace(bad[0], bad[1], 0); err == nil {
			t.Errorf("%v accepted", bad)
		}
	}
	now := time.Date(2026, 10, 13, 12, 0, 0, 0, time.UTC)
	if _, err := whenFor(now.Add(2*time.Hour).Format(time.RFC3339), now); err != nil {
		t.Errorf("two hours ahead: %v", err)
	}
	for _, bad := range []string{now.Add(-time.Hour).Format(time.RFC3339), now.Add(10 * time.Second).Format(time.RFC3339), now.Add(400 * 24 * time.Hour).Format(time.RFC3339), "tomorrow"} {
		if _, err := whenFor(bad, now); !errors.Is(err, ErrBadTime) {
			t.Errorf("%q: %v", bad, err)
		}
	}
}

func TestForwardKeepsOnlyWhatTravels(t *testing.T) {
	until := time.Now().Add(time.Hour)
	live, _ := json.Marshal(Place{Lat: 35.7, Lng: 51.4, Until: &until})
	var p Place
	json.Unmarshal(forwardExtra(&Message{Kind: "location", Extra: live}), &p)
	if p.Until != nil || p.Lat != 35.7 {
		t.Errorf("a forwarded live location stays live: %+v", p)
	}
	poll, _ := json.Marshal(PollDef{ID: 9, Question: "q", Options: []string{"a", "b"}})
	var d PollDef
	json.Unmarshal(forwardExtra(&Message{Kind: "poll", Extra: poll}), &d)
	if !d.Forwarded {
		t.Error("a forwarded poll can be voted on")
	}
}

func TestTranslatorTalksOnlyToOllama(t *testing.T) {
	var asked []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		asked = append(asked, r.URL.Path)
		switch r.URL.Path {
		case "/api/tags":
			w.Write([]byte(`{"models":[{"name":"qwen2.5:7b"}]}`))
		case "/api/chat":
			var b struct {
				Model    string `json:"model"`
				Messages []struct{ Role, Content string }
			}
			json.NewDecoder(r.Body).Decode(&b)
			if b.Model != "qwen2.5:7b" || len(b.Messages) != 2 || !strings.Contains(b.Messages[0].Content, "German") {
				w.WriteHeader(400)
				return
			}
			w.Write([]byte(`{"message":{"role":"assistant","content":"  Guten Morgen  "}}`))
		}
	}))
	defer srv.Close()
	tr := newTranslator(srv.URL, "qwen2.5:7b")
	if !tr.available(context.Background()) {
		t.Fatal("not available")
	}
	got, err := tr.translate(context.Background(), "1|0|de", "صبح بخیر", "de")
	if err != nil || got != "Guten Morgen" {
		t.Fatalf("got %q %v", got, err)
	}
	tr.translate(context.Background(), "1|0|de", "صبح بخیر", "de")
	if n := strings.Count(strings.Join(asked, " "), "/api/chat"); n != 1 {
		t.Errorf("the second ask was not cached: %d calls", n)
	}
	if newTranslator(srv.URL, "missing-model").available(context.Background()) {
		t.Error("a model Ollama does not have counts as available")
	}
	var none *translator
	if none.available(context.Background()) {
		t.Error("no configuration counts as available")
	}
}
