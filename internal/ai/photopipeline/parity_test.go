package photopipeline_test

import (
	"bytes"
	"context"
	"encoding/json/v2"
	"errors"
	"fmt"
	"reflect"
	"runtime/debug"
	"slices"
	"strings"
	"sync"
	"testing"

	"github.com/kcl1104/geminus/internal/ai/aicontext"
	"github.com/kcl1104/geminus/internal/ai/idpool"
	"github.com/kcl1104/geminus/internal/ai/locale"
	"github.com/kcl1104/geminus/internal/ai/photopipeline"
	"github.com/kcl1104/geminus/internal/ai/provider"
	"github.com/kcl1104/geminus/internal/ai/provider/fake"
	"github.com/kcl1104/geminus/internal/ai/repair"
	"github.com/kcl1104/geminus/internal/ai/review"
	"github.com/kcl1104/geminus/internal/contracttest"
	"github.com/kcl1104/geminus/internal/domainpack/electronicsline"
	"github.com/kcl1104/geminus/internal/model"
	"github.com/kcl1104/geminus/internal/model/hashing"
)

// H-059 parity (slice E5): Propose runs on the photo graph
// (internal/aigraph/photo) with its two vision calls through the metered
// Eino ChatModel, and does exactly what it did before. before is the
// pipeline as it was (photopipeline.go at 31ab222), kept here only as the
// test's reference: it is no production path. Each case runs both on fresh,
// identical fakes and compares everything a caller sees: the result (the
// outcome, the segmentation and its metadata, each call's metadata, the
// repair's record), the error (its type, text and code, a cancellation's
// cause), the observer's calls in order, every request the segmenter and the
// vision model got, the spend reserved and settled, and a panic's value.

// before is Propose before E5, verbatim but for the package's names and
// its repair, onceBefore (repair.Once before E5): so the repair branch is
// held to the code as it was too (E5 review round 1).
func before(ctx context.Context, c photopipeline.Clients, req photopipeline.Request, obs photopipeline.Observer) (photopipeline.Result, error) {
	var r photopipeline.Result
	if c.Segmenter == nil || c.Vision == nil {
		return r, provider.NewError(provider.CodeNotConfigured, 0, "")
	}
	if !photopipeline.TakesModel(c.Vision, req.Model) {
		return r, provider.NewError(provider.CodeModelNotApproved, 0, "")
	}
	seg, err := c.Segmenter.Segment(ctx, provider.SegmentRequest{OrganizationID: req.OrganizationID, Role: provider.ModeEdit.Role(),
		Attribution: req.Attribution, Image: req.Image, Concepts: req.Concepts})
	r.Segmenter, r.Segments = &seg.Metadata, len(seg.Segments)
	if obs.Segmented != nil {
		obs.Segmented(seg.Metadata, len(seg.Segments), err)
	}
	if err != nil {
		var pe *provider.Error
		if ctx.Err() == nil && errors.As(err, &pe) && pe.Code == provider.CodeInvalidOutput {
			r.Outcome = review.InvalidOutput(review.Segmentation(pe.Issues))
			return r, nil
		}
		return r, err
	}
	r.Segmentation = &seg
	switch {
	case len(seg.Segments) == 0:
		r.Outcome = beforeNoBoxes(req.OutputLocale)
		return r, nil
	case len(seg.Segments) > review.MaxPhotoSegments:
		r.Outcome = review.InvalidOutput(review.TooManySegments())
		return r, nil
	case seg.Width != req.Image.Width || seg.Height != req.Image.Height:
		r.Outcome = review.InvalidOutput(review.FrameMismatch())
		return r, nil
	}

	pctx, err := aicontext.ImageProposal(aicontext.ImageProposalInput{
		Scope:        aicontext.Scope{OrganizationID: req.OrganizationID, ModelID: req.Base.ModelID},
		Base:         req.Base,
		Domain:       req.Pack.PromptContext(),
		Pool:         req.Pool,
		Photo:        aicontext.Photo{OrganizationID: req.OrganizationID, Image: req.Image, Concepts: req.Concepts, Segments: seg.Segments},
		OutputLocale: req.OutputLocale,
		Budget:       req.Budget,
	})
	if err != nil {
		return r, &photopipeline.ContextError{Code: aicontext.CodeOf(err), Err: err}
	}
	pctx.Attribution, pctx.Model = req.Attribution, req.Model
	res, err := c.Vision.GenerateModelProposal(ctx, pctx)
	meta := res.Metadata
	r.Vision = &meta
	if obs.Answered != nil {
		obs.Answered(1, meta, err)
	}
	check := func(out provider.ProposalOutput) (review.Outcome, error) {
		outcome, err := review.Review(review.Input{Mode: provider.ModeEdit, Base: req.BaseIR, Pool: req.Pool.IDs, Pack: req.Pack, Output: out})
		if err != nil {
			return review.Outcome{}, &photopipeline.ReviewError{Err: err}
		}
		if outcome.Status == review.Ready {
			outcome = outcome.Refuse(review.Photo(req.Base.IR, outcome.Candidate, len(seg.Segments)))
		}
		if outcome.Status == review.Ready && req.Bind != nil {
			issues, err := req.Bind(outcome.Candidate)
			if err != nil {
				return review.Outcome{}, &photopipeline.BindError{Err: err}
			}
			outcome = outcome.Refuse(issues)
		}
		return outcome, nil
	}
	var outcome review.Outcome
	if err != nil {
		var pe *provider.Error
		if ctx.Err() != nil || !errors.As(err, &pe) || pe.Code != provider.CodeInvalidOutput {
			return r, err
		}
		outcome = review.InvalidOutput(pe.Issues)
	} else if outcome, err = check(res.Output); err != nil {
		return r, err
	}
	answered := func(m provider.Metadata, err error) {
		if obs.Answered != nil {
			obs.Answered(2, m, err)
		}
	}
	outcome, r.Repair, err = onceBefore(ctx, c.Vision, pctx, res.Prior, outcome, check, answered)
	if err != nil {
		var ae *aicontext.Error
		if errors.As(err, &ae) {
			return r, &photopipeline.ContextError{Code: ae.Code, Err: err}
		}
		return r, err
	}
	r.Outcome = outcome
	return r, nil
}

