// SPDX-FileCopyrightText: 2026 Latere AI
// SPDX-License-Identifier: Apache-2.0

package objects

import (
	"encoding/json"
	"maps"
	"slices"
	"strings"
	"testing"

	"latere.ai/x/examen/internal/apierr"
	"latere.ai/x/examen/internal/id"
	"latere.ai/x/examen/internal/limits"
)

// A task's factors are checked at apply: at most MAX_TASK_FACTORS, each
// name and level under the name rule, and no name a cell variable Examen
// defines itself; a task without factors renders as it did before factors
// existed, so its digest is unchanged, and one with them has another.
func TestTaskFactors(t *testing.T) {
	ok := task()
	ok.Factors = map[string]string{"object": "build", "volition": "want"}
	if err := ok.Validate(); err != nil {
		t.Fatal(err)
	}
	many := map[string]string{}
	for i := range limits.MaxTaskFactors + 1 {
		many["f"+itoa(i)] = "x"
	}
	for _, c := range []struct {
		name    string
		factors map[string]string
		code    string
		field   string
	}{
		{"too many", many, apierr.CodeLimitExceeded, "factors"},
		{"a name out of the rule", map[string]string{"Object": "build"}, apierr.CodeInvalidField, "factors"},
		{"a reserved name", map[string]string{"model": "m-1"}, apierr.CodeInvalidField, "factors.model"},
		{"the reserved batch", map[string]string{"batch": "pilot"}, apierr.CodeInvalidField, "factors.batch"},
		{"a level out of the rule", map[string]string{"object": "Build it"}, apierr.CodeInvalidField, "factors.object"},
		{"an empty level", map[string]string{"object": ""}, apierr.CodeInvalidField, "factors.object"},
	} {
		tk := task()
		tk.Factors = c.factors
		expect(t, c.name, tk.Validate(), c.code, c.field)
	}
	raw, err := json.Marshal(task())
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(raw), "factors") {
		t.Fatalf("a task without factors renders them: %s", raw)
	}
	plain, err := id.Digest(task())
	if err != nil {
		t.Fatal(err)
	}
	factored, err := id.Digest(ok)
	if err != nil || factored == plain {
		t.Fatalf("factors leave the digest as it was: %v", err)
	}
}

// study is a design of a study's five tasks, which declare the factors
// object, volition and workspace, and a sixth task that declares none,
// over four candidates: two harnesses of each of two models.
func study() (Resolved, []string, []DesignCell) {
	tasks := []struct {
		digest, name string
		factors      map[string]string
	}{
		{d1, "build", map[string]string{"object": "build", "volition": "none", "workspace": "empty"}},
		{d2, "build-want", map[string]string{"object": "build", "volition": "want", "workspace": "empty"}},
		{d3, "do", map[string]string{"object": "do", "volition": "none", "workspace": "empty"}},
		{d4, "do-want", map[string]string{"object": "do", "volition": "want", "workspace": "empty"}},
		{d5, "do-want-seeded", map[string]string{"object": "do", "volition": "want", "workspace": "seeded"}},
		{"sha256:6666666666666666666666666666666666666666666666666666666666666666", "free", nil},
	}
	r := Resolved{Tasks: map[string]Task{}}
	for _, tk := range tasks {
		r.Suite.Tasks = append(r.Suite.Tasks, tk.digest)
		r.Tasks[tk.digest] = Task{Name: tk.name, Factors: tk.factors}
	}
	cands := []string{"sha256:" + strings.Repeat("a", 64), "sha256:" + strings.Repeat("b", 64), "sha256:" + strings.Repeat("c", 64), "sha256:" + strings.Repeat("e", 64)}
	bodies := []Candidate{external(), external(), external(), external()}
	bodies[1].External = &External{Harness: "codex", Version: "1", Model: "claude-haiku-5-5", Reasoning: "high"}
	bodies[2].External = &External{Harness: "goalless", Version: "1", Model: "m-2", Reasoning: "high"}
	bodies[3].External = &External{Harness: "codex", Version: "1", Model: "m-2", Reasoning: "low"}
	return r, cands, r.Design(cands, bodies)
}

