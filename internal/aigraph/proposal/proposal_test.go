package proposal

// The proposal role graph (H-059, slice E3) on the deterministic fake, with
// no database: the order its nodes run in, what runs before the paid call,
// how each failure ends the attempt, the one repair (H-057), the live
// eval's path (no hooks, no repair), cancellation and panics. The job
// handler's behaviour through it is internal/proposals/generator's tests,
// unchanged.

import (
	"context"
	"encoding/json/v2"
	"errors"
	"path/filepath"
	"reflect"
	"slices"
	"strconv"
	"strings"
	"sync"
	"testing"

	"github.com/cloudwego/eino/callbacks"
	"github.com/cloudwego/eino/components"
	ecmodel "github.com/cloudwego/eino/components/model"
	"github.com/cloudwego/eino/compose"

	"github.com/kcl1104/geminus/internal/ai/aicontext"
	"github.com/kcl1104/geminus/internal/ai/idpool"
	"github.com/kcl1104/geminus/internal/ai/provider"
	"github.com/kcl1104/geminus/internal/ai/provider/fake"
	"github.com/kcl1104/geminus/internal/ai/repair"
	"github.com/kcl1104/geminus/internal/ai/review"
	"github.com/kcl1104/geminus/internal/aigraph/einomodel"
	"github.com/kcl1104/geminus/internal/contracttest"
	"github.com/kcl1104/geminus/internal/domainpack"
	"github.com/kcl1104/geminus/internal/domainpack/softwaredelivery"
	"github.com/kcl1104/geminus/internal/model"
	"github.com/kcl1104/geminus/internal/model/hashing"
)

const (
	org = "org_01M4YDQK00Y5H2929M9WWZWTVH"
	aip = "aip_01M4YDQK00ADN261CME3WV5HZ6"
	mv  = "mv_01M4YDQK00ADN261CME3WV5HZ6"
	// raise is a valid edit of the default delivery model; broken makes the
	// CI result's outgoing probabilities add up to 1.1 (ROUTING_INVALID),
	// fixed is the same change made whole.
	raise  = `[{"op":"test","path":"/resources/1/id","value":"ci_runner"},{"op":"replace","path":"/resources/1/capacity","value":6}]`
	broken = `[{"op":"test","path":"/flows/3/id","value":"f_pass"},{"op":"replace","path":"/flows/3/routing/probability","value":0.9}]`
	fixed  = `[{"op":"test","path":"/flows/3/id","value":"f_pass"},{"op":"replace","path":"/flows/3/routing/probability","value":0.9},` +
		`{"op":"test","path":"/flows/4/id","value":"f_retry"},{"op":"replace","path":"/flows/4/routing/probability","value":0.1}]`
)

var attribution = provider.Attribution{TargetType: "ai_proposal", TargetID: aip, RequestID: "req_01M4YDQK00ADN261CME3WV5HZ6"}

func packs(t *testing.T) *domainpack.Registry {
	t.Helper()
	r, err := domainpack.NewRegistry(softwaredelivery.Pack{})
	if err != nil {
		t.Fatal(err)
	}
	return r
}

// request is an edit or scenario proposal on the default delivery model,
// as the worker reads it (its base's canonical IR, its stored pool).
func request(t *testing.T, mode, intent string) Request {
	t.Helper()
	var doc map[string]any
	if err := json.Unmarshal(contracttest.ReadFile(t, filepath.Join(contracttest.Root(t), "testdata", "golden-models", "default-delivery.json")), &doc); err != nil {
		t.Fatal(err)
	}
	doc["model_id"] = "mdl_01M4YDQK00ADN261CME3WV5HZ6"
	raw, _ := json.Marshal(doc)
	ir, err := hashing.Canonical(raw)
	if err != nil {
		t.Fatal(err)
	}
	var decoded model.ModelIR
	if err := model.Decode(ir, &decoded); err != nil {
		t.Fatal(err)
	}
	pool, err := idpool.New(&decoded)
	if err != nil {
		t.Fatal(err)
	}
	poolJSON, _ := json.Marshal(pool)
	return Request{
		Scope:        aicontext.Scope{OrganizationID: org, ModelID: decoded.ModelID},
		Mode:         provider.Mode(mode),
		Base:         Base{OrganizationID: org, ModelID: decoded.ModelID, VersionID: mv, IR: ir},
		Intent:       intent,
		ProposalID:   aip,
		Pool:         poolJSON,
		OutputLocale: "en",
		Attribution:  attribution,
		Repair:       true,
	}
}

