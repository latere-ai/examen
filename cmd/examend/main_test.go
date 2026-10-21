// SPDX-FileCopyrightText: 2026 Latere AI
// SPDX-License-Identifier: Apache-2.0

package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"maps"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"latere.ai/x/pkg/authkit/issuertest"
	"latere.ai/x/pkg/authz/stub"

	"latere.ai/x/examen/authorizer"
	"latere.ai/x/examen/internal/config"
	"latere.ai/x/examen/internal/owner"
	"latere.ai/x/examen/internal/store/migrations"
	"latere.ai/x/examen/internal/store/postgres/pgtest"
	"latere.ai/x/examen/internal/version"
)

func TestMain(m *testing.M) { pgtest.Main(m) }

// env is a test's environment.
type env map[string]string

func (e env) get(name string) string { return e[name] }

// syncBuffer is a log written by the server while the test reads it.
type syncBuffer struct {
	mu sync.Mutex
	b  bytes.Buffer
}

func (s *syncBuffer) Write(p []byte) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.b.Write(p)
}

func (s *syncBuffer) String() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.b.String()
}

// `examend version` prints the version, the commit, the date the build
// stamped and the newest schema version the binary carries.
func TestVersionIsStamped(t *testing.T) {
	if testing.Short() {
		t.Skip("builds the binary")
	}
	bin := filepath.Join(t.TempDir(), "examend")
	pkg := "latere.ai/x/examen/internal/version"
	build := exec.Command("go", "build", "-o", bin, "-ldflags",
		"-X "+pkg+".Version=v9.8.7 -X "+pkg+".Commit=c0ffee1 -X "+pkg+".Date=2026-10-18T00:00:00Z", ".")
	if out, err := build.CombinedOutput(); err != nil {
		t.Fatalf("go build: %v\n%s", err, out)
	}
	out, err := exec.Command(bin, "version").Output()
	if err != nil {
		t.Fatal(err)
	}
	highest, err := migrations.Highest()
	if err != nil {
		t.Fatal(err)
	}
	want := fmt.Sprintf("examend v9.8.7 (c0ffee1, 2026-10-18T00:00:00Z, schema %d)", highest)
	if got := strings.TrimSpace(string(out)); got != want {
		t.Fatalf("examend version printed %q, want %q", got, want)
	}
	// A missing or unknown command exits 2 with the usage on stderr.
	cmd := exec.Command(bin, "start")
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	var exit *exec.ExitError
	if err := cmd.Run(); !errors.As(err, &exit) || exit.ExitCode() != 2 || !strings.Contains(stderr.String(), "usage: examend") {
		t.Fatalf("an unknown command: %v %q", err, stderr.String())
	}
}

// examend accepts exactly serve, migrate and version; any other command or
// none exits 2 with the usage, and a failure exits 1 with one line naming
// what failed.
func TestOneCommandAndNoFlag(t *testing.T) {
	var out, logs bytes.Buffer
	if code := run(t.Context(), []string{"version"}, env{}.get, &out, &logs, Hooks{}); code != 0 || !strings.HasPrefix(out.String(), version.String()[:len("examend ")]) {
		t.Fatalf("version: %d %q", code, out.String())
	}
	for _, args := range [][]string{nil, {"serve", "--addr=:1"}, {"-version"}, {"start"}, {"check"}} {
		logs.Reset()
		if code := run(t.Context(), args, env{}.get, &out, &logs, Hooks{}); code != 2 || !strings.Contains(logs.String(), "usage: examend") {
			t.Errorf("run(%q) = %d %q", args, code, logs.String())
		}
	}
	logs.Reset()
	code := run(t.Context(), []string{"serve"}, env{}.get, &out, &logs, Hooks{})
	if code != 1 || strings.Count(strings.TrimSpace(logs.String()), "\n") != 0 ||
		!strings.Contains(logs.String(), "EXAMEN_DATABASE_URL") || !strings.Contains(logs.String(), "EXAMEN_OIDC_ISSUERS") {
		t.Fatalf("serve with nothing set: %d %q", code, logs.String())
	}
	logs.Reset()
	if code := run(t.Context(), []string{"migrate"}, env{}.get, &out, &logs, Hooks{}); code != 1 || !strings.Contains(logs.String(), "EXAMEN_DATABASE_URL") {
		t.Fatalf("migrate with nothing set: %d %q", code, logs.String())
	}
	if code := run(t.Context(), []string{"version"}, env{}.get, failingWriter{}, &logs, Hooks{}); code != 1 {
		t.Fatalf("version to a closed stream: %d", code)
	}
	if code := fail(failingWriter{}, 1, "x"); code != 1 {
		t.Fatalf("a failure to a closed stream: %d", code)
	}
}

