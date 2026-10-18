// SPDX-FileCopyrightText: 2026 Latere AI
// SPDX-License-Identifier: Apache-2.0

package analysis

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"
)

// entry makes an accepted entry whose tasks hold the values given, each
// task of weight 1, candidates differing by model.
func entry(id string, tasks map[string][]any) Entry {
	e := Entry{
		Submission: id,
		Candidate:  EntryCandidate{Name: id, Kind: "topos", Digest: "sha256:" + id, AgentOrImageDigest: "sha256:agent", Model: "model-" + id, Reasoning: "medium"},
		Window:     Window{From: epoch, To: epoch.Add(time.Hour)},
	}
	for _, name := range sortedKeys(tasks) {
		e.Values = append(e.Values, EntryTask{
			Task: name, Weight: 1, Values: tasks[name],
			Tuple: EntryTuple{TaskHash: "sha256:" + name, EnvironmentDigest: "sha256:env", WorkspaceRef: "none", GraderDigests: []string{grader}},
		})
	}
	return e
}

func table(t *testing.T, m Metric, ref string, entries ...Entry) Table {
	t.Helper()
	return tableAt(t, Defaults(), m, ref, entries...)
}

// tableAt computes a results table at the parameters p.
func tableAt(t *testing.T, p Parameters, m Metric, ref string, entries ...Entry) Table {
	t.Helper()
	tb, err := ComputeTable(TableInput{Parameters: p, Metric: m, Reference: ref, Entries: entries})
	if err != nil {
		t.Fatal(err)
	}
	return tb
}

func ranked(tb Table, id string) *RankedEntry {
	for i := range tb.Entries {
		if tb.Entries[i].Submission == id {
			return &tb.Entries[i]
		}
	}
	return nil
}

// Three entries where the first and the third are apart and the second is
// apart from neither give the intervals [1, 2], [1, 3] and [2, 3].
func TestRankIntervals(t *testing.T) {
	m := binaryMetric("pass", HigherIsBetter)
	tb := table(t, m, "",
		entry("sbm_1", map[string][]any{"t1": bools(8, 10), "t2": bools(8, 10)}),
		entry("sbm_2", map[string][]any{"t1": bools(5, 10), "t2": bools(5, 10)}),
		entry("sbm_3", map[string][]any{"t1": bools(2, 10), "t2": bools(2, 10)}),
	)
	want := map[string]Rank{"sbm_1": {1, 2}, "sbm_2": {1, 3}, "sbm_3": {2, 3}}
	for id, r := range want {
		e := ranked(tb, id)
		if e == nil || e.Rank != r {
			t.Errorf("%s: %+v, want %v", id, e, r)
		}
	}
	if tb.Entries[0].Submission != "sbm_1" || tb.Entries[0].Order != 1 || tb.Entries[2].Order != 3 {
		t.Errorf("order %+v", tb.Entries)
	}
	for _, e := range tb.Entries {
		if e.Rank.Low != 1+countApart(tb, e.Submission, true) || e.Rank.High != 3-countApart(tb, e.Submission, false) {
			t.Errorf("%s: %v is not [1 + B, K - W]", e.Submission, e.Rank)
		}
	}
	if tb.Method.Pairs != 3 || tb.Method.Adjustment != "holm" || tb.Method.Resamples != 10000 {
		t.Errorf("method %+v", tb.Method)
	}
	// Direction lower_is_better orders ascending and reads the better entry
	// as the lower one.
	low := binaryMetric("declined", LowerIsBetter)
	tb = table(t, low, "",
		entry("sbm_1", map[string][]any{"t1": bools(8, 10), "t2": bools(8, 10)}),
		entry("sbm_3", map[string][]any{"t1": bools(2, 10), "t2": bools(2, 10)}),
	)
	if tb.Entries[0].Submission != "sbm_3" || tb.Entries[0].Rank != (Rank{1, 1}) || tb.Entries[1].Rank != (Rank{2, 2}) {
		t.Errorf("lower is better: %+v", tb.Entries)
	}
}

