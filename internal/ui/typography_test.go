package ui

import (
	"go/ast"
	"go/parser"
	"go/token"
	"image"
	"path/filepath"
	"strings"
	"testing"

	"gioui.org/font"
	"gioui.org/unit"
	"gioui.org/widget"
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

// TestReaderBodyLineHeight checks the reader body really gets the wider line
// spacing: the same two lines must take more room with bodyLineHeight than with
// the shaper's 1.2 default.
func TestReaderBodyLineHeight(t *testing.T) {
	a := testApp(t)
	const txt = "first line\nsecond line"
	height := func(lineHeight unit.Sp) int {
		_, _, gtx := newRouterFrame(image.Pt(400, 1<<16))
		sel := new(widget.Selectable)
		sel.SetText(txt)
		return a.th.selectableText(gtx, sel, txt, textMain, lineHeight, colText, font.Normal).Size.Y
	}
	def, body := height(0), height(bodyLineHeight)
	if body <= def {
		t.Fatalf("reader body height = %dpx with the line height, want more than the %dpx default", body, def)
	}
}
