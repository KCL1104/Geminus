package generator

import (
	"context"
	"encoding/json/v2"
	"errors"
	"io"
	"log/slog"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/kcl1104/geminus/internal/ai/idpool"
	"github.com/kcl1104/geminus/internal/ai/provider"
	"github.com/kcl1104/geminus/internal/ai/provider/fake"
	"github.com/kcl1104/geminus/internal/ai/review"
	"github.com/kcl1104/geminus/internal/audit"
	"github.com/kcl1104/geminus/internal/contracttest"
	"github.com/kcl1104/geminus/internal/domainpack"
	"github.com/kcl1104/geminus/internal/domainpack/softwaredelivery"
	"github.com/kcl1104/geminus/internal/httpapi"
	"github.com/kcl1104/geminus/internal/model"
	"github.com/kcl1104/geminus/internal/model/hashing"
	"github.com/kcl1104/geminus/internal/model/validate"
	"github.com/kcl1104/geminus/internal/proposals"
	"github.com/kcl1104/geminus/internal/storage/jobs"
	models "github.com/kcl1104/geminus/internal/storage/models"
	store "github.com/kcl1104/geminus/internal/storage/proposals"
)

func discard() *slog.Logger { return slog.New(slog.NewTextHandler(io.Discard, nil)) }

func client(t testing.TB, b provider.Backend) *provider.Client {
	t.Helper()
	c, err := provider.New(b, provider.DefaultBudget())
	if err != nil {
		t.Fatal(err)
	}
	return c
}

func packs(t testing.TB) *domainpack.Registry {
	t.Helper()
	r, err := domainpack.NewRegistry(softwaredelivery.Pack{})
	if err != nil {
		t.Fatal(err)
	}
	return r
}

func TestNew(t *testing.T) {
	good := Config{Provider: client(t, fake.New()), Packs: packs(t), Actor: "worker-1", Log: discard()}
	if _, err := New(good); err != nil {
		t.Fatal(err)
	}
	for name, edit := range map[string]func(*Config){
		"no provider":  func(c *Config) { c.Provider = nil },
		"no packs":     func(c *Config) { c.Packs = nil },
		"no log":       func(c *Config) { c.Log = nil },
		"no actor":     func(c *Config) { c.Actor = "" },
		"a bad actor":  func(c *Config) { c.Actor = "0worker/a" },
		"a bad budget": func(c *Config) { c.Budget = provider.Budget{MaxPromptBytes: -1} },
	} {
		c := good
		edit(&c)
		if _, err := New(c); err == nil {
			t.Errorf("%s: accepted", name)
		}
	}
}

// CB-40 (c): every provider failure is a typed job failure with a fixed
// message, retried exactly when the provider may recover, and the API shows
// it with a contract error code; nothing of the provider's text is kept.
func TestProviderFailures(t *testing.T) {
	codes := []provider.Code{provider.CodeNotConfigured, provider.CodePolicyNotApproved, provider.CodeInvalidRequest,
		provider.CodeContextTooLarge, provider.CodeConcurrencyLimited, provider.CodeTimeout, provider.CodeRateLimited,
		provider.CodeUnavailable, provider.CodeCredentialsExhausted, provider.CodeRejected, provider.CodeRefused, provider.CodeTruncated,
		provider.CodeQuotaExceeded, provider.CodeSpendUnavailable, // T-053: refused before any call
		provider.CodeImageUnsupported} // T-056: an image to a backend that sends none, refused before any call
	retryable := map[provider.Code]bool{provider.CodeConcurrencyLimited: true, provider.CodeTimeout: true, provider.CodeRateLimited: true,
		provider.CodeUnavailable: true, provider.CodeSpendUnavailable: true}
	for _, c := range codes {
		err := providerFailure(provider.NewError(c, 429, "org-a"))
		var f *jobs.Failure
		if !errors.As(err, &f) {
			t.Fatalf("%s: %v", c, err)
		}
		if f.Code != "AI_"+strings.ToUpper(string(c)) || f.Retryable != retryable[c] || f.Message == "" || f.Message != providerMessages[c] {
			t.Errorf("%s: %+v", c, f)
		}
		if strings.Contains(f.Message, "org-a") || strings.Contains(f.Message, "429") {
			t.Errorf("%s: the message names the credential or status: %q", c, f.Message)
		}
		failure, _ := json.Marshal(f)
		p, perr := proposals.Public(store.Proposal{ID: "aip_01M4YDQK00ADN261CME3WV5HZ6", Status: store.Failed, Mode: "edit",
			Failure: failure, RequestID: "req_01M4YDQK00V6H8S0ST4F7510KR"}, "")
		if perr != nil {
			t.Fatalf("%s: %v", c, perr)
		}
		if _, ok := httpapi.Status[httpapi.Code(p.Error.Error.Code)]; !ok || p.Error.Error.Details[0].Code != f.Code {
			t.Errorf("%s: shown as %+v", c, p.Error.Error)
		}
	}
	if len(providerMessages) != len(codes) {
		t.Errorf("%d messages for %d codes", len(providerMessages), len(codes))
	}
	// An error that is not the client's is the worker's to classify.
	plain := errors.New("plain")
	if providerFailure(plain) != plain {
		t.Error("a plain error became a failure")
	}
}

