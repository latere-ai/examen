// SPDX-FileCopyrightText: 2026 Latere AI
// SPDX-License-Identifier: Apache-2.0

package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"strings"
	"sync/atomic"
	"time"

	"latere.ai/x/pkg/authz"
	"latere.ai/x/pkg/otel"

	"latere.ai/x/examen/internal/access"
	"latere.ai/x/examen/internal/compare"
	"latere.ai/x/examen/internal/config"
	"latere.ai/x/examen/internal/httpapi"
	"latere.ai/x/examen/internal/keys"
	"latere.ai/x/examen/internal/limits"
	"latere.ai/x/examen/internal/store/postgres"
	"latere.ai/x/examen/internal/version"
	"latere.ai/x/examen/internal/work"
)

// The times of the server.
const (
	// openTimeout bounds reaching the database at start.
	openTimeout = 30 * time.Second
	// warmTimeout bounds reading the issuers' keys and asking the probe
	// at start.
	warmTimeout = 10 * time.Second
	// readHeaderTimeout bounds how long a client takes to send its
	// request's headers.
	readHeaderTimeout = 10 * time.Second
	// sweepEvery is how often a replica deletes expired idempotency keys.
	sweepEvery = 10 * time.Minute
	// sweepBatch is how many keys one delete takes.
	sweepBatch = 1000
)

// Hooks are what a test hands the server that no variable sets.
type Hooks struct {
	// Ready, when it is not nil, is sent the address the server listens on
	// once it does.
	Ready chan<- string
	// Grace, when it is not zero, replaces SHUTDOWN_GRACE.
	Grace time.Duration
	// SweepEvery, when it is not zero, replaces how often expired
	// idempotency keys are deleted.
	SweepEvery time.Duration
	// WorkEvery, when it is not zero, replaces REPORT_INTERVAL, how often
	// the unattended work ticks.
	WorkEvery time.Duration
}

// reserved are the audiences Examen acts through: every core's and its
// own, which no program candidate's credential may name.
func reserved(s config.Settings) []string {
	out := []string{s.ToposAudience, s.CellaAudience, s.LuxAudience, s.ArcaAudience, s.OrigoAudience}
	return append(out, s.OIDCAudiences...)
}

