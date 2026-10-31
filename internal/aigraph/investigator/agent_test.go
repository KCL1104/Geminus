package investigator_test

import (
	"context"
	"encoding/json/jsontext"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/kcl1104/geminus/internal/ai/locale"
	"github.com/kcl1104/geminus/internal/ai/provider"
	"github.com/kcl1104/geminus/internal/aigraph/einomodel"
	"github.com/kcl1104/geminus/internal/aigraph/investigator"
)

// H-061 §5.3, slice A6: one ADK run of the investigator agent over the
// planner, with a scripted planner client and a test environment (no
// database, no model).

const org = "org_01M4YDQK00TA00000000000000"

var attr = provider.Attribution{TargetType: provider.PlannerTarget, TargetID: "inv_01M4YDQK00TA00000000000001",
	RequestID: "req_01M4YDQK00TA00000000000001"}

func tools() []provider.Tool {
	obj := jsontext.Value(`{"type":"object","properties":{},"additionalProperties":false}`)
	return []provider.Tool{{Name: "propose_what_if", Description: "Propose.", InputSchema: obj},
		{Name: "save_scenario", Description: "Save.", InputSchema: obj},
		{Name: "finish", Description: "Finish.", InputSchema: obj}}
}

func sealed(t *testing.T, digest int) provider.Prompt {
	t.Helper()
	p, err := provider.Seal(provider.Prompt{OrganizationID: org, Role: provider.RoleInvestigator, TemplateVersion: "investigator-0.1.0",
		Locale: locale.English, System: "rules", Blocks: []provider.Block{{Kind: provider.UntrustedData, Name: "PERSON_REQUEST",
			Content: jsontext.Value(`"cut the lead time"`)}, {Kind: provider.ServerData, Name: "STEP", Content: jsontext.Value([]byte{byte('0' + digest)})}}})
	if err != nil {
		t.Fatal(err)
	}
	return p
}

// scripted is a planner client answering its script in order.
type scripted struct {
	mu     sync.Mutex
	script []answer
	asked  []provider.PlanContext
}

type answer struct {
	res provider.PlanResult
	err error
}

func call(id, name string) answer {
	return answer{res: provider.PlanResult{Call: provider.ToolCall{ID: id, Name: name, Input: jsontext.Value(`{}`)}}}
}

func (s *scripted) PlanTurn(ctx context.Context, pc provider.PlanContext) (provider.PlanResult, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.asked = append(s.asked, pc)
	if err := ctx.Err(); err != nil {
		return provider.PlanResult{}, provider.NewError(provider.CodeCancelled, 0, "").WithCause(err)
	}
	if len(s.script) == 0 {
		return provider.PlanResult{}, provider.NewError(provider.CodeUnavailable, 503, "")
	}
	a := s.script[0]
	s.script = s.script[1:]
	return a.res, a.err
}

// env is a test environment: every turn offers offered (all tools when
// nil); carry answers each call.
type env struct {
	t       *testing.T
	mu      sync.Mutex
	turn    int
	offered func(turn int) []provider.Tool
	carry   func(ctx context.Context, res provider.PlanResult) (investigator.Observed, error)
	next    func(turn int) error
	carried []string
}

func (e *env) Next(context.Context) (*provider.PlanContext, error) {
	e.mu.Lock()
	defer e.mu.Unlock()
	if e.next != nil {
		if err := e.next(e.turn); err != nil {
			return nil, err
		}
	}
	ts := tools()
	if e.offered != nil {
		ts = e.offered(e.turn)
	}
	pc := &provider.PlanContext{OrganizationID: org, Prompt: sealed(e.t, e.turn), Attribution: attr, Model: "muse-spark-1.3", Tools: ts}
	e.turn++
	return pc, nil
}

func (e *env) Carry(ctx context.Context, res provider.PlanResult) (investigator.Observed, error) {
	e.mu.Lock()
	e.carried = append(e.carried, res.Call.Name)
	e.mu.Unlock()
	if e.carry != nil {
		return e.carry(ctx, res)
	}
	return investigator.Observed{Content: `{"ok":true}`}, nil
}