// countApart counts the entries significantly better (or worse) than id.
func countApart(tb Table, id string, betterThanIt bool) int {
	n := 0
	for _, p := range tb.Pairs {
		if p.H == nil || *p.H > 0.05 || p.Delta.Value == 0 || (p.A != id && p.B != id) {
			continue
		}
		aBetter := p.Delta.Value > 0
		if (p.A == id) == (aBetter != betterThanIt) {
			n++
		}
	}
	return n
}

func TestEntryRanking(t *testing.T) {
	m := binaryMetric("pass", HigherIsBetter)
	full := map[string][]any{"t1": bools(5, 5), "t2": bools(4, 5), "t3": bools(3, 5), "t4": bools(5, 5), "t5": bools(4, 5)}
	missing := map[string][]any{"t1": append(bools(4, 4), nil), "t2": bools(4, 5), "t3": bools(3, 5), "t4": bools(5, 5), "t5": bools(4, 5)}
	small := map[string][]any{"t1": bools(5, 5), "t2": bools(4, 5), "t3": bools(1, 1), "t4": bools(1, 1), "t5": bools(4, 5)}
	lacks := map[string][]any{"t1": bools(5, 5), "t2": bools(4, 5), "t3": bools(3, 5), "t4": bools(5, 5), "t5": bools(1, 1)}
	lacks2 := map[string][]any{"t1": bools(5, 5), "t2": bools(4, 5), "t3": bools(3, 5), "t4": bools(1, 1), "t5": bools(4, 5)}
	self := entry("sbm_6", full)
	self.Values[0].Layers.SelfReported = 5
	declared := entry("sbm_7", full)
	declared.Candidate.Kind = "external"
	tb := table(t, m, "sbm_5",
		entry("sbm_1", full), entry("sbm_2", missing), entry("sbm_3", small),
		entry("sbm_4", lacks), entry("sbm_5", lacks2), self, declared)
	why := map[string]string{}
	for _, u := range tb.Unranked {
		why[u.Submission] = u.Why
	}
	if why["sbm_2"] != "task t1 holds a trial without a value of the primary metric" {
		t.Errorf("missing: %q", why["sbm_2"])
	}
	if why["sbm_3"] != "covers 60% of the suite's weight; 80% needed" {
		t.Errorf("small: %q", why["sbm_3"])
	}
	if why["sbm_6"] != "task t1 holds a self-reported value of the primary metric" {
		t.Errorf("self-reported: %q", why["sbm_6"])
	}
	// The reference comes first and sets the common tasks to t1, t2, t3,
	// t5; sbm_1 keeps them; sbm_4 lacks t5 and would leave 60%.
	if why["sbm_4"] != "covering it would leave the table's common tasks at 60% of the suite's weight; 80% needed; it lacks t5" {
		t.Errorf("lacks: %q", why["sbm_4"])
	}
	if strings.Join(tb.Common.Tasks, ",") != "t1,t2,t3,t5" || tb.Common.Share != 0.8 {
		t.Errorf("common %+v", tb.Common)
	}
	if len(tb.Entries) != 3 || ranked(tb, "sbm_7") == nil || !ranked(tb, "sbm_7").Declared {
		t.Errorf("ranked %+v", tb.Entries)
	}
	for _, e := range tb.Entries {
		if *e.Summary.Tasks != 4 {
			t.Errorf("%s: summary over %d tasks", e.Submission, *e.Summary.Tasks)
		}
	}
	// A pair with an unranked entry is insufficient, with its why.
	r, ok := tb.ComparePair("sbm_1", "sbm_3")
	if !ok || r.Verdict != Insufficient || r.P != nil || r.Reason != "entry sbm_3 is not ranked: covers 60% of the suite's weight; 80% needed" {
		t.Errorf("unranked pair %+v", r)
	}
	if _, ok := tb.ComparePair("sbm_1", "nope"); ok {
		t.Error("a pair with an unknown entry")
	}
	if len(tb.Pairs) != 21 || tb.Method.Pairs != 3 {
		t.Errorf("pairs %d, ranked pairs %d", len(tb.Pairs), tb.Method.Pairs)
	}
	if ref := ranked(tb, "sbm_5"); ref.VsReference != nil {
		t.Error("the reference carries a comparison with itself")
	}
	if v := ranked(tb, "sbm_1").VsReference; v == nil || v.A != "sbm_1" || v.B != "sbm_5" {
		t.Errorf("vs reference %+v", v)
	}
}

