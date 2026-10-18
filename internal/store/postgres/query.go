// SPDX-FileCopyrightText: 2026 Latere AI
// SPDX-License-Identifier: Apache-2.0

package postgres

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"

	"latere.ai/x/examen/internal/store"
)

// where builds the condition and the arguments of a statement, numbering
// each argument as it is added.
type where struct {
	conds []string
	args  []any
}

// arg adds an argument and answers its placeholder.
func (w *where) arg(v any) string {
	w.args = append(w.args, v)
	return "$" + strconv.Itoa(len(w.args))
}

// add adds a condition, with %s in it standing for each argument in turn.
func (w *where) add(cond string, args ...any) {
	ph := make([]any, len(args))
	for i, a := range args {
		ph[i] = w.arg(a)
	}
	w.conds = append(w.conds, fmt.Sprintf(cond, ph...))
}

// sql is the WHERE clause, or nothing when there is no condition.
func (w *where) sql() string {
	if len(w.conds) == 0 {
		return ""
	}
	return " WHERE " + strings.Join(w.conds, " AND ")
}

// page adds a page's resume point on a column ordered newest first, and
// answers the ORDER BY and LIMIT that follow the condition.
func (w *where) page(column string, p store.Page) string {
	if p.After != "" {
		w.add(column+" < %s", p.After)
	}
	return " ORDER BY " + column + " DESC LIMIT " + w.arg(p.Limit+1)
}

// now is the time a transaction acts at: the test clock, or the
// database's own, which is the transaction's start for every statement in
// it.
func (s *Store) txNow(ctx context.Context, tx pgx.Tx) (time.Time, error) {
	var now time.Time
	if err := tx.QueryRow(ctx, `SELECT coalesce($1::timestamptz, now())`, s.at()).Scan(&now); err != nil {
		return time.Time{}, fmt.Errorf("postgres: reading the clock: %w", err)
	}
	return now.UTC(), nil
}

// one answers store.ErrNotFound for a row that does not exist.
func one(err error, what string) error {
	switch {
	case errors.Is(err, pgx.ErrNoRows):
		return fmt.Errorf("%w: %s", store.ErrNotFound, what)
	case err != nil:
		return fmt.Errorf("postgres: reading %s: %w", what, err)
	}
	return nil
}

// marshal renders a value as JSON for a jsonb column.
func marshal(v any) ([]byte, error) {
	b, err := json.Marshal(v)
	if err != nil {
		return nil, fmt.Errorf("postgres: rendering a column: %w", err)
	}
	return b, nil
}

// unmarshal reads a jsonb column, leaving v as it is for NULL.
func unmarshal(raw []byte, v any) error {
	if len(raw) == 0 || string(raw) == "null" {
		return nil
	}
	if err := json.Unmarshal(raw, v); err != nil {
		return fmt.Errorf("postgres: reading a column: %w", err)
	}
	return nil
}

// labels renders a filter's labels as the jsonb a containment test reads.
func labels(m map[string]string) ([]byte, error) { return marshal(m) }

// uniqueViolation reports a statement refused by a unique index.
func uniqueViolation(err error) bool {
	var pgErr *pgconn.PgError
	return errors.As(err, &pgErr) && pgErr.Code == "23505"
}

// lock takes a transaction's advisory lock on a key, which a pooler in
// transaction mode keeps, since it ends with the transaction.
func lock(ctx context.Context, tx pgx.Tx, key string) error {
	if _, err := tx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtextextended($1, 0))`, key); err != nil {
		return fmt.Errorf("postgres: taking a lock: %w", err)
	}
	return nil
}

// ptr answers a pointer to a copy of a non-zero time, and nil otherwise.
func ptr(t *time.Time) *time.Time {
	if t == nil || t.IsZero() {
		return nil
	}
	u := t.UTC()
	return &u
}
