package meta_test

import (
	"context"
	"encoding/json"
	"encoding/json/jsontext"
	"maps"
	"reflect"
	"slices"
	"strings"
	"testing"

	"github.com/kcl1104/geminus/internal/ai/investigator"
	"github.com/kcl1104/geminus/internal/ai/provider"
	"github.com/kcl1104/geminus/internal/ai/provider/meta"
	"github.com/kcl1104/geminus/internal/config/aiapproval"
	"github.com/kcl1104/geminus/internal/secret"
)

// H-061 §9 (A5): a planner turn is the tool turn of E1 with tool_choice any
// and no parallel calls, max_tokens 8,192 whatever the budget says, and its
// answer's content kept, opaque, for the next turn of the run to send back
// as returned (reasoning blocks included) in place of the rebuilt step.

const planInv = "inv_01M4YDQK00TA00000000000001"

var planAttr = provider.Attribution{TargetType: provider.PlannerTarget, TargetID: planInv, RequestID: "req_01M4YDQK00TA00000000000001"}

type passDecoder struct{}

func (passDecoder) DecodeCall(_ string, input []byte) ([]byte, *provider.ToolRefusal) {
	var v any
	if json.Unmarshal(input, &v) != nil {
		return nil, &provider.ToolRefusal{Code: "INVALID_ARGUMENTS"}
	}
	out, _ := json.Marshal(v)
	return out, nil
}

func planContext(t *testing.T, planner string, steps ...provider.ToolStep) provider.PlanContext {
	return provider.PlanContext{OrganizationID: org, Prompt: roleSealed(t, provider.RoleInvestigator, "PERSON_REQUEST", "find a shorter lead time"),
		Attribution: planAttr, Model: planner, Tools: []provider.Tool{runTool()}, Steps: steps, Decoder: passDecoder{}}
}

var planThinking = map[string]any{"type": "redacted_thinking", "data": "PLANNER-REASONING-SENTINEL"}

func TestPlanTurnRequestShape(t *testing.T) {
	first := turnAnswer("tool_use", planThinking, text("Run the first what-if."), toolUse("toolu_01", "run_what_if", `{"scenario_id":"scn_1"}`))
	second := turnAnswer("tool_use", text("And the next."), toolUse("toolu_02", "run_what_if", `{"scenario_id":"scn_2"}`))
	second.body = strings.Replace(second.body, `"model":"`+model+`"`, `"model":"`+wonder+`"`, 1) // Muse Wonder answers
	s := newServer(t, map[string][]reply{messagesPath: {first, second}})
	c, b := client(t, s, func(bu *provider.Budget) { bu.MaxOutputTokens = 4321 })
	meta.SetTools(b, "investigator")
	meta.SetPlannerModels(b, model, wonder)
	res, err := c.PlanTurn(context.Background(), planContext(t, model))
	if err != nil {
		t.Fatal(err)
	}
	if res.Call.ID != "toolu_01" || res.Note != "Run the first what-if." || !res.Replay.Present() {
		t.Fatalf("the answer %+v", res)
	}
	body := s.bodies[0]
	if keys := slices.Sorted(maps.Keys(body)); !slices.Equal(keys, []string{"max_tokens", "messages", "model", "system", "tool_choice", "tools"}) {
		t.Errorf("top-level fields %v", keys)
	}
	if want := map[string]any{"type": "any", "disable_parallel_tool_use": true}; !reflect.DeepEqual(body["tool_choice"], want) {
		t.Errorf("tool_choice %v", body["tool_choice"])
	}
	if body["model"] != model || body["max_tokens"] != float64(provider.PlannerMaxOutputTokens) {
		t.Errorf("model %v max_tokens %v", body["model"], body["max_tokens"])
	}
	// The replay is the answer's content array as returned.
	var answer struct {
		Content []any `json:"content"`
	}
	if err := json.Unmarshal([]byte(first.body), &answer); err != nil {
		t.Fatal(err)
	}
	var replayed []any
	if err := json.Unmarshal(res.Replay.Bytes(), &replayed); err != nil || !reflect.DeepEqual(replayed, answer.Content) {
		t.Errorf("the replay %s", res.Replay.Bytes())
	}
	step := provider.ToolStep{Calls: []provider.ToolCall{res.Call}, Results: []provider.ToolResult{{CallID: res.Call.ID,
		Content: jsontext.Value(`{"status":"completed"}`)}}, Replay: res.Replay}
	if _, err := c.PlanTurn(context.Background(), planContext(t, wonder, step)); err != nil {
		t.Fatal(err)
	}
	body = s.bodies[1]
	if body["model"] != wonder || body["max_tokens"] != float64(provider.PlannerMaxOutputTokens) {
		t.Errorf("Muse Wonder's turn: model %v max_tokens %v", body["model"], body["max_tokens"])
	}
	msgs := body["messages"].([]any)
	if len(msgs) != 3 {
		t.Fatalf("%d messages", len(msgs))
	}
	if want := map[string]any{"role": "assistant", "content": answer.Content}; !reflect.DeepEqual(msgs[1], want) {
		t.Errorf("the replayed turn %v", msgs[1])
	}
	if want := map[string]any{"role": "user", "content": []any{map[string]any{"type": "tool_result", "tool_use_id": "toolu_01",
		"content": `{"status":"completed"}`}}}; !reflect.DeepEqual(msgs[2], want) {
		t.Errorf("the result %v", msgs[2])
	}
	// The reasoning goes back to the provider and nowhere else: not in the
	// result, its error or its metadata.
	leaks(t, "the plan result", fmtAll(res), "PLANNER-REASONING-SENTINEL")
}

