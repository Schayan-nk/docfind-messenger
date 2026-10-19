// Package sms sends text messages through the company's SMS panel.
//
// The panel is Asanak's (آسانک), and its web service is one form POST:
//
//	POST https://panel.asanak.com/webservice/v2rest/sendsms
//	username, password, source (the sender number), destination, message
//
// answered with {"meta": {"status": 200, "message": "success"}, "data": [id]}
// -- or, from the older service, with a bare JSON array of ids. Both are read.
//
// The settings are an administrator's (Settings, «پنل پیامک»): the address,
// the username, the password, an API key and the sender number. The password
// and the key are sealed where they are kept and never shown again. The API
// key is kept for the panel's newer interface; the v2rest service above signs
// in with the username and password alone, so it is not sent.
//
// Used for notifications and for the second step of signing in, both to the
// phone number on the person's account.
package sms

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"time"
)

// DefaultURL is Asanak's v2rest send address.
const DefaultURL = "https://panel.asanak.com/webservice/v2rest/sendsms"

// Config is the panel.
type Config struct {
	URL      string `json:"url"`
	Username string `json:"username"`
	Password string `json:"-"`
	APIKey   string `json:"-"`
	From     string `json:"from"` // the sender number, e.g. 98XXXXXXXXXX
}

// Enabled says whether a message can be sent.
func (c Config) Enabled() bool {
	return strings.TrimSpace(c.URL) != "" && c.Username != "" && c.Password != "" && strings.TrimSpace(c.From) != ""
}

// client is swapped by the tests for one pointing at a fake panel.
var client = &http.Client{Timeout: 20 * time.Second}

// ErrNotConfigured is a send with no panel set up.
var ErrNotConfigured = errors.New("sms: the SMS panel is not configured")

// Send sends text to one or more numbers and returns the panel's message ids.
// Numbers are normalised (NormalizePhone); one that is not a mobile number is
// refused before anything is sent.
func (c Config) Send(ctx context.Context, to []string, text string) ([]string, error) {
	if !c.Enabled() {
		return nil, ErrNotConfigured
	}
	text = strings.TrimSpace(text)
	if text == "" {
		return nil, errors.New("sms: an empty message")
	}
	var dest []string
	for _, n := range to {
		p, ok := NormalizePhone(n)
		if !ok {
			return nil, fmt.Errorf("sms: %q is not a mobile number", n)
		}
		dest = append(dest, p)
	}
	if len(dest) == 0 {
		return nil, errors.New("sms: nobody to send to")
	}
	form := url.Values{
		"username":    {c.Username},
		"password":    {c.Password},
		"source":      {strings.TrimSpace(c.From)},
		"destination": {strings.Join(dest, ",")},
		"message":     {text},
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, strings.TrimSpace(c.URL), strings.NewReader(form.Encode()))
	if err != nil {
		return nil, fmt.Errorf("sms: the panel address is not usable: %w", err)
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Accept", "application/json")
	resp, err := client.Do(req)
	if err != nil {
		// The error names the URL and nothing else: the password is in the
		// body, never in the address.
		return nil, fmt.Errorf("sms: the panel did not answer: %w", err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 64<<10))
	ids, err := readAnswer(resp.StatusCode, body)
	if err != nil {
		return nil, err
	}
	return ids, nil
}

// readAnswer reads the panel's answer in either of its shapes.
func readAnswer(status int, body []byte) ([]string, error) {
	trimmed := strings.TrimSpace(string(body))
	if strings.HasPrefix(trimmed, "[") {
		var raw []any
		if json.Unmarshal(body, &raw) == nil && status < 300 {
			return idsOf(raw), nil
		}
	}
	var out struct {
		Meta struct {
			Status  int    `json:"status"`
			Message string `json:"message"`
		} `json:"meta"`
		Data json.RawMessage `json:"data"`
	}
	if err := json.Unmarshal(body, &out); err != nil {
		return nil, fmt.Errorf("sms: the panel answered %d with something unreadable: %s", status, clip(trimmed, 160))
	}
	if status >= 300 || (out.Meta.Status != 0 && out.Meta.Status != 200) {
		code := out.Meta.Status
		if code == 0 {
			code = status
		}
		return nil, fmt.Errorf("sms: the panel refused (%d): %s", code, clip(out.Meta.Message, 160))
	}
	var raw []any
	_ = json.Unmarshal(out.Data, &raw)
	return idsOf(raw), nil
}

func idsOf(raw []any) []string {
	var ids []string
	for _, v := range raw {
		switch x := v.(type) {
		case float64:
			ids = append(ids, strconv.FormatInt(int64(x), 10))
		case string:
			ids = append(ids, x)
		}
	}
	return ids
}

func clip(s string, n int) string {
	if r := []rune(s); len(r) > n {
		return string(r[:n]) + "…"
	}
	return s
}

var mobile = regexp.MustCompile(`^09\d{9}$`)

// NormalizePhone is a mobile number as 09xxxxxxxxx: Persian and Arabic digits
// read as digits, spaces, dashes and brackets dropped, and +98, 0098 or 98 in
// front read as the leading 0. ok is false for anything that is not then an
// Iranian mobile number.
func NormalizePhone(s string) (string, bool) {
	var b strings.Builder
	for _, r := range strings.TrimSpace(s) {
		switch {
		case r >= '0' && r <= '9':
			b.WriteRune(r)
		case r >= '۰' && r <= '۹':
			b.WriteRune('0' + (r - '۰'))
		case r >= '٠' && r <= '٩':
			b.WriteRune('0' + (r - '٠'))
		case r == '+' && b.Len() == 0:
			b.WriteRune(r)
		}
	}
	n := b.String()
	switch {
	case strings.HasPrefix(n, "+98"):
		n = "0" + n[3:]
	case strings.HasPrefix(n, "0098"):
		n = "0" + n[4:]
	case strings.HasPrefix(n, "98") && len(n) == 12:
		n = "0" + n[2:]
	case strings.HasPrefix(n, "9") && len(n) == 10:
		n = "0" + n
	}
	return n, mobile.MatchString(n)
}
