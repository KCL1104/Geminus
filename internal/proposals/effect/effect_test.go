package effect_test

// Without a database: what the AI's effects refuse before they read
// anything, the refusal's shape, and what the package may never do (H-061
// §15.2: it never publishes, approves or rejects).

import (
	"context"
	"errors"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"testing"

	"github.com/kcl1104/geminus/internal/ai/investigator"
	"github.com/kcl1104/geminus/internal/ids"
	"github.com/kcl1104/geminus/internal/proposals/effect"
	"github.com/kcl1104/geminus/internal/tenant"
)

// Outside an acting-for transaction, or for a member who may not write,
// the AI's effects refuse before they read anything: the nil transaction
// is never touched.
func TestRefusedBeforeAnyRead(t *testing.T) {
	s := &effect.Service{}
	org, user, inv, aip := ids.New("org"), ids.New("usr"), ids.New("inv"), ids.New("aip")
	for _, c := range []struct {
		name string
		m    tenant.Membership
		want error
	}{
		{"a person's own request", tenant.Membership{OrganizationID: org, UserID: user, Role: tenant.Editor}, effect.ErrNotActing},
		{"an owner's own request", tenant.Membership{OrganizationID: org, UserID: user, Role: tenant.Owner}, effect.ErrNotActing},
		{"for a viewer", tenant.Membership{OrganizationID: org, UserID: user, Role: tenant.Viewer, ActingFor: inv}, tenant.ErrForbidden},
		{"for no role", tenant.Membership{OrganizationID: org, UserID: user, ActingFor: inv}, tenant.ErrForbidden},
	} {
		if _, err := s.SaveScenario(context.Background(), nil, c.m, ids.New("req"), aip); !errors.Is(err, c.want) {
			t.Errorf("save_scenario %s: %v, want %v", c.name, err, c.want)
		}
		if _, err := s.WriteDraft(context.Background(), nil, c.m, ids.New("req"), aip); !errors.Is(err, c.want) {
			t.Errorf("write_to_draft %s: %v, want %v", c.name, err, c.want)
		}
	}
	// An ID that is not a proposal's is not one of the investigation's.
	m := tenant.Membership{OrganizationID: org, UserID: user, Role: tenant.Editor, ActingFor: inv}
	for _, id := range []string{"", "scn_01M4YDQK00ADN261CME3WV5HZ6", "aip_" + strings.Repeat("Z", 26), "aip_01M4YDQK00ADN261CME3WV5HZ6'; --"} {
		var r *effect.Refusal
		if _, err := s.SaveScenario(context.Background(), nil, m, ids.New("req"), id); !errors.As(err, &r) ||
			r.Code != investigator.NotInInvestigation || r.Path != "/proposal_id" {
			t.Errorf("save_scenario %q: %v", id, err)
		}
		if _, err := s.WriteDraft(context.Background(), nil, m, ids.New("req"), id); !errors.As(err, &r) || r.Code != investigator.NotInInvestigation {
			t.Errorf("write_to_draft %q: %v", id, err)
		}
	}
}

// A refusal is an InvestigationToolCode at a JSON Pointer, errors.Is
// investigator.ErrRefused, and its text names the code and the place only.
func TestRefusalShape(t *testing.T) {
	r := &effect.Refusal{Code: investigator.RecheckFailed, Path: "/proposal_id", Issues: []string{"DOMAIN_RULE_FAILED"}}
	if !errors.Is(r, investigator.ErrRefused) || !slices.Contains(investigator.Codes, r.Code) {
		t.Errorf("%v", r)
	}
	if got := r.Error(); got != `effect: refused: RECHECK_FAILED at "/proposal_id"` {
		t.Errorf("the refusal reads %q", got)
	}
}

// ScenarioName is D-067's name (the approval's tests hold it in detail):
// the reviewed summary, white space folded, at most 200 characters.
func TestScenarioName(t *testing.T) {
	id := ids.New("aip")
	if got := effect.ScenarioName("  Raise\tthe CI\nrunner  ", id); got != "Raise the CI runner" {
		t.Errorf("%q", got)
	}
	if got := effect.ScenarioName(" \n ", id); got != id {
		t.Errorf("only white space: %q", got)
	}
	if got := []rune(effect.ScenarioName(strings.Repeat("a", 300), id)); len(got) != 200 || got[199] != '…' {
		t.Errorf("a long summary: %d runes", len(got))
	}
}

// H-061 §15.2: the shared effects never publish, approve, reject or
// conflict a proposal, and never act without a user: no reference to a
// version insert, a publish, a decision or the organization-only scope in
// the package's source, and no SQL of its own.
func TestNoPublishOrDecision(t *testing.T) {
	forbidden := []string{"InsertVersion", "Publish", "Approve", "Reject", "Conflict", "SetLatest", "Advance", "RunOwners"}
	files, err := filepath.Glob("*.go")
	if err != nil {
		t.Fatal(err)
	}
	fset := token.NewFileSet()
	var sources int
	for _, path := range files {
		if strings.HasSuffix(path, "_test.go") {
			continue
		}
		sources++
		src, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		f, err := parser.ParseFile(fset, path, src, parser.SkipObjectResolution)
		if err != nil {
			t.Fatal(err)
		}
		for _, imp := range f.Imports {
			p, _ := strconv.Unquote(imp.Path.Value)
			if strings.HasSuffix(p, "/tenant/actingfor") || strings.HasSuffix(p, "/proposals/approval") || strings.Contains(p, "pgx") {
				t.Errorf("%s imports %s", path, p)
			}
		}
		ast.Inspect(f, func(n ast.Node) bool {
			switch x := n.(type) {
			case *ast.SelectorExpr:
				if slices.Contains(forbidden, x.Sel.Name) {
					t.Errorf("%s: %s refers to %s", path, fset.Position(x.Pos()), x.Sel.Name)
				}
			case *ast.BasicLit:
				if s, err := strconv.Unquote(x.Value); x.Kind == token.STRING && err == nil {
					for _, verb := range []string{"insert into", "update twin.", "delete from", "select "} {
						if strings.Contains(strings.ToLower(s), verb) {
							t.Errorf("%s: %s holds SQL", path, fset.Position(x.Pos()))
						}
					}
				}
			}
			return true
		})
	}
	if sources == 0 {
		t.Fatal("no source file read")
	}
}
