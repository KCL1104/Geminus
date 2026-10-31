package fake_test

import (
	"bytes"
	"context"
	"encoding/json/jsontext"
	"fmt"
	"testing"

	"github.com/kcl1104/geminus/internal/ai/locale"
	"github.com/kcl1104/geminus/internal/ai/provider"
	"github.com/kcl1104/geminus/internal/ai/provider/fake"
)

// H-061 §9 (A5): the fake answers an investigation's planner turns for its
// two planner models, from ScriptTools' script under the person's request:
// the n-th answer for the turn of an investigation with n recorded steps
// (the digest's entries and the steps replayed), so a digest transcript
// (a fresh prompt every turn) and a replayed one read the same script. Its
// answer's content comes back as a replay, as a live model's would.

type passDecoder struct{}

func (passDecoder) DecodeCall(_ string, input []byte) ([]byte, *provider.ToolRefusal) {
	return input, nil
}

const invID = "inv_01M4YDQK00TA00000000000001"

var planAttr = provider.Attribution{TargetType: provider.PlannerTarget, TargetID: invID, RequestID: "req_01M4YDQK00TA00000000000001"}

func planPrompt(t *testing.T, digest string) provider.Prompt {
	t.Helper()
	return prompt(t, provider.RoleInvestigator, locale.English,
		provider.Block{Kind: provider.UntrustedData, Name: fake.RequestBlock, Content: quote("find a shorter lead time")},
		provider.Block{Kind: provider.UntrustedData, Name: fake.DigestBlock, Content: jsontext.Value(digest)})
}

func TestScriptedPlannerTurns(t *testing.T) {
	ctx := context.Background()
	b := fake.New()
	finish := func(id, outcome string) fake.ToolAnswer {
		return fake.ToolAnswer{Text: "Done " + id + ".", Calls: []provider.ToolCall{{ID: id, Name: "finish", Input: jsontext.Value(`{"outcome":"` + outcome + `"}`)}}}
	}
	b.ScriptTools(provider.RoleInvestigator, "find a shorter lead time", finish("fake_0", "done"), finish("fake_1", "stuck"), finish("fake_2", "done"))
	c := client(t, b)
	if !c.AcceptsTools(provider.RoleInvestigator) {
		t.Fatal("the fake takes no planner turn")
	}
	for _, m := range []string{"muse-spark-1.3", "muse-wonder-1.0"} {
		if !c.AcceptsModel(provider.RoleInvestigator, m) {
			t.Errorf("the planner %s", m)
		}
	}
	for _, m := range []string{"muse-spark-1.3-contributor", "claude-opus-5-5", ""} {
		if c.AcceptsModel(provider.RoleInvestigator, m) {
			t.Errorf("the planner %q", m)
		}
	}
	in := func(digest string, steps ...provider.ToolStep) provider.PlanContext {
		return provider.PlanContext{OrganizationID: org, Prompt: planPrompt(t, digest), Attribution: planAttr, Model: "muse-spark-1.3",
			Tools: []provider.Tool{finishTool}, Steps: steps, Decoder: passDecoder{}}
	}
	first, err := c.PlanTurn(ctx, in(`[]`))
	if err != nil || first.Call.ID != "fake_0" || first.Note != "Done fake_0." || !first.Replay.Present() {
		t.Fatalf("the first turn: %+v %v", first, err)
	}
	// A fresh prompt whose digest holds two steps: the third answer.
	third, err := c.PlanTurn(ctx, in(`[{"seq":1},{"seq":2}]`))
	if err != nil || third.Call.ID != "fake_2" {
		t.Fatalf("a digest of two steps: %+v %v", third, err)
	}
	// One step in the digest and one replayed: the third answer too, and
	// the replay is what the fake returned.
	step := provider.ToolStep{Calls: []provider.ToolCall{first.Call},
		Results: []provider.ToolResult{{CallID: first.Call.ID, Content: jsontext.Value(`{"budget":{}}`)}}, Replay: first.Replay}
	again, err := c.PlanTurn(ctx, in(`[{"seq":1}]`, step))
	if err != nil || again.Call.ID != "fake_2" {
		t.Fatalf("a digest of one step and one replayed: %+v %v", again, err)
	}
	seen := b.Requests()
	if len(seen) != 3 || !seen[0].OneCall || len(seen[2].Steps) != 1 || !bytes.Equal(seen[2].Steps[0].Replay.Bytes(), first.Replay.Bytes()) {
		t.Fatalf("the requests seen: %d", len(seen))
	}
	if !bytes.Contains(first.Replay.Bytes(), []byte(`"type":"tool_use"`)) || bytes.Contains([]byte(fmt.Sprint(first)), []byte(`"type"`)) {
		t.Errorf("the replay: %s", first.Replay.Bytes())
	}
	// Past the script: text, no call (an invalid turn the ledger does not
	// record).
	past, err := c.PlanTurn(ctx, in(`[{"seq":1},{"seq":2},{"seq":3}]`))
	if provider.CodeOf(err) != provider.CodeInvalidOutput || past.Invalid == nil || past.Invalid.Kind != provider.InvalidNoCall {
		t.Errorf("past the script: %+v %v", past.Invalid, err)
	}
	other := in(`[]`)
	other.Model = "muse-spark-1.3-contributor"
	if _, err := c.PlanTurn(ctx, other); provider.CodeOf(err) != provider.CodeModelNotApproved {
		t.Errorf("another model: %v", err)
	}
}
