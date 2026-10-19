package notify

import (
	"context"
	"encoding/json"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Schayan-nk/docfind-messenger/internal/webpush"
)

// memStore is the outbox in memory, with the same claim semantics as the
// PostgreSQL one: claiming pushes next_attempt forward by the lease.
type memStore struct {
	mu      sync.Mutex
	rows    map[int64]*memRow
	dead    []string
	dropped []int64
}

type memRow struct {
	d    Delivery
	next time.Time
}

func newMem() *memStore { return &memStore{rows: map[int64]*memRow{}} }

func (m *memStore) add(id int64, n Notification, s Subscription, due time.Time) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.rows[id] = &memRow{d: Delivery{ID: id, Notification: n, Subscription: s}, next: due}
}

func (m *memStore) Claim(_ context.Context, now time.Time, lease time.Duration, limit int) ([]Delivery, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	var out []Delivery
	for _, r := range m.rows {
		if len(out) == limit {
			break
		}
		if !r.next.After(now) {
			r.d.Attempts++
			r.next = now.Add(lease)
			out = append(out, r.d)
		}
	}
	return out, nil
}

func (m *memStore) Delivered(_ context.Context, d Delivery, _ int, _ time.Time) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	delete(m.rows, d.ID)
	return nil
}

func (m *memStore) Reschedule(_ context.Context, d Delivery, at time.Time, _ int, _ string, _ time.Time) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if r, ok := m.rows[d.ID]; ok {
		r.next = at
	}
	return nil
}

func (m *memStore) Kill(_ context.Context, d Delivery, _ int, reason string, _ time.Time) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	delete(m.rows, d.ID)
	m.dead = append(m.dead, reason)
	return nil
}

func (m *memStore) DropSubscription(_ context.Context, id int64, _ string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	for k, r := range m.rows {
		if r.d.Subscription.ID == id {
			delete(m.rows, k)
		}
	}
	m.dropped = append(m.dropped, id)
	return nil
}

func (m *memStore) pending() int {
	m.mu.Lock()
	defer m.mu.Unlock()
	return len(m.rows)
}

func (m *memStore) nextAt(id int64) time.Time {
	m.mu.Lock()
	defer m.mu.Unlock()
	if r, ok := m.rows[id]; ok {
		return r.next
	}
	return time.Time{}
}

// scripted is a push service that answers from a script, and records what it
// was sent.
type scripted struct {
	mu      sync.Mutex
	answers []webpush.Result
	sent    []webpush.Message
}

func (s *scripted) Send(_ context.Context, _ webpush.Subscription, msg webpush.Message) webpush.Result {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.sent = append(s.sent, msg)
	if len(s.answers) == 0 {
		return webpush.Result{Outcome: webpush.Delivered, Status: 201}
	}
	r := s.answers[0]
	s.answers = s.answers[1:]
	return r
}

type clock struct{ t time.Time }

func (c *clock) now() time.Time          { return c.t }
func (c *clock) advance(d time.Duration) { c.t = c.t.Add(d) }

func setup(answers ...webpush.Result) (*Dispatcher, *memStore, *scripted, *clock) {
	st := newMem()
	sp := &scripted{answers: answers}
	c := &clock{t: time.Date(2026, 9, 23, 9, 0, 0, 0, time.UTC)}
	d := &Dispatcher{Store: st, Sender: sp, now: c.now, Parallel: 1}
	return d, st, sp, c
}

func note(c *clock) Notification {
	return Notification{ID: 1, Username: "sadeghi", Kind: "case", Title: "کار جدید در کارتابل",
		Body: "پرونده #156151", URL: "/#pending", Tag: "case-156151", Urgency: "high",
		ExpiresAt: c.t.Add(24 * time.Hour)}
}

var phone = Subscription{ID: 7, Username: "sadeghi",
	Endpoint: "https://web.push.apple.com/abc", Origin: "https://docfind.example-office.ir"}

// ---------------------------------------------------------------- the contract

// Accepted: the row is DELETED -- the design as asked for.
func TestAnAcceptedDeliveryIsDeleted(t *testing.T) {
	d, st, sp, c := setup()
	st.add(1, note(c), phone, c.t)
	if n, err := d.Pass(context.Background()); err != nil || n != 1 {
		t.Fatalf("pass handled %d, err %v", n, err)
	}
	if st.pending() != 0 {
		t.Error("the row survived a 201 from the push service")
	}
	if len(sp.sent) != 1 {
		t.Fatalf("sent %d messages, want 1", len(sp.sent))
	}
	if sp.sent[0].Urgency != "high" || sp.sent[0].Topic != "case-156151" {
		t.Errorf("urgency/topic not carried: %+v", sp.sent[0])
	}
	if d.Stats().Delivered != 1 {
		t.Errorf("stats = %+v", d.Stats())
	}
}

