package chat

import (
	"context"
	"errors"
	"time"

	"github.com/jackc/pgx/v5"
)

// Secret chats in PostgreSQL (migration 060_secret_chats.sql). The rules are
// in secret.go; these only store, atomically.

type rowQuerier interface {
	QueryRow(ctx context.Context, sql string, args ...any) pgx.Row
}

// putItem adds one frame to a device's mailbox. A resend with the same client
// id finds the row already there (created=false) while it still waits.
func putItem(ctx context.Context, q rowQuerier, it *SecretItem) (bool, error) {
	err := q.QueryRow(ctx, `
		INSERT INTO secret_queue (chat_id, to_user, to_device, from_user, from_device, kind, client_msg_id, body)
		VALUES ($1, $2, NULLIF($3, ''), $4, $5, $6, NULLIF($7, '')::uuid, $8)
		ON CONFLICT (chat_id, from_user, client_msg_id) WHERE client_msg_id IS NOT NULL DO NOTHING
		RETURNING id, created_at`,
		it.ChatID, it.ToUser, it.ToDevice, it.From, it.FromDevice, it.Kind, it.ClientID, it.Body).Scan(&it.ID, &it.At)
	if errors.Is(err, pgx.ErrNoRows) {
		err = q.QueryRow(ctx, `SELECT id, created_at FROM secret_queue WHERE chat_id = $1 AND from_user = $2 AND client_msg_id = $3::uuid`,
			it.ChatID, it.From, it.ClientID).Scan(&it.ID, &it.At)
		return false, err
	}
	return err == nil, err
}

// SecretCreate makes the chat, its two members, the record, the offered keys
// and the invitation, in one transaction. It fills in sc.ChatID.
func (s *Store) SecretCreate(ctx context.Context, sc *SecretChat, keys []SecretKey, fps []string, req *SecretItem) error {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	if err := tx.QueryRow(ctx, `INSERT INTO chats (type, created_by, last_message_at) VALUES ('secret', $1, now()) RETURNING id`, sc.Initiator).Scan(&sc.ChatID); err != nil {
		return err
	}
	for _, u := range []string{sc.Initiator, sc.Responder} {
		if _, err := tx.Exec(ctx, `INSERT INTO chat_users_ext (user_id) VALUES ($1) ON CONFLICT DO NOTHING`, u); err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `INSERT INTO chat_members (chat_id, user_id, role) VALUES ($1, $2, 'member')`, sc.ChatID, u); err != nil {
			return err
		}
	}
	if _, err := tx.Exec(ctx, `INSERT INTO secret_chats (chat_id, initiator, initiator_device, responder) VALUES ($1, $2, $3, $4)`,
		sc.ChatID, sc.Initiator, sc.InitiatorDevice, sc.Responder); err != nil {
		return err
	}
	for i, k := range keys {
		if _, err := tx.Exec(ctx, `INSERT INTO secret_chat_keys (chat_id, device_id, alg, public_key, fingerprint) VALUES ($1, $2, $3, $4, $5)`,
			sc.ChatID, sc.InitiatorDevice, k.Alg, k.Key, fps[i]); err != nil {
			return err
		}
	}
	req.ChatID = sc.ChatID
	if _, err := putItem(ctx, tx, req); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

// SecretGet reads one secret chat; a chat that is not secret is ErrForbidden.
func (s *Store) SecretGet(ctx context.Context, chatID int64) (*SecretChat, error) {
	sc := &SecretChat{}
	err := s.pool.QueryRow(ctx, `
		SELECT sc.chat_id, sc.initiator, sc.initiator_device, sc.responder, COALESCE(sc.responder_device, ''),
		       COALESCE(sc.alg, ''), sc.state,
		       ARRAY(SELECT k.alg FROM secret_chat_keys k WHERE k.chat_id = sc.chat_id AND k.device_id = sc.initiator_device ORDER BY k.alg)
		FROM secret_chats sc WHERE sc.chat_id = $1`, chatID).
		Scan(&sc.ChatID, &sc.Initiator, &sc.InitiatorDevice, &sc.Responder, &sc.ResponderDevice, &sc.Alg, &sc.State, &sc.Offered)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrForbidden
	}
	return sc, err
}

// SecretBind ties the chat to the accepting device. Only the first device to
// accept wins; a second gets ErrSecretState.
func (s *Store) SecretBind(ctx context.Context, sc *SecretChat, key SecretKey, fp string, acc *SecretItem) error {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	tag, err := tx.Exec(ctx, `
		UPDATE secret_chats SET responder_device = $2, alg = $3, state = 'active', accepted_at = now()
		WHERE chat_id = $1 AND state = 'requested' AND responder_device IS NULL`, sc.ChatID, sc.ResponderDevice, key.Alg)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return ErrSecretState
	}
	if _, err := tx.Exec(ctx, `INSERT INTO secret_chat_keys (chat_id, device_id, alg, public_key, fingerprint) VALUES ($1, $2, $3, $4, $5)`,
		sc.ChatID, sc.ResponderDevice, key.Alg, key.Key, fp); err != nil {
		return err
	}
	if _, err := tx.Exec(ctx, `DELETE FROM secret_chat_keys WHERE chat_id = $1 AND device_id = $2 AND alg <> $3`, sc.ChatID, sc.InitiatorDevice, key.Alg); err != nil {
		return err
	}
	if _, err := tx.Exec(ctx, `DELETE FROM secret_queue WHERE chat_id = $1 AND kind = 'request'`, sc.ChatID); err != nil {
		return err
	}
	if _, err := putItem(ctx, tx, acc); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

