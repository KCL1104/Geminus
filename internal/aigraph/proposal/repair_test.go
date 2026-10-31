package proposal

// Slice E4 of docs/design/H-056_INVESTIGATIONS_ON_EINO.md: H-057's one
// repair as the proposal graph's unrolled branch (repair_context,
// generate_2, review_2), with the deterministic fake and a ledger, no
// database. The branch itself came with E3 (H-057 had merged into the old
// generator first, so E3 absorbed it); what this file adds is E4's own
// list: exactly one repair by the graph's shape, the second call reserved
// and settled like the first (two reservations, two settlements, the same
// as a direct repair's), no third call, no repair after a provider
// failure, and H-057's rule on research and a slow model (both repaired:
// research without a second search, a slow model under its own bounds).

import (
	"context"
	"encoding/json/v2"
	"errors"
	"reflect"
	"slices"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/cloudwego/eino/compose"

	"github.com/kcl1104/geminus/internal/ai/aicontext"
	"github.com/kcl1104/geminus/internal/ai/idpool"
	"github.com/kcl1104/geminus/internal/ai/provider"
	"github.com/kcl1104/geminus/internal/ai/provider/fake"
	"github.com/kcl1104/geminus/internal/ai/repair"
	"github.com/kcl1104/geminus/internal/ai/review"
	"github.com/kcl1104/geminus/internal/ai/spend"
	"github.com/kcl1104/geminus/internal/model"
)

const (
	spark  = "muse-spark-1.3"
	wonder = "muse-wonder-1.0"
	// unlimited is a ledger limit no test reaches.
	unlimited = int64(1) << 50
	// searches is how many server tool calls the billing fake quotes a
	// research attempt for.
	searches = 8
	// clientRefused is a needs_input answer the client's strict decoding
	// refuses for what it says (summary is not a string): invalid_output,
	// with the answer handed back for a repair.
	clientRefused = `{"status":"needs_input","summary":7,"assumptions":[],"questions":["q"],"requires_confirmation":true}`
)

// billing is the fake as a billable backend: each call is quoted by its
// prompt's size (a repair's two turns too, as the live adapters quote
// them), for the model the request names (Muse Spark 1.3 for the client's
// own), with searches server tool calls for a research call; it answers as
// that model (or as answerAs, when set) and is billed a fixed usage, so a
// metered client reserves and settles every call it sends.
type billing struct {
	*fake.Backend
	answerAs string
	calls    atomic.Int32
}

func modelOf(req provider.Request) string {
	if req.Model != "" {
		return req.Model
	}
	return spark
}

// quoted is what billing quotes req's input as.
func quoted(req provider.Request) int64 {
	n := int64(len(req.Prompt.System) + len(req.Prompt.User()))
	if r := req.Prompt.Revision; r != nil {
		n += int64(r.Prior.Len() + len(r.User()))
	}
	return n
}

func (b *billing) Quote(_ context.Context, req provider.Request) (provider.Quote, error) {
	q := provider.Quote{Model: modelOf(req), MaxInputTokens: quoted(req)}
	if req.Prompt.Research {
		q.MaxToolCalls = searches
	}
	return q, nil
}

func (b *billing) Complete(ctx context.Context, req provider.Request) (provider.Response, error) {
	b.calls.Add(1)
	resp, err := b.Backend.Complete(ctx, req)
	resp.Model, resp.Usage = modelOf(req), provider.Usage{InputTokens: 100, OutputTokens: 10}
	if b.answerAs != "" {
		resp.Model = b.answerAs
	}
	return resp, err
}

// ledger records every reservation asked for and every settlement of a
// memory meter; it refuses every reservation after the first refuseAfter
// (0: none) as a spend cap does.
type ledger struct {
	mu          sync.Mutex
	m           *provider.MemoryMeter
	refuseAfter int
	asked       int
	charges     []provider.Charge
	ids         []string
	settled     []provider.Settlement
}

