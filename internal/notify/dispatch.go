package notify

import (
	"context"
	"maps"
	"math/rand/v2"
	"net/url"
	"sync"
	"sync/atomic"
	"time"

	"github.com/Schayan-nk/docfind-messenger/internal/obs"
	"github.com/Schayan-nk/docfind-messenger/internal/webpush"
)

// Sender is the push transport. *webpush.Sender implements it; tests supply
// their own.
type Sender interface {
	Send(ctx context.Context, sub webpush.Subscription, msg webpush.Message) webpush.Result
}

// Dispatcher claims due deliveries and sends them.
//
// ONE RETRY POLICY, stated once:
//
//	accepted (2xx)         delete the row                -- done
//	device gone (404/410)  delete the device and its rows -- nothing to retry
//	message refused (400,  dead letter                    -- resending the same
//	  413, bad keys)                                          bytes fails the same
//	past its expiry        dead letter                    -- too late to matter
//	our identity refused   retry, slowly (30 min)         -- a configuration
//	  (401/403)                                               fault; fixing it
//	                                                          should not lose mail
//	anything else          retry with backoff             -- network, 429, 5xx
//
// Backoff doubles from 10 s to a 30-minute ceiling with jitter, and never
// sooner than a Retry-After the push service asked for.
type Dispatcher struct {
	Store  Store
	Sender Sender

	// Log receives one line per attempt outcome worth recording. Nil discards.
	Log func(level, msg string, fields map[string]any)

	// Tunables, with defaults applied in Run.
	Batch       int           // deliveries claimed per pass; default 50
	Lease       time.Duration // how long a claim holds; default 2 min
	Poll        time.Duration // idle wait between passes; default 5 s
	Parallel    int           // concurrent sends; default 4
	SendTimeout time.Duration // per HTTP send; default 20 s
	MaxAttempts int           // hard cap regardless of expiry; default 60

	now  func() time.Time
	wake chan struct{}

	// Counters for the Security Center.
	delivered, retried, killed, dropped atomic.Int64
	lastPass                            atomic.Int64 // unix nanos
	lastErr                             atomic.Value // string
	once                                sync.Once
}

func (d *Dispatcher) init() {
	d.once.Do(func() {
		if d.Batch <= 0 {
			d.Batch = 50
		}
		if d.Lease <= 0 {
			d.Lease = 2 * time.Minute
		}
		if d.Poll <= 0 {
			d.Poll = 5 * time.Second
		}
		if d.Parallel <= 0 {
			d.Parallel = 4
		}
		if d.SendTimeout <= 0 {
			d.SendTimeout = 20 * time.Second
		}
		if d.MaxAttempts <= 0 {
			d.MaxAttempts = 60
		}
		if d.now == nil {
			d.now = time.Now
		}
		d.wake = make(chan struct{}, 1)
	})
}

// Wake asks for a pass now rather than at the next poll, so a notification
// queued by a request is on its way in milliseconds, not seconds.
func (d *Dispatcher) Wake() {
	d.init()
	select {
	case d.wake <- struct{}{}:
	default: // a pass is already pending; one is enough
	}
}

// Run sends until ctx is cancelled.
func (d *Dispatcher) Run(ctx context.Context) {
	d.init()
	for {
		n, err := d.Pass(ctx)
		if err != nil {
			d.lastErr.Store(err.Error())
			d.log("warn", "notification pass failed", map[string]any{"error": err.Error()})
		}
		// A full batch means more is probably waiting: go again at once.
		if n >= d.Batch && err == nil {
			continue
		}
		t := time.NewTimer(d.Poll)
		select {
		case <-ctx.Done():
			t.Stop()
			return
		case <-d.wake:
			t.Stop()
		case <-t.C:
		}
	}
}

// Pass claims one batch, sends it, and records every outcome. It returns how
// many deliveries it handled.
func (d *Dispatcher) Pass(ctx context.Context) (n int, err error) {
	d.init()
	now := d.now()
	d.lastPass.Store(now.UnixNano())
	// Only passes that found work are recorded: an idle pass every five
	// seconds would bury the job list in runs of nothing.
	batch, err := d.Store.Claim(ctx, now, d.Lease, d.Batch)
	if err != nil {
		obs.StartJob("push-sender")(0, err)
		return 0, err
	}
	if len(batch) > 0 {
		done := obs.StartJob("push-sender")
		defer func() { done(n, err) }()
	}
	sem := make(chan struct{}, d.Parallel)
	var wg sync.WaitGroup
	for _, dl := range batch {
		sem <- struct{}{}
		wg.Go(func() {
			defer func() { <-sem }()
			d.deliver(ctx, dl)
		})
	}
	wg.Wait()
	return len(batch), nil
}

