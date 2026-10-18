// SPDX-FileCopyrightText: 2026 Latere AI
// SPDX-License-Identifier: Apache-2.0

// Package live_test checks a deployed examend from outside, as any client
// does: its probes and its contract, its refusals of a bad bearer and of
// work it cannot run, and one small experiment of two external candidates
// run end to end through the lease, renewal and submit an external harness
// uses, down to the grid and the comparison Examen computes from the
// values the test submitted. It calls no model.
//
// It is skipped unless EXAMEN_E2E_URL names the server's base URL, its
// base path included, and EXAMEN_E2E_TOKEN holds a bearer for it, or
// EXAMEN_E2E_TOKEN_FILE a file that holds one, read again before every
// request so another process can keep a short-lived bearer fresh.
// EXAMEN_E2E_PROBES_URL is where the probes answer, the URL's origin when
// unset. Every object it applies is named smoke-<time>-<random> and left
// in place, and its experiment ends complete. It waits for a lease to
// lapse, EXTERNAL_LEASE, so a run takes a little longer than that.
package live_test

import (
	"bytes"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"
)

// The experiment the test runs: one task, two external candidates, N
// trials each, the first candidate the reference.
const (
	trialsPerCell = 5
	parallelism   = 4
	// capUSDMicro is the experiment's spending cap, one cent: nothing the
	// test does calls a model.
	capUSDMicro = 10_000
)

// passes is the value the test submits for a trial of a candidate, by the
// trial's index in its cell: the reference passes four of five, the other
// candidate two of five.
func passes(reference bool, index int) bool {
	if reference {
		return index != 4
	}
	return index == 0 || index == 2
}

// The figures the submitted values make, which the grid and the comparison
// must show exactly.
const (
	referencePassed = 4
	candidatePassed = 2
)

// Deadlines of the waits.
const (
	leaseWait    = 3 * time.Minute // for a cell's trials to be dealt
	lapseMargin  = 5 * time.Minute // past a lease's expires_at, for the server to return its trial
	computeWait  = 5 * time.Minute // for the experiment to complete and its comparison to be final
	pollInterval = 2 * time.Second
)

// client is a client of the server under test.
type client struct {
	t                *testing.T
	base, probes     string
	ownProbes        bool
	token, tokenFile string
}

func newClient(t *testing.T) *client {
	t.Helper()
	base, token, file := os.Getenv("EXAMEN_E2E_URL"), os.Getenv("EXAMEN_E2E_TOKEN"), os.Getenv("EXAMEN_E2E_TOKEN_FILE")
	if base == "" || (token == "" && file == "") {
		t.Skip("EXAMEN_E2E_URL and EXAMEN_E2E_TOKEN or EXAMEN_E2E_TOKEN_FILE name no server to check")
	}
	if token != "" && file != "" {
		t.Fatal("EXAMEN_E2E_TOKEN and EXAMEN_E2E_TOKEN_FILE are both set; keep one")
	}
	u, err := url.Parse(base)
	if err != nil || (u.Scheme != "https" && u.Scheme != "http") || u.Host == "" || u.RawQuery != "" || u.Fragment != "" {
		t.Fatalf("EXAMEN_E2E_URL is not an http or https URL with a host and no query: %q", base)
	}
	c := &client{t: t, base: strings.TrimRight(base, "/"), token: token, tokenFile: file}
	// The probes sit at the root of examend's listener. At the URL's origin
	// they are examend's when the API is served at the root or under the
	// default /v1; under a base path of its own, the origin may route the
	// root to another service, and only EXAMEN_E2E_PROBES_URL reaches
	// examend's.
	if p := os.Getenv("EXAMEN_E2E_PROBES_URL"); p != "" {
		c.probes, c.ownProbes = strings.TrimRight(p, "/"), true
	} else {
		c.probes = u.Scheme + "://" + u.Host
		c.ownProbes = c.basePath() == "" || c.basePath() == "/v1"
	}
	return c
}

