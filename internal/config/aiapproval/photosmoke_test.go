package aiapproval_test

import (
	"bytes"
	"context"
	"encoding/json/v2"
	"os"
	"path/filepath"
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
)

// T-056 review round 1: make ai-smoke-photo-live (cmd/ai-eval-live
// -smoke-photo), the first live SAM 3.1 call, through Main's live path:
// the worker's photo-path gate (H-029 names images and SAM 3.1), the run's
// own cap (USD 1 by default) against the daily limit and what the spend
// record leaves of the day, the record opened before the first call and
// closed after, the report written to its own file. No network: the judge
// and the segmenter are in-process fakes billed as Meta's adapters bill
// (the HTTP path is internal/ai/provider/meta's TestThePhotoSmokeOverHTTP),
// the key a placeholder.

// h029Env is the photo path on as the worker turns it on (AI_PROVIDER meta,
// followed by the photo path).
func h029Env() map[string]string {
	return map[string]string{"AI_PROVIDER": "meta", "AI_POLICY_APPROVAL": "H-029", "AI_MODEL": "muse-spark-1.3",
		"GEMINUS_MUSE_API_KEY": museKey, "GEMINUS_AI_LIVE": "on", "MODEL_API_KEY": "must-not-be-read"}
}

type smokeJudge struct {
	quotes, calls atomic.Int32
	failQuote     bool
	t             *testing.T
}

func (j *smokeJudge) Name() string        { return "meta" }
func (j *smokeJudge) AcceptsImages() bool { return true }

func (j *smokeJudge) Quote(context.Context, provider.Request) (provider.Quote, error) {
	j.quotes.Add(1)
	if j.failQuote {
		return provider.Quote{}, provider.NewError(provider.CodeUnavailable, 500, "meta")
	}
	return provider.Quote{Model: "muse-spark-1.3", MaxInputTokens: 7328, Counted: 6661}, nil
}

func (j *smokeJudge) Complete(_ context.Context, req provider.Request) (provider.Response, error) {
	j.calls.Add(1)
	var pool map[string][]string
	for _, b := range req.Prompt.Blocks {
		if b.Name == "ID_ALLOCATION_POOL" {
			_ = json.Unmarshal(b.Content, &pool)
		}
	}
	return provider.Response{Text: contracttest.PhotoAnswer(j.t, "electronics-line-demo", pool), Model: "muse-spark-1.3", Credential: "meta",
		Usage: provider.Usage{InputTokens: 6702, OutputTokens: 1100}}, nil
}

type smokeSAM struct{ *fake.Segmenter }

func (s smokeSAM) SegmentModel() string { return "sam-3.1" }

func (s smokeSAM) SegmentOne(ctx context.Context, call provider.SegmentCall) (provider.SegmentAnswer, error) {
	a, err := s.Segmenter.SegmentOne(ctx, call)
	if a.Answered {
		a.ImagesProcessed, a.Model = 1, "sam-3.1"
	}
	return a, err
}

func smokeSegmenter(t *testing.T) smokeSAM {
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
	return smokeSAM{seg}
}

type photoMain struct {
	code           int
	stdout, stderr string
	built          int
	asked          config.AIVision
	judge          *smokeJudge
}

func runPhotoMain(t *testing.T, env map[string]string, dir string, judge *smokeJudge) photoMain {
	t.Helper()
	root := contracttest.Root(t)
	r := photoMain{judge: judge}
	b := aievals.Backends{
		Text: func(config.AI) (provider.Billable, error) { t.Error("the text backend was built"); return nil, nil },
		Photo: func(v config.AIVision) (provider.Billable, provider.BillableSegmenter, error) {
			r.built++
			r.asked = v
			return judge, smokeSegmenter(t), nil
		},
	}
	var out, errb bytes.Buffer
	r.code = aievals.MainWith(context.Background(), []string{"-root", root, "-dir", dir, "-smoke-photo"}, func(k string) string { return env[k] }, &out, &errb, b)
	r.stdout, r.stderr = out.String(), errb.String()
	return r
}

