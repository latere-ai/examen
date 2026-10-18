// SPDX-FileCopyrightText: 2026 Latere AI
// SPDX-License-Identifier: Apache-2.0

package analysis

import (
	"errors"
	"fmt"
	"slices"
	"sort"
	"strings"
	"time"
)

// The logical variables a comparison may vary, and their tuple fields
// (spec 008). grader_digests belongs to none: a change of grader changes
// what is measured.
var logicalVariables = []struct {
	name   string
	fields []string
}{
	{"agent", []string{FieldCandidateKind, FieldAgentOrImageDigest, FieldProgram}},
	{"model", []string{FieldModel}},
	{"reasoning", []string{FieldReasoning}},
	{"tools", []string{FieldTools}},
	{"environment", []string{FieldEnvironmentDigest}},
	{"workspace", []string{FieldWorkspaceRef}},
	{"task", []string{FieldTaskHash}},
}

// tupleFields are the tuple fields compared cell by cell, in the tuple's
// order; grader_digests is compared per metric.
var tupleFields = []string{
	FieldCandidateKind, FieldAgentOrImageDigest, FieldProgram, FieldModel, FieldReasoning,
	FieldTools, FieldTaskHash, FieldEnvironmentDigest, FieldWorkspaceRef,
}

// ErrVariable is the error of a declared variable that is not one logical
// variable a request may declare.
var ErrVariable = errors.New("analysis: the variable is not one logical variable")

// VariableFields returns the tuple fields of a logical variable.
func VariableFields(name string) ([]string, bool) {
	for _, v := range logicalVariables {
		if v.name == name {
			return slices.Clone(v.fields), true
		}
	}
	return nil, false
}

// LogicalVariable reads a declared variable given as tuple fields: they
// must be exactly the fields of one logical variable a request may
// declare. task_hash is the variable of a comparison across tasks, which
// names its two tasks instead, and grader_digests belongs to no variable;
// both are refused, as is any other set.
func LogicalVariable(fields []string) (string, error) {
	got := slices.Clone(fields)
	slices.Sort(got)
	for _, f := range got {
		if f == FieldTaskHash || f == FieldGraderDigests {
			return "", fmt.Errorf("%w: %s cannot be declared", ErrVariable, f)
		}
	}
	for _, v := range logicalVariables {
		want := slices.Clone(v.fields)
		slices.Sort(want)
		if slices.Equal(got, want) {
			return v.name, nil
		}
	}
	return "", fmt.Errorf("%w: %s", ErrVariable, strings.Join(fields, ", "))
}

// variableOf is the logical variable a tuple field belongs to.
func variableOf(field string) string {
	for _, v := range logicalVariables {
		if slices.Contains(v.fields, field) {
			return v.name
		}
	}
	return ""
}

// fieldValue is a tuple field's value as compared: tools as a sorted set.
func fieldValue(t Tuple, field string) string {
	switch field {
	case FieldCandidateKind:
		return t.CandidateKind
	case FieldAgentOrImageDigest:
		return t.AgentOrImageDigest
	case FieldProgram:
		return t.Program
	case FieldModel:
		return t.Model
	case FieldReasoning:
		return t.Reasoning
	case FieldTools:
		set := slices.Clone(t.Tools)
		slices.Sort(set)
		return strings.Join(slices.Compact(set), "\x00")
	case FieldTaskHash:
		return t.TaskHash
	case FieldEnvironmentDigest:
		return t.EnvironmentDigest
	}
	return t.WorkspaceRef
}

// differingFields are the tuple fields, but grader_digests, whose values
// differ between two cells.
func differingFields(x, y Tuple) []string {
	var out []string
	for _, f := range tupleFields {
		if fieldValue(x, f) != fieldValue(y, f) {
			out = append(out, f)
		}
	}
	return out
}

// sideCount is a confound's per-side count.
type sideCount struct{ x, y int }

func (c sideCount) any() bool { return c.x > 0 || c.y > 0 }

// layers holds a paired task's layer confounds, by name, with their
// per-side counts.
type layers map[string]sideCount

