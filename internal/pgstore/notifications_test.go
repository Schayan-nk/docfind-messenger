package pgstore

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"testing"
	"time"

	"github.com/Schayan-nk/docfind-messenger/internal/notify"
)

// The notification outbox against a real PostgreSQL.
//
// Skipped without DOCFIND_PG_*, like every test in this package. The retry
// POLICY is tested in memory in internal/notify; what only a real server can
// answer is whether this SQL does what that policy assumes: claims never
// overlap, a lease hides a row, delete-on-success deletes, and an enqueue is
// idempotent.
//
// Every row is created under a throwaway username and removed afterwards, so
// running this against a live database leaves nothing behind.
func notifyStore(t *testing.T) (*Notifications, string) {
	t.Helper()
	db := testDB(t)
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	if _, err := db.Migrate(ctx); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	b := make([]byte, 6)
	rand.Read(b)
	user := "test-notify-" + hex.EncodeToString(b)
	t.Cleanup(func() {
		ctx := context.Background()
		p := db.Pool()
		p.Exec(ctx, `DELETE FROM push_subscriptions WHERE username = $1`, user)
		p.Exec(ctx, `DELETE FROM notifications WHERE username = $1`, user)
		p.Exec(ctx, `DELETE FROM notification_dead WHERE username = $1`, user)
		p.Exec(ctx, `DELETE FROM notification_seen WHERE username = $1`, user)
		p.Exec(ctx, `DELETE FROM notification_primed WHERE username = $1`, user)
		p.Exec(ctx, `DELETE FROM calendar_snapshot WHERE username = $1`, user)
		p.Exec(ctx, `DELETE FROM app_devices WHERE username = $1 OR username = $2`, user, user+"-other")
	})
	return NewNotifications(db), user
}

func sub(user, tag string) notify.Subscription {
	return notify.Subscription{Username: user, Endpoint: "https://web.push.apple.com/test-" + user + "-" + tag,
		P256dh: "BExample", Auth: "AuthExample", Origin: "https://docfind.example-office.ir"}
}

func TestTheOutboxDeletesADeliveredRow(t *testing.T) {
	s, user := notifyStore(t)
	ctx := context.Background()
	if _, err := s.Subscribe(ctx, sub(user, "phone")); err != nil {
		t.Fatal(err)
	}
	id, created, queued, err := s.Enqueue(ctx, notify.Notification{Username: user, Kind: "test",
		DedupeKey: "k1", Title: "t", ExpiresAt: time.Now().Add(time.Hour)})
	if err != nil || !created || queued != 1 || id == 0 {
		t.Fatalf("enqueue: id=%d created=%v queued=%d err=%v", id, created, queued, err)
	}
	// Idempotent: the same event again creates nothing.
	if _, created, _, _ := s.Enqueue(ctx, notify.Notification{Username: user, Kind: "test",
		DedupeKey: "k1", Title: "t"}); created {
		t.Error("the same dedupe key was enqueued twice")
	}

	now := time.Now()
	batch, err := s.Claim(ctx, now, time.Minute, 100)
	if err != nil {
		t.Fatal(err)
	}
	var mine *notify.Delivery
	for i := range batch {
		if batch[i].Notification.Username == user {
			mine = &batch[i]
		}
	}
	if mine == nil {
		t.Fatal("the queued delivery was not claimed")
	}
	if mine.Attempts != 1 || mine.Subscription.Origin != "https://docfind.example-office.ir" {
		t.Errorf("claimed %+v", mine)
	}
	// Leased: a second claim inside the lease does not see it.
	again, _ := s.Claim(ctx, now.Add(time.Second), time.Minute, 100)
	for _, d := range again {
		if d.ID == mine.ID {
			t.Fatal("a leased row was claimed twice")
		}
	}
	if err := s.Delivered(ctx, *mine, 201, now); err != nil {
		t.Fatal(err)
	}
	var left int
	s.db.Pool().QueryRow(ctx, `SELECT count(*) FROM notification_outbox WHERE id = $1`, mine.ID).Scan(&left)
	if left != 0 {
		t.Error("a delivered row was not deleted")
	}
	devs, _ := s.Devices(ctx, user)
	if len(devs) != 1 || devs[0].LastSuccessAt.Year() < 2000 || devs[0].Failures != 0 {
		t.Errorf("device after delivery: %+v", devs)
	}
}

