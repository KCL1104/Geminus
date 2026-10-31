package fake_test

import (
	"context"
	"encoding/json/jsontext"
	"testing"

	"github.com/kcl1104/geminus/internal/ai/locale"
	"github.com/kcl1104/geminus/internal/ai/provider"
	"github.com/kcl1104/geminus/internal/ai/provider/fake"
)

const (
	refusedAnswer = `{"status":"proposed","summary":"first","assumptions":[],"risk_level":"low","confidence":0.5,` +
		`"changes":[{"op":"test","path":"/name","value":"x"}],"expected_effects":["None until a simulation runs."],"requires_confirmation":true}`
	repairedAnswer = `{"status":"needs_input","summary":"second","assumptions":[],"questions":["Which?"],"requires_confirmation":true}`
)

// revise is the repair of first (H-057) on prior, sealed.
func revise(t *testing.T, first provider.Prompt, prior provider.PriorAnswer) provider.Prompt {
	t.Helper()
	p := first
	p.Revision = &provider.Revision{Prior: prior, Rules: "Fix it.",
		Blocks: []provider.Block{{Kind: provider.ServerData, Name: "SERVER_REVIEW", Content: jsontext.Value(`{"issues":[]}`)}}}
	out, err := provider.Seal(p)
	if err != nil {
		t.Fatal(err)
	}
	return out
}

// H-057: the fake answers a repair from its revision script, keyed as the
// first answer is; unscripted, it repeats its first answer, as a model that
// does not fix anything would. Every answer it gives is its Answer too, so
// the client hands it back for a repair; the requests it saw hold the
// revision.
func TestTheFakeAnswersARepair(t *testing.T) {
	ctx := context.Background()
	f := fake.New()
	f.Script(provider.RoleModeler, "Fix the line.", []byte(refusedAnswer))
	c := client(t, f)
	first := prompt(t, provider.RoleModeler, locale.English, provider.Block{Kind: provider.UntrustedData, Name: fake.IntentBlock, Content: quote("Fix the line.")})
	ask := func(p provider.Prompt) provider.ProposalResult {
		t.Helper()
		res, err := c.GenerateModelProposal(ctx, provider.ProposalContext{OrganizationID: org, Mode: provider.ModeEdit, Prompt: p})
		if err != nil {
			t.Fatal(err)
		}
		return res
	}
	res := ask(first)
	if res.Output.Summary != "first" || !res.Prior.Present() || string(res.Prior.Bytes()) != refusedAnswer {
		t.Fatalf("first: %+v", res.Output)
	}
	if again := ask(revise(t, first, res.Prior)); again.Output.Summary != "first" {
		t.Errorf("an unscripted repair: %q, want the first answer again", again.Output.Summary)
	}
	f.ScriptRevision(provider.RoleModeler, "Fix the line.", []byte(repairedAnswer))
	if second := ask(revise(t, first, res.Prior)); second.Output.Summary != "second" {
		t.Errorf("a scripted repair: %q", second.Output.Summary)
	}
	f.FailRevision(provider.RoleModeler, "Fix the line.", provider.NewError(provider.CodeTimeout, 0, ""))
	if _, err := c.GenerateModelProposal(ctx, provider.ProposalContext{OrganizationID: org, Mode: provider.ModeEdit,
		Prompt: revise(t, first, res.Prior)}); provider.CodeOf(err) != provider.CodeTimeout {
		t.Errorf("a failed repair: %v", err)
	}
	reqs := f.Requests()
	if len(reqs) != 4 || reqs[0].Prompt.Revision != nil || reqs[1].Prompt.Revision == nil || string(reqs[1].Prompt.Revision.Prior.Bytes()) != refusedAnswer {
		t.Errorf("%d requests", len(reqs))
	}
	// The safe default (no script at all) repeats too.
	g := fake.New()
	dc := client(t, g)
	p := prompt(t, provider.RoleModeler, locale.English, provider.Block{Kind: provider.UntrustedData, Name: fake.IntentBlock, Content: quote("Anything.")})
	d, err := dc.GenerateModelProposal(ctx, provider.ProposalContext{OrganizationID: org, Mode: provider.ModeEdit, Prompt: p})
	if err != nil || !d.Prior.Present() {
		t.Fatalf("%v", err)
	}
	r, err := dc.GenerateModelProposal(ctx, provider.ProposalContext{OrganizationID: org, Mode: provider.ModeEdit, Prompt: revise(t, p, d.Prior)})
	if err != nil || r.Output.Summary != d.Output.Summary {
		t.Errorf("the default repair: %+v %v", r.Output, err)
	}
}