func proposed(changes string) []byte {
	var c any
	_ = json.Unmarshal([]byte(changes), &c)
	data, _ := json.Marshal(map[string]any{"status": "proposed", "summary": "s", "assumptions": []string{}, "risk_level": "low", "confidence": 0.9,
		"changes": c, "expected_effects": []string{"Waiting may fall; a simulation will tell."}, "requires_confirmation": true})
	return data
}

func client(t *testing.T, b provider.Backend) *provider.Client {
	t.Helper()
	c, err := provider.New(b, provider.DefaultBudget())
	if err != nil {
		t.Fatal(err)
	}
	return c
}

func graphOf(t *testing.T, p provider.LLMProvider) *Graph {
	t.Helper()
	m, err := einomodel.New(p)
	if err != nil {
		t.Fatal(err)
	}
	g, err := New(Config{Model: m, Packs: packs(t), Budget: provider.DefaultBudget()})
	if err != nil {
		t.Fatal(err)
	}
	return g
}

// tracer records the role graph's nodes as they start, and the components
// the model call starts, from a handler attached to one run only.
type tracer struct {
	mu     sync.Mutex
	nodes  []string
	models []string
	inputs []callbacks.CallbackInput
}

func (tr *tracer) option() compose.Option {
	h := callbacks.NewHandlerBuilder().OnStartFn(func(ctx context.Context, info *callbacks.RunInfo, in callbacks.CallbackInput) context.Context {
		tr.mu.Lock()
		defer tr.mu.Unlock()
		switch info.Component {
		case compose.ComponentOfLambda:
			tr.nodes = append(tr.nodes, info.Name)
		case components.ComponentOfChatModel:
			tr.models = append(tr.models, info.Name+"/"+info.Type)
			tr.inputs = append(tr.inputs, in)
		}
		return ctx
	}).Build()
	return compose.WithCallbacks(h)
}

// hooks are the job handler's I/O stand-ins: each notes the requests the
// backend had seen when it ran.
type hooks struct {
	f                       *fake.Backend
	preloaded, verified     []int
	bound                   []int
	answered                []int
	answerErrs              []error
	preloadErr, verifyErr   error
	bindErr                 error
	bindIssues              []model.ValidationIssue
	manifest                []model.DatasetBinding
	bindPanics, verifyPanic bool
}

func (h *hooks) Hooks() Hooks {
	return Hooks{
		Preload: func(ctx context.Context) error {
			h.preloaded = append(h.preloaded, len(h.f.Requests()))
			return h.preloadErr
		},
		Verify: func(ctx context.Context, base *model.ModelIR) ([]model.DatasetBinding, error) {
			if h.verifyPanic {
				panic("verify panicked")
			}
			h.verified = append(h.verified, len(h.f.Requests()))
			if base == nil || base.ModelID == "" {
				return nil, errors.New("no base")
			}
			return h.manifest, h.verifyErr
		},
		Bind: func(ctx context.Context, candidate *model.ModelIR) ([]model.ValidationIssue, error) {
			if h.bindPanics {
				panic(errors.New("bind panicked"))
			}
			h.bound = append(h.bound, len(h.f.Requests()))
			return h.bindIssues, h.bindErr
		},
		Answered: func(call int, m provider.Metadata, err error) {
			h.answered = append(h.answered, call)
			h.answerErrs = append(h.answerErrs, err)
		},
	}
}

var firstPath = []string{NodeDecode, NodeContext, NodePreload, NodeVerify, NodeGenerate, NodeClassify}

func path(more ...string) []string { return append(slices.Clone(firstPath), more...) }