// onceBefore is repair.Once before E5 (repair.go at 31ab222), verbatim but
// for the package's names.
func onceBefore(ctx context.Context, client provider.LLMProvider, first provider.ProposalContext, prior provider.PriorAnswer, refused review.Outcome,
	check repair.Check, answered func(provider.Metadata, error)) (review.Outcome, *repair.Record, error) {
	issues, notListed := repair.Issues(refused)
	if len(issues) == 0 || !prior.Present() {
		return refused, nil, nil
	}
	rc, err := aicontext.Revise(first, prior, issues, notListed)
	if err != nil {
		return refused, nil, err
	}
	if err := ctx.Err(); err != nil {
		// Stopping between the two calls: no second call.
		return refused, nil, provider.NewError(provider.CodeCancelled, 0, "").WithCause(err)
	}
	rec := &repair.Record{Issues: len(issues), NotListed: notListed}
	res, err := client.GenerateModelProposal(ctx, rc)
	meta := res.Metadata
	rec.Call = &meta
	if answered != nil {
		answered(meta, err)
	}
	if err != nil {
		if ctx.Err() != nil {
			return refused, rec, err
		}
		var pe *provider.Error
		if errors.As(err, &pe) && pe.Code == provider.CodeInvalidOutput && res.Prior.Present() {
			// The second answer refused for what it says (the client handed
			// it back, as it does only then): its own issues, cleaned like
			// every other, and nothing of it.
			rec.Outcome = repair.Invalid
			return review.InvalidOutput(pe.Issues), rec, nil
		}
		// Every other failure, and invalid_output with no answer handed back
		// (no text block, a stop reason this build does not know, another
		// model's answer, an empty or oversized one, a web search it was not
		// offered: H-057 review round 1): no second answer the server
		// reviewed, as the same refusal of a first call is not repaired. The
		// first outcome stays; AI_INVALID_OUTPUT here is what shows an
		// endpoint that does not take the repair's turns.
		rec.Outcome, rec.Code = repair.Failed, codeOfBefore(err)
		return refused, rec, nil
	}
	outcome, err := check(res.Output)
	if err != nil {
		return refused, rec, err
	}
	rec.Outcome = string(outcome.Status)
	return outcome, rec, nil
}

// codeOfBefore is repair's codeOf at 31ab222.
func codeOfBefore(err error) string {
	if c := provider.CodeOf(err); c != "" {
		return "AI_" + strings.ToUpper(string(c))
	}
	return "INTERNAL"
}

func beforeNoBoxes(outputLocale string) review.Outcome {
	summary := "The segmenter found no machine or conveyor, and no drawn station or arrow, in the image, so no station can be proposed from it."
	question := "Which stations does the line have, in which order? Upload a clearer photo or floor plan of the line, or describe it in words."
	if outputLocale == string(locale.TraditionalChinese) {
		summary = "影像辨識在這張影像中找不到機台、輸送帶，也找不到畫出的工作站或箭頭，無法從影像提出工作站。"
		question = "這條產線有哪些工作站、順序為何？請上傳更清楚的產線照片或平面圖，或以文字描述產線。"
	}
	return review.Outcome{Status: review.NeedsInput, Summary: summary, Assumptions: []string{}, Questions: []string{question}}
}

