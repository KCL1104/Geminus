package aievals_test

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json/v2"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/kcl1104/geminus/internal/ai/provider"
	"github.com/kcl1104/geminus/internal/ai/provider/fake"
	"github.com/kcl1104/geminus/internal/ai/spend"
	"github.com/kcl1104/geminus/internal/aievals"
	"github.com/kcl1104/geminus/internal/config"
	"github.com/kcl1104/geminus/internal/contracttest"
	"github.com/kcl1104/geminus/internal/packs"
)

// T-056 review round 1: the photo smoke (make ai-smoke-photo-live) runs the
// worker's photo pipeline once on the demo image. No test here calls a
// provider: the judge and the segmenter are in-process fakes billed as the
// Meta adapters bill (the HTTP path, on a local server of the Responses and
// Messages APIs, is internal/ai/provider/meta's TestThePhotoSmokeOverHTTP;
// Main's live path, internal/config/aiapproval's).

// photoJudge answers a photo proposal as the vision model would: the demo
// answer with the proposal's own ID pool (read from the prompt's
// ID_ALLOCATION_POOL block), billed in tokens; its quote is a count of 6,661
// (T-055's live count of the demo prompt) and 10%.
type photoJudge struct {
	calls     atomic.Int32
	quotes    atomic.Int32
	failQuote *provider.Error
	images    bool
	mu        sync.Mutex
	prompts   []provider.Prompt
	// first, when set, is the judge's first answer instead of the demo's
	// (H-057: one the server refuses; its repair answers the demo).
	first func(pool map[string][]string) []byte
}

func (j *photoJudge) Name() string        { return "meta" }
func (j *photoJudge) AcceptsImages() bool { return j.images }

func (j *photoJudge) Quote(context.Context, provider.Request) (provider.Quote, error) {
	j.quotes.Add(1)
	if j.failQuote != nil {
		return provider.Quote{}, j.failQuote
	}
	return provider.Quote{Model: "muse-spark-1.3", MaxInputTokens: 7328, Counted: 6661}, nil
}

// answerText is the demo answer for the prompt's pool.
var answerText func(pool map[string][]string) []byte

func (j *photoJudge) Complete(_ context.Context, req provider.Request) (provider.Response, error) {
	n := j.calls.Add(1)
	j.mu.Lock()
	j.prompts = append(j.prompts, req.Prompt)
	j.mu.Unlock()
	var pool map[string][]string
	for _, b := range req.Prompt.Blocks {
		if b.Name == "ID_ALLOCATION_POOL" {
			if err := json.Unmarshal(b.Content, &pool); err != nil {
				return provider.Response{}, provider.NewError(provider.CodeInvalidRequest, 0, "")
			}
		}
	}
	text := answerText(pool)
	if n == 1 && j.first != nil {
		text = j.first(pool)
	}
	// The text as the model wrote it too, as the Meta adapter keeps it (H-057).
	return provider.Response{Text: text, Answer: text, Model: "muse-spark-1.3", Credential: "meta",
		Usage: provider.Usage{InputTokens: 6702, OutputTokens: 1100}}, nil
}

