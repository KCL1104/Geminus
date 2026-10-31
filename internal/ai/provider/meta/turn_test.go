package meta_test

import (
	"context"
	"encoding/json"
	"encoding/json/jsontext"
	"maps"
	"reflect"
	"slices"
	"testing"

	"github.com/kcl1104/geminus/internal/ai/provider"
	"github.com/kcl1104/geminus/internal/ai/provider/meta"
)

var _ provider.ToolTurner = (*meta.Backend)(nil)

const runSchema = `{"type":"object","properties":{"scenario_id":{"type":"string","description":"The scenario to run."}},"required":["scenario_id"],"additionalProperties":false}`

func runTool() provider.Tool {
	return provider.Tool{Name: "run_what_if", Description: "Run a saved what-if against the baseline.", InputSchema: jsontext.Value(runSchema)}
}

func turnContext(t *testing.T, steps ...provider.ToolStep) provider.TurnContext {
	return provider.TurnContext{OrganizationID: org, Prompt: roleSealed(t, provider.RoleScenario, "USER_INTENT", "find a better shift plan"),
		Attribution: attribution, Tools: []provider.Tool{runTool()}, Steps: steps}
}

// toolUse is a tool_use answer block.
func toolUse(id, name, input string) map[string]any {
	var in any
	if err := json.Unmarshal([]byte(input), &in); err != nil {
		panic(err)
	}
	return map[string]any{"type": "tool_use", "id": id, "name": name, "input": in}
}

// turnAnswer is a Messages answer of blocks with stop.
func turnAnswer(stop string, blocks ...map[string]any) reply {
	content, _ := json.Marshal(blocks)
	return message(stop, string(content), usageJ)
}

var thinking = map[string]any{"type": "redacted_thinking", "data": "PRIVATE-REASONING-SENTINEL"}