// The design is every task × candidate, each cell carrying its candidate,
// its task, its model and its reasoning when not empty, and its task's
// factors.
func TestDesign(t *testing.T) {
	_, cands, design := study()
	if len(design) != 24 {
		t.Fatalf("%d cells", len(design))
	}
	first := design[0]
	want := map[string]string{"candidate": cands[0], "task": d1, "model": "claude-haiku-5-5", "reasoning": "high", "object": "build", "volition": "none", "workspace": "empty"}
	if !maps.Equal(first.Vars, want) || first.TaskName != "build" || design[1].Candidate != cands[1] {
		t.Fatalf("the first cell %+v", first)
	}
	r, _, _ := study()
	bare := r.Design([]string{cands[0]}, []Candidate{{Name: "bare", Kind: CandidateExternal, External: &External{Harness: "h", Version: "1"}}})
	if _, ok := bare[0].Vars["reasoning"]; ok {
		t.Fatalf("a candidate that declares no reasoning carries one: %v", bare[0].Vars)
	}
	if _, ok := bare[0].Vars["model"]; ok {
		t.Fatalf("a candidate that declares no model carries one: %v", bare[0].Vars)
	}
	if free := design[20]; free.TaskName != "free" || len(free.Vars) != 4 {
		t.Fatalf("a task without factors %+v", free)
	}
}

// A declaration of the general form selects its two sides by the levels
// of its variable among the cells where keeps, pairs them by stratum, and
// names each stratum by its task, its levels, or without by its two
// tasks.
func TestSelect(t *testing.T) {
	r, c, design := study()
	sel := func(d DeclaredComparison) Selection {
		t.Helper()
		if err := r.NormalizeDeclaration(&d, "c"); err != nil {
			t.Fatal(err)
		}
		s, err := Select(d, design, "c")
		if err != nil {
			t.Fatal(err)
		}
		return s
	}
	names := func(s Selection) []string {
		var out []string
		for _, st := range s.Strata {
			out = append(out, st.Name)
		}
		return out
	}
	// Object at one volition for one candidate: one cell per side.
	one := sel(DeclaredComparison{Variable: "object", Levels: []string{"build", "do"}, Where: map[string]string{"candidate": c[0], "volition": "none"}})
	if !slices.Equal(names(one), []string{"build vs do"}) || one.Strata[0].Candidate.Task != d1 || one.Strata[0].Reference.Task != d3 || one.CandidateOf() != c[0] {
		t.Fatalf("object at one volition %+v", one)
	}
	// Object stratified by volition within one candidate.
	by := sel(DeclaredComparison{Variable: "object", Levels: []string{"build", "do"}, Where: map[string]string{"candidate": c[0], "workspace": "empty"}, By: []string{"volition"}})
	if !slices.Equal(names(by), []string{"volition=none", "volition=want"}) || by.Strata[1].Candidate.TaskName != "build-want" || by.Strata[1].Reference.TaskName != "do-want" {
		t.Fatalf("object by volition %+v", by)
	}
	// The seeded workspace against the empty one, by task name.
	ws := sel(DeclaredComparison{Variable: "workspace", Levels: []string{"seeded", "empty"}, Where: map[string]string{"candidate": c[1], "object": "do", "volition": "want"}})
	if !slices.Equal(names(ws), []string{"do-want-seeded vs do-want"}) {
		t.Fatalf("workspace %v", names(ws))
	}
	// A pair of candidates by task, a task named by its name.
	pair := sel(DeclaredComparison{Variable: "candidate", Levels: []string{c[0], c[1]}, By: []string{"task"}})
	if !slices.Equal(names(pair), []string{"build", "build-want", "do", "do-want", "do-want-seeded", "free"}) || pair.CandidateOf() != c[0] {
		t.Fatalf("a pair by task %v", names(pair))
	}
	// A high reasoning level against a low one within one model.
	reasoning := sel(DeclaredComparison{Variable: "reasoning", Levels: []string{"high", "low"}, Where: map[string]string{"model": "m-2", "workspace": "empty"}, By: []string{"task"}})
	if !slices.Equal(names(reasoning), []string{"build", "build-want", "do", "do-want"}) || reasoning.CandidateOf() != c[2] || reasoning.Strata[0].Reference.Candidate != c[3] {
		t.Fatalf("reasoning %+v", reasoning)
	}
	// Strata named by several variables, a task by its name.
	two := sel(DeclaredComparison{Variable: "volition", Levels: []string{"want", "none"}, Where: map[string]string{"workspace": "empty"}, By: []string{"candidate", "object"}})
	if len(two.Strata) != 8 || two.Strata[0].Name != "candidate="+c[0]+",object=build" || two.CandidateOf() != "" {
		t.Fatalf("by candidate and object %v", names(two))
	}
	if n := StratumName(&design[0], []string{"task", "object"}); n != "task=build,object=build" {
		t.Fatalf("a stratum named by its task and a factor: %s", n)
	}
	// A level and a where named by a task's name or its digest read alike.
	byName := sel(DeclaredComparison{Variable: "task", Levels: []string{"build", d3}, Where: map[string]string{"candidate": c[0]}})
	if !slices.Equal(names(byName), []string{"build vs do"}) {
		t.Fatalf("tasks by name and digest %v", names(byName))
	}
}