// serve reads the configuration and serves until ctx ends, starting in the
// order of specs/011-distribution.md: configuration, telemetry, the
// database pool and its schema check, the issuers, the authorizer probe,
// and only then the listener.
func serve(ctx context.Context, getenv func(string) string, logs io.Writer, hooks Hooks) error {
	s, err := config.FromEnv(getenv)
	if err != nil {
		return err
	}
	if s.EventsURL != "" {
		// Sink events are journaled with their changes once the objects
		// exist; until delivery is built, an installation that names a sink
		// would believe it receives what nothing sends.
		return errors.New("EXAMEN_EVENTS_URL is set and this release delivers no sink events: unset EXAMEN_EVENTS_URL and EXAMEN_EVENTS_SECRET_FILE")
	}
	log, shutdown, err := otel.Bootstrap(ctx, otel.Config{
		ServiceName: "examend", Version: version.Version, Level: s.LogLevel,
		Stdout: slog.NewJSONHandler(logs, &slog.HandlerOptions{Level: s.LogLevel}),
	})
	if err != nil {
		// The logger works either way; the error is the export path's.
		log.WarnContext(ctx, "telemetry export is not set up", "error", err.Error())
	}
	defer func() {
		if err := shutdown(context.WithoutCancel(ctx)); err != nil {
			log.WarnContext(ctx, "telemetry did not flush", "error", err.Error())
		}
	}()

	st, err := open(ctx, s, log)
	if err != nil {
		return err
	}
	defer func() {
		if err := st.Close(); err != nil {
			log.WarnContext(ctx, "the store did not close", "error", err.Error())
		}
	}()
	who, err := identity(ctx, s, log)
	if err != nil {
		return err
	}
	announce(ctx, s, who, log)

	ln, err := (&net.ListenConfig{}).Listen(ctx, "tcp", s.Addr)
	if err != nil {
		return fmt.Errorf("EXAMEN_ADDR: %w", err)
	}
	var draining atomic.Bool
	engine := &work.Engine{Store: st, Authz: who.Authorizer, Analyzer: compare.Analysis{}, Log: log}
	api := &httpapi.Server{
		Store: st, Auth: who.Authenticator, Authz: who.Authorizer, BasePath: s.BasePath,
		Draining: draining.Load, Log: log, Engine: engine, Analyzer: compare.Analysis{}, Reserved: reserved(s),
	}
	// With the endpoint key source, each experiment whose runs a person's
	// own setup does is handed its Key, and its Key's records are read
	// through the endpoint. The lux key source is not built, so with no
	// endpoint no Key is leased and no record is read.
	if s.KeySource() == config.KeySourceEndpoint {
		src := keys.NewEndpoint(s.KeysURL, s.KeysToken.Reveal, &http.Client{Transport: otel.Transport(nil)})
		engine.Keys, api.Keys = src, src
	}
	httpSrv := &http.Server{Handler: api.Handler(), ReadHeaderTimeout: readHeaderTimeout, ErrorLog: slog.NewLogLogger(log.Handler(), slog.LevelWarn)}
	failed := make(chan error, 1)
	go func() { failed <- httpSrv.Serve(ln) }()
	log.InfoContext(ctx, "listening", "address", ln.Addr().String())

	background, stopWork := context.WithCancel(context.WithoutCancel(ctx))
	defer stopWork()
	every := sweepEvery
	if hooks.SweepEvery > 0 {
		every = hooks.SweepEvery
	}
	go sweep(background, st.Idempotency(), every, log)
	tick := limits.ReportInterval
	if hooks.WorkEvery > 0 {
		tick = hooks.WorkEvery
	}
	go engine.Loop(background, tick)
	if hooks.Ready != nil {
		hooks.Ready <- ln.Addr().String()
	}

	select {
	case err := <-failed:
		return err
	case <-ctx.Done():
	}
	// The stop signal: readiness fails at once, the listener stops taking
	// connections, and open requests have SHUTDOWN_GRACE to finish.
	draining.Store(true)
	log.InfoContext(ctx, "stopping: readiness fails and open requests have the grace to finish")
	grace := limits.ShutdownGrace
	if hooks.Grace > 0 {
		grace = hooks.Grace
	}
	stop, cancel := context.WithTimeout(context.WithoutCancel(ctx), grace)
	defer cancel()
	err = httpSrv.Shutdown(stop)
	if errors.Is(err, context.DeadlineExceeded) {
		// What is left is a request that did not end within the grace.
		err = httpSrv.Close()
	}
	if served := <-failed; served != nil && !errors.Is(served, http.ErrServerClosed) {
		return errors.Join(err, served)
	}
	return err
}

// open opens the pool over the serving URL and checks the schema: one the
// check refuses stops the start, and a newer one of the same major is a
// rollback within the major, named and served.
func open(ctx context.Context, s config.Settings, log *slog.Logger) (*postgres.Store, error) {
	ctx, cancel := context.WithTimeout(ctx, openTimeout)
	defer cancel()
	st, schema, err := postgres.Open(ctx, s.ServingURL(), postgres.Options{})
	if err != nil {
		name := "EXAMEN_DATABASE_URL"
		if s.DatabasePoolURL != "" {
			name = "EXAMEN_DATABASE_POOL_URL"
		}
		return nil, fmt.Errorf("%s: %w", name, err)
	}
	if schema.Newer() {
		log.WarnContext(ctx, "the schema holds migrations newer than this binary; serving it, as a rollback within the major",
			"schema_version", schema.Version, "binary_version", schema.Carried)
	}
	return st, nil
}

