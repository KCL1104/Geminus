package analyzer

// White-box parity tests of analyze (H-059, slice E2 of
// docs/design/H-056_INVESTIGATIONS_ON_EINO.md §6.4): every path of the
// analyst's orchestration, from what the attempt read under the lease to
// the analysis to store or the job's failure, without a database. What each
// path returns (the failure's code, message and retry decision, or the raw
// error), the call's metadata, the number of issues, the checked analysis,
// what the provider was sent (its role, template, locale, input hash and the
// hashes of its system and user texts) and every log line are pinned in
// testdata/analyze-goldens.json, written once from the orchestration before
// it moved onto the Eino graph (GEMINUS_WRITE_ANALYZER_GOLDENS=1) and never
// rewritten by the slice that moved it. H-086 (R) changed one path on
// purpose (an ungrounded answer is now asked again once:
// the-answer-is-ungrounded) and added the re-ask's (the cases named
// reask-...); every other case's golden is byte for byte as before
// (checks/H-086_R_VALIDATION.md). The database side (the job's completion,
// the audit events, a failed analysis) is internal/api's analyses_test.go
// (make integration-test).

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json/jsontext"
	"encoding/json/v2"
	"errors"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/kcl1104/geminus/internal/ai/provider"
	"github.com/kcl1104/geminus/internal/ai/provider/fake"
	"github.com/kcl1104/geminus/internal/contracttest"
	"github.com/kcl1104/geminus/internal/domainpack"
	"github.com/kcl1104/geminus/internal/domainpack/softwaredelivery"
	"github.com/kcl1104/geminus/internal/model"
	"github.com/kcl1104/geminus/internal/simulation"
	store "github.com/kcl1104/geminus/internal/storage/analyses"
	"github.com/kcl1104/geminus/internal/storage/jobs"
	models "github.com/kcl1104/geminus/internal/storage/models"
	"github.com/kcl1104/geminus/internal/storage/runs"
)

const (
	tOrg       = "org_01M4YDQK00TA00000000000000"
	tModel     = "mdl_01M4YDQK00R942EDGA45T538HS" // testdata/golden-models/deterministic-queue.json's model_id
	tVersion   = "mv_01M4YDQK00FRYWV7GS7X1QCXZR"
	tOther     = "mv_01M4YDQK00FRYWV7GS7X1QCXZS"
	tBase      = "sim_01M4YDQK00FGZZWH0PP15XPNFA" // testdata/contracts/valid-result.json's run
	tScen      = "sim_01M4YDQK00FGZZWH0PP15XPNFB" // valid-result-scenario.json's run
	tAnalysis  = "ana_01M4YDQK00FGZZWH0PP15XPNFC"
	tRequest   = "req_01M4YDQK00FGZZWH0PP15XPNFD"
	tQuestion  = "Why is the queue shorter?"
	tEngine    = "des-go-0.1.0-PROPOSED-fixture"
	zeroHash   = "sha256:0000000000000000000000000000000000000000000000000000000000000000"
	twoServers = `[{"op":"test","path":"/resources/0/id","value":"server"},{"op":"replace","path":"/resources/0/capacity","value":2}]`
	goldenFile = "testdata/analyze-goldens.json"
)

var tAttr = provider.Attribution{TargetType: "ai_analysis", TargetID: tAnalysis, RequestID: tRequest}

func readRepo(t *testing.T, rel string) []byte {
	t.Helper()
	return contracttest.ReadFile(t, filepath.Join(contracttest.Root(t), filepath.FromSlash(rel)))
}