// H-057: the smoke runs the worker's one repair too: the judge's first
// answer refused (ROUTING_INVALID), its second the demo, ready; SAM asked
// once per concept; the report's judge is the first call's, its repair the
// second's, and the run's cost both calls'.
func TestThePhotoSmokeRunsTheRepair(t *testing.T) {
	j := &photoJudge{images: true, first: func(pool map[string][]string) []byte {
		var a map[string]any
		if err := json.Unmarshal(contracttest.PhotoAnswer(t, "electronics-line-demo", pool), &a); err != nil {
			t.Fatal(err)
		}
		for _, c := range a["changes"].([]any) {
			if v, ok := c.(map[string]any)["value"].(map[string]any); ok && v["id"] == pool["flow"][2] {
				v["routing"].(map[string]any)["probability"] = 0.5
			}
		}
		out, _ := json.Marshal(a)
		return out
	}}
	r, run := photoSmoke(t, j, demoSAM(t), spend.NanoPerUSD)
	muse, _ := spend.PriceOf("muse-spark-1.3")
	judgeCost := muse.Cost(spend.Tokens{Input: 6702, Output: 1100})
	samPrice, _ := spend.ImagePriceOf("sam-3.1")
	if r.Status != "ready" || !r.Ok() || r.NewProcessSteps != 4 || j.calls.Load() != 2 || r.Segmenter.Calls != 8 {
		t.Fatalf("%s, %d judge calls, %d segmenter calls", r.Status, j.calls.Load(), r.Segmenter.Calls)
	}
	p := r.Repair
	// The photo template's own version is the first call's (r.Template; not
	// pinned here: the photo prompt has versions of its own).
	if p == nil || p.Outcome != "ready" || p.Issues == 0 || !strings.HasPrefix(r.Template, "photo-") || p.Template != r.Template+"+repair-0.1.0" ||
		p.Counted != 6661 || p.ReservedInput != 7328 ||
		p.BilledInput != 6702 || p.Output != 1100 || p.CostNanoUSD != judgeCost {
		t.Errorf("repair %+v", p)
	}
	if g := r.Judge; g.Calls != 2 || g.Quotes != 2 || g.Reservations != 2 || g.CostNanoUSD != judgeCost || g.Counted != 6661 {
		t.Errorf("judge %+v", g)
	}
	if c := r.Cost; c.NanoUSD != 8*samPrice+2*judgeCost || c.NanoUSD != run.Spent() {
		t.Errorf("cost %+v", c)
	}
	data, err := r.Marshal()
	if err != nil {
		t.Fatal(err)
	}
	for _, leak := range []string{"floor plan shows", "Add the AOI", "SERVER_REVIEW", "ROUTING_INVALID", "untrusted_data"} {
		if bytes.Contains(data, []byte(leak)) {
			t.Errorf("the report holds %q", leak)
		}
	}
	if second := j.prompts[1]; second.Revision == nil || second.User() != j.prompts[0].User() || len(second.Images) != 1 {
		t.Errorf("the repair's request")
	}
}

// photoSAM is the fake segmenter billed as SAM 3.1 bills: one image a
// concept it answered.
type photoSAM struct{ *fake.Segmenter }

func (s photoSAM) SegmentModel() string { return "sam-3.1" }

func (s photoSAM) SegmentOne(ctx context.Context, call provider.SegmentCall) (provider.SegmentAnswer, error) {
	a, err := s.Segmenter.SegmentOne(ctx, call)
	if a.Answered {
		a.ImagesProcessed, a.Model = 1, "sam-3.1"
	}
	return a, err
}

// demoSAM is photoSAM scripted with the demo image's segments.
func demoSAM(t *testing.T) photoSAM {
	t.Helper()
	d := contracttest.LoadPhotoDemo(t, "electronics-line-demo")
	seg := fake.NewSegmenter()
	by := d.Segments.ByConcept()
	for _, c := range d.Segments.Concepts {
		var segs []provider.Segment
		for _, x := range by[c] {
			segs = append(segs, provider.Segment{ObjectID: x.ObjectID, Confidence: x.Confidence, Box: provider.Box{X1: x.Box.X1, Y1: x.Box.Y1, X2: x.Box.X2, Y2: x.Box.Y2}})
		}
		seg.Script(c, d.Segments.Width, d.Segments.Height, segs...)
	}
	return photoSAM{seg}
}

func photoSmoke(t *testing.T, j *photoJudge, s provider.BillableSegmenter, capNano int64) (*aievals.PhotoReport, *aievals.PhotoRun) {
	t.Helper()
	r, run, err := photoSmokeWith(t, j, s, capNano, nil)
	if err != nil {
		t.Fatal(err)
	}
	return r, run
}

// photoSmokeWith is photoSmoke asking the segmenter for concepts instead of
// the pack's (nil: the pack's).
func photoSmokeWith(t *testing.T, j *photoJudge, s provider.BillableSegmenter, capNano int64, concepts []string) (*aievals.PhotoReport, *aievals.PhotoRun, error) {
	t.Helper()
	answerText = func(pool map[string][]string) []byte {
		return contracttest.PhotoAnswer(t, "electronics-line-demo", pool)
	}
	root := contracttest.Root(t)
	reg, err := packs.Registry()
	if err != nil {
		t.Fatal(err)
	}
	in, err := aievals.ReadPhotoInputs(context.Background(), root, reg)
	if err != nil {
		t.Fatal(err)
	}
	run, err := aievals.NewPhotoRun(j, s, provider.DefaultBudget(), capNano)
	if err != nil {
		t.Fatal(err)
	}
	r, err := run.Run(context.Background(), aievals.PhotoConfig{Packs: reg, Root: root, Locale: "en", Provider: "meta", Approval: "H-029",
		Model: "muse-spark-1.3", SegmentationModel: "sam-3.1", CapNanoUSD: capNano, RunID: "01M5X00000000000000000PH0T", Concepts: concepts,
		Now: func() time.Time { return time.Date(2026, 10, 27, 1, 0, 0, 0, time.UTC) }}, in)
	return r, run, err
}