func (l *ledger) Reserve(ctx context.Context, c provider.Charge) (provider.Reservation, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.asked++; l.refuseAfter > 0 && l.asked > l.refuseAfter {
		return provider.Reservation{}, &provider.Error{Code: provider.CodeQuotaExceeded, Limit: provider.LimitRun}
	}
	r, err := l.m.Reserve(ctx, c)
	if err == nil {
		l.charges, l.ids = append(l.charges, c), append(l.ids, r.ID)
	}
	return r, err
}

func (l *ledger) Settle(ctx context.Context, r provider.Reservation, s provider.Settlement) error {
	l.mu.Lock()
	l.settled = append(l.settled, s)
	l.mu.Unlock()
	return l.m.Settle(ctx, r, s)
}

// balanced: no reservation is left open, and what was spent is the sum of
// the settlements.
func (l *ledger) balanced() bool {
	var sum int64
	for _, s := range l.settled {
		sum += s.NanoUSD
	}
	return l.m.Committed() == l.m.Spent() && l.m.Spent() == sum
}

// metered is a metered client over a fresh billing fake and ledger.
func metered(t *testing.T, refuseAfter int) (*provider.Client, *billing, *ledger) {
	t.Helper()
	b := &billing{Backend: fake.New()}
	l := &ledger{m: provider.NewMemoryMeter(unlimited), refuseAfter: refuseAfter}
	c, err := provider.NewMetered(b, provider.DefaultBudget(), l)
	if err != nil {
		t.Fatal(err)
	}
	return c, b, l
}

// H-057's one repair is the graph's shape, not a counter: compiled in DAG
// mode, the graph has no cycle, and every way through it from START to END
// asks the model at most twice, generate once, then generate_2 at most
// once and only after repair_context, with nothing after the repair's
// review but the end. Some way reaches the repair, and some way ends
// without it.
func TestOneRepairByConstruction(t *testing.T) {
	var info *compose.GraphInfo
	m := graphOf(t, client(t, fake.New())).cfg.Model
	if _, err := compile(Config{Model: m, Packs: packs(t), Budget: provider.DefaultBudget()},
		compose.WithGraphCompileCallbacks(compiled(func(i *compose.GraphInfo) { info = i }))); err != nil {
		t.Fatal(err)
	}
	if info == nil {
		t.Fatal("no graph info")
	}
	next := map[string][]string{}
	add := func(from, to string) {
		if !slices.Contains(next[from], to) {
			next[from] = append(next[from], to)
		}
	}
	for from, tos := range info.Edges {
		for _, to := range tos {
			add(from, to)
		}
	}
	for from, tos := range info.DataEdges {
		for _, to := range tos {
			add(from, to)
		}
	}
	for from, bs := range info.Branches {
		for _, b := range bs {
			for to := range b.GetEndNode() {
				add(from, to)
			}
		}
	}
	for _, tos := range next {
		slices.Sort(tos)
	}
	var paths [][]string
	var walk func(at string, path []string)
	walk = func(at string, path []string) {
		if slices.Contains(path, at) {
			t.Fatalf("a cycle through %s: %v", at, path)
		}
		path = append(slices.Clone(path), at)
		if at == compose.END {
			paths = append(paths, path)
			return
		}
		if len(next[at]) == 0 {
			t.Fatalf("%s leads nowhere: %v", at, path)
		}
		for _, to := range next[at] {
			walk(to, path)
		}
	}
	walk(compose.START, nil)
	// The walk found no cycle, so no node is on a way twice; the model is
	// asked only in generate and generate_2 (the graph has these nodes and no
	// other), so at most twice on every way.
	var nodes []string
	for k := range info.Nodes {
		nodes = append(nodes, k)
	}
	slices.Sort(nodes)
	want := []string{NodeDecode, NodeContext, NodePreload, NodeVerify, NodeGenerate, NodeClassify, NodeReview, NodeBind,
		NodeRepairContext, NodeRepairGenerate, NodeRepairReview}
	slices.Sort(want)
	if !slices.Equal(nodes, want) {
		t.Errorf("nodes %v, want %v", nodes, want)
	}
	repaired, unrepaired := 0, 0
	for _, p := range paths {
		if i := slices.Index(p, NodeRepairGenerate); i >= 0 {
			repaired++
			if j := slices.Index(p, NodeGenerate); j < 0 || j > i || p[i-1] != NodeRepairContext || !slices.Equal(p[i+1:], []string{NodeRepairReview, compose.END}) {
				t.Errorf("the repair's call out of place: %v", p)
			}
		} else {
			unrepaired++
		}
	}
	if repaired == 0 || unrepaired == 0 {
		t.Errorf("%d ways with a repair, %d without (%d in all)", repaired, unrepaired, len(paths))
	}
	var into []string
	for from, tos := range next {
		if slices.Contains(tos, NodeRepairGenerate) {
			into = append(into, from)
		}
	}
	if !slices.Equal(into, []string{NodeRepairContext}) || !slices.Equal(next[NodeRepairReview], []string{compose.END}) {
		t.Errorf("into generate_2: %v; after review_2: %v", into, next[NodeRepairReview])
	}
}

