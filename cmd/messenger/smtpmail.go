package main

import (
	"context"
	"crypto/tls"
	"encoding/base64"
	"errors"
	"mime"
	"net"
	"net/smtp"
	"os"
	"strings"
	"time"
)

// A mailbox to send sign-in codes from, named in the environment:
//
//	DOCFIND_SMTP_HOST      smtp.example.com
//	DOCFIND_SMTP_PORT      465 (TLS from the start, the default) or 587 (STARTTLS)
//	DOCFIND_SMTP_USERNAME  the mailbox's login
//	DOCFIND_SMTP_PASSWORD  its password
//	DOCFIND_SMTP_FROM      the From address; the username when unset
//
// Only over TLS: a server that offers neither is refused rather than handed
// the password in the clear.
type smtpMailer struct {
	host, port, user, pass, from string
}

// smtpFromEnv is the mailbox the environment names, or nil.
func smtpFromEnv() *smtpMailer {
	m := &smtpMailer{
		host: strings.TrimSpace(os.Getenv("DOCFIND_SMTP_HOST")),
		port: strings.TrimSpace(os.Getenv("DOCFIND_SMTP_PORT")),
		user: strings.TrimSpace(os.Getenv("DOCFIND_SMTP_USERNAME")),
		pass: os.Getenv("DOCFIND_SMTP_PASSWORD"),
		from: strings.TrimSpace(os.Getenv("DOCFIND_SMTP_FROM")),
	}
	if m.host == "" || m.user == "" || m.pass == "" {
		return nil
	}
	if m.port == "" {
		m.port = "465"
	}
	if m.from == "" {
		m.from = m.user
	}
	return m
}

var errHeaderBreak = errors.New("smtp: a line break in an address or the subject")

// SendMail sends one plain-text message.
func (m *smtpMailer) SendMail(ctx context.Context, to, subject, body string) error {
	if strings.ContainsAny(to+m.from+subject, "\r\n") {
		return errHeaderBreak
	}
	msg, err := m.message(to, subject, body, time.Now())
	if err != nil {
		return err
	}
	tlsCfg := &tls.Config{ServerName: m.host, MinVersion: tls.VersionTLS12}
	addr := net.JoinHostPort(m.host, m.port)
	dialer := &net.Dialer{Timeout: 20 * time.Second}
	var conn net.Conn
	if m.port == "465" {
		conn, err = (&tls.Dialer{NetDialer: dialer, Config: tlsCfg}).DialContext(ctx, "tcp", addr)
	} else {
		conn, err = dialer.DialContext(ctx, "tcp", addr)
	}
	if err != nil {
		return err
	}
	if dl, ok := ctx.Deadline(); ok {
		_ = conn.SetDeadline(dl)
	}
	c, err := smtp.NewClient(conn, m.host)
	if err != nil {
		conn.Close()
		return err
	}
	defer c.Close()
	if m.port != "465" {
		if ok, _ := c.Extension("STARTTLS"); !ok {
			return errors.New("smtp: the server offers no STARTTLS")
		}
		if err := c.StartTLS(tlsCfg); err != nil {
			return err
		}
	}
	if err := c.Auth(smtp.PlainAuth("", m.user, m.pass, m.host)); err != nil {
		return err
	}
	if err := c.Mail(m.from); err != nil {
		return err
	}
	if err := c.Rcpt(to); err != nil {
		return err
	}
	w, err := c.Data()
	if err != nil {
		return err
	}
	if _, err := w.Write(msg); err != nil {
		return err
	}
	if err := w.Close(); err != nil {
		return err
	}
	return c.Quit()
}

// message is the whole message: headers, and the body in base64 so Persian
// text arrives as written.
func (m *smtpMailer) message(to, subject, body string, now time.Time) ([]byte, error) {
	if strings.ContainsAny(to+m.from+subject, "\r\n") {
		return nil, errHeaderBreak
	}
	var b strings.Builder
	b.WriteString("From: " + m.from + "\r\n")
	b.WriteString("To: " + to + "\r\n")
	b.WriteString("Subject: " + mime.QEncoding.Encode("utf-8", subject) + "\r\n")
	b.WriteString("Date: " + now.Format(time.RFC1123Z) + "\r\n")
	b.WriteString("MIME-Version: 1.0\r\nContent-Type: text/plain; charset=utf-8\r\nContent-Transfer-Encoding: base64\r\n\r\n")
	enc := base64.StdEncoding.EncodeToString([]byte(body))
	for len(enc) > 76 {
		b.WriteString(enc[:76] + "\r\n")
		enc = enc[76:]
	}
	b.WriteString(enc + "\r\n")
	return []byte(b.String()), nil
}
