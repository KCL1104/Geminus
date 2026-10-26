package anthropic

import (
	"net/http"
	"slices"
	"strings"
	"time"

	"github.com/kcl1104/geminus/internal/ai/provider/structured"
)

// The projection and its decoding moved to internal/ai/provider/structured
// (T-055); its tests stay here, unchanged, and reach what they used of the
// package's own through these names.
var (
	project = structured.ProjectFrom
	dropped = structured.DroppedKeywords()
)

const maxIssues = structured.MaxIssues

// pointerGet resolves the JSON Pointer fragment frag in doc (the guard's
// local $ref targets).
func pointerGet(doc any, frag string) (any, bool) {
	cur := doc
	if frag == "" {
		return cur, true
	}
	for _, tok := range strings.Split(strings.TrimPrefix(frag, "/"), "/") {
		tok = strings.NewReplacer("~1", "/", "~0", "~").Replace(tok)
		m, ok := cur.(map[string]any)
		if !ok {
			return nil, false
		}
		if cur, ok = m[tok]; !ok {
			return nil, false
		}
	}
	return cur, true
}

// NewForTest points the backend at a local test server. Test-only: no
// production code can change the provider URL. The backend serves every
// role (SetRoles narrows it): its zero Approval names none.
func NewForTest(cfg Config, baseURL string, client *http.Client) (*Backend, error) {
	return newBackend(cfg, baseURL, client, func(string) bool { return true })
}

// SetClock replaces the backend's clock (the 402 cooldown).
func SetClock(b *Backend, now func() time.Time) { b.now = now }

// SetRoles makes the backend serve only roles, as an approval's data
// policy would.
func SetRoles(b *Backend, roles ...string) {
	b.allows = func(role string) bool { return slices.Contains(roles, role) }
}

// BillingCooldown is the 402 cooldown.
const BillingCooldown = billingCooldown

// QuoteOverheadTokens is the quote's allowance for the API's own input.
const QuoteOverheadTokens = quoteOverheadTokens

var ProductionURL = productionURL