// fixture is what an attempt reads for the analysis of the two-server run
// (testdata/contracts/valid-result-scenario.json) beside its baseline
// (valid-result.json) on the deterministic queue: the pair of
// internal/results/compare's golden vector, which the server compares.
func fixture(t *testing.T) started {
	t.Helper()
	cfg := simulation.RunConfig{Horizon: simulation.Duration{Value: 1010, Unit: model.Minute}, Warmup: simulation.Duration{Value: 0, Unit: model.Minute},
		Replications: 1, Seed: 7, TraceLevel: simulation.TraceNone}
	run := func(id, patch string) *runs.Run {
		return &runs.Run{ID: id, OrganizationID: tOrg, ModelID: tModel, ModelVersionID: tVersion, Name: id, ScenarioPatch: []byte(patch), Config: cfg,
			EngineVersion: tEngine, ModelHash: zeroHash, EffectiveModelHash: zeroHash, ConfigHash: zeroHash, DatasetManifestHash: zeroHash,
			Status: runs.Completed, RequestID: tRequest}
	}
	base, q := tBase, tQuestion
	return started{
		analysis: store.Analysis{ID: tAnalysis, OrganizationID: tOrg, RunID: tScen, BaselineRunID: &base, Status: store.Generating, Question: &q,
			OutputLocale: "zh-Hant", RequestID: tRequest},
		scenario: run(tScen, twoServers),
		baseline: run(tBase, "[]"),
		results: map[string][]byte{
			tScen: readRepo(t, "testdata/contracts/valid-result-scenario.json"),
			tBase: readRepo(t, "testdata/contracts/valid-result.json"),
		},
		version: models.Version{ID: tVersion, OrganizationID: tOrg, ModelID: tModel, Version: 1, IR: readRepo(t, "testdata/golden-models/deterministic-queue.json")},
	}
}

// alone is fixture without a baseline, in English, without a question.
func alone(t *testing.T) started {
	st := fixture(t)
	st.analysis.BaselineRunID, st.analysis.Question, st.analysis.OutputLocale = nil, nil, "en"
	st.baseline = nil
	delete(st.results, tBase)
	return st
}

// noPacks serves no domain.
type noPacks struct{}

func (noPacks) Lookup(string) (domainpack.DomainPack, error) { return nil, errors.New("no pack") }

// billed is the fake as a billable backend of a priced model, so a metered
// client reserves before the call and settles after it.
type billed struct{ *fake.Backend }

func (b billed) Quote(context.Context, provider.Request) (provider.Quote, error) {
	return provider.Quote{Model: "muse-spark-1.3", MaxInputTokens: 20_000}, nil
}

func (b billed) Complete(ctx context.Context, req provider.Request) (provider.Response, error) {
	r, err := b.Backend.Complete(ctx, req)
	r.Model, r.Usage = "muse-spark-1.3", provider.Usage{InputTokens: 700, OutputTokens: 90, CacheReadInputTokens: 10}
	return r, err
}

// ledger records a memory meter's charges and settlements.
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

// cancelling cancels the attempt's context while the provider is being
// asked, as a worker that stops mid-call does.
type cancelling struct {
	*fake.Backend
	cancel context.CancelFunc
}

func (c cancelling) Complete(ctx context.Context, req provider.Request) (provider.Response, error) {
	c.cancel()
	<-ctx.Done()
	return c.Backend.Complete(ctx, req)
}

// outcome is what one path of analyze did.
type outcome struct {
	// Result: "analysis", "failure" (a *jobs.Failure) or "error" (any
	// other error, which the worker records as INTERNAL or not at all).
	Result    string         `json:"result"`
	Code      string         `json:"code,omitzero"`
	Message   string         `json:"message,omitzero"`
	Retryable bool           `json:"retryable,omitzero"`
	Error     string         `json:"error,omitzero"`
	Canceled  bool           `json:"canceled,omitzero"` // errors.Is(err, context.Canceled)
	Metadata  jsontext.Value `json:"metadata,omitzero"` // latency zeroed, a reservation ID as "set"
	Issues    int            `json:"issues"`
	Analysis  jsontext.Value `json:"analysis,omitzero"`
	// Recheck is the re-ask's record (H-086 (R)), as metadata is shown.
	Recheck  jsontext.Value   `json:"recheck,omitzero"`
	Requests []request        `json:"requests"`
	Charges  []charge         `json:"charges,omitzero"`
	Settled  []settlement     `json:"settled,omitzero"`
	Logs     []map[string]any `json:"logs"`
}

type request struct {
	Role            string `json:"role"`
	TemplateVersion string `json:"prompt_template_version"`
	Locale          string `json:"locale"`
	InputHash       string `json:"input_hash"`
	Schema          string `json:"schema"`
	System          string `json:"system_sha256"`
	User            string `json:"user_sha256"`
	MaxOutputTokens int64  `json:"max_output_tokens"`
	Model           string `json:"model,omitzero"`
	// Revision: the request is a re-ask (H-086 (R)): its follow-up's hash.
	Revision string `json:"revision_sha256,omitzero"`
}