// The tool turn is POST /v1/messages with model, max_tokens, system, the
// user message, each step as an assistant message (its text, then its
// tool_use blocks) and a user message of its tool_result blocks, and tools
// with input_schema: no output_config (the answer is calls), no tool_choice,
// no thinking or other member. Its answer's tool_use blocks (stop_reason
// tool_use) come back as calls, checked by the client; its reasoning is
// dropped unread and never sent back.
func TestTurnRequestShape(t *testing.T) {
	step := provider.ToolStep{Text: "I will run it.", Calls: []provider.ToolCall{{ID: "toolu_01", Name: "run_what_if", Input: jsontext.Value(`{"scenario_id":"scn_1"}`)}},
		Results: []provider.ToolResult{{CallID: "toolu_01", Content: jsontext.Value(`{"status":"failed","code":"RUN_FAILED"}`), IsError: true}}}
	s := newServer(t, map[string][]reply{messagesPath: {turnAnswer("tool_use", thinking, text("Running the next one."), toolUse("toolu_02", "run_what_if", `{"scenario_id":"scn_2"}`))}})
	c, b := client(t, s, func(bu *provider.Budget) { bu.MaxOutputTokens = 4321 })
	meta.SetTools(b, "scenario")
	in := turnContext(t, step)
	res, err := c.Turn(context.Background(), in)
	if err != nil {
		t.Fatal(err)
	}
	if res.Text != "Running the next one." || len(res.Calls) != 1 || res.Calls[0].ID != "toolu_02" || string(res.Calls[0].Input) != `{"scenario_id":"scn_2"}` {
		t.Errorf("the answer %+v", res)
	}
	if s.order()[0] != messagesPath || s.headers[0].Get("Authorization") != "Bearer "+key {
		t.Errorf("sent %v", s.order())
	}
	body := s.bodies[0]
	if keys := slices.Sorted(maps.Keys(body)); !slices.Equal(keys, []string{"max_tokens", "messages", "model", "system", "tools"}) {
		t.Errorf("top-level fields %v", keys)
	}
	if body["model"] != model || body["max_tokens"] != float64(4321) {
		t.Errorf("model %v max_tokens %v", body["model"], body["max_tokens"])
	}
	wantTools := []any{map[string]any{"name": "run_what_if", "description": "Run a saved what-if against the baseline.",
		"input_schema": map[string]any{"type": "object", "properties": map[string]any{"scenario_id": map[string]any{"type": "string", "description": "The scenario to run."}},
			"required": []any{"scenario_id"}, "additionalProperties": false}}}
	if !reflect.DeepEqual(body["tools"], wantTools) {
		t.Errorf("tools %v", body["tools"])
	}
	wantMsgs := []any{
		map[string]any{"role": "user", "content": []any{map[string]any{"type": "text", "text": in.Prompt.User()}}},
		map[string]any{"role": "assistant", "content": []any{map[string]any{"type": "text", "text": "I will run it."},
			map[string]any{"type": "tool_use", "id": "toolu_01", "name": "run_what_if", "input": map[string]any{"scenario_id": "scn_1"}}}},
		map[string]any{"role": "user", "content": []any{map[string]any{"type": "tool_result", "tool_use_id": "toolu_01",
			"content": `{"code":"RUN_FAILED","status":"failed"}`, "is_error": true}}},
	}
	if !reflect.DeepEqual(body["messages"], wantMsgs) {
		t.Errorf("messages %v", body["messages"])
	}
	if want := []any{map[string]any{"type": "text", "text": in.Prompt.System}}; !reflect.DeepEqual(body["system"], want) {
		t.Errorf("system %v", body["system"])
	}
	leaks(t, "the turn request", string(s.raw[0]))
	if m := res.Metadata; m.Usage.InputTokens != 120 || m.Model != model || m.ProviderRequestID != "msg_test_1" || m.CostNanoUSD <= 0 || m.SpendReservation == "" {
		t.Errorf("metadata %+v", m)
	}
	// A plain answer (end_turn, no tool_use) is text.
	s2 := newServer(t, map[string][]reply{messagesPath: {turnAnswer("end_turn", thinking, text("Nothing more to try."))}})
	c2, b2 := client(t, s2)
	meta.SetTools(b2, "scenario")
	if res, err := c2.Turn(context.Background(), turnContext(t)); err != nil || res.Text != "Nothing more to try." || len(res.Calls) != 0 {
		t.Errorf("a plain answer: %+v %v", res, err)
	}
	if steps := s2.bodies[0]["messages"].([]any); len(steps) != 1 {
		t.Errorf("a first turn sends %d messages", len(steps))
	}
}

// A turn's quote is its body's bytes and the overhead, nothing asked of
// the provider: it grows with the transcript.
func TestTurnQuote(t *testing.T) {
	s := newServer(t, map[string][]reply{})
	b, _ := backend(t, s)
	meta.SetTools(b, "scenario")
	in := turnContext(t)
	req := provider.Request{Prompt: in.Prompt, Tools: in.Tools, MaxOutputTokens: 8192}
	q0, err := b.Quote(context.Background(), req)
	if err != nil || q0.Model != model || q0.MaxInputTokens <= int64(len(in.Prompt.System)+len(in.Prompt.User()))+meta.QuoteOverheadTokens {
		t.Fatalf("quote %+v %v", q0, err)
	}
	before := req.ToolBytes()
	req.Steps = []provider.ToolStep{{Calls: []provider.ToolCall{{ID: "toolu_01", Name: "run_what_if", Input: jsontext.Value(`{"scenario_id":"scn_1"}`)}},
		Results: []provider.ToolResult{{CallID: "toolu_01", Content: jsontext.Value(`{"status":"completed"}`)}}}}
	q1, err := b.Quote(context.Background(), req)
	if err != nil || q1.MaxInputTokens <= q0.MaxInputTokens+int64(req.ToolBytes()-before) {
		t.Errorf("the quote with a step %+v, without %+v", q1, q0)
	}
	if len(s.order()) != 0 {
		t.Errorf("the quote asked the provider: %v", s.order())
	}
}

