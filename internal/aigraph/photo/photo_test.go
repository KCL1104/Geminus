package photo_test

import (
	"context"
	"encoding/json/jsontext"
	"errors"
	"fmt"
	"runtime/debug"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/cloudwego/eino/compose"

	"github.com/kcl1104/geminus/internal/ai/locale"
	"github.com/kcl1104/geminus/internal/ai/provider"
	"github.com/kcl1104/geminus/internal/ai/provider/fake"
	"github.com/kcl1104/geminus/internal/aigraph/photo"
)

// The photo graph's own checks (slice E5): the order of the nodes, every
// way out of them, the two vision calls through the metered Eino ChatModel,
// the caller's context (its cancellation never ends a run between nodes;
// each step meets it as the pipeline did), panics and concurrent runs. The
// photo proposal's steps are internal/ai/photopipeline's; its tests (and the
// generator's and the live eval's photo tests) run them on this graph.

const org = "org_01M4YDQK00TA00000000000000"

func sealed(t *testing.T, key string) provider.Prompt {
	t.Helper()
	v, _ := jsontext.AppendQuote(nil, key)
	p, err := provider.Seal(provider.Prompt{OrganizationID: org, Role: provider.RoleModeler, TemplateVersion: "photo-test", Locale: locale.English,
		System: "PROMPT-SENTINEL rules", Blocks: []provider.Block{{Kind: provider.UntrustedData, Name: fake.IntentBlock, Content: v}}})
	if err != nil {
		t.Fatal(err)
	}
	return p
}

// steps is a scripted photo proposal: stop names the step that decides it
// (the graph ends there), after the first answer's way on; every step
// records itself and the node it ran in.
type steps struct {
	t     *testing.T
	mu    sync.Mutex
	stop  string
	after photo.After
	first *provider.ProposalContext
	again *provider.ProposalContext
	// ran: each step and its node, "step@node".
	ran []string
	// errs: each step's ctx.Err() (nil or not).
	errs []error
	// answers: what each call's step was given.
	answers []provider.ProposalResult
	callErr []error
	panicAt string
	onStep  func(ctx context.Context, step string)
}

func (s *steps) rec(ctx context.Context, step string) bool {
	s.mu.Lock()
	addr := compose.GetCurrentAddress(ctx)
	node := ""
	if len(addr) > 0 {
		node = addr[len(addr)-1].ID
	}
	s.ran = append(s.ran, step+"@"+node)
	s.errs = append(s.errs, ctx.Err())
	s.mu.Unlock()
	if s.onStep != nil {
		s.onStep(ctx, step)
	}
	if s.panicAt == step {
		panic("PANIC-" + step)
	}
	return s.stop != step
}

func (s *steps) Gate(ctx context.Context) bool     { return s.rec(ctx, "gate") }
func (s *steps) Segment(ctx context.Context) bool  { return s.rec(ctx, "segment") }
func (s *steps) Segments(ctx context.Context) bool { return s.rec(ctx, "segments") }
func (s *steps) Context(ctx context.Context) (*provider.ProposalContext, bool) {
	return s.first, s.rec(ctx, "context")
}
func (s *steps) Answered(ctx context.Context, res provider.ProposalResult, err error) photo.After {
	s.answers, s.callErr = append(s.answers, res), append(s.callErr, err)
	if !s.rec(ctx, "answered") {
		return photo.ToEnd
	}
	return s.after
}
func (s *steps) Review(ctx context.Context) bool { return s.rec(ctx, "review") }
func (s *steps) Repair(ctx context.Context) (*provider.ProposalContext, bool) {
	return s.again, s.rec(ctx, "repair")
}
func (s *steps) Repaired(ctx context.Context, res provider.ProposalResult, err error) {
	s.answers, s.callErr = append(s.answers, res), append(s.callErr, err)
	s.rec(ctx, "repaired")
}

