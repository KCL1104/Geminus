package provider

import (
	"bytes"
	"context"
	"encoding/json/jsontext"
	"encoding/json/v2"
	"errors"
	"fmt"
	"slices"
	"strings"
	"testing"
	"time"
	"unicode/utf8"

	"github.com/kcl1104/geminus/internal/ai/spend"
)

// H-061 §9, slice A5: a planner turn is the investigator role's only path
// to a model: one call, one attempt, the strict decoder, the note
// sanitized, the content to replay opaque.

const (
	invID     = "inv_01M4YDQK00TA00000000000001"
	sentinelZ = "PLANNER-SENTINEL-Z"
)

var planAttribution = Attribution{TargetType: PlannerTarget, TargetID: invID, RequestID: "req_01M4YDQK00TA00000000000001"}

// planner is a test ToolTurner and ModelChooser for the investigator role.
type planner struct {
	turner
	models []string
}

func (b *planner) AcceptsModel(role Role, m string) bool {
	return role == RoleInvestigator && slices.Contains(b.models, m)
}

// billablePlanner is a metered planner: its quote names the model asked
// for.
type billablePlanner struct {
	planner
	maxInput int64
	quoted   []Request
}

func (b *billablePlanner) Quote(_ context.Context, req Request) (Quote, error) {
	b.quoted = append(b.quoted, req)
	return Quote{Model: req.Model, MaxInputTokens: b.maxInput}, nil
}

// decoderFunc is a test ToolDecoder.
type decoderFunc func(name string, input []byte) ([]byte, *ToolRefusal)

func (f decoderFunc) DecodeCall(name string, input []byte) ([]byte, *ToolRefusal) {
	return f(name, input)
}

// passDecoder takes every call, its input re-encoded (as the registry
// returns RFC 8785).
var passDecoder = decoderFunc(func(_ string, input []byte) ([]byte, *ToolRefusal) {
	var v any
	if json.Unmarshal(input, &v) != nil {
		return nil, &ToolRefusal{Code: "INVALID_ARGUMENTS"}
	}
	out, _ := json.Marshal(v, json.Deterministic(true))
	return out, nil
})

func newPlanner(respond func(context.Context, Request) (Response, error)) *planner {
	return &planner{turner: turner{accepts: true, respond: respond}, models: []string{spark, wonder}}
}

func planCtx(t *testing.T, steps ...ToolStep) PlanContext {
	t.Helper()
	return PlanContext{OrganizationID: orgA, Prompt: prompt(t, RoleInvestigator), Attribution: planAttribution, Model: spark,
		Tools: testTools(), Steps: steps, Decoder: passDecoder}
}

// planAnswerOf answers with the narration text, the calls and a replay.
func planAnswerOf(text string, cs ...ToolCall) func(context.Context, Request) (Response, error) {
	return func(_ context.Context, req Request) (Response, error) {
		replay, _ := json.Marshal([]any{map[string]any{"type": "redacted_thinking", "data": sentinelZ}, map[string]any{"type": "text", "text": text}})
		return Response{Model: req.Model, Credential: "meta", Text: []byte(text), ToolCalls: cs, Replay: replay}, nil
	}
}