// basePath is the path of EXAMEN_E2E_URL, the server's base path.
func (c *client) basePath() string {
	u, err := url.Parse(c.base)
	if err != nil {
		c.t.Fatal(err)
	}
	return strings.TrimRight(u.Path, "/")
}

func (c *client) bearer() string {
	c.t.Helper()
	if c.tokenFile == "" {
		return c.token
	}
	b, err := os.ReadFile(c.tokenFile)
	if err != nil {
		c.t.Fatalf("reading EXAMEN_E2E_TOKEN_FILE: %v", err)
	}
	return strings.TrimSpace(string(b))
}

// answer is one response.
type answer struct {
	status int
	body   []byte
}

// errorCode is an error answer's code and details.
type errorCode struct {
	Code    string `json:"code"`
	Details struct {
		Reason string `json:"reason"`
		Detail string `json:"detail"`
	} `json:"details"`
}

func (a answer) err() errorCode {
	var e struct {
		Error errorCode `json:"error"`
	}
	_ = json.Unmarshal(a.body, &e) // a body that is not an error reads as no code
	return e.Error
}

// raw sends one request to a full URL with the given Authorization header,
// none when empty, waiting out a 429 for as long as its Retry-After asks.
func (c *client) raw(method, target, authorization string, body any) answer {
	c.t.Helper()
	var payload []byte
	if body != nil {
		var err error
		if payload, err = json.Marshal(body); err != nil {
			c.t.Fatal(err)
		}
	}
	for {
		req, err := http.NewRequestWithContext(c.t.Context(), method, target, bytes.NewReader(payload))
		if err != nil {
			c.t.Fatal(err)
		}
		if authorization != "" {
			req.Header.Set("Authorization", authorization)
		}
		if body != nil {
			req.Header.Set("Content-Type", "application/json")
		}
		res, err := http.DefaultClient.Do(req)
		if err != nil {
			c.t.Fatalf("%s %s: %v", method, target, err)
		}
		out, err := io.ReadAll(res.Body)
		if cerr := res.Body.Close(); err == nil {
			err = cerr
		}
		if err != nil {
			c.t.Fatalf("%s %s: %v", method, target, err)
		}
		if res.StatusCode == http.StatusTooManyRequests {
			wait, err := strconv.Atoi(res.Header.Get("Retry-After"))
			if err != nil || wait < 1 {
				wait = 1
			}
			time.Sleep(time.Duration(wait) * time.Second)
			continue
		}
		return answer{res.StatusCode, out}
	}
}

// do sends one request below the base path with the bearer.
func (c *client) do(method, path string, body any) answer {
	c.t.Helper()
	return c.raw(method, c.base+path, "Bearer "+c.bearer(), body)
}

// must sends a request that must answer status and decodes its body.
func (c *client) must(status int, method, path string, body, out any) {
	c.t.Helper()
	a := c.do(method, path, body)
	if a.status != status {
		c.t.Fatalf("%s %s: %d %s, want %d", method, path, a.status, a.body, status)
	}
	if out != nil {
		if err := json.Unmarshal(a.body, out); err != nil {
			c.t.Fatalf("%s %s: %v: %s", method, path, err, a.body)
		}
	}
}

// refuse sends a request that must be refused with status and code.
func (c *client) refuse(status int, code, method, path string, body any) {
	c.t.Helper()
	a := c.do(method, path, body)
	if a.status != status || a.err().Code != code {
		c.t.Fatalf("%s %s: %d %s, want %d %s", method, path, a.status, a.body, status, code)
	}
}

// apply applies one version and answers its digest.
func (c *client) apply(kind, name string, body map[string]any) string {
	c.t.Helper()
	var v struct {
		Digest string `json:"digest"`
	}
	c.must(http.StatusCreated, http.MethodPut, "/"+kind+"/"+name, body, &v)
	return v.Digest
}

// leased is a lease's answer.
type leased struct {
	Lease     string    `json:"lease"`
	ExpiresAt time.Time `json:"expires_at"`
	Trials    []struct {
		ID    string `json:"id"`
		Index int    `json:"index"`
	} `json:"trials"`
}

