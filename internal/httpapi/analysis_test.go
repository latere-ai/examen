// SPDX-FileCopyrightText: 2026 Latere AI
// SPDX-License-Identifier: Apache-2.0

package httpapi

import (
	"encoding/json"
	"math"
	"net/http"
	"strings"
	"testing"
	"time"

	"latere.ai/x/examen/internal/compare"
)

// analyzed is a server whose grid and comparisons are the analysis's.
func analyzed(t *testing.T) *client {
	t.Helper()
	return newAPI(t, func(s *Server) {
		s.Analyzer = compare.Analysis{}
		s.Engine.Analyzer = compare.Analysis{}
	})
}

// pastTrial is a past run of an external harness: its outcome, the seconds it
// took, and the lines of code it reported for itself, or none.
type pastTrial struct {
	outcome string
	seconds int
	loc     *float64
}

func lines(v float64) *float64 { return &v }

// importRuns imports runs of a candidate on a task, one minute apart.
func (x *client) importRuns(eid, task, candidate, prefix string, runs []pastTrial) {
	x.t.Helper()
	start := time.Date(2026, 3, 2, 14, 0, 0, 0, time.UTC)
	for i, r := range runs {
		at := start.Add(time.Duration(i) * time.Minute)
		body := map[string]any{
			"executor": "external", "task": task, "candidate": candidate, "outcome": r.outcome,
			"started_at": at, "agent_ended_at": at.Add(time.Duration(r.seconds) * time.Second), "ended_at": at.Add(time.Duration(r.seconds) * time.Second),
			"declared": map[string]string{"harness_version": "2.1.4", "image": "sandbox-agent:2026-02-27"},
			"ref":      prefix + "/run-" + itoa(i),
		}
		if r.loc != nil {
			body["measurements"] = []map[string]any{{"metric": "loc", "value": *r.loc}}
		}
		x.must(http.StatusCreated, http.MethodPost, "/v1/experiments/"+eid+"/imports", "t-alice", body)
	}
}

// decode reads a member of an answer into v.
func decodeInto(t *testing.T, raw any, v any) {
	t.Helper()
	b, err := json.Marshal(raw)
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(b, v); err != nil {
		t.Fatalf("%s: %v", b, err)
	}
}

// resultRow is the part of a result the test reads.
type resultRow struct {
	Level         string    `json:"level"`
	MetricName    string    `json:"metric_name"`
	Task          string    `json:"task"`
	Delta         *float64  `json:"delta"`
	DeltaInterval []float64 `json:"delta_interval"`
	Method        string    `json:"method"`
	Exact         *bool     `json:"exact"`
	Resamples     *int      `json:"resamples"`
	P             *float64  `json:"p"`
	Q             *float64  `json:"q"`
	Verdict       string    `json:"verdict"`
	Reason        string    `json:"reason"`
	Confounds     []struct {
		Name      string `json:"name"`
		Candidate any    `json:"candidate"`
		Reference any    `json:"reference"`
	} `json:"confounds"`
	Candidate summary `json:"candidate"`
	Reference summary `json:"reference"`
}

// summary is the part of a summary the test reads.
type summary struct {
	N           int       `json:"n"`
	Value       *float64  `json:"value"`
	Numerator   *int      `json:"numerator"`
	Denominator int       `json:"denominator"`
	Missing     int       `json:"missing"`
	Interval    []float64 `json:"interval"`
	NoInterval  string    `json:"no_interval"`
	Final       bool      `json:"final"`
}

func near(got, want, tol float64) bool { return math.Abs(got-want) <= tol }

func findRow(t *testing.T, rows []resultRow, metric, level, task string) resultRow {
	t.Helper()
	for _, r := range rows {
		if r.MetricName == metric && r.Level == level && r.Task == task {
			return r
		}
	}
	t.Fatalf("no %s result of %s for %q among %d", level, metric, task, len(rows))
	return resultRow{}
}

