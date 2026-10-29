package ui

import (
	"go/ast"
	"go/parser"
	"go/token"
	"path/filepath"
	"strings"
	"testing"
)

// textSizeArg is the index of the size argument in the argument list of each
// text helper that takes one.
var textSizeArg = map[string]int{
	"text":           2,
	"wrappedText":    2,
	"selectableText": 3,
	"editor":         2,
	"textField":      3,
	"boxedField":     4,
}

// TestTextSizesComeFromTheScale keeps the ladder in typography.go authoritative:
// every text helper must be handed one of its steps, never a literal. A literal
// silently breaks the shared scale, which is exactly what the ladder exists to
// prevent.
func TestTextSizesComeFromTheScale(t *testing.T) {
	files, err := filepath.Glob("*.go")
	if err != nil {
		t.Fatal(err)
	}
	fset := token.NewFileSet()
	for _, name := range files {
		if strings.HasSuffix(name, "_test.go") {
			continue
		}
		file, err := parser.ParseFile(fset, name, nil, 0)
		if err != nil {
			t.Fatalf("parse %s: %v", name, err)
		}
		ast.Inspect(file, func(n ast.Node) bool {
			call, ok := n.(*ast.CallExpr)
			if !ok {
				return true
			}
			sel, ok := call.Fun.(*ast.SelectorExpr)
			if !ok {
				return true
			}
			arg, ok := textSizeArg[sel.Sel.Name]
			if !ok || arg >= len(call.Args) {
				return true
			}
			if lit, ok := call.Args[arg].(*ast.BasicLit); ok && lit.Kind == token.INT {
				t.Errorf("%s: %s is given the literal size %s, want a text* constant from typography.go",
					fset.Position(lit.Pos()), sel.Sel.Name, lit.Value)
			}
			return true
		})
	}
}