// Refused before any I/O: a backend whose approval names no tool calls (as
// every recorded approval is), a role it names none for, a tool schema
// outside the documented limits, a model it does not list; and Complete
// handed tools. Nothing is sent and nothing is reserved.
func TestTurnRefusedBeforeIO(t *testing.T) {
	s := newServer(t, map[string][]reply{messagesPath: {turnAnswer("tool_use", toolUse("toolu_1", "run_what_if", `{"scenario_id":"scn_1"}`))}})
	c, b, m, _ := meteredClient(t, s, 1000*spendUSD)
	if c.AcceptsTools(provider.RoleScenario) {
		t.Error("a backend whose approval names no tool calls accepts tools")
	}
	if _, err := c.Turn(context.Background(), turnContext(t)); provider.CodeOf(err) != provider.CodePolicyNotApproved {
		t.Errorf("no tool calls approved: %v", err)
	}
	meta.SetTools(b, "analyst")
	if _, err := c.Turn(context.Background(), turnContext(t)); provider.CodeOf(err) != provider.CodePolicyNotApproved {
		t.Errorf("tools for another role: %v", err)
	}
	meta.SetTools(b, "scenario")
	meta.SetRoles(b, "modeler")
	if _, err := c.Turn(context.Background(), turnContext(t)); provider.CodeOf(err) != provider.CodePolicyNotApproved {
		t.Errorf("a role the approval does not serve: %v", err)
	}
	meta.SetRoles(b, "scenario")
	wide := turnContext(t)
	wide.Tools = []provider.Tool{{Name: "propose", InputSchema: jsontext.Value(`{"type":"object","properties":{"idea":{"type":"string","minLength":1}},"additionalProperties":false}`)}}
	if _, err := c.Turn(context.Background(), wide); provider.CodeOf(err) != provider.CodeInvalidRequest {
		t.Errorf("a schema keyword outside the documented limits: %v", err)
	}
	// Review round 1 (M1): an array that does not say what its elements are.
	open := turnContext(t)
	open.Tools = []provider.Tool{{Name: "pick", InputSchema: jsontext.Value(`{"type":"object","properties":{"ids":{"type":"array"}},"required":["ids"],"additionalProperties":false}`)}}
	if _, err := c.Turn(context.Background(), open); provider.CodeOf(err) != provider.CodeInvalidRequest {
		t.Errorf("an array without items: %v", err)
	}
	chosen := turnContext(t)
	chosen.Model = "muse-wonder-1.0"
	if _, err := c.Turn(context.Background(), chosen); provider.CodeOf(err) != provider.CodeModelNotApproved {
		t.Errorf("a model not listed: %v", err)
	}
	in := turnContext(t)
	if _, err := b.Complete(context.Background(), provider.Request{Prompt: in.Prompt, OutputSchema: "proposal-output.schema.json", Tools: in.Tools, Reservation: "r1"}); provider.CodeOf(err) != provider.CodeInvalidRequest {
		t.Errorf("Complete handed tools: %v", err)
	}
	if _, err := b.Turn(context.Background(), provider.Request{Prompt: in.Prompt, Tools: in.Tools}); provider.CodeOf(err) != provider.CodeInvalidRequest {
		t.Errorf("Turn without a reservation: %v", err)
	}
	if _, err := b.Turn(context.Background(), provider.Request{Prompt: in.Prompt, Reservation: "r1"}); provider.CodeOf(err) != provider.CodeInvalidRequest {
		t.Errorf("Turn without tools: %v", err)
	}
	if len(s.order()) != 0 || m.Committed() != 0 {
		t.Errorf("sent %v, committed %d", s.order(), m.Committed())
	}
}

