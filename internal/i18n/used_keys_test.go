package i18n

import (
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"io/fs"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"testing"
)

// metadataKeys are catalog entries the code reads as data instead of showing
// through T or N. language.name is the native name of a language in the
// settings picker; catalogMeta reads it straight from the JSON.
var metadataKeys = map[string]bool{
	"language.name": true,
}

// TestSourceKeysMatchCatalog ties the catalogs to the code in both directions:
// every message a call site names must exist, and every catalog entry must be
// named by a call site, so neither a raw key in the UI nor a stale translation
// survives. Dynamic keys are rejected as well: the key has to be a literal,
// which is what makes the scan exhaustive.
func TestSourceKeysMatchCatalog(t *testing.T) {
	root, err := repoRoot()
	if err != nil {
		t.Fatal(err)
	}

	var (
		used     = map[string]bool{}
		problems []string
		dynamic  []string
	)
	fset := token.NewFileSet()
	for _, dir := range []string{"internal", "cmd"} {
		walkErr := filepath.WalkDir(filepath.Join(root, dir), func(path string, entry fs.DirEntry, walkErr error) error {
			if walkErr != nil {
				return walkErr
			}
			if entry.IsDir() || !strings.HasSuffix(path, ".go") {
				return nil
			}
			file, parseErr := parser.ParseFile(fset, path, nil, parser.SkipObjectResolution)
			if parseErr != nil {
				return parseErr
			}
			ast.Inspect(file, func(n ast.Node) bool {
				call, ok := n.(*ast.CallExpr)
				if !ok || !isTranslateCall(call) {
					return true
				}
				id, ok := messageID(call)
				if !ok {
					dynamic = append(dynamic, fset.Position(call.Pos()).String())
					return true
				}
				used[id] = true
				if !catalogIDs[DefaultLanguage][id] {
					problems = append(problems, fmt.Sprintf("%s: unknown message key %q", fset.Position(call.Pos()), id))
				}
				return true
			})
			return nil
		})
		if walkErr != nil {
			t.Fatalf("scan %s: %v", dir, walkErr)
		}
	}

	sort.Strings(problems)
	for _, problem := range problems {
		t.Error(problem)
	}
	sort.Strings(dynamic)
	for _, call := range dynamic {
		t.Errorf("%s: message key must be a literal", call)
	}

	// The scan is only meaningful when it saw the bulk of the UI; a broken
	// path or a parser change would otherwise pass with nothing checked.
	if len(used) < 200 {
		t.Fatalf("found %d message keys, want at least 200", len(used))
	}

	var unused []string
	for id := range catalogIDs[DefaultLanguage] {
		if used[id] || metadataKeys[id] {
			continue
		}
		unused = append(unused, id)
	}
	sort.Strings(unused)
	for _, id := range unused {
		t.Errorf("unused message key %q", id)
	}
}

// repoRoot walks up from this file to the module root and checks the anchor
// before returning it, so a moved test fails here instead of scanning nothing.
func repoRoot() (string, error) {
	_, file, _, ok := runtime.Caller(0)
	if !ok {
		return "", fmt.Errorf("locate the test file")
	}
	root := filepath.Dir(filepath.Dir(filepath.Dir(file)))
	if _, err := os.Stat(filepath.Join(root, "go.mod")); err != nil {
		return "", fmt.Errorf("locate the module root above %s: %w", file, err)
	}
	return root, nil
}

// isTranslateCall reports whether call is an i18n.T or i18n.N invocation.
func isTranslateCall(call *ast.CallExpr) bool {
	sel, ok := call.Fun.(*ast.SelectorExpr)
	if !ok {
		return false
	}
	pkg, ok := sel.X.(*ast.Ident)
	if !ok || pkg.Name != "i18n" {
		return false
	}
	return sel.Sel.Name == "T" || sel.Sel.Name == "N"
}

// messageID returns the literal message key of a translate call. ok is false
// for a computed key, which the caller reports as a violation.
func messageID(call *ast.CallExpr) (id string, ok bool) {
	if len(call.Args) == 0 {
		return "", false
	}
	lit, ok := call.Args[0].(*ast.BasicLit)
	if !ok || lit.Kind != token.STRING {
		return "", false
	}
	id, err := strconv.Unquote(lit.Value)
	if err != nil {
		return "", false
	}
	return id, true
}
