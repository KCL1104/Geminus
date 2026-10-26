package meta

import (
	"context"
	"net/http"
	"slices"
	"time"

	"github.com/kcl1104/geminus/internal/ai/provider"
)

// NewForTest points the Muse Spark backend at a local test server.
// Test-only: no production code can change the provider URL. It serves
// every role and takes images (SetRoles and SetImages narrow it): its zero
// Approval names none.
func NewForTest(cfg Config, baseURL string, client *http.Client) (*Backend, error) {
	return newBackend(cfg, baseURL, client, func(string) bool { return true }, true)
}

// NewSegmenterForTest points the SAM backend at a local test server.
func NewSegmenterForTest(cfg Config, baseURL string, client *http.Client) (*Segmenter, error) {
	return newSegmenter(cfg, baseURL, client, func(string) bool { return true }, true)
}

func connOf(v any) *conn {
	switch x := v.(type) {
	case *Backend:
		return x.c
	case *Segmenter:
		return x.c
	}
	panic("not a backend")
}

// SetClock replaces the backend's clock (the 402 cooldown).
func SetClock(v any, now func() time.Time) { connOf(v).now = now }

// SetSleep replaces the wait between attempts (the tests record it).
func SetSleep(v any, sleep func(context.Context, time.Duration) error) { connOf(v).sleep = sleep }

// SetBackoff replaces the backoff before an attempt.
func SetBackoff(v any, backoff func(attempt int) time.Duration) { connOf(v).backoff = backoff }

// SetRoles makes the backend serve only roles, as an approval's data
// policy would.
func SetRoles(v any, roles ...string) {
	allows := func(role string) bool { return slices.Contains(roles, role) }
	switch x := v.(type) {
	case *Backend:
		x.allows = allows
	case *Segmenter:
		x.allows = allows
	}
}

// SetImages says whether the approval's data policy names images.
func SetImages(v any, images bool) {
	switch x := v.(type) {
	case *Backend:
		x.images = images
	case *Segmenter:
		x.images = images
	}
}

// DefaultBackoff is the backoff of a production backend.
var DefaultBackoff = defaultBackoff

// SchemaDepth is how deeply schema nests, as the guard counts it.
var SchemaDepth = schemaDepth

// ParseSegmentation reads SAM's output grammar (the fuzz target).
func ParseSegmentation(text, concept string) (int, int, []provider.Segment, error) {
	return parseSegmentation(text, concept)
}

const (
	BillingCooldown     = billingCooldown
	QuoteOverheadTokens = quoteOverheadTokens
	ContextWindowTokens = contextWindowTokens
	MaxResponseBytes    = maxResponseBytes
)

var ProductionURL = productionURL
