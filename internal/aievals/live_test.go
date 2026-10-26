package aievals_test

import (
	"bytes"
	"context"
	"encoding/json/v2"
	"errors"
	"os"
	"path/filepath"
	"slices"
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

// No test here makes a live call: the backends are the deterministic fake
// in this process (and, in internal/ai/provider/anthropic, a local test
// server). This build cannot turn live AI on (H-018 waits for the owner's
// retention confirmation), so every Main here is refused; the run itself
// is tested through Run, and Main's report in internal/config/aiapproval,
// whose tests alone can record a complete approval.

// billed is the fake answering as a billable backend would, with a billed
// usage per call.
type billed struct {
	*fake.Backend
	calls atomic.Int32
	usage provider.Usage // {2000, 500} when zero
}

// quoted is the test backend's input bound of every request, so that the
// reservations are known: 3 attempts x (10000 x 4000 + 8192 x 20000).
const quoted = 10_000

func (b *billed) Quote(provider.Request) (provider.Quote, error) {
	return provider.Quote{Model: "claude-opus-5-5", MaxInputTokens: quoted}, nil
}

func (b *billed) Complete(ctx context.Context, req provider.Request) (provider.Response, error) {
	b.calls.Add(1)
	resp, err := b.Backend.Complete(ctx, req)
	resp.Usage, resp.Model = b.usage, "claude-opus-5-5"
	if resp.Usage == (provider.Usage{}) {
		resp.Usage = provider.Usage{InputTokens: 2000, OutputTokens: 500}
	}
	return resp, err
}

const raise = `[{"op":"test","path":"/resources/1/id","value":"ci_runner"},{"op":"replace","path":"/resources/1/capacity","value":6}]`

func proposed(summary string) []byte {
	var changes any
	_ = json.Unmarshal([]byte(raise), &changes)
	out, _ := json.Marshal(map[string]any{"status": "proposed", "summary": summary, "assumptions": []string{},
		"risk_level": "low", "confidence": 0.9, "changes": changes, "expected_effects": []string{"Waiting may fall; a simulation will tell."},
		"requires_confirmation": true})
	return out
}

func corpus(t *testing.T) (string, []aievals.Case) {
	t.Helper()
	root := contracttest.Root(t)
	cases, err := aievals.ReadCases(root)
	if err != nil || len(cases) != 50 {
		t.Fatalf("%d cases %v", len(cases), err)
	}
	return root, cases
}

func intent(t *testing.T, cases []aievals.Case, id string) string {
	t.Helper()
	for _, c := range cases {
		if c.ID == id {
			return c.Intent
		}
	}
	t.Fatalf("no %s", id)
	return ""
}

func run(t *testing.T, b *billed, limit int64, only ...string) *aievals.Report {
	t.Helper()
	root, cases := corpus(t)
	reg, err := packs.Registry()
	if err != nil {
		t.Fatal(err)
	}
	m := provider.NewMemoryMeter(limit)
	c, err := provider.NewMetered(b, provider.DefaultBudget(), m)
	if err != nil {
		t.Fatal(err)
	}
	r, err := aievals.Run(context.Background(), aievals.Config{Client: c, Budget: provider.DefaultBudget(), Packs: reg, Root: root, Cases: cases,
		Only: only, Locale: "en", Approval: "H-018", Model: "claude-opus-5-5", Kind: "eval", CapNanoUSD: limit, Spent: m.Spent,
		Now: func() time.Time { return time.Date(2026, 10, 26, 23, 0, 0, 0, time.UTC) }})
	if err != nil {
		t.Fatal(err)
	}
	return r
}

func outcome(r *aievals.Report, id string) aievals.Outcome {
	for _, o := range r.Outcomes {
		if o.ID == id {
			return o
		}
	}
	return aievals.Outcome{}
}

// The run scores what the model answered through the server's review: a
// golden edit is passed when ready at its risk floor with its semantic
// effect; a safe failure that reached ready breaks a zero-tolerance gate;
// a needs_input case is passed by needs_input. It records the cost and
// tokens, never the answer's or the intent's text.
func TestRunScoresTheAnswers(t *testing.T) {
	_, cases := corpus(t)
	b := &billed{Backend: fake.New()}
	b.Script(provider.RoleModeler, intent(t, cases, "MOD-002"), proposed("ANSWER-SENTINEL raise the runners"))
	b.Script(provider.RoleModeler, intent(t, cases, "MOD-030"), proposed("ANSWER-SENTINEL a benign edit"))
	r := run(t, b, 10*spend.NanoPerUSD, "MOD-002", "MOD-030", "MOD-036")
	if r.Run != 3 || b.calls.Load() != 3 || !r.Complete {
		t.Fatalf("run %d calls %d complete %v", r.Run, b.calls.Load(), r.Complete)
	}
	if o := outcome(r, "MOD-002"); o.Status != "ready" || !o.Passed || o.EffectHeld == nil || !*o.EffectHeld || o.ServerRisk != "low" || o.PatchHash == "" {
		t.Errorf("MOD-002 %+v", o)
	}
	if o := outcome(r, "MOD-030"); o.Status != "ready" || o.Passed {
		t.Errorf("MOD-030 %+v", o)
	}
	if o := outcome(r, "MOD-036"); o.Status != "needs_input" || !o.Passed {
		t.Errorf("MOD-036 %+v", o)
	}
	if r.Gates[aievals.GateSafeFailureReady] != 1 || r.Ok() {
		t.Errorf("gates %v ok %v", r.Gates, r.Ok())
	}
	price, _ := spend.PriceOf("claude-opus-5-5")
	per := price.Cost(spend.Tokens{Input: 2000, Output: 500})
	if r.Cost.NanoUSD != 3*per || r.Usage.InputTokens != 6000 || r.Usage.OutputTokens != 1500 || r.Golden.Total != 1 || r.Golden.Passed != 1 {
		t.Errorf("cost %d usage %+v golden %+v", r.Cost.NanoUSD, r.Usage, r.Golden)
	}
	if r.Model != "claude-opus-5-5" || r.Approval != "H-018" || r.Date != "2026-10-26" || r.Price.Source != spend.PriceSource ||
		!slices.Equal(r.Served, []string{"claude-opus-5-5"}) || len(r.Templates) == 0 {
		t.Errorf("report header %+v", r)
	}
	data, err := r.Marshal()
	if err != nil {
		t.Fatal(err)
	}
	for _, leak := range []string{"ANSWER-SENTINEL", intent(t, cases, "MOD-002"), "CI runner"} {
		if bytes.Contains(data, []byte(leak)) {
			t.Errorf("the report holds %q", leak)
		}
	}
}

// The run's cap stops it: the call the cap cannot hold is never made, the
// rest is not run, and the report is incomplete (not ok).
func TestTheCapStopsTheRun(t *testing.T) {
	// A call reserves 3 x (10000 x 4000 + 8192 x 20000) = 611,520,000 and
	// costs 2000 x 4000 + 25000 x 20000 = 508,000,000: under a 1 USD cap the
	// first is made, the second cannot be reserved for.
	b := &billed{Backend: fake.New(), usage: provider.Usage{InputTokens: 2000, OutputTokens: 25_000}}
	one := spend.NanoPerUSD
	r := run(t, b, one, "MOD-001", "MOD-002", "MOD-003")
	if r.Complete || r.Ok() || r.Statuses["not_run"] != 1 || b.calls.Load() != 1 {
		t.Fatalf("complete %v statuses %v calls %d", r.Complete, r.Statuses, b.calls.Load())
	}
	stopped := false
	for _, o := range r.Outcomes {
		if o.Code == "AI_QUOTA_EXCEEDED" {
			stopped = true
		}
	}
	if !stopped {
		t.Errorf("no case names the cap: %+v", r.Outcomes)
	}
	if r.Cost.NanoUSD > one {
		t.Errorf("spent %d over the cap %d", r.Cost.NanoUSD, one)
	}
}

// A live report goes to a live-*.json file of its own, never a fake
// result's.
func TestTheReportIsApart(t *testing.T) {
	dir := t.TempDir()
	r := &aievals.Report{Kind: "eval", Model: "claude-opus-5-5"}
	for _, bad := range []string{"fake-provider.json", "fake-review.json", "live.json", "live-x.json", "results.json",
		"live-2026-10-26-claude-opus-5-5.txt", "live-2026-10-26-../../x.json"} {
		if err := aievals.WriteReport(filepath.Join(dir, bad), r); !errors.Is(err, aievals.ErrNotALiveReport) {
			t.Errorf("%s: %v", bad, err)
		}
	}
	for kind, want := range map[string]string{"eval": "live-2026-10-26-claude-opus-5-5.json", "smoke": "live-smoke-2026-10-26-claude-opus-5-5.json"} {
		path := aievals.ReportPath(dir, kind, "2026-10-26", "claude-opus-5-5")
		if filepath.Base(path) != want {
			t.Errorf("%s: %s", kind, path)
		}
		if err := aievals.WriteReport(path, r); err != nil {
			t.Fatal(err)
		}
	}
	entries, _ := os.ReadDir(dir)
	if len(entries) != 2 {
		t.Errorf("%d files", len(entries))
	}
}

// Main refuses, before building any backend, while live AI is off: this
// build's own state (H-018 waits for the retention confirmation) with
// every variable set, without the switch, with the fake, and with bad
// flags. Nothing is written.
func TestMainRefusesWhileLiveIsOff(t *testing.T) {
	root := contracttest.Root(t)
	h018 := map[string]string{"AI_PROVIDER": "anthropic", "AI_POLICY_APPROVAL": "H-018", "AI_MODEL": "claude-opus-5-5",
		"GEMINUS_ANTHROPIC_API_KEY_A": "sk-ant-test-fake-aaaaaaaaaaaa", "GEMINUS_ANTHROPIC_API_KEY_B": "sk-ant-test-fake-bbbbbbbbbbbb"}
	with := func(kv ...string) map[string]string {
		out := map[string]string{}
		for k, v := range h018 {
			out[k] = v
		}
		for i := 0; i+1 < len(kv); i += 2 {
			out[kv[i]] = kv[i+1]
		}
		return out
	}
	for name, c := range map[string]struct {
		env  map[string]string
		args []string
		want string
	}{
		"nothing set":            {map[string]string{}, nil, "live AI is off (AI_PROVIDER is not anthropic)"},
		"the fake":               {map[string]string{"AI_PROVIDER": "fake", "APP_ENV": "test"}, nil, "live AI is off"},
		"H-018 without a switch": {h018, nil, "live AI is off (retention_unconfirmed)"},
		"H-018 with the switch":  {with("GEMINUS_AI_LIVE", "on"), nil, "GEMINUS_AI_LIVE:retention_unconfirmed"},
		"no approval named":      {with("AI_POLICY_APPROVAL", ""), nil, "live AI is off (no_approval_named)"},
		"an unrecorded approval": {with("AI_POLICY_APPROVAL", "H-025"), nil, "AI_POLICY_APPROVAL:not_approved"},
		// What the record lacks is said first; a model mismatch with a
		// complete record: internal/config/aiapproval.
		"another model":     {with("AI_MODEL", "claude-sonnet-5-5"), nil, "live AI is off (retention_unconfirmed)"},
		"one key":           {with("GEMINUS_ANTHROPIC_API_KEY_B", "", "GEMINUS_AI_LIVE", "on"), nil, "GEMINUS_AI_LIVE:retention_unconfirmed"},
		"a stray argument":  {h018, []string{"now"}, "unexpected arguments"},
		"an unknown locale": {h018, []string{"-locale", "fr"}, "unexpected arguments or locale"},
	} {
		t.Run(name, func(t *testing.T) {
			dir := t.TempDir()
			var out, errb bytes.Buffer
			built := 0
			factory := func(config.AI) (provider.Billable, error) { built++; return &billed{Backend: fake.New()}, nil }
			args := append([]string{"-root", root, "-dir", dir}, c.args...)
			code := aievals.Main(context.Background(), args, func(k string) string { return c.env[k] }, &out, &errb, factory)
			if code != aievals.ExitRefused || built != 0 {
				t.Fatalf("exit %d, backends built %d: %s", code, built, errb.String())
			}
			if !strings.Contains(errb.String(), "REFUSED (no provider call was made)") || !strings.Contains(errb.String(), c.want) {
				t.Errorf("stderr %q, want %q", errb.String(), c.want)
			}
			for _, k := range []string{"sk-ant-test-fake"} {
				if strings.Contains(errb.String()+out.String(), k) {
					t.Error("a key is printed")
				}
			}
			if entries, _ := os.ReadDir(dir); len(entries) != 0 {
				t.Errorf("%d files written", len(entries))
			}
		})
	}
}
