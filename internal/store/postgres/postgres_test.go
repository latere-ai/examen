// SPDX-FileCopyrightText: 2026 Latere AI
// SPDX-License-Identifier: Apache-2.0

package postgres_test

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"strings"
	"sync"
	"testing"
	"testing/fstest"
	"time"

	"github.com/jackc/pgx/v5"

	"latere.ai/x/examen/internal/store"
	"latere.ai/x/examen/internal/store/migrations"
	"latere.ai/x/examen/internal/store/postgres"
	"latere.ai/x/examen/internal/store/postgres/pgtest"
)

func TestMain(m *testing.M) { pgtest.Main(m) }

// fresh is an empty database of its own, with no migration applied, and
// its direct and pooled URLs.
func fresh(t *testing.T) (direct, pooled string) {
	t.Helper()
	srv, err := pgtest.Start()
	if err != nil {
		t.Fatalf("no Postgres for the store's suite, and a skipped suite is no pass: %v", err)
	}
	name := fmt.Sprintf("examen_fresh_%d", time.Now().UnixNano())
	if err := pgtest.Exec(t.Context(), srv.Direct, "create database "+name); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		if err := pgtest.Exec(ctx, srv.Direct, "drop database if exists "+name+" with (force)"); err != nil {
			t.Errorf("dropping the database: %v", err)
		}
	})
	return pgtest.OnDatabase(t, srv.Direct, name), pgtest.OnDatabase(t, srv.Pooler, name)
}

// migrate against an empty database applies every migration; a second run
// applies none; two runs at once apply each migration once.
func TestMigrate(t *testing.T) {
	highest, err := migrations.Highest()
	if err != nil {
		t.Fatal(err)
	}
	direct, _ := fresh(t)
	got, err := postgres.Migrate(t.Context(), direct)
	if err != nil || got.From != 0 || got.To != highest {
		t.Fatalf("the first run: %+v %v", got, err)
	}
	if got, err = postgres.Migrate(t.Context(), direct); err != nil || got.From != highest || got.To != highest {
		t.Fatalf("the second run: %+v %v", got, err)
	}

	racing, _ := fresh(t)
	var wg sync.WaitGroup
	errs := make([]error, 4)
	for i := range errs {
		wg.Go(func() { _, errs[i] = postgres.Migrate(t.Context(), racing) })
	}
	wg.Wait()
	for _, err := range errs {
		if err != nil {
			t.Fatalf("a run at once with others: %v", err)
		}
	}
	s, _, err := postgres.Open(t.Context(), racing, postgres.Options{})
	if err != nil {
		t.Fatalf("after runs at once: %v", err)
	}
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
}

// serve refuses a schema that lacks a migration it carries, a dirty schema
// and a schema of another major, and serves a newer schema of its own
// major, saying so; migrate leaves a newer schema as it is and refuses a
// dirty one. No error names any part of the URL.
func TestTheSchemaCheck(t *testing.T) {
	ctx := t.Context()
	highest, err := migrations.Highest()
	if err != nil {
		t.Fatal(err)
	}
	open := func(dsn string) (postgres.Schema, error) {
		s, schema, err := postgres.Open(ctx, dsn, postgres.Options{})
		if err == nil {
			if cerr := s.Close(); cerr != nil {
				t.Fatal(cerr)
			}
		}
		if err != nil && strings.Contains(err.Error(), "examen@") {
			t.Fatalf("the error names the URL: %v", err)
		}
		return schema, err
	}

	empty, _ := fresh(t)
	if _, err := open(empty); !errors.Is(err, postgres.ErrBehind) || !strings.Contains(err.Error(), "run examend migrate") {
		t.Fatalf("an empty database: %v", err)
	}

	newer, _ := pgtest.Database(t)
	if err := pgtest.Exec(ctx, newer, `UPDATE schema_migrations SET version = $1`, int64(highest+1)); err != nil {
		t.Fatal(err)
	}
	schema, err := open(newer)
	if err != nil || !schema.Newer() || schema.Version != highest+1 || schema.Carried != highest {
		t.Fatalf("a newer schema of the same major: %+v %v", schema, err)
	}
	if got, err := postgres.Migrate(ctx, newer); err != nil || got.From != highest+1 || got.To != highest+1 {
		t.Fatalf("migrating a newer schema: %+v %v", got, err)
	}

	dirty, _ := pgtest.Database(t)
	if err := pgtest.Exec(ctx, dirty, `UPDATE schema_migrations SET dirty = true`); err != nil {
		t.Fatal(err)
	}
	if _, err := open(dirty); !errors.Is(err, postgres.ErrDirty) {
		t.Fatalf("a dirty schema: %v", err)
	}
	if _, err := postgres.Migrate(ctx, dirty); !errors.Is(err, postgres.ErrDirty) {
		t.Fatalf("migrating a dirty schema: %v", err)
	}

	other, _ := pgtest.Database(t)
	if err := pgtest.Exec(ctx, other, `UPDATE examen_schema SET major = $1`, migrations.Major+1); err != nil {
		t.Fatal(err)
	}
	if _, err := open(other); !errors.Is(err, postgres.ErrOtherMajor) {
		t.Fatalf("a schema of another major: %v", err)
	}
	if _, err := postgres.Migrate(ctx, other); !errors.Is(err, postgres.ErrOtherMajor) {
		t.Fatalf("migrating a schema of another major: %v", err)
	}

	nomajor, _ := pgtest.Database(t)
	if err := pgtest.Exec(ctx, nomajor, `DROP TABLE examen_schema`); err != nil {
		t.Fatal(err)
	}
	if schema, err := open(nomajor); !errors.Is(err, postgres.ErrOtherMajor) || schema.Major != 0 {
		t.Fatalf("a schema with no major: %+v %v", schema, err)
	}
	if err := pgtest.Exec(ctx, nomajor, `CREATE TABLE examen_schema (major integer)`); err != nil {
		t.Fatal(err)
	}
	if _, err := open(nomajor); !errors.Is(err, postgres.ErrOtherMajor) {
		t.Fatalf("a schema with an empty major table: %v", err)
	}
}