// compiled is a compile callback.
type compiled func(*compose.GraphInfo)

func (c compiled) OnFinish(_ context.Context, info *compose.GraphInfo) { c(info) }

// direct is what a repair made without the graph sends and spends: the
// context the graph builds for req, the client's call, the server's review
// of the answer, then repair.Once around a direct second call (the photo
// pipeline's path), reviewed the same way.
func direct(t *testing.T, c *provider.Client, req Request) (review.Outcome, provider.Metadata, *repair.Record) {
	t.Helper()
	ctx := context.Background()
	var ir model.ModelIR
	if err := model.Decode(req.Base.IR, &ir); err != nil {
		t.Fatal(err)
	}
	pack, err := packs(t).Lookup(ir.Domain)
	if err != nil {
		t.Fatal(err)
	}
	var pool idpool.Pool
	if err := json.Unmarshal(req.Pool, &pool); err != nil {
		t.Fatal(err)
	}
	pc, err := aicontext.Proposal(aicontext.ProposalInput{Scope: req.Scope, Mode: req.Mode,
		Base:   aicontext.Version{OrganizationID: req.Base.OrganizationID, ModelID: req.Base.ModelID, VersionID: req.Base.VersionID, IR: &ir},
		Domain: pack.PromptContext(), Intent: req.Intent, Pool: aicontext.Pool{OrganizationID: req.Scope.OrganizationID, ProposalID: req.ProposalID, IDs: pool},
		OutputLocale: req.OutputLocale, Budget: provider.DefaultBudget(), Research: req.Research})
	if err != nil {
		t.Fatal(err)
	}
	pc.Attribution, pc.Model = req.Attribution, req.Model
	check := func(out provider.ProposalOutput) (review.Outcome, error) {
		return review.Review(review.Input{Mode: req.Mode, Base: req.Base.IR, Pool: pool, Pack: pack, Output: out})
	}
	first, err := c.GenerateModelProposal(ctx, pc)
	if err != nil {
		t.Fatal(err)
	}
	refused, err := check(first.Output)
	if err != nil {
		t.Fatal(err)
	}
	outcome, rec, err := repair.Once(ctx, c, pc, first.Prior, refused, check, nil)
	if err != nil || rec == nil {
		t.Fatalf("the direct repair: %+v %v", rec, err)
	}
	return outcome, first.Metadata, rec
}

// sameCall: two calls' metadata agree on everything but how long they took.
func sameCall(a, b *provider.Metadata) bool {
	if a == nil || b == nil {
		return false
	}
	x, y := *a, *b
	x.LatencyMillis, y.LatencyMillis = 0, 0
	return reflect.DeepEqual(x, y)
}