const (
	parityOrg = "org_01M4YDQK00Y5H2929M9WWZWTVH"
	parityMdl = "mdl_01M4YDQK00ADN261CME3WV5HZ6"
	parityMv  = "mv_01M4YDQK00ADN261CME3WV5HZ6"
	parityAip = "aip_01M4YDQK00ADN261CME3WV5HZ6"
)

// billedVision is the fake as a billable vision backend, so a metered
// client reserves and settles each call; the repair's answer names
// hooks.repairModel when a case sets it.
type billedVision struct {
	*fake.Backend
	h *hooks
}

func (b billedVision) Quote(context.Context, provider.Request) (provider.Quote, error) {
	return provider.Quote{Model: "muse-spark-1.3", MaxInputTokens: 30_000}, nil
}

func (b billedVision) Complete(ctx context.Context, req provider.Request) (provider.Response, error) {
	r, err := b.Backend.Complete(ctx, req)
	r.Model, r.Usage = "muse-spark-1.3", provider.Usage{InputTokens: 7000, OutputTokens: 900}
	if b.h != nil && b.h.repairModel != "" && req.Prompt.Revision != nil {
		r.Model = b.h.repairModel
	}
	return r, err
}

// ledger records each charge and settlement, without the reservation's ID.
type ledger struct {
	mu      sync.Mutex
	m       *provider.MemoryMeter
	charges []provider.Charge
	settled []provider.Settlement
}

func (l *ledger) Reserve(ctx context.Context, c provider.Charge) (provider.Reservation, error) {
	l.mu.Lock()
	l.charges = append(l.charges, c)
	l.mu.Unlock()
	return l.m.Reserve(ctx, c)
}

func (l *ledger) Settle(ctx context.Context, r provider.Reservation, s provider.Settlement) error {
	l.mu.Lock()
	l.settled = append(l.settled, s)
	l.mu.Unlock()
	return l.m.Settle(ctx, r, s)
}

// hooks are a case's ways into a run: the vision backend's calls and the
// segmenter's, by their count from 1.
type hooks struct {
	// onVision runs before the n-th vision call reaches the backend, and
	// after it answered (after true).
	onVision func(n int, after bool)
	// onSegment runs before the n-th concept's segmentation call.
	onSegment func(n int)
	// repairModel, when set, is the model a metered vision backend's
	// repair answer names (another model's answer: the client refuses it
	// and hands nothing back).
	repairModel string
}

type visionHook struct {
	provider.Backend
	h *hooks
	n int
}

func (v *visionHook) AcceptsImages() bool { return true }
func (v *visionHook) AcceptsModel(role provider.Role, model string) bool {
	return v.Backend.(interface {
		AcceptsModel(provider.Role, string) bool
	}).AcceptsModel(role, model)
}
func (v *visionHook) Complete(ctx context.Context, req provider.Request) (provider.Response, error) {
	v.n++
	if v.h.onVision != nil {
		v.h.onVision(v.n, false)
	}
	resp, err := v.Backend.Complete(ctx, req)
	if v.h.onVision != nil {
		v.h.onVision(v.n, true)
	}
	return resp, err
}

type segmentHook struct {
	*fake.Segmenter
	h *hooks
	n int
}

func (s *segmentHook) SegmentOne(ctx context.Context, call provider.SegmentCall) (provider.SegmentAnswer, error) {
	s.n++
	if s.h.onSegment != nil {
		s.h.onSegment(s.n)
	}
	return s.Segmenter.SegmentOne(ctx, call)
}

// world is one run's fakes, clients and request.
type world struct {
	t      *testing.T
	f      *fake.Backend
	seg    *fake.Segmenter
	demo   contracttest.PhotoDemo
	pool   map[string][]string
	req    photopipeline.Request
	c      photopipeline.Clients
	ledger *ledger
	ctx    context.Context
	cancel context.CancelFunc
	hooks  hooks
	events []string
}

// shared is what both runs of a case share: the base and its ID pool
// (idpool.New draws new IDs each time), as the worker reads them stored.
type shared struct {
	baseIR []byte
	pool   []byte
}

func newShared(t *testing.T) shared {
	t.Helper()
	var doc map[string]any
	if err := json.Unmarshal(contracttest.BareLine(t), &doc); err != nil {
		t.Fatal(err)
	}
	doc["model_id"] = parityMdl
	raw, _ := json.Marshal(doc)
	baseIR, err := hashing.Canonical(raw)
	if err != nil {
		t.Fatal(err)
	}
	var ir model.ModelIR
	if err := model.Decode(baseIR, &ir); err != nil {
		t.Fatal(err)
	}
	pool, err := idpool.New(&ir)
	if err != nil {
		t.Fatal(err)
	}
	poolJSON, err := json.Marshal(pool)
	if err != nil {
		t.Fatal(err)
	}
	return shared{baseIR: baseIR, pool: poolJSON}
}

