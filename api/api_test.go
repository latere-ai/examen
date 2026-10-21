// SPDX-FileCopyrightText: 2026 Latere AI
// SPDX-License-Identifier: Apache-2.0

package api_test

import (
	"encoding/json"
	"maps"
	"reflect"
	"regexp"
	"slices"
	"strings"
	"testing"

	"go.yaml.in/yaml/v3"

	"latere.ai/x/examen/api"
	"latere.ai/x/examen/internal/analysis"
	"latere.ai/x/examen/internal/apierr"
	"latere.ai/x/examen/internal/compare"
	"latere.ai/x/examen/internal/httpapi"
	"latere.ai/x/examen/internal/objects"
)

// document is the contract, read.
type document struct {
	OpenAPI string                    `yaml:"openapi"`
	Paths   map[string]map[string]any `yaml:"paths"`
	Servers []struct {
		URL string `yaml:"url"`
	} `yaml:"servers"`
	Components struct {
		Schemas map[string]struct {
			Enum       []string       `yaml:"enum"`
			Properties map[string]any `yaml:"properties"`
		} `yaml:"schemas"`
	} `yaml:"components"`
}

func read(t *testing.T) document {
	t.Helper()
	var d document
	if err := yaml.Unmarshal(api.OpenAPI, &d); err != nil {
		t.Fatal(err)
	}
	if d.OpenAPI != "3.1.0" || len(d.Servers) != 1 || d.Servers[0].URL != "/v1" {
		t.Fatalf("the document is not OpenAPI 3.1 under the default base path: %q %v", d.OpenAPI, d.Servers)
	}
	return d
}

var param = regexp.MustCompile(`\{[^}]+\}`)

// The document describes every route the server serves and no other.
// OpenAPI holds two templates of one shape as one path, so a route is
// compared with its parameters' names dropped.
func TestDocumentMatchesTheRoutes(t *testing.T) {
	documented := map[string]bool{}
	for path, item := range read(t).Paths {
		for method := range item {
			if method == "parameters" {
				continue
			}
			documented[strings.ToUpper(method)+" "+param.ReplaceAllString(path, "{}")] = true
		}
	}
	served := map[string]bool{}
	for _, rt := range httpapi.Routes() {
		served[rt.Method+" "+param.ReplaceAllString(rt.Path, "{}")] = true
	}
	if len(served) == 0 {
		t.Fatal("the server serves no route")
	}
	for r := range served {
		if !documented[r] {
			t.Errorf("%s is served and not documented", r)
		}
	}
	for r := range documented {
		if !served[r] {
			t.Errorf("%s is documented and not served", r)
		}
	}
}

// The error codes the document lists are the error table's, in its order.
func TestDocumentListsEveryErrorCode(t *testing.T) {
	got := read(t).Components.Schemas["ErrorCode"].Enum
	if !slices.Equal(got, apierr.Codes()) {
		t.Fatalf("the document lists\n  %v\nthe table\n  %v", got, apierr.Codes())
	}
}

// members is the JSON member names of the struct v, as encoding/json writes
// them: an embedded struct without a name of its own contributes its
// members, and a field tagged "-" none.
func members(v any) []string {
	var names []string
	var walk func(t reflect.Type)
	walk = func(t reflect.Type) {
		for f := range t.Fields() {
			name, _, _ := strings.Cut(f.Tag.Get("json"), ",")
			switch {
			case f.Anonymous && name == "" && f.Type.Kind() == reflect.Struct:
				walk(f.Type)
				continue
			case !f.IsExported() || name == "-":
				continue
			case name == "":
				name = f.Name
			}
			names = append(names, name)
		}
	}
	walk(reflect.TypeOf(v))
	slices.Sort(names)
	return names
}

// A comparison, its results and each object inside them are documented
// with every member the server answers and no other: the comparison and
// its result rows as the store keeps them, and the objects inside, which
// the analysis writes.
func TestDocumentDescribesTheComparisons(t *testing.T) {
	recorded, err := compare.Record(analysis.Comparison{})
	if err != nil {
		t.Fatal(err)
	}
	var parameters map[string]any
	if err := json.Unmarshal(recorded.Parameters, &parameters); err != nil {
		t.Fatal(err)
	}
	schemas := read(t).Components.Schemas
	for name, want := range map[string][]string{
		"Comparison":    members(objects.Comparison{}),
		"Result":        members(objects.ResultRow{}),
		"Summary":       members(analysis.Summary{}),
		"LabelShare":    members(analysis.LabelShare{}),
		"LeftOut":       members(analysis.LeftOut{}),
		"Confound":      members(analysis.Confound{}),
		"Correction":    members(analysis.Correction{}),
		"Windows":       members(analysis.Windows{}),
		"Window":        members(analysis.Window{}),
		"UnpairedTasks": members(analysis.UnpairedTasks{}),
		"Resamples":     members(analysis.Resolved{}),
		"Parameters":    slices.Sorted(maps.Keys(parameters)),
	} {
		got := slices.Sorted(maps.Keys(schemas[name].Properties))
		if !slices.Equal(got, want) {
			t.Errorf("%s documents\n  %v\nthe server answers\n  %v", name, got, want)
		}
	}
}