// identity builds the verifier and who decides, reads the issuers' keys and
// asks the authorizer the probe. An issuer or an authorizer that does not
// answer is named and the server starts, since every request then fails
// closed; an authorizer that allows the probe does not read the request,
// and the server does not start.
func identity(ctx context.Context, s config.Settings, log *slog.Logger) (*access.Access, error) {
	who, err := access.New(s, access.Options{})
	if err != nil {
		return nil, err
	}
	ctx, cancel := context.WithTimeout(ctx, warmTimeout)
	defer cancel()
	if err := who.Warm(ctx); err != nil {
		log.WarnContext(ctx, "an issuer did not answer; its keys are read when its first token arrives", "error", err.Error())
	}
	err = who.Check(ctx)
	switch {
	case errors.Is(err, authz.ErrProbeAllowed):
		return nil, errors.New("EXAMEN_AUTHORIZER_URL: the authorizer allowed the probe, so it does not read the request")
	case err != nil:
		log.WarnContext(ctx, "the authorizer did not answer the probe; every request fails closed until it does", "error", err.Error())
	}
	return who, nil
}

// announce logs the start lines of specs/011-distribution.md: which token
// source is in use, which key source, who decides, and which cores are
// configured.
func announce(ctx context.Context, s config.Settings, who *access.Access, log *slog.Logger) {
	log.InfoContext(ctx, "token source: "+s.TokenSource())
	log.InfoContext(ctx, "key source: "+s.KeySource())
	log.InfoContext(ctx, "decides: "+who.Mode)
	cores := "none"
	if c := s.Cores(); len(c) > 0 {
		cores = strings.Join(c, ", ")
	}
	log.InfoContext(ctx, "cores: "+cores)
}

// pruner deletes expired idempotency keys.
type pruner interface {
	Prune(ctx context.Context, limit int) (int, error)
}

// sweep deletes expired idempotency keys every interval, on every replica,
// a batch at a time until a batch takes fewer than it could.
func sweep(ctx context.Context, keys pruner, every time.Duration, log *slog.Logger) {
	tick := time.NewTicker(every)
	defer tick.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-tick.C:
			n, err := prune(ctx, keys)
			if err != nil && ctx.Err() == nil {
				log.WarnContext(ctx, "a sweep of the idempotency keys failed; the next tick tries again", "error", err.Error())
				continue
			}
			if n > 0 {
				log.InfoContext(ctx, "swept expired idempotency keys", "count", n)
			}
		}
	}
}

// prune deletes expired keys in batches until a batch takes fewer than
// sweepBatch, and says how many it deleted.
func prune(ctx context.Context, keys pruner) (int, error) {
	total := 0
	for {
		n, err := keys.Prune(ctx, sweepBatch)
		total += n
		if err != nil || n < sweepBatch {
			return total, err
		}
	}
}

// migrate applies the migrations the database lacks over the direct
// connection, then exits. It reads EXAMEN_DATABASE_URL and EXAMEN_LOG_LEVEL
// and nothing else, so a migration job runs with the database alone
// configured.
func migrate(ctx context.Context, getenv func(string) string, out, logs io.Writer) error {
	d, err := config.DatabaseFromEnv(getenv)
	if err != nil {
		return err
	}
	log := slog.New(slog.NewJSONHandler(logs, &slog.HandlerOptions{Level: d.LogLevel}))
	got, err := postgres.Migrate(ctx, d.URL)
	if err != nil {
		return fmt.Errorf("EXAMEN_DATABASE_URL: %w", err)
	}
	if got.From == got.To {
		log.InfoContext(ctx, "the schema holds every migration this binary carries", "schema_version", got.To)
	} else {
		log.InfoContext(ctx, "applied the migrations", "from", got.From, "to", got.To)
	}
	_, err = fmt.Fprintf(out, "examend: the schema is at version %d\n", got.To)
	return err
}