// The demo, as the worker runs it: 8 concepts, each reserved for at 3 SAM
// images and settled at the one billed; the judge counted (6,661 tokens),
// reserved at the count and 10% for 3 attempts, billed at its usage; the
// server reviews the answer and it is ready with the server's risk. The
// report says all of that, and nothing of the prompt, the answer, a box,
// the image or a key.
func TestThePhotoSmokeRunsThePipeline(t *testing.T) {
	j := &photoJudge{images: true}
	r, run := photoSmoke(t, j, demoSAM(t), spend.NanoPerUSD)
	d := contracttest.LoadPhotoDemo(t, "electronics-line-demo")
	samPrice, _ := spend.ImagePriceOf("sam-3.1")
	muse, _ := spend.PriceOf("muse-spark-1.3")
	judgeReserved := 3 * muse.AttemptBound(7328, provider.ImageMaxOutputTokens)
	judgeCost := muse.Cost(spend.Tokens{Input: 6702, Output: 1100})
	if r.Status != "ready" || r.ServerRisk == "" || r.PatchHash == "" || !r.Complete || !r.Ok() || r.NewProcessSteps != 4 {
		t.Fatalf("%+v", r)
	}
	if s := r.Segmenter; s.Concepts != 8 || s.Calls != 8 || s.Answered != 8 || s.Boxes != len(d.Segments.Segments) ||
		!slices.Equal(s.ImagesProcessed, []int{1, 1, 1, 1, 1, 1, 1, 1}) || s.ImagesBilled != 8 || !slices.Equal(s.Frames, []string{"1200x520"}) ||
		s.Reservations != 8 || s.ReservedNanoUSD != 8*3*samPrice || s.CostNanoUSD != 8*samPrice || s.OverReservation || s.WithBoxes == 0 {
		t.Errorf("segmenter %+v", s)
	}
	if g := r.Judge; !g.Asked || g.Quotes != 1 || g.Calls != 1 || g.Counted != 6661 || g.ReservedInput != 7328 || g.BilledInput != 6702 ||
		g.Reservations != 1 || g.ReservedNanoUSD != judgeReserved || g.CostNanoUSD != judgeCost || g.OverReservation ||
		g.MaxOutput != provider.ImageMaxOutputTokens || g.Thinking != nil {
		t.Errorf("judge %+v", g)
	}
	if c := r.Cost; c.NanoUSD != 8*samPrice+judgeCost || c.NanoUSD != run.Spent() || c.CommittedNanoUSD != c.NanoUSD ||
		c.PeakNanoUSD != 8*samPrice+judgeReserved || c.CapNanoUSD != spend.NanoPerUSD {
		t.Errorf("cost %+v", c)
	}
	for _, w := range r.Warnings {
		if w != 0 {
			t.Errorf("observations %v", r.Warnings)
		}
	}
	if r.Template != "photo-0.2.0" || !slices.Equal(r.Served, []string{"muse-spark-1.3", "sam-3.1"}) || r.Image.Width != 1200 ||
		r.Image.StoredSHA256 != d.Segments.StoredDigest || !r.Image.VerifiedAsJob || r.Base.Domain != "electronics_line" {
		t.Errorf("header %+v", r)
	}
	// What the report keeps.
	data, err := r.Marshal()
	if err != nil {
		t.Fatal(err)
	}
	stored, _ := os.ReadFile(filepath.Join(contracttest.Root(t), aievals.PhotoImage))
	for _, leak := range []string{"floor plan shows", "Add the AOI", "ID_ALLOCATION_POOL", base64.StdEncoding.EncodeToString(stored)[:64],
		`"x1"`, "untrusted_data", "SEGMENTS", "AOI machine"} {
		if bytes.Contains(data, []byte(leak)) {
			t.Errorf("the report holds %q", leak)
		}
	}
	// It names its own file, apart from the fake results and the other
	// runs, and never replaces one.
	dir := t.TempDir()
	start := time.Date(2026, 10, 27, 1, 0, 0, 0, time.UTC)
	path := aievals.ReportPath(dir, aievals.PhotoSmokeKind, start, r.RunID, r.Model)
	if filepath.Base(path) != "live-smoke-photo-2026-10-27T010000Z-01M5X00000000000000000PH0T-muse-spark-1.3.json" {
		t.Errorf("report name %s", path)
	}
	if err := aievals.WritePhotoReport(path, r); err != nil {
		t.Fatal(err)
	}
	if err := aievals.WritePhotoReport(path, r); err != aievals.ErrReportExists {
		t.Errorf("a second write: %v", err)
	}
	if err := aievals.WritePhotoReport(filepath.Join(dir, "fake-provider.json"), r); err != aievals.ErrNotALiveReport {
		t.Errorf("a fake result's name: %v", err)
	}
}

