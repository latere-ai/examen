// SPDX-FileCopyrightText: 2026 Latere AI
// SPDX-License-Identifier: Apache-2.0

// Package goalless_test imports the goalless-agents study into examend, as
// declared past trials of an external harness, and holds what Examen
// computes from them to what the study's files say. It reads the study
// from GOALLESS_DIR and is skipped without it. It imports into a local
// examend of its own, or, in remote mode, into the server EXAMEN_E2E_URL
// names; see README.md.
package goalless_test

import (
	"bufio"
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"maps"
	"math"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"sort"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"latere.ai/x/pkg/authkit/issuertest"

	"latere.ai/x/examen/internal/store/postgres/pgtest"
)

func TestMain(m *testing.M) { pgtest.Main(m) }

// selfReported are the metrics whose values the converter computes and
// imports as the harness's own.
var selfReported = []string{"loc", "loc_nonblank", "implemented", "browser_interactive"}

// settleTimeout bounds how long the automatic comparisons take to be
// computed a last time after every intake is closed, and how long one
// experiment takes to complete after its close; the server's work ticks
// every REPORT_INTERVAL.
const settleTimeout = 10 * time.Minute

// activeTimeout bounds how long a create waits for the owner's active
// experiments to drop below the authorizer's or the server's ceiling.
const activeTimeout = 20 * time.Minute

// importCapUSDMicro is the spending cap a remote-mode experiment is
// created with, one cent: an import calls no model, so nothing is ever
// drawn on the Budget an installation's authorizer makes from it.
const importCapUSDMicro = 10_000

func TestGoallessStudy(t *testing.T) {
	dir := os.Getenv("GOALLESS_DIR")
	if dir == "" {
		t.Skip("GOALLESS_DIR names no checkout of the goalless-agents study")
	}
	s, err := readStudy(dir)
	if err != nil {
		t.Fatal(err)
	}
	var orc *oracle
	if p := os.Getenv("GOALLESS_ORACLE"); p != "" {
		if orc, err = readOracle(p); err != nil {
			t.Fatal(err)
		}
	}
	for _, sk := range s.skipped {
		t.Logf("not imported: %s (%s)", sk.Ref, sk.Reason)
	}
	r := target(t).importAndCheck(s, orc)
	t.Log(r.String())
}

// api is a client of examend: a local one the test started, or the one
// remote mode names.
type api struct {
	t *testing.T
	// base is the server's base URL, its base path included.
	base string
	// token is the bearer, or tokenFile the file that holds it, read
	// again before every request, so a short-lived bearer another process
	// keeps fresh is never stale.
	token, tokenFile string
	// remote says the server is one EXAMEN_E2E_URL names.
	remote bool
}

// target is the server an import runs against: the one EXAMEN_E2E_URL
// names, with the bearer EXAMEN_E2E_TOKEN, or the file
// EXAMEN_E2E_TOKEN_FILE, holds; with none of them, a local examend of the
// test's own.
func target(t *testing.T) *api {
	t.Helper()
	base, token, file := os.Getenv("EXAMEN_E2E_URL"), os.Getenv("EXAMEN_E2E_TOKEN"), os.Getenv("EXAMEN_E2E_TOKEN_FILE")
	switch {
	case base == "" && token == "" && file == "":
		return startServer(t, serverOptions{})
	case base == "":
		t.Fatal("a bearer is set and EXAMEN_E2E_URL names no server to send it to")
	case token == "" && file == "":
		t.Fatal("EXAMEN_E2E_URL names a server and neither EXAMEN_E2E_TOKEN nor EXAMEN_E2E_TOKEN_FILE holds a bearer for it")
	case token != "" && file != "":
		t.Fatal("EXAMEN_E2E_TOKEN and EXAMEN_E2E_TOKEN_FILE are both set; keep one")
	}
	u, err := url.Parse(base)
	if err != nil || (u.Scheme != "https" && u.Scheme != "http") || u.Host == "" || u.RawQuery != "" || u.Fragment != "" {
		t.Fatalf("EXAMEN_E2E_URL is not an http or https URL with a host and no query: %q", base)
	}
	return &api{t: t, base: strings.TrimRight(base, "/"), token: token, tokenFile: file, remote: true}
}

// bearer is the token a request carries, never logged.
func (a *api) bearer() string {
	a.t.Helper()
	if a.tokenFile == "" {
		return a.token
	}
	b, err := os.ReadFile(a.tokenFile)
	if err != nil {
		a.t.Fatalf("reading EXAMEN_E2E_TOKEN_FILE: %v", err)
	}
	return strings.TrimSpace(string(b))
}