// An imported experiment's automatic comparison, computed by the analysis
// as trials arrive and once more at completion, its rows, the grid built
// from it, and a comparison on request: every figure is the worked
// examples' of spec 008.
func TestAnalyzedImport(t *testing.T) {
	x := analyzed(t)
	f := x.fixture()
	loc := x.apply("metrics", "loc", map[string]any{"type": "count", "direction": "none", "aggregate": "mean"})
	suite := x.apply("suites", "analyzed", map[string]any{
		"tasks":   []string{f.taskA, f.taskB},
		"metrics": []string{"builtin:declined", "builtin:completed", "builtin:duration", loc}, "primary_metric": "builtin:declined",
	})
	created := x.must(http.StatusCreated, http.MethodPost, "/v1/experiments", "t-alice", map[string]any{
		"suite": suite, "candidates": []string{f.high, f.low}, "trials": 6, "budget": map[string]any{"name": "study"}, "mode": "import",
	})
	eid := created["id"].(string)
	automatic := func() map[string]any {
		t.Helper()
		items := x.must(http.StatusOK, http.MethodGet, "/v1/experiments/"+eid+"/comparisons?automatic=true", "t-alice", nil)["items"].([]any)
		if len(items) != 1 {
			t.Fatalf("automatic comparisons %v", items)
		}
		return items[0].(map[string]any)
	}
	// Before the candidate's first finished trial the comparison waits.
	if err := x.srv.Engine.Compute(t.Context()); err != nil {
		t.Fatal(err)
	}
	first := automatic()
	if first["state"] != "computing" || first["candidate"] != f.low || first["reference_candidate"] != f.high {
		t.Fatalf("before any trial %v", first)
	}
	cid := first["id"].(string)

	// One run of the candidate: provisional, every verdict insufficient.
	x.importRuns(eid, f.taskB, f.low, "low/calculator", []pastTrial{{"completed", 30, nil}})
	if err := x.srv.Engine.Compute(t.Context()); err != nil {
		t.Fatal(err)
	}
	early := automatic()
	if early["id"] != cid || early["state"] != "complete" || early["final"] != false {
		t.Fatalf("after one trial %v", early)
	}
	var earlyRows []resultRow
	decodeInto(t, x.must(http.StatusOK, http.MethodGet, "/v1/comparisons/"+cid+"/rows?limit=200", "t-alice", nil)["items"], &earlyRows)
	for _, r := range earlyRows {
		if r.Verdict != "insufficient" {
			t.Errorf("after one trial %s %s %s is %s", r.MetricName, r.Level, r.Task, r.Verdict)
		}
	}

	// The rest: on calculator the candidate never declines and the
	// reference declines twice in five; on todo-app the two report the
	// lines of code of the Welch permutation's worked example.
	x.importRuns(eid, f.taskB, f.low, "low/calculator-more", []pastTrial{{"completed", 30, nil}, {"completed", 30, nil}, {"completed", 30, nil}, {"completed", 30, nil}})
	x.importRuns(eid, f.taskB, f.high, "high/calculator", []pastTrial{{"declined", 30, nil}, {"declined", 30, nil}, {"completed", 30, nil}, {"completed", 30, nil}, {"completed", 30, nil}})
	var low, high []pastTrial
	for _, v := range []float64{3, 5, 4, 6, 12} {
		low = append(low, pastTrial{"completed", 60, lines(v)})
	}
	for _, v := range []float64{2, 3, 2, 3, 2, 3} {
		high = append(high, pastTrial{"completed", 60, lines(v)})
	}
	x.importRuns(eid, f.taskA, f.low, "low/todo", low)
	x.importRuns(eid, f.taskA, f.high, "high/todo", high)
	if err := x.srv.Engine.Compute(t.Context()); err != nil {
		t.Fatal(err)
	}
	running := automatic()
	if running["id"] != cid || running["final"] != false || running["state"] != "complete" {
		t.Fatalf("while the intake is open %v", running)
	}

	// Closing the intake completes the experiment; the comparison is
	// computed a last time, final, under the same id.
	x.must(http.StatusOK, http.MethodPost, "/v1/experiments/"+eid+"/close", "t-alice", nil)
	if err := x.srv.Engine.Tick(t.Context()); err != nil {
		t.Fatal(err)
	}
	final := x.must(http.StatusOK, http.MethodGet, "/v1/comparisons/"+cid, "t-alice", nil)
	if final["final"] != true || final["state"] != "complete" || final["exploratory"] != false || final["automatic"] != true {
		t.Fatalf("at completion %v", final)
	}
	if final["analysis_version"] != "examen.analysis/v1" || !strings.HasPrefix(final["inputs_digest"].(string), "sha256:") || len(final["seed"].(string)) != 64 || final["computed_at"] == nil {
		t.Fatalf("the record of the computation %v", final)
	}
	if v := final["variable"].([]any); len(v) != 1 || v[0] != "reasoning" || final["single_variable"] != true {
		t.Fatalf("the candidates differ in declared reasoning alone: %v %v", final["variable"], final["single_variable"])
	}
	if dp := final["declared_provenance"].([]any); len(dp) != 2 {
		t.Fatalf("both sides are declared %v", dp)
	}
	params := final["parameters"].(map[string]any)
	if params["false_discovery_rate"] != 0.05 || params["permutation_resamples"].(map[string]any)["primary"] != 10000.0 {
		t.Fatalf("parameters %v", params)
	}
	correction := final["correction"].(map[string]any)
	if correction["primary"] != "builtin:declined" || correction["method"] != "benjamini_hochberg" {
		t.Fatalf("correction %v", correction)
	}
	var rows []resultRow
	decodeInto(t, x.must(http.StatusOK, http.MethodGet, "/v1/comparisons/"+cid+"/rows?limit=200", "t-alice", nil)["items"], &rows)

	// declined, 0 of 5 against 2 of 5: Newcombe's -0.4000 [-0.7693, 0.1182],
	// and Wilson's [0, 0.4345] with its lower bound exactly 0.
	declined := findRow(t, rows, "declined", "task", "calculator")
	if declined.Delta == nil || *declined.Delta != -0.4 || len(declined.DeltaInterval) != 2 ||
		!near(declined.DeltaInterval[0], -0.7693, 5e-5) || !near(declined.DeltaInterval[1], 0.1182, 5e-5) {
		t.Errorf("declined delta %v %v", declined.Delta, declined.DeltaInterval)
	}
	if c := declined.Candidate; c.N != 5 || c.Denominator != 5 || c.Numerator == nil || *c.Numerator != 0 || c.Interval[0] != 0 || !near(c.Interval[1], 0.4345, 5e-5) || !c.Final {
		t.Errorf("declined candidate %+v", c)
	}
	if r := declined.Reference; r.Numerator == nil || *r.Numerator != 2 || r.Denominator != 5 {
		t.Errorf("declined reference %+v", r)
	}
	// Boschloo's p over the grid of 1000 rates, against an independent
	// computation of the same sum; Fisher's p for the table is 4/9.
	if declined.Method != "boschloo_exact" || declined.P == nil || !near(*declined.P, 0.18737677, 1e-8) || declined.Q == nil || declined.Verdict != "within_noise" {
		t.Errorf("declined test %s %v %v %s", declined.Method, declined.P, declined.Q, declined.Verdict)
	}
	// completed, 5 of 5 on both sides: Wilson's [0.5655, 1] and no
	// difference.
	completed := findRow(t, rows, "completed", "task", "calculator")
	if c := completed.Candidate; c.Interval == nil || !near(c.Interval[0], 0.5655, 5e-5) || c.Interval[1] != 1 {
		t.Errorf("completed candidate %+v", c)
	}
	if completed.Verdict != "within_noise" || completed.Delta == nil || *completed.Delta != 0 {
		t.Errorf("completed %+v", completed)
	}
	// The lines of code the harness reported for itself: Welch's
	// permutation over all 462 relabelings gives p = 10/462; the result
	// keeps its p, carries self_reported, has no q and no verdict.
	reported := findRow(t, rows, "loc", "task", "todo-app")
	if reported.P == nil || *reported.P != 10.0/462 || reported.Exact == nil || !*reported.Exact || reported.Delta == nil || *reported.Delta != 3.5 {
		t.Errorf("loc test p %v exact %v delta %v", reported.P, reported.Exact, reported.Delta)
	}
	if reported.Q != nil || reported.Verdict != "" || !strings.Contains(reported.Reason, "not a finding") {
		t.Errorf("a self-reported result is headlined: q %v verdict %q reason %q", reported.Q, reported.Verdict, reported.Reason)
	}
	selfReported := false
	for _, c := range reported.Confounds {
		selfReported = selfReported || c.Name == "self_reported"
	}
	if !selfReported {
		t.Errorf("loc confounds %+v", reported.Confounds)
	}
	// The suite-level result of the primary metric is tested alone: its q
	// is its p.
	var suiteRows []resultRow
	decodeInto(t, final["results"], &suiteRows)
	primary := findRow(t, suiteRows, "declined", "suite", "")
	if primary.P == nil || primary.Q == nil || *primary.P != *primary.Q {
		t.Errorf("the primary result %+v", primary)
	}
	if counts := final["verdict_counts"].(map[string]any)["builtin:declined"].(map[string]any); counts["within_noise"] != 2.0 || len(counts) != 1 {
		t.Errorf("verdict counts %v", final["verdict_counts"])
	}

	// The grid shows each cell's summary of the primary metric, and the
	// candidate's column the verdict of its automatic comparison; the
	// reference's column has none.
	grid := x.must(http.StatusOK, http.MethodGet, "/v1/experiments/"+eid+"/grid", "t-alice", nil)
	var items []struct {
		Task struct {
			Name string `json:"name"`
		} `json:"task"`
		Cells []struct {
			Candidate  string  `json:"candidate"`
			Summary    summary `json:"summary"`
			Verdict    string  `json:"verdict"`
			Comparison string  `json:"comparison"`
		} `json:"cells"`
	}
	decodeInto(t, grid["items"], &items)
	if len(items) != 2 || items[1].Task.Name != "calculator" || len(items[1].Cells) != 2 {
		t.Fatalf("grid %v", grid)
	}
	for _, c := range items[1].Cells {
		switch c.Candidate {
		case f.high:
			if c.Verdict != "" || c.Comparison != "" || c.Summary.Numerator == nil || *c.Summary.Numerator != 2 || c.Summary.Denominator != 5 {
				t.Errorf("the reference's cell %+v", c)
			}
		case f.low:
			if c.Verdict != "within_noise" || c.Comparison != cid || c.Summary.Numerator == nil || *c.Summary.Numerator != 0 || c.Summary.Interval[0] != 0 || !near(c.Summary.Interval[1], 0.4345, 5e-5) {
				t.Errorf("the candidate's cell %+v", c)
			}
		}
	}
	locGrid := x.must(http.StatusOK, http.MethodGet, "/v1/experiments/"+eid+"/grid?metric=loc&task=todo-app", "t-alice", nil)
	var locItems []struct {
		Cells []struct {
			Candidate string  `json:"candidate"`
			Summary   summary `json:"summary"`
			Verdict   string  `json:"verdict"`
			Reason    string  `json:"reason"`
		} `json:"cells"`
	}
	decodeInto(t, locGrid["items"], &locItems)
	for _, c := range locItems[0].Cells {
		want := map[string]float64{f.low: 6, f.high: 2.5}[c.Candidate]
		if c.Summary.Value == nil || *c.Summary.Value != want || c.Verdict != "" || (c.Candidate == f.low && !strings.Contains(c.Reason, "not a finding")) {
			t.Errorf("loc cell %+v, want the mean %v and no verdict", c, want)
		}
	}

	// A comparison on request is exploratory: its primary metric is
	// corrected with the rest, so correction names no primary.
	asked := x.must(http.StatusCreated, http.MethodPost, "/v1/comparisons", "t-alice", map[string]any{"experiment": eid, "candidate": f.low, "reference_candidate": f.high})
	if asked["state"] != "complete" || asked["exploratory"] != true || asked["automatic"] != false || asked["final"] != true {
		t.Fatalf("on request %v", asked)
	}
	if _, named := asked["correction"].(map[string]any)["primary"]; named {
		t.Errorf("an exploratory comparison names a primary %v", asked["correction"])
	}
	// Computed from the same inputs, it read the same measurements.
	if asked["inputs_digest"] == final["inputs_digest"] {
		t.Error("an exploratory comparison has the inputs digest of a confirmatory one")
	}
	// Across two tasks: the candidate's calculator against its todo-app.
	across := x.must(http.StatusCreated, http.MethodPost, "/v1/comparisons", "t-alice", map[string]any{"experiment": eid, "candidate": f.low, "variable": "task", "task": "calculator", "reference_task": "todo-app"})
	if across["state"] != "complete" || across["exploratory"] != true {
		t.Fatalf("across tasks %v", across)
	}
	if v := across["variable"].([]any); len(v) != 1 || v[0] != "task_hash" {
		t.Errorf("across tasks the variable is %v", v)
	}
}

