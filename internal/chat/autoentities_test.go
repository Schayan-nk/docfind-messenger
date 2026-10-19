package chat

import "testing"

func TestAutoEntitiesFindsLinksInPersianText(t *testing.T) {
	text := "سلام @shayan_k ببینید https://chat.example.ir/messages. و a.b@example.ir #گزارش_ماهانه /start 09121234567 $USD www.x.ir"
	got := AutoEntities(text, nil)
	want := map[string]string{
		"mention":      "@shayan_k",
		"url":          "https://chat.example.ir/messages",
		"email":        "a.b@example.ir",
		"hashtag":      "#گزارش_ماهانه",
		"bot_command":  "/start",
		"phone_number": "09121234567",
		"cashtag":      "$USD",
	}
	seen := map[string]bool{}
	for _, e := range got {
		s := Slice16(text, e.Offset, e.Offset+e.Length)
		if w, ok := want[e.Type]; ok && w == s {
			seen[e.Type] = true
		}
		if e.Type == "url" && s == "www.x.ir" {
			seen["www"] = true
		}
	}
	for k := range want {
		if !seen[k] {
			t.Errorf("%s not found in %+v", k, got)
		}
	}
	if !seen["www"] {
		t.Errorf("www address not found")
	}
}

func TestAutoEntitiesLeavesFormattedSpansAlone(t *testing.T) {
	text := "کد https://x.ir اینجا"
	code := Entity{Type: "code", Offset: 3, Length: UTF16Len("https://x.ir")}
	got := AutoEntities(text, []Entity{code})
	if len(got) != 1 || got[0].Type != "code" {
		t.Fatalf("a link was found inside code: %+v", got)
	}
}

func TestSplitEntitiesFollowsThePieces(t *testing.T) {
	a := make([]rune, 0, MaxText+50)
	for i := 0; i < MaxText-5; i++ {
		a = append(a, 'x')
	}
	text := string(a) + " " + "bold tail"
	parts := SplitText(text)
	if len(parts) != 2 {
		t.Fatalf("parts %d", len(parts))
	}
	off := UTF16Len(string(a) + " ")
	ents := []Entity{{Type: "bold", Offset: off, Length: 4}, {Type: "italic", Offset: 0, Length: 3}}
	got := splitEntities(text, parts, ents)
	if len(got[0]) != 1 || got[0][0].Type != "italic" {
		t.Fatalf("first piece: %+v", got[0])
	}
	if len(got[1]) != 1 || got[1][0].Offset != 0 || Slice16(parts[1], 0, 4) != "bold" {
		t.Fatalf("second piece: %+v %q", got[1], parts[1])
	}
}