// transientAttempts bounds how often a request is sent again after the
// network or a gateway failed it, or its idempotency key was still being
// served.
const transientAttempts = 6

// do sends one request, with an Idempotency-Key when key is not empty, and
// answers its status, body and headers. It waits out a 429 for as long as
// its Retry-After asks, as any client does, and sends the request again
// after a transport error, a 502, 503 or 504, and a 409
// request_in_progress: every request the harness sends is safe to repeat,
// an apply and a close by their rule and a create and an import by their
// key.
func (a *api) do(method, path string, body any, key string) (int, []byte, http.Header) {
	a.t.Helper()
	var raw []byte
	if body != nil {
		var err error
		if raw, err = json.Marshal(body); err != nil {
			a.t.Fatal(err)
		}
	}
	for attempt := 1; ; attempt++ {
		code, out, header, err := a.send(method, path, raw, key)
		retry := time.Duration(1<<min(attempt, 5)) * time.Second
		switch {
		case err != nil:
			if attempt >= transientAttempts {
				a.t.Fatalf("%s %s: %v", method, path, err)
			}
			a.t.Logf("%s %s: %v; sending it again in %v", method, path, err, retry)
		case code == http.StatusTooManyRequests:
			wait, err := strconv.Atoi(header.Get("Retry-After"))
			if err != nil || wait < 1 {
				wait = 1
			}
			time.Sleep(time.Duration(wait) * time.Second)
			attempt--
			continue
		case (code == http.StatusBadGateway || code == http.StatusServiceUnavailable || code == http.StatusGatewayTimeout ||
			(code == http.StatusConflict && errorOf(out).Code == "request_in_progress")) && attempt < transientAttempts:
			a.t.Logf("%s %s: %d; sending it again in %v", method, path, code, retry)
		default:
			return code, out, header
		}
		time.Sleep(retry)
	}
}

// send is one round trip.
func (a *api) send(method, path string, raw []byte, key string) (int, []byte, http.Header, error) {
	a.t.Helper()
	req, err := http.NewRequestWithContext(a.t.Context(), method, a.base+path, bytes.NewReader(raw))
	if err != nil {
		a.t.Fatal(err)
	}
	req.Header.Set("Authorization", "Bearer "+a.bearer())
	if raw != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	if key != "" {
		req.Header.Set("Idempotency-Key", key)
	}
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		return 0, nil, nil, err
	}
	out, err := io.ReadAll(res.Body)
	if cerr := res.Body.Close(); err == nil {
		err = cerr
	}
	return res.StatusCode, out, res.Header, err
}

// call sends one request and answers its status and body.
func (a *api) call(method, path string, body any) (int, []byte) {
	a.t.Helper()
	code, out, _ := a.do(method, path, body, "")
	return code, out
}

// must sends a request that must answer one of the statuses and decodes
// its body into out.
func (a *api) must(method, path string, body, out any, statuses ...int) {
	a.t.Helper()
	code, raw := a.call(method, path, body)
	if !slices.Contains(statuses, code) {
		a.t.Fatalf("%s %s: %d %s", method, path, code, raw)
	}
	if out != nil {
		if err := json.Unmarshal(raw, out); err != nil {
			a.t.Fatalf("%s %s: %v: %s", method, path, err, raw)
		}
	}
}

// apiError is the part of an error answer the harness branches on.
type apiError struct {
	Code    string `json:"code"`
	Details struct {
		Reason   string `json:"reason"`
		Constant string `json:"constant"`
	} `json:"details"`
}

// errorOf reads an error answer; a body that is not one reads as no code.
func errorOf(raw []byte) apiError {
	var e struct {
		Error apiError `json:"error"`
	}
	if json.Unmarshal(raw, &e) != nil {
		return apiError{}
	}
	return e.Error
}

// serverOptions shape the local examend a test starts.
type serverOptions struct {
	// hosted serves under a base path of its own and asks the authorizer a
	// hosted installation answers with (hostedDecider), so a create gives a
	// spending cap as remote mode does, in place of the built-in owner
	// policy and a Budget's name.
	hosted bool
}

// hostedBasePath is the base path a hosted local examend serves under, so
// a remote-mode run there meets a base path other than the default /v1.
const hostedBasePath = "/v1/evals"

// localTokenLifetime is how long the bearer a local examend's test issuer
// mints lives: longer than any run, the live smoke test's wait for a lease
// to lapse included.
const localTokenLifetime = 12 * time.Hour

