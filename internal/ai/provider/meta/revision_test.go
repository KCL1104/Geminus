package meta_test

import (
	"encoding/json/jsontext"
	"reflect"
	"strings"
	"testing"

	"github.com/kcl1104/geminus/internal/ai/provider"
	"github.com/kcl1104/geminus/internal/ai/provider/meta"
)

// reviseSealed is p's repair (H-057) on prior, sealed.
func reviseSealed(t *testing.T, p provider.Prompt, prior provider.PriorAnswer) provider.Prompt {
	t.Helper()
	p.Revision = &provider.Revision{Prior: prior, Rules: "The server refused your previous answer. Fix every issue listed.",
		Blocks: []provider.Block{{Kind: provider.ServerData, Name: "SERVER_REVIEW",
			Content: jsontext.Value(`{"issues":[{"code":"ROUTING_INVALID","path":"/nodes/45","message":"the outgoing routing probabilities are not valid"}]}`)}}}
	p.TemplateVersion += "+repair-test"
	out, err := provider.Seal(p)
	if err != nil {
		t.Fatal(err)
	}
	return out
}

// H-057: the repair is one more Messages request: the first request's
// system text and user message (its images first), the model's previous
// answer as an assistant message of one text block (as the model wrote it,
// before the projection was decoded; no reasoning block, none was kept),
// and the server's follow-up as a user message of one text block; no tools.
// A text request is quoted with the two turns' bytes too, and a request
// with images is counted with them (count_tokens takes the same messages).
// An answer whose encoded place does not decode keeps the model's text for
// that repair; one from another model does not.
func TestARepairRequest(t *testing.T) {
	refused := `{"status":"proposed","summary":"Raise it.","assumptions":[],"risk_level":"low","confidence":0.9,` +
		`"changes":[{"op":"test","path":"/resources/0/id","value":"\"ict_tester\""},{"op":"replace","path":"/resources/0/capacity","value":"three"}],` +
		`"expected_effects":["The queue may fall; simulate to know."],"requires_confirmation":true}`
	s := newServer(t, map[string][]reply{messagesPath: {ok(refused), ok(answerJ)}})
	c, b := client(t, s)
	first, err := ask(t, c)
	if provider.CodeOf(err) != provider.CodeInvalidOutput || !first.Prior.Present() || string(first.Prior.Bytes()) != refused {
		t.Fatalf("a decode failure: %v, prior %v", err, first.Prior.Present())
	}
	p := reviseSealed(t, sealed(t), first.Prior)
	res, err := c.GenerateModelProposal(t.Context(), provider.ProposalContext{OrganizationID: org, Mode: provider.ModeEdit, Prompt: p, Attribution: attribution})
	if err != nil || res.Output.Status != "needs_input" {
		t.Fatalf("the repair: %+v %v", res.Output, err)
	}
	body := s.bodies[1]
	if _, ok := body["tools"]; ok {
		t.Error("the repair offers a tool")
	}
	want := []any{
		map[string]any{"role": "user", "content": []any{map[string]any{"type": "text", "text": p.User()}}},
		map[string]any{"role": "assistant", "content": []any{map[string]any{"type": "text", "text": refused}}},
		map[string]any{"role": "user", "content": []any{map[string]any{"type": "text", "text": p.Revision.User()}}},
	}
	if !reflect.DeepEqual(body["messages"], want) {
		t.Errorf("messages %v", body["messages"])
	}
	if want := []any{map[string]any{"type": "text", "text": p.System}}; !reflect.DeepEqual(body["system"], want) {
		t.Errorf("system %v", body["system"])
	}
	leaks(t, "the repair request", string(s.raw[1]))

	// The quote of a text repair holds both turns' bytes.
	q0, err := b.Quote(t.Context(), provider.Request{Prompt: sealed(t), OutputSchema: "proposal-output.schema.json"})
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

	// A request with images is counted with the repair's turns.
	img := pngImage(t, 64, 48)
	s = newServer(t, map[string][]reply{countPath: {counted("3000")}, messagesPath: {ok(answerJ)}})
	c, _ = client(t, s)
	ip := reviseSealed(t, sealed(t, img), first.Prior)
	if _, err := c.GenerateModelProposal(t.Context(), provider.ProposalContext{OrganizationID: org, Mode: provider.ModeEdit, Prompt: ip,
		Attribution: attribution}); err != nil {
		t.Fatal(err)
	}
	if order := s.order(); len(order) != 2 || order[0] != countPath || order[1] != messagesPath {
		t.Fatalf("order %v", order)
	}
	for i, what := range []string{"count", "messages"} {
		msgs, _ := s.bodies[i]["messages"].([]any)
		if len(msgs) != 3 || msgs[1].(map[string]any)["role"] != "assistant" || msgs[2].(map[string]any)["role"] != "user" {
			t.Errorf("%s: %d messages", what, len(msgs))
			continue
		}
		content, _ := msgs[0].(map[string]any)["content"].([]any)
		if len(content) != 2 || content[0].(map[string]any)["type"] != "image" {
			t.Errorf("%s: the first turn lost its image", what)
		}
	}

	// An answer from another model than the one asked for is not sent back.
	other := strings.Replace(ok(refused).body, `"model":"`+model+`"`, `"model":"contributor-model"`, 1)
	s = newServer(t, map[string][]reply{messagesPath: {{status: 200, header: map[string]string{"Content-Type": "application/json"}, body: other}}})
	c, _ = client(t, s)
	if res, err := ask(t, c); provider.CodeOf(err) != provider.CodeInvalidOutput || res.Prior.Present() {
		t.Errorf("another model's answer: %v, prior %v", err, res.Prior.Present())
	}
	// A truncated answer is never sent back.
	s = newServer(t, map[string][]reply{messagesPath: {message("max_tokens", `[{"type":"text","text":"{\"status\":"}]`, usageJ)}})
	c, _ = client(t, s)
	if res, err := ask(t, c); provider.CodeOf(err) != provider.CodeTruncated || res.Prior.Present() {
		t.Errorf("a truncated answer: %v, prior %v", err, res.Prior.Present())
	}
	_ = meta.QuoteOverheadTokens
}

