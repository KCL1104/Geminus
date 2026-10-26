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
	"github.com/kcl1104/geminus/internal/config/aiapproval"
	"github.com/kcl1104/geminus/internal/contracttest"
)

// inProcess answers as the fake does, as a billable backend: the live
// eval's backend in these tests (no network; the HTTP path is tested on a
// local server in internal/ai/provider/anthropic).
type inProcess struct {
	*fake.Backend
	calls atomic.Int32
}

func (b *inProcess) Quote(provider.Request) (provider.Quote, error) {
	return provider.Quote{Model: "claude-opus-5-5", MaxInputTokens: 10_000}, nil
}

func (b *inProcess) Complete(ctx context.Context, req provider.Request) (provider.Response, error) {
	b.calls.Add(1)
	resp, err := b.Backend.Complete(ctx, req)
	resp.Usage, resp.Model, resp.Credential = provider.Usage{InputTokens: 1500, OutputTokens: 300}, "claude-opus-5-5", "org-a"
	resp.InferenceGeo = "global" // as the live API reports it (usage.inference_geo)
	return resp, err
}

// T-053: the live eval runs only when config turns live AI on (the
// complete record, its model, both keys, the switch), meters the run by
// its cap, and writes its report to a live-*.json file of its own with
// the model, date, tokens and cost; it builds the backend only then.
func TestTheLiveEvalRunsOnlyWhenLiveIsOn(t *testing.T) {
	root := contracttest.Root(t)
	env := liveEnv()
	getenv := func(k string) string { return env[k] }
	run := func(args ...string) (int, string, string, int, string) {
		dir := emptyRecord(t) // the report directory, with its spend record (review round 1, H1)
		var out, errb bytes.Buffer
		built := 0
		b := &inProcess{Backend: fake.New()}
		factory := func(ai config.AI) (provider.Billable, error) {
			built++
			if !ai.LiveEnabled() || ai.Model != "claude-opus-5-5" || len(ai.Credentials) != 2 {
				t.Errorf("the backend was asked for %+v", ai)
			}
			return b, nil
		}
		code := aievals.Main(context.Background(), append([]string{"-root", root, "-dir", dir}, args...), getenv, &out, &errb, factory)
		return code, out.String(), errb.String(), built, dir
	}

	// The record before H-026 (no retention confirmation): refused before
	// any backend is built.
	t.Run("before H-026", func(t *testing.T) {
		before := complete(t)
		before.Retention = aiapproval.Retention{}
		aiapproval.RecordForTest(t, before)
		code, _, stderr, built, dir := run("-smoke")
		if code != aievals.ExitRefused || built != 0 || !strings.Contains(stderr, "GEMINUS_AI_LIVE:retention_unconfirmed") {
			t.Fatalf("exit %d built %d: %s", code, built, stderr)
		}
		if entries, _ := os.ReadDir(dir); len(entries) != 1 || entries[0].Name() != recordName {
			t.Fatal("a refused run wrote a file")
		}
		if lines := recordLines(t, dir); len(lines) != 0 {
			t.Fatalf("a refused run wrote the spend record: %v", lines)
		}
	})

	// The build's own record (H-026): the run is made, on the test's
	// backend. Its report names the run's UTC start and ID (review round 1,
	// H1).
	code, stdout, stderr, built, dir := run("-smoke")
	if code != aievals.ExitOK || built != 1 {
		t.Fatalf("smoke: exit %d built %d: %s %s", code, built, stdout, stderr)
	}
	date := time.Now().UTC().Format(time.DateOnly)
	reports, _ := filepath.Glob(filepath.Join(dir, "live-smoke-*-claude-opus-5-5.json"))
	if len(reports) != 1 {
		t.Fatalf("reports %v (%s)", reports, stdout)
	}
	path := reports[0]
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("the report: %v (%s)", err, stdout)
	}
	var r aievals.Report
	if err := json.Unmarshal(data, &r); err != nil {
		t.Fatal(err)
	}
	price, _ := spend.PriceOf("claude-opus-5-5")
	cost := price.Cost(spend.Tokens{Input: 1500, Output: 300})
	if r.Kind != "smoke" || r.Model != "claude-opus-5-5" || r.Approval != "H-018" || r.Date != date || r.Run != 1 ||
		r.Outcomes[0].ID != aievals.SmokeCase || r.Usage.InputTokens != 1500 || r.Cost.NanoUSD != cost ||
		r.Cost.CapNanoUSD != aievals.DefaultSmokeCap || !r.Complete {
		t.Errorf("report %+v", r)
	}
	if entries, _ := os.ReadDir(dir); len(entries) != 2 || !strings.Contains(stdout, path) || !strings.HasPrefix(filepath.Base(path), "live-smoke-"+date+"T") ||
		!strings.Contains(filepath.Base(path), r.RunID) || r.RunID == "" {
		t.Errorf("files %v (the report and the spend record), report %s of run %q, stdout %q", entries, path, r.RunID, stdout)
	}
	for _, leak := range []string{keyA, keyB, "CI runner"} {
		if bytes.Contains(data, []byte(leak)) || strings.Contains(stdout+stderr, leak) {
			t.Errorf("%q is in the report or the output", leak)
		}
	}

	// A run cap above the monthly limit is refused before any backend.
	env["AI_EVAL_LIVE_MAX_USD"], env["AI_MONTHLY_LIMIT_USD"], env["AI_DAILY_LIMIT_USD"] = "20", "10", "10"
	if code, _, stderr, built, _ := run(); code != aievals.ExitRefused || built != 0 || !strings.Contains(stderr, "above the deployment's daily limit") {
		t.Errorf("cap above the month: exit %d built %d: %s", code, built, stderr)
	}
	delete(env, "AI_DAILY_LIMIT_USD")
	env["AI_EVAL_LIVE_MAX_USD"] = "not dollars"
	if code, _, _, built, _ := run(); code != aievals.ExitRefused || built != 0 {
		t.Errorf("a bad cap: exit %d built %d", code, built)
	}
	// An unknown case is refused before any backend.
	delete(env, "AI_EVAL_LIVE_MAX_USD")
	if code, _, _, built, _ := run("-cases", "MOD-999"); code != aievals.ExitRefused || built != 0 {
		t.Errorf("an unknown case: exit %d built %d", code, built)
	}
	// A cap the first call's reservation cannot fit: the run is incomplete
	// (exit 1), the report says so, nothing was asked of the backend.
	env["AI_EVAL_LIVE_MAX_USD"] = "0.01"
	code, stdout, _, built, dir = run("-cases", "MOD-001,MOD-002")
	if code != aievals.ExitGates || built != 1 {
		t.Fatalf("a small cap: exit %d built %d", code, built)
	}
	reports, _ = filepath.Glob(filepath.Join(dir, "live-"+date+"T*-claude-opus-5-5.json"))
	if len(reports) != 1 {
		t.Fatalf("a small cap: reports %v", reports)
	}
	data, _ = os.ReadFile(reports[0])
	r = aievals.Report{}
	if err := json.Unmarshal(data, &r); err != nil || r.Complete || r.Outcomes[0].Code != "AI_QUOTA_EXCEEDED" || r.Cost.NanoUSD != 0 {
		t.Errorf("a small cap: %+v %v (%s)", r, err, stdout)
	}
}
