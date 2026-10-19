// SPDX-FileCopyrightText: 2026 Latere AI
// SPDX-License-Identifier: Apache-2.0

package objects

import (
	"maps"
	"slices"
	"strings"

	"latere.ai/x/examen/internal/apierr"
)

// The cell variables Examen defines itself (specs/002-objects.md, Cell):
// a cell's candidate, its task, its tuple's model and reasoning, and the
// experiment it belongs to, which comparisons across experiments read. A
// task factor takes none of these names.
const (
	VarCandidate = "candidate"
	VarTask      = "task"
	VarModel     = "model"
	VarReasoning = "reasoning"
	VarBatch     = "batch"
)

// ReservedVariables are the cell variables Examen defines itself.
var ReservedVariables = []string{VarCandidate, VarTask, VarModel, VarReasoning, VarBatch}

// DesignCell is one cell an experiment's tasks and candidates make, with
// every cell variable it carries: the candidate's digest, the task's
// digest, the model and reasoning when not empty, and each factor of its
// task.
type DesignCell struct {
	Task      string
	TaskName  string
	Candidate string
	Vars      map[string]string
}

// Design is every cell of the suite's tasks and the candidates, a task's
// cells in the candidates' order and the tasks in the suite's.
func (r Resolved) Design(candidates []string, bodies []Candidate) []DesignCell {
	var out []DesignCell
	for _, td := range r.Suite.Tasks {
		t := r.Tasks[td]
		for i, cd := range candidates {
			vars := map[string]string{VarCandidate: cd, VarTask: td}
			if m := bodies[i].Model(); m != "" {
				vars[VarModel] = m
			}
			if rs := bodies[i].Reasoning(); rs != "" {
				vars[VarReasoning] = rs
			}
			maps.Copy(vars, t.Factors)
			out = append(out, DesignCell{Task: td, TaskName: t.Name, Candidate: cd, Vars: vars})
		}
	}
	return out
}

// LogicalVariableOf is the logical variable of spec 008 a cell variable
// varies: model and reasoning their own, the task and every task factor
// task, and the candidate none, the analysis resolving it from the fields
// that differ.
func LogicalVariableOf(cellVariable string) string {
	switch cellVariable {
	case VarCandidate:
		return ""
	case VarModel, VarReasoning:
		return cellVariable
	}
	return VarTask
}

// taskLevel reports whether a cell variable belongs to a cell's task: the
// task or one of its factors, rather than the candidate, its model or its
// reasoning.
func taskLevel(v string) bool {
	return v != VarCandidate && v != VarModel && v != VarReasoning && v != VarBatch
}

// differs names a cell variable on which two cells of one stratum differ
// besides the compared one and what it implies, and is empty when there is
// none: for a candidate-level variable, the task; for a factor, the
// candidate and every other factor either task declares; for the task
// itself, the candidate. A test that permutes within the stratum would
// read such a difference as the variable's.
func differs(a, b *DesignCell, variable string) string {
	if !taskLevel(variable) {
		if a.Task != b.Task {
			return VarTask
		}
		return ""
	}
	if a.Candidate != b.Candidate {
		return VarCandidate
	}
	if variable == VarTask {
		return ""
	}
	keys := slices.Sorted(maps.Keys(a.Vars))
	for _, k := range slices.Sorted(maps.Keys(b.Vars)) {
		if !slices.Contains(keys, k) {
			keys = append(keys, k)
		}
	}
	for _, k := range keys {
		if !taskLevel(k) || k == VarTask || k == variable {
			continue
		}
		va, oka := a.Vars[k]
		vb, okb := b.Vars[k]
		if oka != okb || va != vb {
			return k
		}
	}
	return ""
}

// Stratum is one stratum of a declared comparison of the general form:
// its name and its cell on each side, nil where the side holds none.
type Stratum struct {
	Name                 string
	Candidate, Reference *DesignCell
}

// Selection is a general declaration's strata, in ascending order of
// name.
type Selection struct {
	Strata []Stratum
}

// CandidateOf is the one candidate every cell of the candidate side
// belongs to, and empty when they belong to several.
func (s Selection) CandidateOf() string {
	one := ""
	for _, st := range s.Strata {
		if st.Candidate == nil {
			continue
		}
		if one != "" && one != st.Candidate.Candidate {
			return ""
		}
		one = st.Candidate.Candidate
	}
	return one
}

