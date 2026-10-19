package find

import (
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync"
	"testing"
)

// fakeAsanak answers sendsms and getcredit, and keeps every message.
type fakeAsanak struct {
	mu     sync.Mutex
	sent   []map[string]string
	credit int
}

func (f *fakeAsanak) serve(t *testing.T) *httptest.Server {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = r.ParseForm()
		f.mu.Lock()
		defer f.mu.Unlock()
		if strings.HasSuffix(r.URL.Path, "/getcredit") {
			w.Write([]byte(`{"meta":{"status":200,"message":"success"},"data":{"credit":` + strconv.Itoa(f.credit) + `}}`))
			return
		}
		m := map[string]string{}
		for k := range r.PostForm {
			m[k] = r.PostForm.Get(k)
		}
		f.sent = append(f.sent, m)
		w.Write([]byte(`{"meta":{"status":200,"message":"success"},"data":[1]}`))
	}))
	t.Cleanup(srv.Close)
	return srv
}

func (f *fakeAsanak) last(t *testing.T) (to, code string) {
	t.Helper()
	f.mu.Lock()
	defer f.mu.Unlock()
	if len(f.sent) == 0 {
		t.Fatal("nothing was texted")
	}
	m := f.sent[len(f.sent)-1]
	return m["destination"], sixDigits.FindString(m["message"])
}