// spy is a vision client that records where each call ran and answers
// from a script.
type spy struct {
	mu     sync.Mutex
	nodes  []string
	keys   []string
	errs   []error
	answer func(pc provider.ProposalContext) (provider.ProposalResult, error)
	panics bool
}

func (v *spy) GenerateModelProposal(ctx context.Context, pc provider.ProposalContext) (provider.ProposalResult, error) {
	v.mu.Lock()
	addr := compose.GetCurrentAddress(ctx)
	if len(addr) > 0 {
		v.nodes = append(v.nodes, addr[len(addr)-1].ID)
	}
	v.keys = append(v.keys, pc.Prompt.InputHash)
	v.errs = append(v.errs, ctx.Err())
	v.mu.Unlock()
	if v.panics {
		panic("PANIC-vision")
	}
	if v.answer != nil {
		return v.answer(pc)
	}
	return provider.ProposalResult{Metadata: provider.Metadata{InputHash: pc.Prompt.InputHash, Role: provider.RoleModeler}}, nil
}
func (v *spy) ProposeImportMapping(context.Context, provider.MappingContext) (provider.MappingResult, error) {
	return provider.MappingResult{}, errors.New("not a photo call")
}
func (v *spy) AnalyzeSimulation(context.Context, provider.AnalysisContext) (provider.AnalysisResult, error) {
	return provider.AnalysisResult{}, errors.New("not a photo call")
}

func newSteps(t *testing.T) *steps {
	first, again := &provider.ProposalContext{OrganizationID: org, Mode: provider.ModeEdit, Prompt: sealed(t, "first")},
		&provider.ProposalContext{OrganizationID: org, Mode: provider.ModeEdit, Prompt: sealed(t, "again")}
	return &steps{t: t, first: first, again: again}
}

// Every way through the graph: the steps run in their own nodes, in order,
// up to the one that decides the proposal; the vision model is asked in the
// generate nodes only, with the steps' calls, and each call's result goes to
// the step after it.
func TestTheGraphsPaths(t *testing.T) {
	cases := []struct {
		name  string
		stop  string
		after photo.After
		want  []string
		calls []string
	}{
		{"refused before anything is spent", "gate", 0, []string{"gate@gate"}, nil},
		{"the segmenter decides", "segment", 0, []string{"gate@gate", "segment@segment"}, nil},
		{"the segmentation's checks decide", "segments", 0, []string{"gate@gate", "segment@segment", "segments@check_segments"}, nil},
		{"no context", "context", 0, []string{"gate@gate", "segment@segment", "segments@check_segments", "context@build_context"}, nil},
		{"the first call decides", "answered", 0,
			[]string{"gate@gate", "segment@segment", "segments@check_segments", "context@build_context", "answered@classify"}, []string{"generate"}},
		{"an error after the first call", "", photo.ToEnd,
			[]string{"gate@gate", "segment@segment", "segments@check_segments", "context@build_context", "answered@classify"}, []string{"generate"}},
		{"the review decides", "review", photo.ToReview,
			[]string{"gate@gate", "segment@segment", "segments@check_segments", "context@build_context", "answered@classify", "review@review"},
			[]string{"generate"}},
		{"nothing to repair", "repair", photo.ToReview,
			[]string{"gate@gate", "segment@segment", "segments@check_segments", "context@build_context", "answered@classify", "review@review",
				"repair@repair_context"}, []string{"generate"}},
		{"reviewed, then repaired", "", photo.ToReview,
			[]string{"gate@gate", "segment@segment", "segments@check_segments", "context@build_context", "answered@classify", "review@review",
				"repair@repair_context", "repaired@settle"}, []string{"generate", "generate_2"}},
		{"refused by the client, then repaired (no review)", "", photo.ToRepair,
			[]string{"gate@gate", "segment@segment", "segments@check_segments", "context@build_context", "answered@classify",
				"repair@repair_context", "repaired@settle"}, []string{"generate", "generate_2"}},
		{"refused by the client, nothing to repair", "repair", photo.ToRepair,
			[]string{"gate@gate", "segment@segment", "segments@check_segments", "context@build_context", "answered@classify",
				"repair@repair_context"}, []string{"generate"}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			s := newSteps(t)
			s.stop, s.after = c.stop, c.after
			v := &spy{}
			if err := photo.Run(context.Background(), v, s); err != nil {
				t.Fatal(err)
			}
			if !slices.Equal(s.ran, c.want) {
				t.Errorf("ran %v, want %v", s.ran, c.want)
			}
			if !slices.Equal(v.nodes, c.calls) {
				t.Errorf("vision calls in %v, want %v", v.nodes, c.calls)
			}
			want := []string{s.first.Prompt.InputHash, s.again.Prompt.InputHash}[:len(c.calls)]
			if !slices.Equal(v.keys, want) {
				t.Errorf("the calls asked %v, want the steps' %v", v.keys, want)
			}
			if len(s.answers) != len(c.calls) {
				t.Fatalf("%d answers given to the steps, %d calls", len(s.answers), len(c.calls))
			}
			for i, a := range s.answers {
				if a.Metadata.InputHash != want[i] || s.callErr[i] != nil {
					t.Errorf("answer %d: %+v %v", i, a.Metadata, s.callErr[i])
				}
			}
		})
	}
}