// H-037: a count that fails ends the judge's call before anything is
// reserved or sent; the segmentation was made and paid, and the report
// says so (failed, its code, SAM's cost, no judge reservation or call).
func TestAFailedCountSendsNothing(t *testing.T) {
	j := &photoJudge{images: true, failQuote: provider.NewError(provider.CodeUnavailable, 503, "meta")}
	r, run := photoSmoke(t, j, demoSAM(t), spend.NanoPerUSD)
	samPrice, _ := spend.ImagePriceOf("sam-3.1")
	if r.Status != "failed" || r.Code != "AI_UNAVAILABLE" || r.Ok() || r.Gates[aievals.GatePipelineFailed] != 1 || !r.Complete {
		t.Fatalf("%+v", r)
	}
	if j.calls.Load() != 0 || r.Judge.Calls != 0 || r.Judge.Reservations != 0 || r.Judge.QuotesFailed != 1 || r.Judge.ReservedNanoUSD != 0 {
		t.Errorf("judge %+v (calls %d)", r.Judge, j.calls.Load())
	}
	if r.Segmenter.CostNanoUSD != 8*samPrice || run.Spent() != 8*samPrice || r.Cost.NanoUSD != 8*samPrice {
		t.Errorf("segmenter %+v spent %d", r.Segmenter, run.Spent())
	}
}

// The run's cap holds every reservation: a cap below the judge's
// reservation stops the run there (AI_QUOTA_EXCEEDED, incomplete: never
// sent); a cap below one concept's stops it before any call.
func TestTheCapStopsThePhotoSmoke(t *testing.T) {
	samPrice, _ := spend.ImagePriceOf("sam-3.1")
	j := &photoJudge{images: true}
	r, _ := photoSmoke(t, j, demoSAM(t), 100_000_000)
	if r.Status != "failed" || r.Code != "AI_QUOTA_EXCEEDED" || r.Complete || r.Ok() || j.calls.Load() != 0 || r.Segmenter.Calls != 8 ||
		r.Cost.NanoUSD != 8*samPrice {
		t.Errorf("below the judge's reservation: %+v (calls %d)", r, j.calls.Load())
	}
	j = &photoJudge{images: true}
	r, _ = photoSmoke(t, j, demoSAM(t), 3*samPrice-1)
	if r.Status != "failed" || r.Code != "AI_QUOTA_EXCEEDED" || r.Complete || r.Segmenter.Calls != 0 || j.quotes.Load() != 0 || r.Cost.NanoUSD != 0 {
		t.Errorf("below one concept's: %+v", r)
	}
}

// A judge that takes no image is refused before the segmenter spends
// anything, as the generator does (AI_IMAGE_UNSUPPORTED).
func TestThePhotoSmokeAsksTheJudgeFirst(t *testing.T) {
	j := &photoJudge{images: false}
	s := demoSAM(t)
	r, run := photoSmoke(t, j, s, spend.NanoPerUSD)
	if r.Status != "failed" || r.Code != "AI_IMAGE_UNSUPPORTED" || len(s.Calls()) != 0 || j.quotes.Load() != 0 || run.Spent() != 0 {
		t.Errorf("%+v", r)
	}
}