// startServer builds examend, migrates a database of its own, and serves
// it with a test issuer, until the test ends: with the built-in owner
// policy, or, hosted, under hostedBasePath with the authorizer a hosted
// installation answers with.
func startServer(t *testing.T, o serverOptions) *api {
	t.Helper()
	bin := filepath.Join(t.TempDir(), "examend")
	build := exec.Command("go", "build", "-o", bin, "latere.ai/x/examen/cmd/examend")
	if out, err := build.CombinedOutput(); err != nil {
		t.Fatalf("go build: %v\n%s", err, out)
	}
	direct, pooled := pgtest.Database(t)
	iss := issuertest.New(t, issuertest.WithDefaultAudience("examen"))
	base := []string{"PATH=" + os.Getenv("PATH"), "HOME=" + os.Getenv("HOME")}
	migrate := exec.Command(bin, "migrate")
	migrate.Env = append(slices.Clone(base), "EXAMEN_DATABASE_URL="+direct)
	if out, err := migrate.CombinedOutput(); err != nil {
		t.Fatalf("examend migrate: %v\n%s", err, out)
	}
	env := append(slices.Clone(base), "EXAMEN_ADDR=127.0.0.1:0", "EXAMEN_DATABASE_URL="+direct,
		"EXAMEN_DATABASE_POOL_URL="+pooled, "EXAMEN_OIDC_ISSUERS="+iss.URL(), "EXAMEN_LOG_LEVEL=info")
	basePath := "/v1"
	if o.hosted {
		authz := hostedDecider(t)
		tokenFile := filepath.Join(t.TempDir(), "authorizer-token")
		if err := os.WriteFile(tokenFile, []byte(authz.Token()), 0o600); err != nil {
			t.Fatal(err)
		}
		basePath = hostedBasePath
		env = append(env, "EXAMEN_BASE_PATH="+basePath, "EXAMEN_AUTHORIZER_URL="+authz.URL(), "EXAMEN_AUTHORIZER_TOKEN_FILE="+tokenFile)
	}
	ctx, cancel := context.WithCancel(context.Background())
	serve := exec.CommandContext(ctx, bin, "serve")
	serve.Env = env
	serve.Cancel = func() error { return serve.Process.Signal(os.Interrupt) }
	serve.WaitDelay = 30 * time.Second
	// examend logs to its standard error, one JSON object a line.
	stderr, err := serve.StderrPipe()
	if err != nil {
		t.Fatal(err)
	}
	var logs bytes.Buffer
	var mu sync.Mutex
	listening := make(chan string, 1)
	if err := serve.Start(); err != nil {
		t.Fatal(err)
	}
	go func() {
		sc := bufio.NewScanner(stderr)
		sc.Buffer(make([]byte, 64<<10), 1<<20)
		for sc.Scan() {
			mu.Lock()
			logs.Write(append(sc.Bytes(), '\n'))
			mu.Unlock()
			var line struct {
				Msg     string `json:"msg"`
				Address string `json:"address"`
			}
			if json.Unmarshal(sc.Bytes(), &line) == nil && line.Msg == "listening" {
				listening <- line.Address
			}
		}
	}()
	t.Cleanup(func() {
		cancel()
		if err := serve.Wait(); err != nil && ctx.Err() == nil {
			t.Errorf("examend serve: %v", err)
		}
		if t.Failed() {
			mu.Lock()
			t.Logf("examend's log:\n%s", logs.String())
			mu.Unlock()
		}
	})
	var addr string
	select {
	case addr = <-listening:
	case <-time.After(time.Minute):
		t.Fatal("examend serve did not listen")
	}
	token := iss.Mint(issuertest.Claims{Sub: "goalless-owner", Exp: time.Now().Add(localTokenLifetime).Unix()})
	return &api{t: t, base: "http://" + addr + basePath, token: token}
}

// run is what one import did and found, for its report.
type run struct {
	base        string
	remote      bool
	exps        []*experiment
	imported    int // trials this run imported
	present     int // trials an earlier run had imported
	cells       int // cells held to the converter's runs
	oracleCells int // cells held to the study's figures, -1 without an oracle
	comparisons int // comparisons read back
	rows        int // their results read, suite-level and task-level
}

