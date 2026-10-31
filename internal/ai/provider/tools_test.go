package provider

import (
	"context"
	"encoding/json/jsontext"
	"errors"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/kcl1104/geminus/internal/ai/spend"
)

// H-060, slice E1: a tool turn offers server-defined tools; only a
// ToolTurner backend that accepts them for the role is handed one, and its
// answer's calls are checked against the tools strictly.

// turner is a test ToolTurner: it counts its calls and answers with respond.
type turner struct {
	accepts bool
	calls   atomic.Int32
	reqs    []Request
	respond func(context.Context, Request) (Response, error)
}

func (b *turner) Name() string { return "test-turner" }
func (b *turner) Complete(context.Context, Request) (Response, error) {
	return Response{}, errors.New("a tool turn went to Complete")
}
func (b *turner) AcceptsTools(Role) bool { return b.accepts }
func (b *turner) Turn(ctx context.Context, req Request) (Response, error) {
	b.calls.Add(1)
	b.reqs = append(b.reqs, req)
	return b.respond(ctx, req)
}

// billableTurner is a metered ToolTurner.
type billableTurner struct {
	turner
	quote Quote
	asked []Request
}

func (b *billableTurner) Quote(_ context.Context, req Request) (Quote, error) {
	b.asked = append(b.asked, req)
	return b.quote, nil
}

const proposeSchema = `{"type":"object","properties":{"idea":{"type":"string","minLength":1,"maxLength":2000},` +
	`"from_suggestion":{"anyOf":[{"type":"null"},{"type":"object","properties":{"analysis_id":{"type":"string"},` +
	`"index":{"type":"integer","minimum":0,"maximum":9}},"required":["analysis_id","index"],"additionalProperties":false}]}},` +
	`"required":["idea","from_suggestion"],"additionalProperties":false}`

const finishSchema = `{"type":"object","properties":{"outcome":{"enum":["improvement_found","no_improvement"]}},"required":["outcome"],"additionalProperties":false}`

func testTools() []Tool {
	return []Tool{
		{Name: "propose_what_if", Description: "Propose one what-if in words.", InputSchema: jsontext.Value(proposeSchema)},
		{Name: "finish", Description: "End the investigation.", InputSchema: jsontext.Value(finishSchema)},
	}
}

func turnCtx(t *testing.T, steps ...ToolStep) TurnContext {
	return TurnContext{OrganizationID: orgA, Prompt: prompt(t, RoleScenario), Attribution: attribution, Tools: testTools(), Steps: steps}
}

func calls(cs ...ToolCall) func(context.Context, Request) (Response, error) {
	return func(context.Context, Request) (Response, error) {
		return Response{Model: "test-model", Credential: "org-a", ToolCalls: cs}, nil
	}
}

const ideaJ = `{"idea":"add a second ICT tester","from_suggestion":null}`

func call(id, name, input string) ToolCall {
	return ToolCall{ID: id, Name: name, Input: jsontext.Value(input)}
}

// A done step: one call and the server's result of it.
func doneStep(id string) ToolStep {
	return ToolStep{Text: "I will propose it.", Calls: []ToolCall{call(id, "propose_what_if", ideaJ)},
		Results: []ToolResult{{CallID: id, Content: jsontext.Value(`{"proposal_id":"aip_1","status":"ready"}`)}}}
}

// A turn is handed only to a ToolTurner that accepts tools for the role,
// and only when it has no web search, image or repair: refused before the
// backend is asked otherwise.
func TestTurnBackendGates(t *testing.T) {
	ctx := context.Background()
	if _, err := client(t, answer(`{}`)).Turn(ctx, turnCtx(t)); CodeOf(err) != CodeToolsUnsupported {
		t.Errorf("a backend that sends no tools: %v", err)
	}
	if _, err := client(t, Disabled{}).Turn(ctx, turnCtx(t)); CodeOf(err) != CodeNotConfigured {
		t.Errorf("disabled: %v", err)
	}
	b := &turner{accepts: false, respond: calls(call("c1", "finish", `{"outcome":"no_improvement"}`))}
	if _, err := client(t, b).Turn(ctx, turnCtx(t)); CodeOf(err) != CodePolicyNotApproved {
		t.Errorf("a backend that refuses tools for the role: %v", err)
	}
	b.accepts = true
	research := turnCtx(t)
	research.Prompt = researchPrompt(t, RoleScenario)
	repair := turnCtx(t)
	repair.Prompt = revisedPrompt(t)
	for name, in := range map[string]TurnContext{"research": research, "repair": repair} {
		if _, err := client(t, b).Turn(ctx, in); CodeOf(err) != CodeInvalidRequest {
			t.Errorf("%s: %v", name, err)
		}
	}
	if b.calls.Load() != 0 {
		t.Errorf("the backend was asked %d times", b.calls.Load())
	}
	if c := client(t, b); !c.AcceptsTools(RoleScenario) || client(t, answer(`{}`)).AcceptsTools(RoleScenario) {
		t.Error("AcceptsTools does not follow the backend")
	}
}