// The calls go through the metered Eino ChatModel over the client: the
// client's request is exactly the steps' call, its result (and an
// invalid_output answer handed back for the repair, with the client's own
// error) goes to the step as the client returned it.
func TestTheCallsAreTheClients(t *testing.T) {
	f := fake.New()
	c, err := provider.New(f, provider.DefaultBudget())
	if err != nil {
		t.Fatal(err)
	}
	s := newSteps(t)
	s.after = photo.ToRepair
	// The first answer refused for what it says (schema-invalid), the
	// repair's needs_input.
	f.Script(provider.RoleModeler, "first", []byte(`{"status":"proposed","summary":"ANSWER-SENTINEL"}`))
	f.Script(provider.RoleModeler, "again", []byte(`{"status":"needs_input","summary":"s","assumptions":[],"questions":["q"],"requires_confirmation":true}`))
	if err := photo.Run(context.Background(), c, s); err != nil {
		t.Fatal(err)
	}
	direct, derr := c.GenerateModelProposal(context.Background(), *s.first)
	if len(s.answers) != 2 || provider.CodeOf(s.callErr[0]) != provider.CodeInvalidOutput || s.callErr[0].Error() != derr.Error() ||
		!s.answers[0].Prior.Present() || s.answers[0].Prior.Len() != direct.Prior.Len() || s.answers[0].Metadata.InputHash != direct.Metadata.InputHash {
		t.Fatalf("the first call: %+v %v", s.answers, s.callErr)
	}
	var pe *provider.Error
	if !errors.As(s.callErr[0], &pe) || fmt.Sprintf("%T", s.callErr[0]) != "*provider.Error" {
		t.Errorf("the first call's error is not the client's own: %T", s.callErr[0])
	}
	if s.callErr[1] != nil || s.answers[1].Output.Status != "needs_input" || s.answers[1].Metadata.InputHash != s.again.Prompt.InputHash {
		t.Errorf("the second call: %+v %v", s.answers[1], s.callErr[1])
	}
	reqs := f.Requests()
	if len(reqs) != 3 || reqs[0].Prompt.InputHash != s.first.Prompt.InputHash || reqs[1].Prompt.InputHash != s.again.Prompt.InputHash {
		t.Errorf("requests %d", len(reqs))
	}
}

type ctxKey struct{}