// The nodes run in the design's order (§6.4, with H-057's branch): what the
// server reads and checks before the paid call, the call, its
// classification, the review, the binding stage, then the one repair; each
// outcome or failure ends where it is decided.
func TestNodeOrder(t *testing.T) {
	ctx := context.Background()
	for name, c := range map[string]struct {
		script  func(*fake.Backend, string)
		hooks   func(*hooks)
		want    []string
		status  review.Status
		repairs bool
	}{
		"ready": {script: func(f *fake.Backend, k string) { f.Script(provider.RoleModeler, k, proposed(raise)) },
			want: path(NodeReview, NodeBind, NodeRepairContext), status: review.Ready},
		"needs_input": {script: func(*fake.Backend, string) {},
			want: path(NodeReview, NodeRepairContext), status: review.NeedsInput},
		"refused by the review, then repaired": {script: func(f *fake.Backend, k string) {
			f.Script(provider.RoleModeler, k, proposed(broken))
			f.ScriptRevision(provider.RoleModeler, k, proposed(fixed))
		}, want: path(NodeReview, NodeRepairContext, NodeRepairGenerate, NodeRepairReview), status: review.Ready, repairs: true},
		"refused by the binding stage, then repaired": {script: func(f *fake.Backend, k string) { f.Script(provider.RoleModeler, k, proposed(raise)) },
			hooks: func(h *hooks) {
				h.bindIssues = []model.ValidationIssue{model.NewIssue("DATASET_BINDING_INVALID", "/nodes/1", "x", model.SeverityError)}
			}, want: path(NodeReview, NodeBind, NodeRepairContext, NodeRepairGenerate, NodeRepairReview), status: review.Invalid, repairs: true},
		"refused by the client": {script: func(f *fake.Backend, k string) {
			f.Script(provider.RoleModeler, k, []byte(`{"status":"needs_input","summary":7,"assumptions":[],"questions":["q"],"requires_confirmation":true}`))
		}, want: path(NodeRepairContext, NodeRepairGenerate, NodeRepairReview), status: review.Invalid, repairs: true},
		"a provider failure": {script: func(f *fake.Backend, k string) {
			f.Fail(provider.RoleModeler, k, provider.NewError(provider.CodeRateLimited, 429, "org-a"))
		}, want: path()},
	} {
		t.Run(name, func(t *testing.T) {
			intent := "Case " + name + "."
			f := fake.New()
			c.script(f, intent)
			h := &hooks{f: f}
			if c.hooks != nil {
				c.hooks(h)
			}
			tr := &tracer{}
			res, err := graphOf(t, client(t, f)).run(ctx, request(t, "edit", intent), h.Hooks(), tr.option())
			if !reflect.DeepEqual(tr.nodes, c.want) {
				t.Errorf("nodes %v, want %v", tr.nodes, c.want)
			}
			if c.status == "" {
				var ce *CallError
				if !errors.As(err, &ce) {
					t.Fatalf("%v", err)
				}
				return
			}
			if err != nil || res.Outcome.Status != c.status || res.Call == nil || (res.Repair != nil) != c.repairs {
				t.Fatalf("%+v %v %+v", res.Outcome.Status, err, res.Repair)
			}
			calls := 1
			if c.repairs {
				calls = 2
			}
			if n := len(f.Requests()); n != calls || len(tr.models) != calls || !slices.Equal(h.answered, []int{1, 2}[:calls]) {
				t.Errorf("%d requests, models %v, answered %v", n, tr.models, h.answered)
			}
			for _, m := range tr.models {
				if m != NodeModel+"/GeminusMetered" {
					t.Errorf("the model's callbacks ran as %s", m)
				}
			}
		})
	}
}

// What the server reads and checks before the paid call runs before it:
// the snapshots are read and the base verified before the backend is asked,
// and a failure of either ends the attempt with the hook's own error and
// no call; nothing is answered.
func TestPreloadAndVerifyRunBeforeThePaidCall(t *testing.T) {
	ctx := context.Background()
	intent := "Six runners."
	f := fake.New()
	f.Script(provider.RoleModeler, intent, proposed(raise))
	h := &hooks{f: f, manifest: []model.DatasetBinding{{LogicalRef: "x"}}}
	g := graphOf(t, client(t, f))
	res, err := g.Run(ctx, request(t, "edit", intent), h.Hooks())
	if err != nil || !slices.Equal(h.preloaded, []int{0}) || !slices.Equal(h.verified, []int{0}) || !slices.Equal(h.bound, []int{1}) {
		t.Fatalf("%v: preloaded %v verified %v bound %v", err, h.preloaded, h.verified, h.bound)
	}
	if !reflect.DeepEqual(res.Manifest, h.manifest) {
		t.Errorf("manifest %+v", res.Manifest)
	}
	for name, set := range map[string]func(*hooks, error){
		"preload": func(h *hooks, err error) { h.preloadErr = err },
		"verify":  func(h *hooks, err error) { h.verifyErr = err },
	} {
		f := fake.New()
		f.Script(provider.RoleModeler, intent, proposed(raise))
		h := &hooks{f: f}
		own := errors.New(name + " failed")
		set(h, own)
		res, err := graphOf(t, client(t, f)).Run(ctx, request(t, "edit", intent), h.Hooks())
		if err != own || len(f.Requests()) != 0 || h.answered != nil || res.Call != nil {
			t.Errorf("%s: %v, %d requests, answered %v", name, err, len(f.Requests()), h.answered)
		}
	}
}