func newWorld(t *testing.T, metered bool, sh shared) *world {
	t.Helper()
	w := &world{t: t, f: fake.New(), seg: fake.NewSegmenter(), demo: contracttest.LoadPhotoDemo(t, "electronics-line-demo")}
	w.ctx, w.cancel = context.WithCancel(context.Background())
	t.Cleanup(w.cancel)
	baseIR := slices.Clone(sh.baseIR)
	var ir model.ModelIR
	if err := model.Decode(baseIR, &ir); err != nil {
		t.Fatal(err)
	}
	var pool idpool.Pool
	if err := json.Unmarshal(sh.pool, &pool); err != nil {
		t.Fatal(err)
	}
	w.pool = map[string][]string{}
	for _, k := range idpool.Kinds {
		w.pool[string(k)] = pool.IDs(k)
	}
	img, err := provider.NewImage(w.demo.Segments.MediaType, w.demo.Image)
	if err != nil {
		t.Fatal(err)
	}
	var vision *provider.Client
	vb := &visionHook{Backend: w.f, h: &w.hooks}
	if metered {
		w.ledger = &ledger{m: provider.NewMemoryMeter(1 << 50)}
		vision, err = provider.NewMetered(billedVision{Backend: w.f, h: &w.hooks}, provider.DefaultBudget(), w.ledger)
	} else {
		vision, err = provider.New(vb, provider.DefaultBudget())
	}
	if err != nil {
		t.Fatal(err)
	}
	segClient, err := provider.NewSegmentClient(&segmentHook{Segmenter: w.seg, h: &w.hooks}, provider.DefaultBudget())
	if err != nil {
		t.Fatal(err)
	}
	pack := electronicsline.Pack{}
	w.c = photopipeline.Clients{Segmenter: segClient, Vision: vision}
	w.req = photopipeline.Request{
		OrganizationID: parityOrg,
		Base:           aicontext.Version{OrganizationID: parityOrg, ModelID: parityMdl, VersionID: parityMv, IR: &ir},
		BaseIR:         baseIR,
		Pack:           pack,
		Concepts:       photopipeline.Concepts(pack),
		Pool:           aicontext.Pool{OrganizationID: parityOrg, ProposalID: parityAip, IDs: pool},
		Image:          img,
		OutputLocale:   "en",
		Budget:         provider.DefaultBudget(),
		Attribution:    provider.Attribution{TargetType: "ai_proposal", TargetID: parityAip, RequestID: "req_01M4YDQK00ADN261CME3WV5HZ6"},
	}
	return w
}

// script makes the segmenter answer the demo's segments, each concept in
// the frame width x height (the image's when 0).
func (w *world) script(width, height int, extra int) {
	s := w.demo.Segments
	if width == 0 {
		width, height = s.Width, s.Height
	}
	by := map[string][]provider.Segment{}
	for _, x := range s.Segments {
		by[x.Concept] = append(by[x.Concept], provider.Segment{Concept: x.Concept, ObjectID: x.ObjectID, Confidence: x.Confidence,
			Box: provider.Box{X1: x.Box.X1, Y1: x.Box.Y1, X2: x.Box.X2, Y2: x.Box.Y2}})
	}
	for i := range extra {
		first := s.Segments[0]
		by[first.Concept] = append(by[first.Concept], provider.Segment{Concept: first.Concept, ObjectID: 1000 + i, Confidence: 0.5,
			Box: provider.Box{X1: 1, Y1: 1, X2: 2, Y2: 2}})
	}
	for c, segs := range by {
		w.seg.Script(c, width, height, segs...)
	}
}

func (w *world) answer() []byte {
	return contracttest.PhotoAnswer(w.t, "electronics-line-demo", w.pool)
}

// edited is the demo's answer with edit applied to the change adding the
// n-th pool ID of kind.
func (w *world) edited(kind string, n int, edit func(v map[string]any)) []byte {
	var a map[string]any
	if err := json.Unmarshal(w.answer(), &a); err != nil {
		w.t.Fatal(err)
	}
	for _, c := range a["changes"].([]any) {
		if v, ok := c.(map[string]any)["value"].(map[string]any); ok && v["id"] == w.pool[kind][n] {
			edit(v)
		}
	}
	// Deterministic: both runs of a case must send the same bytes.
	out, _ := json.Marshal(a, json.Deterministic(true))
	return out
}

func (w *world) digest() string { return w.demo.Segments.StoredDigest }

func (w *world) observer() photopipeline.Observer {
	return photopipeline.Observer{
		Segmented: func(m provider.SegmentMetadata, n int, err error) {
			w.events = append(w.events, fmt.Sprintf("segmented %d %s %d %v", n, provider.CodeOf(err), m.Concepts, w.ctx.Err()))
		},
		Answered: func(call int, m provider.Metadata, err error) {
			w.events = append(w.events, fmt.Sprintf("answered %d %s %s %s %v", call, m.InputHash, m.TemplateVersion, provider.CodeOf(err), w.ctx.Err()))
		},
	}
}

