package webpush

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
)

// Subscription is what a browser's PushManager.subscribe returned.
type Subscription struct {
	Endpoint string
	P256dh   string
	Auth     string
}

// Message is one notification for one device.
type Message struct {
	Payload []byte
	// TTL is how long the push service keeps the message for a device that is
	// off. Zero means "only if the device is reachable right now", which is
	// the wrong default for a notification that has to survive a phone being
	// in a pocket with no signal -- so zero is replaced by a day.
	TTL time.Duration
	// Urgency is "very-low", "low", "normal" or "high". High wakes a phone in
	// battery saver; it is for work that just landed on somebody's desk.
	Urgency string
	// Topic collapses: a newer message with the same topic replaces an older
	// one still waiting at the push service. At most 32 URL-safe characters.
	Topic string
}

// Outcome is what to do about a delivery attempt.
type Outcome int

const (
	// Delivered: the push service accepted the message. That is as far as
	// the protocol reports -- it does not say the phone displayed it -- and
	// it is the point at which the outbox row is deleted.
	Delivered Outcome = iota
	// Gone: the subscription no longer exists (404/410). The browser was
	// reset, the app uninstalled or permission revoked. Delete the
	// subscription; no retry will ever succeed.
	Gone
	// Retry: a transient failure -- the network, a timeout, 429, a 5xx. Try
	// again later, respecting RetryAfter.
	Retry
	// Rejected: the message itself is unacceptable (400, 413). Sending it
	// again unchanged will fail the same way.
	Rejected
	// Unauthorized: the push service refused our VAPID identity (401/403).
	// A configuration fault that affects every message to that service, so
	// it is retried slowly and surfaced to an administrator.
	Unauthorized
)

func (o Outcome) String() string {
	switch o {
	case Delivered:
		return "delivered"
	case Gone:
		return "gone"
	case Retry:
		return "retry"
	case Rejected:
		return "rejected"
	case Unauthorized:
		return "unauthorized"
	}
	return "unknown"
}

// Result is one attempt's result.
type Result struct {
	Outcome    Outcome
	Status     int           // HTTP status, 0 when no response arrived
	RetryAfter time.Duration // from the Retry-After header, when given
	Reason     string        // short, loggable; never contains the payload
}

// Sender delivers messages.
type Sender struct {
	Keys    *Keys
	Subject string // VAPID sub: mailto: or https:
	Client  *http.Client
	// Hosts lets a deployment add push-service hosts to the built-in list.
	Hosts []string
	// now is overridden by tests.
	now func() time.Time
	// allowAnyEndpoint lets a test point the sender at an httptest server.
	// Unexported, so no configuration outside this package can switch the
	// allowlist off.
	allowAnyEndpoint bool
}

// pushHosts are the push services the four browser engines use.
//
// AN ALLOWLIST, BECAUSE THE ENDPOINT COMES FROM THE BROWSER. Anybody signed in
// can POST a "subscription", and without this list its endpoint could be
// https://192.168.77.4/anything -- turning the server into a relay that sends
// POSTs, on its own authority, to any address it can reach inside the office.
// A real subscription only ever points at one of these.
var pushHosts = []string{
	"web.push.apple.com",                // Safari, and every iOS home-screen app
	".push.apple.com",                   // Apple's regional variants
	"fcm.googleapis.com",                // Chrome, and Chromium browsers on Android
	".notify.windows.com",               // Edge (WNS), e.g. sg2p.notify.windows.com
	"updates.push.services.mozilla.com", // Firefox
	".push.services.mozilla.com",
}

// AllowedEndpoint reports whether endpoint is an https URL on a push service.
func (s *Sender) AllowedEndpoint(endpoint string) error {
	u, err := url.Parse(endpoint)
	if err != nil {
		return errors.New("not a URL")
	}
	if u.Scheme != "https" {
		return errors.New("must be https")
	}
	if u.User != nil {
		return errors.New("must not carry credentials")
	}
	if p := u.Port(); p != "" && p != "443" {
		return errors.New("must use the standard port")
	}
	host := strings.ToLower(u.Hostname())
	hosts := pushHosts
	if s != nil && len(s.Hosts) > 0 {
		hosts = append(append([]string{}, pushHosts...), s.Hosts...)
	}
	for _, h := range hosts {
		h = strings.ToLower(strings.TrimSpace(h))
		if h == "" {
			continue
		}
		if strings.HasPrefix(h, ".") {
			if strings.HasSuffix(host, h) && len(host) > len(h) {
				return nil
			}
		} else if host == h {
			return nil
		}
	}
	return fmt.Errorf("%s is not a known push service", host)
}