// importAndCheck applies the suite, imports the study, waits for every
// comparison to be final, and holds what the server computed to the
// converter's runs and, with an oracle, to the study's figures.
func (a *api) importAndCheck(s *study, orc *oracle) run {
	a.t.Helper()
	read := 0
	for _, n := range s.dirs {
		read += n
	}
	if read != len(s.runs)+len(s.skipped) {
		a.t.Fatalf("read %d run directories, imported %d and skipped %d", read, len(s.runs), len(s.skipped))
	}
	digests := a.applySuite(s)
	exps := a.importStudy(s, digests)
	a.settle(exps)
	report(a.t, s, exps)
	r := run{base: a.base, remote: a.remote, exps: exps, oracleCells: -1}
	for _, e := range exps {
		r.imported += e.imported
		r.present += e.present
	}
	if orc != nil {
		if orc.RunDirectories != 0 && read != orc.RunDirectories {
			a.t.Errorf("read %d run directories; the study holds %d", read, orc.RunDirectories)
		}
		if orc.Experiments != 0 && len(exps) != orc.Experiments {
			a.t.Errorf("imported into %d experiments; the study's figures name %d", len(exps), orc.Experiments)
		}
	}
	for _, e := range exps {
		e.cells = a.checkExperiment(e)
		e.comparisons, e.rows = a.checkNeverHeadlined(e)
		r.cells += e.cells
		r.comparisons += e.comparisons
		r.rows += e.rows
	}
	if orc != nil {
		r.oracleCells = a.checkOracle(orc, exps)
	}
	return r
}

// String is the run's report: the experiments created or found, the trials
// imported, the cells checked and the comparisons read back.
func (r run) String() string {
	created := 0
	for _, e := range r.exps {
		if !e.found {
			created++
		}
	}
	where := "a local examend"
	if r.remote {
		where = r.base
	}
	oracle := "no GOALLESS_ORACLE, so against the converter's runs alone"
	if r.oracleCells >= 0 {
		oracle = fmt.Sprintf("%d held to the study's figures in GOALLESS_ORACLE", r.oracleCells)
	}
	var b strings.Builder
	fmt.Fprintf(&b, "goalless import report, into %s\n", where)
	fmt.Fprintf(&b, "  experiments  %d: %d created, %d found from an earlier run\n", len(r.exps), created, len(r.exps)-created)
	fmt.Fprintf(&b, "  trials       %d imported by this run, %d already there\n", r.imported, r.present)
	fmt.Fprintf(&b, "  cells        %d held to their runs' outcomes and values; %s\n", r.cells, oracle)
	fmt.Fprintf(&b, "  comparisons  %d read back, final, with %d results; no self-reported value headlined\n", r.comparisons, r.rows)
	for _, e := range r.exps {
		state := "created"
		if e.found {
			state = "found"
		}
		studies := make([]string, len(e.studies))
		for i, n := range e.studies {
			studies[i] = strconv.Itoa(n)
		}
		fmt.Fprintf(&b, "  experiment %-6s %s  %-7s  %4d runs  %4d imported  %3d cells  %2d comparisons\n",
			strings.Join(studies, ","), e.id, state, len(e.runs), e.imported, e.cells, e.comparisons)
	}
	return strings.TrimRight(b.String(), "\n")
}

// suite are the digests of the applied suite: its own, and its tasks by
// name.
type suite struct {
	digest string
	tasks  map[string]string
}

// applySuite applies the metrics the first release carries, the six
// tasks with the study's prompts, and the suite goalless. An apply of
// content already applied makes nothing new, so a repeated run applies the
// same versions.
func (a *api) applySuite(s *study) suite {
	a.t.Helper()
	apply := func(kind, name string, body map[string]any) string {
		var v struct {
			Digest string `json:"digest"`
		}
		a.must(http.MethodPut, "/"+kind+"/"+name, body, &v, http.StatusCreated, http.StatusOK)
		return v.Digest
	}
	count := map[string]any{"type": "count", "aggregate": "median", "direction": "none"}
	binary := map[string]any{"type": "binary", "aggregate": "rate", "direction": "none"}
	metrics := []string{
		apply("metrics", "loc", count), apply("metrics", "loc_nonblank", count),
		apply("metrics", "implemented", binary), apply("metrics", "browser_interactive", binary),
		"builtin:completed", "builtin:duration",
	}
	out := suite{tasks: map[string]string{}}
	var taskDigests []string
	weights := map[string]float64{}
	for _, t := range tasks {
		d := apply("tasks", t.name, map[string]any{"instruction": s.instructions[t.name], "timeout": "60m"})
		out.tasks[t.name] = d
		taskDigests = append(taskDigests, d)
		weights[d] = 0
		if t.name == controlledTask {
			weights[d] = 1
		}
	}
	out.digest = apply("suites", "goalless", map[string]any{
		"tasks": taskDigests, "metrics": metrics, "primary_metric": "builtin:completed", "weights": weights,
	})
	return out
}

// experiment is one Examen experiment of the import: the study's
// experiments it holds, its id, its candidates by name, and its runs.
type experiment struct {
	key        int
	studies    []int
	id         string
	candidates map[string]string // candidate name to digest
	runs       []studyRun
	trials     map[string]string // a run's ref to its trial id
	// definition is the digest of everything the experiment holds: its
	// suite, its candidates, its N and every import body, so an experiment
	// an earlier run made of the same study and converter is found by it.
	definition string
	// found says an earlier run made the experiment.
	found bool
	// What this run did and checked.
	imported, present, cells, comparisons, rows int
}

