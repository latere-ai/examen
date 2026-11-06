// SPDX-FileCopyrightText: 2026 Latere AI
// SPDX-License-Identifier: Apache-2.0

package analysis

import (
	"encoding/json/v2"
	"fmt"
	"slices"

	"latere.ai/x/examen/internal/id"
)

// The stored analysis (spec 008): the figures an experiment shows before
// any comparison exists, computed by the same procedures as a comparison,
// each from its own inputs document, so recomputing them when nothing
// changed is bit-identical.

// Spend is what a cell's or a candidate's trials and judge gradings cost:
// USD sums the priced trials, the smoke trial and failed trials included,
// and the priced judge gradings; JudgeUSD is the part judge gradings made;
// NotPriced counts the trials whose cost is unpriced or unknown, and
// JudgesNotPriced the judge gradings, which the sums leave out and state,
// never reading them as zero; ServiceUSD sums the costs the trials' APIs
// reported for themselves, kept apart and never added into USD; and
// CostPerSuccess, when the suite's primary metric is binary, is the cost
// of the finished trials over the number of true values, absent when there
// are none or when any finished trial's cost is not known.
type Spend struct {
	USD             float64  `json:"usd"`
	JudgeUSD        float64  `json:"judge_usd"`
	NotPriced       int      `json:"not_priced"`
	JudgesNotPriced int      `json:"judges_not_priced"`
	ServiceUSD      float64  `json:"service_usd"`
	CostPerSuccess  *float64 `json:"cost_per_success,omitzero"`
}

// add sums another spend into this one, cost per success aside.
func (s *Spend) add(o Spend) {
	s.USD += o.USD
	s.JudgeUSD += o.JudgeUSD
	s.NotPriced += o.NotPriced
	s.JudgesNotPriced += o.JudgesNotPriced
	s.ServiceUSD += o.ServiceUSD
}

// CellInput is what one cell's stored analysis reads.
type CellInput struct {
	Parameters Parameters          `json:"parameters"`
	Suite      Suite               `json:"suite"`
	Cell       Cell                `json:"cell"`
	Taxonomies map[string]Taxonomy `json:"taxonomies,omitempty"`
}

// CellAnalysis is one cell's stored analysis.
type CellAnalysis struct {
	Summaries    map[string]Summary `json:"summaries"`
	Outcomes     map[Outcome]int    `json:"outcomes"`
	Spend        Spend              `json:"spend"`
	InputsDigest string             `json:"inputs_digest"`
	Seed         string             `json:"seed"`
}

// spend sums a cell's costs.
func spend(c *Cell, ct cellTrials, primary *sideRead) Spend {
	var s Spend
	for _, t := range c.Trials {
		switch t.Cost.Status {
		case CostPriced:
			s.USD += t.Cost.USD
		default:
			s.NotPriced++
		}
		s.ServiceUSD += t.Cost.Service
	}
	for _, j := range c.Judges {
		if j.Status == CostPriced {
			s.USD += j.USD
			s.JudgeUSD += j.USD
		} else {
			s.JudgesNotPriced++
		}
	}
	if primary == nil {
		return s
	}
	known := true
	cost := 0.0
	for _, t := range ct.finished {
		if t.Cost.Status != CostPriced {
			known = false
		}
		cost += t.Cost.USD
	}
	if k := ones(primary.nums()); known && k > 0 {
		s.CostPerSuccess = ptr(cost / float64(k))
	}
	return s
}

// cellComputer is a computer over one cell as its candidate side, for the
// stored analysis.
func cellComputer(in CellInput) (*computer, error) {
	cin := Input{
		Parameters: in.Parameters, Suite: in.Suite, Taxonomies: in.Taxonomies,
		Candidate: Side{Cells: []Cell{in.Cell}},
	}
	if err := validate(cin); err != nil {
		return nil, err
	}
	c := &computer{in: cin, p: in.Parameters, z: in.Parameters.z(), metrics: map[string]Metric{}}
	for _, m := range in.Suite.Metrics {
		c.metrics[m.Digest] = m
	}
	c.pair()
	c.readAll()
	return c, nil
}

// storedDocument is a stored figure's inputs document: the cell's or
// candidate's finished trials with the measurements they read.
func (c *computer) storedDocument() ([]byte, string, error) {
	doc := struct {
		AnalysisVersion string     `json:"analysis_version"`
		Parameters      Parameters `json:"parameters"`
		Tasks           []docTask  `json:"tasks"`
	}{AnalysisVersion: Version, Parameters: c.p, Tasks: c.inputsDocument().Candidate.Tasks}
	b, err := json.Marshal(doc, id.RenderOptions)
	if err != nil {
		return nil, "", fmt.Errorf("analysis: rendering the inputs document: %w", err)
	}
	canon, err := id.Canonical(b)
	if err != nil {
		return nil, "", err
	}
	digest, err := id.DigestJSON(canon)
	return canon, digest, err
}

