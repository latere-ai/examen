// SPDX-FileCopyrightText: 2026 Latere AI
// SPDX-License-Identifier: Apache-2.0

package analysis

import (
	"go/ast"
	"go/importer"
	"go/parser"
	"go/token"
	"go/types"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// allowedMath are the functions and constants of package math the analysis
// may call: each is exact or correctly rounded by IEEE 754 whatever the
// instruction, so it cannot change a figure between architectures. Exp,
// Log, Pow, Lgamma, Erf, Erfinv and the rest are internal/analysis/mathx's.
var allowedMath = map[string]bool{
	"Sqrt": true, "Abs": true, "Floor": true, "Ceil": true, "Trunc": true, "Round": true,
	"Max": true, "Min": true, "Copysign": true, "Signbit": true,
	"Ldexp": true, "Frexp": true, "Inf": true, "NaN": true, "IsInf": true,
	"IsNaN": true, "Float64bits": true, "Float64frombits": true,
	"Nextafter": true, "Sqrt2": true, "Ln2": true, "Pi": true, "E": true,
	"MaxFloat64": true, "MaxInt": true,
}

// sources parses the package's own files of dir, tests left out.
func sources(t *testing.T, fset *token.FileSet, dir string) []*ast.File {
	t.Helper()
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	var files []*ast.File
	for _, e := range entries {
		name := e.Name()
		if !strings.HasSuffix(name, ".go") || strings.HasSuffix(name, "_test.go") {
			continue
		}
		f, err := parser.ParseFile(fset, filepath.Join(dir, name), nil, 0)
		if err != nil {
			t.Fatal(err)
		}
		files = append(files, f)
	}
	return files
}

// The analysis calls no exp, log, lgamma or erfinv but mathx's: every
// selector of package math it uses is one IEEE 754 fixes exactly.
func TestUsesVendoredMath(t *testing.T) {
	fset := token.NewFileSet()
	for _, dir := range []string{".", "mathx"} {
		for _, f := range sources(t, fset, dir) {
			name := "math"
			for _, imp := range f.Imports {
				if imp.Path.Value == `"math"` && imp.Name != nil {
					name = imp.Name.Name
				}
			}
			ast.Inspect(f, func(n ast.Node) bool {
				sel, ok := n.(*ast.SelectorExpr)
				if !ok {
					return true
				}
				if id, ok := sel.X.(*ast.Ident); ok && id.Name == name && !allowedMath[sel.Sel.Name] {
					t.Errorf("%s: math.%s; the analysis computes it with internal/analysis/mathx", fset.Position(sel.Pos()), sel.Sel.Name)
				}
				return true
			})
		}
	}
}

// moduleImporter type-checks this module's packages from source and reads
// every other package's export data.
type moduleImporter struct {
	t    *testing.T
	fset *token.FileSet
	std  types.Importer
	done map[string]*types.Package
}

const modulePath = "latere.ai/x/examen/"

func (m *moduleImporter) Import(path string) (*types.Package, error) {
	if p, ok := m.done[path]; ok {
		return p, nil
	}
	rel, ok := strings.CutPrefix(path, modulePath)
	if !ok {
		return m.std.Import(path)
	}
	dir := filepath.Join("..", "..", rel)
	conf := types.Config{Importer: m}
	p, err := conf.Check(path, m.fset, sources(m.t, m.fset, dir), nil)
	if err != nil {
		return nil, err
	}
	m.done[path] = p
	return p, nil
}

// isFloat reports whether an expression's type is a floating-point type.
func isFloat(info *types.Info, e ast.Expr) bool {
	tv, ok := info.Types[e]
	if !ok || tv.Value != nil {
		return false
	}
	b, ok := tv.Type.Underlying().(*types.Basic)
	return ok && b.Info()&types.IsFloat != 0
}

// No product of the analysis can be fused with an addition: every
// floating-point multiplication is the operand of an explicit float64
// conversion, which rounds it, unless it feeds another multiplication or a
// division, which no fused instruction combines. Go may fuse x*y + z into
// one instruction on arm64, across statements too, so a product held in a
// variable is no safer than one written inline.
func TestNoFusedMultiplyAdd(t *testing.T) {
	fset := token.NewFileSet()
	imp := &moduleImporter{t: t, fset: fset, std: importer.Default(), done: map[string]*types.Package{}}
	for _, c := range []struct{ dir, path string }{
		{"mathx", modulePath + "internal/analysis/mathx"},
		{".", modulePath + "internal/analysis"},
	} {
		files := sources(t, fset, c.dir)
		info := &types.Info{Types: map[ast.Expr]types.TypeAndValue{}}
		conf := types.Config{Importer: imp}
		if _, err := conf.Check(c.path, fset, files, info); err != nil {
			t.Fatal(err)
		}
		for _, f := range files {
			var stack []ast.Node
			ast.Inspect(f, func(n ast.Node) bool {
				if n == nil {
					stack = stack[:len(stack)-1]
					return true
				}
				defer func() { stack = append(stack, n) }()
				if as, ok := n.(*ast.AssignStmt); ok && as.Tok == token.MUL_ASSIGN && isFloat(info, as.Lhs[0]) {
					t.Errorf("%s: *= on a float; write x = float64(x * y)", fset.Position(as.Pos()))
				}
				be, ok := n.(*ast.BinaryExpr)
				if !ok || be.Op != token.MUL || !isFloat(info, be) {
					return true
				}
				// The parent, past any parentheses.
				i := len(stack) - 1
				for i >= 0 {
					if _, ok := stack[i].(*ast.ParenExpr); !ok {
						break
					}
					i--
				}
				if i >= 0 && rounds(info, stack[i]) {
					return true
				}
				t.Errorf("%s: a float product that is not rounded; wrap it in float64()", fset.Position(be.Pos()))
				return true
			})
		}
	}
}

// rounds reports whether a product's parent keeps it from being fused: a
// float64 conversion, or a multiplication or division.
func rounds(info *types.Info, parent ast.Node) bool {
	switch p := parent.(type) {
	case *ast.CallExpr:
		if id, ok := p.Fun.(*ast.Ident); ok && id.Name == "float64" && len(p.Args) == 1 {
			tv, ok := info.Types[p.Fun]
			return ok && tv.IsType()
		}
	case *ast.BinaryExpr:
		return p.Op == token.MUL || p.Op == token.QUO
	}
	return false
}