func revisedPrompt(t *testing.T) Prompt {
	t.Helper()
	p := prompt(t, RoleScenario)
	p.Revision = &Revision{Prior: PriorAnswer{text: []byte(`{}`)}, Rules: "fix it",
		Blocks: []Block{{Kind: ServerData, Name: "SERVER_REVIEW", Content: jsontext.Value(`{"issues":[]}`)}}}
	out, err := Seal(p)
	if err != nil {
		t.Fatal(err)
	}
	return out
}

// The tools and the transcript are checked before anything is sent: a tool
// whose schema could take a member it does not list, refers outside itself
// or is not an object's, a bad or repeated name, a description holding a
// block, too many tools; a transcript a model could not have made or the
// server did not answer. Each is invalid_request, and the backend is never
// asked.
func TestTurnRequestsTheClientRefuses(t *testing.T) {
	tool := func(name, schema string) []Tool {
		return []Tool{{Name: name, Description: "d", InputSchema: jsontext.Value(schema)}}
	}
	many := make([]Tool, MaxTools+1)
	for i := range many {
		many[i] = Tool{Name: "t" + strings.Repeat("a", i+1), InputSchema: jsontext.Value(finishSchema)}
	}
	deep := `{"type":"object","properties":{"a":` + strings.Repeat(`{"type":"array","items":`, maxToolSchemaDepth+1) + `{"type":"string"}` +
		strings.Repeat(`}`, maxToolSchemaDepth+1) + `},"additionalProperties":false}`
	cases := map[string]func(*TurnContext){
		"no tools":         func(in *TurnContext) { in.Tools = nil },
		"too many tools":   func(in *TurnContext) { in.Tools = many },
		"a repeated name":  func(in *TurnContext) { in.Tools = append(testTools(), testTools()[0]) },
		"a bad name":       func(in *TurnContext) { in.Tools = tool("Run-Simulation", finishSchema) },
		"a name too long":  func(in *TurnContext) { in.Tools = tool("t"+strings.Repeat("a", 64), finishSchema) },
		"a block in rules": func(in *TurnContext) { in.Tools[0].Description = `<untrusted_data name="X">` },
		"a long description": func(in *TurnContext) {
			in.Tools[0].Description = strings.Repeat("d", MaxToolDescriptionBytes+1)
		},
		"no schema": func(in *TurnContext) { in.Tools = tool("t", "") },
		"not JSON":  func(in *TurnContext) { in.Tools = tool("t", `{"type":`) },
		"duplicate members": func(in *TurnContext) {
			in.Tools = tool("t", `{"type":"object","type":"object","additionalProperties":false}`)
		},
		"not an object's":  func(in *TurnContext) { in.Tools = tool("t", `{"type":"string"}`) },
		"open at the root": func(in *TurnContext) { in.Tools = tool("t", `{"type":"object","properties":{}}`) },
		"additional schema": func(in *TurnContext) {
			in.Tools = tool("t", `{"type":"object","additionalProperties":{"type":"string"}}`)
		},
		"open nested object": func(in *TurnContext) {
			in.Tools = tool("t", `{"type":"object","properties":{"a":{"type":"object"}},"additionalProperties":false}`)
		},
		"open in a branch": func(in *TurnContext) {
			in.Tools = tool("t", `{"type":"object","properties":{"a":{"anyOf":[{"type":"null"},{"type":"object"}]}},"additionalProperties":false}`)
		},
		"open array items": func(in *TurnContext) {
			in.Tools = tool("t", `{"type":"object","properties":{"a":{"type":"array","items":{"properties":{}}}},"additionalProperties":false}`)
		},
		"a true schema": func(in *TurnContext) {
			in.Tools = tool("t", `{"type":"object","properties":{"a":true},"additionalProperties":false}`)
		},
		"an empty schema": func(in *TurnContext) {
			in.Tools = tool("t", `{"type":"object","properties":{"a":{}},"additionalProperties":false}`)
		},
		"pattern properties": func(in *TurnContext) {
			in.Tools = tool("t", `{"type":"object","patternProperties":{"^x":{"type":"string"}},"additionalProperties":false}`)
		},
		"unevaluated": func(in *TurnContext) { in.Tools = tool("t", `{"type":"object","unevaluatedProperties":false}`) },
		"a remote reference": func(in *TurnContext) {
			in.Tools = tool("t", `{"type":"object","properties":{"a":{"$ref":"https://example.com/s.json"}},"additionalProperties":false}`)
		},
		"a reference out of $defs": func(in *TurnContext) {
			in.Tools = tool("t", `{"type":"object","properties":{"a":{"$ref":"#/properties"}},"additionalProperties":false}`)
		},
		"a dangling reference": func(in *TurnContext) {
			in.Tools = tool("t", `{"type":"object","properties":{"a":{"$ref":"#/$defs/x"}},"additionalProperties":false}`)
		},
		"another draft": func(in *TurnContext) {
			in.Tools = tool("t", `{"$schema":"http://json-schema.org/draft-07/schema#","type":"object","additionalProperties":false}`)
		},
		"too deep": func(in *TurnContext) { in.Tools = tool("t", deep) },
		"too large": func(in *TurnContext) {
			in.Tools = tool("t", `{"type":"object","description":"`+strings.Repeat("x", MaxToolSchemaBytes)+`","additionalProperties":false}`)
		},
		"an invalid schema": func(in *TurnContext) {
			in.Tools = tool("t", `{"type":"object","properties":{"a":{"type":"strng"}},"additionalProperties":false}`)
		},
		"too many steps": func(in *TurnContext) {
			for i := range MaxToolSteps + 1 {
				in.Steps = append(in.Steps, doneStep("c"+strings.Repeat("x", i+1)))
			}
		},
		"a step without calls": func(in *TurnContext) { in.Steps = []ToolStep{{Text: "hm"}} },
		"a call of no tool offered": func(in *TurnContext) {
			s := doneStep("c1")
			s.Calls[0].Name = "publish_version"
			in.Steps = []ToolStep{s}
		},
		"a call the schema refuses": func(in *TurnContext) {
			s := doneStep("c1")
			s.Calls[0].Input = jsontext.Value(`{"idea":"x","from_suggestion":null,"publish":true}`)
			in.Steps = []ToolStep{s}
		},
		"a call ID of no token's shape": func(in *TurnContext) {
			s := doneStep("c 1")
			in.Steps = []ToolStep{s}
		},
		"a call ID repeated": func(in *TurnContext) { in.Steps = []ToolStep{doneStep("c1"), doneStep("c1")} },
		"a result of another call": func(in *TurnContext) {
			s := doneStep("c1")
			s.Results[0].CallID = "c2"
			in.Steps = []ToolStep{s}
		},
		"a call without its result": func(in *TurnContext) {
			s := doneStep("c1")
			s.Results = nil
			in.Steps = []ToolStep{s}
		},
		"a result that is not JSON": func(in *TurnContext) {
			s := doneStep("c1")
			s.Results[0].Content = jsontext.Value(`observation: ok`)
			in.Steps = []ToolStep{s}
		},
		"a result too large": func(in *TurnContext) {
			s := doneStep("c1")
			s.Results[0].Content = jsontext.Value(`"` + strings.Repeat("x", MaxToolResultBytes) + `"`)
			in.Steps = []ToolStep{s}
		},
		"unsealed": func(in *TurnContext) { in.Prompt.InputHash = "" },
		"changed after sealing": func(in *TurnContext) {
			in.Prompt.System = "other rules"
		},
		"another organization": func(in *TurnContext) { in.OrganizationID = orgB },
	}
	for name, edit := range cases {
		b := &turner{accepts: true, respond: calls(call("c9", "finish", `{"outcome":"no_improvement"}`))}
		in := turnCtx(t)
		edit(&in)
		_, err := client(t, b).Turn(context.Background(), in)
		if CodeOf(err) != CodeInvalidRequest {
			t.Errorf("%s: %v, want invalid_request", name, err)
		}
		if b.calls.Load() != 0 {
			t.Errorf("%s: the backend was asked", name)
		}
	}
	// The tools and the transcript count towards the prompt budget.
	b := &turner{accepts: true, respond: calls()}
	in := turnCtx(t, doneStep("c1"))
	small := func(budget *Budget) { budget.MaxPromptBytes = len(in.Prompt.System) + len(in.Prompt.User()) + 10 }
	if _, err := client(t, b, small).Turn(context.Background(), in); CodeOf(err) != CodeContextTooLarge || b.calls.Load() != 0 {
		t.Errorf("tools over the prompt budget: %v", err)
	}
}