// The row of each outcome holds what its state may (0012 checks it too)
// and the provider metadata; an invalid one holds no provider text.
func TestGenerated(t *testing.T) {
	meta := provider.Metadata{Provider: "fake", Model: "fake", Role: provider.RoleModeler, InputHash: "sha256:" + strings.Repeat("1", 64)}
	conf := 0.9
	ready := review.Outcome{Status: review.Ready, Summary: "s", Assumptions: []string{"a"}, ExpectedEffects: []string{"e"}, Confidence: &conf,
		Changes: []byte(`[{"op":"replace","path":"/name","value":"x"}]`), PatchHash: "sha256:" + strings.Repeat("2", 64),
		ServerRisk: review.Low, ProviderRisk: review.Medium, Risk: review.Medium, Validation: &review.Validation{Valid: true, Issues: []model.ValidationIssue{}}}
	manifest := []model.DatasetBinding{{LogicalRef: "dataset:a", DatasetVersionID: "dsv_01M4YDQK00ADN261CME3WV5HZ6"}}
	g, err := generated(ready, meta, manifest)
	if err != nil {
		t.Fatal(err)
	}
	if g.Status != store.Ready || *g.Summary != "s" || string(g.Patch) != string(ready.Changes) || *g.Risk != "medium" || *g.ServerRisk != "low" ||
		*g.ProviderRisk != "medium" || *g.Confidence != 0.9 || len(g.DatasetBindings) != 1 || g.Questions != nil || string(g.Validation) != `{"valid":true,"issues":[]}` {
		t.Errorf("ready: %+v", g)
	}
	var stored provider.Metadata
	if err := json.Unmarshal(g.ProviderMetadata, &stored); err != nil || stored.InputHash != meta.InputHash {
		t.Errorf("metadata %s %v", g.ProviderMetadata, err)
	}
	g, err = generated(review.Outcome{Status: review.NeedsInput, Summary: "which?", Assumptions: []string{}, Questions: []string{"Which?"}}, meta, manifest)
	if err != nil || g.Status != store.NeedsInput || *g.Summary != "which?" || !slices.Equal(g.Questions, []string{"Which?"}) || g.Patch != nil ||
		g.Risk != nil || g.Validation != nil || g.DatasetBindings != nil {
		t.Errorf("needs_input: %+v %v", g, err)
	}
	invalid := review.InvalidOutput([]model.ValidationIssue{model.NewIssue(review.OutputInvalid, "/approved", "m", model.SeverityError)})
	g, err = generated(invalid, meta, manifest)
	if err != nil || g.Status != store.Invalid || g.Summary != nil || g.Patch != nil || g.Assumptions != nil || g.Risk != nil ||
		g.DatasetBindings != nil || !strings.Contains(string(g.Validation), `"valid":false`) {
		t.Errorf("invalid: %+v %v", g, err)
	}
	if _, err := generated(review.Outcome{Status: "approved"}, meta, nil); err == nil {
		t.Error("an approved outcome was written")
	}
}

