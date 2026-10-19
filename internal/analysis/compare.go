// SPDX-FileCopyrightText: 2026 Latere AI
// SPDX-License-Identifier: Apache-2.0

package analysis

import (
	"cmp"
	"errors"
	"fmt"
	"maps"
	"slices"
)

// ErrInput is the error of a comparison's input that breaks a rule of spec
// 002 or 008: a metric whose aggregate does not fit its type, a cell-level
// metric without its of metric, a comparison across tasks without exactly
// one cell per side.
var ErrInput = errors.New("analysis: the input is not one the analysis reads")

// pairedTask is one unit of pairing: two cells of one task, or for a
// comparison across tasks one candidate's cells of two tasks.
type pairedTask struct {
	name   string // the result's task name
	scope  string // the seed scope
	x, y   *Cell
	xt, yt cellTrials
	weight float64
	tuple  []string // tuple fields outside the variable that differ
	layers layers
}

// pairRead is one paired task's reading of one trial-level metric.
type pairRead struct{ x, y *sideRead }

// computer holds one comparison's computation.
type computer struct {
	in        Input
	p         Parameters
	z         float64
	root      Seed
	metrics   map[string]Metric
	variable  string
	varFields []string
	tasks     []*pairedTask
	byName    map[string]*pairedTask
	reads     map[*pairedTask]map[string]*pairRead
	own       map[string]*sideRead // the candidate's own reading of each of its cells, by task and metric
	ownTrials map[string]cellTrials
	unpaired  UnpairedTasks
	plans     []*plan
	family    int                // the number of results in the family
	floors    map[[2]int]float64 // Boschloo's floors by the sides' sizes
}

// validate checks the metrics and the shape of the input.
func validate(in Input) error {
	if err := in.Parameters.Validate(); err != nil {
		return err
	}
	byDigest := map[string]Metric{}
	for _, m := range in.Suite.Metrics {
		byDigest[m.Digest] = m
	}
	for _, m := range in.Suite.Metrics {
		if err := validMetric(m, byDigest); err != nil {
			return err
		}
	}
	if in.AcrossTasks || unstratified(in) {
		if len(in.Candidate.Cells) != 1 || len(in.Reference.Cells) != 1 {
			return fmt.Errorf("%w: a comparison across tasks or without strata holds one cell per side", ErrInput)
		}
		if in.AcrossTasks {
			return nil
		}
	}
	if in.Variable != "" {
		// A declaration of the general form may vary a task or one of its
		// factors, whose logical variable is task.
		if _, ok := VariableFields(in.Variable); !ok || (in.Variable == "task" && in.Declaration == nil) {
			return fmt.Errorf("%w: %q", ErrVariable, in.Variable)
		}
	}
	return nil
}

// unstratified reports whether an input is a declaration of the general
// form without strata: one cell per side, one task-level result per
// metric and no suite-level result.
func unstratified(in Input) bool {
	return in.Declaration != nil && len(in.Declaration.By) == 0
}

// singlePair reports whether a comparison pairs one cell per side and has
// no suite-level result: one across tasks, or a declaration of the general
// form without strata.
func (c *computer) singlePair() bool { return c.in.AcrossTasks || unstratified(c.in) }

// validMetric checks that a metric's aggregate fits its type and level.
func validMetric(m Metric, byDigest map[string]Metric) error {
	bad := func(why string) error { return fmt.Errorf("%w: metric %s: %s", ErrInput, m.Name, why) }
	switch m.level() {
	case LevelCandidate:
		return nil
	case LevelCell:
		of, ok := byDigest[m.Of]
		if !ok || of.Type != TypeLabel || of.level() != LevelTrial {
			return bad("a cell-level metric reads a trial-level label metric of the suite")
		}
		if m.Aggregate != AggRepeatRate && m.Aggregate != AggModeShare && m.Aggregate != AggDistinctCount {
			return bad("a cell-level metric's aggregate is repeat_rate, mode_share or distinct_count")
		}
		return nil
	}
	switch {
	case m.Type == TypeBinary && m.Aggregate == AggRate:
	case m.Type == TypeLabel && m.Aggregate == AggDistribution:
	case m.numeric() && (m.Aggregate == AggMean || m.Aggregate == AggMedian || m.Aggregate == AggP90):
	default:
		return bad(fmt.Sprintf("the aggregate %q does not fit the type %q", m.Aggregate, m.Type))
	}
	return nil
}