// With H-029, its model, the key and the switch, the photo smoke builds the
// photo path's backends (here in-process: never Meta's), checks the judge
// takes images, opens the run in the spend record (kind smoke-photo, the
// judge's model, H-029, USD 1), runs the pipeline once, writes
// live-smoke-photo-*.json and closes the run with what it cost; neither
// the report, the record nor the output holds the key.
func TestThePhotoSmokeRunsBehindTheWorkersGate(t *testing.T) {
	dir := emptyRecord(t)
	r := runPhotoMain(t, h029Env(), dir, &smokeJudge{t: t})
	if r.code != aievals.ExitOK || r.built != 1 || r.judge.calls.Load() != 1 {
		t.Fatalf("exit %d built %d calls %d: %s %s", r.code, r.built, r.judge.calls.Load(), r.stdout, r.stderr)
	}
	if v := r.asked; !v.LiveEnabled() || v.Provider != config.AIProviderMeta || v.Model != "muse-spark-1.3" || v.SegmentationModel != "sam-3.1" ||
		len(v.Credentials) != 1 || v.Credentials[0].Label != "meta" || v.Credentials[0].Key.Reveal() != museKey {
		t.Errorf("the backends were asked for %+v", v)
	}
	reports, _ := filepath.Glob(filepath.Join(dir, "live-smoke-photo-*-muse-spark-1.3.json"))
	if len(reports) != 1 {
		t.Fatalf("reports %v", reports)
	}
	data, err := os.ReadFile(reports[0])
	if err != nil {
		t.Fatal(err)
	}
	var rep aievals.PhotoReport
	if err := json.Unmarshal(data, &rep); err != nil {
		t.Fatal(err)
	}
	samPrice, _ := spend.ImagePriceOf("sam-3.1")
	muse, _ := spend.PriceOf("muse-spark-1.3")
	cost := 8*samPrice + muse.Cost(spend.Tokens{Input: 6702, Output: 1100})
	if rep.Kind != aievals.PhotoSmokeKind || rep.Status != "ready" || rep.Approval != "H-029" || rep.SegmentationModel != "sam-3.1" ||
		rep.Cost.NanoUSD != cost || rep.Cost.CapNanoUSD != spend.NanoPerUSD || rep.Judge.Counted != 6661 ||
		!strings.HasSuffix(filepath.Base(reports[0]), "-"+rep.RunID+"-muse-spark-1.3.json") {
		t.Errorf("report %s %+v", reports[0], rep)
	}
	lines := recordLines(t, dir)
	if len(lines) != 2 || lines[0]["event"] != "open" || lines[0]["kind"] != "smoke-photo" || lines[0]["model"] != "muse-spark-1.3" ||
		lines[0]["approval"] != "H-029" || lines[0]["cap_nano_usd"] != float64(spend.NanoPerUSD) || lines[0]["report"] != filepath.Base(reports[0]) ||
		lines[1]["event"] != "close" || lines[1]["result"] != "complete" || lines[1]["settled_nano_usd"] != float64(cost) {
		t.Errorf("the spend record: %v", lines)
	}
	record, _ := os.ReadFile(filepath.Join(dir, recordName))
	for _, leak := range []string{museKey, "SENTINEL", "must-not-be-read"} {
		if bytes.Contains(data, []byte(leak)) || bytes.Contains(record, []byte(leak)) || strings.Contains(r.stdout+r.stderr, leak) {
			t.Errorf("%q is in the report, the record or the output", leak)
		}
	}
	if !strings.Contains(r.stdout, "status ready") || !strings.Contains(r.stdout, "judge counted 6661 and billed 6702 input tokens (reserved for 7328)") {
		t.Errorf("summary %q", r.stdout)
	}
	// The spend record now holds the run: the next one sees it.
	if lines := recordLines(t, dir); len(lines) != 2 {
		t.Errorf("%v", lines)
	}
}

