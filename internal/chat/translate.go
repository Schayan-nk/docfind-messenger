package chat

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/url"
	"os"
	"strconv"
	"strings"
	"sync"
	"time"
)

// Translation (SPEC 6.3): a "Translate" button between Persian, English and
// German, answered by the self-hosted model on the server (Ollama). It is
// configured only from the environment:
//   DOCFIND_CHAT_TRANSLATE_URL    the Ollama address, e.g. http://127.0.0.1:11434
//   DOCFIND_CHAT_TRANSLATE_MODEL  the model name, e.g. qwen2.5:7b
// With either unset, or Ollama not answering, the page hides the button. A
// message's text goes only to that address and is never logged.

var langNames = map[string]string{"fa": "Persian (Farsi)", "en": "English", "de": "German"}

type translator struct {
	base, model string
	client      *http.Client

	mu      sync.Mutex
	checked time.Time
	up      bool
	cache   map[string]string
	order   []string
}

var (
	trOnce  sync.Once
	trCur   *translator
	trLimit = newLimiter(0.2, 6) // per person: six at once, then one every five seconds
)

// translatorFromEnv reads the configuration once; nil means no translation.
func translatorFromEnv() *translator {
	trOnce.Do(func() {
		base := strings.TrimRight(strings.TrimSpace(os.Getenv("DOCFIND_CHAT_TRANSLATE_URL")), "/")
		model := strings.TrimSpace(os.Getenv("DOCFIND_CHAT_TRANSLATE_MODEL"))
		if u, err := url.Parse(base); err == nil && (u.Scheme == "http" || u.Scheme == "https") && u.Host != "" && model != "" {
			trCur = newTranslator(base, model)
		}
	})
	return trCur
}

func newTranslator(base, model string) *translator {
	return &translator{base: base, model: model, client: &http.Client{Timeout: 90 * time.Second}, cache: map[string]string{}}
}

// available asks Ollama, at most once a minute, whether the model is there.
func (t *translator) available(ctx context.Context) bool {
	if t == nil {
		return false
	}
	t.mu.Lock()
	if time.Since(t.checked) < time.Minute {
		up := t.up
		t.mu.Unlock()
		return up
	}
	t.mu.Unlock()
	cctx, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()
	up := false
	if req, err := http.NewRequestWithContext(cctx, http.MethodGet, t.base+"/api/tags", nil); err == nil {
		if resp, err := t.client.Do(req); err == nil {
			var tags struct {
				Models []struct {
					Name string `json:"name"`
				} `json:"models"`
			}
			if resp.StatusCode == http.StatusOK && json.NewDecoder(http.MaxBytesReader(nil, resp.Body, 1<<20)).Decode(&tags) == nil {
				for _, m := range tags.Models {
					up = up || m.Name == t.model || m.Name == t.model+":latest"
				}
			}
			resp.Body.Close()
		}
	}
	t.mu.Lock()
	t.checked, t.up = time.Now(), up
	t.mu.Unlock()
	return up
}

var errNoTranslation = errors.New("chat: translation failed")

// translate asks the model for text in language to.
func (t *translator) translate(ctx context.Context, key, text, to string) (string, error) {
	t.mu.Lock()
	if v, ok := t.cache[key]; ok {
		t.mu.Unlock()
		return v, nil
	}
	t.mu.Unlock()
	body, _ := json.Marshal(map[string]any{
		"model": t.model, "stream": false, "options": map[string]any{"temperature": 0},
		"messages": []map[string]string{
			{"role": "system", "content": "You translate chat messages. Translate the user's message into " + langNames[to] +
				". Answer with the translation only: no notes, no quotes, keep line breaks, names, numbers and links as they are."},
			{"role": "user", "content": text},
		},
	})
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, t.base+"/api/chat", bytes.NewReader(body))
	if err != nil {
		return "", errNoTranslation
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := t.client.Do(req)
	if err != nil {
		return "", errNoTranslation
	}
	defer resp.Body.Close()
	var out struct {
		Message struct {
			Content string `json:"content"`
		} `json:"message"`
	}
	if resp.StatusCode != http.StatusOK || json.NewDecoder(http.MaxBytesReader(nil, resp.Body, 1<<20)).Decode(&out) != nil {
		return "", errNoTranslation
	}
	res := strings.TrimSpace(CleanText(out.Message.Content))
	if res == "" {
		return "", errNoTranslation
	}
	if r := []rune(res); len(r) > 3*MaxText {
		res = string(r[:3*MaxText])
	}
	t.mu.Lock()
	if len(t.order) >= 500 {
		delete(t.cache, t.order[0])
		t.order = t.order[1:]
	}
	t.cache[key], t.order = res, append(t.order, key)
	t.mu.Unlock()
	return res, nil
}

// TranslateInfo is GET /api/chat/translate: whether the button shows.
func (a *API) TranslateInfo(w http.ResponseWriter, r *http.Request) {
	ok := translatorFromEnv().available(r.Context())
	writeJSON(w, http.StatusOK, map[string]any{"available": ok, "langs": []string{"fa", "en", "de"}})
}

// Translate is GET /api/chat/chats/{id}/messages/{msg}/translate?to=fa|en|de.
func (a *API) Translate(w http.ResponseWriter, r *http.Request) {
	chatID, me, ok := a.memberChat(w, r)
	if !ok {
		return
	}
	to := r.URL.Query().Get("to")
	msgID, err := strconv.ParseInt(r.PathValue("msg"), 10, 64)
	tr := translatorFromEnv()
	if err != nil || langNames[to] == "" || !tr.available(r.Context()) {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "ترجمه روی این سرور در دسترس نیست."})
		return
	}
	m, err := a.Svc.messageIn(r.Context(), chatID, me, msgID)
	if err != nil {
		writeErr(w, err)
		return
	}
	if strings.TrimSpace(m.Text) == "" {
		writeErr(w, ErrEmpty)
		return
	}
	if !trLimit.allow(me) {
		writeErr(w, ErrRate)
		return
	}
	edited := int64(0)
	if m.Edited != nil {
		edited = m.Edited.Unix()
	}
	key := strconv.FormatInt(m.ID, 10) + "|" + strconv.FormatInt(edited, 10) + "|" + to
	text, err := tr.translate(r.Context(), key, m.Text, to)
	if err != nil {
		writeJSON(w, http.StatusBadGateway, map[string]string{"error": "ترجمه انجام نشد؛ دوباره تلاش کنید."})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"text": text, "to": to})
}
