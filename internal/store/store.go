// SPDX-FileCopyrightText: 2026 Latere AI
// SPDX-License-Identifier: Apache-2.0

// Package store is the contract of what Examen keeps in Postgres
// (specs/011-distribution.md): every table behind one interface, with the
// Postgres adapter in internal/store/postgres and the migrations it applies
// in internal/store/migrations. A handler depends on this package alone and
// cannot tell which database serves it.
//
// Nothing here depends on session state, so the store serves through a
// pooler in transaction mode: no session advisory lock, no LISTEN, and no
// state kept on a connection between transactions.
package store

import (
	"context"
	"errors"
	"time"
)

// The errors an adapter answers with. A handler maps each to the error of
// its route; none carries a value of the row.
var (
	// ErrNotFound is a row that does not exist.
	ErrNotFound = errors.New("store: not found")
)

// Store is the one store a server runs on.
type Store interface {
	// Idempotency is the table of idempotency keys (specs/003-api.md).
	Idempotency() Idempotency
	// Ready answers whether the store can serve: the database answers and
	// its schema holds every migration this binary carries.
	Ready(ctx context.Context) error
	// Close releases what the store holds.
	Close() error
}

// IdempotencyKey is one key a subject sent with a request, and, once the
// request was answered, the answer it replays.
type IdempotencyKey struct {
	Subject string
	Key     string
	// Route is the method and the path the key was first sent to.
	Route string
	// BodyDigest is the digest of the canonical JSON of the first body, so
	// a repeat with the same content in another spelling is the same
	// request.
	BodyDigest string
	// Done is false while the first request is still being answered.
	Done        bool
	Status      int
	ContentType string
	Body        []byte
	CreatedAt   time.Time
	ExpiresAt   time.Time
}

// Idempotency keeps the idempotency keys. A key is one subject's, whatever
// route it is sent to, so a key reused on another route is found and
// refused rather than starting a second request.
type Idempotency interface {
	// Begin reserves the key for reserve and reports true, or returns the
	// record already held and unexpired and false. An expired record, the
	// answer of a window that ended or a reservation whose request never
	// finished, is replaced. Replicas that race on one key reserve it once.
	Begin(ctx context.Context, k IdempotencyKey, reserve time.Duration) (IdempotencyKey, bool, error)
	// Finish stores the answer of a reserved key, kept for window from the
	// key's first use: ErrNotFound when the key is not reserved.
	Finish(ctx context.Context, k IdempotencyKey, window time.Duration) error
	// Abandon drops a reserved key whose request failed, so a retry runs
	// again.
	Abandon(ctx context.Context, subject, key string) error
	// Prune deletes up to limit expired keys and says how many.
	Prune(ctx context.Context, limit int) (int, error)
}
