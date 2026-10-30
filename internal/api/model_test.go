package api_test

import (
	"encoding/json"
	"testing"

	"github.com/kcl1104/geminus/internal/api"
	"github.com/kcl1104/geminus/internal/contracttest"
	"github.com/kcl1104/geminus/internal/tenant"
)

func modelBody(base, mode, intent string, model any) string {
	data, _ := json.Marshal(map[string]any{"base_version_id": base, "mode": mode, "intent": intent, "model": model})
	return string(data)
}

func detailOf(t *testing.T, r resp) (string, string) {
	t.Helper()
	var body struct {
		Error struct {
			Details []struct{ Path, Code string } `json:"details"`
		} `json:"error"`
	}
	_ = json.Unmarshal(r.body, &body)
	if len(body.Error.Details) != 1 {
		return "", ""
	}
	return body.Error.Details[0].Path, body.Error.Details[0].Code
}

// H-049: a person chooses the model of a proposal, per request. A model this
// deployment's approval does not list is refused (503 AI_POLICY_NOT_APPROVED,
// AI_MODEL_NOT_APPROVED at /model) and nothing is stored; an unknown one is
// 422 at /model. Taken, the job carries the choice, the audit names it, the
// worker asks it (the fake answers as itself), and the same key with another
// model is another request. A request that names none is as before.
func TestProposalModelChoice(t *testing.T) {
	e := setupProposals(t)
	editor := e.a.Members[tenant.Editor]
	m := e.newModel(t)
	r := e.propose(editor.Subject, e.a.ID, m.ID, "prop-model-off-01", modelBody(m.LatestVersion.ID, "edit", "Raise a runner.", "muse-wonder-1.0"))
	if path, c := detailOf(t, r); r.status != 503 || code(t, r) != "AI_POLICY_NOT_APPROVED" || path != "/model" || c != "AI_MODEL_NOT_APPROVED" {
		t.Errorf("no model choice: %d %s", r.status, r.body)
	}
	if n := e.count(`SELECT count(*) FROM twin.ai_proposals WHERE organization_id=$1`, e.a.ID); n != 0 {
		t.Errorf("%d proposals stored", n)
	}
	if r := e.propose(editor.Subject, e.a.ID, m.ID, "prop-model-off-02", proposalBody(m.LatestVersion.ID, "edit", "Raise a runner.")); r.status != 202 {
		t.Errorf("without a model: %d %s", r.status, r.body)
	}

	e = setupProposals(t, api.WithProposalModels([]string{"muse-spark-1.3", "muse-wonder-1.0"}, nil))
	editor = e.a.Members[tenant.Editor]
	m = e.newModel(t)
	for _, bad := range []any{"muse-spark-1.3-contributor", "Muse-Wonder-1.0", 1, ""} {
		if r := e.propose(editor.Subject, e.a.ID, m.ID, "prop-model-bad", modelBody(m.LatestVersion.ID, "edit", "x", bad)); r.status != 422 {
			t.Errorf("model %v: %d %s", bad, r.status, r.body)
		}
	}
	r = e.propose(editor.Subject, e.a.ID, m.ID, "prop-model-01", modelBody(m.LatestVersion.ID, "scenario", "Add a runner.", "muse-wonder-1.0"))
	if r.status != 202 {
		t.Fatalf("%d %s", r.status, r.body)
	}
	id := decode[struct {
		ProposalID string `json:"proposal_id"`
	}](t, r).ProposalID
	if r := e.propose(editor.Subject, e.a.ID, m.ID, "prop-model-01", modelBody(m.LatestVersion.ID, "scenario", "Add a runner.", "muse-spark-1.3")); r.status != 409 {
		t.Errorf("the key with another model: %d %s", r.status, r.body)
	}
	// Review round 1, M1: a Muse Wonder job has a single attempt.
	if n := e.count(`SELECT count(*) FROM twin.jobs WHERE organization_id=$1 AND ai_proposal_id=$2 AND payload->>'model' = 'muse-wonder-1.0'
		AND max_attempts = 1`, e.a.ID, id); n != 1 {
		t.Errorf("%d single-attempt jobs carry the model", n)
	}
	if n := e.count(`SELECT count(*) FROM twin.audit_events WHERE organization_id=$1 AND target_id=$2 AND action='proposal.create'
		AND metadata->>'requested_model' = 'muse-wonder-1.0' AND NOT metadata ? 'intent'`, e.a.ID, id); n != 1 {
		t.Errorf("%d proposal.create events name the model", n)
	}
	if p := e.generated(t, editor.Subject, e.a.ID, id); p.Status != "needs_input" {
		t.Errorf("status %s", p.Status)
	}
	if reqs := e.fake.Requests(); len(reqs) == 0 || reqs[len(reqs)-1].Model != "muse-wonder-1.0" {
		t.Errorf("the worker asked %+v", reqs)
	}
}

// The photo's model is a form field before the file: one the photo path's
// approval does not list is 503 at /model and nothing is read or stored; an
// unknown one 422 at /model; one it lists goes with the job to the vision
// call.
func TestPhotoModelChoice(t *testing.T) {
	demo := contracttest.LoadPhotoDemo(t, "electronics-line-demo")
	e := setupPhotos(t, api.WithProposalModels(nil, []string{"muse-spark-1.3"}))
	editor := e.a.Members[tenant.Editor]
	mdl, mv := e.bareLine(t, e.a)
	parts := func(model string) []part {
		ps := photoParts(mv, "", demo.Image)
		return append(ps[:1:1], append([]part{{name: "model", content: model}}, ps[1:]...)...)
	}
	r := e.photo(t, editor.Subject, e.a.ID, mdl, "photo-model-01", parts("muse-wonder-1.0"))
	if path, c := detailOf(t, r); r.status != 503 || code(t, r) != "AI_POLICY_NOT_APPROVED" || path != "/model" || c != "AI_MODEL_NOT_APPROVED" {
		t.Errorf("a model the photo path does not list: %d %s", r.status, r.body)
	}
	r = e.photo(t, editor.Subject, e.a.ID, mdl, "photo-model-02", parts("muse-wonder"))
	if path, _ := detailOf(t, r); r.status != 422 || path != "/model" {
		t.Errorf("an unknown model: %d %s", r.status, r.body)
	}
	if n := len(e.files(t)); n != 0 {
		t.Errorf("%d files stored", n)
	}
	if n := e.count(`SELECT count(*) FROM twin.ai_proposals WHERE organization_id=$1`, e.a.ID); n != 0 {
		t.Errorf("%d proposals stored", n)
	}
	r = e.photo(t, editor.Subject, e.a.ID, mdl, "photo-model-03", parts("muse-spark-1.3"))
	if r.status != 202 {
		t.Fatalf("%d %s", r.status, r.body)
	}
	if n := e.count(`SELECT count(*) FROM twin.jobs WHERE organization_id=$1 AND payload->>'model' = 'muse-spark-1.3'`, e.a.ID); n != 1 {
		t.Errorf("%d jobs carry the model", n)
	}
}
