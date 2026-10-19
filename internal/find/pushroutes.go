package find

import (
	"context"
	"crypto/tls"
	"encoding/json"
	"net"
	"net/http"
	"net/url"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/Schayan-nk/docfind-messenger/internal/auth"
	"github.com/Schayan-nk/docfind-messenger/internal/notify"
	"github.com/Schayan-nk/docfind-messenger/internal/pgstore"
)

// Notifications on a person's own devices, delivered while docfind is closed.
//
// The server side of internal/notify: a person registers a phone or a browser,
// sees the ones they have registered, removes one, and sends themselves a test.
// Everything is scoped to the signed-in account -- there is no username in any
// of these routes and no parameter that could name somebody else's device.

// pushStore is what the routes and the watchers need from storage.
// *pgstore.Notifications implements it; the tests use a map.
type pushStore interface {
	Subscribe(ctx context.Context, s notify.Subscription) (notify.Subscription, error)
	Devices(ctx context.Context, username string) ([]notify.Subscription, error)
	Unsubscribe(ctx context.Context, username string, id int64) (bool, error)
	UnsubscribeEndpoint(ctx context.Context, username, endpoint string) (bool, error)
	Enqueue(ctx context.Context, n notify.Notification) (int64, bool, int, error)
	Recent(ctx context.Context, username string, limit int) ([]notify.Notification, error)
	SubscribedUsers(ctx context.Context) ([]string, error)
	MarkSeen(ctx context.Context, username string, keys []string) ([]string, error)
	Primed(ctx context.Context, username, source string) (bool, error)
	Stats(ctx context.Context, now time.Time) (pgstore.OutboxStats, error)
}

// pushFeature is everything the notification routes hold.
type pushFeature struct {
	store      pushStore
	dispatcher *notify.Dispatcher
	publicKey  string
	subject    string
	// allowed vets a subscription's endpoint against the push-service
	// allowlist (webpush.Sender.AllowedEndpoint).
	allowed func(endpoint string) error
	// validKeys vets its keys (webpush.ValidateKeys).
	validKeys func(p256dh, auth string) error
}

// UseNotifications switches device notifications on.
func (s *Server) UseNotifications(store pushStore, d *notify.Dispatcher, publicKey, subject string,
	allowed func(string) error, validKeys func(string, string) error) {
	s.push = &pushFeature{store: store, dispatcher: d, publicKey: publicKey, subject: subject,
		allowed: allowed, validKeys: validKeys}
}

// NotificationsEnabled reports whether device notifications are configured.
func (s *Server) NotificationsEnabled() bool { return s.push != nil && s.push.store != nil }

func (s *Server) pushOff(w http.ResponseWriter) bool {
	if s.NotificationsEnabled() {
		return false
	}
	writeJSONStatus(w, http.StatusNotFound, map[string]any{
		"error": "اعلان روی دستگاه روی این سرور راه‌اندازی نشده است.",
	})
	return true
}

// handlePushConfig tells the page whether it can subscribe, and with what key.
func (s *Server) handlePushConfig(w http.ResponseWriter, r *http.Request) {
	if !s.NotificationsEnabled() {
		writeJSON(w, map[string]any{"available": false})
		return
	}
	writeJSON(w, map[string]any{"available": true, "public_key": s.push.publicKey})
}

type deviceView struct {
	ID          int64     `json:"id"`
	Label       string    `json:"label"`
	Service     string    `json:"service"`
	Browser     string    `json:"browser,omitempty"`
	CreatedAt   time.Time `json:"created_at"`
	LastSuccess time.Time `json:"last_success,omitzero"`
	LastFailure time.Time `json:"last_failure,omitzero"`
	LastStatus  int       `json:"last_status,omitempty"`
	Failures    int       `json:"failures"`
	// Current marks the device the request came from, when the page said
	// which endpoint it holds. Lets "this device" be shown without ever
	// sending an endpoint back to a page.
	Current bool `json:"current,omitempty"`
}

