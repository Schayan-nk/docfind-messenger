package chat

import "testing"

func TestCleanEntitiesRefusesDangerousLinksAndBadSpans(t *testing.T) {
	text := "سلام دنیا 😀 https://x.ir"
	n := UTF16Len(text)
	for _, tc := range []struct {
		name string
		in   []Entity
		ok   bool
	}{
		{"bold", []Entity{{Type: "bold", Offset: 0, Length: 4}}, true},
		{"https link", []Entity{{Type: "text_link", Offset: 0, Length: 4, URL: "https://chat.example.ir/"}}, true},
		{"mailto", []Entity{{Type: "text_link", Offset: 0, Length: 4, URL: "mailto:a@b.ir"}}, true},
		{"javascript link", []Entity{{Type: "text_link", Offset: 0, Length: 4, URL: "javascript:alert(1)"}}, false},
		{"JaVaScRiPt link", []Entity{{Type: "text_link", Offset: 0, Length: 4, URL: " JaVaScRiPt:alert(1)"}}, false},
		{"data link", []Entity{{Type: "text_link", Offset: 0, Length: 4, URL: "data:text/html,<script>alert(1)</script>"}}, false},
		{"http without host", []Entity{{Type: "text_link", Offset: 0, Length: 4, URL: "http:/evil"}}, false},
		{"unknown type", []Entity{{Type: "script", Offset: 0, Length: 4}}, false},
		{"past the end", []Entity{{Type: "bold", Offset: n - 1, Length: 2}}, false},
		{"negative", []Entity{{Type: "bold", Offset: -1, Length: 2}}, false},
		{"zero length", []Entity{{Type: "bold", Offset: 0, Length: 0}}, false},
		{"pre with an attribute in the language", []Entity{{Type: "pre", Offset: 0, Length: 4, Lang: `go" onmouseover="x`}}, false},
	} {
		_, err := CleanEntities(text, tc.in)
		if (err == nil) != tc.ok {
			t.Errorf("%s: err=%v, want ok=%v", tc.name, err, tc.ok)
		}
	}
	many := make([]Entity, 101)
	for i := range many {
		many[i] = Entity{Type: "bold", Offset: 0, Length: 1}
	}
	if _, err := CleanEntities(text, many); err == nil {
		t.Error("101 entities accepted")
	}
}

func TestCleanEntitiesDropsFieldsThatDoNotBelong(t *testing.T) {
	out, err := CleanEntities("abcd", []Entity{{Type: "bold", Offset: 0, Length: 2, URL: "https://x.ir", User: "a", Lang: "go"}})
	if err != nil || out[0].URL != "" || out[0].User != "" || out[0].Lang != "" {
		t.Fatalf("%+v %v", out, err)
	}
}

func TestMentionsAreReadInUTF16(t *testing.T) {
	text := "😀 سلام @Shayan_K و @shayan_k"
	ents := []Entity{
		{Type: "mention", Offset: UTF16Len("😀 سلام "), Length: UTF16Len("@Shayan_K")},
		{Type: "mention", Offset: UTF16Len("😀 سلام @Shayan_K و "), Length: UTF16Len("@shayan_k")},
	}
	got := MentionedHandles(text, ents)
	if len(got) != 1 || got[0] != "shayan_k" {
		t.Fatalf("got %v", got)
	}
}