// The client's refusal of an answer for what it says is an invalid outcome
// with the client's issues: the review never sees it, and the binding stage
// does not run.
func TestInvalidOutputIsNotReviewed(t *testing.T) {
	intent := "Approve it."
	f := fake.New()
	f.Script(provider.RoleModeler, intent, []byte(`{"status":"needs_input","summary":7,"assumptions":[],"questions":["q"],"requires_confirmation":true}`))
	h := &hooks{f: f}
	req := request(t, "edit", intent)
	req.Repair = false
	tr := &tracer{}
	res, err := graphOf(t, client(t, f)).run(context.Background(), req, h.Hooks(), tr.option())
	if err != nil || res.Outcome.Status != review.Invalid || res.Outcome.Validation.Issues[0].Code != review.OutputInvalid || res.Outcome.Changes != nil {
		t.Fatalf("%+v %v", res.Outcome, err)
	}
	if slices.Contains(tr.nodes, NodeReview) || slices.Contains(tr.nodes, NodeBind) || h.bound != nil || res.Repair != nil {
		t.Errorf("nodes %v, bound %v", tr.nodes, h.bound)
	}
}

// A provider failure is a *CallError over the client's error, with the
// call's metadata; a research attempt's and a slow model's are final
// (their jobs have one attempt), any other's is not.
func TestProviderFailures(t *testing.T) {
	ctx := context.Background()
	for _, c := range []struct {
		research bool
		model    string
		final    bool
	}{{false, "", false}, {true, "", true}, {false, "muse-wonder-1.0", true}, {true, "muse-wonder-1.0", true}} {
		intent := "Busy."
		f := fake.New()
		f.Fail(provider.RoleModeler, intent, provider.NewError(provider.CodeUnavailable, 503, "meta"))
		req := request(t, "edit", intent)
		req.Research, req.Model = c.research, c.model
		h := &hooks{f: f}
		res, err := graphOf(t, client(t, f)).Run(ctx, req, h.Hooks())
		var ce *CallError
		var pe *provider.Error
		if !errors.As(err, &ce) || ce.Final != c.final || !errors.As(err, &pe) || pe.Code != provider.CodeUnavailable || ce.Err != h.answerErrs[0] {
			t.Errorf("%+v: %v", c, err)
		}
		if res.Call == nil || res.Call.Role != provider.RoleModeler || res.Repair != nil || len(f.Requests()) != 1 {
			t.Errorf("%+v: %+v, %d requests", c, res.Call, len(f.Requests()))
		}
	}
}

// The binding stage failing after the answer is a *BindError over the
// hook's error, after one call; the review failing is a *ReviewError.
func TestServerFaultsAfterTheAnswer(t *testing.T) {
	intent := "Six runners."
	f := fake.New()
	f.Script(provider.RoleModeler, intent, proposed(raise))
	own := errors.New("the binding stage failed")
	h := &hooks{f: f, bindErr: own}
	res, err := graphOf(t, client(t, f)).Run(context.Background(), request(t, "edit", intent), h.Hooks())
	var be *BindError
	if !errors.As(err, &be) || be.Err != own || be.Repair || res.Call == nil || len(f.Requests()) != 1 {
		t.Fatalf("%v %+v", err, res.Call)
	}
	// The review that cannot run (a pool it refuses, which the context
	// builder never lets through): the server's own fault.
	pack, _ := packs(t).Lookup("software_delivery")
	fl := &flow{req: request(t, "edit", intent), pack: pack}
	graphOf(t, client(t, fake.New())).reviewFirst(context.Background(), fl)
	var re *ReviewError
	if !errors.As(fl.err, &re) || re.Repair || re.Err == nil {
		t.Errorf("a review that cannot run: %v", fl.err)
	}
}

