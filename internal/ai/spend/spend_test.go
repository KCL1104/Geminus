package spend_test

import (
	"math"
	"testing"
	"time"

	"github.com/kcl1104/geminus/internal/ai/spend"
)

// The price of the model H-018 approves is the list price Anthropic
// publishes (https://platform.claude.com/docs/en/about-claude/pricing,
// retrieved 2026-10-26): USD 4 per million input tokens, 20 per million
// output tokens, 5 and 8 per million 5-minute and 1-hour cache writes,
// 0.20 per million cache reads. 1 USD per million tokens is 1000 nano-USD
// per token.
func TestThePinnedPrice(t *testing.T) {
	p, ok := spend.PriceOf("claude-opus-5-5")
	if !ok {
		t.Fatal("no price for claude-opus-5-5")
	}
	want := spend.Price{Input: 4000, Output: 20000, CacheWrite5m: 5000, CacheWrite1h: 8000, CacheRead: 200}
	if p != want {
		t.Fatalf("price %+v, want %+v", p, want)
	}
	for _, m := range []string{"", "claude-opus-5", "claude-opus-5-5-20260401", "CLAUDE-OPUS-5-5", "claude-sonnet-5-5"} {
		if _, ok := spend.PriceOf(m); ok {
			t.Errorf("%q has a price: only the approved model is priced", m)
		}
	}
	if spend.PriceSource != "https://platform.claude.com/docs/en/about-claude/pricing" || spend.PriceRetrieved != "2026-10-26" {
		t.Errorf("source %q retrieved %q", spend.PriceSource, spend.PriceRetrieved)
	}
}

// A call's cost: every token kind at its rate, cache writes at the 1-hour
// rate (the usage does not say which TTL, so the higher one).
func TestCost(t *testing.T) {
	p, _ := spend.PriceOf("claude-opus-5-5")
	for _, c := range []struct {
		tok  spend.Tokens
		want spend.NanoUSD
	}{
		{spend.Tokens{}, 0},
		{spend.Tokens{Input: 1}, 4000},
		{spend.Tokens{Output: 1}, 20000},
		{spend.Tokens{CacheWrite: 1}, 8000},
		{spend.Tokens{CacheRead: 1}, 200},
		{spend.Tokens{Input: 1_000_000, Output: 1_000_000}, 24 * spend.NanoPerUSD},
		{spend.Tokens{Input: 120, Output: 30, CacheWrite: 10, CacheRead: 5}, 120*4000 + 30*20000 + 10*8000 + 5*200},
		// Negative counts are not tokens.
		{spend.Tokens{Input: -5, Output: 2}, 40000},
	} {
		if got := p.Cost(c.tok); got != c.want {
			t.Errorf("%+v: %d, want %d", c.tok, got, c.want)
		}
	}
	// Saturates instead of wrapping.
	if got := p.Cost(spend.Tokens{Input: math.MaxInt64, Output: math.MaxInt64}); got != math.MaxInt64 {
		t.Errorf("overflow: %d", got)
	}
}

// One attempt's bound: its input tokens at the input rate and max_tokens
// at the output rate (thinking is output and within max_tokens).
func TestAttemptBound(t *testing.T) {
	p, _ := spend.PriceOf("claude-opus-5-5")
	if got := p.AttemptBound(10_000, 8192); got != 10_000*4000+8192*20000 {
		t.Errorf("bound %d", got)
	}
	if got := p.AttemptBound(-1, -1); got != 0 {
		t.Errorf("negative bound %d", got)
	}
	if got := p.AttemptBound(math.MaxInt64, 1); got != math.MaxInt64 {
		t.Errorf("overflow bound %d", got)
	}
}

func TestParseAndFormatUSD(t *testing.T) {
	for in, want := range map[string]spend.NanoUSD{
		"100": 100 * spend.NanoPerUSD, "0.5": spend.NanoPerUSD / 2, "2.25": 2_250_000_000, "0.000000001": 1,
		"1000000": 1_000_000 * spend.NanoPerUSD,
	} {
		got, err := spend.ParseUSD(in)
		if err != nil || got != want {
			t.Errorf("%q: %d %v, want %d", in, got, err, want)
		}
	}
	for _, bad := range []string{"", "0", "0.0", "-1", "+1", "1e3", "1.", ".5", "1.0000000001", "1,5", " 1", "1000000.000000001", "NaN", "１"} {
		if v, err := spend.ParseUSD(bad); err == nil {
			t.Errorf("%q accepted as %d", bad, v)
		}
	}
	for n, want := range map[spend.NanoUSD]string{0: "0.00", 1: "0.000000001", spend.NanoPerUSD: "1.00", 2_250_000_000: "2.25",
		123_456_789: "0.123456789", 100 * spend.NanoPerUSD: "100.00", -1: "-0.000000001"} {
		if got := spend.FormatUSD(n); got != want {
			t.Errorf("%d: %q, want %q", n, got, want)
		}
	}
}