// Every request a planner turn may not make is refused before the backend
// is asked; the investigator's turns never go through Client.Turn.
func TestPlanTurnRefusesBeforeIO(t *testing.T) {
	ctx := context.Background()
	b := newPlanner(planAnswerOf("", call("c1", "finish", `{"outcome":"no_improvement"}`)))
	c := client(t, b)
	other := planCtx(t)
	other.Prompt = prompt(t, RoleScenario)
	noModel := planCtx(t)
	noModel.Model = ""
	noDecoder := planCtx(t)
	noDecoder.Decoder = nil
	wrongTarget := planCtx(t)
	wrongTarget.Attribution.TargetType = "ai_proposal"
	badID := planCtx(t)
	badID.Attribution.TargetID = "aip_01M4YDQK00TA00000000000001"
	noRequest := planCtx(t)
	noRequest.Attribution.RequestID = ""
	twoCalls := planCtx(t, ToolStep{Calls: []ToolCall{call("a1", "finish", `{"outcome":"no_improvement"}`), call("a2", "finish", `{"outcome":"no_improvement"}`)},
		Results: []ToolResult{{CallID: "a1", Content: jsontext.Value(`{}`)}, {CallID: "a2", Content: jsontext.Value(`{}`)}}})
	withText := planCtx(t, ToolStep{Text: "narration sent apart", Calls: []ToolCall{call("a1", "propose_what_if", ideaJ)},
		Results: []ToolResult{{CallID: "a1", Content: jsontext.Value(`{}`)}}})
	research := planCtx(t)
	research.Prompt = prompt(t, RoleInvestigator)
	research.Tools = nil
	for name, in := range map[string]PlanContext{"another role": other, "no planner model": noModel, "no decoder": noDecoder,
		"another target": wrongTarget, "not an investigation ID": badID, "no request ID": noRequest, "a step of two calls": twoCalls,
		"a step's narration sent apart": withText, "no tools": research} {
		res, err := c.PlanTurn(ctx, in)
		if CodeOf(err) != CodeInvalidRequest || res.Invalid != nil {
			t.Errorf("%s: %v %+v", name, err, res.Invalid)
		}
	}
	unknownModel := planCtx(t)
	unknownModel.Model = "muse-spark-1.3-contributor"
	if _, err := c.PlanTurn(ctx, unknownModel); CodeOf(err) != CodeModelNotApproved {
		t.Errorf("a model the backend may not ask: %v", err)
	}
	if _, err := c.Turn(ctx, TurnContext{OrganizationID: orgA, Prompt: prompt(t, RoleInvestigator), Attribution: planAttribution,
		Tools: testTools()}); CodeOf(err) != CodeInvalidRequest {
		t.Errorf("the investigator through Client.Turn: %v", err)
	}
	cancelled, cancel := context.WithCancel(ctx)
	cancel()
	if _, err := c.PlanTurn(cancelled, planCtx(t)); CodeOf(err) != CodeCancelled || !errors.Is(err, context.Canceled) {
		t.Errorf("a cancelled turn: %v", err)
	}
	if b.calls.Load() != 0 {
		t.Errorf("the backend was asked %d times", b.calls.Load())
	}
	if _, err := client(t, answer(`{}`)).PlanTurn(ctx, planCtx(t)); CodeOf(err) != CodeToolsUnsupported {
		t.Errorf("a backend that sends no tools: %v", err)
	}
	refusing := newPlanner(planAnswerOf(""))
	refusing.accepts = false
	if _, err := client(t, refusing).PlanTurn(ctx, planCtx(t)); CodeOf(err) != CodePolicyNotApproved {
		t.Errorf("a backend whose approval names no tool calls for the role: %v", err)
	}
	if _, err := client(t, Disabled{}).PlanTurn(ctx, planCtx(t)); CodeOf(err) != CodeNotConfigured {
		t.Errorf("disabled: %v", err)
	}
	if refusing.calls.Load() != 0 {
		t.Error("a refusing backend was asked")
	}
	// The prompt budget counts the turn's tools and transcript.
	big := planCtx(t)
	if _, err := client(t, b, func(bu *Budget) { bu.MaxPromptBytes = len(big.Prompt.System) + len(big.Prompt.User()) + 10 }).PlanTurn(ctx, big); CodeOf(err) != CodeContextTooLarge {
		t.Errorf("a turn over the prompt budget: %v", err)
	}
}

