package find

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/Schayan-nk/docfind-messenger/internal/sms"
)

// The SMS panel: sign-in codes for accounts that chose SMS as their second
// step, and the code for a forgotten password.
//
// Kept in app_settings under "sms" where a deployment stores it there, the
// password and the key SEALED; neither is ever sent back to a page. Without
// a stored panel it is read from the environment (smsFromEnv). Read once
// and held.

const smsSettingKey = "sms"

// smsStored is the row's shape: the secrets as sealed base64.
type smsStored struct {
	URL      string `json:"url"`
	Username string `json:"username"`
	From     string `json:"from"`
	Password string `json:"password,omitempty"`
	APIKey   string `json:"api_key,omitempty"`
}

type smsState struct {
	store  appSettingsStore
	box    secretSealer
	mu     sync.Mutex
	cfg    sms.Config
	loaded bool
}

// secretSealer is the AES-GCM box (secretbox.OpenFileBox).
type secretSealer interface {
	Seal(plain []byte) ([]byte, error)
	Open(sealed []byte) ([]byte, error)
}

// smsFromEnv is the panel the environment names: DOCFIND_SMS_USERNAME,
// DOCFIND_SMS_PASSWORD and DOCFIND_SMS_FROM (the sender number), and
// optionally DOCFIND_SMS_URL and DOCFIND_SMS_API_KEY. Unset, SMS is off.
var smsFromEnv = sync.OnceValue(func() sms.Config {
	cfg := sms.Config{
		URL:      strings.TrimSpace(os.Getenv("DOCFIND_SMS_URL")),
		Username: strings.TrimSpace(os.Getenv("DOCFIND_SMS_USERNAME")),
		Password: os.Getenv("DOCFIND_SMS_PASSWORD"),
		APIKey:   os.Getenv("DOCFIND_SMS_API_KEY"),
		From:     strings.TrimSpace(os.Getenv("DOCFIND_SMS_FROM")),
	}
	if cfg.URL == "" {
		cfg.URL = sms.DefaultURL
	}
	return cfg
})

// smsConfig is the panel as configured, stored or from the environment.
func (s *Server) smsConfig() sms.Config {
	if s.sms == nil || s.sms.store == nil {
		return smsFromEnv()
	}
	st := s.sms
	st.mu.Lock()
	defer st.mu.Unlock()
	if !st.loaded {
		st.cfg, _ = st.read(context.Background())
		st.loaded = true
	}
	return st.cfg
}

func (st *smsState) read(ctx context.Context) (sms.Config, error) {
	var row smsStored
	if err := st.store.Get(ctx, smsSettingKey, &row); err != nil {
		return sms.Config{URL: sms.DefaultURL}, err
	}
	cfg := sms.Config{URL: row.URL, Username: row.Username, From: row.From}
	if cfg.URL == "" {
		cfg.URL = sms.DefaultURL
	}
	var err error
	if cfg.Password, err = st.unseal(row.Password); err != nil {
		return cfg, err
	}
	cfg.APIKey, err = st.unseal(row.APIKey)
	return cfg, err
}

func (st *smsState) unseal(sealed string) (string, error) {
	if sealed == "" || st.box == nil {
		return "", nil
	}
	raw, err := base64.StdEncoding.DecodeString(sealed)
	if err != nil {
		return "", err
	}
	b, err := st.box.Open(raw)
	return string(b), err
}

func mustJSON(v any) string {
	b, _ := json.Marshal(v)
	return string(b)
}

// maskPhone shows enough of a number to recognise it: 0912***4567.
func maskPhone(p string) string {
	if len(p) < 8 {
		return "***"
	}
	return p[:4] + "***" + p[len(p)-4:]
}

// errNoCodeSMS is a sign-in code to be texted with no panel configured.
var errNoCodeSMS = errors.New("find: the SMS panel is not configured")

// sendSMSCode texts a fresh code for key to a phone, with the same limits as a
// mailed one (twostepmail.go): the store is shared, only the delivery differs.
func (s *Server) sendSMSCode(ctx context.Context, key, phone, purpose string) error {
	cfg := s.smsConfig()
	if !cfg.Enabled() {
		return errNoCodeSMS
	}
	code, err := s.issueCode(key, phone)
	if err != nil {
		return err
	}
	c, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	if _, err := cfg.Send(c, []string{phone}, smsCodeText(purpose, code)); err != nil {
		return fmt.Errorf("texting a sign-in code: %w", err)
	}
	return nil
}

func smsCodeText(purpose, code string) string {
	switch purpose {
	case codeForSetup:
		return "کد تأیید شماره برای ورود دومرحله‌ای docfind: " + code + "\nتا ۱۰ دقیقه معتبر است."
	case codeForConfirm:
		return "کد تأیید تغییر تنظیمات ورود docfind: " + code
	case codeForReset:
		return "کد بازیابی گذرواژهٔ docfind: " + code + "\nتا ۱۰ دقیقه معتبر است. اگر شما درخواست نکرده‌اید، این پیام را نادیده بگیرید."
	}
	return "کد ورود docfind: " + code + "\nاگر شما نبودید، گذرواژه‌تان را عوض کنید."
}
