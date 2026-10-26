package generator

import (
	"testing"

	"github.com/kcl1104/geminus/internal/ai/provider"
	"github.com/kcl1104/geminus/internal/ai/provider/fake"
)

// T-053 I5, the T-055 follow-up (the coordinator's decision): the API
// accepts proposals from its settings alone (config.LoadAPIAI), without a
// key, so a proposal can reach a worker that cannot ask the provider: one
// whose live AI is off (no key, or the switch off: policy_not_approved;
// AI_PROVIDER disabled: not_configured) or whose key the provider refuses
// (credentials_exhausted). (A worker with the switch on and no key does not
// start at all: cmd/worker's TestAProposalTheAPIAcceptsWithoutTheWorkersKey.)
// The proposal fails in its first attempt with the typed code, not
// retried, its one attempt audited (by the handler and by the job loop);
// and nothing else is written: no
// answer, patch, risk, validation or metadata of a call on the proposal,
// no version, draft or scenario of the model, no spend.
func TestAProposalTheWorkerCannotAskFailsSafely(t *testing.T) {
	e := setup(t)
	mdl, mv := e.model(t)
	refusing := fake.New()
	refusing.Fail(provider.RoleModeler, "Raise the CI runners to 6 (a refused key).",
		provider.NewError(provider.CodeCredentialsExhausted, 401, "meta"))
	for name, c := range map[string]struct {
		backend      provider.Backend
		intent       string
		code, detail string
	}{
		"live AI off in the worker": {provider.Disabled{Code: provider.CodePolicyNotApproved}, "Raise the CI runners to 6.",
			"AI_POLICY_NOT_APPROVED", "AI_POLICY_NOT_APPROVED"},
		"AI disabled in the worker": {provider.Disabled{Code: provider.CodeNotConfigured}, "Raise the CI runners to 6, again.",
			"AI_PROVIDER_NOT_CONFIGURED", "AI_PROVIDER_NOT_CONFIGURED"},
		"the worker's key refused": {refusing, "Raise the CI runners to 6 (a refused key).", "SERVICE_UNAVAILABLE", "AI_CREDENTIALS_EXHAUSTED"},
	} {
		t.Run(name, func(t *testing.T) {
			model := func() [4]int {
				return [4]int{
					e.count(t, `SELECT count(*) FROM twin.model_versions WHERE model_id=$1`, mdl),
					e.count(t, `SELECT count(*) FROM twin.model_drafts WHERE model_id=$1`, mdl),
					e.count(t, `SELECT count(*) FROM twin.scenarios WHERE model_id=$1`, mdl),
					e.count(t, `SELECT count(*) FROM twin.ai_spend_reservations`),
				}
			}
			before := model()
			id := e.create(t, mdl, mv, "edit", c.intent)
			p := e.until(t, e.loop(t, c.backend), id)
			if p.Status != "failed" || p.Error == nil || p.Error.Error.Code != c.code || len(p.Error.Error.Details) != 1 ||
				p.Error.Error.Details[0].Code != c.detail {
				t.Fatalf("%+v", p.Error)
			}
			if p.Summary != nil || p.Changes != nil || p.RiskLevel != nil || p.ServerRiskLevel != nil || p.ProviderRiskClaim != nil ||
				p.Confidence != nil || p.PatchHash != nil || p.Validation != nil {
				t.Errorf("the failed proposal holds generated content: %+v", p)
			}
			if n := e.count(t, `SELECT count(*) FROM twin.ai_proposals WHERE id=$1 AND status='failed' AND summary IS NULL AND patch IS NULL
				AND patch_hash IS NULL AND risk_level IS NULL AND server_risk_level IS NULL AND provider_risk_claim IS NULL AND confidence IS NULL
				AND validation IS NULL AND assumptions='[]' AND expected_effects='[]' AND questions='[]' AND candidate_dataset_bindings='[]'
				AND provider_metadata='{}' AND new_version_id IS NULL AND scenario_id IS NULL AND decided_by IS NULL`, id); n != 1 {
				t.Error("the failed proposal's row holds more than its error")
			}
			if n := e.count(t, `SELECT count(*) FROM twin.jobs WHERE ai_proposal_id=$1 AND status='failed' AND attempt=1`, id); n != 1 {
				t.Error("the job was not failed in its first attempt (a retry cannot help)")
			}
			if n := e.count(t, `SELECT count(*) FROM twin.audit_events WHERE target_id=$1 AND action='proposal.attempt_failed'
				AND actor_type='worker' AND metadata->>'code'=$2 AND metadata->>'retryable'='false'`, id, c.detail); n != 1 {
				t.Errorf("%d attempt_failed events with the code", n)
			}
			// The worker's own record of the attempt (D-091 (b)): failed, not
			// requeued.
			if n := e.count(t, `SELECT count(*) FROM twin.audit_events WHERE target_id=$1 AND action='job.attempt_failed' AND actor_type='worker'
				AND metadata->>'code'=$2 AND metadata->>'outcome'='failed' AND metadata->>'attempt'='1' AND metadata->>'retryable'='false'`, id, c.detail); n != 1 {
				t.Errorf("%d job.attempt_failed events, failed in attempt 1 with the code", n)
			}
			if n := e.count(t, `SELECT count(*) FROM twin.audit_events WHERE target_id=$1
				AND action NOT IN ('proposal.create','proposal.attempt_failed','job.attempt_failed')`, id); n != 0 {
				t.Errorf("%d other audit events", n)
			}
			if n := e.count(t, `SELECT count(*) FROM twin.audit_events WHERE target_id=$1`, id); n != 3 {
				t.Errorf("%d audit events, want the proposal's creation and the attempt's two records", n)
			}
			if after := model(); after != before {
				t.Errorf("the model's versions, drafts, scenarios and the spend reservations changed: %v, then %v", before, after)
			}
			if n := e.count(t, `SELECT count(*) FROM twin.models WHERE id=$1 AND latest_version_id=$2`, mdl, mv); n != 1 {
				t.Error("the model's latest version moved")
			}
		})
	}
}