// What the server stored that cannot be read ends the attempt before any
// call, with which part it was; so does a request the context builder
// refuses.
func TestInputsThatCannotBeRead(t *testing.T) {
	ctx := context.Background()
	for name, c := range map[string]struct {
		edit func(*Request)
		want Input
		code aicontext.Code
	}{
		"the base": {edit: func(r *Request) { r.Base.IR = []byte(`{"not":"a model"`) }, want: InputBase},
		"the domain": {edit: func(r *Request) {
			r.Base.IR = []byte(strings.Replace(string(r.Base.IR), `"domain":"software_delivery"`, `"domain":"no_such_pack"`, 1))
		}, want: InputDomain},
		"the pool":   {edit: func(r *Request) { r.Pool = []byte(`[`) }, want: InputPool},
		"the locale": {edit: func(r *Request) { r.OutputLocale = "fr" }, code: aicontext.InvalidLocale},
		"the scope":  {edit: func(r *Request) { r.Base.OrganizationID = "org_01M4YDQK00Y5H2929M9WWZWTVX" }, code: aicontext.CrossTenant},
	} {
		f := fake.New()
		h := &hooks{f: f}
		req := request(t, "edit", "Anything.")
		c.edit(&req)
		_, err := graphOf(t, client(t, f)).Run(ctx, req, h.Hooks())
		var ie *InputError
		var ce *ContextError
		switch {
		case c.want != "" && (!errors.As(err, &ie) || ie.Input != c.want):
			t.Errorf("%s: %v", name, err)
		case c.code != "" && (!errors.As(err, &ce) || ce.Code != c.code || ce.Repair):
			t.Errorf("%s: %v", name, err)
		}
		if len(f.Requests()) != 0 || h.preloaded != nil {
			t.Errorf("%s: %d requests, preloaded %v", name, len(f.Requests()), h.preloaded)
		}
	}
}

// H-057 in the graph: exactly one repair, by construction (a DAG has no
// cycle): an answer refused twice ends with the second answer's issues
// after two calls; a repair call that fails leaves the first outcome; a
// provider failure is never repaired; and the live eval's path (Repair
// false, no hooks) scores the first answer only.
func TestExactlyOneRepair(t *testing.T) {
	ctx := context.Background()
	intent := "Let more CI runs pass."
	f := fake.New()
	f.Script(provider.RoleModeler, intent, proposed(broken))
	h := &hooks{f: f}
	res, err := graphOf(t, client(t, f)).Run(ctx, request(t, "edit", intent), h.Hooks())
	if err != nil || res.Outcome.Status != review.Invalid || res.Repair == nil || res.Repair.Outcome != repair.Invalid || res.Repair.Call == nil ||
		len(f.Requests()) != 2 || f.Requests()[1].Prompt.Revision == nil || !slices.Equal(h.answered, []int{1, 2}) {
		t.Fatalf("refused twice: %+v %+v %v, %d requests", res.Outcome.Status, res.Repair, err, len(f.Requests()))
	}
	// The repair call fails: the first outcome, failed with its code.
	f = fake.New()
	f.Script(provider.RoleModeler, intent, proposed(broken))
	f.FailRevision(provider.RoleModeler, intent, provider.NewError(provider.CodeUnavailable, 503, "meta"))
	h = &hooks{f: f}
	res, err = graphOf(t, client(t, f)).Run(ctx, request(t, "edit", intent), h.Hooks())
	if err != nil || res.Outcome.Status != review.Invalid || res.Repair.Outcome != repair.Failed || res.Repair.Code != "AI_UNAVAILABLE" || len(f.Requests()) != 2 {
		t.Errorf("a failed repair: %+v %v", res.Repair, err)
	}
	// Without a repair: the first answer's outcome, one call.
	f = fake.New()
	f.Script(provider.RoleModeler, intent, proposed(broken))
	req := request(t, "edit", intent)
	req.Repair = false
	tr := &tracer{}
	res, err = graphOf(t, client(t, f)).run(ctx, req, Hooks{}, tr.option())
	if err != nil || res.Outcome.Status != review.Invalid || res.Repair != nil || len(f.Requests()) != 1 || res.Manifest != nil ||
		!reflect.DeepEqual(tr.nodes, path(NodeReview, NodeRepairContext)) {
		t.Errorf("no repair: %+v %+v %v, nodes %v", res.Outcome.Status, res.Repair, err, tr.nodes)
	}
	// No hooks: the review still runs, nothing is read or bound.
	f = fake.New()
	f.Script(provider.RoleModeler, intent, proposed(raise))
	res, err = graphOf(t, client(t, f)).Run(ctx, req, Hooks{})
	if err != nil || res.Outcome.Status != review.Ready || res.Manifest != nil || res.Outcome.PatchHash == "" {
		t.Errorf("no hooks: %+v %v", res.Outcome, err)
	}
}

