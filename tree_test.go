// SPDX-FileCopyrightText: 2026 Latere AI
// SPDX-License-Identifier: Apache-2.0

// The tests of this file hold rules about the whole module that no one
// package can. Each reads the source, so a rule broken by a new file fails
// here.
package examen_test

import (
	"go/ast"
	"go/parser"
	"go/token"
	"io/fs"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

// source is one parsed Go file of the module.
type source struct {
	rel  string // the slash path from the module root
	test bool
	file *ast.File
}

// sources parses every Go file of the module.
func sources(t *testing.T) []source {
	t.Helper()
	var out []source
	fset := token.NewFileSet()
	err := filepath.WalkDir(".", func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			if name := d.Name(); path != "." && (strings.HasPrefix(name, ".") || name == "testdata") {
				return filepath.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(path, ".go") {
			return nil
		}
		f, err := parser.ParseFile(fset, path, nil, parser.SkipObjectResolution)
		if err != nil {
			return err
		}
		out = append(out, source{rel: filepath.ToSlash(path), test: strings.HasSuffix(path, "_test.go"), file: f})
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(out) < 20 {
		t.Fatalf("read %d files; the walk is wrong", len(out))
	}
	return out
}

// imported is the name a file imports a path under, or "".
func imported(f *ast.File, path string) string {
	for _, imp := range f.Imports {
		p, err := strconv.Unquote(imp.Path.Value)
		if err != nil || p != path {
			continue
		}
		if imp.Name != nil {
			return imp.Name.Name
		}
		return path[strings.LastIndex(path, "/")+1:]
	}
	return ""
}

// Only internal/config reads the environment: no other file of the module
// outside the tests calls os.Getenv, os.LookupEnv, os.Environ or
// syscall.Getenv, so every variable an operator sets is read once, in one
// place, and listed in the table of specs/011-distribution.md.
func TestOnlyConfigReadsTheEnvironment(t *testing.T) {
	reads := map[string][]string{"os": {"Getenv", "LookupEnv", "Environ"}, "syscall": {"Getenv", "Environ"}}
	for _, s := range sources(t) {
		if s.test || strings.HasPrefix(s.rel, "internal/config/") {
			continue
		}
		for pkg, names := range reads {
			local := imported(s.file, pkg)
			if local == "" {
				continue
			}
			ast.Inspect(s.file, func(n ast.Node) bool {
				call, ok := n.(*ast.CallExpr)
				if !ok {
					return true
				}
				sel, ok := call.Fun.(*ast.SelectorExpr)
				if !ok {
					return true
				}
				if id, ok := sel.X.(*ast.Ident); ok && id.Name == local {
					for _, name := range names {
						if sel.Sel.Name == name {
							t.Errorf("%s reads the environment with %s.%s", s.rel, pkg, name)
						}
					}
				}
				return true
			})
		}
	}
}