type failingWriter struct{}

func (failingWriter) Write([]byte) (int, error) { return 0, errors.New("closed") }

// migrate against an empty database applies every migration and exits 0;
// a second run applies none and exits 0.
func TestMigrateCommand(t *testing.T) {
	srv, err := pgtest.Start()
	if err != nil {
		t.Fatalf("no Postgres: %v", err)
	}
	name := fmt.Sprintf("examen_cmd_%d", time.Now().UnixNano())
	if err := pgtest.Exec(t.Context(), srv.Direct, "create database "+name); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		if err := pgtest.Exec(ctx, srv.Direct, "drop database if exists "+name+" with (force)"); err != nil {
			t.Error(err)
		}
	})
	direct := pgtest.OnDatabase(t, srv.Direct, name)
	highest, err := migrations.Highest()
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"applied the migrations", "holds every migration"} {
		var out, logs bytes.Buffer
		code := run(t.Context(), []string{"migrate"}, env{"EXAMEN_DATABASE_URL": direct, "EXAMEN_LOG_LEVEL": "debug"}.get, &out, &logs, Hooks{})
		if code != 0 || !strings.Contains(logs.String(), want) || strings.TrimSpace(out.String()) != fmt.Sprintf("examend: the schema is at version %d", highest) {
			t.Fatalf("migrate: %d %q %q", code, out.String(), logs.String())
		}
	}
	var out, logs bytes.Buffer
	if code := run(t.Context(), []string{"migrate"}, env{"EXAMEN_DATABASE_URL": "postgres://examen:pw@127.0.0.1:1/x?connect_timeout=1"}.get, &out, &logs, Hooks{}); code != 1 ||
		!strings.Contains(logs.String(), "EXAMEN_DATABASE_URL") || strings.Contains(logs.String(), "pw@") {
		t.Fatalf("migrate against nothing: %d %q", code, logs.String())
	}
}

// server is a running examend with what it was started with.
type server struct {
	url    string
	logs   *syncBuffer
	issuer *issuertest.Server
	stop   context.CancelFunc
	// done is closed when serve returned, with its status in code.
	done chan struct{}
	code int
}

// start runs `examend serve` in process with the environment, over a
// migrated database of its own and a stub issuer, and stops it when the
// test ends.
func start(t *testing.T, extra env) *server {
	t.Helper()
	direct, pooled := pgtest.Database(t)
	iss := issuertest.New(t, issuertest.WithDefaultAudience("examen"))
	e := env{
		"EXAMEN_ADDR": "127.0.0.1:0", "EXAMEN_DATABASE_URL": direct, "EXAMEN_DATABASE_POOL_URL": pooled,
		"EXAMEN_OIDC_ISSUERS": iss.URL(),
	}
	maps.Copy(e, extra)
	ctx, cancel := context.WithCancel(t.Context())
	ready := make(chan string, 1)
	s := &server{logs: &syncBuffer{}, issuer: iss, stop: cancel, done: make(chan struct{})}
	go func() {
		s.code = run(ctx, []string{"serve"}, e.get, io.Discard, s.logs, Hooks{Ready: ready, Grace: 2 * time.Second, SweepEvery: 50 * time.Millisecond, WorkEvery: 20 * time.Millisecond})
		close(s.done)
	}()
	select {
	case addr := <-ready:
		s.url = "http://" + addr
	case <-s.done:
		t.Fatalf("serve exited %d before it listened: %s", s.code, s.logs.String())
	case <-time.After(60 * time.Second):
		t.Fatalf("serve did not listen: %s", s.logs.String())
	}
	t.Cleanup(func() {
		cancel()
		select {
		case <-s.done:
		case <-time.After(10 * time.Second):
			t.Error("serve did not stop")
		}
	})
	return s
}

func (s *server) get(t *testing.T, path, bearer string) (int, []byte) {
	t.Helper()
	r, err := http.NewRequestWithContext(t.Context(), http.MethodGet, s.url+path, nil)
	if err != nil {
		t.Fatal(err)
	}
	if bearer != "" {
		r.Header.Set("Authorization", "Bearer "+bearer)
	}
	res, err := http.DefaultClient.Do(r)
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if err := res.Body.Close(); err != nil {
			t.Error(err)
		}
	}()
	body, err := io.ReadAll(res.Body)
	if err != nil {
		t.Fatal(err)
	}
	return res.StatusCode, body
}

