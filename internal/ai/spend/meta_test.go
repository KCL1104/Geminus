package spend_test

import (
	"testing"

	"github.com/kcl1104/geminus/internal/ai/spend"
)

// T-055 (H-029, H-033): the Meta Model API's list prices, Standard mode,
// from https://dev.meta.ai/docs/pricing-rate-limits (retrieved
// 2026-10-26): Muse Spark ("muse-spark-1.3 ... in Standard mode") USD 0.15
// per million cached input tokens, 1.25 per million input tokens, 4.25 per
// million output tokens; SAM 3.1 "Image segmentation | $2.50 per 1,000
// images". The page names no cache-write price, so a cache-write token is
// priced as an input token (never below what it can cost).
func TestTheMetaPrices(t *testing.T) {
	p, ok := spend.PriceOf("muse-spark-1.3")
	if !ok {
		t.Fatal("no price for muse-spark-1.3")
	}
	want := spend.Price{Input: 1250, Output: 4250, CacheWrite5m: 1250, CacheWrite1h: 1250, CacheRead: 150}
	if p != want {
		t.Fatalf("price %+v, want %+v", p, want)
	}
	if got := p.Cost(spend.Tokens{Input: 120, Output: 30, CacheWrite: 10, CacheRead: 5}); got != 120*1250+30*4250+10*1250+5*150 {
		t.Errorf("cost %d", got)
	}
	if got := p.Cost(spend.Tokens{Input: 1_000_000, Output: 1_000_000}); got != 5_500_000_000 {
		t.Errorf("a million of each: %d, want USD 5.50", got)
	}
	// Only the Standard-mode model H-029 names: never the Contributor
	// variant (content used for training), another version or spelling.
	for _, m := range []string{"muse-spark-1.3-contributor", "muse-spark-1.2", "muse-spark-1.2-contributor", "muse-spark-1.1",
		"muse-wonder-1.0", "MUSE-SPARK-1.3", "muse-spark-1.3 ", "muse-spark", "sam-3.1"} {
		if _, ok := spend.PriceOf(m); ok {
			t.Errorf("%q has a token price", m)
		}
	}
	img, ok := spend.ImagePriceOf("sam-3.1")
	if !ok || img != 2_500_000 {
		t.Errorf("sam-3.1: %d %v, want 2500000 nano-USD (USD 2.50 per 1,000 images)", img, ok)
	}
	for _, m := range []string{"sam-3", "sam-3.1-video", "SAM-3.1", "muse-spark-1.3", "claude-opus-5-5", ""} {
		if _, ok := spend.ImagePriceOf(m); ok {
			t.Errorf("%q has an image price", m)
		}
	}
}

// Only Anthropic prices inference geos (T-053 review round 1, M2; D-116):
// its reservation assumes the dearest and its answer is priced at the geo
// it reports. Meta names none (the Messages API refuses inference_geo), so
// its calls are reserved and settled at the list price.
func TestOnlyAnthropicPricesInferenceGeos(t *testing.T) {
	for model, want := range map[string]bool{"claude-opus-5-5": true, "muse-spark-1.3": false, "sam-3.1": false, "unknown": false, "": false} {
		if got := spend.GeoPriced(model); got != want {
			t.Errorf("%q: geo priced %v, want %v", model, got, want)
		}
	}
}

// Every price says where it was read and when.
func TestEveryPriceHasItsSource(t *testing.T) {
	for model, want := range map[string]string{
		"claude-opus-5-5": "https://platform.claude.com/docs/en/about-claude/pricing",
		"muse-spark-1.3":  "https://dev.meta.ai/docs/pricing-rate-limits",
		"sam-3.1":         "https://dev.meta.ai/docs/pricing-rate-limits",
	} {
		src, at, ok := spend.SourceOf(model)
		if !ok || src != want || at != "2026-10-26" {
			t.Errorf("%s: %q %q %v", model, src, at, ok)
		}
	}
	if _, _, ok := spend.SourceOf("muse-spark-1.3-contributor"); ok {
		t.Error("an unpriced model has a source")
	}
}