// The request the graph sends is the one a direct call of the client with
// the same context sends: the same prompt, input hash and model (the
// metered adapter's tripwire passes, and nothing is added).
func TestTheRequestIsTheDirectCalls(t *testing.T) {
	ctx := context.Background()
	intent := "Six runners."
	f := fake.New()
	f.Script(provider.RoleModeler, intent, proposed(raise))
	c := client(t, f)
	req := request(t, "scenario", intent)
	req.Model = "muse-wonder-1.0"
	res, err := graphOf(t, c).Run(ctx, req, Hooks{})
	if err != nil {
		t.Fatal(err)
	}
	var ir model.ModelIR
	if err := model.Decode(req.Base.IR, &ir); err != nil {
		t.Fatal(err)
	}
	pack, _ := packs(t).Lookup(ir.Domain)
	var pool idpool.Pool
	_ = json.Unmarshal(req.Pool, &pool)
	pc, err := aicontext.Proposal(aicontext.ProposalInput{Scope: req.Scope, Mode: req.Mode,
		Base:   aicontext.Version{OrganizationID: org, ModelID: req.Base.ModelID, VersionID: mv, IR: &ir},
		Domain: pack.PromptContext(), Intent: intent, Pool: aicontext.Pool{OrganizationID: org, ProposalID: aip, IDs: pool},
		OutputLocale: "en", Budget: provider.DefaultBudget()})
	if err != nil {
		t.Fatal(err)
	}
	pc.Attribution, pc.Model = attribution, req.Model
	direct, err := c.GenerateModelProposal(ctx, pc)
	if err != nil {
		t.Fatal(err)
	}
	reqs := f.Requests()
	if len(reqs) != 2 || !reflect.DeepEqual(reqs[0], reqs[1]) || res.Call.InputHash != direct.Metadata.InputHash || res.Call.InputHash == "" {
		t.Errorf("the graph's request differs from the client's:\n%+v\n%+v", reqs[0], reqs[1])
	}
}

// Cancellation reaches every node: an attempt cancelled before the graph
// runs asks nothing; one cancelled during the call ends there, with the
// client's cancelled error, no review and no repair.
func TestCancellation(t *testing.T) {
	intent := "Let more CI runs pass."
	f := fake.New()
	f.Script(provider.RoleModeler, intent, proposed(broken))
	h := &hooks{f: f}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err := graphOf(t, client(t, f)).Run(ctx, request(t, "edit", intent), h.Hooks())
	if !errors.Is(err, context.Canceled) || provider.CodeOf(err) != provider.CodeCancelled || len(f.Requests()) != 0 || h.preloaded != nil {
		t.Errorf("cancelled first: %v, %d requests", err, len(f.Requests()))
	}
	for _, after := range []int{1, 2} {
		f := fake.New()
		f.Script(provider.RoleModeler, intent, proposed(broken))
		ctx, cancel := context.WithCancel(context.Background())
		b := &cancelling{Backend: f, after: after, cancel: cancel}
		h := &hooks{f: f}
		tr := &tracer{}
		_, err := graphOf(t, client(t, b)).run(ctx, request(t, "edit", intent), h.Hooks(), tr.option())
		cancel()
		if !errors.Is(err, context.Canceled) || len(f.Requests()) != after {
			t.Errorf("cancelled after call %d: %v, %d requests", after, err, len(f.Requests()))
		}
		if after == 1 && (slices.Contains(tr.nodes, NodeReview) || slices.Contains(tr.nodes, NodeRepairGenerate)) {
			t.Errorf("cancelled during the call: nodes %v", tr.nodes)
		}
		if after == 2 && slices.Contains(tr.nodes, NodeRepairReview) {
			t.Errorf("cancelled during the repair: nodes %v", tr.nodes)
		}
	}
}