// What cannot be reached or read is named without the URL.
func TestUnreachable(t *testing.T) {
	ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
	defer cancel()
	const nowhere = "postgres://examen:secret-password@127.0.0.1:1/examen?sslmode=disable&connect_timeout=1"
	if _, err := postgres.Migrate(ctx, nowhere); !errors.Is(err, postgres.ErrUnreachable) {
		t.Fatalf("migrating nowhere: %v", err)
	}
	if _, _, err := postgres.Open(ctx, nowhere, postgres.Options{}); !errors.Is(err, postgres.ErrUnreachable) {
		t.Fatalf("opening nowhere: %v", err)
	}
	for _, bad := range []string{"mysql://examen@db/examen", "postgres://%zz", "::"} {
		if _, err := postgres.Migrate(ctx, bad); err == nil || strings.Contains(err.Error(), bad) {
			t.Errorf("migrating %q: %v", bad, err)
		}
	}
	if _, _, err := postgres.Open(ctx, "postgres://examen@db/examen?pool_max_conns=nope", postgres.Options{}); err == nil || strings.Contains(err.Error(), "nope") {
		t.Errorf("opening a URL that does not parse: %v", err)
	}
}

// The store answers ready while the database answers and the schema holds,
// and not once the schema is behind.
func TestReady(t *testing.T) {
	direct, _ := pgtest.Database(t)
	s := pgtest.Open(t, false, postgres.Options{MaxConns: 2})
	if err := s.Ready(t.Context()); err != nil {
		t.Fatal(err)
	}
	other, _, err := postgres.Open(t.Context(), direct, postgres.Options{})
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if err := other.Close(); err != nil {
			t.Error(err)
		}
	}()
	if err := pgtest.Exec(t.Context(), direct, `UPDATE schema_migrations SET version = 0`); err != nil {
		t.Fatal(err)
	}
	if err := other.Ready(t.Context()); !errors.Is(err, postgres.ErrBehind) {
		t.Fatalf("a schema gone behind: %v", err)
	}
	if err := pgtest.Exec(t.Context(), direct, `DROP TABLE schema_migrations`); err != nil {
		t.Fatal(err)
	}
	if err := other.Ready(t.Context()); !errors.Is(err, postgres.ErrBehind) {
		t.Fatalf("a schema gone: %v", err)
	}
	if err := other.Close(); err != nil {
		t.Fatal(err)
	}
	if err := other.Ready(t.Context()); !errors.Is(err, postgres.ErrUnreachable) {
		t.Fatalf("a closed pool: %v", err)
	}
}

