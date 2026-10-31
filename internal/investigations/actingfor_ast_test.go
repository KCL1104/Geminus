package investigations

import (
	"go/ast"
	"go/parser"
	"go/token"
	"io/fs"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"testing"
)

// sources are the non-test Go files of internal/investigations/..., parsed.
func sources(t *testing.T) map[string]*ast.File {
	t.Helper()
	out := map[string]*ast.File{}
	fset := token.NewFileSet()
	err := filepath.WalkDir(".", func(path string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() || !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
			return err
		}
		f, err := parser.ParseFile(fset, path, nil, parser.SkipObjectResolution)
		if err != nil {
			return err
		}
		out[filepath.ToSlash(path)] = f
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(out) < 5 {
		t.Fatalf("found only %v", out)
	}
	return out
}

func imports(f *ast.File) []string {
	var out []string
	for _, i := range f.Imports {
		p, _ := strconv.Unquote(i.Path.Value)
		out = append(out, p)
	}
	return out
}

// H-061 §8, §15 (2): the investigation packages never publish, decide or
// write SQL of their own, and only the executor acts for the AI.
//   - No reference to a publish, a version's insert, the model's latest
//     version moved, a decision (approve, reject, conflict) or the owners'
//     scope, and no import of the approval service or the model service
//     (which publishes).
//   - Only internal/investigations/executor sets tenant.Scope.ActingFor or
//     links internal/tenant/actingfor (the service is the person's own
//     requests; tenant's TestOnlyTheExecutorActsFor and the architecture
//     rule acting-for-only-in-the-executor hold the rest of the tree).
//   - No pgx and no SQL text: SQL lives in internal/storage.
func TestNoActingPublishOrDecision(t *testing.T) {
	forbidden := []string{"Publish", "InsertVersion", "SetLatest", "Approve", "Reject", "Conflict", "RunOwners"}
	sql := regexp.MustCompile(`(?i)\b(select|insert\s+into|update\s+twin|delete\s+from)\b`)
	for path, f := range sources(t) {
		executor := strings.HasPrefix(path, "executor/")
		for _, p := range imports(f) {
			switch {
			case p == "github.com/kcl1104/geminus/internal/proposals/approval", p == "github.com/kcl1104/geminus/internal/models":
				t.Errorf("%s imports %s", path, p)
			case strings.HasPrefix(p, "github.com/jackc/pgx"):
				t.Errorf("%s imports %s: SQL lives in internal/storage", path, p)
			case p == "github.com/kcl1104/geminus/internal/tenant/actingfor" && !executor:
				t.Errorf("%s links the acting-for scope; only the executor may", path)
			}
		}
		ast.Inspect(f, func(n ast.Node) bool {
			switch x := n.(type) {
			case *ast.SelectorExpr:
				if slices.Contains(forbidden, x.Sel.Name) {
					t.Errorf("%s refers to %s", path, x.Sel.Name)
				}
			case *ast.KeyValueExpr:
				if id, ok := x.Key.(*ast.Ident); ok && id.Name == "ActingFor" && !executor {
					t.Errorf("%s sets ActingFor; only the executor acts for the AI", path)
				}
			case *ast.AssignStmt:
				for _, l := range x.Lhs {
					if sel, ok := l.(*ast.SelectorExpr); ok && sel.Sel.Name == "ActingFor" && !executor {
						t.Errorf("%s assigns ActingFor; only the executor acts for the AI", path)
					}
				}
			case *ast.BasicLit:
				if s, err := strconv.Unquote(x.Value); x.Kind == token.STRING && err == nil && sql.MatchString(s) {
					t.Errorf("%s holds SQL text %q", path, s)
				}
			}
			return true
		})
	}
}

// The executor does set ActingFor (it is the one that acts for the AI), so
// the test above checks the right tree: a probe that it sees the field.
func TestTheProbeSeesTheExecutorsScope(t *testing.T) {
	found := false
	for path, f := range sources(t) {
		if !strings.HasPrefix(path, "executor/") {
			continue
		}
		ast.Inspect(f, func(n ast.Node) bool {
			if kv, ok := n.(*ast.KeyValueExpr); ok {
				if id, ok := kv.Key.(*ast.Ident); ok && id.Name == "ActingFor" {
					found = true
				}
			}
			return true
		})
	}
	if !found {
		t.Error("the executor's acting-for scope was not found: the source test reads the wrong files")
	}
}