// Not accepted: retried until it is, and only then deleted.
func TestAFailureIsRetriedUntilItIsAccepted(t *testing.T) {
	d, st, sp, c := setup(
		webpush.Result{Outcome: webpush.Retry, Status: 503},
		webpush.Result{Outcome: webpush.Retry, Reason: "network: timeout"},
		webpush.Result{Outcome: webpush.Delivered, Status: 201},
	)
	st.add(1, note(c), phone, c.t)
	for range 3 {
		d.Pass(context.Background())
		if st.pending() == 0 {
			break
		}
		// Not due again until its backoff has passed.
		if n, _ := d.Pass(context.Background()); n != 0 {
			t.Fatal("a rescheduled delivery was retried before its backoff elapsed")
		}
		c.t = st.nextAt(1)
	}
	if st.pending() != 0 {
		t.Error("still queued after the push service finally accepted it")
	}
	if len(sp.sent) != 3 {
		t.Errorf("made %d attempts, want 3", len(sp.sent))
	}
}

// A device that no longer exists is removed, not retried for ever.
func TestAVanishedDeviceIsRemovedWithItsQueue(t *testing.T) {
	d, st, _, c := setup(webpush.Result{Outcome: webpush.Gone, Status: 410})
	st.add(1, note(c), phone, c.t)
	n2 := note(c)
	n2.ID = 2
	st.add(2, n2, phone, c.t.Add(time.Hour)) // not yet due, same device
	d.Pass(context.Background())
	if len(st.dropped) != 1 || st.dropped[0] != phone.ID {
		t.Fatalf("dropped = %v, want the device", st.dropped)
	}
	if st.pending() != 0 {
		t.Error("messages queued for a vanished device were kept")
	}
}

// A message the push service rejects outright will be rejected again.
func TestARejectedMessageIsNotRetried(t *testing.T) {
	d, st, sp, c := setup(webpush.Result{Outcome: webpush.Rejected, Status: 413, Reason: "HTTP 413"})
	st.add(1, note(c), phone, c.t)
	d.Pass(context.Background())
	c.advance(time.Hour)
	d.Pass(context.Background())
	if len(sp.sent) != 1 || len(st.dead) != 1 || st.pending() != 0 {
		t.Errorf("sent %d, dead %v, pending %d", len(sp.sent), st.dead, st.pending())
	}
}

// Past its expiry it is abandoned WITHOUT being sent: a reminder for a
// meeting that has finished is noise.
func TestAnExpiredNotificationIsAbandonedNotSent(t *testing.T) {
	d, st, sp, c := setup()
	n := note(c)
	n.ExpiresAt = c.t.Add(-time.Minute)
	st.add(1, n, phone, c.t)
	d.Pass(context.Background())
	if len(sp.sent) != 0 {
		t.Error("an expired notification was sent")
	}
	if len(st.dead) != 1 || !strings.Contains(st.dead[0], "expired") {
		t.Errorf("dead = %v", st.dead)
	}
}

// Our own identity refused: slow retry, never dropped. Fixing the key must not
// have lost everything queued meanwhile.
func TestARefusedIdentityIsRetriedSlowlyNotDropped(t *testing.T) {
	d, st, _, c := setup(webpush.Result{Outcome: webpush.Unauthorized, Status: 403, Reason: `BadJwtToken`})
	st.add(1, note(c), phone, c.t)
	d.Pass(context.Background())
	if st.pending() != 1 {
		t.Fatal("a 403 dropped the message")
	}
	if wait := st.nextAt(1).Sub(c.t); wait < 25*time.Minute {
		t.Errorf("retry after %v; a configuration fault should back off to ~30 min", wait)
	}
}

// A crash mid-send loses nothing: the claim's lease runs out and the row is
// claimed again.
func TestAClaimThatIsNeverFinishedComesBack(t *testing.T) {
	d, st, _, c := setup()
	st.add(1, note(c), phone, c.t)
	d.init()
	claimed, _ := st.Claim(context.Background(), c.t, d.Lease, 10)
	if len(claimed) != 1 {
		t.Fatal("nothing claimed")
	}
	// ...and the process dies here, without Delivered or Reschedule.
	if again, _ := st.Claim(context.Background(), c.t.Add(time.Second), d.Lease, 10); len(again) != 0 {
		t.Error("a row was handed out twice while its lease was live")
	}
	c.t = c.t.Add(d.Lease + time.Second)
	if n, _ := d.Pass(context.Background()); n != 1 || st.pending() != 0 {
		t.Errorf("after the lease expired the row was not re-delivered (n=%d pending=%d)", n, st.pending())
	}
}