// A declaration that does not read as one comparison is refused, naming
// the member at fault.
func TestSelectRefusals(t *testing.T) {
	r, c, design := study()
	r.Suite.Tasks = r.Suite.Tasks[:5]
	for _, x := range []struct {
		name  string
		d     DeclaredComparison
		code  string
		field string
	}{
		{"one level", DeclaredComparison{Variable: "object", Levels: []string{"build"}}, apierr.CodeInvalidField, "c.levels"},
		{"one level twice", DeclaredComparison{Variable: "object", Levels: []string{"do", "do"}}, apierr.CodeInvalidField, "c.levels"},
		{"an empty level", DeclaredComparison{Variable: "object", Levels: []string{"build", ""}}, apierr.CodeInvalidField, "c.levels"},
		{"an empty where", DeclaredComparison{Variable: "candidate", Levels: []string{c[0], c[1]}, Where: map[string]string{"object": ""}}, apierr.CodeInvalidField, "c.where.object"},
		{"a variable no cell carries", DeclaredComparison{Variable: "harness", Levels: []string{"a", "b"}}, apierr.CodeInvalidField, "c.variable"},
		{"where on the variable", DeclaredComparison{Variable: "object", Levels: []string{"build", "do"}, Where: map[string]string{"object": "do"}}, apierr.CodeInvalidField, "c.where.object"},
		{"where on a variable no cell carries", DeclaredComparison{Variable: "object", Levels: []string{"build", "do"}, Where: map[string]string{"tone": "dry"}}, apierr.CodeInvalidField, "c.where.tone"},
		{"by on the variable", DeclaredComparison{Variable: "object", Levels: []string{"build", "do"}, By: []string{"object"}}, apierr.CodeInvalidField, "c.by[0]"},
		{"by twice", DeclaredComparison{Variable: "object", Levels: []string{"build", "do"}, By: []string{"candidate", "candidate"}}, apierr.CodeInvalidField, "c.by[1]"},
		{"by on a where", DeclaredComparison{Variable: "object", Levels: []string{"build", "do"}, Where: map[string]string{"volition": "none"}, By: []string{"volition"}}, apierr.CodeInvalidField, "c.by[0]"},
		{"by on a variable no cell carries", DeclaredComparison{Variable: "object", Levels: []string{"build", "do"}, By: []string{"tone"}}, apierr.CodeInvalidField, "c.by[0]"},
		{"where selects nothing", DeclaredComparison{Variable: "object", Levels: []string{"build", "do"}, Where: map[string]string{"volition": "maybe"}}, apierr.CodeNotInExperiment, "c.where"},
		{"a level selects nothing", DeclaredComparison{Variable: "object", Levels: []string{"build", "make"}}, apierr.CodeNotInExperiment, "c.levels[1]"},
		{"two cells of one side in a stratum", DeclaredComparison{Variable: "object", Levels: []string{"build", "do"}, Where: map[string]string{"candidate": c[0]}, By: []string{"volition"}}, apierr.CodeInvalidField, "c.by"},
		{"two cells of one side without by", DeclaredComparison{Variable: "candidate", Levels: []string{c[0], c[1]}}, apierr.CodeInvalidField, "c.by"},
		{"no stratum holds both levels", DeclaredComparison{Variable: "object", Levels: []string{"build", "do"}, Where: map[string]string{"candidate": c[0]}, By: []string{"task"}}, apierr.CodeInvalidField, "c.by"},
		{"a task the suite does not pin", DeclaredComparison{Variable: "task", Levels: []string{"build", "free"}}, apierr.CodeNotInExperiment, "c.levels[1]"},
		{"a candidate that is no reference", DeclaredComparison{Variable: "candidate", Levels: []string{c[0], "Nope"}}, apierr.CodeUnpinnedReference, "c.levels[1]"},
		{"a where task the suite does not pin", DeclaredComparison{Variable: "object", Levels: []string{"build", "do"}, Where: map[string]string{"task": "free"}}, apierr.CodeNotInExperiment, "c.where.task"},
	} {
		err := r.NormalizeDeclaration(&x.d, "c")
		if err == nil {
			_, err = Select(x.d, design[:20], "c")
		}
		expect(t, x.name, err, x.code, x.field)
	}
	// A cell of a side that does not carry a variable of by.
	_, err := Select(DeclaredComparison{Variable: "candidate", Levels: []string{c[0], c[1]}, By: []string{"task", "volition"}}, design, "c")
	expect(t, "a cell without a variable of by", err, apierr.CodeInvalidField, "c.by")
	// Two strata whose names collide: levels that hold the separator.
	collide := []DesignCell{
		{Task: d1, TaskName: "t", Candidate: "a", Vars: map[string]string{"candidate": "a", "task": d1, "model": "x,reasoning=y", "reasoning": "z", "object": "o1"}},
		{Task: d1, TaskName: "t", Candidate: "b", Vars: map[string]string{"candidate": "b", "task": d1, "model": "x", "reasoning": "y,reasoning=z", "object": "o1"}},
		{Task: d2, TaskName: "u", Candidate: "a", Vars: map[string]string{"candidate": "a", "task": d2, "model": "x,reasoning=y", "reasoning": "z", "object": "o2"}},
	}
	_, err = Select(DeclaredComparison{Variable: "object", Levels: []string{"o1", "o2"}, By: []string{"model", "reasoning"}}, collide, "c")
	expect(t, "two strata of one name", err, apierr.CodeInvalidField, "c.by")
}