// H-057: a research answer's prior is its answer only, the last text block:
// never the narration or a web search step. Its repair is no research call.
func TestTheRepairOfAResearchAnswer(t *testing.T) {
	content := `[{"type":"text","text":"NARRATION-SENTINEL"},{"type":"server_tool_use","id":"s1","name":"web_search","input":{"type":"search","query":"smt"}},` +
		`{"type":"text","text":` + string(mustQuote(answerJ)) + `}]`
	s := newServer(t, map[string][]reply{messagesPath: {message("end_turn", content, usageJ), ok(answerJ)}})
	c, _ := client(t, s)
	rp := sealed(t)
	rp.Research = true
	rp, err := provider.Seal(rp)
	if err != nil {
		t.Fatal(err)
	}
	res, err := c.GenerateModelProposal(t.Context(), provider.ProposalContext{OrganizationID: org, Mode: provider.ModeEdit, Prompt: rp, Attribution: attribution})
	if err != nil || string(res.Prior.Bytes()) != answerJ {
		t.Fatalf("the research answer's prior: %v", err)
	}
	first := sealed(t)
	p := first
	p.Revision = &provider.Revision{Prior: res.Prior, Rules: "Fix it.", Researched: true,
		Blocks: []provider.Block{{Kind: provider.ServerData, Name: "SERVER_REVIEW", Content: jsontext.Value(`{"issues":[]}`)}}}
	if p, err = provider.Seal(p); err != nil {
		t.Fatal(err)
	}
	if _, err := c.GenerateModelProposal(t.Context(), provider.ProposalContext{OrganizationID: org, Mode: provider.ModeEdit, Prompt: p,
		Attribution: attribution}); err != nil {
		t.Fatal(err)
	}
	body := s.bodies[1]
	if _, ok := body["tools"]; ok || body["max_tokens"] != float64(provider.ResearchMaxOutputTokens) {
		t.Errorf("the repair of a research answer: tools %v, max_tokens %v", body["tools"], body["max_tokens"])
	}
	leaks(t, "the repair request", string(s.raw[1]), "NARRATION-SENTINEL")
}
