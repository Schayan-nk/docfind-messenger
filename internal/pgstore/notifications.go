package pgstore

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/Schayan-nk/docfind-messenger/internal/notify"
)

// The notification outbox. See migrations/007_notifications.sql for the shape
// and internal/notify for the retry policy that drives it.

// Notifications is the store. It satisfies notify.Store.
type Notifications struct{ db *DB }

// NewNotifications returns a store over db.
func NewNotifications(db *DB) *Notifications { return &Notifications{db: db} }

var _ notify.Store = (*Notifications)(nil)

const notifyTimeout = 15 * time.Second

// maxDevicesPerUser bounds how many subscriptions one account can hold. Real
// people have two or three; the ceiling is there so a script cannot make the
// server fan every notification out to thousands of endpoints.
const maxDevicesPerUser = 10

// ---------------------------------------------------------------- devices

// Subscribe records a device for username, or updates it.
//
// THE ENDPOINT IS THE DEVICE. A browser that subscribes again gets the same
// endpoint back, so this is an upsert on it -- otherwise re-enabling
// notifications on a phone would make it receive everything twice.
//
// A SHARED BROWSER MOVES WITH THE PERSON. If the endpoint already belongs to
// somebody else, it is reassigned -- whoever signed in and pressed the button
// is who the device is now for -- and anything still queued for the previous
// person on that device is DELETED, not delivered: it is their mail and their
// cases, and it would otherwise arrive on a device now in somebody else's hand.
func (n *Notifications) Subscribe(ctx context.Context, s notify.Subscription) (notify.Subscription, error) {
	ctx, cancel := context.WithTimeout(ctx, notifyTimeout)
	defer cancel()
	tx, err := n.db.Pool().Begin(ctx)
	if err != nil {
		return s, err
	}
	defer tx.Rollback(ctx)

	var existingID int64
	var existingUser string
	err = tx.QueryRow(ctx,
		`SELECT id, username FROM push_subscriptions WHERE endpoint = $1 FOR UPDATE`,
		s.Endpoint).Scan(&existingID, &existingUser)
	switch {
	case errors.Is(err, pgx.ErrNoRows):
	case err != nil:
		return s, err
	case existingUser != s.Username:
		if _, err := tx.Exec(ctx,
			`DELETE FROM notification_outbox WHERE subscription_id = $1`, existingID); err != nil {
			return s, err
		}
	}

	err = tx.QueryRow(ctx, `
		INSERT INTO push_subscriptions (username, endpoint, p256dh, auth, origin, label, user_agent)
		VALUES ($1, $2, $3, $4, $5, $6, $7)
		ON CONFLICT (endpoint) DO UPDATE SET
			username   = EXCLUDED.username,
			p256dh     = EXCLUDED.p256dh,
			auth       = EXCLUDED.auth,
			origin     = EXCLUDED.origin,
			label      = CASE WHEN EXCLUDED.label <> '' THEN EXCLUDED.label
			                  ELSE push_subscriptions.label END,
			user_agent = EXCLUDED.user_agent,
			failures   = 0,
			last_error = ''
		RETURNING id, created_at`,
		s.Username, s.Endpoint, s.P256dh, s.Auth, s.Origin, s.Label, s.UserAgent,
	).Scan(&s.ID, &s.CreatedAt)
	if err != nil {
		return s, err
	}

	// Past the ceiling, the devices least recently heard from go first.
	if _, err := tx.Exec(ctx, `
		DELETE FROM push_subscriptions WHERE id IN (
			SELECT id FROM push_subscriptions WHERE username = $1
			ORDER BY coalesce(last_success_at, created_at) DESC
			OFFSET $2)`, s.Username, maxDevicesPerUser); err != nil {
		return s, err
	}
	return s, tx.Commit(ctx)
}