func TestEntryPairs(t *testing.T) {
	for _, m := range []Metric{
		binaryMetric("pass", HigherIsBetter),
		numericMetric("cost", TypeCost, AggMean, LowerIsBetter),
		numericMetric("time", TypeDuration, AggMedian, LowerIsBetter),
		numericMetric("tail", TypeDuration, AggP90, LowerIsBetter),
	} {
		var a, b map[string][]any
		if m.Type == TypeBinary {
			a = map[string][]any{"t1": bools(9, 10), "t2": bools(8, 10)}
			b = map[string][]any{"t1": bools(2, 10), "t2": bools(3, 10)}
		} else {
			a = map[string][]any{"t1": nums(1, 2, 3, 4, 5, 6, 7, 8), "t2": nums(2, 3, 4, 5, 6, 7, 8, 9)}
			b = map[string][]any{"t1": nums(11, 12, 13, 14, 15, 16, 17, 18), "t2": nums(12, 13, 14, 15, 16, 17, 18, 19)}
		}
		tb := tableAt(t, smallParameters(), m, "sbm_2", entry("sbm_1", a), entry("sbm_2", b))
		r, ok := tb.ComparePair("sbm_1", "sbm_2")
		if !ok || r.Method != methodStratified || r.P == nil || r.H == nil || *r.H != *r.P || r.Q != nil {
			t.Fatalf("%s: %+v", m.Name, r)
		}
		if r.Verdict != Improved {
			t.Errorf("%s: %s (%s)", m.Name, r.Verdict, r.Reason)
		}
		if m.Aggregate != AggP90 && r.DeltaInterval == nil {
			t.Errorf("%s: no delta interval: %q", m.Name, r.NoDeltaInterval)
		}
		if m.Aggregate == AggP90 && r.NoDeltaInterval != noIntervalSmallTask("t1", 8, 29) {
			t.Errorf("p90: %q", r.NoDeltaInterval)
		}
		back, _ := tb.ComparePair("sbm_2", "sbm_1")
		if back.Delta.Value != -r.Delta.Value || *back.P != *r.P || *back.H != *r.H || back.Verdict != Regressed {
			t.Errorf("%s: read the other way %+v", m.Name, back)
		}
		if r.DeltaInterval != nil && (back.DeltaInterval.Interval[0] != -r.DeltaInterval.Interval[1]) {
			t.Errorf("%s: the interval is not negated", m.Name)
		}
		vs := ranked(tb, "sbm_1").VsReference
		if vs == nil || vs.Delta.Value != r.Delta.Value || *vs.H != *r.H {
			t.Errorf("%s: vs reference %+v", m.Name, vs)
		}
	}
	// A rate pair's interval is Agresti and Caffo's.
	m := binaryMetric("pass", HigherIsBetter)
	ten := map[string][]any{}
	ref := map[string][]any{}
	for i := range 10 {
		task := fmt.Sprintf("t%d", i)
		ten[task] = bools(5, 5)
		ref[task] = bools(5, 5)
		if i >= 8 {
			ref[task] = bools(4, 5)
		}
	}
	tb := table(t, m, "", entry("sbm_1", ten), entry("sbm_2", ref))
	r, _ := tb.ComparePair("sbm_1", "sbm_2")
	checkInterval(t, "pair Agresti and Caffo", r.DeltaInterval.Interval, -0.0912, 0.1483)
	if !near(r.Delta.Value, 0.04, 12) {
		t.Errorf("delta %v", r.Delta.Value)
	}
	// The verdict follows h, not p: three pairs where one p = 0.03 is
	// within_noise after Holm's adjustment.
	if !strings.Contains(reasonPair(0.03, 0.06, 3, 0.05), "h = 0.06") {
		t.Error("pair reason")
	}
}