// Compare computes one comparison of a candidate's cells against a
// reference, deterministically: the same input gives the same record, bit
// for bit.
func Compare(in Input) (Comparison, error) {
	c, err := newComputer(in)
	if err != nil {
		return Comparison{}, err
	}
	doc := c.inputsDocument()
	digest, err := documentDigest(doc)
	if err != nil {
		return Comparison{}, err
	}
	c.root = RootSeed(digest)
	c.plan()
	c.classify()
	for _, pl := range c.plans {
		c.compute(pl)
	}
	c.correct()
	for _, pl := range c.plans {
		c.decide(pl)
	}
	return c.assemble(digest), nil
}

// newComputer validates an input, pairs its cells, resolves its variable
// and reads every metric.
func newComputer(in Input) (*computer, error) {
	if err := validate(in); err != nil {
		return nil, err
	}
	if in.Declared < 0 {
		return nil, fmt.Errorf("%w: a declared family of %d comparisons", ErrInput, in.Declared)
	}
	if in.Declared > 0 {
		in.Exploratory = false
	} else if in.AcrossTasks {
		// A comparison across tasks is made on request unless the
		// experiment declared it.
		in.Exploratory = true
	}
	c := &computer{in: in, p: in.Parameters, z: in.Parameters.z(), metrics: map[string]Metric{}}
	for _, m := range in.Suite.Metrics {
		c.metrics[m.Digest] = m
	}
	c.pair()
	c.resolveVariable()
	c.readAll()
	return c, nil
}

// pair pairs the two sides' cells by task name, or for a comparison across
// tasks pairs the two cells.
func (c *computer) pair() {
	c.byName = map[string]*pairedTask{}
	if c.in.AcrossTasks {
		x, y := &c.in.Candidate.Cells[0], &c.in.Reference.Cells[0]
		pt := &pairedTask{
			name: x.Task + " vs " + y.Task, scope: acrossTasksScope(x.Task, y.Task),
			x: x, y: y, xt: sortTrials(x), yt: sortTrials(y), weight: c.in.Suite.weight(x.TaskDigest),
		}
		c.tasks = []*pairedTask{pt}
		c.byName[x.Task] = pt
		c.unpaired = UnpairedTasks{CandidateOnly: []string{}, ReferenceOnly: []string{}}
		return
	}
	ref := map[string]*Cell{}
	for i := range c.in.Reference.Cells {
		ref[c.in.Reference.Cells[i].Task] = &c.in.Reference.Cells[i]
	}
	cand := map[string]bool{}
	c.unpaired = UnpairedTasks{CandidateOnly: []string{}, ReferenceOnly: []string{}}
	for i := range c.in.Candidate.Cells {
		x := &c.in.Candidate.Cells[i]
		cand[x.Task] = true
		y, ok := ref[x.Task]
		if !ok {
			c.unpaired.CandidateOnly = append(c.unpaired.CandidateOnly, x.Task)
			continue
		}
		pt := &pairedTask{
			name: x.Task, scope: taskScope(x.Task), x: x, y: y,
			xt: sortTrials(x), yt: sortTrials(y), weight: c.in.Suite.weight(x.TaskDigest),
		}
		c.tasks = append(c.tasks, pt)
		c.byName[x.Task] = pt
	}
	for name := range ref {
		if !cand[name] {
			c.unpaired.ReferenceOnly = append(c.unpaired.ReferenceOnly, name)
		}
	}
	slices.SortFunc(c.tasks, func(a, b *pairedTask) int { return cmp.Compare(a.name, b.name) })
	slices.Sort(c.unpaired.CandidateOnly)
	slices.Sort(c.unpaired.ReferenceOnly)
}