type charge struct {
	Organization string `json:"organization_id"`
	Role         string `json:"role"`
	Model        string `json:"model"`
	Target       string `json:"target"`
	NanoUSD      int64  `json:"nano_usd"`
}

type settlement struct {
	NanoUSD int64  `json:"nano_usd"`
	Outcome string `json:"outcome"`
}

func digest(s string) string {
	sum := sha256.Sum256([]byte(s))
	return hex.EncodeToString(sum[:])
}

// setup is a handler over the fake f behind client, logging JSON lines to
// buf with neither time nor latency (which vary).
func setup(t *testing.T, client *provider.Client, packs PackLookup, budget provider.Budget, buf *bytes.Buffer) *Handler {
	t.Helper()
	log := slog.New(slog.NewJSONHandler(buf, &slog.HandlerOptions{ReplaceAttr: func(groups []string, a slog.Attr) slog.Attr {
		switch a.Key {
		case slog.TimeKey:
			return slog.Attr{}
		case "latency_ms":
			return slog.Int64("latency_ms", 0)
		}
		return a
	}}))
	if packs == nil {
		reg, err := domainpack.NewRegistry(softwaredelivery.Pack{})
		if err != nil {
			t.Fatal(err)
		}
		packs = reg
	}
	h, err := New(Config{Provider: client, Budget: budget, Packs: packs, Actor: "test-worker", Log: log})
	if err != nil {
		t.Fatal(err)
	}
	return h
}

// observe runs analyze once and records what it did.
func observe(t *testing.T, ctx context.Context, h *Handler, f *fake.Backend, l *ledger, buf *bytes.Buffer, st started) outcome {
	t.Helper()
	a, err := h.analyze(ctx, tOrg, st, tAttr)
	out, meta, issues := a.out, a.meta, a.issues
	o := outcome{Result: "analysis", Issues: issues, Requests: []request{}, Logs: []map[string]any{}}
	var fl *jobs.Failure
	switch {
	case errors.As(err, &fl):
		o.Result, o.Code, o.Message, o.Retryable = "failure", fl.Code, fl.Message, fl.Retryable
	case err != nil:
		o.Result, o.Error = "error", err.Error()
	}
	o.Canceled = errors.Is(err, context.Canceled)
	if meta != nil {
		m := *meta
		m.LatencyMillis = 0
		if m.SpendReservation != "" {
			m.SpendReservation = "set"
		}
		b, merr := json.Marshal(m, json.Deterministic(true))
		if merr != nil {
			t.Fatal(merr)
		}
		o.Metadata = b
	}
	if err == nil {
		b, merr := json.Marshal(out, json.Deterministic(true))
		if merr != nil {
			t.Fatal(merr)
		}
		o.Analysis = b
	}
	if rc := a.recheck; rc != nil {
		c := *rc
		if c.Call != nil {
			m := *c.Call
			m.LatencyMillis = 0
			if m.SpendReservation != "" {
				m.SpendReservation = "set"
			}
			c.Call = &m
		}
		b, merr := json.Marshal(c, json.Deterministic(true))
		if merr != nil {
			t.Fatal(merr)
		}
		o.Recheck = b
	}
	for _, r := range f.Requests() {
		req := request{Role: string(r.Prompt.Role), TemplateVersion: r.Prompt.TemplateVersion, Locale: string(r.Prompt.Locale),
			InputHash: r.Prompt.InputHash, Schema: r.OutputSchema, System: digest(r.Prompt.System), User: digest(r.Prompt.User()),
			MaxOutputTokens: r.MaxOutputTokens, Model: r.Model}
		if rv := r.Prompt.Revision; rv != nil {
			req.Revision = digest(rv.User())
		}
		o.Requests = append(o.Requests, req)
	}
	if l != nil {
		l.mu.Lock()
		for _, c := range l.charges {
			o.Charges = append(o.Charges, charge{Organization: c.OrganizationID, Role: string(c.Role), Model: c.Model,
				Target: c.Attribution.TargetType + ":" + c.Attribution.TargetID + ":" + c.Attribution.RequestID, NanoUSD: c.NanoUSD})
		}
		for _, s := range l.settled {
			o.Settled = append(o.Settled, settlement{NanoUSD: s.NanoUSD, Outcome: string(s.Outcome)})
		}
		l.mu.Unlock()
	}
	for line := range strings.SplitSeq(strings.TrimSpace(buf.String()), "\n") {
		if line == "" {
			continue
		}
		var m map[string]any
		if err := json.Unmarshal([]byte(line), &m); err != nil {
			t.Fatalf("a log line that is not JSON: %q", line)
		}
		o.Logs = append(o.Logs, m)
	}
	return o
}