// The request: one call asked for, one attempt, the default budget's
// max_tokens and at most PlannerTurnTimeout, for Muse Spark and Muse Wonder
// alike (never the slow model's 128,000 tokens and 900 s).
func TestPlanTurnRequest(t *testing.T) {
	var deadlines []time.Duration
	b := newPlanner(func(ctx context.Context, req Request) (Response, error) {
		d, _ := ctx.Deadline()
		deadlines = append(deadlines, time.Until(d))
		return planAnswerOf("", call("c1", "finish", `{"outcome":"no_improvement"}`))(ctx, req)
	})
	c := client(t, b, func(bu *Budget) {
		bu.MaxOutputTokens, bu.Timeout, bu.AttemptTimeout, bu.MaxRetries = 32000, 300*time.Second, 250*time.Second, 4
	})
	for _, m := range []string{spark, wonder} {
		in := planCtx(t)
		in.Model = m
		if _, err := c.PlanTurn(context.Background(), in); err != nil {
			t.Fatalf("%s: %v", m, err)
		}
	}
	if len(b.reqs) != 2 {
		t.Fatalf("%d requests", len(b.reqs))
	}
	for i, req := range b.reqs {
		if !req.OneCall || req.MaxRetries != 0 || req.MaxOutputTokens != PlannerMaxOutputTokens || req.AttemptTimeout != PlannerTurnTimeout ||
			req.OutputSchema != "" || len(req.Tools) != 2 || req.Prompt.Role != RoleInvestigator {
			t.Errorf("request %d: one call %v, retries %d, max_tokens %d, attempt %v, schema %q, %d tools", i, req.OneCall, req.MaxRetries,
				req.MaxOutputTokens, req.AttemptTimeout, req.OutputSchema, len(req.Tools))
		}
		if deadlines[i] > PlannerTurnTimeout || deadlines[i] < PlannerTurnTimeout-10*time.Second {
			t.Errorf("request %d: the call's deadline is %v away", i, deadlines[i])
		}
	}
	if b.reqs[0].Model != spark || b.reqs[1].Model != wonder {
		t.Errorf("models %q %q", b.reqs[0].Model, b.reqs[1].Model)
	}
	if PlannerMaxOutputTokens != DefaultBudget().MaxOutputTokens {
		t.Error("a planner turn's max_tokens is not the default budget's")
	}
	// A deployment's shorter timeouts bound Muse Spark's turns.
	short := client(t, b, func(bu *Budget) { bu.Timeout, bu.AttemptTimeout = 60*time.Second, 45*time.Second })
	if _, err := short.PlanTurn(context.Background(), planCtx(t)); err != nil || b.reqs[2].AttemptTimeout != 45*time.Second {
		t.Errorf("a shorter budget: %v %v", err, b.reqs[2].AttemptTimeout)
	}
	// Every other tool turn is as before: no one-call request.
	if _, err := client(t, &turner{accepts: true, respond: calls(call("c1", "finish", `{"outcome":"no_improvement"}`))}).Turn(context.Background(), turnCtx(t)); err != nil {
		t.Fatal(err)
	}
}