// metaDefinition is the metadata key an experiment's definition is kept
// under, and metaStudies the one that names the study's experiments.
const (
	metaDefinition = "goalless.definition"
	metaStudies    = "goalless.experiment"
)

// importStudy makes or finds one experiment in mode import per study
// experiment, a merged pair as one, with one candidate per declared
// configuration in the order the runs first name them and N the most runs
// one cell holds; imports every run it does not already hold; and closes
// every intake, waiting for each experiment to complete before the next
// is made, so a ceiling on the owner's active experiments is never met by
// the import alone.
func (a *api) importStudy(s *study, su suite) []*experiment {
	a.t.Helper()
	byKey := map[int]*experiment{}
	var keys []int
	for _, r := range s.runs {
		e, ok := byKey[r.Experiment]
		if !ok {
			e = &experiment{key: r.Experiment, candidates: map[string]string{}, trials: map[string]string{}}
			byKey[r.Experiment] = e
			keys = append(keys, r.Experiment)
		}
		if !slices.Contains(e.studies, r.Study) {
			e.studies = append(e.studies, r.Study)
		}
		e.runs = append(e.runs, r)
	}
	sort.Ints(keys)
	applied := map[string]string{}
	var out []*experiment
	for _, k := range keys {
		e := byKey[k]
		var order []string
		perCell := map[[2]string]int{}
		for _, r := range e.runs {
			name := r.Config.name()
			if _, ok := applied[name]; !ok {
				var v struct {
					Digest string `json:"digest"`
				}
				a.must(http.MethodPut, "/candidates/"+name, map[string]any{"kind": "external", "external": r.Config}, &v, http.StatusCreated, http.StatusOK)
				applied[name] = v.Digest
			}
			if !slices.Contains(order, name) {
				order = append(order, name)
			}
			e.candidates[name] = applied[name]
			perCell[[2]string{r.Task, name}]++
		}
		n := slices.Max(slices.Collect(maps.Values(perCell)))
		var cands []string
		for _, name := range order {
			cands = append(cands, applied[name])
		}
		bodies := make([]map[string]any, len(e.runs))
		for i, r := range e.runs {
			bodies[i] = r.importBody(su.tasks[r.Task], applied[r.Config.name()])
		}
		e.definition = digest(map[string]any{"suite": su.digest, "candidates": cands, "trials": n, "imports": bodies})
		a.openExperiment(e, su.digest, cands, n)
		a.importRuns(e, bodies)
		a.closeExperiment(e)
		out = append(out, e)
	}
	return out
}

// digest is the first 32 hex digits of the SHA-256 of a value's JSON.
func digest(v any) string {
	b, err := json.Marshal(v)
	if err != nil {
		panic(err) // maps of strings, numbers and times always marshal
	}
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:16])
}

// listed is the part of a listed experiment the harness compares.
type listed struct {
	ID         string   `json:"id"`
	Suite      string   `json:"suite"`
	Candidates []string `json:"candidates"`
	Trials     int      `json:"trials"`
	Mode       string   `json:"mode"`
	State      string   `json:"state"`
}

// openExperiment finds the experiment an earlier run made of the same
// definition, one not canceled, or creates it. A create carries an
// Idempotency-Key of the definition, so a create whose answer was lost is
// answered again rather than made twice. A create refused because the
// owner holds as many active experiments as allowed waits, and is sent
// again under a new key, since a refusal is kept under its key and would
// be answered again.
func (a *api) openExperiment(e *experiment, suiteDigest string, cands []string, n int) {
	a.t.Helper()
	q := url.Values{"mode": {"import"}, "suite": {suiteDigest}, "metadata." + metaDefinition: {e.definition}}
	for _, x := range all[listed](a, "/experiments?"+q.Encode()) {
		if x.State != "canceled" && x.Mode == "import" && x.Suite == suiteDigest && slices.Equal(x.Candidates, cands) && x.Trials == n {
			e.id, e.found = x.ID, true
			return
		}
	}
	studies := make([]string, len(e.studies))
	for i, n := range e.studies {
		studies[i] = strconv.Itoa(n)
	}
	budget := map[string]any{"name": "goalless"}
	if a.remote {
		budget = map[string]any{"cap_usd_micro": importCapUSDMicro}
	}
	body := map[string]any{
		"suite": suiteDigest, "candidates": cands, "trials": n, "budget": budget, "mode": "import",
		"metadata": map[string]string{metaStudies: strings.Join(studies, ","), metaDefinition: e.definition},
	}
	deadline := time.Now().Add(activeTimeout)
	for attempt := 1; ; attempt++ {
		code, raw, header := a.do(http.MethodPost, "/experiments", body, "goalless-"+e.definition+"-"+strconv.Itoa(attempt))
		switch {
		case code == http.StatusCreated:
			var created struct {
				ID string `json:"id"`
			}
			if err := json.Unmarshal(raw, &created); err != nil {
				a.t.Fatalf("POST /experiments: %v: %s", err, raw)
			}
			e.id, e.found = created.ID, header.Get("Idempotent-Replayed") == "true"
			return
		case atActiveCeiling(code, raw) && time.Now().Before(deadline):
			a.t.Logf("experiment %s waits: the owner holds as many active experiments as allowed", strings.Join(studies, ","))
			time.Sleep(5 * time.Second)
		default:
			a.t.Fatalf("POST /experiments: %d %s", code, raw)
		}
	}
}