// The repair's call is reserved and settled like the first (H-057:
// "metered like any call"), through the graph as through a direct repair:
// an answer the review refuses, then repaired, makes exactly two
// reservations and two settlements, charged to the same organization,
// role, model and attribution, each reservation's ID in its call's
// metadata, nothing left open; and the requests, the reservations, the
// settlements, both calls' metadata and the repair's record are those of
// repair.Once around a direct call of the same client, for an edit, a
// scenario, research, and Muse Wonder (H-049, H-065), with and without
// research. The repair's request keeps its first call's output bound.
func TestTheRepairIsMeteredAsADirectRepair(t *testing.T) {
	ctx := context.Background()
	for name, c := range map[string]struct {
		mode     string
		research bool
		model    string
	}{
		"an edit": {mode: "edit"}, "a scenario": {mode: "scenario"}, "research": {mode: "edit", research: true},
		"Muse Wonder": {mode: "scenario", model: wonder}, "Muse Wonder with research": {mode: "edit", research: true, model: wonder},
	} {
		t.Run(name, func(t *testing.T) {
			intent := "Repair " + name + "."
			req := request(t, c.mode, intent)
			req.Research, req.Model = c.research, c.model
			role := req.Mode.Role()
			// The same bytes for both (proposed marshals a map, whose
			// members it orders anew each time).
			first, second := proposed(broken), proposed(fixed)
			script := func(b *billing) {
				b.Script(role, intent, first)
				b.ScriptRevision(role, intent, second)
			}
			gc, gb, gl := metered(t, 0)
			script(gb)
			res, err := graphOf(t, gc).Run(ctx, req, Hooks{})
			if err != nil || res.Outcome.Status != review.Ready || res.Repair == nil || res.Repair.Outcome != repair.Ready {
				t.Fatalf("the graph: %v %+v %+v", err, res.Outcome.Validation, res.Repair)
			}
			dc, db, dl := metered(t, 0)
			script(db)
			outcome, firstCall, rec := direct(t, dc, req)

			reqs := gb.Requests()
			if len(reqs) != 2 || gb.calls.Load() != 2 || reqs[0].Prompt.Revision != nil || reqs[1].Prompt.Revision == nil {
				t.Fatalf("%d requests, %d calls", len(reqs), gb.calls.Load())
			}
			if !reflect.DeepEqual(reqs, db.Requests()) {
				t.Errorf("the requests differ from a direct repair's:\n%+v\n%+v", reqs, db.Requests())
			}
			if reqs[1].MaxOutputTokens != reqs[0].MaxOutputTokens || reqs[1].AttemptTimeout != reqs[0].AttemptTimeout || reqs[1].Model != c.model {
				t.Errorf("the repair's bounds: %d %v %q, first %d %v", reqs[1].MaxOutputTokens, reqs[1].AttemptTimeout, reqs[1].Model, reqs[0].MaxOutputTokens, reqs[0].AttemptTimeout)
			}
			if len(gl.charges) != 2 || len(gl.settled) != 2 || !gl.balanced() {
				t.Fatalf("%d reservations, %d settlements, committed %d spent %d", len(gl.charges), len(gl.settled), gl.m.Committed(), gl.m.Spent())
			}
			if !reflect.DeepEqual(gl.charges, dl.charges) || !reflect.DeepEqual(gl.settled, dl.settled) || !slices.Equal(gl.ids, dl.ids) {
				t.Errorf("the ledger differs from a direct repair's:\n%+v %+v\n%+v %+v", gl.charges, gl.settled, dl.charges, dl.settled)
			}
			want := provider.Charge{OrganizationID: org, Role: role, Model: modelOf(reqs[0]), Attribution: attribution}
			for i, ch := range gl.charges {
				ch.NanoUSD = 0
				if ch != want || gl.charges[i].NanoUSD <= 0 || gl.settled[i].Outcome != provider.SpendUsage {
					t.Errorf("reservation %d: %+v, settled %+v", i, gl.charges[i], gl.settled[i])
				}
			}
			if res.Call.SpendReservation != gl.ids[0] || res.Repair.Call.SpendReservation != gl.ids[1] ||
				res.Call.CostNanoUSD != gl.settled[0].NanoUSD || res.Repair.Call.CostNanoUSD != gl.settled[1].NanoUSD {
				t.Errorf("the calls' spend: %+v / %+v", res.Call, res.Repair.Call)
			}
			if !sameCall(res.Call, &firstCall) || !sameCall(res.Repair.Call, rec.Call) {
				t.Errorf("the calls' metadata differ from a direct repair's:\n%+v\n%+v\n%+v\n%+v", res.Call, firstCall, res.Repair.Call, rec.Call)
			}
			gr, dr := *res.Repair, *rec
			gr.Call, dr.Call = nil, nil
			if gr != dr || res.Outcome.Status != outcome.Status || res.Outcome.PatchHash != outcome.PatchHash || res.Outcome.PatchHash == "" {
				t.Errorf("the repair: %+v %s %s, direct %+v %s %s", gr, res.Outcome.Status, res.Outcome.PatchHash, dr, outcome.Status, outcome.PatchHash)
			}
		})
	}
}

