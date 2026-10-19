package sms

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestPhoneNumbersAreReadTheWayTheyAreTyped(t *testing.T) {
	for in, want := range map[string]string{
		"09121234567":       "09121234567",
		"۰۹۱۲ ۱۲۳ ۴۵۶۷":     "09121234567",
		"+98 912 123 4567":  "09121234567",
		"0098-912-123-4567": "09121234567",
		"989121234567":      "09121234567",
		"9121234567":        "09121234567",
	} {
		if got, ok := NormalizePhone(in); !ok || got != want {
			t.Errorf("%q -> %q %v, want %q", in, got, ok, want)
		}
	}
	for _, bad := range []string{"", "021-88776655", "0912123", "abc", "+1 555 1234567"} {
		if got, ok := NormalizePhone(bad); ok {
			t.Errorf("%q was accepted as %q", bad, got)
		}
	}
}

func fakePanel(t *testing.T, answer string, status int, seen *map[string]string) func() {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = r.ParseForm()
		m := map[string]string{}
		for k := range r.PostForm {
			m[k] = r.PostForm.Get(k)
		}
		*seen = m
		w.WriteHeader(status)
		w.Write([]byte(answer))
	}))
	prev := client
	client = srv.Client()
	cfgURL = srv.URL
	return func() { client = prev; srv.Close() }
}

var cfgURL string

func TestAMessageGoesOutAsTheAsanakFormAsks(t *testing.T) {
	var seen map[string]string
	defer fakePanel(t, `{"meta":{"status":200,"message":"success"},"data":[123456]}`, 200, &seen)()
	c := Config{URL: cfgURL, Username: "09120000001", Password: "pw", APIKey: "k", From: "989120000002"}
	ids, err := c.Send(context.Background(), []string{"۰۹۱۲۱۲۳۴۵۶۷", "+98 935 000 1122"}, " کد ورود: 123456 ")
	if err != nil {
		t.Fatal(err)
	}
	if len(ids) != 1 || ids[0] != "123456" {
		t.Errorf("ids %v", ids)
	}
	want := map[string]string{"username": "09120000001", "password": "pw", "source": "989120000002",
		"destination": "09121234567,09350001122", "message": "کد ورود: 123456"}
	for k, v := range want {
		if seen[k] != v {
			t.Errorf("%s = %q, want %q", k, seen[k], v)
		}
	}
	if _, sent := seen["api_key"]; sent {
		t.Error("the API key was sent to the v2rest service")
	}
}

func TestAPanelRefusalIsReportedWithoutThePassword(t *testing.T) {
	var seen map[string]string
	defer fakePanel(t, `{"meta":{"status":1001,"message":"invalid username or password"},"data":[]}`, 200, &seen)()
	c := Config{URL: cfgURL, Username: "u", Password: "secret-pw", From: "98999"}
	_, err := c.Send(context.Background(), []string{"09121234567"}, "x")
	if err == nil || !strings.Contains(err.Error(), "1001") || strings.Contains(err.Error(), "secret-pw") {
		t.Errorf("err %v", err)
	}
	if _, err := (Config{}).Send(context.Background(), []string{"09121234567"}, "x"); err != ErrNotConfigured {
		t.Errorf("an empty configuration: %v", err)
	}
	if _, err := c.Send(context.Background(), []string{"021-1234"}, "x"); err == nil {
		t.Error("a landline was sent to")
	}
}

func TestTheOlderArrayAnswerIsRead(t *testing.T) {
	var seen map[string]string
	defer fakePanel(t, `["987654"]`, 200, &seen)()
	ids, err := Config{URL: cfgURL, Username: "u", Password: "p", From: "98"}.Send(context.Background(), []string{"09121234567"}, "x")
	if err != nil || len(ids) != 1 || ids[0] != "987654" {
		t.Errorf("ids %v err %v", ids, err)
	}
}
