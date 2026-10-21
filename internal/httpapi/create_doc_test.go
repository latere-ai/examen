// SPDX-FileCopyrightText: 2026 Latere AI
// SPDX-License-Identifier: Apache-2.0

package httpapi

import (
	"encoding/json"
	"strings"
	"testing"

	"go.yaml.in/yaml/v3"

	"latere.ai/x/examen/api"
	"latere.ai/x/examen/internal/objects"
)

// The create route's description says which candidate kinds run as the
// server decides it: each kind a create with every core configured admits
// is named among those that run, and each it refuses is named as refused.
func TestTheCreateDescriptionNamesTheKindsThatRun(t *testing.T) {
	var doc struct {
		Paths map[string]map[string]any `yaml:"paths"`
	}
	if err := yaml.Unmarshal(api.OpenAPI, &doc); err != nil {
		t.Fatal(err)
	}
	post, _ := doc.Paths["/experiments"]["post"].(map[string]any)
	desc, _ := post["description"].(string)
	runs, _, ok := strings.Cut(desc, " run;")
	if !ok {
		t.Fatalf("the description names no kinds that run: %q", desc)
	}
	all := cores{topos: true, origo: true, checks: true, judges: true}
	// A create is held to name a budget before these checks run.
	var in createBody
	if err := json.Unmarshal([]byte(`{"budget":{"name":"b"}}`), &in); err != nil {
		t.Fatal(err)
	}
	for _, c := range []objects.Candidate{
		{Kind: objects.CandidateTopos, Topos: &objects.Topos{Agent: "sha256:a", Model: "m"}},
		{Kind: objects.CandidateProgram, Program: &objects.Program{Image: "img@sha256:b"}},
		{Kind: objects.CandidateExternal, External: &objects.External{Harness: "h", Version: "1"}},
	} {
		err := unbuilt(in, plan{candBodies: []objects.Candidate{c}}, true, all)
		kind := "`" + c.Kind + "`"
		switch {
		case err == nil && !strings.Contains(runs, kind):
			t.Errorf("a %s candidate runs, and the description does not say so: %q", c.Kind, runs)
		case err != nil && !strings.Contains(desc, kind+" is `422 executor_unavailable`"):
			t.Errorf("a %s candidate is refused (%v), and the description does not say so: %q", c.Kind, err, desc)
		case err != nil && strings.Contains(runs, kind):
			t.Errorf("a %s candidate is refused, and the description names it among the kinds that run", c.Kind)
		}
	}
}
