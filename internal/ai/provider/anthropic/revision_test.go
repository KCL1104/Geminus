package anthropic_test

import (
	"encoding/json/jsontext"
	"reflect"
	"testing"

	"github.com/kcl1104/geminus/internal/ai/provider"
)

// H-057: the Claude adapter sends a repair as the Messages API's
// conversation: the first user message, the model's previous answer as an
// assistant message of one text block (as it wrote it, before the
// projection was decoded), and the server's follow-up as a user message.
// Its quote holds the two turns' bytes too.
func TestARepairRequest(t *testing.T) {
	refused := `{"status":"proposed","summary":"Raise it.","assumptions":[],"risk_level":"low","confidence":0.9,` +
		`"changes":[{"op":"test","path":"/resources/0/id","value":"\"ict_tester\""},{"op":"replace","path":"/resources/0/capacity","value":"three"}],` +
		`"expected_effects":["The queue may fall; simulate to know."],"requires_confirmation":true}`
	s := newServer(t, map[string][]reply{keyA: {ok(refused), ok(answerJ)}})
	c, b := client(t, s, config(keyA))
	first, err := ask(t, c)
	if provider.CodeOf(err) != provider.CodeInvalidOutput || !first.Prior.Present() || string(first.Prior.Bytes()) != refused {
		t.Fatalf("a decode failure: %v, prior %v", err, first.Prior.Present())
	}
	p := prompt(t)
	p.Revision = &provider.Revision{Prior: first.Prior, Rules: "Fix every issue listed.",
		Blocks: []provider.Block{{Kind: provider.ServerData, Name: "SERVER_REVIEW", Content: jsontext.Value(`{"issues":[]}`)}}}
	if p, err = provider.Seal(p); err != nil {
		t.Fatal(err)
	}
	res, err := c.GenerateModelProposal(t.Context(), provider.ProposalContext{OrganizationID: org, Mode: provider.ModeEdit, Prompt: p,
		Attribution: provider.Attribution{TargetType: "ai_proposal", TargetID: "aip_01M4YDQK00TA00000000000001", RequestID: "req_01M4YDQK00TA00000000000001"}})
	if err != nil || res.Output.Status != "needs_input" {
		t.Fatalf("the repair: %+v %v", res.Output, err)
	}
	want := []any{
		map[string]any{"role": "user", "content": []any{map[string]any{"type": "text", "text": p.User()}}},
		map[string]any{"role": "assistant", "content": []any{map[string]any{"type": "text", "text": refused}}},
		map[string]any{"role": "user", "content": []any{map[string]any{"type": "text", "text": p.Revision.User()}}},
	}
	if got := s.bodies[1]["messages"]; !reflect.DeepEqual(got, want) {
		t.Errorf("messages %v", got)
	}
	q0, err := b.Quote(t.Context(), provider.Request{Prompt: prompt(t), OutputSchema: "proposal-output.schema.json"})
	if err != nil {
		t.Fatal(err)
	}
	q1, err := b.Quote(t.Context(), provider.Request{Prompt: p, OutputSchema: "proposal-output.schema.json"})
	if err != nil {
		t.Fatal(err)
	}
	if got, want := q1.MaxInputTokens-q0.MaxInputTokens, int64(len(refused)+len(p.Revision.User())); got != want {
		t.Errorf("the repair's quote is %d tokens over the first's, want %d", got, want)
	}
}