// resolveVariable fixes the comparison's logical variable: the one it
// declared; for a comparison across tasks, task; otherwise the one logical
// variable whose fields differ on the paired tasks, or none when no field
// or fields of several differ. It then records each paired task's tuple
// confounds, the differing fields outside the variable, and its layer
// confounds.
func (c *computer) resolveVariable() {
	switch {
	case c.in.AcrossTasks:
		c.variable = "task"
	case c.in.Variable != "":
		c.variable = c.in.Variable
	default:
		vars := map[string]bool{}
		for _, pt := range c.tasks {
			for _, f := range differingFields(pt.x.Tuple, pt.y.Tuple) {
				vars[variableOf(f)] = true
			}
		}
		if len(vars) == 1 {
			for v := range vars {
				c.variable = v
			}
		}
	}
	c.varFields, _ = VariableFields(c.variable)
	if c.varFields == nil {
		c.varFields = []string{}
	}
	for _, pt := range c.tasks {
		for _, f := range differingFields(pt.x.Tuple, pt.y.Tuple) {
			if !slices.Contains(c.varFields, f) {
				pt.tuple = append(pt.tuple, f)
			}
		}
		pt.layers = layerConfounds(pt.x, pt.y, pt.xt.finished, pt.yt.finished, c.variable)
	}
}

// trialMetric is the trial-level metric a metric reads: itself, or a
// cell-level metric's of metric.
func (c *computer) trialMetric(m Metric) Metric {
	if m.level() == LevelCell {
		return c.metrics[m.Of]
	}
	return m
}

// taxonomy is the record an open-vocabulary metric is read at.
func (c *computer) taxonomy(m Metric) *Taxonomy {
	if m.Type != TypeLabel || m.Vocabulary != Open {
		return nil
	}
	if t, ok := c.in.Taxonomies[m.Digest]; ok {
		return &t
	}
	return nil
}

// readAll reads every trial-level metric on every paired task, and the
// candidate's own cells for its suite-level summaries. The candidate side
// is read at the producing grader its suite names; the reference side at
// the candidate side's producing grader when every finished reference
// trial has a grading by it, and at its own producer otherwise.
func (c *computer) readAll() {
	c.reads = map[*pairedTask]map[string]*pairRead{}
	c.own = map[string]*sideRead{}
	c.ownTrials = map[string]cellTrials{}
	for _, pt := range c.tasks {
		c.reads[pt] = map[string]*pairRead{}
		for _, m := range c.in.Suite.Metrics {
			if m.level() != LevelTrial {
				continue
			}
			tax := c.taxonomy(m)
			gx := pt.x.Producers[m.Digest]
			gy := pt.y.Producers[m.Digest]
			if gx != "" && hasGrading(pt.yt, m, gx) {
				gy = gx
			}
			c.reads[pt][m.Digest] = &pairRead{
				x: readMetric(pt.x, pt.xt, m, gx, tax),
				y: readMetric(pt.y, pt.yt, m, gy, tax),
			}
		}
	}
	for i := range c.in.Candidate.Cells {
		x := &c.in.Candidate.Cells[i]
		ct := sortTrials(x)
		c.ownTrials[x.Task] = ct
		for _, m := range c.in.Suite.Metrics {
			if m.level() != LevelTrial {
				continue
			}
			c.own[x.Task+"\x00"+m.Digest] = readMetric(x, ct, m, x.Producers[m.Digest], c.taxonomy(m))
		}
	}
}

// read is a paired task's reading of the trial-level metric a metric
// reads.
func (c *computer) read(pt *pairedTask, m Metric) *pairRead {
	return c.reads[pt][c.trialMetric(m).Digest]
}

