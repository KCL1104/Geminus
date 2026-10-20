package engine

import (
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// Compile runs validateIR, a package variable so that a test can stand in a
// validator (T-046 review M2). No production file of this package may
// assign it or take its address, or a build could run models the validator
// refuses (T-046 phase 2 review I2, a follow-up to close before T-047 lifted
// the first gate). The variable is unexported, so only this package's files
// can; this reads them all. It is a source check, not an architecture rule:
// scripts/check-architecture reads the import graph, not code.
func TestValidateIRIsOnlyReplacedInTests(t *testing.T) {
	files, err := filepath.Glob("*.go")
	if err != nil || len(files) == 0 {
		t.Fatalf("no Go files: %v", err)
	}
	fset := token.NewFileSet()
	declared, checked := 0, 0
	for _, name := range files {
		if strings.HasSuffix(name, "_test.go") {
			continue
		}
		src, err := os.ReadFile(name)
		if err != nil {
			t.Fatal(err)
		}
		f, err := parser.ParseFile(fset, name, src, 0)
		if err != nil {
			t.Fatal(err)
		}
		checked++
		ast.Inspect(f, func(n ast.Node) bool {
			switch n := n.(type) {
			case *ast.ValueSpec:
				for _, id := range n.Names {
					if id.Name == "validateIR" {
						declared++
					}
				}
			case *ast.AssignStmt:
				for _, lhs := range n.Lhs {
					if id, ok := lhs.(*ast.Ident); ok && id.Name == "validateIR" {
						t.Errorf("%s: production code assigns validateIR", fset.Position(n.Pos()))
					}
				}
			case *ast.UnaryExpr:
				if id, ok := n.X.(*ast.Ident); ok && n.Op == token.AND && id.Name == "validateIR" {
					t.Errorf("%s: production code takes validateIR's address", fset.Position(n.Pos()))
				}
			}
			return true
		})
	}
	if declared != 1 || checked < 5 {
		t.Fatalf("validateIR declared %d times in %d files", declared, checked)
	}
}