// The idempotency keys, on a direct connection and through a pooler in
// transaction mode, which keeps no session state between two statements.
func TestIdempotency(t *testing.T) {
	for _, pooled := range []bool{false, true} {
		t.Run(map[bool]string{false: "direct", true: "pooled"}[pooled], func(t *testing.T) {
			clock := time.Date(2026, 10, 18, 9, 0, 0, 0, time.UTC)
			var mu sync.Mutex
			now := func() time.Time { mu.Lock(); defer mu.Unlock(); return clock }
			advance := func(d time.Duration) { mu.Lock(); clock = clock.Add(d); mu.Unlock() }
			s := pgtest.Open(t, pooled, postgres.Options{Now: now, MaxConns: 4})
			idem := s.Idempotency()
			ctx := t.Context()
			k := store.IdempotencyKey{Subject: "https://issuer.example.com|alice", Key: "k-1", Route: "POST /v1/experiments", BodyDigest: "sha256:aa"}

			first, fresh, err := idem.Begin(ctx, k, time.Minute)
			if err != nil || !fresh || first.Done || !first.CreatedAt.Equal(clock) || !first.ExpiresAt.Equal(clock.Add(time.Minute)) {
				t.Fatalf("the first reservation: %+v %v %v", first, fresh, err)
			}
			held, fresh, err := idem.Begin(ctx, k, time.Minute)
			if err != nil || fresh || held.Done || held.Route != k.Route || held.BodyDigest != k.BodyDigest {
				t.Fatalf("a repeat while the first is served: %+v %v %v", held, fresh, err)
			}
			answered := k
			answered.Status, answered.ContentType, answered.Body = 201, "application/json", []byte(`{"id":"exp_1"}`)
			if err := idem.Finish(ctx, answered, 24*time.Hour); err != nil {
				t.Fatal(err)
			}
			if err := idem.Finish(ctx, answered, 24*time.Hour); !errors.Is(err, store.ErrNotFound) {
				t.Fatalf("a second finish: %v", err)
			}
			advance(23 * time.Hour)
			replay, fresh, err := idem.Begin(ctx, k, time.Minute)
			if err != nil || fresh || !replay.Done || replay.Status != 201 || string(replay.Body) != `{"id":"exp_1"}` ||
				replay.ContentType != "application/json" || !replay.ExpiresAt.Equal(first.CreatedAt.Add(24*time.Hour)) {
				t.Fatalf("a repeat within the window: %+v %v %v", replay, fresh, err)
			}
			// An abandon leaves a finished key as it is.
			if err := idem.Abandon(ctx, k.Subject, k.Key); err != nil {
				t.Fatal(err)
			}
			if again, _, err := idem.Begin(ctx, k, time.Minute); err != nil || !again.Done {
				t.Fatalf("an abandoned finished key: %+v %v", again, err)
			}
			advance(2 * time.Hour)
			renewed, fresh, err := idem.Begin(ctx, k, time.Minute)
			if err != nil || !fresh || renewed.Done || renewed.Body != nil || !renewed.CreatedAt.Equal(clock) {
				t.Fatalf("a repeat past the window: %+v %v %v", renewed, fresh, err)
			}

			// A reservation whose request never finished expires with it.
			other := k
			other.Key, other.BodyDigest = "k-2", "sha256:bb"
			if _, fresh, err := idem.Begin(ctx, other, time.Minute); err != nil || !fresh {
				t.Fatal(err)
			}
			advance(2 * time.Minute)
			if _, fresh, err := idem.Begin(ctx, other, time.Minute); err != nil || !fresh {
				t.Fatalf("a stale reservation was not replaced: %v %v", fresh, err)
			}
			// An abandoned reservation runs again.
			if err := idem.Abandon(ctx, other.Subject, other.Key); err != nil {
				t.Fatal(err)
			}
			if _, fresh, err := idem.Begin(ctx, other, time.Minute); err != nil || !fresh {
				t.Fatalf("an abandoned reservation: %v %v", fresh, err)
			}
			// Another subject's key of the same spelling is its own.
			bob := k
			bob.Subject = "https://issuer.example.com|bob"
			if _, fresh, err := idem.Begin(ctx, bob, time.Minute); err != nil || !fresh {
				t.Fatalf("bob's key: %v %v", fresh, err)
			}

			// Replicas that race on one key reserve it once.
			race := k
			race.Key = "k-race"
			var wg sync.WaitGroup
			var won sync.Map
			for i := range 8 {
				wg.Go(func() {
					_, fresh, err := idem.Begin(ctx, race, time.Minute)
					if err != nil {
						t.Error(err)
					}
					won.Store(i, fresh)
				})
			}
			wg.Wait()
			wins := 0
			won.Range(func(_, v any) bool {
				if v.(bool) {
					wins++
				}
				return true
			})
			if wins != 1 {
				t.Fatalf("%d replicas reserved one key", wins)
			}

			// Prune deletes the expired keys alone.
			advance(25 * time.Hour)
			if _, _, err := idem.Begin(ctx, store.IdempotencyKey{Subject: k.Subject, Key: "k-live", Route: k.Route, BodyDigest: "sha256:cc"}, time.Hour); err != nil {
				t.Fatal(err)
			}
			n, err := idem.Prune(ctx, 2)
			if err != nil || n != 2 {
				t.Fatalf("the first batch: %d %v", n, err)
			}
			n, err = idem.Prune(ctx, 100)
			if err != nil || n != 2 {
				t.Fatalf("the rest: %d %v", n, err)
			}
			if live, fresh, err := idem.Begin(ctx, store.IdempotencyKey{Subject: k.Subject, Key: "k-live", Route: k.Route, BodyDigest: "sha256:cc"}, time.Hour); err != nil || fresh || live.Done {
				t.Fatalf("a live key was pruned: %+v %v %v", live, fresh, err)
			}
		})
	}
}