// SecretPut queues one frame and moves the chat up its members' lists.
func (s *Store) SecretPut(ctx context.Context, it *SecretItem) (bool, error) {
	created, err := putItem(ctx, s.pool, it)
	if err == nil && created {
		_, err = s.pool.Exec(ctx, `UPDATE chats SET last_message_at = now() WHERE id = $1`, it.ChatID)
	}
	return created, err
}

// SecretPending is what waits for this device (and invitations for any of
// user's devices), oldest first, in chats user is still in. Frames older than
// before are dropped first.
func (s *Store) SecretPending(ctx context.Context, user, device string, before time.Time) ([]*SecretItem, error) {
	if _, err := s.pool.Exec(ctx, `DELETE FROM secret_queue WHERE created_at < $1`, before); err != nil {
		return nil, err
	}
	rows, err := s.pool.Query(ctx, `
		SELECT q.id, q.chat_id, q.kind, COALESCE(q.from_user, ''), COALESCE(q.client_msg_id::text, ''), q.body, q.created_at
		FROM secret_queue q
		JOIN chat_members m ON m.chat_id = q.chat_id AND m.user_id = q.to_user AND m.role NOT IN ('left', 'banned')
		WHERE q.to_user = $1 AND (q.to_device = $2 OR q.to_device IS NULL)
		ORDER BY q.id LIMIT 1000`, user, device)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []*SecretItem
	for rows.Next() {
		it := &SecretItem{ToUser: user}
		if err := rows.Scan(&it.ID, &it.ChatID, &it.Kind, &it.From, &it.ClientID, &it.Body, &it.At); err != nil {
			return nil, err
		}
		out = append(out, it)
	}
	return out, rows.Err()
}

// SecretAck deletes what the device confirmed: its own frames, and a "close"
// sent to any device of user. It returns the chats those closes ended.
func (s *Store) SecretAck(ctx context.Context, user, device string, ids []int64) ([]int64, error) {
	if len(ids) == 0 {
		return nil, nil
	}
	rows, err := s.pool.Query(ctx, `
		DELETE FROM secret_queue WHERE to_user = $1 AND id = ANY($3)
		  AND (to_device = $2 OR (to_device IS NULL AND kind = 'close'))
		RETURNING chat_id, kind`, user, device, ids)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var closed []int64
	for rows.Next() {
		var id int64
		var kind string
		if err := rows.Scan(&id, &kind); err != nil {
			return nil, err
		}
		if kind == secretClose {
			closed = append(closed, id)
		}
	}
	return closed, rows.Err()
}

// SecretEnd closes the chat: keys and waiting frames go, the other side gets
// note, and user leaves the chat.
func (s *Store) SecretEnd(ctx context.Context, chatID int64, user string, note *SecretItem) error {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	tag, err := tx.Exec(ctx, `UPDATE secret_chats SET state = 'closed', closed_at = now() WHERE chat_id = $1 AND state <> 'closed'`, chatID)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return ErrSecretState
	}
	for _, q := range []string{`DELETE FROM secret_chat_keys WHERE chat_id = $1`, `DELETE FROM secret_queue WHERE chat_id = $1`} {
		if _, err := tx.Exec(ctx, q, chatID); err != nil {
			return err
		}
	}
	if _, err := putItem(ctx, tx, note); err != nil {
		return err
	}
	if _, err := tx.Exec(ctx, `UPDATE chat_members SET role = 'left' WHERE chat_id = $1 AND user_id = $2`, chatID, user); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

// SecretLeave takes a closed chat off user's list; the chat row goes with the
// last member.
func (s *Store) SecretLeave(ctx context.Context, chatID int64, user string) error {
	if _, err := s.pool.Exec(ctx, `UPDATE chat_members SET role = 'left' WHERE chat_id = $1 AND user_id = $2`, chatID, user); err != nil {
		return err
	}
	_, err := s.pool.Exec(ctx, `
		DELETE FROM chats c WHERE c.id = $1 AND c.type = 'secret'
		  AND EXISTS (SELECT 1 FROM secret_chats sc WHERE sc.chat_id = c.id AND sc.state = 'closed')
		  AND NOT EXISTS (SELECT 1 FROM chat_members m WHERE m.chat_id = c.id AND m.role NOT IN ('left', 'banned'))`, chatID)
	return err
}

// SecretLive lists the open secret chats this device holds, and invitations
// any of user's devices may still take.
func (s *Store) SecretLive(ctx context.Context, user, device string) ([]int64, error) {
	rows, err := s.pool.Query(ctx, `
		SELECT chat_id FROM secret_chats
		WHERE state <> 'closed' AND (
		      (initiator = $1 AND initiator_device = $2)
		   OR (responder = $1 AND (responder_device = $2 OR (responder_device IS NULL AND state = 'requested'))))
		ORDER BY chat_id`, user, device)
	if err != nil {
		return nil, err
	}
	return pgx.CollectRows(rows, pgx.RowTo[int64])
}
