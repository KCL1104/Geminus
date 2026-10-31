package aicontext

import (
	"strings"
	"testing"

	"github.com/kcl1104/geminus/internal/ai/provider"
)

// H-061 §9 (slice A6): a proposal or an analysis an AI investigation asked
// for carries one more server rule, investigation-rules-1, reworded for an
// AI planner acting for the person; every other call's prompt is byte for
// byte what it was (its goldens and InputHash do not move).

const (
	investigationProposalLine = "- In this request an AI acting for the person who asked may save a ready what-if as a scenario and run it, or write a ready edit into a clean draft; the server validates every change and sets the risk, and only a person publishes.\n"
	investigationAnalystLine  = "- An AI acting for the person may turn your suggested experiments into what-ifs; each is reviewed like any proposal.\n"
	investigationHeader       = "\n## Investigation rules (investigation-rules-1)\n\n"
)

func TestTheInvestigationRulesLine(t *testing.T) {
	for _, mode := range []provider.Mode{provider.ModeEdit, provider.ModeScenario} {
		in := input(t)
		in.Mode = mode
		plain, err := Proposal(in)
		if err != nil {
			t.Fatal(err)
		}
		in.Investigation = true
		labelled, err := Proposal(in)
		if err != nil {
			t.Fatal(err)
		}
		if strings.Contains(plain.Prompt.System, "investigation-rules") || strings.Contains(plain.Prompt.System, "acting for the person") {
			t.Fatalf("%s: the rule reached a person's proposal", mode)
		}
		if got := strings.Replace(labelled.Prompt.System, investigationHeader+investigationProposalLine, "", 1); got != plain.Prompt.System {
			t.Fatalf("%s: the labelled prompt differs by more than the one rule", mode)
		}
		if strings.Count(labelled.Prompt.System, investigationProposalLine) != 1 || labelled.Prompt.User() != plain.Prompt.User() ||
			labelled.Prompt.TemplateVersion != plain.Prompt.TemplateVersion || labelled.Prompt.InputHash == plain.Prompt.InputHash {
			t.Fatalf("%s: the rule is not the system text's one change", mode)
		}
	}
	// With research, the research template stays last.
	in := input(t)
	in.Mode, in.Research, in.Investigation = provider.ModeScenario, true, true
	r, err := Proposal(in)
	if err != nil {
		t.Fatal(err)
	}
	research, _, err := template("research")
	if err != nil {
		t.Fatal(err)
	}
	if i, j := strings.Index(r.Prompt.System, investigationProposalLine), strings.Index(r.Prompt.System, research); i < 0 || j < i {
		t.Fatalf("research and the rule: %d %d", i, j)
	}

	a := analysisInput(t)
	plain, err := Analysis(a)
	if err != nil {
		t.Fatal(err)
	}
	a.Investigation = true
	labelled, err := Analysis(a)
	if err != nil {
		t.Fatal(err)
	}
	if got := strings.Replace(labelled.Context.Prompt.System, investigationHeader+investigationAnalystLine, "", 1); got != plain.Context.Prompt.System ||
		strings.Contains(plain.Context.Prompt.System, "investigation-rules") {
		t.Fatal("the analyst's rule is not the system text's one change")
	}
}