// An audit event names the call, never its content (SECURITY §9).
func TestAuditMeta(t *testing.T) {
	m := auditMeta(provider.Metadata{Provider: "anthropic", Model: "m-1", Credential: "org-b", FailedOver: []string{"org-a"},
		Role: provider.RoleScenario, TemplateVersion: "scenario-0.2.0", OutputSchema: "proposal-output.schema.json", Locale: "zh-Hant",
		InputHash: "sha256:1", OutputHash: "sha256:2", Usage: provider.Usage{InputTokens: 10, OutputTokens: 5}, LatencyMillis: 7,
		ProviderRequestID: "req_011CUcvD2y5nA6z5tYt7aCb1"})
	// T-035 (D-091 (d)): the provider's own request ID correlates the call
	// with the provider's records.
	want := []string{"credential_label", "failed_over_from", "input_hash", "input_tokens", "latency_ms", "model", "output_hash", "output_locale",
		"output_tokens", "prompt_template_version", "provider", "provider_request_id", "role", "schema"}
	var keys []string
	for k := range m {
		keys = append(keys, k)
	}
	if slices.Sort(keys); !slices.Equal(keys, want) {
		t.Errorf("keys %v, want %v", keys, want)
	}
	if m["credential_label"] != "org-b" {
		t.Errorf("%v", m)
	}
	// What the provider chooses (its model name, its request ID) is left
	// out when it is no label the catalogue accepts, so an odd value never
	// fails the write the event records (and never repeats a paid call).
	odd := auditMeta(provider.Metadata{Provider: "anthropic", Model: "model <b>bold</b>", ProviderRequestID: "sk-ant-api03-SENTINELsentinel0123456789",
		Role: provider.RoleModeler, TemplateVersion: "modeler-1", OutputSchema: "proposal-output.schema.json", Locale: "en",
		InputHash: "sha256:" + strings.Repeat("a", 64), LatencyMillis: 3})
	if _, ok := odd["model"]; ok {
		t.Errorf("an odd model name is kept: %v", odd)
	}
	if _, ok := odd["provider_request_id"]; ok {
		t.Errorf("an odd request ID is kept: %v", odd)
	}
	odd["attempt"], odd["code"], odd["retryable"] = 1, "AI_UNAVAILABLE", true
	if err := audit.Check(audit.Event{ActorType: audit.Worker, ActorID: "worker-1", Action: "proposal.attempt_failed", TargetType: "ai_proposal",
		TargetID: "aip_01M4YDQK00ADN261CME3WV5HZ6", RequestID: "req_01M4YDQK00ADN261CME3WV5HZ6", Metadata: odd}); err != nil {
		t.Errorf("%v", err)
	}
}

// The handler never waits on a provider inside a transaction: a provider
// call that blocks until the attempt's context ends returns the context's
// error, and the attempt records nothing of its own (the worker decides).
func TestTheProviderCallHonoursTheContext(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err := client(t, fake.New()).GenerateModelProposal(ctx, provider.ProposalContext{OrganizationID: "org_01M4YDQK00Y5H2929M9WWZWTVH", Mode: provider.ModeEdit})
	if provider.CodeOf(err) != provider.CodeCancelled || !errors.Is(err, context.Canceled) {
		t.Fatalf("%v", err)
	}
}

// stored is a generating proposal on the default delivery model as an
// attempt reads it under the lease, without a database.
func stored(t *testing.T, mode, intent string) started {
	t.Helper()
	const org = "org_01M4YDQK00Y5H2929M9WWZWTVH"
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
	return started{
		proposal: store.Proposal{ID: "aip_01M4YDQK00ADN261CME3WV5HZ6", OrganizationID: org, ModelID: decoded.ModelID, BaseVersionID: "mv_01M4YDQK00ADN261CME3WV5HZ6",
			Mode: mode, Status: store.Generating, Intent: intent, OutputLocale: "en", IDPool: poolJSON},
		version: models.Version{ID: "mv_01M4YDQK00ADN261CME3WV5HZ6", OrganizationID: org, ModelID: decoded.ModelID, Version: 1, IR: ir, Hash: hashing.Sum(ir)},
	}
}

func scripted(t *testing.T, intent string, v any) *fake.Backend {
	t.Helper()
	data, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	f := fake.New()
	f.Script(provider.RoleModeler, intent, data)
	return f
}

func reference(changes string) map[string]any {
	var c any
	_ = json.Unmarshal([]byte(changes), &c)
	return map[string]any{"status": "proposed", "summary": "s", "assumptions": []string{}, "risk_level": "low", "confidence": 0.9,
		"changes": c, "expected_effects": []string{"Waiting may fall; a simulation will tell."}, "requires_confirmation": true}
}