// The observations the first live call is to show (H-037, the review's
// I1): billed beyond the reservation's input, SAM's images other than one,
// a frame that is not the image's, no box at all.
func TestThePhotoSmokeObserves(t *testing.T) {
	j := &photoJudge{images: true}
	seg := fake.NewSegmenter()
	r, _ := photoSmoke(t, j, photoSAM{seg}, spend.NanoPerUSD)
	if r.Status != "needs_input" || r.Warnings[aievals.WarningNoBoxes] != 1 || j.calls.Load() != 0 || !r.Ok() {
		t.Errorf("no boxes: %+v", r)
	}
	other := fake.NewSegmenter()
	other.Script("smt line", 520, 1200, provider.Segment{ObjectID: 0, Confidence: 0.9, Box: provider.Box{X1: 1, Y1: 1, X2: 10, Y2: 10}})
	r, _ = photoSmoke(t, &photoJudge{images: true}, photoSAM{other}, spend.NanoPerUSD)
	if r.Status != "invalid" || r.Warnings[aievals.WarningFrame] != 1 || !slices.Equal(r.Segmenter.Frames, []string{"520x1200"}) {
		t.Errorf("a turned frame: %+v", r)
	}
	// SAM says it processed two images for a concept: observed, and settled
	// as billed (two images).
	samPrice, _ := spend.ImagePriceOf("sam-3.1")
	r, _ = photoSmoke(t, &photoJudge{images: true}, twoImages{demoSAM(t)}, spend.NanoPerUSD)
	if r.Status != "ready" || r.Warnings[aievals.WarningImagesProcessed] != 1 || r.Segmenter.ImagesProcessed[0] != 2 || r.Segmenter.CostNanoUSD != 9*samPrice {
		t.Errorf("two images: %+v", r.Segmenter)
	}
}

// twoImages is photoSAM saying, for its first concept, that it billed two
// images.
type twoImages struct{ photoSAM }

func (s twoImages) SegmentOne(ctx context.Context, call provider.SegmentCall) (provider.SegmentAnswer, error) {
	a, err := s.photoSAM.SegmentOne(ctx, call)
	if call.Concept == "smt line" {
		a.ImagesProcessed = 2
	}
	return a, err
}

// Main refuses the photo smoke, before building any backend and writing
// anything, while the photo path is off as the worker decides it: nothing
// set, H-029 without the switch, Claude's H-018 (its approval names no
// image), or with another mode's flags; and with no photo backends in the
// build. The key is never printed.
func TestMainRefusesThePhotoSmokeWhileThePhotoPathIsOff(t *testing.T) {
	root := contracttest.Root(t)
	h029 := map[string]string{"AI_PROVIDER": "meta", "AI_POLICY_APPROVAL": "H-029", "AI_MODEL": "muse-spark-1.3", "GEMINUS_MUSE_API_KEY": museKey}
	h018 := map[string]string{"AI_PROVIDER": "anthropic", "AI_POLICY_APPROVAL": "H-018", "AI_MODEL": "claude-opus-5-5",
		"GEMINUS_ANTHROPIC_API_KEY_A": "sk-ant-test-fake-aaaaaaaaaaaa", "GEMINUS_ANTHROPIC_API_KEY_B": "sk-ant-test-fake-bbbbbbbbbbbb", "GEMINUS_AI_LIVE": "on"}
	on := map[string]string{"GEMINUS_AI_LIVE": "on"}
	for k, v := range h029 {
		on[k] = v
	}
	for name, c := range map[string]struct {
		env   map[string]string
		args  []string
		photo bool
		want  string
	}{
		"nothing set":            {map[string]string{}, nil, true, "the photo path is off (the photo path's provider is not a live provider)"},
		"H-029 without a switch": {h029, nil, true, "the photo path is off (switch_off)"},
		"Claude's H-018":         {h018, nil, true, "the photo path is off (images_not_approved)"},
		"with -smoke":            {on, []string{"-smoke"}, true, "-smoke-photo runs alone"},
		"with -cases":            {on, []string{"-cases", "MOD-002"}, true, "-smoke-photo runs alone"},
		"no photo backends":      {on, nil, false, "this build has no photo backends"},
	} {
		t.Run(name, func(t *testing.T) {
			dir := t.TempDir()
			var out, errb bytes.Buffer
			built := 0
			b := aievals.Backends{Text: func(config.AI) (provider.Billable, error) { built++; return nil, nil }}
			if c.photo {
				b.Photo = func(config.AIVision) (provider.Billable, provider.BillableSegmenter, error) {
					built++
					return nil, nil, nil
				}
			}
			args := append([]string{"-root", root, "-dir", dir, "-smoke-photo"}, c.args...)
			code := aievals.MainWith(context.Background(), args, func(k string) string { return c.env[k] }, &out, &errb, b)
			if code != aievals.ExitRefused || built != 0 {
				t.Fatalf("exit %d, backends built %d: %s", code, built, errb.String())
			}
			if !strings.Contains(errb.String(), "REFUSED (no provider call was made)") || !strings.Contains(errb.String(), c.want) {
				t.Errorf("stderr %q, want %q", errb.String(), c.want)
			}
			for _, k := range []string{museKey, "SENTINEL", "sk-ant-test-fake"} {
				if strings.Contains(errb.String()+out.String(), k) {
					t.Error("a key is printed")
				}
			}
			if entries, _ := os.ReadDir(dir); len(entries) != 0 {
				t.Errorf("%d files written", len(entries))
			}
		})
	}
	// The photo smoke's report is apart from the text runs': its own kind
	// and prefix; the record takes its kind.
	if n := aievals.ReportName(aievals.PhotoSmokeKind, time.Date(2026, 10, 27, 1, 2, 3, 0, time.UTC), "01M5X00000000000000000PH0T", "muse-spark-1.3"); n !=
		"live-smoke-photo-2026-10-27T010203Z-01M5X00000000000000000PH0T-muse-spark-1.3.json" {
		t.Errorf("%s", n)
	}
	if strconv.Itoa(int(aievals.DefaultPhotoSmokeCap/spend.NanoPerUSD)) != "1" {
		t.Errorf("the photo smoke's default cap is USD %s", spend.FormatUSD(aievals.DefaultPhotoSmokeCap))
	}
}