// A test of independence reads one side per level of its variable, the
// candidates in the experiment's order and other levels ascending, and
// one stratum per combination of by's levels, the candidate by task when
// it names neither.
func TestSelectIndependence(t *testing.T) {
	r, c, design := study()
	in := Independence{Metric: d1}
	if err := r.NormalizeIndependence(&in, "independence"); err != nil || in.Variable != "candidate" || !slices.Equal(in.By, []string{"task"}) {
		t.Fatalf("defaults %+v %v", in, err)
	}
	sel, err := SelectIndependence(in, design, c, "independence")
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(sel.Levels, c) || len(sel.Strata) != 6 || sel.Strata[0].Name != "build" || sel.Strata[0].Cells[3].Candidate != c[3] {
		t.Fatalf("the defaults %+v", sel)
	}
	obj, err := SelectIndependence(Independence{Variable: "object", By: []string{"candidate", "volition"}, Where: map[string]string{"workspace": "empty"}}, design, c, "independence")
	if err != nil || !slices.Equal(obj.Levels, []string{"build", "do"}) || len(obj.Strata) != 8 {
		t.Fatalf("object %+v %v", obj, err)
	}
	all, err := SelectIndependence(Independence{Variable: "task", By: []string{}, Where: map[string]string{"candidate": c[0]}}, design, c, "independence")
	if err != nil || len(all.Strata) != 1 || all.Strata[0].Name != "all" || len(all.Levels) != 6 {
		t.Fatalf("without strata %+v %v", all, err)
	}
	for _, x := range []struct {
		name  string
		in    Independence
		field string
	}{
		{"one level", Independence{Variable: "candidate", By: []string{"task"}, Where: map[string]string{"candidate": c[0]}}, "independence.where.candidate"},
		{"one candidate's level", Independence{Variable: "candidate", By: []string{"task"}, Where: map[string]string{"model": "m-2", "reasoning": "high"}}, "independence.variable"},
		{"two cells of one level in a stratum", Independence{Variable: "candidate", By: []string{"volition"}}, "independence.by"},
		{"a variable no cell carries", Independence{Variable: "tone", By: []string{"task"}}, "independence.variable"},
	} {
		_, err := SelectIndependence(x.in, design, c, "independence")
		expect(t, x.name, err, apierr.CodeInvalidField, x.field)
	}
	free := Independence{Variable: "candidate", By: []string{"object"}}
	_, err = SelectIndependence(free, design, c, "independence")
	expect(t, "the factor-less task in a stratum", err, apierr.CodeInvalidField, "independence.by")
	if err := r.NormalizeIndependence(&Independence{Where: map[string]string{"task": "nope"}}, "independence"); err == nil {
		t.Error("a where task the suite does not pin")
	}
	collide := []DesignCell{
		{Task: d1, TaskName: "t", Candidate: "a", Vars: map[string]string{"candidate": "a", "task": d1, "model": "x,reasoning=y", "reasoning": "z"}},
		{Task: d1, TaskName: "t", Candidate: "b", Vars: map[string]string{"candidate": "b", "task": d1, "model": "x", "reasoning": "y,reasoning=z"}},
	}
	_, err = SelectIndependence(Independence{Variable: "task", By: []string{"model", "reasoning"}}, append(collide, DesignCell{Task: d2, TaskName: "u", Candidate: "a", Vars: map[string]string{"candidate": "a", "task": d2, "model": "x,reasoning=y", "reasoning": "z"}}), []string{"a", "b"}, "independence")
	expect(t, "two strata of one name", err, apierr.CodeInvalidField, "independence.by")
}

// A cell variable's logical variable: model and reasoning their own, the
// task and every factor task, the candidate none.
func TestLogicalVariableOf(t *testing.T) {
	for v, want := range map[string]string{"model": "model", "reasoning": "reasoning", "task": "task", "object": "task", "candidate": ""} {
		if got := LogicalVariableOf(v); got != want {
			t.Errorf("%s: %q, want %q", v, got, want)
		}
	}
	if !(DeclaredComparison{Levels: []string{"a", "b"}}).General() || (DeclaredComparison{Candidate: "c"}).General() {
		t.Error("General")
	}
}