func (d *Dispatcher) deliver(ctx context.Context, dl Delivery) {
	now := d.now()
	n, sub := dl.Notification, dl.Subscription

	// Too late to be worth delivering.
	if !n.ExpiresAt.IsZero() && now.After(n.ExpiresAt) {
		d.kill(ctx, dl, 0, "expired before it could be delivered", now)
		return
	}
	if dl.Attempts > d.MaxAttempts {
		d.kill(ctx, dl, 0, "gave up after too many attempts", now)
		return
	}

	payload, err := Payload(n, sub.Origin)
	if err != nil {
		d.kill(ctx, dl, 0, "payload: "+err.Error(), now)
		return
	}
	ttl := time.Duration(0)
	if !n.ExpiresAt.IsZero() {
		// At least a minute; at most the four weeks an RFC 8030 service keeps
		// a message for.
		ttl = min(max(n.ExpiresAt.Sub(now), time.Minute), 28*24*time.Hour)
	}
	msg := webpush.Message{Payload: payload, TTL: ttl, Urgency: n.Urgency, Topic: n.Tag}

	sctx, cancel := context.WithTimeout(ctx, d.SendTimeout)
	res := d.Sender.Send(sctx, webpush.Subscription{Endpoint: sub.Endpoint, P256dh: sub.P256dh, Auth: sub.Auth}, msg)
	cancel()

	fields := map[string]any{
		"notification_id": n.ID, "subscription_id": sub.ID, "user": n.Username,
		"attempt": dl.Attempts, "status": res.Status, "service": host(sub.Endpoint),
	}
	now = d.now()
	switch res.Outcome {
	case webpush.Delivered:
		if err := d.Store.Delivered(ctx, dl, res.Status, now); err != nil {
			d.log("warn", "delivered but the row could not be removed", with(fields, "error", err.Error()))
			return
		}
		d.delivered.Add(1)
		d.log("info", "notification delivered", fields)

	case webpush.Gone:
		if err := d.Store.DropSubscription(ctx, sub.ID, res.Reason); err != nil {
			d.log("warn", "could not drop a vanished device", with(fields, "error", err.Error()))
			return
		}
		d.dropped.Add(1)
		d.log("info", "device no longer subscribed; removed", with(fields, "reason", res.Reason))

	case webpush.Rejected:
		d.kill(ctx, dl, res.Status, res.Reason, now)

	case webpush.Unauthorized:
		// Everything to this service will fail the same way until the key or
		// subject is fixed. Retried slowly, and loudly, rather than dropped.
		at := now.Add(30 * time.Minute)
		d.reschedule(ctx, dl, at, res, fields, "error")

	default: // Retry
		at := now.Add(Backoff(dl.Attempts, res.RetryAfter))
		d.reschedule(ctx, dl, at, res, fields, "warn")
	}
}

func (d *Dispatcher) reschedule(ctx context.Context, dl Delivery, at time.Time, res webpush.Result, fields map[string]any, level string) {
	if err := d.Store.Reschedule(ctx, dl, at, res.Status, res.Reason, d.now()); err != nil {
		d.log("warn", "could not reschedule a delivery", with(fields, "error", err.Error()))
		return
	}
	d.retried.Add(1)
	d.log(level, "notification not delivered; will retry",
		with(with(fields, "reason", res.Reason), "retry_at", at.Format(time.RFC3339)))
}

func (d *Dispatcher) kill(ctx context.Context, dl Delivery, status int, reason string, now time.Time) {
	if err := d.Store.Kill(ctx, dl, status, reason, now); err != nil {
		d.log("warn", "could not move a delivery to the dead letters", map[string]any{
			"notification_id": dl.Notification.ID, "error": err.Error()})
		return
	}
	d.killed.Add(1)
	d.log("warn", "notification abandoned", map[string]any{
		"notification_id": dl.Notification.ID, "subscription_id": dl.Subscription.ID,
		"user": dl.Notification.Username, "status": status, "reason": reason,
		"service": host(dl.Subscription.Endpoint)})
}

// Backoff is the wait before attempt n+1 (n counts attempts already made).
//
// 10 s, 20 s, 40 s ... capped at 30 minutes, with +-20% jitter so a push
// service that comes back after an outage is not hit by every queued message
// in the same second. A Retry-After from the service is a floor.
func Backoff(attempts int, retryAfter time.Duration) time.Duration {
	if attempts < 1 {
		attempts = 1
	}
	d := 10 * time.Second
	for i := 1; i < attempts && d < 30*time.Minute; i++ {
		d *= 2
	}
	d = min(d, 30*time.Minute)
	jitter := 0.8 + rand.Float64()*0.4
	d = time.Duration(float64(d) * jitter)
	return max(d, retryAfter)
}

// Stats is what an administrator sees about the sender.
type Stats struct {
	Delivered int64     `json:"delivered"`
	Retried   int64     `json:"retried"`
	Abandoned int64     `json:"abandoned"`
	Removed   int64     `json:"devices_removed"`
	LastPass  time.Time `json:"last_pass"`
	LastError string    `json:"last_error,omitempty"`
}

// Stats reports counts since start.
func (d *Dispatcher) Stats() Stats {
	s := Stats{
		Delivered: d.delivered.Load(), Retried: d.retried.Load(),
		Abandoned: d.killed.Load(), Removed: d.dropped.Load(),
	}
	if ns := d.lastPass.Load(); ns > 0 {
		s.LastPass = time.Unix(0, ns)
	}
	if e, ok := d.lastErr.Load().(string); ok {
		s.LastError = e
	}
	return s
}

func (d *Dispatcher) log(level, msg string, fields map[string]any) {
	if d.Log != nil {
		d.Log(level, msg, fields)
	}
}

func with(m map[string]any, k string, v any) map[string]any {
	out := make(map[string]any, len(m)+1)
	maps.Copy(out, m)
	out[k] = v
	return out
}

// host is the push service's host, for the log: which platform, never the
// device-specific path.
func host(endpoint string) string {
	u, err := url.Parse(endpoint)
	if err != nil {
		return ""
	}
	return u.Hostname()
}