// primaryM is the m the primary result's PERMUTATION_RESAMPLES and power
// floor resolve with: D for a declared comparison, whose primary result is
// corrected in the declared family, and 1 for one tested alone.
func (c *computer) primaryM() int {
	return max(c.in.Declared, 1)
}

// external reports whether a side's candidate is of kind external.
func external(s Side) bool {
	for _, cell := range s.Cells {
		if cell.Tuple.CandidateKind == "external" {
			return true
		}
	}
	return false
}

// allFinal reports whether every cell of the paired tasks is final on a
// side.
func (c *computer) allFinal() bool {
	for _, pt := range c.tasks {
		if !pt.x.Final || !pt.y.Final {
			return false
		}
	}
	return len(c.tasks) > 0
}

// assemble writes the comparison record.
func (c *computer) assemble(digest string) Comparison {
	out := Comparison{
		Variable:           c.varFields,
		Exploratory:        c.in.Exploratory,
		LabelTests:         c.in.LabelTests,
		Final:              c.allFinal(),
		SingleVariable:     true,
		DeclaredProvenance: []string{},
		Confounds:          []string{},
		Results:            make([]Result, 0, len(c.plans)),
		UnpairedTasks:      c.unpaired,
		CandidateSuite:     c.candidateSuite(),
		VerdictCounts:      map[string]map[Verdict]int{},
		AnalysisVersion:    Version,
		Parameters:         c.p,
		InputsDigest:       digest,
		Seed:               c.root.String(),
	}
	if external(c.in.Candidate) {
		out.DeclaredProvenance = append(out.DeclaredProvenance, "candidate")
	}
	if external(c.in.Reference) {
		out.DeclaredProvenance = append(out.DeclaredProvenance, "reference")
	}
	family := c.family
	names := map[string]bool{}
	for _, pl := range c.plans {
		out.Results = append(out.Results, pl.res)
		for _, cf := range pl.res.Confounds {
			names[cf.Name] = true
		}
		if pl.level == LevelTask && pl.res.Verdict != "" {
			if out.VerdictCounts[pl.metric.Digest] == nil {
				out.VerdictCounts[pl.metric.Digest] = map[Verdict]int{}
			}
			out.VerdictCounts[pl.metric.Digest][pl.res.Verdict]++
		}
	}
	for _, pt := range c.tasks {
		if len(pt.tuple) > 0 {
			out.SingleVariable = false
		}
	}
	for _, pl := range c.plans {
		for _, cf := range pl.res.Confounds {
			if cf.Name == FieldGraderDigests {
				out.SingleVariable = false
			}
		}
	}
	out.Confounds = slices.Sorted(maps.Keys(names))
	if out.Confounds == nil {
		out.Confounds = []string{}
	}
	out.Correction = Correction{Method: "benjamini_hochberg", Family: family, Declared: c.in.Declared}
	if !c.in.Exploratory {
		out.Correction.Primary = c.in.Suite.PrimaryMetric
	}
	out.Resamples = Resolved{Primary: resamples(c.primaryM(), c.p.FalseDiscoveryRate), Family: resamples(family, c.p.FalseDiscoveryRate)}
	var wx, wy window
	for _, pt := range c.tasks {
		for _, t := range pt.xt.finished {
			wx.add(t.StartedAt, t.EndedAt)
		}
		for _, t := range pt.yt.finished {
			wy.add(t.StartedAt, t.EndedAt)
		}
	}
	out.Windows = Windows{Candidate: wx.public(), Reference: wy.public()}
	if len(c.in.Taxonomies) > 0 {
		out.Taxonomies = map[string]string{}
		for _, m := range c.in.Suite.Metrics {
			if t := c.taxonomy(m); t != nil {
				out.Taxonomies[m.Digest] = t.ID
			}
		}
	}
	return out
}