// invented is an answer that states a delta the comparison does not hold.
func invented(t *testing.T) []byte {
	t.Helper()
	b, err := json.Marshal(provider.AnalysisOutput{
		Baseline: &provider.AnalysisSummary{Summary: "Baseline."}, Scenario: provider.AnalysisSummary{Summary: "Throughput rose by 4242.5."},
		Delta: []provider.AnalysisDelta{}, Bottleneck: []provider.Bottleneck{}, Assumptions: []string{}, Uncertainty: []string{"Only one replication was run."},
		SuggestedNextExperiments: []provider.Experiment{}})
	if err != nil {
		t.Fatal(err)
	}
	return b
}

// fixedAnswer is invented's answer as the re-ask fixes it (H-086 (R)): the
// figure nobody supplied left out.
func fixedAnswer(t *testing.T) []byte {
	t.Helper()
	b, err := json.Marshal(provider.AnalysisOutput{
		Baseline: &provider.AnalysisSummary{Summary: "Baseline."}, Scenario: provider.AnalysisSummary{Summary: "Throughput rose with the second server."},
		Delta: []provider.AnalysisDelta{}, Bottleneck: []provider.Bottleneck{}, Assumptions: []string{}, Uncertainty: []string{"Only one replication was run."},
		SuggestedNextExperiments: []provider.Experiment{}})
	if err != nil {
		t.Fatal(err)
	}
	return b
}

// inventedAgain is a second answer the check refuses for another reason: a
// promised outcome and an experiment named by a figure nobody supplied.
func inventedAgain(t *testing.T) []byte {
	t.Helper()
	b, err := json.Marshal(provider.AnalysisOutput{
		Baseline: &provider.AnalysisSummary{Summary: "Baseline."}, Scenario: provider.AnalysisSummary{Summary: "A second server is guaranteed to help."},
		Delta: []provider.AnalysisDelta{}, Bottleneck: []provider.Bottleneck{}, Assumptions: []string{}, Uncertainty: []string{"Only one replication was run."},
		SuggestedNextExperiments: []provider.Experiment{{Name: "Add 4242 servers", Hypothesis: "More servers may help.", RequiresConfirmation: true}}})
	if err != nil {
		t.Fatal(err)
	}
	return b
}