// atActiveCeiling reports whether a create was refused because the owner
// holds as many active experiments as the authorizer's limit, or the
// server's constant, allows.
func atActiveCeiling(code int, raw []byte) bool {
	e := errorOf(raw)
	return (code == http.StatusForbidden && e.Code == "forbidden" && e.Details.Reason == "max_active_experiments") ||
		(code == http.StatusUnprocessableEntity && e.Code == "limit_exceeded" && e.Details.Constant == "MAX_ACTIVE_EXPERIMENTS_PER_OWNER")
}

// importRuns imports every run the experiment does not already hold, by
// its ref, each under an Idempotency-Key of the experiment and the ref,
// and reads the trial id of every run.
func (a *api) importRuns(e *experiment, bodies []map[string]any) {
	a.t.Helper()
	held := a.trialRefs(e)
	for i, r := range e.runs {
		if id, ok := held[r.Ref]; ok {
			e.trials[r.Ref] = id
			e.present++
			continue
		}
		code, raw, header := a.do(http.MethodPost, "/experiments/"+e.id+"/imports", bodies[i], "goalless-"+digest([]string{e.id, r.Ref}))
		switch {
		case code == http.StatusCreated:
			var tr struct {
				ID string `json:"id"`
			}
			if err := json.Unmarshal(raw, &tr); err != nil {
				a.t.Fatalf("import %s: %v: %s", r.Ref, err, raw)
			}
			e.trials[r.Ref] = tr.ID
			if header.Get("Idempotent-Replayed") == "true" {
				e.present++
			} else {
				e.imported++
			}
		case code == http.StatusConflict && errorOf(raw).Code == "already_imported":
			e.present++
		default:
			a.t.Fatalf("import %s into %s: %d %s", r.Ref, e.id, code, raw)
		}
	}
	if len(e.trials) < len(e.runs) {
		held = a.trialRefs(e)
		for _, r := range e.runs {
			if e.trials[r.Ref] = held[r.Ref]; e.trials[r.Ref] == "" {
				a.t.Fatalf("experiment %s holds no trial of %s", e.id, r.Ref)
			}
		}
	}
}

// trialRefs reads an experiment's imported trials, by their ref.
func (a *api) trialRefs(e *experiment) map[string]string {
	a.t.Helper()
	out := map[string]string{}
	for _, t := range all[trial](a, "/experiments/"+e.id+"/trials?imported=true") {
		if t.Ref != "" {
			out[t.Ref] = t.ID
		}
	}
	return out
}

// closeExperiment ends an experiment's intake and waits for it to
// complete.
func (a *api) closeExperiment(e *experiment) {
	a.t.Helper()
	var x listed
	a.must(http.MethodGet, "/experiments/"+e.id, nil, &x, http.StatusOK)
	if x.State != "complete" {
		code, raw := a.call(http.MethodPost, "/experiments/"+e.id+"/close", nil)
		if code != http.StatusOK && (code != http.StatusConflict || errorOf(raw).Code != "experiment_finished") {
			a.t.Fatalf("closing %s: %d %s", e.id, code, raw)
		}
	}
	deadline := time.Now().Add(settleTimeout)
	for x.State != "complete" {
		if time.Now().After(deadline) {
			a.t.Fatalf("experiment %s is %s %v after its intake closed", e.id, x.State, settleTimeout)
		}
		time.Sleep(2 * time.Second)
		a.must(http.MethodGet, "/experiments/"+e.id, nil, &x, http.StatusOK)
	}
}