// handlePushDevices lists the caller's own devices.
//
// The endpoint itself is never returned. It is the device's address at its
// push service, and while it is useless without this server's private key, a
// value that only the server needs is a value the page does not get.
func (s *Server) handlePushDevices(w http.ResponseWriter, r *http.Request) {
	if s.pushOff(w) {
		return
	}
	u := auth.UserFrom(r)
	list, err := s.push.store.Devices(r.Context(), u.Username)
	if err != nil {
		logReportError("push devices", err)
		writeJSONError(w, http.StatusBadGateway, `{"error":"فهرست دستگاه‌ها خوانده نشد."}`)
		return
	}
	mine := strings.TrimSpace(r.URL.Query().Get("endpoint"))
	out := make([]deviceView, 0, len(list))
	for _, d := range list {
		out = append(out, deviceView{
			ID: d.ID, Label: d.Label, Service: pushServiceName(d.Endpoint),
			Browser: browserName(d.UserAgent), CreatedAt: d.CreatedAt,
			LastSuccess: realTime(d.LastSuccessAt), LastFailure: realTime(d.LastFailureAt),
			LastStatus: d.LastStatus, Failures: d.Failures,
			Current: mine != "" && mine == d.Endpoint,
		})
	}
	writeJSON(w, map[string]any{"devices": out})
}

// realTime turns the database's "never" placeholder back into the zero time.
func realTime(t time.Time) time.Time {
	if t.Year() <= 1970 {
		return time.Time{}
	}
	return t
}

// handlePushSubscribe registers the device the request came from.
func (s *Server) handlePushSubscribe(w http.ResponseWriter, r *http.Request) {
	if s.pushOff(w) {
		return
	}
	u := auth.UserFrom(r)
	var req struct {
		Endpoint string `json:"endpoint"`
		Keys     struct {
			P256dh string `json:"p256dh"`
			Auth   string `json:"auth"`
		} `json:"keys"`
		Label string `json:"label"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 4<<10)).Decode(&req); err != nil {
		writeJSONError(w, http.StatusBadRequest, `{"error":"درخواست نامعتبر است."}`)
		return
	}
	req.Endpoint = strings.TrimSpace(req.Endpoint)
	if len(req.Endpoint) > 1024 {
		writeJSONError(w, http.StatusBadRequest, `{"error":"نشانی دستگاه نامعتبر است."}`)
		return
	}
	// The endpoint is the address the SERVER will send to, and it came from
	// the browser. Unchecked, anyone signed in could make the server POST to
	// an internal address on its own authority.
	if err := s.push.allowed(req.Endpoint); err != nil {
		writeJSONError(w, http.StatusBadRequest, `{"error":"این نشانی متعلق به هیچ سرویس اعلان شناخته‌شده‌ای نیست."}`)
		return
	}
	if err := s.push.validKeys(req.Keys.P256dh, req.Keys.Auth); err != nil {
		writeJSONError(w, http.StatusBadRequest, `{"error":"کلیدهای دستگاه نامعتبرند."}`)
		return
	}
	// Where pressing a notification should take this device: the origin the
	// request came from. Taken from the Origin header, which the mutating
	// check has already matched against this server, rather than from the
	// body, which the caller writes.
	origin := requestOrigin(r)
	if !notify.ValidOrigin(origin) {
		// Not refused: a subscription only exists in a secure context, so
		// this is a proxy that dropped the header. The service worker
		// resolves a relative address against its own origin instead.
		origin = ""
	}
	label := strings.TrimSpace(req.Label)
	if len([]rune(label)) > 60 {
		label = string([]rune(label)[:60])
	}
	ua := r.UserAgent()
	if len(ua) > 300 {
		ua = ua[:300]
	}
	sub, err := s.push.store.Subscribe(r.Context(), notify.Subscription{
		Username: u.Username, Endpoint: req.Endpoint, P256dh: req.Keys.P256dh, Auth: req.Keys.Auth,
		Origin: origin, Label: label, UserAgent: ua,
	})
	if err != nil {
		logReportError("push subscribe", err)
		writeJSONError(w, http.StatusBadGateway, `{"error":"دستگاه ثبت نشد."}`)
		return
	}
	writeJSONStatus(w, http.StatusCreated, map[string]any{"id": sub.ID})
}

// requestOrigin is the scheme and host the request was made to, from its
// Origin header.
func requestOrigin(r *http.Request) string {
	o := strings.TrimSpace(r.Header.Get("Origin"))
	if o == "" || o == "null" {
		return ""
	}
	u, err := url.Parse(o)
	if err != nil || u.Host == "" {
		return ""
	}
	return u.Scheme + "://" + u.Host
}

// handlePushUnsubscribeID removes one of the caller's devices by ID.
func (s *Server) handlePushUnsubscribeID(w http.ResponseWriter, r *http.Request) {
	if s.pushOff(w) {
		return
	}
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil || id <= 0 {
		writeJSONError(w, http.StatusBadRequest, `{"error":"شناسه نامعتبر است."}`)
		return
	}
	ok, err := s.push.store.Unsubscribe(r.Context(), auth.UserFrom(r).Username, id)
	if err != nil {
		logReportError("push unsubscribe", err)
		writeJSONError(w, http.StatusBadGateway, `{"error":"حذف انجام نشد."}`)
		return
	}
	if !ok {
		// Somebody else's device and a device that does not exist get the
		// same answer, so this cannot be used to discover other people's IDs.
		writeJSONError(w, http.StatusNotFound, `{"error":"این دستگاه پیدا نشد."}`)
		return
	}
	writeJSON(w, map[string]any{"deleted": true})
}

// handlePushUnsubscribeEndpoint removes the device the page is running on.
// Called when notifications are switched off on it, and at sign-out -- a phone
// handed to somebody else should not go on receiving its old owner's cases.
func (s *Server) handlePushUnsubscribeEndpoint(w http.ResponseWriter, r *http.Request) {
	if s.pushOff(w) {
		return
	}
	var req struct {
		Endpoint string `json:"endpoint"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 2<<10)).Decode(&req); err != nil ||
		strings.TrimSpace(req.Endpoint) == "" {
		writeJSONError(w, http.StatusBadRequest, `{"error":"درخواست نامعتبر است."}`)
		return
	}
	ok, err := s.push.store.UnsubscribeEndpoint(r.Context(), auth.UserFrom(r).Username, strings.TrimSpace(req.Endpoint))
	if err != nil {
		logReportError("push unsubscribe endpoint", err)
		writeJSONError(w, http.StatusBadGateway, `{"error":"حذف انجام نشد."}`)
		return
	}
	writeJSON(w, map[string]any{"deleted": ok})
}