// shapesSAM is photoSAM scripted with the demo floor plan's shapes, as SAM
// might answer the floor plan concepts (contracttest's ShapeSegments: the
// six drawn stations as "rectangle", the four conveyors as "arrow"),
// nothing for any other concept.
func shapesSAM(t *testing.T) photoSAM {
	t.Helper()
	d := contracttest.LoadPhotoDemo(t, "electronics-line-demo")
	by := map[string][]provider.Segment{}
	for _, x := range d.Segments.ShapeSegments() {
		by[x.Concept] = append(by[x.Concept], provider.Segment{ObjectID: x.ObjectID, Confidence: x.Confidence,
			Box: provider.Box{X1: x.Box.X1, Y1: x.Box.Y1, X2: x.Box.X2, Y2: x.Box.Y2}})
	}
	seg := fake.NewSegmenter()
	for c, segs := range by {
		seg.Script(c, d.Segments.Width, d.Segments.Height, segs...)
	}
	return photoSAM{seg}
}

// T-056 after the first live run (01M5XDJ3T0TTGE55K3A3JVK29N: SAM 3.1 found
// none of the pack's 8 concepts in the drawn floor plan): the smoke can ask
// SAM for other concepts, to explore their wording live through the
// pipeline the worker runs, never by hand. The same pipeline, cap and
// meter: one SAM image a concept; the judge sees the concepts asked for and
// the boxes; the server's review and photo rules decide. The report says
// the concepts were overridden, which, and for each concept how many boxes
// SAM found and their sizes as shares of the frame: never where a box is,
// nor the image.
func TestThePhotoSmokeTakesOtherConcepts(t *testing.T) {
	j := &photoJudge{images: true}
	s := shapesSAM(t)
	concepts := []string{"rectangle", "box", "arrow"}
	r, run, err := photoSmokeWith(t, j, s, spend.NanoPerUSD, concepts)
	if err != nil {
		t.Fatal(err)
	}
	samPrice, _ := spend.ImagePriceOf("sam-3.1")
	muse, _ := spend.PriceOf("muse-spark-1.3")
	if r.Status != "ready" || !r.Ok() || r.NewProcessSteps != 4 || r.ServerRisk == "" || !r.ConceptsOverridden ||
		!slices.Equal(r.Segmenter.ConceptList, concepts) {
		t.Fatalf("%+v", r)
	}
	var asked []string
	for _, c := range s.Calls() {
		asked = append(asked, c.Concept)
	}
	if !slices.Equal(asked, concepts) || r.Segmenter.Concepts != 3 || r.Segmenter.Calls != 3 || r.Segmenter.Boxes != 10 ||
		r.Segmenter.BoxesByConcept["rectangle"] != 6 || r.Segmenter.BoxesByConcept["arrow"] != 4 || r.Segmenter.WithBoxes != 2 ||
		r.Segmenter.CostNanoUSD != 3*samPrice || r.Segmenter.ReservedNanoUSD != 3*3*samPrice {
		t.Errorf("segmenter %+v (asked %q)", r.Segmenter, asked)
	}
	if r.Cost.NanoUSD != 3*samPrice+muse.Cost(spend.Tokens{Input: 6702, Output: 1100}) || r.Cost.NanoUSD != run.Spent() {
		t.Errorf("cost %+v", r.Cost)
	}
	// Per concept, in the order asked: the boxes and their sizes as shares
	// of the frame (SMT's 190 x 120 of 1200 x 520; a conveyor's 100 x 16).
	by := r.Segmenter.ByConcept
	if len(by) != 3 || by[0].Concept != "rectangle" || by[0].Boxes != 6 || len(by[0].Sizes) != 6 || by[1].Concept != "box" ||
		by[1].Boxes != 0 || len(by[1].Sizes) != 0 || !by[1].Answered || by[2].Concept != "arrow" || by[2].Boxes != 4 || len(by[2].Sizes) != 4 {
		t.Fatalf("by concept %+v", by)
	}
	if by[0].Sizes[0] != (aievals.BoxSize{Width: 0.158, Height: 0.231}) || by[2].Sizes[0] != (aievals.BoxSize{Width: 0.083, Height: 0.031}) {
		t.Errorf("sizes %+v %+v", by[0].Sizes, by[2].Sizes)
	}
	// The judge was asked with those concepts and the shape boxes.
	j.mu.Lock()
	prompts := j.prompts
	j.mu.Unlock()
	if len(prompts) != 1 {
		t.Fatalf("%d judge calls", len(prompts))
	}
	user := prompts[0].User()
	if !strings.Contains(user, `["rectangle","box","arrow"]`) || !strings.Contains(user, `"concept":"rectangle"`) || !strings.Contains(user, `"concept":"arrow"`) ||
		strings.Contains(user, `"concept":"smt line"`) {
		t.Errorf("the judge's prompt does not hold the concepts asked for and their boxes")
	}
	// What the report keeps: no box's place, no image.
	data, err := r.Marshal()
	if err != nil {
		t.Fatal(err)
	}
	stored, _ := os.ReadFile(filepath.Join(contracttest.Root(t), aievals.PhotoImage))
	for _, leak := range []string{`"x1"`, `"y1"`, `"x2"`, `"y2"`, `"confidence"`, base64.StdEncoding.EncodeToString(stored)[:64], "SEGMENTS"} {
		if bytes.Contains(data, []byte(leak)) {
			t.Errorf("the report holds %q", leak)
		}
	}
	for _, want := range []string{`"concepts_overridden": true`, `"concept_list": [`, `"by_concept": [`, `"width_share": 0.158`} {
		if !bytes.Contains(data, []byte(want)) {
			t.Errorf("the report lacks %s", want)
		}
	}
}

