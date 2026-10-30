// Package spend is the arithmetic of live AI spend (T-053, H-018): the
// list price of the approved model, the cost of a call's billed tokens, the
// most one attempt can cost, and the limits a deployment keeps (PROPOSED
// D-110, D-111). It is computation only: the persistent ledger that
// reserves before a call and settles after it is internal/storage/aispend,
// and provider.Client calls it through provider.Meter.
//
// Amounts are integers of nano-USD (1e-9 US dollar), so a list price of
// "USD x per million tokens" is x·1000 nano-USD per token and no amount is
// ever a float.
package spend

import (
	"errors"
	"math"
	"strconv"
	"strings"
	"time"
)

// NanoUSD is an amount of US dollars in units of 1e-9 dollar.
type NanoUSD = int64

// NanoPerUSD is one US dollar.
const NanoPerUSD NanoUSD = 1_000_000_000

// The source of the price table and when it was read (D-110). A price
// that changes is a reviewed change to this table and these constants.
const (
	PriceSource    = "https://platform.claude.com/docs/en/about-claude/pricing"
	PriceRetrieved = "2026-10-26"
)

// Price is a model's list price per token, first-party Claude API, global
// inference, standard speed, no batch discount. Another inference geo
// costs more (AtGeo).
type Price struct {
	Input        NanoUSD // base input tokens
	Output       NanoUSD // output tokens, thinking included
	CacheWrite5m NanoUSD // 5-minute cache writes
	CacheWrite1h NanoUSD // 1-hour cache writes
	CacheRead    NanoUSD // cache hits and refreshes
}

// prices is every model this build can price per token, by its exact
// model ID. Only the models an approval names are listed (H-018's Claude
// Opus 5.5; H-029's Muse Spark 1.3, T-055, and Muse Wonder 1.0, H-049): a
// model without a price cannot be reserved for, so it cannot be called live.
var prices = map[string]Price{
	// Claude Opus 5.5: USD 4 / 5 / 8 / 0.20 / 20 per million tokens
	// (input, 5m write, 1h write, cache hit, output), pricing page
	// retrieved 2026-10-26; the same as the claude-api reference of
	// 2026-10-06. No long-context premium on this model.
	"claude-opus-5-5": {Input: 4000, Output: 20000, CacheWrite5m: 5000, CacheWrite1h: 8000, CacheRead: 200},
	// Muse Spark 1.3, Standard mode (T-055; PROPOSED D-121): USD 0.15 /
	// 1.25 / 4.25 per million tokens (cached input, input, output),
	// MetaPriceSource retrieved 2026-10-26. The page names no cache-write
	// price: a cache-write token, should the usage report one, is priced as
	// an input token. Reasoning tokens are output tokens ("billed at the
	// same rate as visible output tokens", https://dev.meta.ai/docs/reasoning)
	// and within max_tokens. No long-context premium. Contributor mode
	// (muse-spark-1.3-contributor, whose content Meta may use for training)
	// is not priced, so it cannot be called.
	"muse-spark-1.3": {Input: 1250, Output: 4250, CacheWrite5m: 1250, CacheWrite1h: 1250, CacheRead: 150},
	// Muse Wonder 1.0, Standard mode (H-049, a person's choice for a
	// proposal): USD 4.00 / 0.20 / 20.00 per million tokens (input, cached
	// input, output), MetaPriceSource as H-049 records it (2026-10-30). A
	// cache-write token is priced as an input token, reasoning tokens as
	// output tokens, as for Muse Spark.
	"muse-wonder-1.0": {Input: 4000, Output: 20000, CacheWrite5m: 4000, CacheWrite1h: 4000, CacheRead: 200},
}

// PriceOf is model's price, if this build has one.
func PriceOf(model string) (Price, bool) {
	p, ok := prices[model]
	return p, ok
}

// The source of the Meta Model API's prices and when it was read (T-055,
// H-029, H-033; PROPOSED D-121).
const (
	MetaPriceSource    = "https://dev.meta.ai/docs/pricing-rate-limits"
	MetaPriceRetrieved = "2026-10-26"
)

// imagePrices are the segmentation models this build prices per image:
// SAM 3.1 "Image segmentation | $2.50 per 1,000 images" (MetaPriceSource,
// retrieved 2026-10-26), USD 0.0025 an image. A model without an image
// price cannot be reserved for, so it cannot be called.
var imagePrices = map[string]NanoUSD{"sam-3.1": 2_500_000}

// ImagePriceOf is the price of one image the segmentation model segments,
// if this build has one.
func ImagePriceOf(model string) (NanoUSD, bool) {
	p, ok := imagePrices[model]
	return p, ok
}

