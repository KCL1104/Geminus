package provider

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

// slowModels are the models whose calls are bounded as a research call is
// (Budget.ResearchTimeout for each attempt and for the whole call: at the
// defaults, D-007's 300 s job timeout for both): Muse Wonder 1.0, Meta's most
// capable model, in preview, which took 117 s for one small question in the
// coordinator's probe of 2026-10-30 (H-049), more than the 90 s default
// attempt. A call on one keeps its own output bound and retries, all within
// the one deadline; a research call or a call with images already has those
// bounds.
var slowModels = map[string]bool{"muse-wonder-1.0": true}

// SlowModel reports whether model's calls have a research call's timeouts.
func SlowModel(model string) bool { return slowModels[model] }