// seen is everything a caller sees of one run.
type seen struct {
	result   photopipeline.Result
	err      string
	errType  string
	code     provider.Code
	canceled bool
	detail   string
	panicked any
	// site is the function the panic was raised in, from the stack taken
	// where the caller recovered it (panicSite).
	site     string
	events   []string
	visions  []provider.Request
	segments []provider.SegmentCall
	charges  []provider.Charge
	settled  []provider.Settlement
}

func run(w *world, propose func(context.Context, photopipeline.Clients, photopipeline.Request, photopipeline.Observer) (photopipeline.Result, error)) (s seen) {
	defer func() {
		if v := recover(); v != nil {
			// As the worker's handler does (internal/worker, handle): the
			// stack where the panic is recovered, which its log line holds.
			s.panicked, s.site = v, panicSite(debug.Stack())
		}
		s.events, s.visions, s.segments = w.events, w.f.Requests(), w.seg.Calls()
		if w.ledger != nil {
			s.charges, s.settled = w.ledger.charges, w.ledger.settled
		}
	}()
	res, err := propose(w.ctx, w.c, w.req, w.observer())
	s.result = normalized(res)
	if err != nil {
		s.err, s.errType, s.code, s.canceled = err.Error(), fmt.Sprintf("%T", err), provider.CodeOf(err), errors.Is(err, context.Canceled)
		var ce *photopipeline.ContextError
		var re *photopipeline.ReviewError
		var be *photopipeline.BindError
		switch {
		case errors.As(err, &ce):
			s.detail = "context " + string(ce.Code) + " " + ce.Err.Error()
		case errors.As(err, &re):
			s.detail = "review " + re.Err.Error()
		case errors.As(err, &be):
			s.detail = "bind " + be.Err.Error()
		}
	}
	return s
}

