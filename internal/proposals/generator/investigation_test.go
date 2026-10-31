package generator

import (
	"context"
	"strings"
	"testing"

	"github.com/kcl1104/geminus/internal/ai/provider"
	"github.com/kcl1104/geminus/internal/ai/review"
)

// H-061 §9 (slice A6, re-applied on the proposal graph when A6 met E2-E6):
// a proposal whose row carries an investigation's label (started's
// investigation, read under the lease by storage/investigations.LabelOf) is
// asked with the investigation-rules-1 line once; a person's proposal is
// asked as before, without it, and both answers get the same review.
func TestAnInvestigationsProposalIsAskedWithItsRule(t *testing.T) {
	const rule = "\n## Investigation rules (investigation-rules-1)\n\n"
	raise := `[{"op":"test","path":"/resources/1/id","value":"ci_runner"},{"op":"replace","path":"/resources/1/capacity","value":6}]`
	var hashes []string
	for _, labelled := range []bool{false, true} {
		f := scripted(t, "Six runners.", reference(raise))
		h, err := New(Config{Provider: client(t, f), Packs: packs(t), Actor: "worker-1", Log: discard()})
		if err != nil {
			t.Fatal(err)
		}
		st := stored(t, "edit", "Six runners.")
		st.investigation = labelled
		o, meta, err := outcomeOf(h.generate(context.Background(), st.proposal.OrganizationID, st, testAttribution))
		if err != nil || o.Status != review.Ready || meta == nil {
			t.Fatalf("labelled %v: %+v %v", labelled, o, err)
		}
		reqs := f.Requests()
		if len(reqs) != 1 || reqs[0].Prompt.Role != provider.RoleModeler {
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