// The smoke with the pack's own concepts (D-131 (b) after the first live
// run: the machines and conveyor of a photo, then a floor plan's rectangle
// and arrow), on SAM answering the drawn floor plan's shapes only: still 8
// SAM images (USD 0.02), the six rectangles and four arrows reach the
// judge, and its answer is ready with the server's risk.
func TestThePhotoSmokeFindsTheFloorPlansShapes(t *testing.T) {
	j := &photoJudge{images: true}
	s := shapesSAM(t)
	r, run := photoSmoke(t, j, s, spend.NanoPerUSD)
	samPrice, _ := spend.ImagePriceOf("sam-3.1")
	muse, _ := spend.PriceOf("muse-spark-1.3")
	if r.Status != "ready" || !r.Ok() || r.NewProcessSteps != 4 || r.ConceptsOverridden || r.Template != "photo-0.2.0" {
		t.Fatalf("%+v", r)
	}
	if sg := r.Segmenter; sg.Concepts != 8 || sg.Calls != 8 || sg.Boxes != 10 || sg.BoxesByConcept["rectangle"] != 6 || sg.BoxesByConcept["arrow"] != 4 ||
		sg.WithBoxes != 2 || sg.CostNanoUSD != 8*samPrice || len(sg.ByConcept) != 8 || sg.ByConcept[6].Concept != "rectangle" || sg.ByConcept[7].Concept != "arrow" {
		t.Errorf("segmenter %+v", sg)
	}
	if r.Cost.NanoUSD != 8*samPrice+muse.Cost(spend.Tokens{Input: 6702, Output: 1100}) || r.Cost.NanoUSD != run.Spent() || r.Warnings[aievals.WarningNoBoxes] != 0 {
		t.Errorf("cost %+v observations %v", r.Cost, r.Warnings)
	}
}