// comparison is the part of a comparison the harness reads.
type comparison struct {
	ID        string         `json:"id"`
	Candidate string         `json:"candidate"`
	State     string         `json:"state"`
	Final     bool           `json:"final"`
	Automatic bool           `json:"automatic"`
	Declared  []string       `json:"declared_provenance"`
	Results   []result       `json:"results"`
	Corr      map[string]any `json:"correction"`
}

type result struct {
	Level      string   `json:"level"`
	MetricName string   `json:"metric_name"`
	Task       string   `json:"task"`
	P          *float64 `json:"p"`
	Q          *float64 `json:"q"`
	Verdict    string   `json:"verdict"`
	Reason     string   `json:"reason"`
	Confounds  []struct {
		Name string `json:"name"`
	} `json:"confounds"`
}

type page[T any] struct {
	Items      []T    `json:"items"`
	NextCursor string `json:"next_cursor"`
}

// all reads every page of a list.
func all[T any](a *api, path string) []T {
	a.t.Helper()
	var out []T
	cursor := ""
	for {
		sep := "?"
		if strings.Contains(path, "?") {
			sep = "&"
		}
		p := path + sep + "limit=200"
		if cursor != "" {
			p += "&cursor=" + url.QueryEscape(cursor)
		}
		var pg page[T]
		a.must(http.MethodGet, p, nil, &pg, http.StatusOK)
		out = append(out, pg.Items...)
		if pg.NextCursor == "" {
			return out
		}
		cursor = pg.NextCursor
	}
}

// settle waits until every automatic comparison of every experiment is
// computed a last time, final.
func (a *api) settle(exps []*experiment) {
	a.t.Helper()
	deadline := time.Now().Add(settleTimeout)
	for _, e := range exps {
		for {
			cs := all[comparison](a, "/experiments/"+e.id+"/comparisons?automatic=true")
			done := true
			for _, c := range cs {
				done = done && c.Final && c.State == "complete"
			}
			if done {
				break
			}
			if time.Now().After(deadline) {
				a.t.Fatalf("the automatic comparisons of %s are not final after %v: %+v", e.id, settleTimeout, cs)
			}
			time.Sleep(2 * time.Second)
		}
	}
}

// report logs, per study experiment, the run directories read, the runs
// imported and those that ended infra_failed.
func report(t *testing.T, s *study, exps []*experiment) {
	t.Helper()
	imported, failed := map[int]int{}, map[int]int{}
	for _, r := range s.runs {
		imported[r.Study]++
		if r.Outcome == "infra_failed" {
			failed[r.Study]++
		}
	}
	var lines []string
	for _, n := range slices.Sorted(maps.Keys(s.dirs)) {
		lines = append(lines, fmt.Sprintf("results%-2d  %4d read  %4d imported  %3d infra_failed", n, s.dirs[n], imported[n], failed[n]))
	}
	t.Logf("%d experiments hold the study:\n%s", len(exps), strings.Join(lines, "\n"))
}

// gridCell is the part of a grid entry the harness reads.
type gridCell struct {
	Cell      string `json:"cell"`
	Candidate string `json:"candidate"`
	Trials    struct {
		Done     int            `json:"done"`
		Outcomes map[string]int `json:"outcomes"`
	} `json:"trials"`
	Summary *summary `json:"summary"`
	Verdict string   `json:"verdict"`
	Reason  string   `json:"reason"`
}

type summary struct {
	N           int      `json:"n"`
	Value       *float64 `json:"value"`
	Numerator   *int     `json:"numerator"`
	Denominator int      `json:"denominator"`
	Missing     int      `json:"missing"`
	Mean        *float64 `json:"mean"`
}

type gridRow struct {
	Task struct {
		Name string `json:"name"`
	} `json:"task"`
	Cells []gridCell `json:"cells"`
}

// grid reads an experiment's grid of one metric, by task and candidate
// digest.
func (a *api) grid(e *experiment, metric string) map[[2]string]gridCell {
	a.t.Helper()
	out := map[[2]string]gridCell{}
	for _, row := range all[gridRow](a, "/experiments/"+e.id+"/grid?metric="+metric) {
		for _, c := range row.Cells {
			out[[2]string{row.Task.Name, c.Candidate}] = c
		}
	}
	return out
}

// cellRuns groups an experiment's runs by task and candidate digest.
func (e *experiment) cellRuns() map[[2]string][]studyRun {
	out := map[[2]string][]studyRun{}
	for _, r := range e.runs {
		k := [2]string{r.Task, e.candidates[r.Config.name()]}
		out[k] = append(out[k], r)
	}
	return out
}