// cancelling cancels the attempt once its n-th call has been answered.
type cancelling struct {
	*fake.Backend
	after, seen int
	cancel      func()
}

func (c *cancelling) Complete(ctx context.Context, req provider.Request) (provider.Response, error) {
	resp, err := c.Backend.Complete(ctx, req)
	if c.seen++; c.seen == c.after {
		c.cancel()
	}
	return resp, err
}

// A node that panics, a hook's included, panics again on the caller's
// goroutine with its own value, so the worker's reviewed panic handling
// applies as before; Eino's own panic error (whose text holds the value and
// a stack) never comes back as an error.
func TestAPanicIsRaisedAgain(t *testing.T) {
	for name, set := range map[string]func(*hooks){
		"before the call": func(h *hooks) { h.verifyPanic = true },
		"after the call":  func(h *hooks) { h.bindPanics = true },
	} {
		intent := "Six runners."
		f := fake.New()
		f.Script(provider.RoleModeler, intent, proposed(raise))
		h := &hooks{f: f}
		set(h)
		g := graphOf(t, client(t, f))
		got := func() (v any) {
			defer func() { v = recover() }()
			_, err := g.Run(context.Background(), request(t, "edit", intent), h.Hooks())
			t.Errorf("%s: no panic, %v", name, err)
			return nil
		}()
		switch v := got.(type) {
		case string:
			if v != "verify panicked" {
				t.Errorf("%s: %q", name, v)
			}
		case error:
			if v.Error() != "bind panicked" {
				t.Errorf("%s: %v", name, v)
			}
		default:
			t.Errorf("%s: %#v", name, got)
		}
	}
}

// Graph-level callbacks see the run's state, so none is attached by the
// graph itself: a run with no handler of its own has none, and the model's
// callbacks (its own, attached here) carry no message.
func TestTheModelsCallbacksCarryNoContent(t *testing.T) {
	intent := "Six runners."
	f := fake.New()
	f.Script(provider.RoleModeler, intent, proposed(raise))
	tr := &tracer{}
	if _, err := graphOf(t, client(t, f)).run(context.Background(), request(t, "edit", intent), Hooks{}, tr.option()); err != nil {
		t.Fatal(err)
	}
	if len(tr.inputs) != 1 {
		t.Fatalf("%d model starts", len(tr.inputs))
	}
	if in := ecmodel.ConvCallbackInput(tr.inputs[0]); in == nil || len(in.Messages) != 0 || in.Config != nil {
		t.Errorf("the model's start carries %+v", in)
	}
}

// One compiled graph serves every attempt of the worker: concurrent runs
// keep their own state (run under -race), each with its own outcome, call
// and repair. Each run is another organization's: the client lets one
// organization have one call in flight (its slot), which is the client's
// rule, not the graph's.
func TestConcurrentRuns(t *testing.T) {
	f := fake.New()
	g := graphOf(t, client(t, f))
	const n = 8
	for i := range n {
		k := "Case " + strconv.Itoa(i) + "."
		if i%2 == 0 {
			f.Script(provider.RoleModeler, k, proposed(raise))
		} else {
			f.Script(provider.RoleModeler, k, proposed(broken))
			f.ScriptRevision(provider.RoleModeler, k, proposed(fixed))
		}
	}
	var wg sync.WaitGroup
	results := make([]Result, n)
	errs := make([]error, n)
	for i := range n {
		wg.Go(func() {
			h := &hooks{f: f}
			req := request(t, "edit", "Case "+strconv.Itoa(i)+".")
			req.Scope.OrganizationID = org[:len(org)-1] + strconv.Itoa(i)
			req.Base.OrganizationID = req.Scope.OrganizationID
			results[i], errs[i] = g.Run(context.Background(), req, h.Hooks())
		})
	}
	wg.Wait()
	for i := range n {
		if errs[i] != nil || results[i].Outcome.Status != review.Ready || results[i].Call == nil || (results[i].Repair != nil) != (i%2 == 1) {
			t.Errorf("run %d: %+v %v %+v", i, results[i].Outcome.Status, errs[i], results[i].Repair)
		}
	}
	if got := len(f.Requests()); got != n+n/2 {
		t.Errorf("%d requests, want %d", got, n+n/2)
	}
}