// searchPrices are the models this build prices web search for (H-046 (2),
// a research call's web_search tool): "Web search grounding: $2.50 per
// 1,000 search queries, in addition to the request's token cost. This
// applies to the web_search tool on text models such as Muse Wonder and
// Muse Spark" (MetaPriceSource, saved copy of 2026-10-27, restated by the
// coordinator on 2026-10-29), USD 0.0025 a query. A model without a search
// price cannot be reserved for a research call, so it never researches.
var searchPrices = map[string]NanoUSD{"muse-spark-1.3": 2_500_000, "muse-wonder-1.0": 2_500_000}

// SearchPriceOf is the price of one web search query of a research call on
// model, if this build has one.
func SearchPriceOf(model string) (NanoUSD, bool) {
	p, ok := searchPrices[model]
	return p, ok
}

// geoPriced are the models whose provider prices inference geos
// (Anthropic, T-053 review round 1 M2, D-116): a reservation assumes the
// dearest geo and an answer is priced at the geo it reports. Every other
// model is reserved and settled at its list price: the Meta Messages API
// names no geo and refuses inference_geo
// (https://dev.meta.ai/docs/protocols/messages).
var geoPriced = map[string]bool{"claude-opus-5-5": true}

// GeoPriced reports whether model's provider prices inference geos.
func GeoPriced(model string) bool { return geoPriced[model] }

// sources says where each priced model's price was read and when.
var sources = map[string][2]string{
	"claude-opus-5-5": {PriceSource, PriceRetrieved},
	"muse-spark-1.3":  {MetaPriceSource, MetaPriceRetrieved},
	"sam-3.1":         {MetaPriceSource, MetaPriceRetrieved},
	"muse-wonder-1.0": {MetaPriceSource, WonderPriceRetrieved},
}

// WonderPriceRetrieved is when Muse Wonder 1.0's price was read
// (MetaPriceSource; H-049).
const WonderPriceRetrieved = "2026-10-30"

// SourceOf is where model's price was read and when.
func SourceOf(model string) (source, retrieved string, ok bool) {
	src, ok := sources[model]
	return src[0], src[1], ok
}

// Tokens are the tokens a call was billed for (the provider's usage).
type Tokens struct {
	Input      int64 // uncached input tokens
	Output     int64
	CacheWrite int64 // cache creation input tokens, whatever their TTL
	CacheRead  int64
}

// mul is a·b for a, b ≥ 0, saturating at MaxInt64.
func mul(a, b int64) int64 {
	if a <= 0 || b <= 0 {
		return 0
	}
	if a > math.MaxInt64/b {
		return math.MaxInt64
	}
	return a * b
}

// add is a+b for a, b ≥ 0, saturating at MaxInt64.
func add(a, b int64) int64 {
	if a > math.MaxInt64-b {
		return math.MaxInt64
	}
	return a + b
}

// Cost is what t costs at p. Cache writes are priced at the 1-hour rate:
// the usage does not say which TTL a write had, so the higher one. A
// negative count is no tokens. It saturates at MaxInt64.
func (p Price) Cost(t Tokens) NanoUSD {
	c := mul(t.Input, p.Input)
	c = add(c, mul(t.Output, p.Output))
	c = add(c, mul(t.CacheWrite, max(p.CacheWrite1h, p.CacheWrite5m)))
	return add(c, mul(t.CacheRead, p.CacheRead))
}

// AttemptBound is the most one attempt can cost: inputTokens at the input
// rate and outputTokens (max_tokens, which bounds thinking too) at the
// output rate. The adapter never asks for caching, so no input token is a
// cache write.
func (p Price) AttemptBound(inputTokens, outputTokens int64) NanoUSD {
	return add(mul(inputTokens, p.Input), mul(outputTokens, p.Output))
}

// Inference geos (T-053 review round 1, M2; PROPOSED D-110 (a), D-116):
// where the provider ran a request. The data residency page
// (https://platform.claude.com/docs/en/manage-claude/data-residency,
// retrieved 2026-10-26) lists two, "global" (the default, standard price)
// and "us" (US-only inference, 1.1x every token category on Claude 4.6 and
// later). A request that names none runs at its workspace's
// default_inference_geo, an account setting this build cannot see, and the
// answer's usage.inference_geo says where it ran.
const (
	GeoGlobal = "global"
	GeoUS     = "us"
	// GeoUnknown is an answer that names no geo, or one this build does not
	// know: priced at the dearest.
	GeoUnknown = "unknown"
)

// geoTenths is each known geo's price in tenths of the list price.
var geoTenths = map[string]int64{GeoGlobal: 10, GeoUS: 11}

// dearestTenths is the most any geo costs, in tenths of the list price.
const dearestTenths = 11

// Geo is the geo an answer reported, as this build prices it: GeoGlobal,
// GeoUS, or GeoUnknown for anything else (nothing, another spelling, a geo
// added after this build). The provider's own text never goes further.
func Geo(reported string) string {
	if _, ok := geoTenths[reported]; ok {
		return reported
	}
	return GeoUnknown
}

