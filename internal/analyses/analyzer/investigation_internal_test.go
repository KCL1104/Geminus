package analyzer

import (
	"bytes"
	"context"
	"strings"
	"testing"

	"github.com/kcl1104/geminus/internal/ai/provider"
	"github.com/kcl1104/geminus/internal/ai/provider/fake"
)

// H-061 §9 (slice A6, re-applied on the analysis graph when A6 met E2-E6):
// an analysis whose row carries an investigation's label (started's
// investigation, read under the lease by storage/investigations.LabelOf) is
// asked with the investigation-rules-1 line once; a person's analysis is
// asked as before, without it, and both come back checked.
func TestAnInvestigationsAnalysisIsAskedWithItsRule(t *testing.T) {
	const rule = "\n## Investigation rules (investigation-rules-1)\n\n"
	var hashes []string
	for _, labelled := range []bool{false, true} {
		f := fake.New()
		c, err := provider.New(f, provider.DefaultBudget())
		if err != nil {
			t.Fatal(err)
		}
		var buf bytes.Buffer
		h := setup(t, c, nil, provider.DefaultBudget(), &buf)
		st := fixture(t)
		st.investigation = labelled
		a, err := h.analyze(context.Background(), tOrg, st, tAttr)
		meta, issues := a.meta, a.issues
		if err != nil || meta == nil || issues != 0 {
			t.Fatalf("labelled %v: %v, %d issues", labelled, err, issues)
		}
		reqs := f.Requests()
		if len(reqs) != 1 || reqs[0].Prompt.Role != provider.RoleAnalyst {
			t.Fatalf("labelled %v: %d requests", labelled, len(reqs))
		}
		want := 0
		if labelled {
			want = 1
		}
		if n := strings.Count(reqs[0].Prompt.System, rule); n != want {
			t.Errorf("labelled %v: the call carries the rule %d times", labelled, n)
		}
		hashes = append(hashes, meta.InputHash)
	}
	if hashes[0] == hashes[1] {
		t.Error("the labelled prompt hashes as the person's")
	}
}
