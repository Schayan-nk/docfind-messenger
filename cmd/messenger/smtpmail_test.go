package main

import (
	"encoding/base64"
	"strings"
	"testing"
	"time"
)

func TestAMailboxNeedsHostUserAndPassword(t *testing.T) {
	t.Setenv("DOCFIND_SMTP_HOST", "smtp.example.com")
	t.Setenv("DOCFIND_SMTP_USERNAME", "codes@example.com")
	t.Setenv("DOCFIND_SMTP_PASSWORD", "")
	if smtpFromEnv() != nil {
		t.Fatal("a mailbox without a password was configured")
	}
	t.Setenv("DOCFIND_SMTP_PASSWORD", "secret")
	m := smtpFromEnv()
	if m == nil || m.port != "465" || m.from != "codes@example.com" {
		t.Fatalf("defaults: %+v", m)
	}
}

func TestTheMessageCarriesPersianIntact(t *testing.T) {
	m := &smtpMailer{host: "smtp.example.com", from: "codes@example.com"}
	body := "کد ورود شما: 123456"
	raw, err := m.message("someone@example.com", "کد ورود", body, time.Date(2026, 10, 18, 9, 0, 0, 0, time.UTC))
	if err != nil {
		t.Fatal(err)
	}
	head, enc, ok := strings.Cut(string(raw), "\r\n\r\n")
	if !ok || !strings.Contains(head, "Subject: =?utf-8?q?") || !strings.Contains(head, "Content-Transfer-Encoding: base64") {
		t.Fatalf("headers:\n%s", head)
	}
	got, err := base64.StdEncoding.DecodeString(strings.ReplaceAll(enc, "\r\n", ""))
	if err != nil || string(got) != body {
		t.Fatalf("body %q, %v", got, err)
	}
}

func TestALineBreakCannotAddAHeader(t *testing.T) {
	m := &smtpMailer{host: "smtp.example.com", from: "codes@example.com"}
	if _, err := m.message("a@example.com\r\nBcc: b@example.com", "x", "y", time.Now()); err != errHeaderBreak {
		t.Fatalf("got %v", err)
	}
}