func TestEntryPairConfounds(t *testing.T) {
	m := binaryMetric("pass", HigherIsBetter)
	vals := map[string][]any{"t1": bools(5, 10), "t2": bools(5, 10)}
	a, b := entry("sbm_1", vals), entry("sbm_2", vals)
	b.Values[0].Tuple.TaskHash = "sha256:t1-old"
	a.Values[1].Layers = EntryLayers{ServedByOther: 2, SentNoneToConfigured: 1, Reasoning: map[string]int{"high": 3}, ServiceVersions: map[string]map[string]int{"api": {"2": 4}}, Images: map[string]int{"img:1": 10}, Incomplete: 1}
	b.Values[1].Layers = EntryLayers{Reasoning: map[string]int{"low": 2}, ServiceVersions: map[string]map[string]int{"api": {"1": 5}}, Images: map[string]int{"img:2": 10}}
	b.Candidate.Kind = "external"
	b.Window = Window{From: epoch.Add(-30 * 24 * time.Hour), To: epoch.Add(-29 * 24 * time.Hour)}
	a.Taxonomy, b.Taxonomy = "tax_1", "tax_2"
	tb := table(t, m, "", a, b)
	r, _ := tb.ComparePair("sbm_1", "sbm_2")
	names := map[string]*Confound{}
	for i := range r.Confounds {
		names[r.Confounds[i].Name] = &r.Confounds[i]
	}
	for _, n := range []string{FieldTaskHash, layerObservedModel, layerSentModel, layerServiceVersions, confoundIncomplete, confoundDeclared, confoundWindow, confoundTaxonomy} {
		if names[n] == nil {
			t.Errorf("no %s: %+v", n, r.Confounds)
		}
	}
	// The candidates differ in model and kind: neither sent.reasoning nor
	// declared.image is named.
	if names[layerSentReasoning] != nil || names[layerDeclaredImage] != nil {
		t.Errorf("variable rule: %+v", r.Confounds)
	}
	if c := names[confoundDeclared]; c.Candidate != 0 || c.Reference != 20 {
		t.Errorf("declared %+v", c)
	}
	if c := names[FieldTaskHash]; len(c.Tasks) != 1 || c.Tasks[0] != "t1" {
		t.Errorf("task hash %+v", c)
	}
	back, _ := tb.ComparePair("sbm_2", "sbm_1")
	for _, c := range back.Confounds {
		if c.Name == layerObservedModel && (c.Candidate != 0 || c.Reference != 2) {
			t.Errorf("read the other way, the counts change sides: %+v", c)
		}
	}
	// Two candidates that agree on agent, model and reasoning name both.
	b.Candidate = a.Candidate
	b.Candidate.Name, b.Candidate.Digest = "sbm_2", "sbm_2"
	r, _ = table(t, m, "", a, b).ComparePair("sbm_1", "sbm_2")
	got := map[string]bool{}
	for _, c := range r.Confounds {
		got[c.Name] = true
	}
	if !got[layerSentReasoning] || !got[layerDeclaredImage] {
		t.Errorf("same candidate fields: %+v", r.Confounds)
	}
}

// PERMUTATION_RESAMPLES for a table resolves with its m at
// 1 - CONFIDENCE_LEVEL, and Holm's adjustment over the pairs.
func TestTableResamples(t *testing.T) {
	m := numericMetric("cost", TypeCost, AggMean, LowerIsBetter)
	var es []Entry
	for i := range 9 {
		v := make([]float64, 3)
		for j := range v {
			v[j] = float64(i + j)
		}
		es = append(es, entry(fmt.Sprintf("sbm_%d", i), map[string][]any{"t1": nums(v...), "t2": nums(v...)}))
	}
	tb := tableAt(t, smallParameters(), m, "", es...)
	if tb.Method.Pairs != 36 || tb.Method.Resamples != resamples(36, 1-Defaults().ConfidenceLevel) || tb.Method.Resamples != 14400 {
		t.Errorf("method %+v", tb.Method)
	}
}

