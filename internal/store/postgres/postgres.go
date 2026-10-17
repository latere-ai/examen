// SPDX-FileCopyrightText: 2026 Latere AI
// SPDX-License-Identifier: Apache-2.0

// Package postgres is the store over Postgres (specs/011-distribution.md).
//
// A transaction-mode pooler may sit in front of the database, so nothing
// here depends on session state: no LISTEN or NOTIFY, no session advisory
// lock, no named prepared statement. The pool describes the unnamed
// statement and caches the description in the client, unless the URL names
// another query mode.
//
// Two connections are named apart. Migrate takes the direct URL, because
// the migrator holds a session lock across its statements, so two runs at
// once apply each migration once. Open takes the serving URL, which may be
// a pooler's, and never migrates: it checks the schema.
package postgres

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"net/url"
	"strings"
	"time"

	_ "github.com/golang-migrate/migrate/v4/database/pgx/v5" // the pgx5:// driver pgxmigrate selects by scheme
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"

	"latere.ai/x/pkg/pgxmigrate"

	"latere.ai/x/examen/internal/store"
	"latere.ai/x/examen/internal/store/migrations"
)

// Pool bounds.
const (
	// DefaultMaxConns is the pool a zero Options.MaxConns takes; a URL
	// that names pool_max_conns keeps its own.
	DefaultMaxConns = 8
	// readyTimeout bounds the readiness check's statements.
	readyTimeout = time.Second

	maxConnLifetime   = 30 * time.Minute
	maxConnIdleTime   = time.Minute
	healthCheckPeriod = time.Minute
)

// The errors of the schema check. None names any part of a URL, which
// holds a password.
var (
	// ErrUnreachable is a database that did not answer.
	ErrUnreachable = errors.New("store: the database does not answer")
	// ErrBehind is a schema that lacks a migration this binary carries.
	ErrBehind = errors.New("store: the schema lacks a migration this binary carries; run examend migrate")
	// ErrDirty is a schema a failed migration left halfway.
	ErrDirty = errors.New("store: the schema is dirty: a migration failed halfway, and the way back is the backup taken before the upgrade")
	// ErrOtherMajor is a schema of another major release.
	ErrOtherMajor = errors.New("store: the schema belongs to another major release")
)

// Schema is what the database's schema holds.
type Schema struct {
	// Version is the newest migration applied, 0 for a database with none.
	Version uint
	// Dirty says a migration failed halfway.
	Dirty bool
	// Major is the major release examen_schema names, -1 when the table
	// is missing.
	Major int
	// Carried is the newest migration this binary carries.
	Carried uint
}

// Newer reports whether the schema holds migrations newer than the
// binary's, which within one major is a rollback the binary serves.
func (s Schema) Newer() bool { return s.Version > s.Carried }

// Check is the schema check of specs/011-distribution.md: a schema that
// lacks a migration this binary carries, a dirty one and one of another
// major are each refused; a newer one of the same major is served.
func (s Schema) Check() error {
	switch {
	case s.Dirty:
		return fmt.Errorf("%w (version %d)", ErrDirty, s.Version)
	case s.Version < s.Carried:
		return fmt.Errorf("%w (the schema is at version %d and this binary carries %d)", ErrBehind, s.Version, s.Carried)
	case s.Major != migrations.Major:
		return fmt.Errorf("%w (the schema is of major %d and this binary of %d)", ErrOtherMajor, s.Major, migrations.Major)
	}
	return nil
}

// querier is a pool, a connection or a transaction.
type querier interface {
	Exec(ctx context.Context, sql string, args ...any) (pgconn.CommandTag, error)
	Query(ctx context.Context, sql string, args ...any) (pgx.Rows, error)
	QueryRow(ctx context.Context, sql string, args ...any) pgx.Row
}

// ReadSchema reads what the database's schema holds, against the
// migrations this binary carries.
func ReadSchema(ctx context.Context, q querier) (Schema, error) {
	carried, err := migrations.Highest()
	if err != nil {
		return Schema{}, fmt.Errorf("store: %w", err)
	}
	return readSchema(ctx, q, carried)
}

// readSchema reads what the database's schema holds, against a binary
// that carries migrations up to carried.
func readSchema(ctx context.Context, q querier, carried uint) (Schema, error) {
	s := Schema{Major: -1, Carried: carried}
	var v int64
	err := q.QueryRow(ctx, `SELECT version, dirty FROM schema_migrations`).Scan(&v, &s.Dirty)
	switch {
	case errors.Is(err, pgx.ErrNoRows), undefinedTable(err):
		return s, nil // a database no migration has touched
	case err != nil:
		return Schema{}, ErrUnreachable
	}
	s.Version = uint(v)
	err = q.QueryRow(ctx, `SELECT major FROM examen_schema`).Scan(&s.Major)
	switch {
	case errors.Is(err, pgx.ErrNoRows), undefinedTable(err):
		s.Major = -1
	case err != nil:
		return Schema{}, ErrUnreachable
	}
	return s, nil
}

// undefinedTable reports a statement about a table that does not exist.
func undefinedTable(err error) bool {
	var pgErr *pgconn.PgError
	return errors.As(err, &pgErr) && pgErr.Code == "42P01"
}

// Migrated is what one run of Migrate found and left.
type Migrated struct {
	// From and To are the schema's version before and after the run; they
	// are equal when there was nothing to apply.
	From, To uint
}

// Migrate applies the migrations this binary carries that the database
// lacks, over the direct connection, under the migrator's session lock. A
// schema that holds every migration the binary carries, newer ones of the
// same major included, is left as it is. A dirty schema and one of another
// major are refused, and a migration that fails leaves the schema dirty,
// named in the error.
func Migrate(ctx context.Context, directURL string) (Migrated, error) {
	return migrate(ctx, directURL, migrations.FS)
}