func (l leased) ids() []string {
	out := make([]string, len(l.Trials))
	for i, t := range l.Trials {
		out[i] = t.ID
	}
	slices.Sort(out)
	return out
}

// lease leases a candidate's pending trials, at most max when max is not
// 0, waiting while none may be leased yet: a cell's main trials are dealt
// only after its smoke trial.
func (c *client) lease(eid, candidate string, max int) leased {
	c.t.Helper()
	body := map[string]any{"candidate": candidate}
	if max > 0 {
		body["max"] = max
	}
	deadline := time.Now().Add(leaseWait)
	for {
		a := c.do(http.MethodPost, "/experiments/"+eid+"/work/lease", body)
		switch {
		case a.status == http.StatusOK:
			var l leased
			if err := json.Unmarshal(a.body, &l); err != nil {
				c.t.Fatalf("lease: %v: %s", err, a.body)
			}
			return l
		case a.status != http.StatusNoContent:
			c.t.Fatalf("lease of %s: %d %s", candidate, a.status, a.body)
		case time.Now().After(deadline):
			c.t.Fatalf("no trial of %s could be leased within %v", candidate, leaseWait)
		}
		time.Sleep(pollInterval)
	}
}

// submission is a submit body: a completed trial with its self-reported
// value.
func submission(lease, metric string, pass bool) map[string]any {
	ended := time.Now().UTC().Add(-time.Minute).Truncate(time.Second)
	return map[string]any{
		"lease": lease, "outcome": "completed", "started_at": ended.Add(-time.Minute), "agent_ended_at": ended,
		"measurements": []map[string]any{{"metric": metric, "value": pass}},
	}
}

// summary is the part of a summary the test reads.
type summary struct {
	N           int      `json:"n"`
	Value       *float64 `json:"value"`
	Numerator   *int     `json:"numerator"`
	Denominator int      `json:"denominator"`
	Missing     int      `json:"missing"`
}

// holds reports whether a rate's summary is passed of trialsPerCell.
func (s *summary) holds(passed int) bool {
	return s != nil && s.N == trialsPerCell && s.Denominator == trialsPerCell && s.Missing == 0 &&
		s.Numerator != nil && *s.Numerator == passed && s.Value != nil && *s.Value == float64(passed)/trialsPerCell
}

func (s *summary) String() string {
	if s == nil {
		return "no summary"
	}
	if s.Numerator == nil || s.Value == nil {
		return fmt.Sprintf("no value (n %d, %d missing)", s.N, s.Missing)
	}
	return fmt.Sprintf("%d/%d = %v (n %d)", *s.Numerator, s.Denominator, *s.Value, s.N)
}

// result is the part of a comparison's result the test reads.
type result struct {
	Level      string   `json:"level"`
	MetricName string   `json:"metric_name"`
	Candidate  *summary `json:"candidate"`
	Reference  *summary `json:"reference"`
	Delta      *float64 `json:"delta"`
	DeltaKind  string   `json:"delta_kind"`
	P          *float64 `json:"p"`
	Q          *float64 `json:"q"`
	Verdict    string   `json:"verdict"`
	Reason     string   `json:"reason"`
	Confounds  []struct {
		Name string `json:"name"`
	} `json:"confounds"`
}

type comparison struct {
	ID        string   `json:"id"`
	Candidate string   `json:"candidate"`
	State     string   `json:"state"`
	Final     bool     `json:"final"`
	Declared  []string `json:"declared_provenance"`
	Results   []result `json:"results"`
}

type page[T any] struct {
	Items      []T    `json:"items"`
	NextCursor string `json:"next_cursor"`
}

