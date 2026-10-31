package einomodel_test

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/kcl1104/geminus/internal/ai/provider"
	"github.com/kcl1104/geminus/internal/ai/provider/fake"
	"github.com/kcl1104/geminus/internal/aigraph/einomodel"
)

// H-057 through the adapter (slice E3): a proposal the client refuses for
// what it says (invalid_output) comes with the model's answer handed back
// for the one repair (provider.ProposalResult.Prior); the adapter's error
// keeps it, as the client's result does, so the proposal graph repairs
// exactly what a direct call would. It is withheld when printed, and no
// other failure carries one.
func TestARefusedProposalKeepsTheAnswerForItsRepair(t *testing.T) {
	ctx := context.Background()
	c, f, _ := meteredFake(t)
	// An answer of the right shape whose summary is not a string: the
	// client's strict decoding refuses it (invalid_output) and hands it back.
	f.Script(provider.RoleScenario, intent, []byte(`{"status":"needs_input","summary":7,"assumptions":[],"questions":["q"],"requires_confirmation":true}`))
	pc := provider.ProposalContext{OrganizationID: org, Mode: provider.ModeScenario, Prompt: sealed(t, provider.RoleScenario, fake.IntentBlock, intent), Attribution: attribution}
	direct, derr := c.GenerateModelProposal(ctx, pc)
	if provider.CodeOf(derr) != provider.CodeInvalidOutput || !direct.Prior.Present() {
		t.Fatalf("the direct call: %v (prior %v)", derr, direct.Prior.Present())
	}
	_, err := adapter(t, c).Generate(ctx, messages(pc.Prompt, &einomodel.Call{Proposal: &pc}))
	var ce *einomodel.CallError
	if !errors.As(err, &ce) || provider.CodeOf(err) != provider.CodeInvalidOutput {
		t.Fatalf("through the adapter: %v", err)
	}
	if !ce.Prior.Present() || ce.Prior.Len() != direct.Prior.Len() || string(ce.Prior.Bytes()) != string(direct.Prior.Bytes()) {
		t.Errorf("the answer handed back: %d bytes, want %d", ce.Prior.Len(), direct.Prior.Len())
	}
	if ce.Metadata.InputHash != direct.Metadata.InputHash || ce.Metadata.SpendReservation == "" {
		t.Errorf("metadata %+v", ce.Metadata)
	}
	if s := fmt.Sprintf("%v %+v %#v", ce, *ce, ce.Prior); strings.Contains(s, "needs_input") {
		t.Errorf("the answer is printed: %s", s)
	}
	// Any other failure hands nothing back.
	f.Fail(provider.RoleScenario, intent, provider.NewError(provider.CodeRateLimited, 429, "org-a"))
	_, err = adapter(t, c).Generate(ctx, messages(pc.Prompt, &einomodel.Call{Proposal: &pc}))
	if !errors.As(err, &ce) || ce.Prior.Present() {
		t.Errorf("a rate-limited call: %v (prior %v)", err, ce != nil && ce.Prior.Present())
	}
}