// AnalyzeCell computes a cell's stored analysis: its summary of every
// trial-level and cell-level metric, its outcome counts and its spend.
func AnalyzeCell(in CellInput) (CellAnalysis, error) {
	c, err := cellComputer(in)
	if err != nil {
		return CellAnalysis{}, err
	}
	_, digest, err := c.storedDocument()
	if err != nil {
		return CellAnalysis{}, err
	}
	c.root = RootSeed(digest)
	cell := &c.in.Candidate.Cells[0]
	ct := c.ownTrials[cell.Task]
	out := CellAnalysis{Summaries: map[string]Summary{}, Outcomes: map[Outcome]int{}, InputsDigest: digest, Seed: c.root.String()}
	for _, t := range cell.Trials {
		if t.Outcome != "" {
			out.Outcomes[t.Outcome]++
		}
	}
	var primary *sideRead
	for _, m := range in.Suite.Metrics {
		if m.level() == LevelCandidate {
			continue
		}
		of := c.trialMetric(m)
		r := c.own[cell.Task+"\x00"+of.Digest]
		g := newGenerator(ProcedureSeed(c.root, m.Digest, taskScope(cell.Task), procSummaryCandidate))
		switch {
		case m.level() == LevelCell:
			order := labelOrder(of, c.taxonomy(of), r.values)
			out.Summaries[m.Digest] = cellLevelSummary(m, readCellStat(m, r, order))
		case m.Type == TypeLabel:
			out.Summaries[m.Digest] = labelSummary(m, r, labelOrder(m, c.taxonomy(m), r.values), c.z)
		default:
			out.Summaries[m.Digest] = cellSummary(m, r, c.p, g)
		}
		if m.Digest == in.Suite.PrimaryMetric && m.Type == TypeBinary {
			primary = r
		}
	}
	out.Spend = spend(cell, ct, primary)
	return out, nil
}

// CandidateInput is what a candidate's stored suite-level analysis reads:
// its cells in one experiment.
type CandidateInput struct {
	Parameters Parameters          `json:"parameters"`
	Suite      Suite               `json:"suite"`
	Cells      []Cell              `json:"cells"`
	Taxonomies map[string]Taxonomy `json:"taxonomies,omitempty"`
}

// CandidateAnalysis is a candidate's stored analysis in one experiment: its
// suite-level summary of every metric and its cells' spend summed.
type CandidateAnalysis struct {
	Summaries    map[string]Summary `json:"summaries"`
	Spend        Spend              `json:"spend"`
	InputsDigest string             `json:"inputs_digest"`
	Seed         string             `json:"seed"`
}

// AnalyzeCandidate computes a candidate's stored suite-level analysis.
func AnalyzeCandidate(in CandidateInput) (CandidateAnalysis, error) {
	cin := Input{Parameters: in.Parameters, Suite: in.Suite, Taxonomies: in.Taxonomies, Candidate: Side{Cells: in.Cells}}
	if err := validate(cin); err != nil {
		return CandidateAnalysis{}, err
	}
	c := &computer{in: cin, p: in.Parameters, z: in.Parameters.z(), metrics: map[string]Metric{}}
	for _, m := range in.Suite.Metrics {
		c.metrics[m.Digest] = m
	}
	c.pair()
	c.readAll()
	_, digest, err := c.storedDocument()
	if err != nil {
		return CandidateAnalysis{}, err
	}
	c.root = RootSeed(digest)
	out := CandidateAnalysis{Summaries: c.candidateSuite(), InputsDigest: digest, Seed: c.root.String()}
	var cost float64
	known, k := true, 0
	cells := slices.Clone(in.Cells)
	slices.SortFunc(cells, func(a, b Cell) int {
		if a.Task < b.Task {
			return -1
		}
		if a.Task > b.Task {
			return 1
		}
		return 0
	})
	for i := range cells {
		cell := &cells[i]
		ct := sortTrials(cell)
		var primary *sideRead
		if m, ok := c.metrics[in.Suite.PrimaryMetric]; ok && m.Type == TypeBinary {
			primary = c.own[cell.Task+"\x00"+m.Digest]
		}
		out.Spend.add(spend(cell, ct, nil))
		for _, t := range ct.finished {
			if t.Cost.Status != CostPriced {
				known = false
			}
			cost += t.Cost.USD
		}
		if primary != nil {
			k += ones(primary.nums())
		}
	}
	if m, ok := c.metrics[in.Suite.PrimaryMetric]; ok && m.Type == TypeBinary && known && k > 0 {
		out.Spend.CostPerSuccess = ptr(cost / float64(k))
	}
	return out, nil
}