// NormalizeDeclaration resolves a general declaration's levels and where
// to how the design holds them: a candidate by its digest, a task by its
// digest, named by name or digest.
func (r Resolved) NormalizeDeclaration(d *DeclaredComparison, field string) error {
	norm := func(variable, at, v string) (string, error) {
		switch variable {
		case VarCandidate:
			_, digest, err := ParseRef(at, v)
			return digest, err
		case VarTask:
			if _, ok := r.Tasks[v]; ok {
				return v, nil
			}
			if digest, ok := r.TaskByName(v); ok {
				return digest, nil
			}
			return "", apierr.OnField(apierr.CodeNotInExperiment, at, "the suite pins no task "+v)
		}
		return v, nil
	}
	for i, l := range d.Levels {
		v, err := norm(d.Variable, field+".levels["+itoa(i)+"]", l)
		if err != nil {
			return err
		}
		d.Levels[i] = v
	}
	for _, k := range slices.Sorted(maps.Keys(d.Where)) {
		v, err := norm(k, field+".where."+k, d.Where[k])
		if err != nil {
			return err
		}
		d.Where[k] = v
	}
	return nil
}

// carried reports whether any cell of the design carries a variable.
func carried(design []DesignCell, v string) bool {
	return slices.ContainsFunc(design, func(c DesignCell) bool { _, ok := c.Vars[v]; return ok })
}

// kept are the cells of the design carrying each variable of where at its
// level.
func kept(design []DesignCell, where map[string]string) []*DesignCell {
	var out []*DesignCell
	for i := range design {
		c := &design[i]
		match := true
		for k, v := range where {
			if held, ok := c.Vars[k]; !ok || held != v {
				match = false
				break
			}
		}
		if match {
			out = append(out, c)
		}
	}
	return out
}

// checkVariables holds a declaration's variable, where and by to the cell
// variables the design carries: each carried, by named once, and the
// variable in neither where nor by.
func checkVariables(design []DesignCell, variable string, where map[string]string, by []string, field string) error {
	if !carried(design, variable) {
		return apierr.OnField(apierr.CodeInvalidField, field+".variable", "no cell of the experiment carries the variable "+quote(variable)+": a cell carries candidate, task, its model and reasoning, and its task's factors")
	}
	for _, k := range slices.Sorted(maps.Keys(where)) {
		switch {
		case where[k] == "":
			return apierr.OnField(apierr.CodeInvalidField, field+".where."+k, "where holds a variable to a level that is not empty")
		case k == variable:
			return apierr.OnField(apierr.CodeInvalidField, field+".where."+k, "where holds the compared variable to one level")
		case !carried(design, k):
			return apierr.OnField(apierr.CodeInvalidField, field+".where."+k, "no cell of the experiment carries the variable "+quote(k))
		}
	}
	for i, b := range by {
		at := field + ".by[" + itoa(i) + "]"
		_, inWhere := where[b]
		switch {
		case b == variable:
			return apierr.OnField(apierr.CodeInvalidField, at, "a comparison is not stratified by the variable it compares")
		case slices.Contains(by[:i], b):
			return apierr.OnField(apierr.CodeInvalidField, at, "by names "+quote(b)+" twice")
		case inWhere:
			return apierr.OnField(apierr.CodeInvalidField, at, "where already holds "+quote(b)+" to one level")
		case !carried(design, b):
			return apierr.OnField(apierr.CodeInvalidField, at, "no cell of the experiment carries the variable "+quote(b))
		}
	}
	return nil
}

// StratumName names the stratum a cell is in: its task's name when by is
// task alone, and otherwise name=level for each variable of by in its
// order, joined by ",", a task's level written as its name and a
// candidate's as its digest.
func StratumName(c *DesignCell, by []string) string {
	if len(by) == 1 && by[0] == VarTask {
		return c.TaskName
	}
	parts := make([]string, len(by))
	for i, b := range by {
		v := c.Vars[b]
		if b == VarTask {
			v = c.TaskName
		}
		parts[i] = b + "=" + v
	}
	return strings.Join(parts, ",")
}

// stratumKey is the levels of by a cell holds, which tell its stratum.
func stratumKey(c *DesignCell, by []string) string {
	parts := make([]string, len(by))
	for i, b := range by {
		parts[i] = c.Vars[b]
	}
	return strings.Join(parts, "\x00")
}