// No third call: whatever the repair's answer comes to, the attempt ends
// there, with two requests, two reservations and two settlements (the
// backend would answer a third time). The second answer refused by the
// review, by the client for what it says, or by the binding stage is the
// outcome; a spend cap that refuses the repair's reservation is a failed
// repair with no second request, never another try.
func TestNoThirdCall(t *testing.T) {
	ctx := context.Background()
	unbound := []model.ValidationIssue{model.NewIssue("DATASET_BINDING_INVALID", "/nodes/1", "x", model.SeverityError)}
	for name, c := range map[string]struct {
		first, second string // "" for the fake to repeat itself
		bindIssues    []model.ValidationIssue
		code          string // the outcome's issue code
		bound         []int
	}{
		"refused by the review twice":                   {first: broken, code: "ROUTING_INVALID"},
		"the repair refused by the client":              {first: broken, second: clientRefused, code: review.OutputInvalid},
		"refused by the client, then by the review":     {first: clientRefused, second: broken, code: "ROUTING_INVALID"},
		"refused by the client twice":                   {first: clientRefused, code: review.OutputInvalid},
		"refused by the binding stage twice":            {first: raise, code: "DATASET_BINDING_INVALID", bound: []int{1, 2}, bindIssues: unbound},
		"refused by the binding stage, then the review": {first: raise, second: broken, code: "ROUTING_INVALID", bound: []int{1}, bindIssues: unbound},
	} {
		t.Run(name, func(t *testing.T) {
			intent := "No third call: " + name + "."
			gc, gb, gl := metered(t, 0)
			answerOf := func(s string) []byte {
				if s == clientRefused {
					return []byte(s)
				}
				return proposed(s)
			}
			gb.Script(provider.RoleModeler, intent, answerOf(c.first))
			if c.second != "" {
				gb.ScriptRevision(provider.RoleModeler, intent, answerOf(c.second))
			}
			h := &hooks{f: gb.Backend, bindIssues: c.bindIssues}
			tr := &tracer{}
			res, err := graphOf(t, gc).run(ctx, request(t, "edit", intent), h.Hooks(), tr.option())
			if err != nil || res.Outcome.Status != review.Invalid || res.Repair == nil || res.Repair.Outcome != repair.Invalid || !res.Repair.Revised() {
				t.Fatalf("%v %+v %+v", err, res.Outcome.Status, res.Repair)
			}
			if !slices.ContainsFunc(res.Outcome.Validation.Issues, func(is model.ValidationIssue) bool { return is.Code == c.code }) {
				t.Errorf("the outcome is not the second answer's: %+v", res.Outcome.Validation.Issues)
			}
			reqs := gb.Requests()
			if len(reqs) != 2 || gb.calls.Load() != 2 || reqs[1].Prompt.Revision == nil || len(tr.models) != 2 || !slices.Equal(h.answered, []int{1, 2}) {
				t.Errorf("%d requests, %d calls, models %v, answered %v", len(reqs), gb.calls.Load(), tr.models, h.answered)
			}
			if len(gl.charges) != 2 || len(gl.settled) != 2 || gl.asked != 2 || !gl.balanced() {
				t.Errorf("%d reservations (%d asked), %d settlements", len(gl.charges), gl.asked, len(gl.settled))
			}
			if !slices.Equal(h.bound, c.bound) {
				t.Errorf("bound %v, want %v", h.bound, c.bound)
			}
			if n := slices.Index(tr.nodes, NodeRepairGenerate); n < 0 || slices.Contains(tr.nodes[n+1:], NodeRepairGenerate) || slices.Contains(tr.nodes[n+1:], NodeGenerate) {
				t.Errorf("nodes %v", tr.nodes)
			}
		})
	}

	// A spend cap refuses the repair's reservation: no second request, the
	// first outcome, the repair failed with the cap's code.
	intent := "No third call: capped."
	gc, gb, gl := metered(t, 1)
	gb.Script(provider.RoleModeler, intent, proposed(broken))
	gb.ScriptRevision(provider.RoleModeler, intent, proposed(fixed))
	h := &hooks{f: gb.Backend}
	res, err := graphOf(t, gc).Run(ctx, request(t, "edit", intent), h.Hooks())
	if err != nil || res.Outcome.Status != review.Invalid || res.Repair == nil || res.Repair.Outcome != repair.Failed || res.Repair.Code != "AI_QUOTA_EXCEEDED" ||
		res.Repair.Call == nil || res.Repair.Call.SpendReservation != "" {
		t.Fatalf("a capped repair: %v %+v %+v", err, res.Outcome.Status, res.Repair)
	}
	if len(gb.Requests()) != 1 || gl.asked != 2 || len(gl.charges) != 1 || len(gl.settled) != 1 || !gl.balanced() || !slices.Equal(h.answered, []int{1, 2}) {
		t.Errorf("%d requests, %d reservations asked, %d made, %d settled, answered %v", len(gb.Requests()), gl.asked, len(gl.charges), len(gl.settled), h.answered)
	}
}

