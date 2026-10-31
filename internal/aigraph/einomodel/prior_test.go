package einomodel_test

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"testing"

	"github.com/cloudwego/eino/callbacks"
	"github.com/cloudwego/eino/compose"
	"github.com/cloudwego/eino/schema"

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

// H-057 through the adapter (slice E5, the photo graph's repair): the client
// hands a proposal's answer back with an invalid_output error when it refused
// the answer only for what it says (provider.ProposalResult.Prior), and the
// one repair follows exactly that answer. The adapter keeps it on the error
// (CallError.Prior), the same bytes as a direct call's, also through a
// compiled graph; a failure with no answer handed back has none. The
// adapter's own callbacks still see no content: their error has no answer.
func TestARefusedAnswerComesBackForTheRepair(t *testing.T) {
	ctx := context.Background()
	c, f, _ := meteredFake(t)
	// The request's answer, schema-invalid (no assumptions, risk, changes):
	// refused for what it says.
	f.Script(provider.RoleScenario, intent, []byte(`{"status":"proposed","summary":"`+sentAns+`"}`))
	pc := &provider.ProposalContext{OrganizationID: org, Mode: provider.ModeScenario, Prompt: sealed(t, provider.RoleScenario, fake.IntentBlock, intent), Attribution: attribution}
	direct, err := c.GenerateModelProposal(ctx, *pc)
	if provider.CodeOf(err) != provider.CodeInvalidOutput || !direct.Prior.Present() {
		t.Fatalf("the direct call: %v, prior %v", err, direct.Prior.Present())
	}

	var mu sync.Mutex
	var seen []error
	// The adapter's own callbacks (as TestCallbacksCarryNoContent: a handler
	// on a compiled graph also gets the graph's own error, the node's
	// returned one; E1 finding 4).
	h := callbacks.NewHandlerBuilder().OnErrorFn(func(ctx context.Context, info *callbacks.RunInfo, err error) context.Context {
		if info == nil || info.Type != "GeminusMetered" {
			return ctx
		}
		mu.Lock()
		defer mu.Unlock()
		seen = append(seen, err)
		return ctx
	}).Build()
	chain, err := compose.NewChain[[]*schema.Message, *schema.Message]().AppendChatModel(adapter(t, c)).Compile(ctx)
	if err != nil {
		t.Fatal(err)
	}
	_, err = chain.Invoke(ctx, messages(pc.Prompt, &einomodel.Call{Proposal: pc}), compose.WithCallbacks(h))
	var ce *einomodel.CallError
	if provider.CodeOf(err) != provider.CodeInvalidOutput || !errors.As(err, &ce) || !ce.Prior.Present() ||
		!bytes.Equal(ce.Prior.Bytes(), direct.Prior.Bytes()) || ce.Metadata.InputHash != direct.Metadata.InputHash || ce.Metadata.SpendReservation == "" {
		t.Errorf("through a graph: %v %+v", err, ce)
	}
	// Standalone, the same.
	sctx := callbacks.InitCallbacks(ctx, &callbacks.RunInfo{Type: "GeminusMetered", Component: "ChatModel", Name: "standalone"}, h)
	_, err = adapter(t, c).Generate(sctx, messages(pc.Prompt, &einomodel.Call{Proposal: pc}))
	if !errors.As(err, &ce) || !bytes.Equal(ce.Prior.Bytes(), direct.Prior.Bytes()) {
		t.Errorf("standalone: %v %+v", err, ce)
	}
	if len(seen) != 2 {
		t.Fatalf("the adapter's callbacks saw %d errors, want 2: the test would be vacuous", len(seen))
	}
	for _, e := range seen {
		var cce *einomodel.CallError
		if errors.As(e, &cce) && cce.Prior.Present() {
			t.Errorf("a callback's error carries the answer handed back")
		}
		if provider.CodeOf(e) != provider.CodeInvalidOutput {
			t.Errorf("a callback's error: %v", e)
		}
	}

	// No answer handed back: none on the error.
	f.Fail(provider.RoleScenario, intent, provider.NewError(provider.CodeRateLimited, 429, "meta"))
	_, err = adapter(t, c).Generate(ctx, messages(pc.Prompt, &einomodel.Call{Proposal: pc}))
	if provider.CodeOf(err) != provider.CodeRateLimited || !errors.As(err, &ce) || ce.Prior.Present() {
		t.Errorf("a failure without an answer: %v %+v", err, ce)
	}
	// An explanation never hands an answer back.
	f.Script(provider.RoleAnalyst, "why", []byte(`{"summary":"`+sentAns+`"}`))
	ac := &provider.AnalysisContext{OrganizationID: org, Prompt: sealed(t, provider.RoleAnalyst, fake.QuestionBlock, "why"), Attribution: attribution}
	_, err = adapter(t, c).Generate(ctx, messages(ac.Prompt, &einomodel.Call{Analysis: ac}))
	if provider.CodeOf(err) != provider.CodeInvalidOutput || !errors.As(err, &ce) || ce.Prior.Present() {
		t.Errorf("an explanation refused: %v %+v", err, ce)
	}
}
