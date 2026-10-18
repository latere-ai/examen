// SPDX-FileCopyrightText: 2026 Latere AI
// SPDX-License-Identifier: Apache-2.0

package analysis

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"math"
	"runtime"
	"slices"
	"testing"
	"time"
)

// golden is a comparison that reads every kind of figure: rates, a skewed
// mean, a median and a p90 above the exact bound, labels, and a repeat
// rate, over three tasks, one of them small.
func golden() Input {
	pass := binaryMetric("pass", HigherIsBetter)
	cost := numericMetric("cost", TypeCost, AggMean, LowerIsBetter)
	dur := numericMetric("duration", TypeDuration, AggMedian, LowerIsBetter)
	tail := numericMetric("tail", TypeDuration, AggP90, LowerIsBetter)
	topic := labelMetric("topic", "a", "b", "c", "declined")
	rr := cellMetric("agreement", AggRepeatRate, topic)
	metrics := []Metric{pass, cost, dur, tail, topic, rr}
	g := newGenerator(RootSeed("golden"))
	side := func(prefix string, shift float64, n int) []Cell {
		var cells []Cell
		for ti, task := range []string{"t1", "t2", "t3"} {
			size := n
			if task == "t3" {
				size = 6
			}
			trials := make([]Trial, size)
			for i := range trials {
				at := epoch.Add(time.Duration(i) * time.Minute)
				u := float64(g.below(1000)) / 1000
				trials[i] = Trial{ID: fmt.Sprintf("%s-%s-%03d", prefix, task, i), Outcome: OutcomeCompleted, StartedAt: at, EndedAt: at.Add(time.Second)}
				vals := []any{
					u < 0.5+shift,
					0.01 + float64(float64(u*u)*u) + float64(shift*0.02),
					30 + float64(u*20) - float64(shift*10),
					60 + float64(u*60) + float64(float64(ti)*5),
					[]string{"a", "b", "c", "declined"}[g.below(4)],
				}
				for k, m := range metrics[:5] {
					trials[i].Measurements = append(trials[i].Measurements, Measurement{
						ID: fmt.Sprintf("msr-%s-%s-%03d-%d", prefix, task, i, k), Metric: m.Digest, Grader: grader, Generation: 1, Value: vals[k],
					})
				}
			}
			cells = append(cells, cell(task, tuple("model-"+prefix, task), trials, metrics...))
		}
		return cells
	}
	return Input{
		LabelTests: true,
		Parameters: Defaults(),
		Suite:      Suite{Metrics: metrics, PrimaryMetric: pass.Digest, Weights: map[string]float64{"sha256:t2": 2}},
		Candidate:  Side{Candidate: "sha256:cand", Cells: side("b", 0.2, 40)},
		Reference:  Side{Candidate: "sha256:ref", Cells: side("a", 0, 36)},
	}
}

// The golden comparison's canonical bytes, held to their SHA-256: the same
// on amd64 and arm64, which `GOARCH=amd64 go test` checks on an arm64
// machine.
const goldenDigest = "2186c0886bede1a7b0fe41d453ed6cde40aa9a63058e1f9d415e8c60ab0abb0f"

func TestGoldenAcrossArchitectures(t *testing.T) {
	c := mustCompare(t, golden())
	b, err := json.Marshal(c)
	if err != nil {
		t.Fatal(err)
	}
	sum := sha256.Sum256(b)
	got := hex.EncodeToString(sum[:])
	if got != goldenDigest {
		t.Errorf("the golden comparison's digest on %s is %s, want %s", runtime.GOARCH, got, goldenDigest)
	}
	// Spot checks that it reads every kind of figure.
	kinds := map[string]bool{}
	for _, r := range c.Results {
		if r.Method != "" {
			kinds[r.Method+"/"+r.Statistic+fmt.Sprint(*r.Exact)] = true
		}
	}
	for _, k := range []string{
		methodBoschloo + "/" + statFisherP + "true",
		methodPerm + "/" + statWelchTfalse,
		methodPerm + "/" + statStudentizedQ + "false",
		methodPerm + "/" + statDifference + "false",
		methodStratified + "/" + statLabelDistance + "false",
	} {
		if !kinds[k] {
			t.Errorf("the golden comparison runs no %s: %v", k, kinds)
		}
	}
}

const statWelchTfalse = statWelchT + "false"