func TestBackoffDoublesToACeilingAndHonoursRetryAfter(t *testing.T) {
	for _, c := range []struct {
		attempts int
		lo, hi   time.Duration
	}{
		{1, 8 * time.Second, 12 * time.Second},
		{2, 16 * time.Second, 24 * time.Second},
		{4, 64 * time.Second, 96 * time.Second},
		{30, 24 * time.Minute, 36 * time.Minute},
	} {
		for range 20 {
			if got := Backoff(c.attempts, 0); got < c.lo || got > c.hi {
				t.Errorf("Backoff(%d) = %v, want within [%v, %v]", c.attempts, got, c.lo, c.hi)
			}
		}
	}
	if got := Backoff(1, 10*time.Minute); got != 10*time.Minute {
		t.Errorf("a Retry-After of 10m gave %v", got)
	}
}

func TestMaxAttemptsIsAHardCap(t *testing.T) {
	d, st, _, c := setup()
	d.MaxAttempts = 2
	st.add(1, note(c), phone, c.t)
	st.rows[1].d.Attempts = 2 // claimed as the third
	d.Pass(context.Background())
	if len(st.dead) != 1 {
		t.Error("the attempt cap did not stop the retries")
	}
}

// ---------------------------------------------------------------- payload

func TestThePayloadIsDeclarativeWebPushWithAnAbsoluteTarget(t *testing.T) {
	c := &clock{t: time.Now()}
	b, err := Payload(note(c), "https://docfind.example-office.ir")
	if err != nil {
		t.Fatal(err)
	}
	var p struct {
		WebPush      int `json:"web_push"`
		Notification struct {
			Title    string `json:"title"`
			Body     string `json:"body"`
			Navigate string `json:"navigate"`
			Tag      string `json:"tag"`
			Lang     string `json:"lang"`
			Dir      string `json:"dir"`
			Data     struct {
				URL string `json:"url"`
			} `json:"data"`
		} `json:"notification"`
	}
	if err := json.Unmarshal(b, &p); err != nil {
		t.Fatal(err)
	}
	if p.WebPush != 8030 {
		t.Errorf("web_push = %d; Safari only renders declaratively with 8030", p.WebPush)
	}
	if p.Notification.Title == "" {
		t.Error("the title is empty; Safari requires one")
	}
	if p.Notification.Navigate != "https://docfind.example-office.ir/#pending" {
		t.Errorf("navigate = %q, want an absolute URL on the device's own origin", p.Notification.Navigate)
	}
	if p.Notification.Data.URL != p.Notification.Navigate || p.Notification.Tag != "case-156151" ||
		p.Notification.Lang != "fa" || p.Notification.Dir != "rtl" {
		t.Errorf("notification = %+v", p.Notification)
	}
	if len(b) > webpush.MaxPayload {
		t.Errorf("payload is %d bytes, over the push limit", len(b))
	}
}

// Quiet hours mark a notification quiet, which the device shows without
// sound; its urgency is left alone.
func TestAQuietNotificationIsSilent(t *testing.T) {
	silent := func(quiet bool) bool {
		n := note(&clock{t: time.Now()})
		n.Quiet = quiet
		b, err := Payload(n, "https://docfind.example-office.ir")
		if err != nil {
			t.Fatal(err)
		}
		var p struct {
			Notification struct {
				Silent bool `json:"silent"`
			} `json:"notification"`
		}
		if err := json.Unmarshal(b, &p); err != nil {
			t.Fatal(err)
		}
		return p.Notification.Silent
	}
	if !silent(true) || silent(false) {
		t.Error("only a quiet notification should be silent")
	}
}

// A notification can only ever open this application.
func TestANotificationCannotSendTheDeviceElsewhere(t *testing.T) {
	origin := "https://docfind.example-office.ir"
	for _, path := range []string{"https://evil.example/x", "//evil.example/x", "javascript:alert(1)"} {
		got := resolve(origin, path)
		if !strings.HasPrefix(got, origin+"/") {
			t.Errorf("resolve(%q) = %q, left the application", path, got)
		}
	}
	// Without a usable origin: a path, never an absolute foreign URL.
	if got := resolve("", "//evil.example/x"); got != "/" {
		t.Errorf("with no origin, a protocol-relative URL became %q", got)
	}
	if got := resolve("", "/#pending"); got != "/#pending" {
		t.Errorf("with no origin, /#pending became %q", got)
	}
}

func TestOnlySecureOriginsAreAccepted(t *testing.T) {
	for _, ok := range []string{"https://docfind.example.ir", "https://10.0.0.1:8443", "http://localhost:8300"} {
		if !ValidOrigin(ok) {
			t.Errorf("%q refused", ok)
		}
	}
	for _, bad := range []string{"http://192.168.77.13:8300", "ftp://x", "https://x/path", "https://u:p@x", "", "https://"} {
		if ValidOrigin(bad) {
			t.Errorf("%q accepted", bad)
		}
	}
}