// panicSite is the function a panic was raised in, read from a stack taken
// in the deferred function that recovered it: the first frame below the
// panic's own that is not the runtime's. "" when the stack shows none.
func panicSite(stack []byte) string {
	lines := strings.Split(string(stack), "\n")
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

// normalized is res without its calls' latencies (a clock's) and their
// reservations' IDs (random): the rest is compared as it is.
func normalized(res photopipeline.Result) photopipeline.Result {
	if res.Segmenter != nil {
		m := *res.Segmenter
		m.LatencyMillis, m.SpendReservations = 0, slices.Repeat([]string{"r"}, len(m.SpendReservations))
		res.Segmenter = &m
	}
	if res.Segmentation != nil {
		s := *res.Segmentation
		s.Metadata.LatencyMillis, s.Metadata.SpendReservations = 0, slices.Repeat([]string{"r"}, len(s.Metadata.SpendReservations))
		res.Segmentation = &s
	}
	call := func(m *provider.Metadata) *provider.Metadata {
		if m == nil {
			return nil
		}
		c := *m
		c.LatencyMillis = 0
		if c.SpendReservation != "" {
			c.SpendReservation = "r"
		}
		return &c
	}
	res.Vision = call(res.Vision)
	if res.Repair != nil {
		r := *res.Repair
		r.Call = call(r.Call)
		res.Repair = &r
	}
	return res
}

func TestProposeIsThePipelineAsItWas(t *testing.T) {
	routingHalf := func(w *world) []byte {
		return w.edited("flow", 2, func(v map[string]any) { v["routing"].(map[string]any)["probability"] = 0.5 })
	}
	unbound := func(w *world) []byte {
		return w.edited("node", 1, func(v map[string]any) {
			v["service_time"] = map[string]any{"distribution": map[string]any{"type": "empirical", "samples_ref": "dataset:aoi_times"}}
		})
	}
	schemaInvalid := []byte(`{"status":"proposed","summary":"ANSWER-SENTINEL"}`)
	needsInput := []byte(`{"status":"needs_input","summary":"Which line?","assumptions":[],"questions":["Which stations?"],"requires_confirmation":true}`)
	// The client's refusal of the repair's answer with nothing handed back
	// (H-057 review round 1), with an issue of its own that must never be
	// the outcome.
	noAnswer := func() error {
		e := provider.NewError(provider.CodeInvalidOutput, 0, "")
		e.Issues = []model.ValidationIssue{model.NewIssue(provider.OutputInvalid, "", "the answer is not from the Standard-mode model the request named", model.SeverityError)}
		return e
	}
	cases := []struct {
		name    string
		metered bool
		setup   func(w *world)
	}{
		{name: "the demo, ready", setup: func(w *world) { w.script(0, 0, 0); w.f.Script(provider.RoleModeler, w.digest(), w.answer()) }},
		{name: "the demo, ready, metered", metered: true, setup: func(w *world) { w.script(0, 0, 0); w.f.Script(provider.RoleModeler, w.digest(), w.answer()) }},
		{name: "the demo in zh-Hant, ready", setup: func(w *world) {
			w.req.OutputLocale = string(locale.TraditionalChinese)
			w.script(0, 0, 0)
			w.f.Script(provider.RoleModeler, w.digest(), w.answer())
		}},
		{name: "no boxes", setup: func(w *world) {}},
		{name: "no boxes, zh-Hant", setup: func(w *world) { w.req.OutputLocale = string(locale.TraditionalChinese) }},
		{name: "too many boxes", setup: func(w *world) { w.script(0, 0, review.MaxPhotoSegments) }},
		{name: "another frame", setup: func(w *world) { w.script(w.demo.Segments.Width+10, w.demo.Segments.Height+10, 0) }},
		{name: "a box outside the image: the segmentation refused", setup: func(w *world) {
			s := w.demo.Segments
			w.seg.Script(s.Concepts[0], s.Width, s.Height, provider.Segment{ObjectID: 0, Confidence: 0.9, Box: provider.Box{X1: 0, Y1: 0, X2: s.Width + 5, Y2: 3}})
		}},
		{name: "the segmenter fails", setup: func(w *world) {
			for _, c := range w.req.Concepts {
				w.seg.Fail(c, provider.NewError(provider.CodeRateLimited, 429, "meta"))
			}
		}},
		{name: "no segmenter", setup: func(w *world) { w.c.Segmenter = nil }},
		{name: "no vision model", setup: func(w *world) { w.c.Vision = nil }},
		{name: "a model the vision client may not ask", setup: func(w *world) {
			w.req.Model = "muse-wonder-1.0"
			c, err := provider.New(onlyImages{w.f}, provider.DefaultBudget())
			if err != nil {
				w.t.Fatal(err)
			}
			w.c.Vision = c
		}},
		{name: "the chosen model", setup: func(w *world) {
			w.req.Model = "muse-wonder-1.0"
			w.script(0, 0, 0)
			w.f.Script(provider.RoleModeler, w.digest(), w.answer())
		}},
		{name: "the context too large", setup: func(w *world) { w.script(0, 0, 0); w.req.Budget.MaxPromptBytes = 64 }},
		{name: "the vision call fails", setup: func(w *world) {
			w.script(0, 0, 0)
			w.f.Fail(provider.RoleModeler, w.digest(), provider.NewError(provider.CodeRateLimited, 429, "meta"))
		}},
		{name: "the vision model refuses", setup: func(w *world) {
			w.script(0, 0, 0)
			w.f.Fail(provider.RoleModeler, w.digest(), provider.NewError(provider.CodeRefused, 0, "meta"))
		}},
		{name: "needs input", setup: func(w *world) { w.script(0, 0, 0); w.f.Script(provider.RoleModeler, w.digest(), needsInput) }},
		{name: "schema-invalid, repaired", setup: func(w *world) {
			w.script(0, 0, 0)
			w.f.Script(provider.RoleModeler, w.digest(), schemaInvalid)
			w.f.ScriptRevision(provider.RoleModeler, w.digest(), w.answer())
		}},
		{name: "schema-invalid, repaired, metered", metered: true, setup: func(w *world) {
			w.script(0, 0, 0)
			w.f.Script(provider.RoleModeler, w.digest(), schemaInvalid)
			w.f.ScriptRevision(provider.RoleModeler, w.digest(), w.answer())
		}},
		{name: "schema-invalid twice", setup: func(w *world) { w.script(0, 0, 0); w.f.Script(provider.RoleModeler, w.digest(), schemaInvalid) }},
		{name: "refused by the review, repaired", setup: func(w *world) {
			w.script(0, 0, 0)
			w.f.Script(provider.RoleModeler, w.digest(), routingHalf(w))
			w.f.ScriptRevision(provider.RoleModeler, w.digest(), w.answer())
		}},
		{name: "refused by the review twice", setup: func(w *world) { w.script(0, 0, 0); w.f.Script(provider.RoleModeler, w.digest(), routingHalf(w)) }},
		{name: "refused by the review, the repair fails", setup: func(w *world) {
			w.script(0, 0, 0)
			w.f.Script(provider.RoleModeler, w.digest(), routingHalf(w))
			w.f.FailRevision(provider.RoleModeler, w.digest(), provider.NewError(provider.CodeRateLimited, 429, "meta"))
		}},
		{name: "refused by the review, the repair refused with no answer handed back", setup: func(w *world) {
			w.script(0, 0, 0)
			w.f.Script(provider.RoleModeler, w.digest(), routingHalf(w))
			w.f.FailRevision(provider.RoleModeler, w.digest(), noAnswer())
		}},
		{name: "schema-invalid, the repair refused with no answer handed back", setup: func(w *world) {
			w.script(0, 0, 0)
			w.f.Script(provider.RoleModeler, w.digest(), schemaInvalid)
			w.f.FailRevision(provider.RoleModeler, w.digest(), noAnswer())
		}},
		{name: "refused by the review, the repair answered by another model, metered", metered: true, setup: func(w *world) {
			w.script(0, 0, 0)
			w.f.Script(provider.RoleModeler, w.digest(), routingHalf(w))
			w.f.ScriptRevision(provider.RoleModeler, w.digest(), w.answer())
			// The person chose a model (H-049): the client takes no other
			// model's answer in its place.
			w.req.Model = "muse-spark-1.3"
			w.hooks.repairModel = "claude-opus-5-5"
		}},
		{name: "refused by the review, the repair needs input", setup: func(w *world) {
			w.script(0, 0, 0)
			w.f.Script(provider.RoleModeler, w.digest(), routingHalf(w))
			w.f.ScriptRevision(provider.RoleModeler, w.digest(), needsInput)
		}},
		{name: "refused by the binding stage, repaired", setup: func(w *world) {
			w.script(0, 0, 0)
			w.f.Script(provider.RoleModeler, w.digest(), unbound(w))
			w.f.ScriptRevision(provider.RoleModeler, w.digest(), w.answer())
			w.req.Bind = bindRefusing
		}},
		{name: "the binding stage fails", setup: func(w *world) {
			w.script(0, 0, 0)
			w.f.Script(provider.RoleModeler, w.digest(), w.answer())
			w.req.Bind = func(*model.ModelIR) ([]model.ValidationIssue, error) {
				return nil, errors.New("the snapshots are gone")
			}
		}},
		{name: "the binding stage fails on the repair", setup: func(w *world) {
			w.script(0, 0, 0)
			w.f.Script(provider.RoleModeler, w.digest(), routingHalf(w))
			w.f.ScriptRevision(provider.RoleModeler, w.digest(), w.answer())
			w.req.Bind = func(*model.ModelIR) ([]model.ValidationIssue, error) {
				return nil, errors.New("the snapshots are gone")
			}
		}},
		{name: "the review cannot run", setup: func(w *world) {
			w.script(0, 0, 0)
			w.f.Script(provider.RoleModeler, w.digest(), w.answer())
			w.req.BaseIR = []byte(`{"not":"a model"`)
		}},
		{name: "cancelled before", setup: func(w *world) {
			w.script(0, 0, 0)
			w.f.Script(provider.RoleModeler, w.digest(), w.answer())
			w.cancel()
		}},
		{name: "cancelled while segmenting", setup: func(w *world) {
			w.script(0, 0, 0)
			w.hooks.onSegment = func(n int) {
				if n == 3 {
					w.cancel()
				}
			}
		}},
		{name: "cancelled after segmenting", setup: func(w *world) {
			w.script(0, 0, 0)
			w.f.Script(provider.RoleModeler, w.digest(), w.answer())
			w.c.Segmenter = cancelAfter{Segmenter: w.c.Segmenter, cancel: w.cancel}
		}},
		{name: "cancelled during the vision call", setup: func(w *world) {
			w.script(0, 0, 0)
			w.f.Script(provider.RoleModeler, w.digest(), w.answer())
			w.hooks.onVision = func(n int, after bool) {
				if !after {
					w.cancel()
				}
			}
		}},
		{name: "cancelled after a refused first answer, before the repair", setup: func(w *world) {
			w.script(0, 0, 0)
			w.f.Script(provider.RoleModeler, w.digest(), routingHalf(w))
			w.f.ScriptRevision(provider.RoleModeler, w.digest(), w.answer())
			w.hooks.onVision = func(n int, after bool) {
				if n == 1 && after {
					w.cancel()
				}
			}
		}},
		{name: "cancelled after a schema-invalid first answer, before the repair", setup: func(w *world) {
			w.script(0, 0, 0)
			w.f.Script(provider.RoleModeler, w.digest(), schemaInvalid)
			w.f.ScriptRevision(provider.RoleModeler, w.digest(), w.answer())
			w.hooks.onVision = func(n int, after bool) {
				if n == 1 && after {
					w.cancel()
				}
			}
		}},
		{name: "cancelled during the repair", setup: func(w *world) {
			w.script(0, 0, 0)
			w.f.Script(provider.RoleModeler, w.digest(), routingHalf(w))
			w.f.ScriptRevision(provider.RoleModeler, w.digest(), w.answer())
			w.hooks.onVision = func(n int, after bool) {
				if n == 2 && !after {
					w.cancel()
				}
			}
		}},
		{name: "cancelled after the repair answered", setup: func(w *world) {
			w.script(0, 0, 0)
			w.f.Script(provider.RoleModeler, w.digest(), routingHalf(w))
			w.f.ScriptRevision(provider.RoleModeler, w.digest(), w.answer())
			w.hooks.onVision = func(n int, after bool) {
				if n == 2 && after {
					w.cancel()
				}
			}
		}},
		{name: "the segmenter panics", setup: func(w *world) {
			w.script(0, 0, 0)
			w.hooks.onSegment = func(n int) {
				if n == 2 {
					panic(fmt.Errorf("SEGMENTER-PANIC"))
				}
			}
		}},
		{name: "the vision backend panics", setup: func(w *world) {
			w.script(0, 0, 0)
			w.hooks.onVision = func(int, bool) { panic("VISION-PANIC") }
		}},
		{name: "the binding stage panics on the repair", setup: func(w *world) {
			w.script(0, 0, 0)
			w.f.Script(provider.RoleModeler, w.digest(), routingHalf(w))
			w.f.ScriptRevision(provider.RoleModeler, w.digest(), w.answer())
			w.req.Bind = func(*model.ModelIR) ([]model.ValidationIssue, error) {
				var m map[string]int
				m["nil map"]++ // a runtime error, as a bug would be
				return nil, nil
			}
		}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			sh := newShared(t)
			old, now := newWorld(t, c.metered, sh), newWorld(t, c.metered, sh)
			c.setup(old)
			c.setup(now)
			want, got := run(old, before), run(now, photopipeline.Propose)
			if fmt.Sprint(got.panicked) != fmt.Sprint(want.panicked) || reflect.TypeOf(got.panicked) != reflect.TypeOf(want.panicked) {
				t.Fatalf("panicked with %v, before with %v", got.panicked, want.panicked)
			}
			// The stack the caller recovers the panic with (the worker logs
			// it) still shows where it was raised (E5 review round 1).
			if want.panicked != nil && (want.site == "" || got.site != want.site) {
				t.Errorf("a panic raised in %q, before in %q", got.site, want.site)
			}
			if !reflect.DeepEqual(got.result, want.result) {
				t.Errorf("result\n %+v\nbefore\n %+v", got.result, want.result)
			}
			if got.err != want.err || got.errType != want.errType || got.code != want.code || got.canceled != want.canceled || got.detail != want.detail {
				t.Errorf("error %q (%s, %s, canceled %v, %s), before %q (%s, %s, canceled %v, %s)", got.err, got.errType, got.code, got.canceled, got.detail,
					want.err, want.errType, want.code, want.canceled, want.detail)
			}
			if !slices.Equal(got.events, want.events) {
				t.Errorf("observed\n %q\nbefore\n %q", got.events, want.events)
			}
			if !reflect.DeepEqual(got.visions, want.visions) {
				t.Errorf("%d vision requests, before %d (or they differ)", len(got.visions), len(want.visions))
			}
			if !reflect.DeepEqual(got.segments, want.segments) {
				t.Errorf("%d segmenter calls, before %d (or they differ)", len(got.segments), len(want.segments))
			}
			if !reflect.DeepEqual(got.charges, want.charges) || !reflect.DeepEqual(got.settled, want.settled) {
				t.Errorf("spend %+v %+v, before %+v %+v", got.charges, got.settled, want.charges, want.settled)
			}
			if c.metered && len(want.charges) == 0 {
				t.Error("a metered case reserved nothing: the test would be vacuous")
			}
		})
	}
}