// A results table recomputed from its recorded inputs is byte-identical,
// and its pairs' seeds are the recorded ones.
func TestRecomputeResultsTable(t *testing.T) {
	m := numericMetric("cost", TypeCost, AggMean, LowerIsBetter)
	es := []Entry{
		entry("sbm_2", map[string][]any{"t1": nums(1, 2, 3, 4, 5, 6, 7, 8, 9, 10, 11, 12), "t2": nums(2, 3, 4, 5, 6, 7, 8, 9, 10, 11, 12, 13)}),
		entry("sbm_1", map[string][]any{"t1": nums(2, 3, 4, 5, 6, 7, 8, 9, 10, 11, 12, 13), "t2": nums(1, 2, 3, 4, 5, 6, 7, 8, 9, 10, 11, 12)}),
	}
	a := tableAt(t, smallParameters(), m, "sbm_1", es...)
	b := tableAt(t, smallParameters(), m, "sbm_1", es[1], es[0])
	ja, err := json.Marshal(a)
	if err != nil {
		t.Fatal(err)
	}
	jb, err := json.Marshal(b)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(ja, jb) {
		t.Error("the same entries in another order gave another table")
	}
	root := RootSeed(a.Method.InputsDigest)
	if root.String() != a.Method.Seed {
		t.Error("the root seed is not the inputs digest's")
	}
	if want := ProcedureSeed(root, m.Digest, "pair:sbm_1:sbm_2", procTest).String(); a.Pairs[0].Seed != want {
		t.Errorf("pair seed %s, want %s", a.Pairs[0].Seed, want)
	}
	// After a change, every pair is drawn again from the new root seed.
	es[0].Values[0].Values[0] = 1.5
	c := tableAt(t, smallParameters(), m, "sbm_1", es...)
	if c.Method.Seed == a.Method.Seed || c.Pairs[0].Seed == a.Pairs[0].Seed {
		t.Error("a changed entry kept the seeds")
	}
}

func TestTableInputs(t *testing.T) {
	label := labelMetric("answer", "a", "b", "c")
	if _, err := ComputeTable(TableInput{Parameters: Defaults(), Metric: label}); !errors.Is(err, ErrInput) {
		t.Errorf("a label primary metric: %v", err)
	}
	rr := cellMetric("agreement", AggRepeatRate, label)
	if _, err := ComputeTable(TableInput{Parameters: Defaults(), Metric: rr}); !errors.Is(err, ErrInput) {
		t.Errorf("a cell-level metric without its of metric: %v", err)
	}
	bad := Defaults()
	bad.BootstrapResamples = 0
	if _, err := ComputeTable(TableInput{Parameters: bad, Metric: binaryMetric("p", HigherIsBetter)}); !errors.Is(err, ErrParameters) {
		t.Errorf("parameters: %v", err)
	}
	wrong := numericMetric("x", TypeBinary, AggMean, HigherIsBetter)
	if _, err := ComputeTable(TableInput{Parameters: Defaults(), Metric: wrong}); !errors.Is(err, ErrInput) {
		t.Errorf("a binary mean: %v", err)
	}
	// A repeat rate's table: entries of labels, the declines left out.
	tb, err := ComputeTable(TableInput{Parameters: Defaults(), Metric: rr, Of: &label, Entries: []Entry{
		entry("sbm_1", map[string][]any{"t1": strs("a", "a", "a", "a", "a", "declined"), "t2": strs("a", "a", "a", "a", "a")}),
		entry("sbm_2", map[string][]any{"t1": strs("a", "b", "c", "a", "b"), "t2": strs("c", "b", "a", "c", "b")}),
	}})
	if err != nil {
		t.Fatal(err)
	}
	r, _ := tb.ComparePair("sbm_1", "sbm_2")
	if r.Statistic != statDifference || r.NoDeltaInterval != noIntervalRepeatRate || r.P == nil {
		t.Errorf("repeat rate pair %+v", r)
	}
	if e := ranked(tb, "sbm_1"); e == nil || e.Summary.Denominator != 10 {
		t.Errorf("summary %+v", e)
	}
	// A mode share's table is descriptive.
	ms := cellMetric("mode", AggModeShare, label)
	tb, err = ComputeTable(TableInput{Parameters: Defaults(), Metric: ms, Of: &label, Entries: []Entry{
		entry("sbm_1", map[string][]any{"t1": strs("a", "a", "b"), "t2": strs("a", "a")}),
		entry("sbm_2", map[string][]any{"t1": strs("a", "b", "c"), "t2": strs("c", "b")}),
	}})
	if err != nil {
		t.Fatal(err)
	}
	if r, _ := tb.ComparePair("sbm_1", "sbm_2"); r.P != nil || r.Verdict != "" || r.Reason != reasonModeShare {
		t.Errorf("mode share pair %+v", r)
	}
}