// The caller's context: a run is never ended between nodes by the caller's
// cancellation (each step meets it, as the pipeline's code did: a client
// answers it cancelled), each step sees the caller's values and deadline,
// and a context a step derives ends when the caller's does.
func TestTheCallersContext(t *testing.T) {
	s := newSteps(t)
	s.after = photo.ToReview
	deadline := time.Now().Add(time.Hour)
	ctx, stop := context.WithDeadline(context.WithValue(context.Background(), ctxKey{}, "caller"), deadline)
	defer stop()
	ctx, cancel := context.WithCancelCause(ctx)
	stopped := errors.New("the lease is gone")
	var derived []context.Context
	s.onStep = func(c context.Context, step string) {
		if c.Value(ctxKey{}) != "caller" {
			t.Errorf("%s: the caller's value is not seen", step)
		}
		if d, ok := c.Deadline(); !ok || !d.Equal(deadline) {
			t.Errorf("%s: deadline %v %v", step, d, ok)
		}
		if len(compose.GetCurrentAddress(c)) == 0 {
			t.Errorf("%s: the node's values are not seen", step)
		}
		if step == "segment" {
			// A context the step derives (as a client derives each
			// attempt's) ends with the caller's at once, as it did when the
			// pipeline handed the caller's on: a call made just after the
			// cancellation sees it.
			child, done := context.WithTimeout(c, time.Hour)
			t.Cleanup(done)
			derived = append(derived, child)
			// Cancelled while the segmenter works.
			cancel(stopped)
			if child.Err() == nil {
				t.Error("a context the step derived did not end at once with the caller's")
			}
		}
		if step != "gate" && step != "segment" && context.Cause(c) != stopped {
			t.Errorf("%s: the cause %v", step, context.Cause(c))
		}
	}
	v := &spy{}
	if err := photo.Run(ctx, v, s); err != nil {
		t.Fatal(err)
	}
	want := []string{"gate@gate", "segment@segment", "segments@check_segments", "context@build_context", "answered@classify", "review@review",
		"repair@repair_context", "repaired@settle"}
	if !slices.Equal(s.ran, want) {
		t.Fatalf("ran %v: the graph ended the run on the caller's cancellation", s.ran)
	}
	for i, e := range s.errs {
		if i >= 2 && !errors.Is(e, context.Canceled) {
			t.Errorf("%s saw %v", s.ran[i], e)
		}
	}
	if len(v.errs) != 2 || !errors.Is(v.errs[0], context.Canceled) || !errors.Is(v.errs[1], context.Canceled) {
		t.Errorf("the vision calls saw %v", v.errs)
	}
	if len(derived) != 1 || !errors.Is(derived[0].Err(), context.Canceled) {
		t.Errorf("derived %v", derived)
	}

	// Cancelled before the run: every step still runs as it decides.
	s = newSteps(t)
	s.stop = "answered"
	if err := photo.Run(ctx, v, s); err != nil || len(s.ran) != 5 {
		t.Errorf("a cancelled caller: %v %v", err, s.ran)
	}
}

// A step that panics (or the vision client) panics the caller with the
// same value, as the pipeline's own code did, so the worker's handler
// treats it as before (a bug, not retried); no step after it runs, and
// Eino's own panic error (whose text holds the stack) never comes out.
func TestAPanicIsTheCallers(t *testing.T) {
	for _, at := range []string{"gate", "segment", "context", "answered", "review", "repair", "repaired"} {
		t.Run(at, func(t *testing.T) {
			s := newSteps(t)
			s.after, s.panicAt = photo.ToReview, at
			v := &spy{}
			got := func() (p any) {
				defer func() { p = recover() }()
				_ = photo.Run(context.Background(), v, s)
				return nil
			}()
			if got != "PANIC-"+at {
				t.Fatalf("panicked with %v", got)
			}
			if last := s.ran[len(s.ran)-1]; !strings.HasPrefix(last, at+"@") {
				t.Errorf("ran %v after the panic", s.ran)
			}
		})
	}
	s := newSteps(t)
	s.after = photo.ToReview
	got := func() (p any) {
		defer func() { p = recover() }()
		_ = photo.Run(context.Background(), &spy{panics: true}, s)
		return nil
	}()
	if got != "PANIC-vision" || len(s.ran) != 4 {
		t.Errorf("the vision client's panic: %v, ran %v", got, s.ran)
	}
}

