// SPDX-FileCopyrightText: 2026 Latere AI
// SPDX-License-Identifier: Apache-2.0

// Package goalless_test imports the goalless-agents study into a local
// examend, as declared past trials of an external harness, and holds what
// Examen computes from them to what the study's files say. It reads the
// study from GOALLESS_DIR and is skipped without it; see README.md.
package goalless_test

import (
	"bufio"
	"bytes"
	"context"
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
// computed a last time after every intake is closed; the server's work
// ticks every REPORT_INTERVAL.
const settleTimeout = 10 * time.Minute

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
	read := 0
	for _, n := range s.dirs {
		read += n
	}
	if read != len(s.runs)+len(s.skipped) {
		t.Fatalf("read %d run directories, imported %d and skipped %d", read, len(s.runs), len(s.skipped))
	}
	for _, sk := range s.skipped {
		t.Logf("not imported: %s (%s)", sk.Ref, sk.Reason)
	}

	a := startServer(t)
	digests := a.applySuite(s)
	exps := a.importStudy(s, digests)
	a.settle(exps)
	report(t, s, exps)

	if orc != nil {
		if orc.RunDirectories != 0 && read != orc.RunDirectories {
			t.Errorf("read %d run directories; the study holds %d", read, orc.RunDirectories)
		}
		if orc.Experiments != 0 && len(exps) != orc.Experiments {
			t.Errorf("imported into %d experiments; the study's figures name %d", len(exps), orc.Experiments)
		}
	}
	for _, e := range exps {
		a.checkExperiment(e)
		a.checkNeverHeadlined(e)
	}
	if orc != nil {
		a.checkOracle(orc, exps)
	}
}

// api is a client of the local examend.
type api struct {
	t     *testing.T
	base  string
	token string
}

// call sends one request and answers its status and body, waiting out a
// 429 for as long as its Retry-After asks, as any client does.
func (a *api) call(method, path string, body any) (int, []byte) {
	a.t.Helper()
	var raw []byte
	if body != nil {
		var err error
		if raw, err = json.Marshal(body); err != nil {
			a.t.Fatal(err)
		}
	}
	for {
		req, err := http.NewRequestWithContext(a.t.Context(), method, a.base+path, bytes.NewReader(raw))
		if err != nil {
			a.t.Fatal(err)
		}
		req.Header.Set("Authorization", "Bearer "+a.token)
		if body != nil {
			req.Header.Set("Content-Type", "application/json")
		}
		res, err := http.DefaultClient.Do(req)
		if err != nil {
			a.t.Fatal(err)
		}
		out, err := io.ReadAll(res.Body)
		if cerr := res.Body.Close(); err == nil {
			err = cerr
		}
		if err != nil {
			a.t.Fatal(err)
		}
		if res.StatusCode == http.StatusTooManyRequests {
			wait, err := strconv.Atoi(res.Header.Get("Retry-After"))
			if err != nil || wait < 1 {
				wait = 1
			}
			time.Sleep(time.Duration(wait) * time.Second)
			continue
		}
		return res.StatusCode, out
	}
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

// startServer builds examend, migrates a database of its own, and serves
// it with the built-in owner policy and a test issuer, until the test
// ends.
func startServer(t *testing.T) *api {
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
	ctx, cancel := context.WithCancel(context.Background())
	serve := exec.CommandContext(ctx, bin, "serve")
	serve.Env = append(slices.Clone(base), "EXAMEN_ADDR=127.0.0.1:0", "EXAMEN_DATABASE_URL="+direct,
		"EXAMEN_DATABASE_POOL_URL="+pooled, "EXAMEN_OIDC_ISSUERS="+iss.URL(), "EXAMEN_LOG_LEVEL=info")
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
	return &api{t: t, base: "http://" + addr, token: iss.Mint(issuertest.Claims{Sub: "goalless-owner"})}
}

// suite are the digests of the applied suite: its own, and its tasks by
// name.
type suite struct {
	digest string
	tasks  map[string]string
}

// applySuite applies the metrics the first release carries, the six
// tasks with the study's prompts, and the suite goalless.
func (a *api) applySuite(s *study) suite {
	a.t.Helper()
	apply := func(kind, name string, body map[string]any) string {
		var v struct {
			Digest string `json:"digest"`
		}
		a.must(http.MethodPut, "/v1/"+kind+"/"+name, body, &v, http.StatusCreated, http.StatusOK)
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
}

// importStudy creates one experiment in mode import per study experiment,
// a merged pair as one, with one candidate per declared configuration in
// the order the runs first name them and N the most runs one cell holds;
// imports every run; and closes every intake.
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
				a.must(http.MethodPut, "/v1/candidates/"+name, map[string]any{"kind": "external", "external": r.Config}, &v, http.StatusCreated, http.StatusOK)
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
		studies := make([]string, len(e.studies))
		for i, n := range e.studies {
			studies[i] = strconv.Itoa(n)
		}
		var created struct {
			ID string `json:"id"`
		}
		a.must(http.MethodPost, "/v1/experiments", map[string]any{
			"suite": su.digest, "candidates": cands, "trials": n, "budget": map[string]any{"name": "goalless"}, "mode": "import",
			"metadata": map[string]string{"goalless.experiment": strings.Join(studies, ",")},
		}, &created, http.StatusCreated)
		e.id = created.ID
		for _, r := range e.runs {
			var tr struct {
				ID string `json:"id"`
			}
			a.must(http.MethodPost, "/v1/experiments/"+e.id+"/imports", r.importBody(su.tasks[r.Task], applied[r.Config.name()]), &tr, http.StatusCreated)
			e.trials[r.Ref] = tr.ID
		}
		a.must(http.MethodPost, "/v1/experiments/"+e.id+"/close", nil, nil, http.StatusOK)
		out = append(out, e)
	}
	return out
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
			cs := all[comparison](a, "/v1/experiments/"+e.id+"/comparisons?automatic=true")
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
	t.Logf("%d experiments imported:\n%s", len(exps), strings.Join(lines, "\n"))
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
	for _, row := range all[gridRow](a, "/v1/experiments/"+e.id+"/grid?metric="+metric) {
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
// to the values the converter computed, over its finished runs.
func (a *api) checkExperiment(e *experiment) {
	a.t.Helper()
	loc, implemented := a.grid(e, "loc"), a.grid(e, "implemented")
	cells := e.cellRuns()
	if len(loc) != len(cells) {
		a.t.Errorf("experiment %v: the grid holds %d cells, the runs make %d", e.studies, len(loc), len(cells))
	}
	for k, runs := range cells {
		g, ok := loc[k]
		if !ok {
			a.t.Errorf("experiment %v: no cell of %s and %s", e.studies, k[0], k[1])
			continue
		}
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
}

// checkNeverHeadlined holds every result of every final comparison of an
// experiment that reads a value the converter reported as the harness's
// own: it is never a finding, so it has no q and no verdict but
// insufficient, and a p it keeps comes with the self_reported confound
// and a reason that says so. Every comparison of imported candidates
// names both sides declared.
func (a *api) checkNeverHeadlined(e *experiment) {
	a.t.Helper()
	for _, c := range all[comparison](a, "/v1/experiments/"+e.id+"/comparisons") {
		if len(c.Declared) != 2 {
			a.t.Errorf("%s: declared provenance %v", c.ID, c.Declared)
		}
		rows := append(slices.Clone(c.Results), all[result](a, "/v1/comparisons/"+c.ID+"/rows")...)
		for _, r := range rows {
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
}