// quantile7 is the type 7 quantile of sorted values.
func quantile7(sorted []float64, q float64) float64 {
	h := float64(len(sorted)-1) * q
	lo := math.Floor(h)
	if int(lo)+1 >= len(sorted) {
		return sorted[len(sorted)-1]
	}
	return sorted[int(lo)] + (h-lo)*(sorted[int(lo)+1]-sorted[int(lo)])
}

// checkExperiment holds each cell of an experiment to its runs: its trials
// and outcomes, and its summaries of lines of code and of a source file
// to the values the converter computed, over its finished runs. It answers
// how many cells it held.
func (a *api) checkExperiment(e *experiment) int {
	a.t.Helper()
	loc, implemented := a.grid(e, "loc"), a.grid(e, "implemented")
	cells := e.cellRuns()
	if len(loc) != len(cells) {
		a.t.Errorf("experiment %v: the grid holds %d cells, the runs make %d", e.studies, len(loc), len(cells))
	}
	held := 0
	for k, runs := range cells {
		g, ok := loc[k]
		if !ok {
			a.t.Errorf("experiment %v: no cell of %s and %s", e.studies, k[0], k[1])
			continue
		}
		held++
		outcomes := map[string]int{}
		var values []float64
		finished, sources := 0, 0
		for _, r := range runs {
			outcomes[r.Outcome]++
			if r.Outcome == "infra_failed" {
				continue
			}
			finished++
			if r.LOC != nil {
				values = append(values, float64(*r.LOC))
			}
			if r.Implemented {
				sources++
			}
		}
		where := fmt.Sprintf("experiment %v, %s, %s", e.studies, k[0], runs[0].Ref)
		if g.Trials.Done != len(runs) || !maps.Equal(g.Trials.Outcomes, outcomes) {
			a.t.Errorf("%s: %d trials done %v, the runs %d %v", where, g.Trials.Done, g.Trials.Outcomes, len(runs), outcomes)
		}
		s := g.Summary
		if s == nil || s.N != finished || s.Denominator != len(values) || s.Missing != finished-len(values) {
			a.t.Errorf("%s: loc summary %+v, want n %d over %d values", where, s, finished, len(values))
			continue
		}
		if len(values) > 0 {
			sort.Float64s(values)
			mean := 0.0
			for _, v := range values {
				mean += v
			}
			mean /= float64(len(values))
			if s.Value == nil || math.Abs(*s.Value-quantile7(values, 0.5)) > 1e-9 || s.Mean == nil || math.Abs(*s.Mean-mean) > 1e-9 {
				a.t.Errorf("%s: loc median %v mean %v, the values give %v and %v", where, s.Value, s.Mean, quantile7(values, 0.5), mean)
			}
		}
		// A cell with no finished run has no value, and so no numerator.
		if im := implemented[k].Summary; im == nil || im.Denominator != finished || (im.Numerator == nil) != (finished == 0) || (im.Numerator != nil && *im.Numerator != sources) {
			a.t.Errorf("%s: implemented %+v, want %d of %d", where, im, sources, finished)
		}
		if g.Verdict != "" && g.Verdict != "insufficient" {
			a.t.Errorf("%s: a self-reported value has the verdict %s", where, g.Verdict)
		}
	}
	return held
}

// checkNeverHeadlined holds every result of every final comparison of an
// experiment that reads a value the converter reported as the harness's
// own: it is never a finding, so it has no q and no verdict but
// insufficient, and a p it keeps comes with the self_reported confound
// and a reason that says so. Every comparison of imported candidates
// names both sides declared. It answers the comparisons and the results it
// read.
func (a *api) checkNeverHeadlined(e *experiment) (comparisons, rows int) {
	a.t.Helper()
	for _, c := range all[comparison](a, "/experiments/"+e.id+"/comparisons") {
		comparisons++
		if len(c.Declared) != 2 {
			a.t.Errorf("%s: declared provenance %v", c.ID, c.Declared)
		}
		read := append(slices.Clone(c.Results), all[result](a, "/comparisons/"+c.ID+"/rows")...)
		rows += len(read)
		for _, r := range read {
			if !slices.Contains(selfReported, r.MetricName) {
				continue
			}
			named := slices.ContainsFunc(r.Confounds, func(cf struct {
				Name string `json:"name"`
			}) bool {
				return cf.Name == "self_reported"
			})
			if r.Q != nil || (r.Verdict != "" && r.Verdict != "insufficient") || (r.P != nil && (!named || !strings.Contains(r.Reason, "not a finding"))) {
				a.t.Errorf("%s: %s %s %s is headlined: q %v verdict %q reason %q", c.ID, r.MetricName, r.Level, r.Task, r.Q, r.Verdict, r.Reason)
			}
		}
	}
	return comparisons, rows
}