// serve starts over a migrated database, names its sources at start,
// answers its probes and its contract, verifies a bearer, and on the stop
// signal exits 0.
func TestServe(t *testing.T) {
	s := start(t, env{"EXAMEN_LOG_LEVEL": "debug"})
	for _, line := range []string{"token source: none", "key source: lux", "decides: owner policy", "cores: none", "listening"} {
		if !strings.Contains(s.logs.String(), line) {
			t.Errorf("the start log lacks %q", line)
		}
	}
	if code, _ := s.get(t, "/livez", ""); code != http.StatusOK {
		t.Fatalf("/livez %d", code)
	}
	if code, body := s.get(t, "/readyz", ""); code != http.StatusOK {
		t.Fatalf("/readyz %d %s", code, body)
	}
	code, body := s.get(t, "/version", "")
	var v struct {
		Version string `json:"version"`
	}
	if err := json.Unmarshal(body, &v); err != nil || code != http.StatusOK || v.Version != version.Version {
		t.Fatalf("/version %d %s", code, body)
	}
	if code, _ := s.get(t, "/v1/openapi.yaml", ""); code != http.StatusOK {
		t.Fatalf("the contract %d", code)
	}
	token := s.issuer.Mint(issuertest.Claims{Sub: "alice"})
	if code, body := s.get(t, "/v1/nothing", token); code != http.StatusNotFound || !strings.Contains(string(body), `"not_found"`) {
		t.Fatalf("no route: %d %s", code, body)
	}
	if strings.Contains(s.logs.String(), token) {
		t.Fatal("the log carries a token")
	}
	s.stop()
	select {
	case <-s.done:
		if s.code != 0 {
			t.Fatalf("serve exited %d on the stop signal: %s", s.code, s.logs.String())
		}
	case <-time.After(10 * time.Second):
		t.Fatal("serve did not stop on the signal")
	}
	if !strings.Contains(s.logs.String(), "stopping") {
		t.Fatalf("the stop was not logged: %s", s.logs.String())
	}
}

// serve names the token source, the key source, who decides and every
// configured core when they are set.
func TestServeAnnouncesWhatIsConfigured(t *testing.T) {
	authz := stub.New(t, stub.WithVocabulary(authorizer.Vocabulary()))
	dir := t.TempDir()
	write := func(name, content string) string {
		p := filepath.Join(dir, name)
		if err := os.WriteFile(p, []byte(content), 0o600); err != nil {
			t.Fatal(err)
		}
		return p
	}
	s := start(t, env{
		"EXAMEN_AUTHORIZER_URL": authz.URL(), "EXAMEN_AUTHORIZER_TOKEN_FILE": write("authz", authz.Token()),
		"EXAMEN_KEYS_URL": "http://127.0.0.1:1", "EXAMEN_KEYS_TOKEN_FILE": write("keys", "k"),
		"EXAMEN_SOLE_OWNER": "https://issuer.example.com|alice", "EXAMEN_OWNER_TOKEN_DIR": dir,
		"EXAMEN_LUX_URL": "http://127.0.0.1:2", "EXAMEN_ARCA_URL": "http://localhost:3",
	})
	for _, line := range []string{"token source: token files", "key source: endpoint", "decides: authorizer", "cores: lux, arca"} {
		if !strings.Contains(s.logs.String(), line) {
			t.Errorf("the start log lacks %q: %s", line, s.logs.String())
		}
	}
	if strings.Contains(s.logs.String(), authz.Token()) {
		t.Fatal("the log carries the authorizer's bearer")
	}
}