func runWith(t *testing.T, ctx context.Context, s *scripted, e *env, steps int) investigator.Outcome {
	t.Helper()
	p, err := einomodel.NewPlanner(s)
	if err != nil {
		t.Fatal(err)
	}
	e.t = t
	return investigator.Run(ctx, investigator.Config{Planner: p, Tools: tools(), StepsLeft: steps, Env: e})
}

func TestTheAgentCarriesTheCallsAndFinishes(t *testing.T) {
	s := &scripted{script: []answer{call("toolu_1", "propose_what_if"), call("toolu_2", "save_scenario"), call("toolu_3", "finish")}}
	e := &env{}
	o := runWith(t, context.Background(), s, e, 14)
	if o.Kind != investigator.Finished || o.Finish == nil || o.Finish.Call.ID != "toolu_3" || o.Turns != 3 {
		t.Fatalf("outcome %+v", o)
	}
	if got := e.carried; len(got) != 3 || got[0] != "propose_what_if" || got[1] != "save_scenario" || got[2] != "finish" {
		t.Fatalf("carried %v", got)
	}
	// Every turn saw a fresh transcript of its own (the planner's tripwire
	// passed on each), and no turn after finish was asked.
	if len(s.asked) != 3 || s.asked[0].Prompt.InputHash == s.asked[1].Prompt.InputHash {
		t.Fatalf("asked %d turns", len(s.asked))
	}
}

// The budget narrows the tools a turn offers: once only finish is left, the
// planner is sent only finish (the tripwire holds the state's ToolInfos to
// the turn's).
func TestTheBudgetNarrowsTheTools(t *testing.T) {
	s := &scripted{script: []answer{call("toolu_1", "finish")}}
	e := &env{offered: func(int) []provider.Tool { return tools()[2:] }}
	o := runWith(t, context.Background(), s, e, 1)
	if o.Kind != investigator.Finished || len(s.asked) != 1 || len(s.asked[0].Tools) != 1 || s.asked[0].Tools[0].Name != "finish" {
		t.Fatalf("outcome %+v, asked %+v", o, s.asked)
	}
}