// No repair after a first call that failed for anything but what its
// answer says (H-057: truncation, refusal, policy, spend caps, provider
// failure, cancellation): the attempt ends after the classification with
// the client's error, one request, one reservation and one settlement,
// though the backend would have answered the repair; nor after an
// invalid_output with no answer handed back (another model's answer, or a
// refusal with none), which is the first outcome as it is.
func TestNoRepairAfterAProviderFailure(t *testing.T) {
	ctx := context.Background()
	for _, code := range []provider.Code{provider.CodeTruncated, provider.CodeRefused, provider.CodeQuotaExceeded, provider.CodeSpendUnavailable,
		provider.CodePolicyNotApproved, provider.CodeTimeout, provider.CodeUnavailable, provider.CodeRateLimited, provider.CodeRejected,
		provider.CodeCredentialsExhausted, provider.CodeModelNotApproved, provider.CodeCancelled, provider.CodeConcurrencyLimited} {
		t.Run(string(code), func(t *testing.T) {
			intent := "No repair: " + string(code) + "."
			gc, gb, gl := metered(t, 0)
			gb.Fail(provider.RoleModeler, intent, provider.NewError(code, 0, "meta"))
			gb.ScriptRevision(provider.RoleModeler, intent, proposed(fixed))
			h := &hooks{f: gb.Backend}
			tr := &tracer{}
			res, err := graphOf(t, gc).run(ctx, request(t, "edit", intent), h.Hooks(), tr.option())
			var ce *CallError
			if !errors.As(err, &ce) || provider.CodeOf(err) != code || res.Repair != nil || res.Call == nil {
				t.Fatalf("%v %+v", err, res.Repair)
			}
			if !reflect.DeepEqual(tr.nodes, path()) || len(gb.Requests()) != 1 || !slices.Equal(h.answered, []int{1}) {
				t.Errorf("nodes %v, %d requests, answered %v", tr.nodes, len(gb.Requests()), h.answered)
			}
			if len(gl.charges) != 1 || len(gl.settled) != 1 || !gl.balanced() {
				t.Errorf("%d reservations, %d settlements", len(gl.charges), len(gl.settled))
			}
		})
	}
	for name, set := range map[string]func(*billing, string){
		"invalid_output with no answer": func(b *billing, intent string) {
			e := provider.NewError(provider.CodeInvalidOutput, 0, "meta")
			e.Issues = []model.ValidationIssue{model.NewIssue(review.OutputInvalid, "", "no answer", model.SeverityError)}
			b.Fail(provider.RoleModeler, intent, e)
		},
		"another model's answer": func(b *billing, intent string) {
			b.Script(provider.RoleModeler, intent, proposed(broken))
			b.answerAs = spark
		},
	} {
		t.Run(name, func(t *testing.T) {
			intent := "No repair: " + name + "."
			gc, gb, gl := metered(t, 0)
			set(gb, intent)
			gb.ScriptRevision(provider.RoleModeler, intent, proposed(fixed))
			h := &hooks{f: gb.Backend}
			tr := &tracer{}
			req := request(t, "edit", intent)
			req.Model = wonder
			res, err := graphOf(t, gc).run(ctx, req, h.Hooks(), tr.option())
			if err != nil || res.Outcome.Status != review.Invalid || res.Repair != nil || res.Outcome.Validation.Issues[0].Code != review.OutputInvalid {
				t.Fatalf("%v %+v %+v", err, res.Outcome, res.Repair)
			}
			if !reflect.DeepEqual(tr.nodes, path(NodeRepairContext)) || len(gb.Requests()) != 1 || len(gl.charges) != 1 || len(gl.settled) != 1 || !gl.balanced() {
				t.Errorf("nodes %v, %d requests, %d reservations, %d settlements", tr.nodes, len(gb.Requests()), len(gl.charges), len(gl.settled))
			}
		})
	}
}

