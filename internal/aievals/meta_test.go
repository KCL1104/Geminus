package aievals_test

import (
	"bytes"
	"context"
	"os"
	"strings"
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

// T-055: no test here calls Meta either; the key is a placeholder in the
// documented shape.
const museKey = "LLM_000000000000_test-key-SENTINEL-not-a-real-key"

// A report names the provider and the price source of the run's model:
// Meta's page for Muse Spark (T-055), Anthropic's for Claude.
func TestTheReportNamesTheProviderAndItsPrices(t *testing.T) {
	root, cases := corpus(t)
	reg, err := packs.Registry()
	if err != nil {
		t.Fatal(err)
	}
	for _, c := range []struct {
		provider, model, source, retrieved string
	}{
		{"meta", "muse-spark-1.3", spend.MetaPriceSource, spend.MetaPriceRetrieved},
		{"anthropic", "claude-opus-5-5", spend.PriceSource, spend.PriceRetrieved},
	} {
		m := provider.NewMemoryMeter(spend.NanoPerUSD)
		client, err := provider.NewMetered(&billed{Backend: fake.New()}, provider.DefaultBudget(), m)
		if err != nil {
			t.Fatal(err)
		}
		r, err := aievals.Run(context.Background(), aievals.Config{Client: client, Budget: provider.DefaultBudget(), Packs: reg, Root: root,
			Cases: cases, Only: []string{"MOD-036"}, Locale: "en", Provider: c.provider, Approval: "H-029", Model: c.model, Kind: "smoke",
			CapNanoUSD: spend.NanoPerUSD, Spent: m.Spent, Now: func() time.Time { return started }})
		if err != nil {
			t.Fatal(err)
		}
		if r.Provider != c.provider || r.Model != c.model || r.Price.Source != c.source || r.Price.Retrieved != c.retrieved {
			t.Errorf("%s: provider %q model %q price %+v", c.provider, r.Provider, r.Model, r.Price)
		}
	}
	// A run names its provider: there is no default.
	client, _ := provider.New(fake.New(), provider.DefaultBudget())
	if _, err := aievals.Run(context.Background(), aievals.Config{Client: client, Packs: reg, Root: root, Cases: cases, Model: "muse-spark-1.3"}); err == nil {
		t.Error("a run without its provider was accepted")
	}
}

// T-055: Main refuses a Meta configuration as it refuses Claude's, before
// building any backend and without printing the key: H-029 without the
// switch, the switch without the key, a key that is not Meta's shape (an
// Anthropic key set in its place), another model.
func TestMainRefusesMetaWhileLiveIsOff(t *testing.T) {
	root := contracttest.Root(t)
	h029 := map[string]string{"AI_PROVIDER": "meta", "AI_POLICY_APPROVAL": "H-029", "AI_MODEL": "muse-spark-1.3", "GEMINUS_MUSE_API_KEY": museKey}
	with := func(kv ...string) map[string]string {
		out := map[string]string{}
		for k, v := range h029 {
			out[k] = v
		}
		for i := 0; i+1 < len(kv); i += 2 {
			out[kv[i]] = kv[i+1]
		}
		return out
	}
	for name, c := range map[string]struct {
		env  map[string]string
		want string
	}{
		"nothing set":               {map[string]string{}, "live AI is off (AI_PROVIDER is not a live provider)"},
		"H-029 without a switch":    {h029, "live AI is off (switch_off)"},
		"Claude's approval":         {with("AI_POLICY_APPROVAL", "H-018"), "live AI is off (provider_mismatch)"},
		"Claude's approval, on":     {with("AI_POLICY_APPROVAL", "H-018", "GEMINUS_AI_LIVE", "on"), "AI_POLICY_APPROVAL:provider_mismatch"},
		"another model, on":         {with("AI_MODEL", "muse-spark-1.3-contributor", "GEMINUS_AI_LIVE", "on"), "AI_MODEL:model_mismatch"},
		"no key, on":                {with("GEMINUS_MUSE_API_KEY", "", "GEMINUS_AI_LIVE", "on"), "GEMINUS_MUSE_API_KEY:required"},
		"an Anthropic key, on":      {with("GEMINUS_MUSE_API_KEY", "sk-ant-test-fake-aaaaaaaaaaaa", "GEMINUS_AI_LIVE", "on"), "GEMINUS_MUSE_API_KEY:invalid"},
		"a key with a newline, on":  {with("GEMINUS_MUSE_API_KEY", museKey+"\n", "GEMINUS_AI_LIVE", "on"), "GEMINUS_MUSE_API_KEY:invalid"},
		"the generic key name only": {with("GEMINUS_MUSE_API_KEY", "", "MODEL_API_KEY", museKey, "GEMINUS_AI_LIVE", "on"), "GEMINUS_MUSE_API_KEY:required"},
	} {
		t.Run(name, func(t *testing.T) {
			dir := t.TempDir()
			var out, errb bytes.Buffer
			built := 0
			factory := func(config.AI) (provider.Billable, error) { built++; return &billed{Backend: fake.New()}, nil }
			code := aievals.Main(context.Background(), []string{"-root", root, "-dir", dir}, func(k string) string { return c.env[k] }, &out, &errb, factory)
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
}
