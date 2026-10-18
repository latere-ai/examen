// SPDX-FileCopyrightText: 2026 Latere AI
// SPDX-License-Identifier: Apache-2.0

package analysis

import (
	"encoding/json"
	"errors"
	"testing"
)

// costCell is a cell of a binary primary metric whose trials cost what is
// given, with an infrastructure failure and a judge grading.
func costCell() (Cell, Metric) {
	m := binaryMetric("pass", HigherIsBetter)
	c := cell("t1", tuple("model-a", "t1"), trialsOf("c", epoch, m, true, false, true, true), m)
	for i := range c.Trials {
		c.Trials[i].Cost = Cost{USD: 0.25, Status: CostPriced, Service: 0.01}
	}
	c.Trials = append(c.Trials, Trial{ID: "c-900", Outcome: OutcomeInfraFailed, Cost: Cost{USD: 0.05, Status: CostPriced}})
	c.Judges = []Cost{{USD: 0.02, Status: CostPriced}, {Status: CostUnknown}}
	return c, m
}

// A trial of unknown cost is counted apart in the spend and never as zero.
func TestUnknownCostNeverZero(t *testing.T) {
	c, m := costCell()
	in := CellInput{Parameters: Defaults(), Suite: Suite{Metrics: []Metric{m}, PrimaryMetric: m.Digest}, Cell: c}
	a, err := AnalyzeCell(in)
	if err != nil {
		t.Fatal(err)
	}
	s := a.Spend
	if !near(s.USD, 1.07, 12) || !near(s.JudgeUSD, 0.02, 12) || s.NotPriced != 0 || s.JudgesNotPriced != 1 || !near(s.ServiceUSD, 0.04, 12) {
		t.Errorf("spend %+v", s)
	}
	if s.CostPerSuccess == nil || !near(*s.CostPerSuccess, 1.0/3, 12) {
		t.Errorf("cost per success %v, want the finished trials' 1.00 over 3 successes", s.CostPerSuccess)
	}
	// One trial of unknown cost: left out of the sum and counted, and the
	// cost per success is absent rather than read as if it cost nothing.
	in.Cell.Trials[1].Cost = Cost{Status: CostUnknown}
	a, err = AnalyzeCell(in)
	if err != nil {
		t.Fatal(err)
	}
	if a.Spend.NotPriced != 1 || !near(a.Spend.USD, 0.82, 12) || a.Spend.CostPerSuccess != nil {
		t.Errorf("unknown cost: %+v", a.Spend)
	}
	if a.Outcomes[OutcomeCompleted] != 4 || a.Outcomes[OutcomeInfraFailed] != 1 {
		t.Errorf("outcomes %v", a.Outcomes)
	}
	// No success: no cost per success.
	for i := range in.Cell.Trials[:4] {
		in.Cell.Trials[i].Cost = Cost{USD: 0.25, Status: CostPriced}
		in.Cell.Trials[i].Measurements[0].Value = false
	}
	if a, _ := AnalyzeCell(in); a.Spend.CostPerSuccess != nil {
		t.Errorf("no success: %v", *a.Spend.CostPerSuccess)
	}
}

func TestStoredAnalysis(t *testing.T) {
	in := golden()
	cin := CellInput{Parameters: in.Parameters, Suite: in.Suite, Cell: in.Candidate.Cells[0]}
	a, err := AnalyzeCell(cin)
	if err != nil {
		t.Fatal(err)
	}
	b, err := AnalyzeCell(cin)
	if err != nil {
		t.Fatal(err)
	}
	ja, _ := json.Marshal(a)
	jb, _ := json.Marshal(b)
	if string(ja) != string(jb) || a.Seed != RootSeed(a.InputsDigest).String() {
		t.Error("a stored analysis recomputed from the same inputs differs")
	}
	if len(a.Summaries) != len(in.Suite.Metrics) {
		t.Errorf("summaries of %d metrics, want %d", len(a.Summaries), len(in.Suite.Metrics))
	}
	for d, s := range a.Summaries {
		if s.Interval == nil && s.NoInterval == "" && s.Distribution == nil {
			t.Errorf("%s: no interval and no sentence", d)
		}
	}
	cand := CandidateInput{Parameters: in.Parameters, Suite: in.Suite, Cells: in.Candidate.Cells}
	ca, err := AnalyzeCandidate(cand)
	if err != nil {
		t.Fatal(err)
	}
	cmp := mustCompare(t, in)
	for d, s := range ca.Summaries {
		cs := cmp.CandidateSuite[d]
		if v1, _ := s.Number(); s.Value != nil {
			if v2, _ := cs.Number(); v1 != v2 {
				t.Errorf("%s: stored %v, comparison's candidate suite %v", d, v1, v2)
			}
		}
	}
	// A candidate's spend sums its cells'.
	c1, m := costCell()
	c2, _ := costCell()
	c2.Task, c2.TaskDigest = "t2", "sha256:t2"
	ca, err = AnalyzeCandidate(CandidateInput{Parameters: Defaults(), Suite: Suite{Metrics: []Metric{m}, PrimaryMetric: m.Digest}, Cells: []Cell{c2, c1}})
	if err != nil {
		t.Fatal(err)
	}
	if !near(ca.Spend.USD, 2.14, 12) || ca.Spend.JudgesNotPriced != 2 || ca.Spend.CostPerSuccess == nil || !near(*ca.Spend.CostPerSuccess, 2.0/6, 12) {
		t.Errorf("candidate spend %+v", ca.Spend)
	}
	bad := cand
	bad.Parameters.TieTolerance = 2
	if _, err := AnalyzeCandidate(bad); !errors.Is(err, ErrParameters) {
		t.Errorf("parameters: %v", err)
	}
	cin.Parameters = bad.Parameters
	if _, err := AnalyzeCell(cin); !errors.Is(err, ErrParameters) {
		t.Errorf("parameters: %v", err)
	}
}
