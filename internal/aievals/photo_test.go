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
	j.calls.Add(1)
	var pool map[string][]string
	for _, b := range req.Prompt.Blocks {
		if b.Name == "ID_ALLOCATION_POOL" {
			if err := json.Unmarshal(b.Content, &pool); err != nil {
				return provider.Response{}, provider.NewError(provider.CodeInvalidRequest, 0, "")
			}
		}
	}
	return provider.Response{Text: answerText(pool), Model: "muse-spark-1.3", Credential: "meta",
		Usage: provider.Usage{InputTokens: 6702, OutputTokens: 1100}}, nil
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

func photoSmoke(t *testing.T, j *photoJudge, s photoSAM, capNano int64) (*aievals.PhotoReport, *aievals.PhotoRun) {
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
		Model: "muse-spark-1.3", SegmentationModel: "sam-3.1", CapNanoUSD: capNano, RunID: "01M5X00000000000000000PH0T",
		Now: func() time.Time { return time.Date(2026, 10, 27, 1, 0, 0, 0, time.UTC) }}, in)
	if err != nil {
		t.Fatal(err)
	}
	return r, run
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
	judgeReserved := 3 * muse.AttemptBound(7328, provider.DefaultBudget().MaxOutputTokens)
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
		g.Reservations != 1 || g.ReservedNanoUSD != judgeReserved || g.CostNanoUSD != judgeCost || g.OverReservation {
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
	if r.Template != "photo-0.1.0" || !slices.Equal(r.Served, []string{"muse-spark-1.3", "sam-3.1"}) || r.Image.Width != 1200 ||
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