// AtGeo is n, an amount at the list price, at geo's price, rounded up to
// the next nano-USD: n for global, n·1.1 for us, n·1.1 for an unknown geo
// (the dearest). It saturates at MaxInt64.
func AtGeo(n NanoUSD, geo string) NanoUSD {
	tenths, ok := geoTenths[geo]
	if !ok {
		tenths = dearestTenths
	}
	return tenthsOf(n, tenths)
}

// AtDearestGeo is n at the dearest geo: what a request that names no geo
// can cost, whatever its workspace's default (the reservation's bound).
func AtDearestGeo(n NanoUSD) NanoUSD { return tenthsOf(n, dearestTenths) }

// tenthsOf is ⌈n·tenths/10⌉ for n ≥ 0, saturating at MaxInt64.
func tenthsOf(n, tenths int64) NanoUSD {
	if n <= 0 {
		return 0
	}
	if tenths == 10 {
		return n
	}
	q, r := n/10, n%10
	whole := mul(q, tenths)
	return add(whole, (r*tenths+9)/10)
}

// maxUSD bounds what ParseUSD accepts: a million dollars.
const maxUSD = 1_000_000

var errUSD = errors.New("spend: not a positive amount of US dollars (digits, an optional '.' and at most 9 more digits, at most 1000000)")

// ParseUSD reads a positive decimal amount of dollars ("100", "2.5",
// "0.000000001"): no sign, exponent, separators or spaces.
func ParseUSD(s string) (NanoUSD, error) {
	whole, frac, dot := strings.Cut(s, ".")
	if whole == "" || (dot && (frac == "" || len(frac) > 9)) {
		return 0, errUSD
	}
	for _, part := range []string{whole, frac} {
		for _, r := range part {
			if r < '0' || r > '9' {
				return 0, errUSD
			}
		}
	}
	w, err := strconv.ParseInt(whole, 10, 64)
	if err != nil || w > maxUSD {
		return 0, errUSD
	}
	var f int64
	if frac != "" {
		f, _ = strconv.ParseInt(frac+strings.Repeat("0", 9-len(frac)), 10, 64)
	}
	n := w*NanoPerUSD + f
	if n <= 0 || n > maxUSD*NanoPerUSD {
		return 0, errUSD
	}
	return n, nil
}

// FormatUSD writes n as dollars with at least two decimals and no
// trailing zeros beyond them ("0.00", "2.25", "0.000000001").
func FormatUSD(n NanoUSD) string {
	sign := ""
	if n < 0 {
		sign, n = "-", -n
	}
	frac := strconv.FormatInt(n%NanoPerUSD, 10)
	frac = strings.Repeat("0", 9-len(frac)) + frac
	frac = strings.TrimRight(frac, "0")
	for len(frac) < 2 {
		frac += "0"
	}
	return sign + strconv.FormatInt(n/NanoPerUSD, 10) + "." + frac
}

// Limits are what the deployment lets live AI spend (H-018; D-110):
// across both provider organizations combined, in a UTC calendar month
// and in a UTC day, and for one tenant organization in a UTC day.
type Limits struct {
	Month           NanoUSD
	Day             NanoUSD
	OrganizationDay NanoUSD
}

// The proposed daily limits (D-110, PROPOSED): a tenth of H-018's monthly
// budget for the deployment's day, half of that for one organization.
const (
	DefaultDay             NanoUSD = 10 * NanoPerUSD
	DefaultOrganizationDay NanoUSD = 5 * NanoPerUSD
)

// DefaultLimits are the limits under a monthly budget: the default days,
// each at most the limit above it.
func DefaultLimits(month NanoUSD) Limits {
	day := min(DefaultDay, month)
	return Limits{Month: month, Day: day, OrganizationDay: min(DefaultOrganizationDay, day)}
}

// Validate checks that every limit is positive and that the
// organization's day ≤ the deployment's day ≤ the month.
func (l Limits) Validate() error {
	switch {
	case l.Month <= 0 || l.Day <= 0 || l.OrganizationDay <= 0:
		return errors.New("spend: every limit must be positive")
	case l.Day > l.Month:
		return errors.New("spend: the daily limit is above the monthly limit")
	case l.OrganizationDay > l.Day:
		return errors.New("spend: an organization's daily limit is above the deployment's")
	}
	return nil
}

// Day is the UTC calendar day of t, at midnight UTC.
func Day(t time.Time) time.Time {
	y, m, d := t.UTC().Date()
	return time.Date(y, m, d, 0, 0, 0, 0, time.UTC)
}

// Month is the first day of t's UTC calendar month, at midnight UTC.
func Month(t time.Time) time.Time {
	y, m, _ := t.UTC().Date()
	return time.Date(y, m, 1, 0, 0, 0, 0, time.UTC)
}