func TestLiveSmoke(t *testing.T) {
	c := newClient(t)
	var report []string
	note := func(format string, args ...any) { report = append(report, fmt.Sprintf(format, args...)) }
	defer func() { t.Logf("live smoke report, against %s\n  %s", c.base, strings.Join(report, "\n  ")) }()

	note("probes       %s", c.checkProbes())
	note("contract     %s", c.checkContract())
	note("bearers      %s", c.checkBearers())

	suffix := make([]byte, 3)
	if _, err := rand.Read(suffix); err != nil {
		t.Fatal(err)
	}
	run := "smoke-" + time.Now().UTC().Format("20060102t150405") + "-" + hex.EncodeToString(suffix)
	metric := strings.ReplaceAll(run, "-", "_") + "_pass"
	metricDigest := c.apply("metrics", metric, map[string]any{
		"type": "binary", "aggregate": "rate", "direction": "higher_is_better",
		"description": "Whether a trial of a smoke test passed, as the test itself reports it.",
	})
	task := c.apply("tasks", run+"-task", map[string]any{
		"instruction": "Reply with ok. This task belongs to a smoke test of the deployment; only the test runs it.", "timeout": "5m",
	})
	suite := c.apply("suites", run, map[string]any{
		"tasks": []string{task}, "metrics": []string{metricDigest, "builtin:completed", "builtin:duration"}, "primary_metric": "builtin:completed",
	})
	external := func(side string) map[string]any {
		return map[string]any{"kind": "external", "external": map[string]any{"harness": "examen-smoke", "version": "1", "flags": map[string]string{"side": side}}}
	}
	ref, cand := c.apply("candidates", run+"-a", external("reference")), c.apply("candidates", run+"-b", external("candidate"))
	note("objects      metric %s, task, suite and candidates %s-a and %s-b, left in place", metric, run, run)

	note("topos        %s", c.checkToposRefused(run, suite))

	var exp struct {
		ID    string `json:"id"`
		State string `json:"state"`
	}
	c.must(http.StatusCreated, http.MethodPost, "/experiments", map[string]any{
		"suite": suite, "candidates": []string{ref, cand}, "trials": trialsPerCell, "parallelism": parallelism,
		"budget": map[string]any{"cap_usd_micro": capUSDMicro}, "metadata": map[string]string{"smoke.run": run},
	}, &exp)
	eid := exp.ID

	// The lease that lapses: one trial of the candidate, leased first and
	// never submitted under it.
	lapsing := c.lease(eid, cand, 1)
	if len(lapsing.Trials) != 1 {
		t.Fatalf("a lease of at most 1 holds %d trials", len(lapsing.Trials))
	}
	lapsed := lapsing.Trials[0].ID

	// The reference's trials, one lease renewed before its trials are
	// submitted.
	submitted := map[string]bool{}
	renewed := false
	for len(submitted) < trialsPerCell {
		l := c.lease(eid, ref, 0)
		if !renewed {
			time.Sleep(1100 * time.Millisecond) // a renewal's expires_at is later than the lease's
			var r leased
			c.must(http.StatusOK, http.MethodPost, "/experiments/"+eid+"/work/lease", map[string]any{"renew": l.Lease}, &r)
			if !r.ExpiresAt.After(l.ExpiresAt) || !slices.Equal(r.ids(), l.ids()) {
				t.Fatalf("the renewal of %s: %+v; the lease %+v", l.Lease, r, l)
			}
			renewed = true
		}
		for _, tr := range l.Trials {
			c.must(http.StatusOK, http.MethodPost, "/trials/"+tr.ID+"/submit", submission(l.Lease, metricDigest, passes(true, tr.Index)), nil)
			submitted[tr.ID] = true
		}
	}

	// The lapse: the server returns the trial to pending once its lease
	// expires, and a renewal or a submit under the lease is refused.
	deadline := lapsing.ExpiresAt.Add(lapseMargin)
	for {
		var tr struct {
			State string          `json:"state"`
			Lease json.RawMessage `json:"lease"`
		}
		c.must(http.StatusOK, http.MethodGet, "/trials/"+lapsed, nil, &tr)
		if tr.State == "pending" && (len(tr.Lease) == 0 || string(tr.Lease) == "null") {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("trial %s is %s with lease %s at %v past its lease's expiry", lapsed, tr.State, tr.Lease, lapseMargin)
		}
		time.Sleep(15 * time.Second)
	}
	c.refuse(http.StatusConflict, "lease_expired", http.MethodPost, "/experiments/"+eid+"/work/lease", map[string]any{"renew": lapsing.Lease})
	c.refuse(http.StatusConflict, "lease_expired", http.MethodPost, "/trials/"+lapsed+"/submit", submission(lapsing.Lease, metricDigest, true))

	// The candidate's trials, the lapsed one leased again among them.
	again := false
	for count := 0; count < trialsPerCell; {
		l := c.lease(eid, cand, 0)
		for _, tr := range l.Trials {
			again = again || tr.ID == lapsed
			c.must(http.StatusOK, http.MethodPost, "/trials/"+tr.ID+"/submit", submission(l.Lease, metricDigest, passes(false, tr.Index)), nil)
			count++
		}
	}
	if !again {
		t.Fatalf("trial %s was not leased again after its lease lapsed", lapsed)
	}
	note("experiment   %s: %d trials submitted; a lease renewed; %s lapsed, its renewal and submit refused lease_expired, its trial leased again",
		eid, 2*trialsPerCell, lapsing.Lease)

	deadline = time.Now().Add(computeWait)
	for exp.State != "complete" {
		if time.Now().After(deadline) {
			t.Fatalf("experiment %s is %s %v after its last submit", eid, exp.State, computeWait)
		}
		time.Sleep(pollInterval)
		c.must(http.StatusOK, http.MethodGet, "/experiments/"+eid, nil, &exp)
	}
	note("grid         %s", c.checkGrid(eid, metric, ref, cand))
	note("comparison   %s", c.checkComparison(eid, metric, cand, deadline))
}

