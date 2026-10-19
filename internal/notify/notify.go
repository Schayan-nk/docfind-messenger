// Package notify decides what to deliver, to which device, and when to try
// again. The wire protocol is internal/webpush; the storage is internal/pgstore.
//
// THE CONTRACT, which is the one asked for: every notification owed to a
// device is a row. A sender claims due rows, hands each to the device's push
// service, and DELETES the row the moment the service accepts it. Anything
// else is retried with backoff -- except the answers that mean retrying can
// never succeed, which are recorded and dropped rather than retried for ever.
//
// Kept free of any database import so the whole retry policy is tested in
// memory, in milliseconds, with a clock the test controls.
package notify

import (
	"context"
	"encoding/json"
	"net/url"
	"strings"
	"time"
)

// Notification is one event announced to one person.
type Notification struct {
	ID        int64
	Username  string
	Kind      string // case, appointment, announcement, test
	DedupeKey string // the event's identity: "case:156151:3"
	Title     string
	Body      string
	URL       string // path within the application, e.g. "/#pending"
	Tag       string // a newer one with the same tag replaces this on the device
	Urgency   string // "normal" or "high"
	// Quiet is set inside the recipient's quiet hours: delivered and listed
	// as usual, shown by the device without sound or vibration.
	Quiet     bool
	CreatedAt time.Time
	ExpiresAt time.Time
}

// Subscription is one device that asked to be notified.
type Subscription struct {
	ID       int64
	Username string
	Endpoint string
	P256dh   string
	Auth     string
	// Origin is the address the device subscribed FROM -- the site it will
	// open when the notification is pressed. Stored per device because the
	// same server can be reached under more than one name, and a phone must
	// be sent back to the one it actually uses.
	Origin        string
	Label         string
	UserAgent     string
	CreatedAt     time.Time
	LastSuccessAt time.Time
	LastFailureAt time.Time
	LastStatus    int
	LastError     string
	Failures      int
}

// Delivery is one notification owed to one device: an outbox row, claimed.
type Delivery struct {
	ID           int64
	Attempts     int // including the one about to be made
	Notification Notification
	Subscription Subscription
}

// Store is what the dispatcher needs from storage.
type Store interface {
	// Claim returns up to limit due deliveries and pushes each one's next
	// attempt past now+lease, so a crash mid-send re-delivers rather than
	// loses. Two senders never claim the same row.
	Claim(ctx context.Context, now time.Time, lease time.Duration, limit int) ([]Delivery, error)
	// Delivered deletes the row: the push service accepted it.
	Delivered(ctx context.Context, d Delivery, status int, now time.Time) error
	// Reschedule keeps the row for another attempt at `at`.
	Reschedule(ctx context.Context, d Delivery, at time.Time, status int, reason string, now time.Time) error
	// Kill moves the row to the dead letters: it will never be delivered.
	Kill(ctx context.Context, d Delivery, status int, reason string, now time.Time) error
	// DropSubscription deletes a device that no longer exists, and with it
	// everything queued for it.
	DropSubscription(ctx context.Context, subscriptionID int64, reason string) error
}

// Payload builds what is sent to the device.
//
// DECLARATIVE WEB PUSH, with a service-worker fallback in the same message.
// The top-level "web_push": 8030 is the marker Safari 18.4 and later look for:
// on an iPhone the system shows the notification straight from this JSON,
// with no script of ours having to wake up and run in time -- which is the
// iOS failure mode this format was invented to remove. Browsers without it
// deliver the same JSON to the service worker's push handler, which reads
// the same fields and shows the same notification.
//
// "navigate" must be ABSOLUTE for the declarative path, so it is resolved
// against the origin the device subscribed from.
func Payload(n Notification, origin string) ([]byte, error) {
	target := resolve(origin, n.URL)
	note := map[string]any{
		"title":    n.Title,
		"body":     n.Body,
		"navigate": target,
		"lang":     "fa",
		"dir":      "rtl",
		"silent":   n.Quiet,
		"data":     map[string]any{"url": target, "id": n.ID, "kind": n.Kind},
	}
	if n.Tag != "" {
		note["tag"] = n.Tag
	}
	return json.Marshal(map[string]any{
		"web_push":     8030,
		"notification": note,
	})
}

// resolve makes path absolute against origin, refusing anything that would
// send the device somewhere other than the application.
func resolve(origin, path string) string {
	if path == "" {
		path = "/"
	}
	base, err := url.Parse(strings.TrimRight(origin, "/") + "/")
	if err != nil || base.Scheme == "" || base.Host == "" {
		// No usable origin: a relative path, which the service worker
		// resolves against its own.
		if strings.HasPrefix(path, "/") && !strings.HasPrefix(path, "//") {
			return path
		}
		return "/"
	}
	ref, err := url.Parse(path)
	if err != nil {
		return base.String()
	}
	abs := base.ResolveReference(ref)
	// A notification can only ever open this application.
	if abs.Scheme != base.Scheme || abs.Host != base.Host {
		return base.String()
	}
	return abs.String()
}

// ValidOrigin reports whether a device's claimed origin is usable.
//
// HTTPS only, except for this machine. A push subscription only exists in a
// secure context anyway, so an http origin from anywhere but localhost is a
// request that did not come from a real subscription.
func ValidOrigin(origin string) bool {
	u, err := url.Parse(origin)
	if err != nil || u.Host == "" || u.Path != "" && u.Path != "/" || u.RawQuery != "" || u.User != nil {
		return false
	}
	switch u.Scheme {
	case "https":
		return true
	case "http":
		h := u.Hostname()
		return h == "localhost" || h == "127.0.0.1" || h == "::1"
	}
	return false
}