// A store whose database went away answers each call with an error.
func TestIdempotencyOnAClosedStore(t *testing.T) {
	s := pgtest.Open(t, false, postgres.Options{})
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	idem := s.Idempotency()
	k := store.IdempotencyKey{Subject: "s", Key: "k", Route: "r", BodyDigest: "d"}
	if _, _, err := idem.Begin(t.Context(), k, time.Minute); err == nil {
		t.Error("Begin")
	}
	if err := idem.Finish(t.Context(), k, time.Minute); err == nil || errors.Is(err, store.ErrNotFound) {
		t.Errorf("Finish: %v", err)
	}
	if err := idem.Abandon(t.Context(), "s", "k"); err == nil {
		t.Error("Abandon")
	}
	if _, err := idem.Prune(t.Context(), 1); err == nil {
		t.Error("Prune")
	}
}

// A run that starts while another is inside a migration waits for it: the
// migrator marks the schema dirty while a migration runs, and a check
// outside the lock read that as a migration that failed halfway.
func TestARunWaitsForOneInProgress(t *testing.T) {
	first, err := fs.ReadFile(migrations.FS, "000001_schema.up.sql")
	if err != nil {
		t.Fatal(err)
	}
	files := fstest.MapFS{
		"000001_schema.up.sql": {Data: first},
		"000002_slow.up.sql":   {Data: []byte("SELECT pg_sleep(2); CREATE TABLE slow (id int);")},
	}
	direct, _ := fresh(t)
	done := make(chan error, 1)
	go func() {
		_, err := postgres.MigrateWith(t.Context(), direct, files)
		done <- err
	}()
	conn, err := pgx.Connect(t.Context(), direct)
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if err := conn.Close(context.Background()); err != nil {
			t.Error(err)
		}
	}()
	deadline := time.Now().Add(30 * time.Second)
	for {
		var version int64
		var dirty bool
		err := conn.QueryRow(t.Context(), `SELECT version, dirty FROM schema_migrations`).Scan(&version, &dirty)
		if err == nil && version == 2 && dirty {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("the first run never entered the slow migration: %v", err)
		}
		time.Sleep(20 * time.Millisecond)
	}
	got, err := postgres.MigrateWith(t.Context(), direct, files)
	if err != nil || got.To != 2 {
		t.Fatalf("a run while another was inside a migration: %+v %v", got, err)
	}
	if err := <-done; err != nil {
		t.Fatalf("the first run: %v", err)
	}
}

// A migration that fails leaves the schema dirty, and migrate says which
// one; the way back is the backup, so the next run refuses the schema.
func TestAFailedMigration(t *testing.T) {
	first, err := fs.ReadFile(migrations.FS, "000001_schema.up.sql")
	if err != nil {
		t.Fatal(err)
	}
	files := fstest.MapFS{
		"000001_schema.up.sql": {Data: first},
		"000002_broken.up.sql": {Data: []byte("CREATE TABLE broken (;")},
	}
	direct, _ := fresh(t)
	_, err = postgres.MigrateWith(t.Context(), direct, files)
	if err == nil || !strings.Contains(err.Error(), "migration 2 failed and left the schema dirty") {
		t.Fatalf("a failed migration: %v", err)
	}
	if _, err := postgres.MigrateWith(t.Context(), direct, files); !errors.Is(err, postgres.ErrDirty) {
		t.Fatalf("the run after: %v", err)
	}
	if _, err := postgres.MigrateWith(t.Context(), direct, fstest.MapFS{}); err == nil {
		t.Fatal("a binary that carries no migration")
	}
}