// checkProbes reads /livez, /readyz and /version, which take no token.
func (c *client) checkProbes() string {
	c.t.Helper()
	live := c.raw(http.MethodGet, c.probes+"/livez", "", nil)
	ready := c.raw(http.MethodGet, c.probes+"/readyz", "", nil)
	version := c.raw(http.MethodGet, c.probes+"/version", "", nil)
	var build struct {
		Version   string `json:"version"`
		Commit    string `json:"commit"`
		BuildTime string `json:"build_time"`
	}
	parsed := json.Unmarshal(version.body, &build) == nil && build.Version != ""
	if !c.ownProbes {
		return fmt.Sprintf("not checked: %s serves the API under %s, and its root answered /livez %d, /readyz %d, /version %d %s, "+
			"which need not be examend's; set EXAMEN_E2E_PROBES_URL to examend's own address", c.probes, c.basePath(),
			live.status, ready.status, version.status, strings.TrimSpace(string(version.body)))
	}
	if live.status != http.StatusOK || ready.status != http.StatusOK || version.status != http.StatusOK || !parsed {
		c.t.Fatalf("the probes at %s: /livez %d %s, /readyz %d %s, /version %d %s", c.probes,
			live.status, live.body, ready.status, ready.body, version.status, version.body)
	}
	return fmt.Sprintf("/livez 200, /readyz 200, /version %s (%s, %s), at %s", build.Version, build.Commit, build.BuildTime, c.probes)
}

// checkContract reads the contract, which takes no token, and holds its
// servers entry to the URL's base path.
func (c *client) checkContract() string {
	c.t.Helper()
	a := c.raw(http.MethodGet, c.base+"/openapi.yaml", "", nil)
	path := c.basePath()
	if path == "" {
		path = "/"
	}
	if a.status != http.StatusOK || !bytes.HasPrefix(a.body, []byte("openapi: 3.1.0\n")) || !bytes.Contains(a.body, []byte("servers:\n  - url: "+path+"\n")) {
		c.t.Fatalf("GET %s/openapi.yaml: %d, %d bytes beginning %.80q; want the contract with the server at %s", c.base, a.status, len(a.body), a.body, path)
	}
	return fmt.Sprintf("/openapi.yaml 200, %d bytes, servers %s", len(a.body), path)
}