// A turn is answered with text, calls of the tools it offered, or both: the
// client hands back the text and each call with its arguments canonical,
// the backend gets the tools and transcript canonical, and the metadata has
// the turn's own input hash (the prompt's, the tools' and the
// transcript's), an output hash and no output schema.
func TestTurnAnswers(t *testing.T) {
	ctx := context.Background()
	b := &turner{accepts: true, respond: func(context.Context, Request) (Response, error) {
		return Response{Model: "test-model", Text: []byte("Plain text, no call.")}, nil
	}}
	c := client(t, b)
	res, err := c.Turn(ctx, turnCtx(t))
	if err != nil || res.Text != "Plain text, no call." || len(res.Calls) != 0 {
		t.Fatalf("a plain answer: %+v %v", res, err)
	}
	p := prompt(t, RoleScenario)
	if m := res.Metadata; m.InputHash == "" || m.InputHash == p.InputHash || m.OutputHash == "" || m.OutputSchema != "" ||
		m.Role != RoleScenario || m.Provider != "test-turner" {
		t.Errorf("metadata %+v", m)
	}
	req := b.reqs[0]
	if len(req.Tools) != 2 || req.OutputSchema != "" || len(req.Steps) != 0 || req.MaxOutputTokens != DefaultBudget().MaxOutputTokens {
		t.Errorf("request %+v", req)
	}

	// A call, with whitespace and members in another order: canonical.
	b.respond = calls(ToolCall{ID: "toolu_01", Name: "propose_what_if", Input: jsontext.Value(" {\"from_suggestion\" : null, \"idea\":\"add a second ICT tester\"}")})
	res, err = c.Turn(ctx, turnCtx(t))
	if err != nil || len(res.Calls) != 1 || res.Calls[0].ID != "toolu_01" || res.Calls[0].Name != "propose_what_if" ||
		string(res.Calls[0].Input) != `{"from_suggestion":null,"idea":"add a second ICT tester"}` {
		t.Fatalf("a call: %+v %v", res, err)
	}

	// A turn after a step: the transcript goes to the backend canonical, and
	// the input hash covers it.
	step := doneStep("toolu_01")
	step.Calls[0].Input = jsontext.Value(" {\"idea\":\"add a second ICT tester\", \"from_suggestion\":null}")
	step.Results[0].Content = jsontext.Value(` {"status":"ready", "proposal_id":"aip_1"}`)
	b.respond = calls(call("toolu_02", "finish", `{"outcome":"improvement_found"}`))
	after, err := c.Turn(ctx, turnCtx(t, step))
	if err != nil || len(after.Calls) != 1 || after.Calls[0].Name != "finish" {
		t.Fatalf("a turn after a step: %+v %v", after, err)
	}
	sent := b.reqs[len(b.reqs)-1].Steps
	if len(sent) != 1 || string(sent[0].Calls[0].Input) != `{"from_suggestion":null,"idea":"add a second ICT tester"}` ||
		string(sent[0].Results[0].Content) != `{"proposal_id":"aip_1","status":"ready"}` || sent[0].Text != step.Text {
		t.Errorf("the transcript sent: %+v", sent)
	}
	if after.Metadata.InputHash == res.Metadata.InputHash {
		t.Error("the input hash does not cover the transcript")
	}
	// The same turn hashes the same; another tool list does not.
	again, _ := c.Turn(ctx, turnCtx(t, step))
	other := turnCtx(t, step)
	other.Tools[1].Description = "End it."
	b.respond = calls(call("toolu_02", "finish", `{"outcome":"improvement_found"}`))
	changed, _ := c.Turn(ctx, other)
	if again.Metadata.InputHash != after.Metadata.InputHash || changed.Metadata.InputHash == after.Metadata.InputHash {
		t.Error("the input hash is not the turn's")
	}
	// The call ID of a step may not come back as a new call.
	b.respond = calls(call("toolu_01", "finish", `{"outcome":"improvement_found"}`))
	if _, err := c.Turn(ctx, turnCtx(t, step)); CodeOf(err) != CodeInvalidOutput {
		t.Errorf("a call ID of the transcript again: %v", err)
	}
}