// bindRefusing stands for the dataset binding stage refusing a candidate
// whose node samples a dataset the base does not bind.
func bindRefusing(candidate *model.ModelIR) ([]model.ValidationIssue, error) {
	for _, n := range candidate.Nodes {
		raw, err := json.Marshal(n)
		if err != nil {
			return nil, err
		}
		if bytes.Contains(raw, []byte("dataset:aoi_times")) {
			return []model.ValidationIssue{model.NewIssue("DATASET_BINDING_INVALID", "/nodes", "the node samples a dataset the base does not bind", model.SeverityError)}, nil
		}
	}
	return nil, nil
}

// onlyImages takes images but no model choice.
type onlyImages struct{ b *fake.Backend }

func (o onlyImages) Name() string        { return o.b.Name() }
func (o onlyImages) AcceptsImages() bool { return true }
func (o onlyImages) Complete(ctx context.Context, req provider.Request) (provider.Response, error) {
	return o.b.Complete(ctx, req)
}

// cancelAfter is a segmenter whose run ends as its answer comes back.
type cancelAfter struct {
	provider.Segmenter
	cancel context.CancelFunc
}

func (c cancelAfter) Segment(ctx context.Context, req provider.SegmentRequest) (provider.Segmentation, error) {
	s, err := c.Segmenter.Segment(ctx, req)
	c.cancel()
	return s, err
}