// A final comparison recomputed from its recorded inputs is byte-identical,
// and its seeds are the recorded ones.
func TestRecomputeFromRecordedInputs(t *testing.T) {
	in := golden()
	a := mustCompare(t, in)
	ja, err := json.Marshal(a)
	if err != nil {
		t.Fatal(err)
	}
	// The same reads in another order: cells and trials shuffled.
	shuffled := golden()
	for _, side := range []*Side{&shuffled.Candidate, &shuffled.Reference} {
		side.Cells[0], side.Cells[2] = side.Cells[2], side.Cells[0]
		for ci := range side.Cells {
			tr := side.Cells[ci].Trials
			for i, j := 0, len(tr)-1; i < j; i, j = i+1, j-1 {
				tr[i], tr[j] = tr[j], tr[i]
			}
		}
	}
	jb, err := json.Marshal(mustCompare(t, shuffled))
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(ja, jb) {
		t.Error("the same inputs in another order gave another comparison")
	}
	_, digest, err := InputsDocument(in)
	if err != nil {
		t.Fatal(err)
	}
	if digest != a.InputsDigest {
		t.Errorf("inputs digest %s, the document's %s", a.InputsDigest, digest)
	}
	root := RootSeed(digest)
	if root.String() != a.Seed {
		t.Error("the root seed is not the inputs digest's")
	}
	for _, r := range a.Results {
		scope := "suite"
		if r.Level == LevelTask {
			scope = taskScope(r.Task)
		}
		if want := ProcedureSeed(root, r.Metric, scope, procTest).String(); r.Seed != want {
			t.Errorf("%s %s %s: seed %s, want %s", r.MetricName, r.Level, r.Task, r.Seed, want)
		}
	}
	// Recorded parameters are read: a changed default does not move a
	// comparison computed with the recorded ones.
	in.Parameters = a.Parameters
	jc, err := json.Marshal(mustCompare(t, in))
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(ja, jc) {
		t.Error("the recorded parameters gave another comparison")
	}
	var back Parameters
	pj, err := json.Marshal(a.Parameters)
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(pj, &back); err != nil || back != a.Parameters {
		t.Errorf("parameters do not round-trip: %v %+v", err, back)
	}
	if err := json.Unmarshal([]byte(`{"window_tolerance":"7 days"}`), &back); err == nil {
		t.Error("a malformed duration was read")
	}
	if err := json.Unmarshal([]byte(`{"window_tolerance":7}`), &back); err == nil {
		t.Error("a number was read as a duration")
	}
}

// A task named suite and the suite-level result draw from different seeds.
func TestSeedScopes(t *testing.T) {
	m := numericMetric("cost", TypeCost, AggMean, LowerIsBetter)
	c := mustCompare(t, pairInput(m, map[string][]any{"suite": nums(1, 2, 3, 4, 5)}, map[string][]any{"suite": nums(2, 3, 4, 5, 6)}))
	task, suite := find(c, m.Digest, LevelTask, "suite"), find(c, m.Digest, LevelSuite, "")
	if task.Seed == suite.Seed {
		t.Error("a task named suite draws from the suite-level result's seed")
	}
	root := RootSeed("sha256:x")
	seen := map[string]string{}
	for _, s := range []struct{ scope, proc string }{
		{taskScope("suite"), procTest}, {"suite", procTest}, {acrossTasksScope("a", "b"), procTest},
		{pairScope("a", "b"), procTest}, {"suite", procDeltaInterval}, {"suite", procSummaryCandidate}, {"suite", procSummaryReference},
	} {
		k := ProcedureSeed(root, "m", s.scope, s.proc).String()
		if prev, ok := seen[k]; ok {
			t.Errorf("%s/%s repeats the seed of %s", s.scope, s.proc, prev)
		}
		seen[k] = s.scope + "/" + s.proc
	}
}

// Swapping the sides negates the delta and its interval and keeps an exact
// test's p-value.
func TestSidesAreSymmetric(t *testing.T) {
	pass := binaryMetric("pass", HigherIsBetter)
	cost := numericMetric("cost", TypeCost, AggMean, LowerIsBetter)
	x := map[string][]any{"t1": bools(7, 9)}
	y := map[string][]any{"t1": bools(2, 8)}
	a := find(mustCompare(t, pairInput(pass, x, y)), pass.Digest, LevelTask, "t1")
	b := find(mustCompare(t, pairInput(pass, y, x)), pass.Digest, LevelTask, "t1")
	if a.Delta.Value != -b.Delta.Value || math.Abs(*a.P-*b.P) > 1e-12 {
		t.Errorf("Boschloo: %v %v against %v %v", a.Delta.Value, *a.P, b.Delta.Value, *b.P)
	}
	if math.Abs(a.DeltaInterval.Interval[0]+b.DeltaInterval.Interval[1]) > 1e-12 {
		t.Errorf("Newcombe: %v against %v", a.DeltaInterval.Interval, b.DeltaInterval.Interval)
	}
	xc := map[string][]any{"t1": nums(1, 3, 2, 5, 4, 9)}
	yc := map[string][]any{"t1": nums(4, 6, 5, 8, 7)}
	a = find(mustCompare(t, pairInput(cost, xc, yc)), cost.Digest, LevelTask, "t1")
	b = find(mustCompare(t, pairInput(cost, yc, xc)), cost.Digest, LevelTask, "t1")
	if a.Delta.Value != -b.Delta.Value || *a.P != *b.P || !*a.Exact {
		t.Errorf("Welch: %v %v against %v %v", a.Delta.Value, *a.P, b.Delta.Value, *b.P)
	}
	// Relabeling the trial ids, keeping their order, changes no figure.
	in := pairInput(cost, xc, yc)
	for i := range in.Candidate.Cells[0].Trials {
		in.Candidate.Cells[0].Trials[i].ID = fmt.Sprintf("z-%03d", i)
	}
	c := find(mustCompare(t, in), cost.Digest, LevelTask, "t1")
	if c.Delta.Value != a.Delta.Value || *c.P != *a.P {
		t.Error("renamed trials changed the figures")
	}
}