// The answer: exactly one call of an offered tool that the decoder takes,
// with the decoder's canonical arguments; the narration as the note,
// sanitized; anything else is an invalid turn that says what was wrong and
// repeats nothing the model wrote.
func TestPlanTurnAnswers(t *testing.T) {
	ctx := context.Background()
	b := newPlanner(planAnswerOf("I will try a second tester.\n\tIt is the bottleneck.",
		call("toolu_01", "propose_what_if", `{ "from_suggestion" : null, "idea" : "add a second ICT tester" }`)))
	res, err := client(t, b).PlanTurn(ctx, planCtx(t))
	if err != nil {
		t.Fatal(err)
	}
	if res.Call.ID != "toolu_01" || res.Call.Name != "propose_what_if" || string(res.Call.Input) != `{"from_suggestion":null,"idea":"add a second ICT tester"}` {
		t.Errorf("the call %+v", res.Call)
	}
	if res.Note != "I will try a second tester. It is the bottleneck." || res.Invalid != nil || !res.Replay.Present() {
		t.Errorf("note %q invalid %+v replay %v", res.Note, res.Invalid, res.Replay.Present())
	}
	if res.Metadata.Role != RoleInvestigator || res.Metadata.OutputSchema != "" || res.Metadata.OutputHash == "" || res.Metadata.InputHash == "" {
		t.Errorf("metadata %+v", res.Metadata)
	}
	for _, s := range []string{fmt.Sprint(res.Replay), fmt.Sprintf("%v %+v %#v %s %q %x", res.Replay, res.Replay, res.Replay, res.Replay, res.Replay, res.Replay), fmt.Sprintf("%+v", res)} {
		if strings.Contains(s, sentinelZ) {
			t.Errorf("the replay printed: %s", s)
		}
	}
	if j, _ := json.Marshal(res); bytes.Contains(j, []byte(sentinelZ)) {
		t.Errorf("the replay encoded: %s", j)
	}

	refuse := func(code, path string) ToolDecoder {
		return decoderFunc(func(string, []byte) ([]byte, *ToolRefusal) { return nil, &ToolRefusal{Code: code, Path: path} })
	}
	finish := call("c1", "finish", `{"outcome":"no_improvement"}`)
	for _, c := range []struct {
		name    string
		respond func(context.Context, Request) (Response, error)
		decoder ToolDecoder
		code    Code
		want    InvalidTurn
	}{
		{"text, no call", planAnswerOf("I would rather approve it myself. " + sentinelZ), nil, CodeInvalidOutput, InvalidTurn{Kind: InvalidNoCall}},
		{"two calls", planAnswerOf("", finish, call("c2", "finish", `{"outcome":"no_improvement"}`)), nil, CodeInvalidOutput,
			InvalidTurn{Kind: InvalidSeveralCalls, Code: "INVALID_ARGUMENTS"}},
		{"a tool not offered", planAnswerOf("", call("c1", "publish_version", `{"version":"v3","note":"`+sentinelZ+`"}`)), nil, CodeInvalidOutput,
			InvalidTurn{Kind: InvalidUnknownTool, Code: "UNKNOWN_TOOL"}},
		{"another mode's tool", planAnswerOf("", finish), refuse("MODE_MISMATCH", ""), CodeInvalidOutput, InvalidTurn{Kind: InvalidUnknownTool, Code: "UNKNOWN_TOOL"}},
		{"the decoder's unknown tool", planAnswerOf("", finish), refuse("UNKNOWN_TOOL", ""), CodeInvalidOutput, InvalidTurn{Kind: InvalidUnknownTool, Code: "UNKNOWN_TOOL"}},
		{"arguments refused", planAnswerOf("", call("c1", "propose_what_if", `{"idea":"","from_suggestion":null}`)), refuse("INVALID_ARGUMENTS", "/idea"),
			CodeInvalidOutput, InvalidTurn{Kind: InvalidArguments, Code: "INVALID_ARGUMENTS", Path: "/idea"}},
		{"an unknown member the projection refuses", planAnswerOf("", call("c1", "finish", `{"outcome":"no_improvement","`+sentinelZ+`":1}`)), nil,
			CodeInvalidOutput, InvalidTurn{Kind: InvalidArguments, Code: "INVALID_ARGUMENTS"}},
		{"a call ID not of a token's shape", planAnswerOf("", call("c 1 "+sentinelZ, "finish", `{"outcome":"no_improvement"}`)), nil, CodeInvalidOutput,
			InvalidTurn{Kind: InvalidArguments, Code: "INVALID_ARGUMENTS"}},
		{"text not UTF-8", planAnswerOf("\xff\xfe", finish), nil, CodeInvalidOutput, InvalidTurn{Kind: InvalidText}},
		{"truncated", func(context.Context, Request) (Response, error) {
			return Response{}, NewError(CodeTruncated, 0, "meta")
		}, nil,
			CodeTruncated, InvalidTurn{Kind: InvalidTruncated}},
		{"refused", func(context.Context, Request) (Response, error) { return Response{}, NewError(CodeRefused, 0, "meta") }, nil,
			CodeRefused, InvalidTurn{Kind: InvalidNoCall}},
		{"an answer the backend could not take", func(context.Context, Request) (Response, error) {
			return Response{}, NewError(CodeInvalidOutput, 0, "meta")
		}, nil,
			CodeInvalidOutput, InvalidTurn{Kind: InvalidNoCall}},
		{"web search not offered", func(_ context.Context, req Request) (Response, error) {
			return Response{Model: req.Model, ToolCalls: []ToolCall{finish}, Research: Research{Queries: 1, ToolCalls: 1}}, nil
		}, nil, CodeInvalidOutput, InvalidTurn{Kind: InvalidNoCall}},
	} {
		in := planCtx(t)
		if c.decoder != nil {
			in.Decoder = c.decoder
		}
		res, err := client(t, newPlanner(c.respond)).PlanTurn(ctx, in)
		var pe *Error
		if !errors.As(err, &pe) || pe.Code != c.code || res.Invalid == nil || *res.Invalid != c.want || res.Call.Name != "" || res.Replay.Present() || res.Note != "" {
			t.Errorf("%s: %v %+v %+v", c.name, err, res.Invalid, res.Call)
			continue
		}
		if c.want.Recorded() != (c.want.Code != "") || (c.want.Kind == InvalidNoCall && c.want.Recorded()) {
			t.Errorf("%s: recorded %v", c.name, c.want.Recorded())
		}
		if strings.Contains(err.Error(), sentinelZ) || strings.Contains(fmt.Sprintf("%+v", res), sentinelZ) {
			t.Errorf("%s: the model's text in %v", c.name, err)
		}
	}
	// A decoder that fails of its own (INTERNAL) is the server's fault: a
	// refused request, not an invalid turn.
	in := planCtx(t)
	in.Decoder = refuse("INTERNAL", "")
	if res, err := client(t, newPlanner(planAnswerOf("", finish))).PlanTurn(ctx, in); CodeOf(err) != CodeInvalidRequest || res.Invalid != nil {
		t.Errorf("an internal decoder failure: %v %+v", err, res.Invalid)
	}
}