// handlePushTest sends the caller a notification on every device they have.
//
// The test that matters is the one with the application CLOSED, so the page
// says so: press, close the app, and see whether it arrives. A test sent while
// the page is open proves much less.
func (s *Server) handlePushTest(w http.ResponseWriter, r *http.Request) {
	if s.pushOff(w) {
		return
	}
	u := auth.UserFrom(r)
	// Refused before anything is recorded: a test with nowhere to go would
	// otherwise sit in the person's history as a notification they "received".
	if devs, err := s.push.store.Devices(r.Context(), u.Username); err == nil && len(devs) == 0 {
		writeJSONStatus(w, http.StatusConflict, map[string]any{
			"error": "هیچ دستگاهی برای شما ثبت نشده است. اول روی همین دستگاه «فعال‌سازی» را بزنید.",
		})
		return
	}
	now := time.Now()
	_, _, queued, err := s.push.store.Enqueue(r.Context(), notify.Notification{
		Username:  u.Username,
		Kind:      "test",
		DedupeKey: "test:" + strconv.FormatInt(now.UnixNano(), 10),
		Title:     "اعلان آزمایشی پیام‌رسان",
		Body:      "اگر این را می‌بینید، اعلان‌ها روی این دستگاه کار می‌کنند، حتی وقتی برنامه بسته است.",
		URL:       "/settings#notifications",
		Tag:       "docfind-test",
		Urgency:   "high",
		ExpiresAt: now.Add(time.Hour),
	})
	if err != nil {
		logReportError("push test", err)
		writeJSONError(w, http.StatusBadGateway, `{"error":"اعلان آزمایشی ساخته نشد."}`)
		return
	}
	if queued == 0 {
		writeJSONStatus(w, http.StatusConflict, map[string]any{
			"error": "هیچ دستگاهی برای شما ثبت نشده است. اول روی همین دستگاه «فعال‌سازی» را بزنید.",
		})
		return
	}
	s.push.dispatcher.Wake()
	writeJSON(w, map[string]any{"queued": queued})
}

// handlePushRecent is the caller's own recent notifications, for the list in
// the application.
func (s *Server) handlePushRecent(w http.ResponseWriter, r *http.Request) {
	if s.pushOff(w) {
		return
	}
	list, err := s.push.store.Recent(r.Context(), auth.UserFrom(r).Username, 50)
	if err != nil {
		logReportError("push recent", err)
		writeJSONError(w, http.StatusBadGateway, `{"error":"اعلان‌ها خوانده نشد."}`)
		return
	}
	type item struct {
		ID        int64     `json:"id"`
		Kind      string    `json:"kind"`
		Title     string    `json:"title"`
		Body      string    `json:"body"`
		URL       string    `json:"url"`
		Tag       string    `json:"tag,omitempty"`
		Priority  string    `json:"priority"`
		Quiet     bool      `json:"quiet,omitempty"`
		CreatedAt time.Time `json:"created_at"`
	}
	out := make([]item, 0, len(list))
	for _, n := range list {
		out = append(out, item{n.ID, n.Kind, n.Title, n.Body, n.URL, n.Tag, notifyPriority(n), n.Quiet, n.CreatedAt})
	}
	writeJSON(w, map[string]any{"notifications": out})
}