// checkBearers sends a request with no bearer and one with a bearer that
// is no token.
func (c *client) checkBearers() string {
	c.t.Helper()
	none := c.raw(http.MethodGet, c.base+"/experiments", "", nil)
	bad := c.raw(http.MethodGet, c.base+"/experiments", "Bearer smoke-test-not-a-token", nil)
	if none.status != http.StatusUnauthorized || none.err().Code != "missing_token" || bad.status != http.StatusUnauthorized || bad.err().Code != "invalid_token" {
		c.t.Fatalf("GET /experiments: no bearer %d %s, a bad one %d %s; want 401 missing_token and invalid_token", none.status, none.body, bad.status, bad.body)
	}
	return "no bearer 401 missing_token; a bad bearer 401 invalid_token"
}

// checkToposRefused applies a topos candidate and creates an experiment
// with it, which is refused, and no experiment of it is stored. Which
// refusal comes first depends on who decides: an authorizer that keeps no
// sign-in of the caller for unattended work denies sign_in_not_kept, one
// that names the executors it runs refuses forbidden with the reason
// executors, and otherwise examend itself answers executor_unavailable,
// since this release runs no topos candidate.
func (c *client) checkToposRefused(run, suite string) string {
	c.t.Helper()
	topos := c.apply("candidates", run+"-topos", map[string]any{"kind": "topos", "topos": map[string]any{
		"agent": "sha256:" + strings.Repeat("0", 64), "model": "claude-haiku-5-5",
	}})
	a := c.do(http.MethodPost, "/experiments", map[string]any{
		"suite": suite, "candidates": []string{topos}, "trials": 1,
		"budget": map[string]any{"cap_usd_micro": capUSDMicro}, "metadata": map[string]string{"smoke.run": run},
	})
	e := a.err()
	refused := (a.status == http.StatusUnprocessableEntity && e.Code == "executor_unavailable") ||
		(a.status == http.StatusForbidden && e.Code == "sign_in_not_kept") ||
		(a.status == http.StatusForbidden && e.Code == "forbidden" && e.Details.Reason == "executors")
	if !refused {
		c.t.Fatalf("an experiment of a topos candidate: %d %s; want 422 executor_unavailable, 403 sign_in_not_kept or 403 forbidden for executors", a.status, a.body)
	}
	var stored page[struct {
		ID string `json:"id"`
	}]
	c.must(http.StatusOK, http.MethodGet, "/experiments?candidate="+url.QueryEscape(topos), nil, &stored)
	if len(stored.Items) != 0 {
		c.t.Fatalf("a refused create stored %v", stored.Items)
	}
	why := e.Code
	if e.Details.Reason != "" && e.Details.Reason != e.Code {
		why += " (" + e.Details.Reason + ")"
	}
	return fmt.Sprintf("an experiment of candidate %s-topos refused %d %s; none stored", run, a.status, why)
}

// checkGrid holds the grid of the self-reported metric to the submitted
// values.
func (c *client) checkGrid(eid, metric, ref, cand string) string {
	c.t.Helper()
	var grid page[struct {
		Cells []struct {
			Candidate string `json:"candidate"`
			Trials    struct {
				Done     int            `json:"done"`
				Outcomes map[string]int `json:"outcomes"`
			} `json:"trials"`
			Summary *summary `json:"summary"`
		} `json:"cells"`
	}]
	c.must(http.StatusOK, http.MethodGet, "/experiments/"+eid+"/grid?metric="+metric, nil, &grid)
	if len(grid.Items) != 1 || len(grid.Items[0].Cells) != 2 {
		c.t.Fatalf("the grid of %s: %+v", eid, grid)
	}
	passed := map[string]int{ref: referencePassed, cand: candidatePassed}
	var out []string
	for _, cell := range grid.Items[0].Cells {
		want, ok := passed[cell.Candidate]
		if !ok || cell.Trials.Done != trialsPerCell || cell.Trials.Outcomes["completed"] != trialsPerCell || !cell.Summary.holds(want) {
			c.t.Fatalf("the grid cell of %s: %d done %v, summary %s; want %d of %d", cell.Candidate, cell.Trials.Done, cell.Trials.Outcomes, cell.Summary, want, trialsPerCell)
		}
		out = append(out, cell.Summary.String())
	}
	return fmt.Sprintf("%s: reference %s, candidate %s", metric, out[0], out[1])
}