// Within one ADK run the next turn replays the model's content as
// returned, bound to its call: the backend is handed it, its digest (never
// its content) is in the input hash, and a replay is never sent with
// another call or on any turn but a planner's.
func TestPlanTurnReplay(t *testing.T) {
	ctx := context.Background()
	first := call("toolu_01", "propose_what_if", ideaJ)
	b := newPlanner(planAnswerOf("Proposing.", first))
	c := client(t, b)
	r1, err := c.PlanTurn(ctx, planCtx(t))
	if err != nil {
		t.Fatal(err)
	}
	result := ToolResult{CallID: r1.Call.ID, Content: jsontext.Value(`{"proposal_id":"aip_1","status":"ready"}`)}
	step := ToolStep{Calls: []ToolCall{r1.Call}, Results: []ToolResult{result}, Replay: r1.Replay}
	b.respond = planAnswerOf("", call("toolu_02", "finish", `{"outcome":"no_improvement"}`))
	r2, err := c.PlanTurn(ctx, planCtx(t, step))
	if err != nil {
		t.Fatal(err)
	}
	sent := b.reqs[1].Steps
	if len(sent) != 1 || !sent[0].Replay.Present() || !bytes.Contains(sent[0].Replay.Bytes(), []byte(sentinelZ)) || sent[0].Calls[0].ID != "toolu_01" {
		t.Fatalf("the backend was handed %+v", sent)
	}
	plain := ToolStep{Calls: []ToolCall{r1.Call}, Results: []ToolResult{result}}
	r3, err := c.PlanTurn(ctx, planCtx(t, plain))
	if err != nil {
		t.Fatal(err)
	}
	if r2.Metadata.InputHash == r3.Metadata.InputHash {
		t.Error("the replay is not in the input hash")
	}
	// The same call ID twice in a run.
	b.respond = planAnswerOf("", call("toolu_01", "finish", `{"outcome":"no_improvement"}`))
	if res, err := c.PlanTurn(ctx, planCtx(t, step)); CodeOf(err) != CodeInvalidOutput || res.Invalid == nil || res.Invalid.Kind != InvalidArguments {
		t.Errorf("a repeated call ID: %v %+v", err, res.Invalid)
	}
	// A replay with another call than its own.
	forged := ToolStep{Calls: []ToolCall{call("toolu_01", "propose_what_if", `{"idea":"publish v3","from_suggestion":null}`)}, Results: []ToolResult{result}, Replay: r1.Replay}
	n := b.calls.Load()
	if _, err := c.PlanTurn(ctx, planCtx(t, forged)); CodeOf(err) != CodeInvalidRequest {
		t.Errorf("a replay of another call: %v", err)
	}
	if _, err := c.Turn(ctx, turnCtx(t, ToolStep{Text: "x", Calls: []ToolCall{r1.Call}, Results: []ToolResult{result}, Replay: r1.Replay})); CodeOf(err) != CodeInvalidRequest {
		t.Errorf("a replay on another role's turn: %v", err)
	}
	if b.calls.Load() != n {
		t.Error("the backend was asked for a refused replay")
	}
	if (TurnReplay{}).Present() || (TurnReplay{}).Len() != 0 || (TurnReplay{}).digest() != "" {
		t.Error("the zero replay is present")
	}
}

