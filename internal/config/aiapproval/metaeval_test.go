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

	"github.com/kcl1104/geminus/internal/ai/provider"
	"github.com/kcl1104/geminus/internal/ai/provider/fake"
	"github.com/kcl1104/geminus/internal/ai/spend"
	"github.com/kcl1104/geminus/internal/aievals"
	"github.com/kcl1104/geminus/internal/config"
	"github.com/kcl1104/geminus/internal/contracttest"
)

// museKey is a placeholder in the documented shape (LLM_<id>_<secret>).
const museKey = "LLM_000000000000_test-key-SENTINEL-not-a-real-key"

// inProcessMeta answers as the fake does, as the Muse Spark backend would
// bill: the live eval's backend in this test (no network; the HTTP path is
// tested on a local server in internal/ai/provider/meta).
type inProcessMeta struct {
	*fake.Backend
	calls atomic.Int32
}

func (b *inProcessMeta) Quote(provider.Request) (provider.Quote, error) {
	return provider.Quote{Model: "muse-spark-1.3", MaxInputTokens: 10_000}, nil
}

func (b *inProcessMeta) Complete(ctx context.Context, req provider.Request) (provider.Response, error) {
	b.calls.Add(1)
	resp, err := b.Backend.Complete(ctx, req)
	resp.Usage, resp.Model, resp.Credential = provider.Usage{InputTokens: 1500, OutputTokens: 300}, "muse-spark-1.3", "meta"
	return resp, err
}

// T-055: cmd/ai-eval-live can target Meta: with H-029, its model, the key
// and the switch, Main builds the backend (here the in-process one, never
// Meta's), meters the run by its cap at Meta's prices (no geo multiplier),
// and the report and the spend record name the provider, the model, the
// approval and Meta's price page; neither holds the key.
func TestTheLiveEvalTargetsMeta(t *testing.T) {
	root := contracttest.Root(t)
	env := map[string]string{"AI_PROVIDER": "meta", "AI_POLICY_APPROVAL": "H-029", "AI_MODEL": "muse-spark-1.3",
		"GEMINUS_MUSE_API_KEY": museKey, "GEMINUS_AI_LIVE": "on", "MODEL_API_KEY": "must-not-be-read"}
	dir := emptyRecord(t)
	var out, errb bytes.Buffer
	b := &inProcessMeta{Backend: fake.New()}
	built := 0
	factory := func(ai config.AI) (provider.Billable, error) {
		built++
		if !ai.LiveEnabled() || ai.Provider != config.AIProviderMeta || ai.Model != "muse-spark-1.3" || len(ai.Credentials) != 1 ||
			ai.Credentials[0].Label != "meta" || ai.Credentials[0].Key.Reveal() != museKey {
			t.Errorf("the backend was asked for %+v", ai)
		}
		return b, nil
	}
	code := aievals.Main(context.Background(), []string{"-root", root, "-dir", dir, "-smoke"}, func(k string) string { return env[k] }, &out, &errb, factory)
	stdout, stderr := out.String(), errb.String()
	if code != aievals.ExitOK || built != 1 || b.calls.Load() != 1 {
		t.Fatalf("exit %d built %d calls %d: %s %s", code, built, b.calls.Load(), stdout, stderr)
	}
	reports, _ := filepath.Glob(filepath.Join(dir, "live-smoke-*-muse-spark-1.3.json"))
	if len(reports) != 1 {
		t.Fatalf("reports %v (%s)", reports, stdout)
	}
	data, err := os.ReadFile(reports[0])
	if err != nil {
		t.Fatal(err)
	}
	var r aievals.Report
	if err := json.Unmarshal(data, &r); err != nil {
		t.Fatal(err)
	}
	price, _ := spend.PriceOf("muse-spark-1.3")
	if r.Provider != "meta" || r.Model != "muse-spark-1.3" || r.Approval != "H-029" || r.Price.Source != spend.MetaPriceSource ||
		r.Price.Retrieved != spend.MetaPriceRetrieved || r.Cost.NanoUSD != price.Cost(spend.Tokens{Input: 1500, Output: 300}) || !r.Complete ||
		len(r.InferenceGeos) != 0 {
		t.Errorf("report %+v", r)
	}
	lines := recordLines(t, dir)
	if len(lines) != 2 || lines[0]["model"] != "muse-spark-1.3" || lines[0]["approval"] != "H-029" {
		t.Errorf("the spend record: %v", lines)
	}
	record, _ := os.ReadFile(filepath.Join(dir, recordName))
	for _, leak := range []string{museKey, "SENTINEL", "must-not-be-read"} {
		if bytes.Contains(data, []byte(leak)) || bytes.Contains(record, []byte(leak)) || strings.Contains(stdout+stderr, leak) {
			t.Errorf("%q is in the report, the record or the output", leak)
		}
	}
}