// migrate is Migrate with the migration files given.
func migrate(ctx context.Context, directURL string, files fs.FS) (Migrated, error) {
	dsn, err := migrationURL(directURL)
	if err != nil {
		return Migrated{}, err
	}
	carried, err := migrations.HighestOf(files)
	if err != nil {
		return Migrated{}, fmt.Errorf("store: %w", err)
	}
	before, err := schemaAt(ctx, directURL, carried)
	if err != nil {
		return Migrated{}, err
	}
	switch {
	case before.Dirty:
		return Migrated{}, fmt.Errorf("%w (version %d)", ErrDirty, before.Version)
	case before.Version > 0 && before.Major != migrations.Major:
		return Migrated{}, fmt.Errorf("%w (the schema is of major %d and this binary of %d)", ErrOtherMajor, before.Major, migrations.Major)
	case before.Newer():
		return Migrated{From: before.Version, To: before.Version}, nil
	}
	if err := pgxmigrate.Up(dsn, files, "."); err != nil {
		after, readErr := schemaAt(ctx, directURL, carried)
		if readErr == nil && after.Dirty {
			return Migrated{}, fmt.Errorf("store: migration %d failed and left the schema dirty: %w", after.Version, err)
		}
		return Migrated{}, fmt.Errorf("store: applying the migrations: %w", err)
	}
	after, err := schemaAt(ctx, directURL, carried)
	if err != nil {
		return Migrated{}, err
	}
	return Migrated{From: before.Version, To: after.Version}, nil
}

// schemaAt reads the schema over a connection of its own.
func schemaAt(ctx context.Context, dsn string, carried uint) (Schema, error) {
	conn, err := pgx.Connect(ctx, dsn)
	if err != nil {
		return Schema{}, ErrUnreachable
	}
	s, err := readSchema(ctx, conn, carried)
	if closeErr := conn.Close(ctx); closeErr != nil && err == nil {
		err = fmt.Errorf("store: closing the schema check's connection: %w", closeErr)
	}
	return s, err
}

// migrationURL is the direct URL under the scheme golang-migrate's pgx/v5
// driver registers. The errors name no part of the URL.
func migrationURL(raw string) (string, error) {
	u, err := url.Parse(raw)
	if err != nil {
		return "", errors.New("store: the direct database URL is not a URL")
	}
	if u.Scheme != "postgres" && u.Scheme != "postgresql" {
		return "", errors.New("store: the direct database URL does not have the scheme postgres")
	}
	u.Scheme = "pgx5"
	return u.String(), nil
}

// Options opens the store.
type Options struct {
	// MaxConns bounds the pool when the URL names no pool_max_conns. Zero
	// takes DefaultMaxConns.
	MaxConns int32
	// Now stands in for the database's now(), for a test that moves the
	// store's clock. Nil in a running server: every statement then reads
	// the database's own clock, so no replica's clock is compared with
	// another's.
	Now func() time.Time
}

// Store is the store over one connection pool.
type Store struct {
	pool *pgxpool.Pool
	now  func() time.Time
}

var _ store.Store = (*Store)(nil)

// Open connects over the serving URL and checks the schema: one the check
// refuses is an error, and a newer one of the same major is answered in
// the Schema for the caller to warn about. It does not migrate.
func Open(ctx context.Context, servingURL string, o Options) (*Store, Schema, error) {
	cfg, err := pgxpool.ParseConfig(servingURL)
	if err != nil {
		// The parse error is not kept: it may quote the URL.
		return nil, Schema{}, errors.New("store: the serving database URL is not a Postgres URL")
	}
	// The driver's default prepares a named statement per query and keeps
	// it for the life of the connection, which a transaction-mode pooler
	// hands to another client between two statements. Describing the
	// unnamed statement keeps nothing on the server. A URL that names
	// another mode keeps it.
	if cfg.ConnConfig.DefaultQueryExecMode == pgx.QueryExecModeCacheStatement {
		cfg.ConnConfig.DefaultQueryExecMode = pgx.QueryExecModeCacheDescribe
	}
	if !strings.Contains(servingURL, "pool_max_conns") {
		cfg.MaxConns = DefaultMaxConns
		if o.MaxConns > 0 {
			cfg.MaxConns = o.MaxConns
		}
	}
	cfg.MinConns = 0
	cfg.MaxConnLifetime = maxConnLifetime
	cfg.MaxConnIdleTime = maxConnIdleTime
	cfg.HealthCheckPeriod = healthCheckPeriod
	pool, err := pgxpool.NewWithConfig(ctx, cfg)
	if err != nil {
		return nil, Schema{}, ErrUnreachable
	}
	s, err := ReadSchema(ctx, pool)
	if err == nil {
		err = s.Check()
	}
	if err != nil {
		pool.Close()
		return nil, Schema{}, err
	}
	return &Store{pool: pool, now: o.Now}, s, nil
}

// Close closes the pool.
func (s *Store) Close() error {
	s.pool.Close()
	return nil
}

// Ready is the database check of /readyz: the pool answers within a
// second, and the schema check holds.
func (s *Store) Ready(ctx context.Context) error {
	ctx, cancel := context.WithTimeout(ctx, readyTimeout)
	defer cancel()
	schema, err := ReadSchema(ctx, s.pool)
	if err != nil {
		return err
	}
	return schema.Check()
}

// Idempotency is the table of idempotency keys.
func (s *Store) Idempotency() store.Idempotency { return idempotency{s} }

// at is a test's clock as a statement's parameter, and NULL in a running
// server, where the statement reads the database's now().
func (s *Store) at() *time.Time {
	if s.now == nil {
		return nil
	}
	t := s.now()
	return &t
}