func fmtAll(res provider.PlanResult) string {
	j, _ := json.Marshal(res)
	return string(j) + " " + res.Replay.String() + " " + res.Note
}

// Without a replay, a planner turn's step is sent as E1 sends one (the
// rebuilt tool_use); a max_tokens answer is a truncated, unrecorded invalid
// turn; a role the approval names no tool calls for, or a planner model it
// does not list, is refused before any I/O.
func TestPlanTurnRefusalsAndTruncation(t *testing.T) {
	s := newServer(t, map[string][]reply{messagesPath: {turnAnswer("max_tokens", text("I will"))}})
	c, b, m, _ := meteredClient(t, s, 1000*spendUSD)
	meta.SetPlannerModels(b, model)
	if _, err := c.PlanTurn(context.Background(), planContext(t, model)); provider.CodeOf(err) != provider.CodePolicyNotApproved {
		t.Errorf("no tool calls approved: %v", err)
	}
	meta.SetTools(b, "investigator")
	if _, err := c.PlanTurn(context.Background(), planContext(t, wonder)); provider.CodeOf(err) != provider.CodeModelNotApproved {
		t.Errorf("a planner the approval does not list: %v", err)
	}
	if len(s.order()) != 0 || m.Committed() != 0 {
		t.Fatalf("sent %v, committed %d", s.order(), m.Committed())
	}
	res, err := c.PlanTurn(context.Background(), planContext(t, model))
	if provider.CodeOf(err) != provider.CodeTruncated || res.Invalid == nil || res.Invalid.Kind != provider.InvalidTruncated || res.Invalid.Recorded() {
		t.Errorf("a truncated turn: %v %+v", err, res.Invalid)
	}
	if res.Metadata.CostNanoUSD <= 0 || m.Spent() != res.Metadata.CostNanoUSD {
		t.Errorf("not settled: %+v", res.Metadata)
	}
}

// New takes the approval's tool calls and planner models: H-029 names tool
// calls for the investigator only, and Muse Spark 1.3 and Muse Wonder 1.0
// as its planners (H-060, H-063).
func TestNewTakesTheApprovalsPlanner(t *testing.T) {
	h029, ok := aiapproval.Recorded("H-029")
	if !ok {
		t.Fatal("H-029 is not recorded")
	}
	b, err := meta.New(meta.Config{Approval: h029, Model: model, Credential: meta.Credential{Label: "meta", Key: secret.Secret(key)}})
	if err != nil {
		t.Fatal(err)
	}
	if !b.AcceptsTools(provider.RoleInvestigator) {
		t.Error("the investigator's tool calls")
	}
	for _, r := range []provider.Role{provider.RoleModeler, provider.RoleScenario, provider.RoleAnalyst, provider.RoleImporter} {
		if b.AcceptsTools(r) {
			t.Errorf("tool calls for %s", r)
		}
	}
	for m, want := range map[string]bool{model: true, wonder: true, "muse-spark-1.3-contributor": false, "": false} {
		if got := b.AcceptsModel(provider.RoleInvestigator, m); got != want {
			t.Errorf("the planner %q: %v", m, got)
		}
	}
	if b.AcceptsResearch(provider.RoleInvestigator) {
		t.Error("the planner researches")
	}
}

// The registry's seven tools (internal/ai/investigator.ProviderTools, the
// closed projections of schemas/investigator.schema.json) are within Meta's
// documented schema limits, so a planner turn offering them is sent (H-061
// §9; E1 review finding 5: the bounds stay with the client and the
// registry's decoder).
func TestTheRegistrysToolsAreSendable(t *testing.T) {
	tools, err := investigator.ProviderTools(investigator.Tools)
	if err != nil {
		t.Fatal(err)
	}
	for _, tool := range tools {
		var schema any
		if err := json.Unmarshal(tool.InputSchema, &schema); err != nil {
			t.Fatal(err)
		}
		if v := meta.SchemaViolations(schema); len(v) > 0 {
			t.Errorf("%s: %v", tool.Name, v)
		}
	}
	s := newServer(t, map[string][]reply{messagesPath: {turnAnswer("tool_use", toolUse("toolu_01", "finish", `{"outcome":"out_of_ideas","best_scenario_id":null}`))}})
	c, b := client(t, s)
	meta.SetTools(b, "investigator")
	meta.SetPlannerModels(b, model)
	in := planContext(t, model)
	in.Tools, in.Decoder = tools, investigator.Decoder{Mode: investigator.Scenario}
	res, err := c.PlanTurn(context.Background(), in)
	if err != nil || res.Call.Name != "finish" || string(res.Call.Input) != `{"best_scenario_id":null,"outcome":"out_of_ideas"}` {
		t.Fatalf("%+v %v", res, err)
	}
	if sent := s.bodies[0]["tools"].([]any); len(sent) != len(investigator.Tools) {
		t.Errorf("%d tools sent", len(sent))
	}
}