func TestOutcomes(t *testing.T) {
	inv := &provider.InvalidTurn{Kind: provider.InvalidArguments, Code: "INVALID_ARGUMENTS", Path: "/idea"}
	cases := map[string]struct {
		script []answer
		env    *env
		steps  int
		check  func(t *testing.T, o investigator.Outcome, e *env)
	}{
		"an invalid answer": {script: []answer{{res: provider.PlanResult{Invalid: inv, Metadata: provider.Metadata{Model: "muse-spark-1.3"}},
			err: provider.NewError(provider.CodeInvalidOutput, 0, "")}}, steps: 14,
			check: func(t *testing.T, o investigator.Outcome, _ *env) {
				if o.Kind != investigator.Invalid || o.Invalid == nil || o.Invalid.Path != "/idea" || o.Turns != 1 {
					t.Fatalf("outcome %+v", o)
				}
			}},
		// errors.As reaches the client's error through Eino's wrapping.
		"a failed turn": {script: []answer{{err: provider.NewError(provider.CodeUnavailable, 503, "")}}, steps: 14,
			check: func(t *testing.T, o investigator.Outcome, _ *env) {
				var pe *provider.Error
				if o.Kind != investigator.Failed || !errors.As(o.Err, &pe) || pe.Code != provider.CodeUnavailable || o.Turns != 1 {
					t.Fatalf("outcome %+v", o)
				}
			}},
		"a turn refused before any I/O": {script: []answer{{err: provider.NewError(provider.CodeConcurrencyLimited, 0, "")}}, steps: 14,
			check: func(t *testing.T, o investigator.Outcome, _ *env) {
				if o.Kind != investigator.Failed || provider.CodeOf(o.Err) != provider.CodeConcurrencyLimited || o.Turns != 0 {
					t.Fatalf("outcome %+v", o)
				}
			}},
		"the environment ends it after a call": {script: []answer{call("toolu_1", "propose_what_if"), call("toolu_2", "finish")},
			env: &env{carry: func(context.Context, provider.PlanResult) (investigator.Observed, error) {
				return investigator.Observed{}, &investigator.End{Reason: "draft_written"}
			}}, steps: 14,
			check: func(t *testing.T, o investigator.Outcome, e *env) {
				if o.Kind != investigator.Ended || o.End.Reason != "draft_written" || len(e.carried) != 1 {
					t.Fatalf("outcome %+v, carried %v", o, e.carried)
				}
			}},
		"the environment ends it before a turn": {script: []answer{call("toolu_1", "finish")},
			env: &env{next: func(int) error { return &investigator.End{Reason: "stopped_by_person"} }}, steps: 14,
			check: func(t *testing.T, o investigator.Outcome, e *env) {
				if o.Kind != investigator.Ended || o.End.Reason != "stopped_by_person" || len(e.carried) != 0 || o.Turns != 0 {
					t.Fatalf("outcome %+v", o)
				}
			}},
		"the iterations are used": {script: []answer{call("toolu_1", "propose_what_if"), call("toolu_2", "propose_what_if")}, steps: 1,
			check: func(t *testing.T, o investigator.Outcome, e *env) {
				if o.Kind != investigator.LimitReached || len(e.carried) != 1 {
					t.Fatalf("outcome %+v, carried %v", o, e.carried)
				}
			}},
		"the environment panics": {script: []answer{call("toolu_1", "propose_what_if")},
			env: &env{carry: func(context.Context, provider.PlanResult) (investigator.Observed, error) { panic("boom") }}, steps: 14,
			check: func(t *testing.T, o investigator.Outcome, _ *env) {
				if o.Kind != investigator.Internal || o.Err == nil {
					t.Fatalf("outcome %+v", o)
				}
				if contains(o.Err.Error(), "boom") {
					t.Fatal("the panic's value reached the outcome")
				}
			}},
		"an internal error of the environment": {script: []answer{call("toolu_1", "propose_what_if")},
			env: &env{carry: func(context.Context, provider.PlanResult) (investigator.Observed, error) {
				return investigator.Observed{}, errors.New("SECRET-SENTINEL ledger")
			}}, steps: 14,
			check: func(t *testing.T, o investigator.Outcome, _ *env) {
				if o.Kind != investigator.Internal || contains(o.Err.Error(), "SECRET-SENTINEL") {
					t.Fatalf("outcome %+v", o)
				}
			}},
	}
	for name, c := range cases {
		t.Run(name, func(t *testing.T) {
			e := c.env
			if e == nil {
				e = &env{}
			}
			c.check(t, runWith(t, context.Background(), &scripted{script: c.script}, e, c.steps), e)
		})
	}
}

// Cancellation reaches a blocked tool: the run ends Cancelled and the
// tool's body returns.
func TestCancellationReachesABlockedTool(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	blocked, returned := make(chan struct{}), make(chan struct{})
	e := &env{carry: func(ctx context.Context, _ provider.PlanResult) (investigator.Observed, error) {
		close(blocked)
		<-ctx.Done()
		close(returned)
		return investigator.Observed{}, ctx.Err()
	}}
	go func() { <-blocked; cancel() }()
	done := make(chan investigator.Outcome, 1)
	go func() {
		done <- runWith(t, ctx, &scripted{script: []answer{call("toolu_1", "propose_what_if")}}, e, 14)
	}()
	select {
	case o := <-done:
		if o.Kind != investigator.Cancelled {
			t.Fatalf("outcome %+v", o)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("the run did not end")
	}
	select {
	case <-returned:
	default:
		t.Fatal("the tool did not return")
	}
}

func TestTheRunNeedsItsParts(t *testing.T) {
	p, _ := einomodel.NewPlanner(&scripted{})
	for name, cfg := range map[string]investigator.Config{
		"no planner":   {Tools: tools(), StepsLeft: 3, Env: &env{}},
		"no env":       {Planner: p, Tools: tools(), StepsLeft: 3},
		"no step left": {Planner: p, Tools: tools(), StepsLeft: 0, Env: &env{}},
		"no finish":    {Planner: p, Tools: tools()[:2], StepsLeft: 3, Env: &env{}},
	} {
		if o := investigator.Run(context.Background(), cfg); o.Kind != investigator.Internal {
			t.Fatalf("%s: %+v", name, o)
		}
	}
}

func contains(s, sub string) bool {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return true
		}
	}
	return false
}