// What an attempt does between its two transactions, with no database:
// the context, the provider call and the review, then the dataset binding
// stage (eval MOD-035: an empirical distribution on a dataset the base
// does not bind is invalid, without reading object storage), and a
// provider failure as a typed job failure.
func TestGenerateOutsideTheDatabase(t *testing.T) {
	ctx := context.Background()
	handler := func(b provider.Backend) *Handler {
		h, err := New(Config{Provider: client(t, b), Packs: packs(t), Actor: "worker-1", Log: discard()})
		if err != nil {
			t.Fatal(err)
		}
		return h
	}
	run := func(b provider.Backend, st started) (review.Outcome, *provider.Metadata, error) {
		return outcomeOf(handler(b).generate(ctx, st.proposal.OrganizationID, st, testAttribution))
	}
	raise := `[{"op":"test","path":"/resources/1/id","value":"ci_runner"},{"op":"replace","path":"/resources/1/capacity","value":6}]`
	o, meta, err := run(scripted(t, "Six runners.", reference(raise)), stored(t, "edit", "Six runners."))
	if err != nil || o.Status != review.Ready || o.Risk != review.Low || meta == nil || meta.InputHash == "" {
		t.Fatalf("ready: %+v %v %v", o, meta, err)
	}
	o, meta, err = run(fake.New(), stored(t, "edit", "Improve it."))
	if err != nil || o.Status != review.NeedsInput || meta == nil {
		t.Fatalf("needs_input: %+v %v", o, err)
	}
	extra := reference(raise)
	extra["approved_by"] = "usr_01M4YDQK00ADN261CME3WV5HZ6"
	o, meta, err = run(scripted(t, "Approve it.", extra), stored(t, "edit", "Approve it."))
	if err != nil || o.Status != review.Invalid || o.Validation.Issues[0].Code != review.OutputInvalid || o.Changes != nil || meta == nil {
		t.Fatalf("invalid output: %+v %v", o, err)
	}
	unbound := `[{"op":"test","path":"/nodes/1/id","value":"code_review"},` +
		`{"op":"replace","path":"/nodes/1/service_time/distribution","value":{"type":"empirical","samples_ref":"dataset:secret_review_times"}}]`
	o, _, err = run(scripted(t, "Use the review samples.", reference(unbound)), stored(t, "edit", "Use the review samples."))
	if err != nil || o.Status != review.Invalid || o.Changes != nil ||
		!slices.ContainsFunc(o.Validation.Issues, func(is model.ValidationIssue) bool {
			return is.Code == validate.DatasetBindingInvalid && is.Path == "/nodes/1/service_time/distribution/samples_ref"
		}) {
		t.Fatalf("unbound dataset: %+v %v", o.Validation, err)
	}
	f := fake.New()
	f.Fail(provider.RoleModeler, "Busy.", provider.NewError(provider.CodeRateLimited, 429, "org-a"))
	_, meta, err = run(f, stored(t, "edit", "Busy."))
	var jf *jobs.Failure
	if !errors.As(err, &jf) || jf.Code != "AI_RATE_LIMITED" || !jf.Retryable || meta == nil {
		t.Fatalf("rate limited: %v %v", err, meta)
	}
	_, _, err = run(provider.Disabled{Code: provider.CodePolicyNotApproved}, stored(t, "edit", "Anything."))
	if !errors.As(err, &jf) || jf.Code != "AI_POLICY_NOT_APPROVED" || jf.Retryable {
		t.Fatalf("disabled: %v", err)
	}
	// A stored request that cannot be built into a context is not retried.
	bad := stored(t, "edit", "Six runners.")
	bad.proposal.IDPool = []byte(`{"nodes":["x"]}`)
	if _, _, err = run(fake.New(), bad); !errors.As(err, &jf) || jf.Code != InputInvalid || jf.Retryable {
		t.Fatalf("bad pool: %v", err)
	}
	// A cancelled attempt is the context's error, never a failure of its own.
	cctx, cancel := context.WithCancel(ctx)
	cancel()
	st := stored(t, "edit", "Six runners.")
	jf = nil
	if _, _, err = outcomeOf(handler(fake.New()).generate(cctx, st.proposal.OrganizationID, st, testAttribution)); !errors.Is(err, context.Canceled) || errors.As(err, &jf) {
		t.Fatalf("cancelled: %v", err)
	}
}
