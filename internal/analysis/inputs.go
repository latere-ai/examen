// SPDX-FileCopyrightText: 2026 Latere AI
// SPDX-License-Identifier: Apache-2.0

package analysis

import (
	"cmp"
	"encoding/json/v2"
	"fmt"
	"slices"
	"time"

	"latere.ai/x/examen/internal/id"
)

// The inputs document of a comparison (spec 008, Determinism): everything
// its figures read, as canonical JSON. Its digest seeds every draw, so the
// same reads give the same results, and a recomputation from the recorded
// measurement ids rebuilds the same document.

type docMeasurement struct {
	ID           string `json:"id"`
	Metric       string `json:"metric"`
	Grader       string `json:"grader"`
	Generation   int    `json:"generation"`
	Value        any    `json:"value"`
	SelfReported bool   `json:"self_reported"`
	// Canonical is the canonical label an open-vocabulary value maps to;
	// absent when it maps to none.
	Canonical *string `json:"canonical,omitzero"`
}

type docTrial struct {
	ID              string            `json:"id"`
	Outcome         Outcome           `json:"outcome"`
	StartedAt       string            `json:"started_at"`
	EndedAt         string            `json:"ended_at"`
	Sent            []string          `json:"sent"`
	Served          []string          `json:"served"`
	Reasoning       []string          `json:"reasoning"`
	ServiceVersions map[string]string `json:"service_versions"`
	DeclaredImage   string            `json:"declared_image"`
	Measurements    []docMeasurement  `json:"measurements"`
}

type docExcluded struct {
	ID      string  `json:"id"`
	Outcome Outcome `json:"outcome"`
}

type docTaxonomy struct {
	ID          string `json:"id"`
	Digest      string `json:"digest"`
	PinnedByBar bool   `json:"pinned_by_bar"`
}

type docTask struct {
	Task         string                 `json:"task"`
	Digest       string                 `json:"digest"`
	Tuple        Tuple                  `json:"tuple"`
	Graders      map[string]string      `json:"graders"`
	Taxonomies   map[string]docTaxonomy `json:"taxonomies"`
	Calibrations []Calibration          `json:"calibrations"`
	Trials       []docTrial             `json:"trials"`
	Excluded     []docExcluded          `json:"excluded"`
}

type docSide struct {
	Candidate string           `json:"candidate"`
	Tasks     []docTask        `json:"tasks"`
	Values    []docMeasurement `json:"values"`
}

type document struct {
	AnalysisVersion string             `json:"analysis_version"`
	Parameters      Parameters         `json:"parameters"`
	Variable        []string           `json:"variable"`
	Exploratory     bool               `json:"exploratory"`
	Declared        int                `json:"declared,omitzero"`
	Declaration     *Declaration       `json:"declaration,omitzero"`
	LabelTests      bool               `json:"label_tests"`
	Weights         map[string]float64 `json:"weights"`
	PrimaryMetric   string             `json:"primary_metric"`
	Candidate       docSide            `json:"candidate"`
	Reference       docSide            `json:"reference"`
}

// stamp writes an instant as RFC 3339 in UTC.
func stamp(t time.Time) string { return t.UTC().Format(time.RFC3339Nano) }

// docTrialOf writes a trial with the measurements read of it.
func docTrialOf(t *Trial, read []docMeasurement) docTrial {
	dt := docTrial{
		ID: t.ID, Outcome: t.Outcome, StartedAt: stamp(t.StartedAt), EndedAt: stamp(t.EndedAt),
		Sent: []string{}, Served: []string{}, Reasoning: []string{},
		ServiceVersions: t.ServiceVersions, DeclaredImage: t.DeclaredImage, Measurements: read,
	}
	if dt.ServiceVersions == nil {
		dt.ServiceVersions = map[string]string{}
	}
	for _, r := range t.Requests {
		dt.Sent = append(dt.Sent, r.Model)
		dt.Served = append(dt.Served, r.Served)
		dt.Reasoning = append(dt.Reasoning, r.Reasoning)
	}
	slices.SortFunc(dt.Measurements, func(a, b docMeasurement) int {
		return cmp.Or(cmp.Compare(a.Metric, b.Metric), cmp.Compare(a.Grader, b.Grader))
	})
	return dt
}

// docTaskOf writes one side's cell of a task with every trial-level
// metric's reading of it.
func (c *computer) docTaskOf(cell *Cell, ct cellTrials, reads map[string]*sideRead) docTask {
	dt := docTask{
		Task: cell.Task, Digest: cell.TaskDigest, Tuple: cell.Tuple,
		Graders: map[string]string{}, Taxonomies: map[string]docTaxonomy{},
		Calibrations: []Calibration{}, Trials: []docTrial{}, Excluded: []docExcluded{},
	}
	byTrial := map[string][]docMeasurement{}
	for _, m := range c.in.Suite.Metrics {
		r := reads[m.Digest]
		if r == nil {
			continue
		}
		dt.Graders[m.Digest] = r.grader
		if t := c.taxonomy(m); t != nil {
			dt.Taxonomies[m.Digest] = docTaxonomy{ID: t.ID, Digest: t.Digest, PinnedByBar: t.PinnedByBar}
		}
		for _, cal := range c.in.Calibrations {
			if cal.Metric == m.Digest && cal.Grader == r.grader {
				dt.Calibrations = append(dt.Calibrations, cal)
			}
		}
		for _, v := range r.values {
			dm := docMeasurement{ID: v.m.ID, Metric: v.m.Metric, Grader: v.m.Grader, Generation: v.m.Generation, Value: v.m.Value, SelfReported: v.m.SelfReported}
			if m.Type == TypeLabel && m.Vocabulary == Open && !v.unmapped {
				dm.Canonical = ptr(v.label)
			}
			byTrial[v.trial.ID] = append(byTrial[v.trial.ID], dm)
		}
	}
	for _, t := range ct.finished {
		read := byTrial[t.ID]
		if read == nil {
			read = []docMeasurement{}
		}
		dt.Trials = append(dt.Trials, docTrialOf(t, read))
	}
	for _, t := range ct.incomplete {
		dt.Excluded = append(dt.Excluded, docExcluded{ID: t.ID, Outcome: t.Outcome})
	}
	return dt
}

