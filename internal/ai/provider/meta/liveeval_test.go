package meta_test

import (
	"context"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/kcl1104/geminus/internal/ai/provider"
	"github.com/kcl1104/geminus/internal/ai/spend"
	"github.com/kcl1104/geminus/internal/aievals"
	"github.com/kcl1104/geminus/internal/contracttest"
	"github.com/kcl1104/geminus/internal/packs"
)

// T-055: the live eval (cmd/ai-eval-live with AI_PROVIDER=meta) over the
// Muse Spark adapter and a local server of the Messages API: each case is
// one request; the report names the provider, model, Meta's price page,
// the tokens and the cost at Meta's prices, and keeps no answer, prompt,
// reasoning, key or server address. No request leaves the machine.
func TestTheLiveEvalOverHTTP(t *testing.T) {
	s := newServer(t, map[string][]reply{messagesPath: {apiError(400, "invalid_request_error"), ok(answerJ)}})
	c, _, m, _ := meteredClient(t, s, 10*spendUSD)
	root := contracttest.Root(t)
	cases, err := aievals.ReadCases(root)
	if err != nil {
		t.Fatal(err)
	}
	reg, err := packs.Registry()
	if err != nil {
		t.Fatal(err)
	}
	start := time.Date(2026, 10, 27, 1, 0, 0, 0, time.UTC)
	const runID = "01JB0000000000000000000003"
	r, err := aievals.Run(context.Background(), aievals.Config{Client: c, Budget: provider.DefaultBudget(), Packs: reg, Root: root,
		Cases: cases, Only: []string{"MOD-002", "MOD-036"}, Locale: "en", Provider: "meta", Approval: "H-029", Model: model, Kind: "eval",
		CapNanoUSD: 10 * spendUSD, Spent: m.Spent, RunID: runID, Now: func() time.Time { return start }})
	if err != nil {
		t.Fatal(err)
	}
	// The first case is refused (400, not retried, nothing billed); the
	// second is answered needs_input and passes.
	if r.Run != 2 || !r.Complete || r.Statuses["failed"] != 1 || r.Statuses["needs_input"] != 1 || r.Passed != 1 || s.count(messagesPath) != 2 {
		t.Fatalf("%+v hits %d", r, s.count(messagesPath))
	}
	if o := r.Outcomes[0]; o.Code != "AI_REQUEST_REJECTED" || o.ProviderStatus != 400 || o.ProviderErrorType != "invalid_request_error" {
		t.Errorf("the refused case %+v", o)
	}
	price, _ := spend.PriceOf(model)
	if cost := price.Cost(spend.Tokens{Input: 120, Output: 30}); r.Cost.NanoUSD != cost || r.Usage.InputTokens != 120 {
		t.Errorf("cost %+v usage %+v, want %d", r.Cost, r.Usage, cost)
	}
	if r.Provider != "meta" || r.Model != model || r.Price.Source != spend.MetaPriceSource || len(r.Served) != 1 || r.Served[0] != model ||
		len(r.InferenceGeos) != 0 {
		t.Errorf("header %+v", r)
	}
	path := aievals.ReportPath(t.TempDir(), r.Kind, start, runID, r.Model)
	if err := aievals.WriteReport(path, r); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasSuffix(path, "live-2026-10-27T010000Z-"+runID+"-muse-spark-1.3.json") {
		t.Errorf("report path %s", path)
	}
	leaks(t, "the report", string(data), "Which station?", "CI runner", s.srv.URL)
}
