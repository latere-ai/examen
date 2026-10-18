// SPDX-FileCopyrightText: 2026 Latere AI
// SPDX-License-Identifier: Apache-2.0

package analysis

import (
	"fmt"
	"time"
)

// Fixtures: a small vocabulary to build comparisons in tests.

var epoch = time.Date(2026, 10, 1, 12, 0, 0, 0, time.UTC)

func binaryMetric(name string, d Direction) Metric {
	return Metric{Digest: "sha256:" + name, Name: name, Type: TypeBinary, Aggregate: AggRate, Direction: d}
}

func numericMetric(name string, t Type, a Aggregate, d Direction) Metric {
	return Metric{Digest: "sha256:" + name, Name: name, Type: t, Aggregate: a, Direction: d}
}

func labelMetric(name string, labels ...string) Metric {
	m := Metric{Digest: "sha256:" + name, Name: name, Type: TypeLabel, Aggregate: AggDistribution, Direction: NoDirection, Labels: labels, Vocabulary: Closed}
	if len(labels) == 0 {
		m.Vocabulary = Open
	}
	return m
}

func cellMetric(name string, a Aggregate, of Metric) Metric {
	t := TypeScore
	if a == AggDistinctCount {
		t = TypeCount
	}
	return Metric{Digest: "sha256:" + name, Name: name, Type: t, Aggregate: a, Direction: NoDirection, Level: LevelCell, Of: of.Digest}
}

// tuple is a topos candidate's tuple of a model on a task.
func tuple(model, task string) Tuple {
	return Tuple{
		CandidateKind: "topos", AgentOrImageDigest: "sha256:agent", Model: model, Reasoning: "medium",
		Tools: []string{"read", "write"}, TaskHash: "sha256:" + task, EnvironmentDigest: "sha256:env",
		WorkspaceRef: "none", GraderDigests: []string{"sha256:check"},
	}
}

const grader = "sha256:check"

// trialsOf makes one finished trial per value of a metric, graded by
// grader, with ids prefix-000 … and times a minute apart from start.
func trialsOf(prefix string, start time.Time, m Metric, values ...any) []Trial {
	out := make([]Trial, len(values))
	for i, v := range values {
		at := start.Add(time.Duration(i) * time.Minute)
		out[i] = Trial{
			ID: fmt.Sprintf("%s-%03d", prefix, i), Outcome: OutcomeCompleted,
			StartedAt: at, EndedAt: at.Add(30 * time.Second),
		}
		if v != nil {
			out[i].Measurements = []Measurement{{ID: fmt.Sprintf("msr-%s-%03d-%s", prefix, i, m.Name), Metric: m.Digest, Grader: grader, Generation: 1, Value: v}}
		}
	}
	return out
}

// bools writes k trues and n - k falses.
func bools(k, n int) []any {
	out := make([]any, n)
	for i := range out {
		out[i] = i < k
	}
	return out
}

// nums writes numbers as values.
func nums(v ...float64) []any {
	out := make([]any, len(v))
	for i, x := range v {
		out[i] = x
	}
	return out
}

// strs writes labels as values.
func strs(v ...string) []any {
	out := make([]any, len(v))
	for i, x := range v {
		out[i] = x
	}
	return out
}

// cell makes a final cell of a task whose producers are grader for every
// metric given.
func cell(task string, tp Tuple, trials []Trial, metrics ...Metric) Cell {
	prod := map[string]string{}
	for _, m := range metrics {
		prod[m.Digest] = grader
	}
	return Cell{Task: task, TaskDigest: "sha256:" + task, Tuple: tp, Producers: prod, Trials: trials, Final: true}
}

// merge joins the measurements of trials of one cell made per metric.
func merge(sets ...[]Trial) []Trial {
	out := make([]Trial, len(sets[0]))
	copy(out, sets[0])
	for i := range out {
		out[i].Measurements = append([]Measurement{}, out[i].Measurements...)
	}
	for _, s := range sets[1:] {
		for i := range s {
			out[i].Measurements = append(out[i].Measurements, s[i].Measurements...)
		}
	}
	return out
}

// pairInput makes a comparison of two models on the given tasks, the
// candidate's and the reference's values per task, of one metric.
func pairInput(m Metric, cand, ref map[string][]any) Input {
	in := Input{
		Parameters: Defaults(),
		Suite:      Suite{Metrics: []Metric{m}, PrimaryMetric: m.Digest},
		Candidate:  Side{Candidate: "sha256:cand"},
		Reference:  Side{Candidate: "sha256:ref"},
	}
	for _, task := range sortedKeys(cand) {
		in.Candidate.Cells = append(in.Candidate.Cells, cell(task, tuple("model-b", task), trialsOf("c-"+task, epoch, m, cand[task]...), m))
	}
	for _, task := range sortedKeys(ref) {
		in.Reference.Cells = append(in.Reference.Cells, cell(task, tuple("model-a", task), trialsOf("r-"+task, epoch, m, ref[task]...), m))
	}
	return in
}

func sortedKeys(m map[string][]any) []string {
	var out []string
	for k := range m {
		out = append(out, k)
	}
	for i := range out {
		for j := i + 1; j < len(out); j++ {
			if out[j] < out[i] {
				out[i], out[j] = out[j], out[i]
			}
		}
	}
	return out
}

// find returns the result of a metric at a level and task.
func find(c Comparison, metric string, level Level, task string) *Result {
	for i := range c.Results {
		r := &c.Results[i]
		if r.Metric == metric && r.Level == level && r.Task == task {
			return r
		}
	}
	return nil
}

// confound returns a result's confound of a name.
func confound(r *Result, name string) *Confound {
	for i := range r.Confounds {
		if r.Confounds[i].Name == name {
			return &r.Confounds[i]
		}
	}
	return nil
}