// candidateValues writes a side's candidate-level measurements read.
func (c *computer) candidateValues(s *Side) []docMeasurement {
	out := []docMeasurement{}
	for _, m := range c.in.Suite.Metrics {
		if m.level() != LevelCandidate {
			continue
		}
		if v := candidateValue(s, m, s.Producers[m.Digest]); v != nil {
			out = append(out, docMeasurement{ID: v.ID, Metric: v.Metric, Grader: v.Grader, Generation: v.Generation, Value: v.Value, SelfReported: v.SelfReported})
		}
	}
	return out
}

// inputsDocument builds the comparison's inputs document: for the
// candidate side every cell it holds, whose readings its own suite-level
// summaries also read, and for the reference side every paired cell.
func (c *computer) inputsDocument() document {
	weights := map[string]float64{}
	for _, st := range c.suiteTasks() {
		weights[st.Digest] = c.in.Suite.weight(st.Digest)
	}
	doc := document{
		AnalysisVersion: Version, Parameters: c.p, Variable: c.varFields, Exploratory: c.in.Exploratory, Declared: c.in.Declared,
		Declaration: c.in.Declaration, LabelTests: c.in.LabelTests,
		Weights: weights, PrimaryMetric: c.in.Suite.PrimaryMetric,
		Candidate: docSide{Candidate: c.in.Candidate.Candidate, Tasks: []docTask{}, Values: c.candidateValues(&c.in.Candidate)},
		Reference: docSide{Candidate: c.in.Reference.Candidate, Tasks: []docTask{}, Values: c.candidateValues(&c.in.Reference)},
	}
	for i := range c.in.Candidate.Cells {
		cell := &c.in.Candidate.Cells[i]
		reads := map[string]*sideRead{}
		pt := c.byName[cell.Task]
		for _, m := range c.in.Suite.Metrics {
			if m.level() != LevelTrial {
				continue
			}
			if pt != nil {
				reads[m.Digest] = c.reads[pt][m.Digest].x
			} else {
				reads[m.Digest] = c.own[cell.Task+"\x00"+m.Digest]
			}
		}
		ct := c.ownTrials[cell.Task]
		if pt != nil {
			ct = pt.xt
		}
		doc.Candidate.Tasks = append(doc.Candidate.Tasks, c.docTaskOf(cell, ct, reads))
	}
	for _, pt := range c.tasks {
		reads := map[string]*sideRead{}
		for d, r := range c.reads[pt] {
			reads[d] = r.y
		}
		doc.Reference.Tasks = append(doc.Reference.Tasks, c.docTaskOf(pt.y, pt.yt, reads))
	}
	slices.SortFunc(doc.Candidate.Tasks, func(a, b docTask) int { return cmp.Compare(a.Task, b.Task) })
	slices.SortFunc(doc.Reference.Tasks, func(a, b docTask) int { return cmp.Compare(a.Task, b.Task) })
	return doc
}

// documentDigest is "sha256:" and the hex of the SHA-256 of a document's
// canonical JSON.
func documentDigest(doc any) (string, error) {
	b, err := json.Marshal(doc, id.RenderOptions)
	if err != nil {
		return "", fmt.Errorf("analysis: rendering the inputs document: %w", err)
	}
	return id.DigestJSON(b)
}

// InputsDocument returns a comparison's inputs document as canonical JSON,
// and its digest.
func InputsDocument(in Input) ([]byte, string, error) {
	c, err := newComputer(in)
	if err != nil {
		return nil, "", err
	}
	b, err := json.Marshal(c.inputsDocument(), id.RenderOptions)
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

// MeasurementsRead are the ids of the measurements a comparison of the
// input reads, sorted and each once: the trial-level values of both sides'
// inputs documents and each side's candidate-level values. They are what
// comparison_inputs records (spec 007), from which the inputs document is
// rebuilt.
func MeasurementsRead(in Input) ([]string, error) {
	c, err := newComputer(in)
	if err != nil {
		return nil, err
	}
	doc := c.inputsDocument()
	var out []string
	for _, side := range []docSide{doc.Candidate, doc.Reference} {
		for _, task := range side.Tasks {
			for _, t := range task.Trials {
				for _, m := range t.Measurements {
					out = append(out, m.ID)
				}
			}
		}
		for _, m := range side.Values {
			out = append(out, m.ID)
		}
	}
	slices.Sort(out)
	return slices.Compact(out), nil
}