// notifyPriority is where a notification sits in the notification centre:
// "important" for a case waiting in the cartable and for a change to a
// meeting within a day (what the watcher sends as high urgency), "info" for
// the rest. "critical" is not stored: it is a service that is down, which the
// centre reads live for an administrator.
func notifyPriority(n notify.Notification) string {
	if n.Kind == "case" || n.Kind == "reminder" || n.Urgency == "high" {
		return "important"
	}
	return "info"
}

// handlePushStatus is the sender's health, for an administrator.
func (s *Server) handlePushStatus(w http.ResponseWriter, r *http.Request) {
	if !s.NotificationsEnabled() {
		writeJSON(w, map[string]any{"enabled": false})
		return
	}
	st, err := s.push.store.Stats(r.Context(), time.Now())
	out := map[string]any{
		"enabled":  true,
		"subject":  s.push.subject,
		"sender":   s.push.dispatcher.Stats(),
		"queue":    st,
		"watchers": s.pushWatch.snapshot(),
	}
	if err != nil {
		out["queue_error"] = "صف اعلان خوانده نشد"
		logReportError("push status", err)
	}
	// Can this server reach each push service at all? From this office the
	// answer is not guaranteed -- some platforms' services may be filtered --
	// and it is the first question when one platform stops receiving.
	//
	// Only when asked (?probe=1): it opens connections to outside hosts and
	// takes seconds, which a status read should not do on its own.
	if r.URL.Query().Get("probe") == "1" {
		hosts := []string{"web.push.apple.com", "fcm.googleapis.com"}
		for h := range st.ByService {
			if h != "" && h != "?" && !slices.Contains(hosts, h) {
				hosts = append(hosts, h)
			}
		}
		out["services"] = probePushServices(r.Context(), hosts)
	}
	writeJSON(w, out)
}

// serviceProbe is one push service's reachability from this server.
type serviceProbe struct {
	Host      string  `json:"host"`
	Reachable bool    `json:"reachable"`
	Ms        float64 `json:"ms,omitempty"`
	Error     string  `json:"error,omitempty"`
}

// probePushServices opens a TLS connection to each host, concurrently, with a
// short deadline. A handshake, not a request: it proves the route and the
// certificate without sending anything a push service would count.
func probePushServices(ctx context.Context, hosts []string) []serviceProbe {
	out := make([]serviceProbe, len(hosts))
	var wg sync.WaitGroup
	for i, h := range hosts {
		wg.Go(func() {
			start := time.Now()
			d := &tls.Dialer{NetDialer: &net.Dialer{Timeout: 5 * time.Second},
				Config: &tls.Config{ServerName: h, MinVersion: tls.VersionTLS12}}
			cctx, cancel := context.WithTimeout(ctx, 6*time.Second)
			defer cancel()
			conn, err := d.DialContext(cctx, "tcp", net.JoinHostPort(h, "443"))
			p := serviceProbe{Host: h}
			if err != nil {
				p.Error = err.Error()
			} else {
				conn.Close()
				p.Reachable = true
				p.Ms = float64(time.Since(start).Microseconds()) / 1000
			}
			out[i] = p
		})
	}
	wg.Wait()
	return out
}

// pushServiceName says, in a word, which platform delivers to a device.
func pushServiceName(endpoint string) string {
	u, err := url.Parse(endpoint)
	if err != nil {
		return ""
	}
	h := strings.ToLower(u.Hostname())
	switch {
	case strings.HasSuffix(h, "push.apple.com"):
		return "Apple"
	case strings.HasSuffix(h, "notify.windows.com"):
		return "Microsoft"
	case strings.HasSuffix(h, "googleapis.com"):
		return "Google"
	case strings.HasSuffix(h, "mozilla.com"):
		return "Mozilla"
	}
	return h
}

// browserName is a short, human name for a User-Agent. Only for telling one's
// own devices apart on the list.
func browserName(ua string) string {
	switch {
	case strings.Contains(ua, "iPhone"):
		return "iPhone"
	case strings.Contains(ua, "iPad"):
		return "iPad"
	case strings.Contains(ua, "Edg/"):
		return "Edge"
	case strings.Contains(ua, "Firefox/"):
		return "Firefox"
	case strings.Contains(ua, "Android"):
		return "Android"
	case strings.Contains(ua, "Chrome/"):
		return "Chrome"
	case strings.Contains(ua, "Safari/"):
		return "Safari"
	}
	return ""
}