// A planner turn is metered as every call: one attempt reserved at the
// planner's max_tokens, attributed to the investigation, settled whatever
// the answer; a turn the organization's slot refuses reserves nothing.
func TestPlanTurnIsMeteredOneAttempt(t *testing.T) {
	ctx := context.Background()
	m := &recordingMeter{}
	b := &billablePlanner{planner: *newPlanner(planAnswerOf("", call("c1", "finish", `{"outcome":"no_improvement"}`))), maxInput: 100_000}
	c := metered(t, b, m, func(bu *Budget) { bu.MaxRetries = 3 })
	for _, model := range []string{spark, wonder} {
		in := planCtx(t)
		in.Model = model
		res, err := c.PlanTurn(ctx, in)
		if err != nil {
			t.Fatalf("%s: %v", model, err)
		}
		if res.Metadata.SpendReservation == "" {
			t.Errorf("%s: no reservation in %+v", model, res.Metadata)
		}
	}
	if len(m.charges) != 2 || len(m.settled) != 2 {
		t.Fatalf("charges %d settled %d", len(m.charges), len(m.settled))
	}
	for i, model := range []string{spark, wonder} {
		ch := m.charges[i]
		price, _ := spend.PriceOf(model)
		if want := price.AttemptBound(100_000, PlannerMaxOutputTokens); ch.NanoUSD != want || ch.Role != RoleInvestigator || ch.Model != model || ch.Attribution != planAttribution {
			t.Errorf("%s: charge %+v, want one attempt of %d", model, ch, want)
		}
	}
	// An invalid answer is settled too.
	b.respond = planAnswerOf("no call")
	if _, err := c.PlanTurn(ctx, planCtx(t)); CodeOf(err) != CodeInvalidOutput || len(m.settled) != 3 {
		t.Errorf("an invalid answer: %v, %d settled", err, len(m.settled))
	}
	// The organization's slot: refused before any quote or reservation.
	hold, entered := make(chan struct{}), make(chan struct{})
	b.respond = func(ctx context.Context, req Request) (Response, error) {
		close(entered) // reserved, and holding the organization's one slot
		<-hold
		return planAnswerOf("", call("c1", "finish", `{"outcome":"no_improvement"}`))(ctx, req)
	}
	one := metered(t, b, m, func(bu *Budget) { bu.MaxConcurrentPerOrganization = 1 })
	done := make(chan error)
	first, second := planCtx(t), planCtx(t)
	go func() { _, err := one.PlanTurn(ctx, first); done <- err }()
	<-entered
	if one.inFlight(orgA) != 1 {
		t.Fatalf("%d calls in flight", one.inFlight(orgA))
	}
	before := len(m.charges)
	if _, err := one.PlanTurn(ctx, second); CodeOf(err) != CodeConcurrencyLimited || len(m.charges) != before {
		t.Errorf("a turn past the slot: %v, %d reservations", err, len(m.charges)-before)
	}
	close(hold)
	if err := <-done; err != nil {
		t.Error(err)
	}
}

// inFlight is the organization's calls in flight (tests only).
func (c *Client) inFlight(org string) int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.active[org]
}

// The note: one line of valid UTF-8, no control, format or separator
// character, at most 500 characters; "" for none.
func TestSanitizeNote(t *testing.T) {
	long := strings.Repeat("a", 499) + " bcd"
	for in, want := range map[string]string{
		"":                              "",
		"  \n\t ":                       "",
		"Try a second tester.":          "Try a second tester.",
		"line one\nline two\r\n\tthree": "line one line two three",
		"rtl \u202Eevil\u202C override": "rtl evil override",
		"zero\u200Bwidth":               "zero width",
		"e\u0301":                       "e\u0301",
		"bad \xff byte":                 "bad \uFFFD byte",
		"para\u2029graph\u2028line":     "para graph line",
		long:                            strings.Repeat("a", 499),
		strings.Repeat("字", 600):        strings.Repeat("字", 500),
	} {
		got := SanitizeNote(in)
		if got != want {
			t.Errorf("SanitizeNote(%q) = %q, want %q", in, got, want)
		}
		if utf8.RuneCountInString(got) > MaxNoteRunes || !utf8.ValidString(got) {
			t.Errorf("SanitizeNote(%q) is out of bounds", in)
		}
	}
}