// Send delivers one message to one subscription.
func (s *Sender) Send(ctx context.Context, sub Subscription, msg Message) Result {
	if !s.allowAnyEndpoint {
		if err := s.AllowedEndpoint(sub.Endpoint); err != nil {
			return Result{Outcome: Rejected, Reason: "endpoint refused: " + err.Error()}
		}
	}
	body, err := Encrypt(msg.Payload, sub.P256dh, sub.Auth)
	if err != nil {
		return Result{Outcome: Rejected, Reason: "encrypt: " + err.Error()}
	}
	now := time.Now
	if s.now != nil {
		now = s.now
	}
	authz, err := s.Keys.authorization(sub.Endpoint, s.Subject, now())
	if err != nil {
		return Result{Outcome: Rejected, Reason: "vapid: " + err.Error()}
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, sub.Endpoint, bytes.NewReader(body))
	if err != nil {
		return Result{Outcome: Rejected, Reason: "request: " + err.Error()}
	}
	ttl := msg.TTL
	if ttl <= 0 {
		ttl = 24 * time.Hour
	}
	req.Header.Set("TTL", strconv.Itoa(int(ttl.Seconds())))
	req.Header.Set("Content-Encoding", "aes128gcm")
	req.Header.Set("Content-Type", "application/octet-stream")
	req.Header.Set("Authorization", authz)
	if u := strings.TrimSpace(msg.Urgency); u != "" {
		req.Header.Set("Urgency", u)
	}
	if t := topic(msg.Topic); t != "" {
		req.Header.Set("Topic", t)
	}

	client := s.Client
	if client == nil {
		client = &http.Client{Timeout: 20 * time.Second}
	}
	// A push service is never asked to redirect a POST; following one would
	// also be a way round the allowlist.
	c := *client
	c.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }

	resp, err := c.Do(req)
	if err != nil {
		return Result{Outcome: Retry, Reason: "network: " + shortErr(err)}
	}
	defer resp.Body.Close()
	// The push service's own words, a little of them, for the log. Apple's
	// 403 says "BadJwtToken" and that one word is the whole diagnosis.
	snippet, _ := io.ReadAll(io.LimitReader(resp.Body, 256))
	return classify(resp.StatusCode, resp.Header.Get("Retry-After"), strings.TrimSpace(string(snippet)), now())
}

func classify(status int, retryAfter, snippet string, now time.Time) Result {
	r := Result{Status: status, Reason: fmt.Sprintf("HTTP %d", status)}
	if snippet != "" {
		r.Reason += ": " + snippet
	}
	switch {
	case status >= 200 && status < 300:
		r.Outcome, r.Reason = Delivered, fmt.Sprintf("HTTP %d", status)
	case status == http.StatusNotFound || status == http.StatusGone:
		r.Outcome = Gone
	case status == http.StatusUnauthorized || status == http.StatusForbidden:
		r.Outcome = Unauthorized
	case status == http.StatusTooManyRequests || status >= 500:
		r.Outcome = Retry
	case status == http.StatusRequestEntityTooLarge || status == http.StatusBadRequest:
		r.Outcome = Rejected
	default:
		// Anything else unexpected is retried: an unknown status from a push
		// service is more likely a passing fault than a verdict.
		r.Outcome = Retry
	}
	if retryAfter != "" {
		if secs, err := strconv.Atoi(strings.TrimSpace(retryAfter)); err == nil && secs > 0 {
			r.RetryAfter = time.Duration(secs) * time.Second
		} else if t, err := http.ParseTime(retryAfter); err == nil && t.After(now) {
			r.RetryAfter = t.Sub(now)
		}
	}
	return r
}

// topic makes a Topic header value: URL-safe base64 alphabet only, 32 at most.
func topic(s string) string {
	var b strings.Builder
	for _, r := range s {
		if (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9') || r == '-' || r == '_' {
			b.WriteRune(r)
		}
		if b.Len() == 32 {
			break
		}
	}
	return b.String()
}

// shortErr trims an error for the log. A network error's text names the host
// and the address, which is useful; it never contains the payload.
func shortErr(err error) string {
	s := err.Error()
	if len(s) > 200 {
		s = s[:200]
	}
	return s
}