// Refused before any backend is built and nothing written: the cap above
// the daily limit, or above what the spend record leaves of the day; a
// record that cannot be used. And after the backends are built but before
// any call: a factory that fails.
func TestThePhotoSmokeCapFitsTheDay(t *testing.T) {
	now := time.Now().UTC()
	usd := func(x float64) int64 { return int64(x * float64(spend.NanoPerUSD)) }
	for name, c := range map[string]struct {
		env    map[string]string
		record *string
		want   string
	}{
		"the cap above the daily limit": {map[string]string{"AI_EVAL_LIVE_MAX_USD": "10.01"}, nil, "above the deployment's daily limit"},
		"the default cap above what the day has left": {nil, ptr(openLine(runA, "eval", now, usd(9.5)) + closeLine(runA, now, usd(9.25), "complete")),
			"more than the UTC day has left"},
		"an open run's whole cap":     {nil, ptr(openLine(runA, "smoke", now, usd(9.1))), "more than the UTC day has left"},
		"a cap that is not an amount": {map[string]string{"AI_EVAL_LIVE_MAX_USD": "-1"}, nil, "AI_EVAL_LIVE_MAX_USD is not a positive amount"},
		"no record":                   {nil, ptr("\x00missing"), "cannot be used"},
	} {
		t.Run(name, func(t *testing.T) {
			dir := t.TempDir()
			if c.record == nil {
				dir = emptyRecord(t)
			} else if *c.record != "\x00missing" {
				if err := os.WriteFile(filepath.Join(dir, recordName), []byte(*c.record), 0o644); err != nil {
					t.Fatal(err)
				}
			}
			before, _ := os.ReadFile(filepath.Join(dir, recordName))
			env := h029Env()
			for k, v := range c.env {
				env[k] = v
			}
			j := &smokeJudge{t: t}
			r := runPhotoMain(t, env, dir, j)
			if r.code != aievals.ExitRefused || r.built != 0 || j.quotes.Load() != 0 || !strings.Contains(r.stderr, "REFUSED (no provider call was made)") ||
				!strings.Contains(r.stderr, c.want) {
				t.Errorf("exit %d built %d: %s", r.code, r.built, r.stderr)
			}
			after, _ := os.ReadFile(filepath.Join(dir, recordName))
			if !bytes.Equal(before, after) {
				t.Error("a refused run changed the record")
			}
			if reports, _ := filepath.Glob(filepath.Join(dir, "live-*.json")); len(reports) != 0 {
				t.Errorf("reports %v", reports)
			}
		})
	}
	// Within what the day has left (here USD 0.75 left, a cap of 0.5), the
	// run is made.
	dir := t.TempDir()
	rec := openLine(runA, "eval", now, usd(9.5)) + closeLine(runA, now, usd(9.25), "complete")
	if err := os.WriteFile(filepath.Join(dir, recordName), []byte(rec), 0o644); err != nil {
		t.Fatal(err)
	}
	env := h029Env()
	env["AI_EVAL_LIVE_MAX_USD"] = "0.5"
	if r := runPhotoMain(t, env, dir, &smokeJudge{t: t}); r.code != aievals.ExitOK || r.built != 1 {
		t.Errorf("a cap within the day: exit %d built %d: %s", r.code, r.built, r.stderr)
	}
}

func ptr(s string) *string { return &s }

// H-037 through Main: the count fails, so the judge is never reserved for
// or sent; the run ends with the report (failed, its code) and exit 1, and
// the record is closed with what SAM cost (gate_failed).
func TestThePhotoSmokeReportsAFailedCount(t *testing.T) {
	dir := emptyRecord(t)
	j := &smokeJudge{t: t, failQuote: true}
	r := runPhotoMain(t, h029Env(), dir, j)
	samPrice, _ := spend.ImagePriceOf("sam-3.1")
	if r.code != aievals.ExitGates || r.built != 1 || j.calls.Load() != 0 || j.quotes.Load() != 1 {
		t.Fatalf("exit %d built %d calls %d: %s %s", r.code, r.built, j.calls.Load(), r.stdout, r.stderr)
	}
	lines := recordLines(t, dir)
	if len(lines) != 2 || lines[1]["result"] != "gate_failed" || lines[1]["settled_nano_usd"] != float64(8*samPrice) {
		t.Errorf("the spend record: %v", lines)
	}
	if !strings.Contains(r.stdout, "status failed AI_UNAVAILABLE") || !strings.Contains(r.stderr, "WARNING: the photo smoke failed with AI_UNAVAILABLE: the provider answered 500") {
		t.Errorf("stdout %q stderr %q", r.stdout, r.stderr)
	}
}
