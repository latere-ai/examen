// SPDX-FileCopyrightText: 2026 Latere AI
// SPDX-License-Identifier: Apache-2.0

package postgres

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"

	"latere.ai/x/examen/internal/store"
)

// idempotency is the table of idempotency keys.
type idempotency struct{ s *Store }

const idempotencyColumns = `subject, key, route, body_digest, done, status, content_type, body, created_at, expires_at`

// scanKey reads a row of idempotencyColumns, and any further columns into
// extra.
func scanKey(row pgx.Row, extra ...any) (store.IdempotencyKey, error) {
	var k store.IdempotencyKey
	dest := append([]any{&k.Subject, &k.Key, &k.Route, &k.BodyDigest, &k.Done, &k.Status, &k.ContentType, &k.Body, &k.CreatedAt, &k.ExpiresAt}, extra...)
	if err := row.Scan(dest...); err != nil {
		return store.IdempotencyKey{}, err
	}
	k.CreatedAt, k.ExpiresAt = k.CreatedAt.UTC(), k.ExpiresAt.UTC()
	return k, nil
}

// reserveAttempts bounds how often Begin inserts again after the held
// record it lost the insert to was abandoned before it could read it.
const reserveAttempts = 3

// A duration is bound as whole milliseconds and made an interval in the
// statement, so its encoding does not depend on whether the pooler in
// front lets the driver describe the statement.
const (
	insertKey = `INSERT INTO idempotency_keys (subject, key, route, body_digest, created_at, expires_at)
		VALUES ($1, $2, $3, $4, coalesce($5::timestamptz, now()), coalesce($5::timestamptz, now()) + $6::bigint * interval '1 millisecond')
		ON CONFLICT (subject, key) DO NOTHING
		RETURNING ` + idempotencyColumns
	heldKey = `SELECT ` + idempotencyColumns + `, expires_at > coalesce($3::timestamptz, now())
		FROM idempotency_keys WHERE subject = $1 AND key = $2 FOR UPDATE`
	replaceKey = `UPDATE idempotency_keys SET route = $3, body_digest = $4, done = false, status = 0, content_type = '', body = NULL,
		created_at = coalesce($5::timestamptz, now()), expires_at = coalesce($5::timestamptz, now()) + $6::bigint * interval '1 millisecond'
		WHERE subject = $1 AND key = $2
		RETURNING ` + idempotencyColumns
)

// Begin reserves the key in one transaction: an insert that does nothing
// when the key is held, then a read of the held record under its row lock,
// replaced when it has expired. Replicas that race on one key reserve it
// once. Every time is the database's, or the test clock's.
func (t idempotency) Begin(ctx context.Context, k store.IdempotencyKey, reserve time.Duration) (store.IdempotencyKey, bool, error) {
	var out store.IdempotencyKey
	var fresh bool
	at, ms := t.s.at(), reserve.Milliseconds()
	err := pgx.BeginFunc(ctx, t.s.pool, func(tx pgx.Tx) error {
		for range reserveAttempts {
			made, err := scanKey(tx.QueryRow(ctx, insertKey, k.Subject, k.Key, k.Route, k.BodyDigest, at, ms))
			if err == nil {
				out, fresh = made, true
				return nil
			}
			if !errors.Is(err, pgx.ErrNoRows) {
				return fmt.Errorf("postgres: reserving an idempotency key: %w", err)
			}
			var live bool
			held, err := scanKey(tx.QueryRow(ctx, heldKey, k.Subject, k.Key, at), &live)
			if errors.Is(err, pgx.ErrNoRows) {
				continue
			}
			if err != nil {
				return fmt.Errorf("postgres: reading an idempotency key: %w", err)
			}
			if live {
				out = held
				return nil
			}
			replaced, err := scanKey(tx.QueryRow(ctx, replaceKey, k.Subject, k.Key, k.Route, k.BodyDigest, at, ms))
			if err != nil {
				return fmt.Errorf("postgres: replacing an expired idempotency key: %w", err)
			}
			out, fresh = replaced, true
			return nil
		}
		return fmt.Errorf("postgres: the idempotency key was abandoned %d times while it was reserved", reserveAttempts)
	})
	if err != nil {
		return store.IdempotencyKey{}, false, err
	}
	return out, fresh, nil
}

// Finish stores the answer of a reserved key and keeps it for window from
// the key's first use.
func (t idempotency) Finish(ctx context.Context, k store.IdempotencyKey, window time.Duration) error {
	tag, err := t.s.pool.Exec(ctx, `UPDATE idempotency_keys SET done = true, status = $3, content_type = $4, body = $5,
		expires_at = created_at + $6::bigint * interval '1 millisecond'
		WHERE subject = $1 AND key = $2 AND NOT done`,
		k.Subject, k.Key, k.Status, k.ContentType, k.Body, window.Milliseconds())
	if err != nil {
		return fmt.Errorf("postgres: finishing an idempotency key: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return fmt.Errorf("%w: no reserved idempotency key", store.ErrNotFound)
	}
	return nil
}

// Abandon drops a reserved key, and leaves a finished one as it is.
func (t idempotency) Abandon(ctx context.Context, subject, key string) error {
	if _, err := t.s.pool.Exec(ctx, `DELETE FROM idempotency_keys WHERE subject = $1 AND key = $2 AND NOT done`, subject, key); err != nil {
		return fmt.Errorf("postgres: abandoning an idempotency key: %w", err)
	}
	return nil
}

// Prune deletes up to limit expired keys, oldest first, skipping rows
// another replica holds.
func (t idempotency) Prune(ctx context.Context, limit int) (int, error) {
	tag, err := t.s.pool.Exec(ctx, `DELETE FROM idempotency_keys WHERE ctid IN (
		SELECT ctid FROM idempotency_keys WHERE expires_at <= coalesce($1::timestamptz, now())
		ORDER BY expires_at LIMIT $2 FOR UPDATE SKIP LOCKED)`, t.s.at(), limit)
	if err != nil {
		return 0, fmt.Errorf("postgres: pruning the idempotency keys: %w", err)
	}
	return int(tag.RowsAffected()), nil
}