// Select reads a general declaration over an experiment's design: the
// cells where keeps at each level form the two sides, stratified by by,
// each side holding at most one cell of a stratum; without by, each side
// is one cell. It refuses a declaration that does not read as one
// comparison, naming the member at fault.
func Select(d DeclaredComparison, design []DesignCell, field string) (Selection, error) {
	if len(d.Levels) != 2 || d.Levels[0] == d.Levels[1] || slices.Contains(d.Levels, "") {
		return Selection{}, apierr.OnField(apierr.CodeInvalidField, field+".levels", "levels are two different levels of the variable, neither empty, the candidate side's first")
	}
	if err := checkVariables(design, d.Variable, d.Where, d.By, field); err != nil {
		return Selection{}, err
	}
	cells := kept(design, d.Where)
	if len(cells) == 0 {
		return Selection{}, apierr.OnField(apierr.CodeNotInExperiment, field+".where", "where selects no cell of the experiment")
	}
	sides := [2][]*DesignCell{}
	for _, c := range cells {
		held, ok := c.Vars[d.Variable]
		if !ok {
			continue
		}
		for s, l := range d.Levels {
			if held == l {
				sides[s] = append(sides[s], c)
			}
		}
	}
	for s := range sides {
		if len(sides[s]) == 0 {
			return Selection{}, apierr.OnField(apierr.CodeNotInExperiment, field+".levels["+itoa(s)+"]", "the level "+quote(d.Levels[s])+" selects no cell of the experiment")
		}
	}
	strata := map[string]*Stratum{}
	names := map[string]string{} // a stratum's name to its key
	for s, side := range sides {
		for _, c := range side {
			for _, b := range d.By {
				if _, ok := c.Vars[b]; !ok {
					return Selection{}, apierr.OnField(apierr.CodeInvalidField, field+".by", "a cell of the comparison, task "+c.TaskName+", does not carry the variable "+quote(b))
				}
			}
			key := stratumKey(c, d.By)
			st := strata[key]
			if st == nil {
				st = &Stratum{Name: StratumName(c, d.By)}
				if other, taken := names[st.Name]; taken && other != key {
					return Selection{}, apierr.OnField(apierr.CodeInvalidField, field+".by", "two strata take the name "+st.Name)
				}
				names[st.Name] = key
				strata[key] = st
			}
			at := &st.Candidate
			if s == 1 {
				at = &st.Reference
			}
			if *at != nil {
				return Selection{}, apierr.OnField(apierr.CodeInvalidField, field+".by", "two cells of one side, tasks "+(*at).TaskName+" and "+c.TaskName+", share a stratum: name the variable that separates them in by or where")
			}
			*at = c
		}
	}
	out := Selection{}
	paired := false
	for _, st := range strata {
		if st.Candidate != nil && st.Reference != nil {
			if k := differs(st.Candidate, st.Reference, d.Variable); k != "" {
				return Selection{}, apierr.OnField(apierr.CodeInvalidField, field, "the cells of tasks "+st.Candidate.TaskName+" and "+st.Reference.TaskName+", paired in one stratum, also differ in "+quote(k)+": hold it to one level in where, or name it in by")
			}
		}
		if len(d.By) == 0 && st.Candidate != nil && st.Reference != nil {
			// Without strata the comparison has the shape of one across
			// tasks: named by its two tasks, or by its one task.
			st.Name = st.Candidate.TaskName
			if st.Reference.Task != st.Candidate.Task {
				st.Name += " vs " + st.Reference.TaskName
			}
		}
		paired = paired || (st.Candidate != nil && st.Reference != nil)
		out.Strata = append(out.Strata, *st)
	}
	if !paired {
		return Selection{}, apierr.OnField(apierr.CodeInvalidField, field+".by", "no stratum holds a cell of both levels")
	}
	slices.SortFunc(out.Strata, func(a, b Stratum) int { return strings.Compare(a.Name, b.Name) })
	return out, nil
}

// IndependenceStratum is one stratum of a test of independence: its name
// and its cell at each level, nil where the level holds none.
type IndependenceStratum struct {
	Name  string
	Cells []*DesignCell
}

// IndependenceSelection is a test of independence over an experiment's
// design: the variable's levels in order, and the strata in ascending
// order of name.
type IndependenceSelection struct {
	Levels []string
	Strata []IndependenceStratum
}