// E5 review round 1: the stack a caller takes where it recovers a step's
// panic (as the worker's handler does, internal/worker handle, and logs as
// "stack") shows where the panic was raised, as it did before the graph: the
// panic is never recovered and raised again on the way, which would start
// the stack at Run.
func TestAPanicKeepsItsStack(t *testing.T) {
	// raisedIn is the first frame below the panic's own that is not the
	// runtime's.
	raisedIn := func(stack string) string {
		lines := strings.Split(stack, "\n")
		for i, l := range lines {
			if !strings.HasPrefix(l, "panic(") {
				continue
			}
			for _, f := range lines[i+1:] {
				if f == "" || strings.HasPrefix(f, "\t") || strings.HasPrefix(f, "runtime.") {
					continue
				}
				if j := strings.LastIndex(f, "("); j > 0 {
					f = f[:j]
				}
				return f
			}
		}
		return ""
	}
	recovered := func(run func()) (v any, stack string) {
		defer func() {
			v, stack = recover(), string(debug.Stack())
		}()
		run()
		return nil, ""
	}
	const steps = "github.com/kcl1104/geminus/internal/aigraph/photo_test.(*steps)."
	for _, c := range []struct{ at, method string }{
		{"gate", "Gate"}, {"segment", "Segment"}, {"segments", "Segments"}, {"context", "Context"}, {"answered", "Answered"},
		{"review", "Review"}, {"repair", "Repair"}, {"repaired", "Repaired"},
	} {
		t.Run(c.at, func(t *testing.T) {
			s := newSteps(t)
			s.after, s.panicAt = photo.ToReview, c.at
			v, stack := recovered(func() { _ = photo.Run(context.Background(), &spy{}, s) })
			if v != "PANIC-"+c.at {
				t.Fatalf("panicked with %v", v)
			}
			if got := raisedIn(stack); got != steps+"rec" || !strings.Contains(stack, "\n"+steps+c.method+"(") {
				t.Errorf("the stack shows the panic raised in %q, not in the step %s:\n%s", got, c.method, stack)
			}
		})
	}
	s := newSteps(t)
	s.after = photo.ToReview
	v, stack := recovered(func() { _ = photo.Run(context.Background(), &spy{panics: true}, s) })
	if got := raisedIn(stack); v != "PANIC-vision" || got != "github.com/kcl1104/geminus/internal/aigraph/photo_test.(*spy).GenerateModelProposal" {
		t.Errorf("the vision client's panic %v raised in %q:\n%s", v, got, stack)
	}
}

// Runs share the compiled graph and nothing else.
func TestConcurrentRuns(t *testing.T) {
	var wg sync.WaitGroup
	stops := []string{"gate", "segments", "answered", "review", ""}
	results := make([]*steps, 32)
	for i := range results {
		s := newSteps(t)
		s.stop, s.after = stops[i%len(stops)], photo.ToReview
		results[i] = s
		wg.Add(1)
		go func() {
			defer wg.Done()
			if err := photo.Run(context.Background(), &spy{}, s); err != nil {
				t.Error(err)
			}
		}()
	}
	wg.Wait()
	lens := map[string]int{"gate": 1, "segments": 3, "answered": 5, "review": 6, "": 8}
	for i, s := range results {
		if len(s.ran) != lens[s.stop] {
			t.Errorf("run %d (%q): %v", i, s.stop, s.ran)
		}
	}
}

// No steps, no run.
func TestNoSteps(t *testing.T) {
	if err := photo.Run(context.Background(), &spy{}, nil); !errors.Is(err, photo.ErrGraph) {
		t.Errorf("%v", err)
	}
}
