package main

import (
	"context"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/Schayan-nk/docfind-messenger/internal/find"
	"github.com/Schayan-nk/docfind-messenger/internal/notify"
	"github.com/Schayan-nk/docfind-messenger/internal/obs"
	"github.com/Schayan-nk/docfind-messenger/internal/pgstore"
	"github.com/Schayan-nk/docfind-messenger/internal/secretbox"
	"github.com/Schayan-nk/docfind-messenger/internal/secrets"
	"github.com/Schayan-nk/docfind-messenger/internal/webpush"
)

// Device notifications: the sender, the watchers and the key they share.
//
// ON WHEN POSTGRESQL IS CONFIGURED -- the queue lives there -- unless
// DOCFIND_PUSH=off. Everything that can stop it is logged in one line that says
// what to do, because "notifications silently never arrive" is the failure
// this whole feature exists to end.

// pushSubject is the VAPID contact the push services see.
//
// Apple REFUSES anything but a real mailto: or https: address (403
// BadJwtToken) while every other service accepts the same message -- so a bad
// value shows up as "works on Windows, never on iPhone". The default is the
// mailbox this server already reads, which is a real, monitored address.
func pushSubject() string {
	if s := strings.TrimSpace(os.Getenv("DOCFIND_PUSH_SUBJECT")); s != "" {
		return s
	}
	if a := strings.TrimSpace(os.Getenv("DOCFIND_MAIL_ADDRESS")); a != "" {
		return "mailto:" + a
	}
	return ""
}

func pushOff() bool {
	v := strings.ToLower(strings.TrimSpace(os.Getenv("DOCFIND_PUSH")))
	return v == "0" || v == "off" || v == "false" || v == "no"
}

// startNotifications wires the feature in, or logs why it is off.
func startNotifications(app *find.Server, db *pgstore.DB, stateDir string) {
	if pushOff() {
		logf("device notifications are off: DOCFIND_PUSH says so")
		return
	}
	if db == nil {
		logf("device notifications are off: they need PostgreSQL (DOCFIND_PG_*), which holds their queue")
		return
	}
	subject := pushSubject()
	if err := webpush.ValidSubject(subject); err != nil {
		logf("device notifications are off: the sender contact %q is not usable (%v). "+
			"Set DOCFIND_PUSH_SUBJECT=mailto:someone@your-domain -- Apple refuses anything else", subject, err)
		return
	}

	// The key sealing the stored VAPID key sits beside the accounts, like
	// every other key this server keeps. BACK IT UP WITH THE DATABASE.
	box, err := secretbox.OpenFileBox(filepath.Join(stateDir, "secrets"))
	if err != nil {
		logf("device notifications are off: the secrets key could not be opened: %v", err)
		return
	}
	keys, created, err := loadOrCreateVAPID(secrets.Open(db, box))
	if err != nil {
		logf("device notifications are off: %v", err)
		return
	}
	if created {
		logf("device notifications: generated this server's push key -- back up %s together with the database",
			filepath.Join(stateDir, "secrets.key"))
	}

	sender := &webpush.Sender{
		Keys: keys, Subject: subject,
		Client: &http.Client{Timeout: 20 * time.Second},
		Hosts:  extraPushHosts(),
	}
	store := pgstore.NewNotifications(db)
	d := &notify.Dispatcher{
		Store: store, Sender: sender,
		Log: func(level, msg string, fields map[string]any) {
			f := obs.Fields{"msg": msg}
			for k, v := range fields {
				f[k] = v
			}
			obs.Default().Write("push", level, f)
		},
	}
	app.UseNotifications(store, d, keys.PublicB64(), subject, sender.AllowedEndpoint, webpush.ValidateKeys)
	// The Android app's APK: DOCFIND_ANDROID_DIR, or "app" beside the program.
	apkDir := strings.TrimSpace(os.Getenv("DOCFIND_ANDROID_DIR"))
	if exe, err := os.Executable(); apkDir == "" && err == nil {
		apkDir = exe[:strings.LastIndexAny(exe, `\/`)+1] + "app"
	}
	app.UseAndroidApp(apkDir)

	// NOT STARTED HERE: main calls startPushWorkers once everything is in
	// place, just before serving, so no goroutine reads the server while it
	// is still being wired.
	startPushWorkers = func() {
		ctx := context.Background()
		go d.Run(ctx)
		go pruneNotificationsDaily(ctx, store)
	}
	logf("device notifications on: sender contact %s", subject)
}

// loadOrCreateVAPID returns the server's push key, generating it the first
// time.
//
// A key that EXISTS but cannot be read is an error, not a reason to make a new
// one. A new key silently orphans every device that ever subscribed, and each
// person would find out only by noticing that nothing arrives. So the feature
// stays off and the log says why, and replacing the key is left to a person.
func loadOrCreateVAPID(sec *secrets.Store) (*webpush.Keys, bool, error) {
	stored, err := sec.Get(secrets.KeyVAPID)
	if err != nil {
		return nil, false, err
	}
	if stored != "" {
		k, err := webpush.ParseKeys(stored)
		return k, false, err
	}
	k, err := webpush.GenerateKeys()
	if err != nil {
		return nil, false, err
	}
	if err := sec.Set(secrets.KeyVAPID, k.PrivateB64(), "docfind"); err != nil {
		return nil, false, err
	}
	return k, true, nil
}

// extraPushHosts reads DOCFIND_PUSH_HOSTS, a comma-separated list added to the
// built-in push-service allowlist -- for a browser whose push service is not
// one of the four known ones.
func extraPushHosts() []string {
	var out []string
	for _, h := range strings.Split(os.Getenv("DOCFIND_PUSH_HOSTS"), ",") {
		if h = strings.TrimSpace(h); h != "" {
			out = append(out, h)
		}
	}
	return out
}

// pruneNotificationsDaily trims the history once a day.
func pruneNotificationsDaily(ctx context.Context, store *pgstore.Notifications) {
	t := time.NewTicker(24 * time.Hour)
	defer t.Stop()
	for {
		if err := store.Prune(ctx, time.Now()); err != nil {
			logf("notification history: %v", err)
		}
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		}
	}
}