// The cases: each is a fresh fake, client and handler, and one analyze.
func cases(t *testing.T) map[string]func(t *testing.T) outcome {
	plain := func(t *testing.T, st started, script func(*fake.Backend), packs PackLookup, budget provider.Budget) outcome {
		f := fake.New()
		if script != nil {
			script(f)
		}
		client, err := provider.New(f, provider.DefaultBudget())
		if err != nil {
			t.Fatal(err)
		}
		var buf bytes.Buffer
		h := setup(t, client, packs, budget, &buf)
		return observe(t, context.Background(), h, f, nil, &buf, st)
	}
	none := provider.Budget{}
	return map[string]func(t *testing.T) outcome{
		"explained-beside-a-baseline": func(t *testing.T) outcome { return plain(t, fixture(t), nil, nil, none) },
		"explained-alone":             func(t *testing.T) outcome { return plain(t, alone(t), nil, nil, none) },
		"explained-metered": func(t *testing.T) outcome {
			f := fake.New()
			l := &ledger{m: provider.NewMemoryMeter(1_000_000_000_000)}
			client, err := provider.NewMetered(billed{f}, provider.DefaultBudget(), l)
			if err != nil {
				t.Fatal(err)
			}
			var buf bytes.Buffer
			h := setup(t, client, nil, none, &buf)
			return observe(t, context.Background(), h, f, l, &buf, fixture(t))
		},
		"the-version-does-not-decode": func(t *testing.T) outcome {
			st := fixture(t)
			st.version.IR = []byte(`{"model_id":`)
			return plain(t, st, nil, nil, none)
		},
		"the-domain-is-not-served": func(t *testing.T) outcome { return plain(t, fixture(t), nil, noPacks{}, none) },
		"the-scenario-result-does-not-decode": func(t *testing.T) outcome {
			st := fixture(t)
			st.results[tScen] = []byte(`{"run_id":"` + tScen + `","unknown":1}`)
			return plain(t, st, nil, nil, none)
		},
		"the-patch-does-not-decode": func(t *testing.T) outcome {
			st := fixture(t)
			st.scenario.ScenarioPatch = []byte(`{"op":`)
			return plain(t, st, nil, nil, none)
		},
		"the-baseline-result-does-not-decode": func(t *testing.T) outcome {
			st := fixture(t)
			st.results[tBase] = []byte(`[]`)
			return plain(t, st, nil, nil, none)
		},
		"the-runs-cannot-be-compared": func(t *testing.T) outcome {
			// The baseline's stored result is another run's: the comparison
			// refuses it as inconsistent (a server fault).
			st := fixture(t)
			st.results[tBase] = st.results[tScen]
			return plain(t, st, nil, nil, none)
		},
		"the-runs-are-not-comparable": func(t *testing.T) outcome {
			st := fixture(t)
			st.baseline.ModelVersionID = tOther
			return plain(t, st, nil, nil, none)
		},
		"the-context-is-too-large": func(t *testing.T) outcome {
			b := provider.DefaultBudget()
			b.MaxPromptBytes = 2000
			return plain(t, fixture(t), nil, nil, b)
		},
		"the-context-cannot-be-built": func(t *testing.T) outcome {
			st := fixture(t)
			st.analysis.OutputLocale = "fr"
			return plain(t, st, nil, nil, none)
		},
		"the-provider-refuses": func(t *testing.T) outcome {
			return plain(t, fixture(t), func(f *fake.Backend) {
				f.Fail(provider.RoleAnalyst, tQuestion, provider.NewError(provider.CodeRefused, 0, ""))
			}, nil, none)
		},
		"the-provider-is-unavailable": func(t *testing.T) outcome {
			return plain(t, fixture(t), func(f *fake.Backend) {
				f.Fail(provider.RoleAnalyst, tQuestion, provider.NewError(provider.CodeUnavailable, 503, ""))
			}, nil, none)
		},
		"the-provider-fails-with-another-error": func(t *testing.T) outcome {
			return plain(t, fixture(t), func(f *fake.Backend) {
				f.Fail(provider.RoleAnalyst, tQuestion, errors.New("a backend error that is not the client's"))
			}, nil, none)
		},
		"the-answer-is-not-an-analysis": func(t *testing.T) outcome {
			return plain(t, fixture(t), func(f *fake.Backend) { f.Script(provider.RoleAnalyst, tQuestion, []byte(`{"scenario":{}}`)) }, nil, none)
		},
		"the-answer-is-ungrounded": func(t *testing.T) outcome {
			// H-086 (R): asked again once; the fake repeats itself.
			return plain(t, fixture(t), func(f *fake.Backend) { f.Script(provider.RoleAnalyst, tQuestion, invented(t)) }, nil, none)
		},
		"reask-grounded": func(t *testing.T) outcome {
			return plain(t, fixture(t), func(f *fake.Backend) {
				f.Script(provider.RoleAnalyst, tQuestion, invented(t))
				f.ScriptRevision(provider.RoleAnalyst, tQuestion, fixedAnswer(t))
			}, nil, none)
		},
		"reask-grounded-metered": func(t *testing.T) outcome {
			f := fake.New()
			f.Script(provider.RoleAnalyst, tQuestion, invented(t))
			f.ScriptRevision(provider.RoleAnalyst, tQuestion, fixedAnswer(t))
			l := &ledger{m: provider.NewMemoryMeter(1_000_000_000_000)}
			client, err := provider.NewMetered(billed{f}, provider.DefaultBudget(), l)
			if err != nil {
				t.Fatal(err)
			}
			var buf bytes.Buffer
			h := setup(t, client, nil, none, &buf)
			return observe(t, context.Background(), h, f, l, &buf, fixture(t))
		},
		"reask-ungrounded-again": func(t *testing.T) outcome {
			return plain(t, fixture(t), func(f *fake.Backend) {
				f.Script(provider.RoleAnalyst, tQuestion, invented(t))
				f.ScriptRevision(provider.RoleAnalyst, tQuestion, inventedAgain(t))
			}, nil, none)
		},
		"reask-call-fails": func(t *testing.T) outcome {
			return plain(t, fixture(t), func(f *fake.Backend) {
				f.Script(provider.RoleAnalyst, tQuestion, invented(t))
				f.FailRevision(provider.RoleAnalyst, tQuestion, provider.NewError(provider.CodeUnavailable, 503, ""))
			}, nil, none)
		},
		"cancelled-during-the-call": func(t *testing.T) outcome {
			f := fake.New()
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			client, err := provider.New(cancelling{Backend: f, cancel: cancel}, provider.DefaultBudget())
			if err != nil {
				t.Fatal(err)
			}
			var buf bytes.Buffer
			h := setup(t, client, nil, none, &buf)
			return observe(t, ctx, h, f, nil, &buf, fixture(t))
		},
	}
}

