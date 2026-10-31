package fake_test

import (
	"context"
	"encoding/json/jsontext"
	"testing"

	"github.com/kcl1104/geminus/internal/ai/locale"
	"github.com/kcl1104/geminus/internal/ai/provider"
	"github.com/kcl1104/geminus/internal/ai/provider/fake"
)

var finishTool = provider.Tool{Name: "finish", Description: "End it.",
	InputSchema: jsontext.Value(`{"type":"object","properties":{"outcome":{"enum":["done","stuck"]}},"required":["outcome"],"additionalProperties":false}`)}

func turn(t *testing.T, loc locale.Locale, steps ...provider.ToolStep) provider.TurnContext {
	return provider.TurnContext{OrganizationID: org, Tools: []provider.Tool{finishTool}, Steps: steps,
		Prompt: prompt(t, provider.RoleScenario, loc, provider.Block{Kind: provider.UntrustedData, Name: fake.IntentBlock, Content: quote("try a second shift")})}
}

// H-060: the fake answers a tool turn from its script, the n-th answer after
// n steps, through the client's checks like a live model; unscripted, safely
// in text with no call. Its calls are checked by the client: a scripted call
// of no offered tool is refused. A request that offers no tool never gets a
// call, and Complete refuses tools.
func TestScriptedToolTurns(t *testing.T) {
	ctx := context.Background()
	b := fake.New()
	b.ScriptTools(provider.RoleScenario, "try a second shift",
		fake.ToolAnswer{Text: "I will finish.", Calls: []provider.ToolCall{{ID: "fake_1", Name: "finish", Input: jsontext.Value(`{"outcome":"done"}`)}}},
		fake.ToolAnswer{Text: "All done."},
		fake.ToolAnswer{Calls: []provider.ToolCall{{ID: "fake_3", Name: "publish_version", Input: jsontext.Value(`{}`)}}})
	c := client(t, b)
	if !c.AcceptsTools(provider.RoleScenario) {
		t.Fatal("the fake takes no tool turn")
	}
	first, err := c.Turn(ctx, turn(t, locale.English))
	if err != nil || first.Text != "I will finish." || len(first.Calls) != 1 || first.Calls[0].ID != "fake_1" || first.Metadata.Provider != "fake" {
		t.Fatalf("the first turn: %+v %v", first, err)
	}
	step := provider.ToolStep{Text: first.Text, Calls: first.Calls,
		Results: []provider.ToolResult{{CallID: "fake_1", Content: jsontext.Value(`{"ended":true}`)}}}
	second, err := c.Turn(ctx, turn(t, locale.English, step))
	if err != nil || second.Text != "All done." || len(second.Calls) != 0 {
		t.Fatalf("the turn after a step: %+v %v", second, err)
	}
	if seen := b.Requests(); len(seen) != 2 || len(seen[1].Steps) != 1 || len(seen[1].Tools) != 1 {
		t.Errorf("the requests seen: %+v", seen)
	}
	step2 := provider.ToolStep{Calls: []provider.ToolCall{{ID: "fake_2", Name: "finish", Input: jsontext.Value(`{"outcome":"stuck"}`)}},
		Results: []provider.ToolResult{{CallID: "fake_2", Content: jsontext.Value(`{}`)}}}
	if _, err := c.Turn(ctx, turn(t, locale.English, step, step2)); provider.CodeOf(err) != provider.CodeInvalidOutput {
		t.Errorf("a scripted call of no offered tool: %v", err)
	}
	// Past the script, and unscripted: text, no call, in the locale.
	for _, loc := range locale.All {
		res, err := client(t, fake.New()).Turn(ctx, turn(t, loc))
		if err != nil || res.Text == "" || len(res.Calls) != 0 {
			t.Errorf("%s unscripted: %+v %v", loc, res, err)
		}
	}
	// A request that offers no tool is answered as before: no call.
	p, err := c.GenerateModelProposal(ctx, provider.ProposalContext{OrganizationID: org, Mode: provider.ModeScenario,
		Prompt: prompt(t, provider.RoleScenario, locale.English, provider.Block{Kind: provider.UntrustedData, Name: fake.IntentBlock, Content: quote("try a second shift")})})
	if err != nil || p.Output.Status != "needs_input" {
		t.Errorf("a proposal of the same key: %+v %v", p.Output, err)
	}
	if _, err := b.Complete(ctx, provider.Request{Prompt: turn(t, locale.English).Prompt, Tools: []provider.Tool{finishTool}}); provider.CodeOf(err) != provider.CodeInvalidRequest {
		t.Errorf("Complete with tools: %v", err)
	}
	if _, err := b.Turn(ctx, provider.Request{Prompt: turn(t, locale.English).Prompt}); provider.CodeOf(err) != provider.CodeInvalidRequest {
		t.Errorf("Turn without tools: %v", err)
	}
}
