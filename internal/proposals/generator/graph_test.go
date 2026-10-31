package generator

import (
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/kcl1104/geminus/internal/contracttest"
)

// H-059 (slice E3): a proposal asked for in words runs on the proposal role
// graph only, so no second pipeline is left: neither this handler nor the
// live eval calls the client's proposal method itself, and the handler
// does not run the repair around the graph. (The photo path,
// internal/ai/photopipeline, is slice E5's; the seal over every package is
// E6's.)
func TestTextProposalsRunOnTheGraphOnly(t *testing.T) {
	root := contracttest.Root(t)
	files, err := filepath.Glob(filepath.Join(root, "internal", "proposals", "generator", "*.go"))
	if err != nil {
		t.Fatal(err)
	}
	files = append(files, filepath.Join(root, "internal", "aievals", "live.go"))
	checked := 0
	for _, path := range files {
		if strings.HasSuffix(path, "_test.go") {
			continue
		}
		src, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		f, err := parser.ParseFile(token.NewFileSet(), path, src, 0)
		if err != nil {
			t.Fatal(err)
		}
		checked++
		ast.Inspect(f, func(n ast.Node) bool {
			call, ok := n.(*ast.CallExpr)
			if !ok {
				return true
			}
			sel, ok := call.Fun.(*ast.SelectorExpr)
			if !ok {
				return true
			}
			if x, ok := sel.X.(*ast.Ident); ok && x.Name == "repair" && sel.Sel.Name == "Once" {
				t.Errorf("%s runs the repair around the graph (repair.Once)", filepath.Base(path))
			}
			if sel.Sel.Name == "GenerateModelProposal" {
				t.Errorf("%s calls GenerateModelProposal itself, around the proposal graph", filepath.Base(path))
			}
			return true
		})
	}
	if checked < 5 {
		t.Fatalf("checked %d files", checked)
	}
}