// checkComparison waits for the candidate's automatic comparison to be
// final and holds it to the submitted values exactly: each side's rate of
// the self-reported metric, their difference, and the built-in completed
// rate of every trial. A self-reported value is never a finding, so its
// results have no q, no verdict but insufficient, and a p only beside the
// self_reported confound.
func (c *client) checkComparison(eid, metric, cand string, deadline time.Time) string {
	c.t.Helper()
	var cmp comparison
	for {
		var list page[comparison]
		c.must(http.StatusOK, http.MethodGet, "/experiments/"+eid+"/comparisons?automatic=true", nil, &list)
		if len(list.Items) == 1 && list.Items[0].State == "complete" && list.Items[0].Final {
			cmp = list.Items[0]
			break
		}
		if time.Now().After(deadline) {
			c.t.Fatalf("the automatic comparisons of %s are not final: %+v", eid, list.Items)
		}
		time.Sleep(pollInterval)
	}
	c.must(http.StatusOK, http.MethodGet, "/comparisons/"+cmp.ID, nil, &cmp)
	var rows page[result]
	c.must(http.StatusOK, http.MethodGet, "/comparisons/"+cmp.ID+"/rows?limit=200", nil, &rows)
	if cmp.Candidate != cand {
		c.t.Fatalf("comparison %s is of %s, want %s", cmp.ID, cmp.Candidate, cand)
	}
	wantDelta := float64(candidatePassed)/trialsPerCell - float64(referencePassed)/trialsPerCell
	var self *result
	held := 0
	for _, r := range append(slices.Clone(cmp.Results), rows.Items...) {
		switch r.MetricName {
		case metric:
			if !r.Candidate.holds(candidatePassed) || !r.Reference.holds(referencePassed) || r.Delta == nil || *r.Delta != wantDelta || r.DeltaKind != "difference" {
				c.t.Fatalf("%s %s result of %s: candidate %s, reference %s, delta %v %s; want %d and %d of %d, delta %v difference",
					cmp.ID, r.Level, metric, r.Candidate, r.Reference, r.Delta, r.DeltaKind, candidatePassed, referencePassed, trialsPerCell, wantDelta)
			}
			named := slices.ContainsFunc(r.Confounds, func(cf struct {
				Name string `json:"name"`
			}) bool {
				return cf.Name == "self_reported"
			})
			if r.Q != nil || (r.Verdict != "" && r.Verdict != "insufficient") || (r.P != nil && !named) {
				c.t.Fatalf("%s %s result of a self-reported value is headlined: q %v, verdict %q, p %v, confounds %v", cmp.ID, r.Level, r.Q, r.Verdict, r.P, r.Confounds)
			}
			if r.Level == "suite" {
				self = &r
			}
			held++
		case "completed":
			if !r.Candidate.holds(trialsPerCell) || !r.Reference.holds(trialsPerCell) || r.Delta == nil || *r.Delta != 0 {
				c.t.Fatalf("%s %s result of completed: candidate %s, reference %s, delta %v; want every trial completed", cmp.ID, r.Level, r.Candidate, r.Reference, r.Delta)
			}
			held++
		}
	}
	if self == nil || held < 4 {
		c.t.Fatalf("comparison %s holds %d results of %s and completed, no suite-level one of %s: %+v", cmp.ID, held, metric, metric, cmp)
	}
	verdict := "verdict " + self.Verdict
	if self.Verdict == "" {
		verdict = "no verdict, as a self-reported value has none"
	}
	return fmt.Sprintf("%s, final: candidate %s against reference %s, delta %v (%s), %s; completed 5/5 on both sides; declared provenance %v",
		cmp.ID, self.Candidate, self.Reference, *self.Delta, self.DeltaKind, verdict, cmp.Declared)
}
