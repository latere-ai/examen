// SPDX-FileCopyrightText: 2026 Latere AI
// SPDX-License-Identifier: Apache-2.0

package authorizer_test

import (
	"bufio"
	"encoding/json/jsontext"
	"encoding/json/v2"
	"errors"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"testing"

	"latere.ai/x/pkg/authz"

	"latere.ai/x/examen/authorizer"
)

// specAction is one row of the vocabulary table of
// specs/004-identity-and-authorization.md.
type specAction struct {
	kind   string
	id     bool
	fields []string
}

func specVocabulary(t *testing.T) ([]string, map[string]specAction) {
	t.Helper()
	f, err := os.Open(filepath.Join("..", "specs", "004-identity-and-authorization.md"))
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if err := f.Close(); err != nil {
			t.Error(err)
		}
	}()
	row := regexp.MustCompile("^\\| `([A-Za-z]+)` \\| `([a-z.]+)` \\| .* \\| ([^|]+) \\|$")
	field := regexp.MustCompile("`([a-z_]+)`")
	var order []string
	rows := map[string]specAction{}
	scan := bufio.NewScanner(f)
	for scan.Scan() {
		m := row.FindStringSubmatch(scan.Text())
		if m == nil {
			continue
		}
		cell := strings.TrimSpace(m[3])
		a := specAction{kind: m[1], id: strings.HasPrefix(cell, "id")}
		for _, fm := range field.FindAllStringSubmatch(cell, -1) {
			a.fields = append(a.fields, fm[1])
		}
		order = append(order, m[2])
		rows[m[2]] = a
	}
	if err := scan.Err(); err != nil {
		t.Fatal(err)
	}
	if len(order) < 60 {
		t.Fatalf("read %d actions of the table; the reader is wrong", len(order))
	}
	return order, rows
}

// Every action of the package is a row of the spec's table, with its kind,
// whether it sends an id, and fields the row lists, in the table's order.
func TestTheVocabularyIsTheSpecs(t *testing.T) {
	order, rows := specVocabulary(t)
	var inSpecOrder []string
	for _, name := range order {
		if authorizer.Known(name) {
			inSpecOrder = append(inSpecOrder, name)
		}
	}
	if !slices.Equal(inSpecOrder, authorizer.Actions()) {
		t.Fatalf("the actions are not in the spec's order, or one is not in the spec:\n spec %v\n here %v", inSpecOrder, authorizer.Actions())
	}
	for _, name := range authorizer.Actions() {
		row := rows[name]
		if authorizer.Kind(name) != row.kind {
			t.Errorf("%s acts on %s here and on %s in the spec", name, authorizer.Kind(name), row.kind)
		}
		if authorizer.SendsID(name) != row.id {
			t.Errorf("%s sends an id: %v here, %v in the spec", name, authorizer.SendsID(name), row.id)
		}
		for _, f := range authorizer.Fields(name) {
			if !slices.Contains(row.fields, f) {
				t.Errorf("%s sends %s, which the spec's row does not list", name, f)
			}
			if f == "kind" || f == "id" {
				t.Errorf("%s names a field %s, which the envelope keeps for the resource", name, f)
			}
		}
	}
}

// The vocabulary is the contract's, and the start-up probe's action is in
// it.
func TestVocabulary(t *testing.T) {
	v := authorizer.Vocabulary()
	if v.Core != authorizer.Core || len(v.Actions) != len(authorizer.Actions()) {
		t.Fatalf("%s %d", v.Core, len(v.Actions))
	}
	if authorizer.Kind(authorizer.ActionExperimentRead) != authorizer.KindExperiment {
		t.Fatal("the probe's action does not act on experiments")
	}
	for _, k := range v.Kinds() {
		if v.Label(k) == k {
			t.Errorf("the kind %s has no label", k)
		}
	}
	if authorizer.Known("experiment.delete") || authorizer.Kind("nope") != "" || authorizer.Fields("nope") != nil || authorizer.SendsID("nope") {
		t.Fatal("an action outside the table")
	}
	f := authorizer.Fields(authorizer.ActionMetricRead)
	f[0] = "changed"
	if authorizer.Fields(authorizer.ActionMetricRead)[0] == "changed" {
		t.Fatal("Fields hands out the table")
	}
	if !slices.Equal(authorizer.Executors(), []string{"topos", "cella", "external"}) {
		t.Fatal(authorizer.Executors())
	}
}

// Limits read every member, and anything Examen cannot hold is no
// decision.
func TestDecodeLimits(t *testing.T) {
	full := `{"owner":"https://issuer.example.com|org","max_parallelism":4,"max_trials":100,"max_active_experiments":2,
		"executors":["external"],"judge_models":["claude-*"],"credential_audiences":["lectio"],"budget":"b-1","attribution":"Example Org"}`
	l, err := authorizer.DecodeLimits(authz.Decision{Allow: true, Limits: jsontext.Value(full)})
	if err != nil {
		t.Fatal(err)
	}
	want := authorizer.Limits{
		Owner: "https://issuer.example.com|org", MaxParallelism: 4, MaxTrials: 100, MaxActiveExperiments: 2,
		Executors: []string{"external"}, JudgeModels: []string{"claude-*"}, CredentialAudiences: []string{"lectio"},
		Budget: "b-1", Attribution: "Example Org",
	}
	if b1, b2 := mustJSON(t, l), mustJSON(t, want); b1 != b2 {
		t.Fatalf("got %s want %s", b1, b2)
	}
	for _, none := range []string{"", "null", "  ", `{}`, `{"attribution":null}`} {
		if l, err := authorizer.DecodeLimits(authz.Decision{Limits: jsontext.Value(none)}); err != nil || mustJSON(t, l) != "{}" {
			t.Errorf("%q: %+v %v", none, l, err)
		}
	}
	for _, bad := range []string{
		`[]`, `{"owner":1}`, `{"Owner":"x"}`, `{"vouched":true}`, `{} {}`, `{`,
		`{"max_parallelism":-1}`, `{"max_trials":-1}`, `{"max_active_experiments":-1}`,
		`{"judge_models":[""]}`, `{"credential_audiences":["a",""]}`,
		`{"attribution":""}`, `{"attribution":"` + strings.Repeat("x", authorizer.AttributionMaxBytes+1) + `"}`,
		`{"executors":["docker"]}`,
	} {
		if _, err := authorizer.DecodeLimits(authz.Decision{Allow: true, Limits: jsontext.Value(bad)}); !errors.Is(err, authorizer.ErrUnreadableLimits) {
			t.Errorf("%s: %v", bad, err)
		}
	}
}

func mustJSON(t *testing.T, v any) string {
	t.Helper()
	b, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}