func TestAKilledRowIsRecordedAndAVanishedDeviceTakesItsQueue(t *testing.T) {
	s, user := notifyStore(t)
	ctx := context.Background()
	d1, _ := s.Subscribe(ctx, sub(user, "a"))
	s.Subscribe(ctx, sub(user, "b"))
	s.Enqueue(ctx, notify.Notification{Username: user, Kind: "case", DedupeKey: "c1", Title: "x"})
	s.Enqueue(ctx, notify.Notification{Username: user, Kind: "case", DedupeKey: "c2", Title: "y"})

	batch, _ := s.Claim(ctx, time.Now(), time.Minute, 1000)
	var first notify.Delivery
	for _, d := range batch {
		if d.Notification.Username == user && d.Subscription.ID != d1.ID {
			first = d
			break
		}
	}
	// Without this the test passed with NOTHING claimed: it killed a
	// zero-value delivery, which still wrote one dead letter, and every later
	// assertion held vacuously. Found when a clock-skew bug stopped claims.
	if first.ID == 0 {
		t.Fatal("nothing was claimed for the second device")
	}
	if err := s.Kill(ctx, first, 413, "HTTP 413", time.Now()); err != nil {
		t.Fatal(err)
	}
	var dead int
	s.db.Pool().QueryRow(ctx, `SELECT count(*) FROM notification_dead WHERE username = $1`, user).Scan(&dead)
	if dead != 1 {
		t.Errorf("dead letters = %d, want 1", dead)
	}
	// Device a is gone: its two queued rows go with it, recorded as dead.
	if err := s.DropSubscription(ctx, d1.ID, "HTTP 410"); err != nil {
		t.Fatal(err)
	}
	var queued int
	s.db.Pool().QueryRow(ctx, `SELECT count(*) FROM notification_outbox WHERE subscription_id = $1`, d1.ID).Scan(&queued)
	if queued != 0 {
		t.Errorf("%d rows still queued for a deleted device", queued)
	}
}

// A shared browser moving to a new person must not deliver the previous
// person's queued notifications to them.
func TestReassigningADeviceDropsThePreviousOwnersQueue(t *testing.T) {
	s, user := notifyStore(t)
	other := user + "-other"
	ctx := context.Background()
	t.Cleanup(func() {
		s.db.Pool().Exec(context.Background(), `DELETE FROM notifications WHERE username = $1`, other)
	})
	d, _ := s.Subscribe(ctx, sub(user, "shared"))
	s.Enqueue(ctx, notify.Notification{Username: user, Kind: "case", DedupeKey: "private", Title: "p"})

	moved := sub(user, "shared")
	moved.Username = other
	if _, err := s.Subscribe(ctx, moved); err != nil {
		t.Fatal(err)
	}
	var queued int
	s.db.Pool().QueryRow(ctx, `SELECT count(*) FROM notification_outbox WHERE subscription_id = $1`, d.ID).Scan(&queued)
	if queued != 0 {
		t.Error("the previous owner's notification is still queued for the reassigned device")
	}
}

func TestSeenReturnsOnlyNewKeysAndPrimedFlipsOnce(t *testing.T) {
	s, user := notifyStore(t)
	ctx := context.Background()
	fresh, err := s.MarkSeen(ctx, user, []string{"a", "b"})
	if err != nil || len(fresh) != 2 {
		t.Fatalf("first mark: %v %v", fresh, err)
	}
	fresh, _ = s.MarkSeen(ctx, user, []string{"a", "b", "c"})
	if len(fresh) != 1 || fresh[0] != "c" {
		t.Errorf("second mark returned %v, want [c]", fresh)
	}
	if was, _ := s.Primed(ctx, user, "cartable"); was {
		t.Error("the first Primed call reported already primed")
	}
	if was, _ := s.Primed(ctx, user, "cartable"); !was {
		t.Error("the second Primed call did not report primed")
	}
}