func TestAnalyzeParity(t *testing.T) {
	got := map[string]outcome{}
	for name, run := range cases(t) {
		got[name] = run(t)
	}
	if os.Getenv("GEMINUS_WRITE_ANALYZER_GOLDENS") == "1" {
		b, err := json.Marshal(got, json.Deterministic(true), jsontext.WithIndent("  "))
		if err != nil {
			t.Fatal(err)
		}
		if err := os.MkdirAll(filepath.Dir(goldenFile), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(goldenFile, append(b, '\n'), 0o644); err != nil {
			t.Fatal(err)
		}
		t.Logf("wrote %s (%d cases)", goldenFile, len(got))
		return
	}
	data, err := os.ReadFile(goldenFile)
	if err != nil {
		t.Fatalf("%v (write it from the orchestration before E2 with GEMINUS_WRITE_ANALYZER_GOLDENS=1)", err)
	}
	var want map[string]jsontext.Value
	if err := json.Unmarshal(data, &want); err != nil {
		t.Fatal(err)
	}
	if len(want) != len(got) {
		t.Errorf("%d cases, the golden has %d", len(got), len(want))
	}
	for name, o := range got {
		w, ok := want[name]
		if !ok {
			t.Errorf("%s: not in the golden", name)
			continue
		}
		g, err := json.Marshal(o, json.Deterministic(true))
		if err != nil {
			t.Fatal(err)
		}
		if err := w.Canonicalize(); err != nil {
			t.Fatal(err)
		}
		gv := jsontext.Value(g)
		if err := gv.Canonicalize(); err != nil {
			t.Fatal(err)
		}
		if string(gv) != string(w) {
			t.Errorf("%s:\n got %s\nwant %s", name, gv, w)
		}
	}
}

// Paths whose outcome the goldens pin, checked on their own so a golden
// written wrong cannot hide them: the analysis is the checked one; every
// failure before the call makes no provider request; the call's metadata
// comes back with a provider failure and an ungrounded answer, never with a
// failure before the call.
func TestAnalyzeOutcomes(t *testing.T) {
	all := cases(t)
	for _, c := range []struct {
		name, result, code string
		retryable          bool
		requests           int
		metadata           bool
	}{
		{"explained-beside-a-baseline", "analysis", "", false, 1, true},
		{"explained-alone", "analysis", "", false, 1, true},
		{"explained-metered", "analysis", "", false, 1, true},
		{"the-version-does-not-decode", "failure", InputInvalid, false, 0, false},
		{"the-domain-is-not-served", "failure", InputInvalid, false, 0, false},
		{"the-scenario-result-does-not-decode", "failure", InputInvalid, false, 0, false},
		{"the-patch-does-not-decode", "failure", InputInvalid, false, 0, false},
		{"the-baseline-result-does-not-decode", "failure", InputInvalid, false, 0, false},
		{"the-runs-cannot-be-compared", "failure", InputInvalid, false, 0, false},
		{"the-runs-are-not-comparable", "failure", NotComparable, false, 0, false},
		{"the-context-is-too-large", "failure", "AI_CONTEXT_TOO_LARGE", false, 0, false},
		{"the-context-cannot-be-built", "failure", InputInvalid, false, 0, false},
		{"the-provider-refuses", "failure", "AI_REFUSED", false, 1, true},
		{"the-provider-is-unavailable", "failure", "AI_UNAVAILABLE", true, 1, true},
		{"the-provider-fails-with-another-error", "failure", "AI_UNAVAILABLE", true, 1, true},
		{"the-answer-is-not-an-analysis", "failure", "AI_INVALID_OUTPUT", false, 1, true},
		{"the-answer-is-ungrounded", "failure", Ungrounded, false, 2, true},
		{"reask-grounded", "analysis", "", false, 2, true},
		{"reask-grounded-metered", "analysis", "", false, 2, true},
		{"reask-ungrounded-again", "failure", Ungrounded, false, 2, true},
		{"reask-call-fails", "failure", Ungrounded, false, 2, true},
		{"cancelled-during-the-call", "error", "", false, 0, false},
	} {
		t.Run(c.name, func(t *testing.T) {
			o := all[c.name](t)
			if o.Result != c.result || o.Code != c.code || o.Retryable != c.retryable || len(o.Requests) != c.requests || (o.Metadata != nil) != c.metadata {
				t.Errorf("%+v", o)
			}
			if c.name == "the-answer-is-ungrounded" && (o.Issues < 1 || !strings.HasPrefix(o.Message, "the AI answer is not grounded in the results (")) {
				t.Errorf("ungrounded: %d issues, %q", o.Issues, o.Message)
			}
			if c.name == "cancelled-during-the-call" && !o.Canceled {
				t.Errorf("a cancelled call: %q is not a cancellation", o.Error)
			}
			if c.name == "explained-metered" && (len(o.Charges) != 1 || len(o.Settled) != 1 || o.Charges[0].Target != "ai_analysis:"+tAnalysis+":"+tRequest) {
				t.Errorf("metered: %+v %+v", o.Charges, o.Settled)
			}
			// H-086 (R): the re-ask's own reservation, charged to the same
			// analysis, and its own settlement.
			if c.name == "reask-grounded-metered" && (len(o.Charges) != 2 || len(o.Settled) != 2 || o.Charges[1].Target != o.Charges[0].Target) {
				t.Errorf("the re-ask metered: %+v %+v", o.Charges, o.Settled)
			}
			var rc struct {
				Outcome string
				Code    string
			}
			if o.Recheck != nil {
				if err := json.Unmarshal(o.Recheck, &rc); err != nil {
					t.Fatal(err)
				}
			}
			want := map[string]string{"the-answer-is-ungrounded": "ungrounded", "reask-grounded": "grounded", "reask-grounded-metered": "grounded",
				"reask-ungrounded-again": "ungrounded", "reask-call-fails": "failed"}[c.name]
			if rc.Outcome != want || (want == "failed") != (rc.Code == "AI_UNAVAILABLE") {
				t.Errorf("the re-ask's record %s, want %q", o.Recheck, want)
			}
			if want != "" && (len(o.Logs) != 2 || o.Logs[1]["call_attempt"] != float64(2) || o.Logs[0]["call_attempt"] != nil) {
				t.Errorf("the calls' lines %v", o.Logs)
			}
		})
	}
}

// An attempt already stopping when analyze starts makes no provider call
// and spends nothing: the error is a cancellation, not a job failure (the
// worker records the attempt's end as for any stopping attempt), with no
// metadata.
func TestAnalyzeCancelledBeforeTheCall(t *testing.T) {
	f := fake.New()
	l := &ledger{m: provider.NewMemoryMeter(1_000_000_000_000)}
	client, err := provider.NewMetered(billed{f}, provider.DefaultBudget(), l)
	if err != nil {
		t.Fatal(err)
	}
	var buf bytes.Buffer
	h := setup(t, client, nil, provider.Budget{}, &buf)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	a, err := h.analyze(ctx, tOrg, fixture(t), tAttr)
	meta, issues := a.meta, a.issues
	var fl *jobs.Failure
	if !errors.Is(err, context.Canceled) || errors.As(err, &fl) || meta != nil || issues != 0 {
		t.Errorf("cancelled: %v (failure %v), metadata %v, %d issues", err, fl, meta, issues)
	}
	if n := len(f.Requests()); n != 0 {
		t.Errorf("%d provider requests", n)
	}
	if len(l.charges) != 0 || len(l.settled) != 0 {
		t.Errorf("spend: %+v %+v", l.charges, l.settled)
	}
}