// serve stops before it listens on each refusal it can meet here: a schema
// that lacks a migration, a database that does not answer, an authorizer
// that allows the probe, a sink it cannot deliver to, and an address it
// cannot listen on; an issuer and an authorizer that do not answer are
// named and the server starts.
func TestServeRefuses(t *testing.T) {
	iss := issuertest.New(t, issuertest.WithDefaultAudience("examen"))
	srv, err := pgtest.Start()
	if err != nil {
		t.Fatalf("no Postgres: %v", err)
	}
	name := fmt.Sprintf("examen_empty_%d", time.Now().UnixNano())
	if err := pgtest.Exec(t.Context(), srv.Direct, "create database "+name); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		if err := pgtest.Exec(ctx, srv.Direct, "drop database if exists "+name+" with (force)"); err != nil {
			t.Error(err)
		}
	})
	empty := pgtest.OnDatabase(t, srv.Direct, name)
	migrated, pooled := pgtest.Database(t)
	yes := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		if _, err := io.WriteString(w, `{"allow":true}`); err != nil {
			t.Error(err)
		}
	}))
	defer yes.Close()
	token := filepath.Join(t.TempDir(), "token")
	if err := os.WriteFile(token, []byte("bearer"), 0o600); err != nil {
		t.Fatal(err)
	}
	busy, err := (&net.ListenConfig{}).Listen(t.Context(), "tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if err := busy.Close(); err != nil {
			t.Error(err)
		}
	}()
	base := env{"EXAMEN_ADDR": "127.0.0.1:0", "EXAMEN_OIDC_ISSUERS": iss.URL(), "EXAMEN_DATABASE_URL": migrated}
	cases := []struct {
		name string
		set  env
		says string
	}{
		{"a schema that lacks a migration", env{"EXAMEN_DATABASE_URL": empty}, "run examend migrate"},
		{"a pooled database that does not answer", env{"EXAMEN_DATABASE_POOL_URL": "postgres://examen:pw@127.0.0.1:1/x?connect_timeout=1"}, "EXAMEN_DATABASE_POOL_URL"},
		{"an authorizer that allows the probe", env{"EXAMEN_AUTHORIZER_URL": yes.URL, "EXAMEN_AUTHORIZER_TOKEN_FILE": token}, "allowed the probe"},
		{"a sink", env{"EXAMEN_EVENTS_URL": "https://sink.example.com", "EXAMEN_EVENTS_SECRET_FILE": token}, "EXAMEN_EVENTS_URL"},
		{"an address in use", env{"EXAMEN_ADDR": busy.Addr().String(), "EXAMEN_DATABASE_POOL_URL": pooled}, "EXAMEN_ADDR"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			e := env{}
			maps.Copy(e, base)
			maps.Copy(e, c.set)
			var logs syncBuffer
			ready := make(chan string, 1)
			code := run(t.Context(), []string{"serve"}, e.get, io.Discard, &logs, Hooks{Ready: ready})
			if code != 1 || !strings.Contains(logs.String(), c.says) || len(ready) != 0 {
				t.Fatalf("%d %s", code, logs.String())
			}
			if strings.Contains(logs.String(), "pw@") {
				t.Fatalf("the refusal names a password: %s", logs.String())
			}
		})
	}
	// An issuer and an authorizer that do not answer are named, and the
	// server starts.
	gone := httptest.NewServer(http.NotFoundHandler())
	gone.Close()
	down := stub.New(t, stub.WithVocabulary(authorizer.Vocabulary()))
	down.Fail(http.StatusInternalServerError)
	s := start(t, env{"EXAMEN_OIDC_ISSUERS": gone.URL, "EXAMEN_AUTHORIZER_URL": down.URL(), "EXAMEN_AUTHORIZER_TOKEN_FILE": token})
	for _, line := range []string{"an issuer did not answer", "the authorizer did not answer the probe"} {
		if !strings.Contains(s.logs.String(), line) {
			t.Errorf("the start log lacks %q: %s", line, s.logs.String())
		}
	}
	if code, _ := s.get(t, "/readyz", ""); code != http.StatusOK {
		t.Fatalf("readiness depends on the issuer or the authorizer: %d", code)
	}
}

// A newer schema of the same major is named and served: a rollback within
// the major.
func TestServeANewerSchema(t *testing.T) {
	direct, _ := pgtest.Database(t)
	highest, err := migrations.Highest()
	if err != nil {
		t.Fatal(err)
	}
	if err := pgtest.Exec(t.Context(), direct, `UPDATE schema_migrations SET version = $1`, int64(highest+1)); err != nil {
		t.Fatal(err)
	}
	iss := issuertest.New(t)
	ready := make(chan string, 1)
	ctx, cancel := context.WithCancel(t.Context())
	var logs syncBuffer
	done := make(chan int, 1)
	go func() {
		done <- run(ctx, []string{"serve"}, env{"EXAMEN_ADDR": "127.0.0.1:0", "EXAMEN_DATABASE_URL": direct, "EXAMEN_OIDC_ISSUERS": iss.URL()}.get, io.Discard, &logs, Hooks{Ready: ready})
	}()
	select {
	case <-ready:
	case code := <-done:
		t.Fatalf("serve refused a newer schema: %d %s", code, logs.String())
	}
	cancel()
	if code := <-done; code != 0 || !strings.Contains(logs.String(), "newer than this binary") {
		t.Fatalf("%d %s", code, logs.String())
	}
}