// The limits: each positive, the organization's day within the
// deployment's day, the day within the month (PROPOSED D-110).
func TestLimits(t *testing.T) {
	month := 100 * spend.NanoPerUSD
	d := spend.DefaultLimits(month)
	if d.Month != month || d.Day != 10*spend.NanoPerUSD || d.OrganizationDay != 5*spend.NanoPerUSD || d.Validate() != nil {
		t.Fatalf("defaults %+v %v", d, d.Validate())
	}
	// A month smaller than the default day caps the days.
	if s := spend.DefaultLimits(3 * spend.NanoPerUSD); s.Day != 3*spend.NanoPerUSD || s.OrganizationDay != 3*spend.NanoPerUSD || s.Validate() != nil {
		t.Errorf("small month %+v", s)
	}
	for name, l := range map[string]spend.Limits{
		"zero":                {},
		"no month":            {Day: 1, OrganizationDay: 1},
		"no day":              {Month: 2, OrganizationDay: 1},
		"no organization day": {Month: 2, Day: 1},
		"day above month":     {Month: 1, Day: 2, OrganizationDay: 1},
		"org day above day":   {Month: 3, Day: 1, OrganizationDay: 2},
		"negative":            {Month: -1, Day: -1, OrganizationDay: -1},
	} {
		if l.Validate() == nil {
			t.Errorf("%s: %+v accepted", name, l)
		}
	}
}

// Periods are UTC calendar days and months, whatever the time zone of
// the clock.
func TestPeriods(t *testing.T) {
	taipei := time.FixedZone("UTC+8", 8*3600)
	at := time.Date(2026, 11, 1, 3, 0, 0, 0, taipei) // 2026-10-31T19:00Z
	if got := spend.Day(at); !got.Equal(time.Date(2026, 10, 31, 0, 0, 0, 0, time.UTC)) || got.Location() != time.UTC {
		t.Errorf("day %v", got)
	}
	if got := spend.Month(at); !got.Equal(time.Date(2026, 10, 1, 0, 0, 0, 0, time.UTC)) || got.Location() != time.UTC {
		t.Errorf("month %v", got)
	}
}

// T-053 review round 1, M2: the data residency page (retrieved 2026-10-26)
// prices US-only inference at 1.1x every token category on Claude 4.6 and
// later, global at the list price; those are the only two geos. An answer
// that names another, or none, is priced at the dearest; amounts round up
// to the next nano-USD, so a geo's price is never under-counted.
func TestInferenceGeos(t *testing.T) {
	for reported, want := range map[string]string{"global": spend.GeoGlobal, "us": spend.GeoUS, "": spend.GeoUnknown, "eu": spend.GeoUnknown,
		"GLOBAL": spend.GeoUnknown, " us": spend.GeoUnknown, "unknown": spend.GeoUnknown} {
		if got := spend.Geo(reported); got != want {
			t.Errorf("Geo(%q) = %q, want %q", reported, got, want)
		}
	}
	for _, c := range []struct {
		n                        spend.NanoUSD
		global, us, unknown, any spend.NanoUSD
	}{
		{0, 0, 0, 0, 0},
		{-5, 0, 0, 0, 0},
		{1, 1, 2, 2, 2}, // 1.1 rounds up
		{10, 10, 11, 11, 11},
		{1_080_000, 1_080_000, 1_188_000, 1_188_000, 1_188_000},
		{187_720_001, 187_720_001, 206_492_002, 206_492_002, 206_492_002},
		{math.MaxInt64, math.MaxInt64, math.MaxInt64, math.MaxInt64, math.MaxInt64},
	} {
		if g, u, k, a := spend.AtGeo(c.n, spend.GeoGlobal), spend.AtGeo(c.n, spend.GeoUS), spend.AtGeo(c.n, spend.GeoUnknown), spend.AtDearestGeo(c.n); g != c.global || u != c.us || k != c.unknown || a != c.any {
			t.Errorf("%d: global %d us %d unknown %d dearest %d, want %d %d %d %d", c.n, g, u, k, a, c.global, c.us, c.unknown, c.any)
		}
	}
	if got := spend.AtGeo(1000, "mars"); got != 1100 {
		t.Errorf("a geo this build does not know: %d", got)
	}
}

// T-056 on T-055: the photo pipeline's segmenter is priced per image
// (sam-3.1; T-055's meta_test.go holds the price), and a token model's
// name is no image price, so a vision model cannot be reserved for as a
// segmenter.
func TestOnlyASegmenterIsPricedPerImage(t *testing.T) {
	if p, ok := spend.ImagePriceOf("sam-3.1"); !ok || p <= 0 {
		t.Errorf("sam-3.1 is not priced per image: %d", p)
	}
	for _, model := range []string{"claude-opus-5-5", "muse-spark-1.3", ""} {
		if p, ok := spend.ImagePriceOf(model); ok || p != 0 {
			t.Errorf("%q is priced per image: %d", model, p)
		}
	}
}