// A box SAM puts outside its own frame is counted for its concept but has
// no size (the client refuses the answer: invalid); the whole frame is 1 by
// 1.
func TestABoxOutsideTheFrameHasNoSize(t *testing.T) {
	seg := fake.NewSegmenter()
	seg.Script("rectangle", 1200, 520, provider.Segment{ObjectID: 0, Confidence: 0.9, Box: provider.Box{X1: 1100, Y1: 10, X2: 1299, Y2: 59}},
		provider.Segment{ObjectID: 1, Confidence: 0.9, Box: provider.Box{X1: 0, Y1: 0, X2: 1199, Y2: 519}})
	r, _, err := photoSmokeWith(t, &photoJudge{images: true}, photoSAM{seg}, spend.NanoPerUSD, []string{"rectangle"})
	if err != nil {
		t.Fatal(err)
	}
	if by := r.Segmenter.ByConcept; r.Status != "invalid" || len(by) != 1 || by[0].Boxes != 2 ||
		!slices.Equal(by[0].Sizes, []aievals.BoxSize{{Width: 1, Height: 1}}) {
		t.Errorf("%s %+v", r.Status, by)
	}
}

// Without an override the report says so and lists the pack's concepts; an
// override the client would refuse is refused before any call.
func TestThePhotoSmokesConceptsAreThePacksByDefault(t *testing.T) {
	r, _ := photoSmoke(t, &photoJudge{images: true}, demoSAM(t), spend.NanoPerUSD)
	d := contracttest.LoadPhotoDemo(t, "electronics-line-demo")
	if r.ConceptsOverridden || !slices.Equal(r.Segmenter.ConceptList, d.Segments.Concepts) || len(r.Segmenter.ByConcept) != len(d.Segments.Concepts) {
		t.Errorf("%v %q %d", r.ConceptsOverridden, r.Segmenter.ConceptList, len(r.Segmenter.ByConcept))
	}
	for _, bad := range [][]string{{}, {"Rectangle"}, {"rectangle", "rectangle"}, {"rectangle, arrow"}, {"a", "b", "c", "d", "e", "f", "g", "h", "i"}} {
		j, s := &photoJudge{images: true}, shapesSAM(t)
		r, run, err := photoSmokeWith(t, j, s, spend.NanoPerUSD, bad)
		if err == nil || r != nil || len(s.Calls()) != 0 || j.quotes.Load() != 0 || run.Committed() != 0 {
			t.Errorf("%q: %v %+v", bad, err, r)
		}
	}
}

// The override is the photo smoke's alone: -concepts without -smoke-photo is
// refused before the configuration is read, and no code but the smoke's
// (internal/aievals) reads AI_SMOKE_PHOTO_CONCEPTS: not the worker, the
// API, config or a deployment file.
func TestTheConceptOverrideIsTheSmokesOnly(t *testing.T) {
	for _, args := range [][]string{{"-concepts", "rectangle"}, {"-smoke", "-concepts", "rectangle"}} {
		var out, errb bytes.Buffer
		read := 0
		code := aievals.MainWith(context.Background(), args, func(string) string { read++; return "" }, &out, &errb, aievals.Backends{})
		if code != aievals.ExitRefused || read != 0 || !strings.Contains(errb.String(), "-concepts is the photo smoke's (-smoke-photo)") {
			t.Errorf("%q: exit %d, %d variables read: %s", args, code, read, errb.String())
		}
	}
	root := contracttest.Root(t)
	var readers []string
	for _, top := range []string{"cmd", "internal", "deploy", "scripts", "apps/web/lib", "apps/web/app"} {
		err := filepath.WalkDir(filepath.Join(root, top), func(path string, d os.DirEntry, err error) error {
			if err != nil || d.IsDir() || strings.HasSuffix(path, "_test.go") || strings.Contains(path, "node_modules") {
				return err
			}
			data, err := os.ReadFile(path)
			if err != nil {
				return err
			}
			if bytes.Contains(data, []byte("AI_SMOKE_PHOTO_CONCEPTS")) {
				rel, _ := filepath.Rel(root, path)
				readers = append(readers, filepath.ToSlash(rel))
			}
			return nil
		})
		if err != nil && !os.IsNotExist(err) {
			t.Fatal(err)
		}
	}
	if !slices.Contains(readers, "internal/aievals/main.go") {
		t.Errorf("the smoke does not name AI_SMOKE_PHOTO_CONCEPTS: %q", readers)
	}
	for _, r := range readers {
		if !strings.HasPrefix(r, "internal/aievals/") {
			t.Errorf("AI_SMOKE_PHOTO_CONCEPTS is named in %s", r)
		}
	}
}