// Every call is checked strictly: an offered tool's name, an ID of a
// token's shape not repeated, arguments that are one strict JSON object
// within the bound and valid under the tool's schema (no unknown member, no
// missing one, no wrong type), at most MaxToolCallsPerAnswer calls, and an
// answer that is not empty. A refused answer is invalid_output with where it
// is wrong, never what the model wrote.
func TestTurnCallsAreCheckedStrictly(t *testing.T) {
	const sentinel = "ARGUMENT-SENTINEL"
	big := `{"idea":"` + strings.Repeat("x", MaxToolArgumentBytes) + `","from_suggestion":null}`
	five := []ToolCall{}
	for i := range MaxToolCallsPerAnswer + 1 {
		five = append(five, call("c"+strings.Repeat("x", i+1), "finish", `{"outcome":"no_improvement"}`))
	}
	cases := map[string]struct {
		calls []ToolCall
		text  string
		path  string
	}{
		"an unknown member":      {calls: []ToolCall{call("c1", "propose_what_if", `{"idea":"x","from_suggestion":null,"`+sentinel+`":1}`)}, path: "/tool_calls/0/input"},
		"a nested unknown":       {calls: []ToolCall{call("c1", "propose_what_if", `{"idea":"x","from_suggestion":{"analysis_id":"a","index":1,"`+sentinel+`":1}}`)}, path: "/tool_calls/0/input/from_suggestion"},
		"a missing member":       {calls: []ToolCall{call("c1", "propose_what_if", `{"idea":"x"}`)}, path: "/tool_calls/0/input"},
		"a wrong type":           {calls: []ToolCall{call("c1", "propose_what_if", `{"idea":7,"from_suggestion":null}`)}, path: "/tool_calls/0/input/idea"},
		"out of its bounds":      {calls: []ToolCall{call("c1", "propose_what_if", `{"idea":"","from_suggestion":null}`)}, path: "/tool_calls/0/input/idea"},
		"not in its enum":        {calls: []ToolCall{call("c1", "finish", `{"outcome":"`+sentinel+`"}`)}, path: "/tool_calls/0/input/outcome"},
		"duplicate members":      {calls: []ToolCall{call("c1", "finish", `{"outcome":"no_improvement","outcome":"improvement_found"}`)}, path: "/tool_calls/0/input"},
		"not an object":          {calls: []ToolCall{call("c1", "finish", `["no_improvement"]`)}, path: "/tool_calls/0/input"},
		"not JSON":               {calls: []ToolCall{call("c1", "finish", `{"outcome":`)}, path: "/tool_calls/0/input"},
		"two values":             {calls: []ToolCall{call("c1", "finish", `{"outcome":"no_improvement"}{}`)}, path: "/tool_calls/0/input"},
		"a number JSON can't":    {calls: []ToolCall{call("c1", "propose_what_if", `{"idea":"x","from_suggestion":{"analysis_id":"a","index":1e400}}`)}, path: "/tool_calls/0/input"},
		"invalid UTF-8":          {calls: []ToolCall{call("c1", "finish", "{\"outcome\":\"\xff\"}")}, path: "/tool_calls/0/input"},
		"no arguments":           {calls: []ToolCall{{ID: "c1", Name: "finish"}}, path: "/tool_calls/0/input"},
		"arguments too large":    {calls: []ToolCall{call("c1", "propose_what_if", big)}, path: "/tool_calls/0/input"},
		"a tool not offered":     {calls: []ToolCall{call("c1", "publish_version", `{}`)}, path: "/tool_calls/0/name"},
		"an ID of no token":      {calls: []ToolCall{call("c1\n", "finish", `{"outcome":"no_improvement"}`)}, path: "/tool_calls/0/id"},
		"no ID":                  {calls: []ToolCall{call("", "finish", `{"outcome":"no_improvement"}`)}, path: "/tool_calls/0/id"},
		"an ID repeated":         {calls: []ToolCall{call("c1", "finish", `{"outcome":"no_improvement"}`), call("c1", "finish", `{"outcome":"no_improvement"}`)}, path: "/tool_calls/1/id"},
		"the second call wrong":  {calls: []ToolCall{call("c1", "finish", `{"outcome":"no_improvement"}`), call("c2", "finish", `{}`)}, path: "/tool_calls/1/input"},
		"too many calls":         {calls: five, path: "/tool_calls"},
		"no text and no call":    {text: "  \n", path: ""},
		"text that is not UTF-8": {text: "\xff", path: ""},
	}
	for name, tc := range cases {
		b := &turner{accepts: true, respond: func(context.Context, Request) (Response, error) {
			return Response{Model: "test-model", Text: []byte(tc.text), ToolCalls: tc.calls}, nil
		}}
		res, err := client(t, b).Turn(context.Background(), turnCtx(t))
		var pe *Error
		if !errors.As(err, &pe) || pe.Code != CodeInvalidOutput || len(pe.Issues) == 0 || pe.Issues[0].Path != tc.path {
			t.Errorf("%s: %+v %v, want invalid_output at %q", name, res, err, tc.path)
			continue
		}
		if len(res.Calls) != 0 || res.Text != "" {
			t.Errorf("%s: a refused answer handed back %+v", name, res)
		}
		for _, is := range pe.Issues {
			if strings.Contains(is.Message, sentinel) || strings.Contains(is.Path, sentinel) || strings.Contains(err.Error(), sentinel) {
				t.Errorf("%s: the issue carries what the model wrote: %+v", name, is)
			}
		}
	}
	// The bounds themselves pass: MaxToolCallsPerAnswer calls, arguments of
	// exactly MaxToolArgumentBytes.
	b := &turner{accepts: true, respond: calls(five[:MaxToolCallsPerAnswer]...)}
	if res, err := client(t, b).Turn(context.Background(), turnCtx(t)); err != nil || len(res.Calls) != MaxToolCallsPerAnswer {
		t.Errorf("%d calls: %v", MaxToolCallsPerAnswer, err)
	}
	padded := `{"idea":"x","from_suggestion":null}` + strings.Repeat(" ", MaxToolArgumentBytes-len(`{"idea":"x","from_suggestion":null}`))
	b.respond = calls(call("c1", "propose_what_if", padded))
	if _, err := client(t, b).Turn(context.Background(), turnCtx(t)); err != nil {
		t.Errorf("arguments of exactly the bound: %v", err)
	}
}