// An answer that is not a turn's: tool_use with end_turn, tool_use stop
// without a tool_use block, a built-in tool's block or a tool result, an ID
// that is not a string, another model's; truncated and refused answers keep
// their codes. Each is settled at its usage all the same.
func TestTurnAnswersRefused(t *testing.T) {
	cases := map[string]struct {
		r    reply
		code provider.Code
	}{
		"tool_use with end_turn":   {turnAnswer("end_turn", toolUse("toolu_1", "run_what_if", `{"scenario_id":"scn_1"}`)), provider.CodeInvalidOutput},
		"tool_use stop, no call":   {turnAnswer("tool_use", text("I would call it.")), provider.CodeInvalidOutput},
		"a built-in tool's block":  {turnAnswer("tool_use", step("search", map[string]string{"query": "x"}), toolUse("toolu_1", "run_what_if", `{"scenario_id":"scn_1"}`)), provider.CodeInvalidOutput},
		"a tool result":            {turnAnswer("tool_use", map[string]any{"type": "tool_result", "tool_use_id": "toolu_1", "content": "x"}, toolUse("toolu_1", "run_what_if", `{"scenario_id":"scn_1"}`)), provider.CodeInvalidOutput},
		"an ID that is no string":  {turnAnswer("tool_use", map[string]any{"type": "tool_use", "id": 7, "name": "run_what_if", "input": map[string]any{"scenario_id": "scn_1"}}), provider.CodeInvalidOutput},
		"a call of no tool":        {turnAnswer("tool_use", toolUse("toolu_1", "publish_version", `{}`)), provider.CodeInvalidOutput},
		"an unknown argument":      {turnAnswer("tool_use", toolUse("toolu_1", "run_what_if", `{"scenario_id":"scn_1","replications":1000000}`)), provider.CodeInvalidOutput},
		"another model":            {otherModel(turnAnswer("tool_use", toolUse("toolu_1", "run_what_if", `{"scenario_id":"scn_1"}`))), provider.CodeInvalidOutput},
		"pause_turn":               {turnAnswer("pause_turn", text("...")), provider.CodeInvalidOutput},
		"truncated":                {turnAnswer("max_tokens", text("I will")), provider.CodeTruncated},
		"refused":                  {turnAnswer("refusal", text("No.")), provider.CodeRefused},
		"no text and no tool call": {turnAnswer("end_turn", thinking), provider.CodeInvalidOutput},
	}
	for name, tc := range cases {
		s := newServer(t, map[string][]reply{messagesPath: {tc.r}})
		c, b, m, _ := meteredClient(t, s, 1000*spendUSD)
		meta.SetTools(b, "scenario")
		res, err := c.Turn(context.Background(), turnContext(t))
		if provider.CodeOf(err) != tc.code {
			t.Errorf("%s: %v, want %s", name, err, tc.code)
		}
		if len(res.Calls) != 0 || res.Text != "" {
			t.Errorf("%s: handed back %+v", name, res)
		}
		if res.Metadata.Usage.InputTokens != 120 || res.Metadata.CostNanoUSD <= 0 || m.Spent() != res.Metadata.CostNanoUSD {
			t.Errorf("%s: not settled at its usage: %+v, spent %d", name, res.Metadata, m.Spent())
		}
		leaks(t, name, err.Error())
	}
}

// Offering tools changes nothing for the existing roles: with tools
// approved for every role, a proposal's request still offers none and its
// answer's tool_use is still refused (as TestRoleRequestGoldens pins the
// requests byte for byte).
func TestToolUseRefusedForRequestsWithoutTools(t *testing.T) {
	for _, r := range []reply{
		turnAnswer("tool_use", toolUse("toolu_1", "run_what_if", `{"scenario_id":"scn_1"}`)),
		turnAnswer("end_turn", toolUse("toolu_1", "run_what_if", `{"scenario_id":"scn_1"}`), text(answerJ)),
	} {
		s := newServer(t, map[string][]reply{messagesPath: {r}})
		c, b := client(t, s)
		meta.SetTools(b, "modeler", "scenario", "analyst", "importer")
		if _, err := ask(t, c); provider.CodeOf(err) != provider.CodeInvalidOutput {
			t.Errorf("a proposal answered with a tool call: %v", err)
		}
		if _, ok := s.bodies[0]["tools"]; ok {
			t.Error("a proposal offered tools")
		}
	}
}
