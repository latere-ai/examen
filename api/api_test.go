// SPDX-FileCopyrightText: 2026 Latere AI
// SPDX-License-Identifier: Apache-2.0

package api_test

import (
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
	"latere.ai/x/examen/internal/httpapi"
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
// them.
func members(v any) []string {
	var names []string
	t := reflect.TypeOf(v)
	for f := range t.Fields() {
		if !f.IsExported() {
			continue
		}
		name, _, _ := strings.Cut(f.Tag.Get("json"), ",")
		if name == "-" {
			continue
		}
		if name == "" {
			name = f.Name
		}
		names = append(names, name)
	}
	slices.Sort(names)
	return names
}

// A comparison's result, and each object inside it, is documented with
// every member the analysis writes and no other.
func TestDocumentDescribesTheResults(t *testing.T) {
	schemas := read(t).Components.Schemas
	for name, v := range map[string]any{
		"Result":     analysis.Result{},
		"Summary":    analysis.Summary{},
		"LabelShare": analysis.LabelShare{},
		"LeftOut":    analysis.LeftOut{},
		"Confound":   analysis.Confound{},
	} {
		got := slices.Sorted(maps.Keys(schemas[name].Properties))
		if want := members(v); !slices.Equal(got, want) {
			t.Errorf("%s documents\n  %v\nthe analysis writes\n  %v", name, got, want)
		}
	}
}