// The layer confounds, in the order a result lists them.
const (
	layerObservedModel   = "observed.model"
	layerSentModel       = "sent.model"
	layerSentReasoning   = "sent.reasoning"
	layerServiceVersions = "observed.service_versions"
	layerDeclaredImage   = "declared.image"
	confoundMissing      = "missing"
	confoundSelfReported = "self_reported"
	confoundIncomplete   = "incomplete"
	confoundWindow       = "window"
	confoundUncalibrated = "uncalibrated"
	confoundDeclared     = "declared"
	confoundTaxonomy     = "taxonomy"
)

var layerOrder = []string{layerObservedModel, layerSentModel, layerSentReasoning, layerServiceVersions, layerDeclaredImage}

// servedByOther counts the requests served by a model other than the one
// they were sent to.
func servedByOther(ts []*Trial) int {
	c := 0
	for _, t := range ts {
		for _, r := range t.Requests {
			if r.Served != "" && r.Served != r.Model {
				c++
			}
		}
	}
	return c
}

// sentNoneToConfigured counts the requests of the trials that sent none of
// their requests to the configured model: the override did not take
// effect.
func sentNoneToConfigured(ts []*Trial, configured string) int {
	if configured == "" {
		return 0
	}
	c := 0
	for _, t := range ts {
		if len(t.Requests) == 0 {
			continue
		}
		hit := false
		for _, r := range t.Requests {
			if r.Model == configured {
				hit = true
				break
			}
		}
		if !hit {
			c += len(t.Requests)
		}
	}
	return c
}

// reasoningSent is the set of reasoning values sent to the configured
// model, every request's when the tuple configures none, with the requests
// that carried each.
func reasoningSent(ts []*Trial, configured string) map[string]int {
	out := map[string]int{}
	for _, t := range ts {
		for _, r := range t.Requests {
			if r.Reasoning != "" && (configured == "" || r.Model == configured) {
				out[r.Reasoning]++
			}
		}
	}
	return out
}

// onlyIn counts what x carries that y never does.
func onlyIn(x, y map[string]int) int {
	c := 0
	for k, n := range x {
		if _, ok := y[k]; !ok {
			c += n
		}
	}
	return c
}

// serviceVersions counts, for the services named on both sides, the
// finished trials of each side carrying a version the other side never
// recorded.
func serviceVersions(xs, ys []*Trial) sideCount {
	seen := func(ts []*Trial) map[string]map[string]bool {
		out := map[string]map[string]bool{}
		for _, t := range ts {
			for svc, ver := range t.ServiceVersions {
				if out[svc] == nil {
					out[svc] = map[string]bool{}
				}
				out[svc][ver] = true
			}
		}
		return out
	}
	sx, sy := seen(xs), seen(ys)
	carry := func(ts []*Trial, other map[string]map[string]bool, own map[string]map[string]bool) int {
		c := 0
		for _, t := range ts {
			for svc, ver := range t.ServiceVersions {
				if other[svc] != nil && own[svc] != nil && !other[svc][ver] {
					c++
					break
				}
			}
		}
		return c
	}
	return sideCount{carry(xs, sy, sx), carry(ys, sx, sy)}
}

// images counts each declared image of a side's external trials.
func images(ts []*Trial, external bool) map[string]int {
	out := map[string]int{}
	if !external {
		return out
	}
	for _, t := range ts {
		if t.DeclaredImage != "" {
			out[t.DeclaredImage]++
		}
	}
	return out
}

