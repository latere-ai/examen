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
	"os"
	"path"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"testing"
)

// source is one parsed Go file of the module.
type source struct {
	rel  string // the slash path from the module root
	test bool
	file *ast.File
	fset *token.FileSet
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
		out = append(out, source{rel: filepath.ToSlash(path), test: strings.HasSuffix(path, "_test.go"), file: f, fset: fset})
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

// namedTest matches a reference to a test as SECURITY.md and the security
// spec write it: a backticked package, a dot and the test's name. The
// package is the directory's base name, or a slash path ending in it; the
// repository's root is `examen`.
var namedTest = regexp.MustCompile("`([a-z][a-z0-9_]*(?:/[a-z][a-z0-9_]*)*)\\.((?:Test|Fuzz)[A-Za-z0-9_]*)`")

// TestNamedTestsExist holds every test that SECURITY.md and the security
// spec name to one that a _test.go file of the named package declares, so
// a rename or a removal fails here rather than leaving a commitment that
// no test holds.
func TestNamedTestsExist(t *testing.T) {
	declared := map[string]map[string]bool{} // directory -> declared test functions
	dirs := map[string]bool{}
	for _, s := range sources(t) {
		dir := path.Dir(s.rel)
		dirs[dir] = true
		if !s.test {
			continue
		}
		if declared[dir] == nil {
			declared[dir] = map[string]bool{}
		}
		for _, d := range s.file.Decls {
			if fn, ok := d.(*ast.FuncDecl); ok && fn.Recv == nil {
				declared[dir][fn.Name.Name] = true
			}
		}
	}
	// dirsOf is the directories a package name stands for.
	dirsOf := func(pkg string) []string {
		if pkg == "examen" {
			return []string{"."}
		}
		var out []string
		for dir := range dirs {
			if dir == pkg || strings.HasSuffix(dir, "/"+pkg) {
				out = append(out, dir)
			}
		}
		sort.Strings(out)
		return out
	}
	for _, file := range []string{"SECURITY.md", "specs/012-security-and-threat-model.md"} {
		text, err := os.ReadFile(file)
		if err != nil {
			t.Fatal(err)
		}
		found := 0
		seen := map[string]bool{}
		for _, m := range namedTest.FindAllStringSubmatch(string(text), -1) {
			ref := m[1] + "." + m[2]
			if seen[ref] {
				continue
			}
			seen[ref] = true
			found++
			where := dirsOf(m[1])
			if len(where) == 0 {
				t.Errorf("%s names %s, but no directory of the module is %q", file, ref, m[1])
				continue
			}
			ok := false
			for _, dir := range where {
				ok = ok || declared[dir][m[2]]
			}
			if !ok {
				t.Errorf("%s names %s, but no _test.go file in %s declares func %s(", file, ref, strings.Join(where, " or "), m[2])
			}
		}
		if found == 0 {
			t.Errorf("%s names no test; the pattern or the file is wrong", file)
		}
	}
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

// Every readiness check of a Postgres container asks over TCP. The image's
// setup first runs a server that listens on its socket alone, and a check
// over the socket counts that server as ready, so a migration started then
// fails when the setup restarts the server (make run on a new database).
func TestPostgresReadinessAsksOverTCP(t *testing.T) {
	files := []string{"Makefile"}
	workflows, err := filepath.Glob(filepath.Join(".github", "workflows", "*.yml"))
	if err != nil {
		t.Fatal(err)
	}
	files = append(files, workflows...)
	checks := 0
	for _, name := range files {
		b, err := os.ReadFile(name)
		if err != nil {
			t.Fatal(err)
		}
		for i, line := range strings.Split(string(b), "\n") {
			if !strings.Contains(line, "pg_isready") {
				continue
			}
			checks++
			if !strings.Contains(line, "-h 127.0.0.1") {
				t.Errorf("%s:%d checks readiness over the socket: %s", name, i+1, strings.TrimSpace(line))
			}
		}
	}
	if checks < 1 {
		t.Fatalf("found %d readiness checks; the Makefile holds one", checks)
	}
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

// An error a log line carries goes through apierr.LogText, which cuts URL
// queries and user info, so a token in a query a library quoted never
// reaches the log. A log call that passes an error's own text fails here.
func TestLoggedErrorsAreCut(t *testing.T) {
	logs := map[string]bool{
		"Debug": true, "Info": true, "Warn": true, "Error": true,
		"DebugContext": true, "InfoContext": true, "WarnContext": true, "ErrorContext": true, "Log": true,
	}
	for _, s := range sources(t) {
		if s.test {
			continue
		}
		ast.Inspect(s.file, func(n ast.Node) bool {
			call, ok := n.(*ast.CallExpr)
			if !ok {
				return true
			}
			sel, ok := call.Fun.(*ast.SelectorExpr)
			if !ok || !logs[sel.Sel.Name] {
				return true
			}
			for _, arg := range call.Args {
				inner, ok := arg.(*ast.CallExpr)
				if !ok || len(inner.Args) != 0 {
					continue
				}
				if m, ok := inner.Fun.(*ast.SelectorExpr); ok && m.Sel.Name == "Error" {
					t.Errorf("%s:%d logs an error's text uncut; pass apierr.LogText(err)", s.rel, s.fset.Position(arg.Pos()).Line)
				}
			}
			return true
		})
	}
}
