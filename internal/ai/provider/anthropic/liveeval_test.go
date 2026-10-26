package anthropic_test

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/kcl1104/geminus/internal/ai/provider"
	"github.com/kcl1104/geminus/internal/ai/spend"
	"github.com/kcl1104/geminus/internal/aievals"
	"github.com/kcl1104/geminus/internal/contracttest"
	"github.com/kcl1104/geminus/internal/packs"
)

// T-053: the live eval (make ai-eval-live) over the live adapter and a
// local test server of the Messages API: every case is one request with
// the injected keys, the answers go through the client and the server's
// review, the report records the model, date, tokens and cost from the
// answers' usage, and keeps no answer, prompt, thinking or key. It is
// written to a live-*.json file apart from the fake results. No request
// leaves the machine.
func TestTheLiveEvalOverHTTP(t *testing.T) {
	s := newServer(t, map[string][]reply{keyA: {apiError(402, "billing_error")}, keyB: {ok(answerJ)}})
	c, _, m := meteredClient(t, s, config(keyA, keyB), 10*spendUSD)
	root := contracttest.Root(t)
	cases, err := aievals.ReadCases(root)
	if err != nil {
		t.Fatal(err)
	}
	reg, err := packs.Registry()
	if err != nil {
		t.Fatal(err)
	}
	only := []string{"MOD-002", "MOD-030", "MOD-036"}
	start := time.Date(2026, 10, 27, 1, 0, 0, 0, time.UTC)
	const runID = "01JB0000000000000000000001"
	r, err := aievals.Run(context.Background(), aievals.Config{Client: c, Budget: provider.DefaultBudget(), Packs: reg, Root: root,
		Cases: cases, Only: only, Locale: "en", Approval: "H-018", Model: model, Kind: "eval", CapNanoUSD: 10 * spendUSD, Spent: m.Spent,
		RunID: runID, Now: func() time.Time { return start }})
	if err != nil {
		t.Fatal(err)
	}
	// The server answers needs_input to every case: MOD-036 passes, the
	// golden MOD-002 does not, the safe failure MOD-030 does; no gate.
	if r.Run != 3 || !r.Complete || !r.Ok() || r.Statuses["needs_input"] != 3 || r.Passed != 2 || r.Golden.Total != 1 || r.Golden.Passed != 0 {
		t.Fatalf("%+v", r)
	}
	// Each case: org-a refused with 402 once (then skipped), org-b answered.
	if s.count(keyA) != 1 || s.count(keyB) != 3 {
		t.Errorf("hits a=%d b=%d", s.count(keyA), s.count(keyB))
	}
	cost := price(t).Cost(spend.Tokens{Input: 120, Output: 30})
	if r.Usage.InputTokens != 360 || r.Usage.OutputTokens != 90 || r.Cost.NanoUSD != 3*cost || r.Cost.USD != spend.FormatUSD(3*cost) {
		t.Errorf("usage %+v cost %+v", r.Usage, r.Cost)
	}
	if r.Model != model || r.Date != "2026-10-27" || r.RunID != runID || len(r.Served) != 1 || r.Served[0] != model {
		t.Errorf("header %+v", r)
	}
	// Review round 1, M2: every answer named its geo (global).
	if r.InferenceGeos["global"] != 3 || r.SpendWarnings["inference_geo_unknown"] != 0 || r.SpendWarnings["over_reservation"] != 0 {
		t.Errorf("geos %v warnings %v", r.InferenceGeos, r.SpendWarnings)
	}
	dir := t.TempDir()
	path := aievals.ReportPath(dir, r.Kind, start, runID, r.Model)
	if err := aievals.WriteReport(path, r); err != nil {
		t.Fatal(err)
	}
	if err := aievals.WriteReport(filepath.Join(dir, "fake-review.json"), r); err == nil {
		t.Error("a live report was written over a fake result's name")
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var back map[string]any
	if err := json.Unmarshal(data, &back); err != nil || back["model"] != model || back["run_id"] != runID ||
		filepath.Base(path) != "live-2026-10-27T010000Z-"+runID+"-claude-opus-5-5.json" {
		t.Errorf("%s: %v %v", path, back["model"], err)
	}
	for _, leak := range []string{keyA, keyB, "PRIVATE-REASONING-SENTINEL", "Which station?", "CI runner", s.srv.URL} {
		if strings.Contains(string(data), leak) {
			t.Errorf("the report holds %q", leak)
		}
	}
}

// liveRun runs the live eval's cases only over the local server s, with
// the primary credential only.
func liveRun(t *testing.T, s *server, only ...string) *aievals.Report {
	t.Helper()
	c, _, m := meteredClient(t, s, config(keyA), 10*spendUSD)
	root := contracttest.Root(t)
	cases, err := aievals.ReadCases(root)
	if err != nil {
		t.Fatal(err)
	}
	reg, err := packs.Registry()
	if err != nil {
		t.Fatal(err)
	}
	r, err := aievals.Run(context.Background(), aievals.Config{Client: c, Budget: provider.DefaultBudget(), Packs: reg, Root: root,
		Cases: cases, Only: only, Locale: "en", Approval: "H-018", Model: model, Kind: "smoke", CapNanoUSD: 10 * spendUSD, Spent: m.Spent,
		RunID: "01JB0000000000000000000002", Now: func() time.Time { return time.Date(2026, 10, 27, 2, 0, 0, 0, time.UTC) }})
	if err != nil {
		t.Fatal(err)
	}
	if r.Run != len(only) || len(r.Outcomes) != len(only) {
		t.Fatalf("%+v", r)
	}
	return r
}

// The smoke's case over HTTP, answered as the projection asks (each value
// a JSON text): the answer decodes into the canonical patch, passes the
// full schema and the server's review, and MOD-002 is ready and passed.
// An encoded value that does not decode, and one that decodes to a value
// the model cannot take (a capacity that is a string), end without a ready
// proposal.
func TestTheLiveEvalDecodesTheEncodedValues(t *testing.T) {
	mod002 := proposed(`[{"op":"test","path":"/resources/1/id","value":"\"ci_runner\""},{"op":"replace","path":"/resources/1/capacity","value":"6"}]`)
	for name, c := range map[string]struct {
		answer, status string
		passed         bool
		issue          string
	}{
		"decodes":                      {mod002, "ready", true, ""},
		"does not decode":              {strings.Replace(mod002, `"value":"6"`, `"value":"six"`, 1), "invalid", false, provider.OutputInvalid},
		"decodes to a string capacity": {strings.Replace(mod002, `"value":"6"`, `"value":"\"six\""`, 1), "invalid", false, ""},
	} {
		t.Run(name, func(t *testing.T) {
			s := newServer(t, map[string][]reply{keyA: {ok(c.answer)}})
			r := liveRun(t, s, aievals.SmokeCase)
			o := r.Outcomes[0]
			if o.Status != c.status || o.Passed != c.passed || (c.issue != "" && !slices.Contains(o.Issues, c.issue)) {
				t.Errorf("%+v", o)
			}
			if c.status == "ready" && (o.PatchHash == "" || o.EffectHeld == nil || !*o.EffectHeld || o.ServerRisk != "low") {
				t.Errorf("ready without its patch, effect or risk: %+v", o)
			}
			if c.status != "ready" && (o.PatchHash != "" || o.Risk != "") {
				t.Errorf("not ready, yet with a patch or risk: %+v", o)
			}
			if r.Gates[aievals.GateReadyInvalid] != 0 || o.InputTokens != 120 {
				t.Errorf("gates %v tokens %d", r.Gates, o.InputTokens)
			}
		})
	}
}

// T-053 schema follow-up: a case the provider refused names the HTTP
// status and the error type (the adapter's list, or other) in the report,
// never the provider's message.
func TestTheLiveEvalNamesTheProviderError(t *testing.T) {
	for kind, want := range map[string]string{"invalid_request_error": "invalid_request_error", "a_new_error": "other"} {
		s := newServer(t, map[string][]reply{keyA: {apiError(400, kind)}})
		r := liveRun(t, s, aievals.SmokeCase)
		o := r.Outcomes[0]
		if o.Status != "failed" || o.Code != "AI_REQUEST_REJECTED" || o.ProviderStatus != 400 || o.ProviderErrorType != want || o.InputTokens != 0 {
			t.Errorf("%s: %+v", kind, o)
		}
		data, err := r.Marshal()
		if err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(string(data), `"provider_status": 400`) || !strings.Contains(string(data), `"provider_error_type": "`+want+`"`) ||
			strings.Contains(string(data), "BODY-SENTINEL") || strings.Contains(string(data), "a_new_error") {
			t.Errorf("%s: the report %s", kind, data)
		}
	}
	// A case that did not fail on an error status names none.
	s := newServer(t, map[string][]reply{keyA: {ok(answerJ)}})
	data, _ := liveRun(t, s, aievals.SmokeCase).Marshal()
	if strings.Contains(string(data), "provider_status") || strings.Contains(string(data), "provider_error_type") {
		t.Errorf("an answered case names a provider error: %s", data)
	}
}