// NormalizeIndependence fills a test of independence's defaults, the
// candidate, stratified by task for a variable of the candidate and by
// candidate for the task or a factor, and resolves its where as a
// declaration's.
func (r Resolved) NormalizeIndependence(in *Independence, field string) error {
	if in.Variable == "" {
		in.Variable = VarCandidate
	}
	if in.By == nil {
		in.By = []string{VarTask}
		if taskLevel(in.Variable) {
			in.By = []string{VarCandidate}
		}
	}
	d := DeclaredComparison{Where: in.Where}
	return r.NormalizeDeclaration(&d, field)
}

// SelectIndependence reads a test of independence over an experiment's
// design: one side per level of its variable among the cells where keeps,
// the experiment's candidates in their order for candidate and the
// levels ascending otherwise, at least two, and one stratum per
// combination of by's levels, in which each level holds at most one cell.
func SelectIndependence(in Independence, design []DesignCell, candidates []string, field string) (IndependenceSelection, error) {
	if err := checkVariables(design, in.Variable, in.Where, in.By, field); err != nil {
		return IndependenceSelection{}, err
	}
	cells := kept(design, in.Where)
	held := map[string]bool{}
	for _, c := range cells {
		if v, ok := c.Vars[in.Variable]; ok {
			held[v] = true
		}
	}
	var out IndependenceSelection
	if in.Variable == VarCandidate {
		for _, cd := range candidates {
			if held[cd] {
				out.Levels = append(out.Levels, cd)
			}
		}
	} else {
		out.Levels = slices.Sorted(maps.Keys(held))
	}
	if len(out.Levels) < 2 {
		return IndependenceSelection{}, apierr.OnField(apierr.CodeInvalidField, field+".variable", "the variable "+quote(in.Variable)+" holds fewer than two levels among the cells the test reads")
	}
	strata := map[string]*IndependenceStratum{}
	names := map[string]string{}
	for _, c := range cells {
		level := slices.Index(out.Levels, c.Vars[in.Variable])
		if level < 0 {
			continue
		}
		for _, b := range in.By {
			if _, ok := c.Vars[b]; !ok {
				return IndependenceSelection{}, apierr.OnField(apierr.CodeInvalidField, field+".by", "a cell of the test, task "+c.TaskName+", does not carry the variable "+quote(b))
			}
		}
		key := stratumKey(c, in.By)
		st := strata[key]
		if st == nil {
			st = &IndependenceStratum{Name: independenceStratumName(c, in.By), Cells: make([]*DesignCell, len(out.Levels))}
			if other, taken := names[st.Name]; taken && other != key {
				return IndependenceSelection{}, apierr.OnField(apierr.CodeInvalidField, field+".by", "two strata take the name "+st.Name)
			}
			names[st.Name] = key
			strata[key] = st
		}
		if st.Cells[level] != nil {
			return IndependenceSelection{}, apierr.OnField(apierr.CodeInvalidField, field+".by", "two cells of one level, tasks "+st.Cells[level].TaskName+" and "+c.TaskName+", share a stratum: name the variable that separates them in by or where")
		}
		st.Cells[level] = c
	}
	held2 := false
	for _, st := range strata {
		var first *DesignCell
		levels := 0
		for _, c := range st.Cells {
			if c == nil {
				continue
			}
			levels++
			if first == nil {
				first = c
				continue
			}
			if k := differs(first, c, in.Variable); k != "" {
				return IndependenceSelection{}, apierr.OnField(apierr.CodeInvalidField, field, "the cells of tasks "+first.TaskName+" and "+c.TaskName+", in one stratum, also differ in "+quote(k)+": hold it to one level in where, or name it in by")
			}
		}
		held2 = held2 || levels >= 2
		out.Strata = append(out.Strata, *st)
	}
	if !held2 {
		return IndependenceSelection{}, apierr.OnField(apierr.CodeInvalidField, field+".by", "no stratum holds two levels of the variable")
	}
	slices.SortFunc(out.Strata, func(a, b IndependenceStratum) int { return strings.Compare(a.Name, b.Name) })
	return out, nil
}

// independenceStratumName names a test of independence's stratum as a
// declared comparison's, and its one stratum, when by names no variable,
// all.
func independenceStratumName(c *DesignCell, by []string) string {
	if len(by) == 0 {
		return "all"
	}
	return StratumName(c, by)
}