// Devices lists username's subscriptions, newest first.
func (n *Notifications) Devices(ctx context.Context, username string) ([]notify.Subscription, error) {
	ctx, cancel := context.WithTimeout(ctx, notifyTimeout)
	defer cancel()
	rows, err := n.db.Pool().Query(ctx, `
		SELECT id, username, endpoint, origin, label, user_agent, created_at,
		       coalesce(last_success_at, 'epoch'), coalesce(last_failure_at, 'epoch'),
		       coalesce(last_status, 0), last_error, failures
		FROM push_subscriptions WHERE username = $1
		ORDER BY created_at DESC`, username)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []notify.Subscription
	for rows.Next() {
		var s notify.Subscription
		if err := rows.Scan(&s.ID, &s.Username, &s.Endpoint, &s.Origin, &s.Label, &s.UserAgent,
			&s.CreatedAt, &s.LastSuccessAt, &s.LastFailureAt, &s.LastStatus, &s.LastError,
			&s.Failures); err != nil {
			return nil, err
		}
		out = append(out, s)
	}
	return out, rows.Err()
}

// Unsubscribe deletes one of username's devices. It reports whether one was
// deleted: an ID belonging to somebody else deletes nothing, which is what
// keeps this from being a way to switch off a colleague's notifications.
func (n *Notifications) Unsubscribe(ctx context.Context, username string, id int64) (bool, error) {
	ctx, cancel := context.WithTimeout(ctx, notifyTimeout)
	defer cancel()
	tag, err := n.db.Pool().Exec(ctx,
		`DELETE FROM push_subscriptions WHERE id = $1 AND username = $2`, id, username)
	if err != nil {
		return false, err
	}
	return tag.RowsAffected() > 0, nil
}

// UnsubscribeEndpoint deletes the device with this endpoint, if it is
// username's. For signing out: the page knows its own endpoint, not its ID.
func (n *Notifications) UnsubscribeEndpoint(ctx context.Context, username, endpoint string) (bool, error) {
	ctx, cancel := context.WithTimeout(ctx, notifyTimeout)
	defer cancel()
	tag, err := n.db.Pool().Exec(ctx,
		`DELETE FROM push_subscriptions WHERE endpoint = $1 AND username = $2`, endpoint, username)
	if err != nil {
		return false, err
	}
	return tag.RowsAffected() > 0, nil
}

// SubscribedUsers is every account with at least one device -- the people the
// watchers have to look at.
func (n *Notifications) SubscribedUsers(ctx context.Context) ([]string, error) {
	ctx, cancel := context.WithTimeout(ctx, notifyTimeout)
	defer cancel()
	rows, err := n.db.Pool().Query(ctx, `
		SELECT DISTINCT username FROM push_subscriptions ORDER BY username`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var u string
		if err := rows.Scan(&u); err != nil {
			return nil, err
		}
		out = append(out, u)
	}
	return out, rows.Err()
}

// ---------------------------------------------------------------- enqueueing

// Enqueue records a notification and queues one delivery per device.
//
// IDEMPOTENT ON (username, dedupe_key): the same event enqueued twice creates
// nothing the second time, and reports that with created=false. That is what
// lets a watcher run every two minutes over the same list of open cases.
//
// queued is how many devices it was queued for -- zero for a person with none,
// in which case the notification is still recorded, for the history.
func (n *Notifications) Enqueue(ctx context.Context, nt notify.Notification) (id int64, created bool, queued int, err error) {
	ctx, cancel := context.WithTimeout(ctx, notifyTimeout)
	defer cancel()
	if nt.ExpiresAt.IsZero() {
		nt.ExpiresAt = time.Now().Add(24 * time.Hour)
	}
	if nt.Urgency == "" {
		nt.Urgency = "normal"
	}
	if nt.URL == "" {
		nt.URL = "/"
	}
	tx, err := n.db.Pool().Begin(ctx)
	if err != nil {
		return 0, false, 0, err
	}
	defer tx.Rollback(ctx)

	err = tx.QueryRow(ctx, `
		INSERT INTO notifications (username, kind, dedupe_key, title, body, url, tag, urgency, expires_at, quiet)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10)
		ON CONFLICT (username, dedupe_key) DO NOTHING
		RETURNING id`,
		nt.Username, nt.Kind, nt.DedupeKey, nt.Title, nt.Body, nt.URL, nt.Tag, nt.Urgency, nt.ExpiresAt, nt.Quiet,
	).Scan(&id)
	if errors.Is(err, pgx.ErrNoRows) {
		return 0, false, 0, nil // already announced
	}
	if err != nil {
		return 0, false, 0, err
	}
	// next_attempt_at from THE APPLICATION'S clock, not the column default.
	//
	// Every other scheduling time -- the claim's "due before now", the lease,
	// each backoff -- comes from the process's clock, and a due time stamped by
	// the database's clock is being compared across two machines. Measured
	// against the server from a development workstation: the server ran 9.5 ms
	// ahead, which was enough for a row inserted a moment earlier to look like
	// the future and not be claimed. One clock for every comparison, whichever
	// machine the process runs on.
	tag, err := tx.Exec(ctx, `
		INSERT INTO notification_outbox (notification_id, subscription_id, next_attempt_at)
		SELECT $1, id, $3 FROM push_subscriptions WHERE username = $2`, id, nt.Username, time.Now())
	if err != nil {
		return 0, false, 0, err
	}
	if err := tx.Commit(ctx); err != nil {
		return 0, false, 0, err
	}
	return id, true, int(tag.RowsAffected()), nil
}