// normal draws a standard normal value by Marsaglia's polar method.
func normal(g *generator) float64 {
	for {
		u := float64(g.below(1<<53))/(1<<53)*2 - 1
		v := float64(g.below(1<<53))/(1<<53)*2 - 1
		s := u*u + v*v
		if s > 0 && s < 1 {
			return u * math.Sqrt(-2*math.Log(s)/s)
		}
	}
}

// levelResamples and levelStop are the relabelings each simulated test
// draws and the exceedances it stops at. The level is a property of the
// statistic; B and the stop rule only resolve the p-value, and any stop
// keeps it valid, so 500 draws stopped at 50 exceedances decide p <= 0.05
// at a small part of the defaults' time.
const (
	levelResamples = 500
	levelStop      = 50
)

// With equal means and equal medians, 50 normal trials of spread 1 against
// 5 of spread 10, the Welch permutation test rejects at 5% in at most 10%
// of 2,000 simulated experiments and the studentized median test in at
// most 12%, where the bare differences reject in more than 40%.
func TestStudentizedLevel(t *testing.T) {
	if testing.Short() {
		t.Skip("2,000 simulated experiments")
	}
	const experiments = 2000
	p := Defaults()
	bx, by := hallSheather(50, 0.5, z95), hallSheather(5, 0.5, z95)
	stats := map[string]statistic{
		"welch":     welchT,
		"median":    studentizedQuantile(0.5, bx, by),
		"bare mean": func(x, y []float64) float64 { return mean(x) - mean(y) },
		"bare median": func(x, y []float64) float64 {
			slices.Sort(x)
			slices.Sort(y)
			return quantile(x, 0.5) - quantile(y, 0.5)
		},
	}
	// The experiments run one after another: the suite runs under the race
	// detector with atomic coverage counters, which goroutines sharing them
	// would slow a hundredfold.
	rejected := map[string]int{}
	names := []string{"welch", "median", "bare mean", "bare median"}
	for e := range experiments {
		g := newGenerator(ProcedureSeed(RootSeed("level"), "sim", fmt.Sprint(e), "data"))
		x, y := make([]float64, 50), make([]float64, 5)
		for i := range x {
			x[i] = normal(g)
		}
		for i := range y {
			y[i] = 10 * normal(g)
		}
		for _, name := range names {
			cfg := mcConfig{g: newGenerator(ProcedureSeed(RootSeed("level"), name, fmt.Sprint(e), procTest)), b: levelResamples, stop: levelStop, tol: p.TieTolerance}
			if r := permutationTest(x, y, stats[name], p.PermutationExactMax, cfg); r.p <= 0.05 {
				rejected[name]++
			}
		}
	}
	rate := func(name string) float64 { return float64(rejected[name]) / experiments }
	t.Logf("rejection rates: welch %.3f, median %.3f, bare mean %.3f, bare median %.3f", rate("welch"), rate("median"), rate("bare mean"), rate("bare median"))
	if rate("welch") > 0.10 || rate("median") > 0.12 {
		t.Errorf("the studentized tests reject %.3f and %.3f", rate("welch"), rate("median"))
	}
	if rate("bare mean") <= 0.40 || rate("bare median") <= 0.40 {
		t.Errorf("the bare differences reject %.3f and %.3f", rate("bare mean"), rate("bare median"))
	}
}

// The measurements a comparison reads are the ids its inputs document
// holds, sorted and each once: a value of another grader and a trial left
// out of analysis are not read.
func TestMeasurementsRead(t *testing.T) {
	m := binaryMetric("tests_pass", HigherIsBetter)
	in := pairInput(m, map[string][]any{"a": bools(2, 3)}, map[string][]any{"a": bools(1, 2)})
	// A value of a grader that does not produce the metric, and a value of
	// an infrastructure failure, are not read.
	c := &in.Candidate.Cells[0]
	c.Trials[0].Measurements = append(c.Trials[0].Measurements, Measurement{ID: "msr-other", Metric: m.Digest, Grader: "sha256:other", Generation: 1, Value: true})
	c.Trials = append(c.Trials, Trial{ID: "c-a-failed", Outcome: OutcomeInfraFailed, StartedAt: epoch, EndedAt: epoch,
		Measurements: []Measurement{{ID: "msr-failed", Metric: m.Digest, Grader: grader, Generation: 1, Value: true}}})
	got, err := MeasurementsRead(in)
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"msr-c-a-000-tests_pass", "msr-c-a-001-tests_pass", "msr-c-a-002-tests_pass", "msr-r-a-000-tests_pass", "msr-r-a-001-tests_pass"}
	if !slices.Equal(got, want) {
		t.Fatalf("read %v, want %v", got, want)
	}
	if _, err := MeasurementsRead(Input{Parameters: Parameters{}}); err == nil {
		t.Error("an input of invalid parameters was read")
	}
}