// Every existing role's request offers no tool, and a tool call in its
// answer is refused (invalid_output), whatever else the answer holds.
func TestRolesRefuseToolCalls(t *testing.T) {
	b := backendFunc(func(context.Context, Request) (Response, error) {
		return Response{Model: "test-model", Text: []byte(`{"status":"needs_input","summary":"s","assumptions":[],"questions":["q"],"requires_confirmation":true}`),
			ToolCalls: []ToolCall{call("c1", "finish", `{"outcome":"no_improvement"}`)}}, nil
	})
	c := client(t, b)
	if _, err := proposal(t, c); CodeOf(err) != CodeInvalidOutput {
		t.Errorf("a proposal answer with a tool call: %v", err)
	}
	var seen []Request
	b2 := backendFunc(func(_ context.Context, req Request) (Response, error) {
		seen = append(seen, req)
		return Response{Model: "test-model", Text: []byte(`{"status":"needs_input","summary":"s","assumptions":[],"questions":["q"],"requires_confirmation":true}`)}, nil
	})
	if _, err := proposal(t, client(t, b2)); err != nil {
		t.Fatal(err)
	}
	if len(seen) != 1 || seen[0].Tools != nil || seen[0].Steps != nil || seen[0].OutputSchema != "proposal-output.schema.json" {
		t.Errorf("a proposal request: %+v", seen)
	}
}