// A comparison is final once its figures are the ones computed after its
// experiment ended. The end marks every kept comparison final and computes
// it again on the next round, so between the two the record holds the
// figures of its last provisional computation; it reads final false until
// they are replaced, and a list of final comparisons leaves it out.
func TestAComparisonIsFinalOnceComputedAfterTheEnd(t *testing.T) {
	x := analyzed(t)
	f := x.fixture()
	suite := x.apply("suites", "finality", map[string]any{
		"tasks": []string{f.taskB}, "metrics": []string{"builtin:declined", "builtin:completed"}, "primary_metric": "builtin:declined",
	})
	created := x.must(http.StatusCreated, http.MethodPost, "/v1/experiments", "t-alice", map[string]any{
		"suite": suite, "candidates": []string{f.high, f.low}, "trials": 5, "budget": map[string]any{"name": "study"}, "mode": "import",
	})
	eid := created["id"].(string)
	runs := func(n int, outcome string) []pastTrial {
		out := make([]pastTrial, n)
		for i := range out {
			out[i] = pastTrial{outcome, 30, nil}
		}
		return out
	}
	declined := func(c map[string]any) resultRow {
		t.Helper()
		var rows []resultRow
		decodeInto(t, c["results"], &rows)
		return findRow(t, rows, "declined", "suite", "")
	}
	compute := func() {
		t.Helper()
		if err := x.srv.Engine.Compute(t.Context()); err != nil {
			t.Fatal(err)
		}
	}

	// Provisional: the candidate's first two runs, both declined.
	x.importRuns(eid, f.taskB, f.high, "high", runs(5, "completed"))
	x.importRuns(eid, f.taskB, f.low, "low-first", runs(2, "declined"))
	compute()
	items := x.must(http.StatusOK, http.MethodGet, "/v1/experiments/"+eid+"/comparisons?automatic=true", "t-alice", nil)["items"].([]any)
	if len(items) != 1 {
		t.Fatalf("automatic comparisons %v", items)
	}
	early := items[0].(map[string]any)
	cid := early["id"].(string)
	if early["state"] != "complete" || early["final"] != false || declined(early).Candidate.N != 2 {
		t.Fatalf("provisional %v", early)
	}

	// The candidate's last three runs, and the end: the record is marked
	// final and still holds the two runs' figures.
	x.importRuns(eid, f.taskB, f.low, "low-rest", runs(3, "completed"))
	x.must(http.StatusOK, http.MethodPost, "/v1/experiments/"+eid+"/close", "t-alice", nil)
	marked := x.must(http.StatusOK, http.MethodGet, "/v1/comparisons/"+cid, "t-alice", nil)
	if marked["final"] != false || declined(marked).Candidate.N != 2 {
		t.Fatalf("marked and not yet computed again: final %v over %d trials", marked["final"], declined(marked).Candidate.N)
	}
	for _, path := range []string{"/v1/experiments/" + eid + "/comparisons?final=true", "/v1/comparisons?final=true"} {
		if got := x.must(http.StatusOK, http.MethodGet, path, "t-alice", nil)["items"].([]any); len(got) != 0 {
			t.Fatalf("%s lists %d comparisons before their last computation", path, len(got))
		}
	}

	// Computed again: final, over every run.
	compute()
	done := x.must(http.StatusOK, http.MethodGet, "/v1/comparisons/"+cid, "t-alice", nil)
	if done["final"] != true || done["state"] != "complete" || declined(done).Candidate.N != 5 {
		t.Fatalf("computed after the end: final %v, %s, over %d trials", done["final"], done["state"], declined(done).Candidate.N)
	}
	for _, path := range []string{"/v1/experiments/" + eid + "/comparisons?final=true", "/v1/comparisons?final=true"} {
		if got := x.must(http.StatusOK, http.MethodGet, path, "t-alice", nil)["items"].([]any); len(got) != 1 {
			t.Fatalf("%s lists %d comparisons after their last computation", path, len(got))
		}
	}
}
