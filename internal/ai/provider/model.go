package provider

import "time"

// Model choice (H-049): a person asking for a proposal (in words, from a
// photo or floor plan, a scenario, with or without research) chooses its
// model, Muse Spark 1.3 (the default) or Muse Wonder 1.0, per request
// (ProposalContext.Model). The client hands a request that names a model to
// no backend but a ModelChooser that may ask that model for the role, and
// refuses the call before anything else is asked (model_not_approved):
// never answered by another model instead. A metered call is reserved at the
// price of the model its quote names, which must be the model asked for, and
// an answer that names another model is refused (invalid_output), its usage
// settled all the same. Explanations of results (the analyst) never name a
// model: AnalysisContext has none, so they stay on the backend's own.

// ModelChooser is a backend that can ask another model than its own for a
// call: the Meta adapter, for the models its approval lists for the proposal
// roles (aiapproval.Approval.AllowsModel), or the deterministic fake.
type ModelChooser interface {
	Backend
	// AcceptsModel reports whether the backend may ask model for a call of
	// role.
	AcceptsModel(role Role, model string) bool
}

// CodeModelNotApproved: the request names a model the client's backend
// cannot ask, or may not for the role under its approval (H-049); refused
// before any I/O, never retried (the approval does not change on its own).
const CodeModelNotApproved Code = "model_not_approved"

// AcceptsModel reports whether the client hands a request of role that
// names model to its backend, so a caller can refuse it up front. The call
// itself still checks (call).
func (c *Client) AcceptsModel(role Role, model string) bool {
	m, ok := c.backend.(ModelChooser)
	return ok && model != "" && m.AcceptsModel(role, model)
}

// slowModels are the models whose calls have their own bounds
// (SlowModelMaxOutputTokens, SlowModelTimeout): Muse Wonder 1.0, Meta's most
// capable model, in preview. It took 117 s for one small question in the
// coordinator's probe of 2026-10-30 (H-049), and live on 2026-10-31 a text
// scenario used all 8,192 output tokens in 61 s and a photo proposal all
// 32,000 in 91 s, both truncated (H-065; its reasoning counts toward
// max_tokens, dev.meta.ai/docs/reasoning).
var slowModels = map[string]bool{"muse-wonder-1.0": true}

// SlowModel reports whether model's calls have a slow model's own bounds.
func SlowModel(model string) bool { return slowModels[model] }

// A slow model's bounds (H-065, the owner, 2026-10-31): every call on one,
// of every proposal path (in words, a scenario, research mode, a photo) and
// its H-057 repair, is sent with max_tokens SlowModelMaxOutputTokens and has
// SlowModelTimeout for each attempt and for the whole call, whatever the
// deployment's budget says (AI_MAX_OUTPUT_TOKENS, AI_TIMEOUT_SECONDS,
// AI_ATTEMPT_TIMEOUT_SECONDS, BudgetCaps: those bound Muse Spark 1.3, within
// D-007's 300 s). Meta's API accepts max_tokens of at least 200,000. Its
// retries are the call's own (the budget's; research none), all within the
// one deadline, and the reservation is every attempt at this output bound
// (meter.go), so the spend caps (H-050 as amended: USD 40 a day) still bound
// what it costs. The job runs once (internal/proposals SlowModelJobAttempts),
// and the worker's heartbeat renews its lease for as long as the attempt runs:
// a call and its repair may take twice SlowModelTimeout.
// ponytail: constants, not deployment settings, as ImageMaxOutputTokens is.
const (
	SlowModelMaxOutputTokens int64 = 128_000
	SlowModelTimeout               = 900 * time.Second
)