// ---------------------------------------------------------------- the sender

// Claim implements notify.Store.
//
// FOR UPDATE SKIP LOCKED makes two senders -- a second process started by
// mistake, or a restart overlapping the old one -- each take different rows
// rather than both sending the same message. The lease is written in the same
// statement, so there is no instant at which a claimed row looks unclaimed.
func (n *Notifications) Claim(ctx context.Context, now time.Time, lease time.Duration, limit int) ([]notify.Delivery, error) {
	ctx, cancel := context.WithTimeout(ctx, notifyTimeout)
	defer cancel()
	rows, err := n.db.Pool().Query(ctx, `
		WITH due AS (
			SELECT id FROM notification_outbox
			WHERE next_attempt_at <= $1
			ORDER BY next_attempt_at
			LIMIT $3
			FOR UPDATE SKIP LOCKED
		), claimed AS (
			UPDATE notification_outbox o
			   SET next_attempt_at = $2, attempts = o.attempts + 1
			  FROM due WHERE o.id = due.id
			RETURNING o.id, o.notification_id, o.subscription_id, o.attempts
		)
		SELECT c.id, c.attempts,
		       nt.id, nt.username, nt.kind, nt.dedupe_key, nt.title, nt.body, nt.url,
		       nt.tag, nt.urgency, nt.created_at, nt.expires_at, nt.quiet,
		       s.id, s.username, s.endpoint, s.p256dh, s.auth, s.origin, s.label
		  FROM claimed c
		  JOIN notifications nt     ON nt.id = c.notification_id
		  JOIN push_subscriptions s ON s.id = c.subscription_id`,
		now, now.Add(lease), limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []notify.Delivery
	for rows.Next() {
		var d notify.Delivery
		nt, s := &d.Notification, &d.Subscription
		if err := rows.Scan(&d.ID, &d.Attempts,
			&nt.ID, &nt.Username, &nt.Kind, &nt.DedupeKey, &nt.Title, &nt.Body, &nt.URL,
			&nt.Tag, &nt.Urgency, &nt.CreatedAt, &nt.ExpiresAt, &nt.Quiet,
			&s.ID, &s.Username, &s.Endpoint, &s.P256dh, &s.Auth, &s.Origin, &s.Label); err != nil {
			return nil, err
		}
		out = append(out, d)
	}
	return out, rows.Err()
}

// Delivered implements notify.Store: the row is deleted.
func (n *Notifications) Delivered(ctx context.Context, d notify.Delivery, status int, now time.Time) error {
	ctx, cancel := context.WithTimeout(ctx, notifyTimeout)
	defer cancel()
	tx, err := n.db.Pool().Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	if _, err := tx.Exec(ctx, `DELETE FROM notification_outbox WHERE id = $1`, d.ID); err != nil {
		return err
	}
	if _, err := tx.Exec(ctx, `
		UPDATE push_subscriptions
		   SET last_success_at = $2, last_status = $3, failures = 0, last_error = ''
		 WHERE id = $1`, d.Subscription.ID, now, status); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

// Reschedule implements notify.Store.
func (n *Notifications) Reschedule(ctx context.Context, d notify.Delivery, at time.Time, status int, reason string, now time.Time) error {
	ctx, cancel := context.WithTimeout(ctx, notifyTimeout)
	defer cancel()
	reason = clip(reason, 300)
	tx, err := n.db.Pool().Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	if _, err := tx.Exec(ctx, `
		UPDATE notification_outbox
		   SET next_attempt_at = $2, last_status = $3, last_error = $4
		 WHERE id = $1`, d.ID, at, nullStatus(status), reason); err != nil {
		return err
	}
	if err := failure(ctx, tx, d.Subscription.ID, status, reason, now); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

// Kill implements notify.Store: moved to the dead letters.
func (n *Notifications) Kill(ctx context.Context, d notify.Delivery, status int, reason string, now time.Time) error {
	ctx, cancel := context.WithTimeout(ctx, notifyTimeout)
	defer cancel()
	reason = clip(reason, 300)
	tx, err := n.db.Pool().Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	if _, err := tx.Exec(ctx, `
		INSERT INTO notification_dead
			(notification_id, subscription_id, username, endpoint_host, attempts, last_status, reason, died_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8)`,
		d.Notification.ID, d.Subscription.ID, d.Notification.Username,
		endpointHost(d.Subscription.Endpoint), d.Attempts, nullStatus(status), reason, now); err != nil {
		return err
	}
	if _, err := tx.Exec(ctx, `DELETE FROM notification_outbox WHERE id = $1`, d.ID); err != nil {
		return err
	}
	if err := failure(ctx, tx, d.Subscription.ID, status, reason, now); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

// DropSubscription implements notify.Store: the device is gone, and so is
// everything queued for it. What was queued is recorded as dead first, so the
// history says why those messages never arrived.
func (n *Notifications) DropSubscription(ctx context.Context, id int64, reason string) error {
	ctx, cancel := context.WithTimeout(ctx, notifyTimeout)
	defer cancel()
	reason = clip("device gone: "+reason, 300)
	tx, err := n.db.Pool().Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	if _, err := tx.Exec(ctx, `
		INSERT INTO notification_dead
			(notification_id, subscription_id, username, endpoint_host, attempts, last_status, reason)
		SELECT o.notification_id, o.subscription_id, s.username,
		       substring(s.endpoint from '^https?://([^/:]+)'), o.attempts, o.last_status, $2
		  FROM notification_outbox o JOIN push_subscriptions s ON s.id = o.subscription_id
		 WHERE o.subscription_id = $1`, id, reason); err != nil {
		return err
	}
	if _, err := tx.Exec(ctx, `DELETE FROM push_subscriptions WHERE id = $1`, id); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

func failure(ctx context.Context, tx pgx.Tx, subID int64, status int, reason string, now time.Time) error {
	_, err := tx.Exec(ctx, `
		UPDATE push_subscriptions
		   SET last_failure_at = $2, last_status = $3, last_error = $4, failures = failures + 1
		 WHERE id = $1`, subID, now, nullStatus(status), reason)
	return err
}

// ---------------------------------------------------------------- watchers

// MarkSeen records keys as seen for username and returns the ones that were
// NOT seen before -- the new events. One statement for the whole list.
func (n *Notifications) MarkSeen(ctx context.Context, username string, keys []string) ([]string, error) {
	if len(keys) == 0 {
		return nil, nil
	}
	ctx, cancel := context.WithTimeout(ctx, notifyTimeout)
	defer cancel()
	rows, err := n.db.Pool().Query(ctx, `
		INSERT INTO notification_seen (username, dedupe_key)
		SELECT $1, k FROM unnest($2::text[]) AS k
		ON CONFLICT DO NOTHING
		RETURNING dedupe_key`, username, keys)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var fresh []string
	for rows.Next() {
		var k string
		if err := rows.Scan(&k); err != nil {
			return nil, err
		}
		fresh = append(fresh, k)
	}
	return fresh, rows.Err()
}

// Primed reports whether source has made its silent first pass for username,
// and records that it now has. The first call for a pair returns false.
func (n *Notifications) Primed(ctx context.Context, username, source string) (bool, error) {
	ctx, cancel := context.WithTimeout(ctx, notifyTimeout)
	defer cancel()
	tag, err := n.db.Pool().Exec(ctx, `
		INSERT INTO notification_primed (username, source) VALUES ($1, $2)
		ON CONFLICT DO NOTHING`, username, source)
	if err != nil {
		return false, err
	}
	// A row inserted means it was not primed before this call.
	return tag.RowsAffected() == 0, nil
}

// ---------------------------------------------------------------- reading

// Recent is username's latest notifications, for the in-app list.
func (n *Notifications) Recent(ctx context.Context, username string, limit int) ([]notify.Notification, error) {
	ctx, cancel := context.WithTimeout(ctx, notifyTimeout)
	defer cancel()
	if limit <= 0 || limit > 200 {
		limit = 50
	}
	rows, err := n.db.Pool().Query(ctx, `
		SELECT id, username, kind, dedupe_key, title, body, url, tag, urgency, created_at, expires_at, quiet
		  FROM notifications WHERE username = $1 AND kind <> 'chat'
		 ORDER BY created_at DESC LIMIT $2`, username, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []notify.Notification
	for rows.Next() {
		var nt notify.Notification
		if err := rows.Scan(&nt.ID, &nt.Username, &nt.Kind, &nt.DedupeKey, &nt.Title, &nt.Body,
			&nt.URL, &nt.Tag, &nt.Urgency, &nt.CreatedAt, &nt.ExpiresAt, &nt.Quiet); err != nil {
			return nil, err
		}
		out = append(out, nt)
	}
	return out, rows.Err()
}

// OutboxStats is the queue as an administrator sees it.
type OutboxStats struct {
	Pending       int       `json:"pending"`
	DueNow        int       `json:"due_now"`
	OldestPending time.Time `json:"oldest_pending,omitzero"`
	Dead24h       int       `json:"dead_24h"`
	Devices       int       `json:"devices"`
	Users         int       `json:"users"`
	// ByService counts devices per push service -- how many iPhones, how
	// many Edge windows -- which is the first question when one platform
	// stops receiving.
	ByService map[string]int `json:"by_service"`
}

// Stats reports the queue's size and health.
func (n *Notifications) Stats(ctx context.Context, now time.Time) (OutboxStats, error) {
	ctx, cancel := context.WithTimeout(ctx, notifyTimeout)
	defer cancel()
	var st OutboxStats
	var oldest *time.Time
	err := n.db.Pool().QueryRow(ctx, `
		SELECT
		  (SELECT count(*) FROM notification_outbox),
		  (SELECT count(*) FROM notification_outbox WHERE next_attempt_at <= $1),
		  (SELECT min(created_at) FROM notification_outbox),
		  (SELECT count(*) FROM notification_dead WHERE died_at > $1::timestamptz - interval '24 hours'),
		  (SELECT count(*) FROM push_subscriptions),
		  (SELECT count(DISTINCT username) FROM push_subscriptions)`, now,
	).Scan(&st.Pending, &st.DueNow, &oldest, &st.Dead24h, &st.Devices, &st.Users)
	if err != nil {
		return st, err
	}
	if oldest != nil {
		st.OldestPending = *oldest
	}
	rows, err := n.db.Pool().Query(ctx, `
		SELECT coalesce(substring(endpoint from '^https?://([^/:]+)'), '?'), count(*)
		  FROM push_subscriptions GROUP BY 1`)
	if err != nil {
		return st, err
	}
	defer rows.Close()
	st.ByService = map[string]int{}
	for rows.Next() {
		var h string
		var c int
		if err := rows.Scan(&h, &c); err != nil {
			return st, err
		}
		st.ByService[h] = c
	}
	return st, rows.Err()
}

// Prune removes history past its usefulness. Called by the sender once a day.
func (n *Notifications) Prune(ctx context.Context, now time.Time) error {
	ctx, cancel := context.WithTimeout(ctx, notifyTimeout)
	defer cancel()
	for _, q := range []string{
		`DELETE FROM notification_dead WHERE died_at < $1::timestamptz - interval '30 days'`,
		`DELETE FROM notification_seen WHERE seen_at < $1::timestamptz - interval '120 days'`,
		`DELETE FROM notifications WHERE created_at < $1::timestamptz - interval '90 days'`,
		// A chat message's notification is worth nothing after two days.
		`DELETE FROM notifications WHERE kind = 'chat' AND created_at < $1::timestamptz - interval '2 days'`,
	} {
		if _, err := n.db.Pool().Exec(ctx, q, now); err != nil {
			return fmt.Errorf("pgstore: pruning notifications: %w", err)
		}
	}
	return nil
}

func nullStatus(s int) any {
	if s == 0 {
		return nil
	}
	return s
}

func clip(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n]
}

func endpointHost(endpoint string) string {
	u, err := url.Parse(endpoint)
	if err != nil {
		return ""
	}
	return u.Hostname()
}
