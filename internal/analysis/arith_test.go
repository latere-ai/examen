// SPDX-FileCopyrightText: 2026 Latere AI
// SPDX-License-Identifier: Apache-2.0

package analysis

import (
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"go/types"
	"maps"
	"math"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strconv"
	"strings"
	"testing"
)

// allowedMath are the functions and constants of package math the analysis
// may call: each is exact or correctly rounded by IEEE 754 whatever the
// instruction, so it cannot change a figure between architectures. Exp,
// Log, Pow, Lgamma, Erf, Erfinv and the rest are internal/analysis/mathx's.
// The values are package math's own: the declarations the type check of
// TestNoFusedMultiplyAdd reads for them are written from these.
var allowedMath = map[string]any{
	"Sqrt": math.Sqrt, "Abs": math.Abs, "Floor": math.Floor, "Ceil": math.Ceil, "Trunc": math.Trunc, "Round": math.Round,
	"Max": math.Max, "Min": math.Min, "Copysign": math.Copysign, "Signbit": math.Signbit,
	"Ldexp": math.Ldexp, "Frexp": math.Frexp, "Inf": math.Inf, "NaN": math.NaN, "IsInf": math.IsInf,
	"IsNaN": math.IsNaN, "Float64bits": math.Float64bits, "Float64frombits": math.Float64frombits,
	"Nextafter": math.Nextafter, "Sqrt2": math.Sqrt2, "Ln2": math.Ln2, "Pi": math.Pi, "E": math.E,
	"MaxFloat64": math.MaxFloat64, "MaxInt": math.MaxInt,
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
				if _, allowed := allowedMath[sel.Sel.Name]; !allowed {
					if id, ok := sel.X.(*ast.Ident); ok && id.Name == name {
						t.Errorf("%s: math.%s; the analysis computes it with internal/analysis/mathx", fset.Position(sel.Pos()), sel.Sel.Name)
					}
				}
				return true
			})
		}
	}
}

// mathStub is the source of a package math that declares allowedMath
// alone: each function bodiless with the signature this binary's package
// math gives it, and each constant untyped, as package math declares it.
func mathStub() string {
	var b strings.Builder
	b.WriteString("package math\n\n")
	for _, name := range slices.Sorted(maps.Keys(allowedMath)) {
		switch v := allowedMath[name].(type) {
		case int:
			fmt.Fprintf(&b, "const %s = %d\n", name, v)
		case float64:
			fmt.Fprintf(&b, "const %s = %s\n", name, strconv.FormatFloat(v, 'e', -1, 64))
		default:
			ft := reflect.TypeOf(v)
			in, out := make([]string, ft.NumIn()), make([]string, ft.NumOut())
			for i := range in {
				in[i] = ft.In(i).String()
			}
			for i := range out {
				out[i] = ft.Out(i).String()
			}
			fmt.Fprintf(&b, "func %s(%s) (%s)\n", name, strings.Join(in, ", "), strings.Join(out, ", "))
		}
	}
	return b.String()
}

const modulePath = "latere.ai/x/examen/"

// stubImporter serves a type check without the standard library, so the
// check runs wherever the tests run, whether or not the toolchain's
// sources or export data can be found: this module's packages are
// type-checked from source, package math is mathStub, and every other
// package is empty, so whatever is read from one has no type.
type stubImporter struct {
	t    *testing.T
	fset *token.FileSet
	done map[string]*types.Package
}

func (m *stubImporter) Import(path string) (*types.Package, error) {
	if p, ok := m.done[path]; ok {
		return p, nil
	}
	var p *types.Package
	if rel, ok := strings.CutPrefix(path, modulePath); ok {
		p = typeCheck(path, m.fset, sources(m.t, m.fset, filepath.Join("..", "..", rel)), m, nil)
	} else if path == "math" {
		f, err := parser.ParseFile(m.fset, "math.go", mathStub(), 0)
		if err != nil {
			return nil, err
		}
		if p, err = (&types.Config{}).Check(path, m.fset, []*ast.File{f}, nil); err != nil {
			return nil, err
		}
	} else {
		p = types.NewPackage(path, packageName(path))
		p.MarkComplete()
	}
	m.done[path] = p
	return p, nil
}

// packageName is the name a package of an import path declares by
// convention: its last element, or the one before a major version suffix.
func packageName(path string) string {
	parts := strings.Split(path, "/")
	last := parts[len(parts)-1]
	if len(parts) > 1 && len(last) > 1 && last[0] == 'v' && strings.Trim(last[1:], "0123456789") == "" {
		return parts[len(parts)-2]
	}
	return last
}

// typeCheck type-checks files as the package path and goes on past every
// error: a name the importer does not serve leaves the expressions that
// read it without a type, which fusedProducts reports rather than passes.
func typeCheck(path string, fset *token.FileSet, files []*ast.File, imp types.Importer, info *types.Info) *types.Package {
	conf := types.Config{Importer: imp, Error: func(error) {}}
	p, _ := conf.Check(path, fset, files, info)
	return p
}

// operand is how fusedProducts reads a product's type.
type operand int