// A metered turn reserves before it is sent, from the backend's quote of
// the turn (the tools and transcript included), at 1 + the budget's
// retries, and settles what it cost after; a turn refused before it is sent
// reserves nothing. The metadata carries what logCall and the audit read.
func TestTurnIsMetered(t *testing.T) {
	m := &meter{}
	b := &billableTurner{turner: turner{accepts: true, respond: func(context.Context, Request) (Response, error) {
		return Response{Model: pricedModel, Credential: "org-a", Usage: Usage{InputTokens: 900, OutputTokens: 60}, InferenceGeo: spend.GeoGlobal,
			ProviderRequestID: "req_1", ToolCalls: []ToolCall{call("c1", "finish", `{"outcome":"no_improvement"}`)}}, nil
	}}, quote: Quote{Model: pricedModel, MaxInputTokens: 10_000}}
	c := metered(t, b, m)
	res, err := c.Turn(context.Background(), turnCtx(t, doneStep("c0")))
	if err != nil {
		t.Fatal(err)
	}
	if len(m.charges) != 1 || len(m.settled) != 1 || len(b.asked) != 1 || len(b.asked[0].Steps) != 1 || len(b.asked[0].Tools) != 2 {
		t.Fatalf("charges %v settled %v quoted %d", m.charges, m.settled, len(b.asked))
	}
	ch := m.charges[0]
	pr := price(t)
	if want := spend.AtDearestGeo(pr.AttemptBound(10_000, DefaultBudget().MaxOutputTokens)) * int64(1+DefaultBudget().MaxRetries); ch.NanoUSD != want ||
		ch.Role != RoleScenario || ch.Attribution != attribution || ch.OrganizationID != orgA {
		t.Errorf("charge %+v, want %d", ch, want)
	}
	if b.reqs[0].Reservation != "r1" {
		t.Errorf("the turn was sent without its reservation: %q", b.reqs[0].Reservation)
	}
	md := res.Metadata
	if md.SpendReservation != "r1" || md.CostNanoUSD <= 0 || md.CostNanoUSD != m.settled[0].NanoUSD || md.Usage.InputTokens != 900 ||
		md.Model != pricedModel || md.Credential != "org-a" || md.ProviderRequestID != "req_1" || md.LatencyMillis < 0 {
		t.Errorf("metadata %+v", md)
	}
	// Refused before it is sent: nothing reserved.
	in := turnCtx(t)
	in.Attribution = Attribution{}
	if _, err := c.Turn(context.Background(), in); CodeOf(err) != CodeInvalidRequest || len(m.charges) != 1 {
		t.Errorf("an unattributed turn: %v, %d charges", err, len(m.charges))
	}
	// A refused answer is settled all the same.
	b.respond = func(context.Context, Request) (Response, error) {
		return Response{Model: pricedModel, Credential: "org-a", Usage: Usage{InputTokens: 900, OutputTokens: 60}, InferenceGeo: spend.GeoGlobal,
			ToolCalls: []ToolCall{call("c1", "publish_version", `{}`)}}, nil
	}
	res, err = c.Turn(context.Background(), turnCtx(t))
	if CodeOf(err) != CodeInvalidOutput || len(m.settled) != 2 || m.settled[1].NanoUSD <= 0 || res.Metadata.CostNanoUSD != m.settled[1].NanoUSD {
		t.Errorf("a refused answer: %v, settled %v", err, m.settled)
	}
	// An answer from another model than the one asked for is refused.
	in = turnCtx(t)
	in.Model = "muse-wonder-1.0"
	if _, err := c.Turn(context.Background(), in); CodeOf(err) != CodeModelNotApproved {
		t.Errorf("a model the backend cannot ask: %v", err)
	}
}

// Cancellation reaches the turn, and is settled like any call's.
func TestTurnCancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	m := &meter{}
	b := &billableTurner{turner: turner{accepts: true, respond: func(ctx context.Context, _ Request) (Response, error) {
		cancel()
		<-ctx.Done()
		return Response{Model: pricedModel, UnknownAttempts: 1}, NewError(CodeCancelled, 0, "").WithCause(ctx.Err())
	}}, quote: Quote{Model: pricedModel, MaxInputTokens: 10_000}}
	_, err := metered(t, b, m).Turn(ctx, turnCtx(t))
	if CodeOf(err) != CodeCancelled || !errors.Is(err, context.Canceled) || len(m.settled) != 1 || m.settled[0].Outcome != SpendUnknown {
		t.Errorf("a cancelled turn: %v, settled %+v", err, m.settled)
	}
	if m.ctxErr[0] != nil {
		t.Errorf("settled under a cancelled context: %v", m.ctxErr[0])
	}
}