// H-057's rule names no exclusion for research or a slow model (the slice
// list asked; the DECISIONS row answers): a research proposal is repaired
// without a second search (the repair's request has no web search, keeps
// the research call's output bound and the budget's retries, and is
// reserved for no search; the first call's sources stay), and Muse
// Wonder's under its own bounds (H-065: both calls of that model, at its
// output bound and timeout, reserved at its price). A repair of either
// that fails is the first outcome, not the job's failure, though their
// first call's failure is final.
func TestTheRepairOfResearchAndASlowModel(t *testing.T) {
	ctx := context.Background()
	budget := provider.DefaultBudget()
	reserved := func(model string, req provider.Request) int64 {
		price, ok := spend.PriceOf(model)
		if !ok {
			t.Fatalf("%s has no price", model)
		}
		per := price.AttemptBound(quoted(req), req.MaxOutputTokens)
		if req.Prompt.Research {
			search, _ := spend.SearchPriceOf(model)
			per += searches * search
		}
		return per * int64(1+req.MaxRetries)
	}
	for name, c := range map[string]struct {
		research bool
		model    string
	}{"research": {research: true}, "Muse Wonder": {model: wonder}, "Muse Wonder with research": {research: true, model: wonder}} {
		t.Run(name, func(t *testing.T) {
			intent := "Researched or slow: " + name + "."
			answer := proposed(broken)
			if c.research {
				// A page the research opened, which the proposal keeps.
				first := map[string]any{}
				_ = json.Unmarshal(answer, &first)
				first["sources"] = []map[string]string{{"url": fake.ResearchSources[0].URL, "title": "A page"}}
				answer, _ = json.Marshal(first)
			}
			gc, gb, gl := metered(t, 0)
			gb.Script(provider.RoleModeler, intent, answer)
			gb.ScriptRevision(provider.RoleModeler, intent, proposed(fixed))
			req := request(t, "edit", intent)
			req.Research, req.Model = c.research, c.model
			res, err := graphOf(t, gc).Run(ctx, req, Hooks{})
			if err != nil || res.Outcome.Status != review.Ready || res.Repair == nil || res.Repair.Outcome != repair.Ready {
				t.Fatalf("%v %+v %+v", err, res.Outcome.Validation, res.Repair)
			}
			reqs := gb.Requests()
			if len(reqs) != 2 || reqs[0].Prompt.Research != c.research || reqs[1].Prompt.Research || reqs[1].Prompt.Revision == nil ||
				reqs[1].Prompt.Revision.Researched != c.research {
				t.Fatalf("the requests: %d", len(reqs))
			}
			outBound, attempt := budget.MaxOutputTokens, budget.AttemptTimeout
			switch {
			case c.model != "":
				outBound, attempt = provider.SlowModelMaxOutputTokens, provider.SlowModelTimeout
			case c.research:
				outBound, attempt = provider.ResearchMaxOutputTokens, budget.ResearchTimeout(budget.AttemptTimeout)
			}
			for i, r := range reqs {
				if r.Model != c.model || r.MaxOutputTokens != outBound || r.AttemptTimeout != attempt {
					t.Errorf("request %d: model %q, max_tokens %d, attempt timeout %v", i, r.Model, r.MaxOutputTokens, r.AttemptTimeout)
				}
			}
			if c.research && (reqs[0].MaxRetries != provider.ResearchMaxRetries || reqs[1].MaxRetries != budget.MaxRetries) {
				t.Errorf("retries %d then %d", reqs[0].MaxRetries, reqs[1].MaxRetries)
			}
			m := modelOf(reqs[0])
			if len(gl.charges) != 2 || len(gl.settled) != 2 || !gl.balanced() {
				t.Fatalf("%d reservations, %d settlements", len(gl.charges), len(gl.settled))
			}
			for i, r := range reqs {
				if gl.charges[i].Model != m || gl.charges[i].NanoUSD != reserved(m, r) {
					t.Errorf("reservation %d: %+v, want %d at %s", i, gl.charges[i], reserved(m, r), m)
				}
			}
			if (res.Call.WebSearch != nil) != c.research || res.Repair.Call.WebSearch != nil {
				t.Errorf("web search: first %+v, repair %+v", res.Call.WebSearch, res.Repair.Call.WebSearch)
			}
			if c.research && (len(res.Sources) != 1 || res.Sources[0].URL != fake.ResearchSources[0].URL) {
				t.Errorf("sources %+v", res.Sources)
			}

			// The repair fails: the first outcome, no error (final or not).
			intent += " Failed."
			gc, gb, _ = metered(t, 0)
			gb.Script(provider.RoleModeler, intent, answer)
			gb.FailRevision(provider.RoleModeler, intent, provider.NewError(provider.CodeUnavailable, 503, "meta"))
			req = request(t, "edit", intent)
			req.Research, req.Model = c.research, c.model
			res, err = graphOf(t, gc).Run(ctx, req, Hooks{})
			if err != nil || res.Outcome.Status != review.Invalid || res.Repair == nil || res.Repair.Outcome != repair.Failed || res.Repair.Code != "AI_UNAVAILABLE" ||
				len(gb.Requests()) != 2 {
				t.Errorf("a failed repair: %v %+v %+v", err, res.Outcome.Status, res.Repair)
			}
		})
	}
}
