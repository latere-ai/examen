// SPDX-FileCopyrightText: 2026 Latere AI
// SPDX-License-Identifier: Apache-2.0

package api_test

import (
	"regexp"
	"slices"
	"strings"
	"testing"

	"go.yaml.in/yaml/v3"

	"latere.ai/x/examen/api"
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
			Enum []string `yaml:"enum"`
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