const (
	operandExact   operand = iota // an integer, or a constant computed at compile time
	operandFloat                  // a floating-point value computed at run time
	operandUntyped                // a type the check could not read
)

func operandOf(info *types.Info, e ast.Expr) operand {
	tv, ok := info.Types[e]
	if !ok || tv.Type == nil {
		return operandUntyped
	}
	if tv.Value != nil {
		return operandExact
	}
	b, ok := tv.Type.Underlying().(*types.Basic)
	switch {
	case !ok || b.Kind() == types.Invalid:
		return operandUntyped
	case b.Info()&(types.IsFloat|types.IsComplex) != 0:
		return operandFloat
	}
	return operandExact
}

// finding is one product fusedProducts reports.
type finding struct {
	pos token.Position
	msg string
}

// fusedProducts reports every product of files that the compiler may fuse
// with an addition. Go may fuse x*y + z into one instruction on arm64,
// across statements too, so a product held in a variable is no safer than
// one written inline: every floating-point multiplication must be the
// operand of an explicit float64 conversion, which rounds it, unless it
// feeds another multiplication or a division, which no fused instruction
// combines. A product the type check could not type is reported too, so a
// name the importer does not serve hides nothing.
func fusedProducts(fset *token.FileSet, files []*ast.File, info *types.Info) []finding {
	var out []finding
	report := func(n ast.Node, k operand, float string) {
		msg := float
		if k == operandUntyped {
			msg = "a product the check could not type; serve the names it reads in stubImporter"
		}
		out = append(out, finding{fset.Position(n.Pos()), msg})
	}
	for _, f := range files {
		var stack []ast.Node
		ast.Inspect(f, func(n ast.Node) bool {
			if n == nil {
				stack = stack[:len(stack)-1]
				return true
			}
			defer func() { stack = append(stack, n) }()
			if as, ok := n.(*ast.AssignStmt); ok && as.Tok == token.MUL_ASSIGN {
				if k := operandOf(info, as.Lhs[0]); k != operandExact {
					report(as, k, "*= on a float; write x = float64(x * y)")
				}
			}
			be, ok := n.(*ast.BinaryExpr)
			if !ok || be.Op != token.MUL {
				return true
			}
			k := operandOf(info, be)
			if k == operandExact {
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
			if i < 0 || !rounds(info, stack[i]) {
				report(be, k, "a float product that is not rounded; wrap it in float64()")
			}
			return true
		})
	}
	return out
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

// No product of the analysis can be fused with an addition.
func TestNoFusedMultiplyAdd(t *testing.T) {
	fset := token.NewFileSet()
	imp := &stubImporter{t: t, fset: fset, done: map[string]*types.Package{}}
	for _, c := range []struct{ dir, path string }{
		{"mathx", modulePath + "internal/analysis/mathx"},
		{".", modulePath + "internal/analysis"},
	} {
		files := sources(t, fset, c.dir)
		info := &types.Info{Types: map[ast.Expr]types.TypeAndValue{}}
		typeCheck(c.path, fset, files, imp, info)
		for _, f := range fusedProducts(fset, files, info) {
			t.Errorf("%s: %s", f.pos, f.msg)
		}
	}
}

// plantedProducts holds every kind of product fusedProducts reads; the
// lines marked "want" are the ones it must report.
const plantedProducts = `package planted

import (
	"math"
	"time"
)

func f(x, y, z float64, a, b, c int) (float64, int, time.Duration) {
	r := float64(x*y) + z
	r += x*y + z // want: a product summed
	p := math.Sqrt(x) * y // want: a product held in a variable, typed through mathStub
	r += float64(float64(x*y*z)/y) + p
	r *= 2 // want: *= on a float
	r += float64((math.Pi * x))
	n := a*b + c
	const k = 2 * 3.5
	d := time.Duration(a) * time.Second // want: a product of a package the importer does not serve
	return r + k, n, d
}
`

// The check reports a product summed, one held in a variable, *= on a float
// and a product it cannot type, and passes rounded, integer and constant
// products, so a violation planted in the analysis fails
// TestNoFusedMultiplyAdd.
func TestFusedProductsFindsPlanted(t *testing.T) {
	fset := token.NewFileSet()
	f, err := parser.ParseFile(fset, "planted.go", plantedProducts, 0)
	if err != nil {
		t.Fatal(err)
	}
	info := &types.Info{Types: map[ast.Expr]types.TypeAndValue{}}
	typeCheck("planted", fset, []*ast.File{f}, &stubImporter{t: t, fset: fset, done: map[string]*types.Package{}}, info)
	var want, got []int
	for i, line := range strings.Split(plantedProducts, "\n") {
		if strings.Contains(line, "// want") {
			want = append(want, i+1)
		}
	}
	for _, f := range fusedProducts(fset, []*ast.File{f}, info) {
		got = append(got, f.pos.Line)
	}
	if !slices.Equal(got, want) {
		t.Errorf("reported lines %v, want %v", got, want)
	}
	if s := mathStub(); !strings.Contains(s, "func Sqrt(float64) (float64)\n") || !strings.Contains(s, "func Frexp(float64) (float64, int)\n") {
		t.Errorf("the stub of package math declares\n%s", s)
	}
}