// layerConfounds computes a paired task's layer confounds from the sent and
// observed layers of both cells' finished trials, with variable the
// comparison's logical variable.
func layerConfounds(x, y *Cell, xs, ys []*Trial, variable string) layers {
	out := layers{}
	put := func(name string, c sideCount) {
		if c.any() {
			out[name] = c
		}
	}
	put(layerObservedModel, sideCount{servedByOther(xs), servedByOther(ys)})
	put(layerSentModel, sideCount{sentNoneToConfigured(xs, x.Tuple.Model), sentNoneToConfigured(ys, y.Tuple.Model)})
	if variable != "agent" && variable != "model" && variable != "reasoning" {
		rx, ry := reasoningSent(xs, x.Tuple.Model), reasoningSent(ys, y.Tuple.Model)
		put(layerSentReasoning, sideCount{onlyIn(rx, ry), onlyIn(ry, rx)})
	}
	put(layerServiceVersions, serviceVersions(xs, ys))
	if variable != "agent" {
		ix := images(xs, x.Tuple.CandidateKind == "external")
		iy := images(ys, y.Tuple.CandidateKind == "external")
		put(layerDeclaredImage, sideCount{onlyIn(ix, iy), onlyIn(iy, ix)})
	}
	return out
}

// windowGap is how far apart two windows' starts, or their ends, lie: the
// larger of the two gaps.
func windowGap(x, y window) time.Duration {
	abs := func(d time.Duration) time.Duration {
		if d < 0 {
			return -d
		}
		return d
	}
	return max(abs(x.from.Sub(y.from)), abs(x.to.Sub(y.to)))
}

// confoundSet gathers a result's confound entries, keyed by name, and
// writes them in a fixed order: the tuple fields, grader_digests, the
// layers, then missing, self_reported, incomplete, window, uncalibrated,
// declared and taxonomy.
type confoundSet struct {
	entries map[string]*Confound
	tasks   map[string]map[string]bool
	counts  map[string]*sideCount
}

func newConfoundSet() *confoundSet {
	return &confoundSet{entries: map[string]*Confound{}, tasks: map[string]map[string]bool{}, counts: map[string]*sideCount{}}
}

// add records a confound entry by name, naming the task when one is given
// and summing its counts when it has them.
func (s *confoundSet) add(name, task string, c *sideCount) {
	key := name
	if _, ok := s.entries[key]; !ok {
		s.entries[key] = &Confound{Name: name}
		s.tasks[key] = map[string]bool{}
	}
	if task != "" {
		s.tasks[key][task] = true
	}
	if c != nil {
		if s.counts[key] == nil {
			s.counts[key] = &sideCount{}
		}
		s.counts[key].x += c.x
		s.counts[key].y += c.y
	}
}

// set records an entry whole, replacing any of its name.
func (s *confoundSet) set(c Confound) {
	key := c.Name
	if c.Name == confoundUncalibrated {
		key += ":" + c.Grader
	}
	cp := c
	s.entries[key] = &cp
}

var confoundOrder = func() map[string]int {
	names := slices.Concat(tupleFields, []string{FieldGraderDigests}, layerOrder,
		[]string{confoundMissing, confoundSelfReported, confoundIncomplete, confoundWindow,
			confoundUncalibrated, confoundDeclared, confoundTaxonomy})
	out := map[string]int{}
	for i, n := range names {
		out[n] = i
	}
	return out
}()

// list writes the entries; withTasks names the tasks of each, for a
// suite-level result.
func (s *confoundSet) list(withTasks bool) []Confound {
	keys := make([]string, 0, len(s.entries))
	for k := range s.entries {
		keys = append(keys, k)
	}
	sort.Slice(keys, func(i, j int) bool {
		a, b := s.entries[keys[i]], s.entries[keys[j]]
		if confoundOrder[a.Name] != confoundOrder[b.Name] {
			return confoundOrder[a.Name] < confoundOrder[b.Name]
		}
		return keys[i] < keys[j]
	})
	out := make([]Confound, 0, len(keys))
	for _, k := range keys {
		c := *s.entries[k]
		if withTasks && len(s.tasks[k]) > 0 {
			c.Tasks = make([]string, 0, len(s.tasks[k]))
			for t := range s.tasks[k] {
				c.Tasks = append(c.Tasks, t)
			}
			slices.Sort(c.Tasks)
		}
		if n := s.counts[k]; n != nil {
			c.Candidate, c.Reference = n.x, n.y
		}
		out = append(out, c)
	}
	return out
}

// has reports whether an entry of the name is present.
func (s *confoundSet) has(name string) bool {
	for _, c := range s.entries {
		if c.Name == name {
			return true
		}
	}
	return false
}