// The sweep deletes expired keys in batches until one takes fewer than it
// could, and a failed batch is logged and tried at the next tick.
func TestSweep(t *testing.T) {
	p := &batches{answers: []int{sweepBatch, sweepBatch, 3}}
	if n, err := prune(t.Context(), p); err != nil || n != 2*sweepBatch+3 || p.calls != 3 {
		t.Fatalf("%d %v %d", n, err, p.calls)
	}
	broken := &batches{err: errors.New("the database does not answer")}
	logs := &syncBuffer{}
	ctx, cancel := context.WithCancel(t.Context())
	done := make(chan struct{})
	go func() {
		sweep(ctx, broken, time.Millisecond, slog.New(slog.NewJSONHandler(logs, nil)))
		close(done)
	}()
	deadline := time.Now().Add(5 * time.Second)
	for !strings.Contains(logs.String(), "sweep of the idempotency keys failed") {
		if time.Now().After(deadline) {
			t.Fatal("a failed sweep was not logged")
		}
		time.Sleep(5 * time.Millisecond)
	}
	cancel()
	<-done
	swept := &batches{answers: []int{2}}
	logs = &syncBuffer{}
	ctx, cancel = context.WithCancel(t.Context())
	done = make(chan struct{})
	go func() {
		sweep(ctx, swept, time.Millisecond, slog.New(slog.NewJSONHandler(logs, nil)))
		close(done)
	}()
	deadline = time.Now().Add(5 * time.Second)
	for !strings.Contains(logs.String(), "swept expired idempotency keys") {
		if time.Now().After(deadline) {
			t.Fatal("a sweep was not logged")
		}
		time.Sleep(5 * time.Millisecond)
	}
	cancel()
	<-done
}

// batches answers each Prune with the next of its answers, then zero.
type batches struct {
	mu      sync.Mutex
	answers []int
	err     error
	calls   int
}

func (b *batches) Prune(context.Context, int) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.calls++
	if b.err != nil {
		return 0, b.err
	}
	if len(b.answers) == 0 {
		return 0, nil
	}
	n := b.answers[0]
	b.answers = b.answers[1:]
	return n, nil
}

// TestServeGradesWhereItsCoresAre: with Topos and the minter, the server
// grades topos trials: checks where Cella and Arca are named, judges where
// Lux and Arca are, and says so at start; with neither, it grades nothing.
func TestServeGradesWhereItsCoresAre(t *testing.T) {
	authz := stub.New(t, stub.WithVocabulary(authorizer.Vocabulary()))
	dir := t.TempDir()
	write := func(name, content string) string {
		p := filepath.Join(dir, name)
		if err := os.WriteFile(p, []byte(content), 0o600); err != nil {
			t.Fatal(err)
		}
		return p
	}
	s := start(t, env{
		"EXAMEN_AUTHORIZER_URL": authz.URL(), "EXAMEN_AUTHORIZER_TOKEN_FILE": write("authz", authz.Token()),
		"EXAMEN_KEYS_URL": "http://127.0.0.1:1", "EXAMEN_KEYS_TOKEN_FILE": write("keys", "k"),
		"EXAMEN_MINTER_URL": "http://127.0.0.1:4", "EXAMEN_MINTER_TOKEN_FILE": write("minter", "m"),
		"EXAMEN_TOPOS_URL": "http://127.0.0.1:5", "EXAMEN_CELLA_URL": "http://127.0.0.1:6", "EXAMEN_CELLA_ENVIRONMENT": "default",
		"EXAMEN_LUX_URL": "http://127.0.0.1:2", "EXAMEN_ARCA_URL": "http://localhost:3",
	})
	if !strings.Contains(s.logs.String(), `"checks":true`) || !strings.Contains(s.logs.String(), `"model_judges":true`) {
		t.Errorf("the start log does not say what grades: %s", s.logs.String())
	}
	cfg := config.Settings{}
	if grading(cfg, nil) != nil {
		t.Error("a server with no core of grading grades")
	}
	cfg.ArcaURL, cfg.CellaURL = "http://a", "http://c"
	if g := grading(cfg, &owner.Minter{}); g == nil || !g.Checks() || g.Judges() {
		t.Errorf("Cella and Arca run checks alone: %+v", g)
	}
}
