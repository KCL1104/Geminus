package api_test

import (
	"encoding/json"
	"testing"

	"github.com/kcl1104/geminus/internal/ai/provider/fake"
	"github.com/kcl1104/geminus/internal/api"
	"github.com/kcl1104/geminus/internal/proposals"
	"github.com/kcl1104/geminus/internal/tenant"
)

func researchBody(base, mode, intent string, research any) string {
	data, _ := json.Marshal(map[string]any{"base_version_id": base, "mode": mode, "intent": intent, "research": research})
	return string(data)
}

// H-046 (2): research is asked per request and taken only when this
// deployment's provider may search; refused, nothing is stored. Taken, the
// job carries the choice, the audit says so (a flag, never the intent),
// and the proposal shows the pages the call opened and the web search's
// counts (the deterministic fake's test pages: no page is fetched).
func TestProposalResearch(t *testing.T) {
	for _, c := range []struct {
		opts []api.Option
		code string
	}{
		{nil, "AI_PROVIDER_NOT_CONFIGURED"},
		{[]api.Option{api.WithResearchAI(proposals.NotConfigured)}, "AI_PROVIDER_NOT_CONFIGURED"},
		{[]api.Option{api.WithResearchAI(proposals.PolicyNotApproved)}, "AI_POLICY_NOT_APPROVED"},
	} {
		e := setupProposals(t, c.opts...)
		editor := e.a.Members[tenant.Editor]
		m := e.newModel(t)
		r := e.propose(editor.Subject, e.a.ID, m.ID, "prop-research-off-01", researchBody(m.LatestVersion.ID, "edit", "Model a carton plant.", true))
		if r.status != 503 || code(t, r) != c.code {
			t.Errorf("%s: %d %s", c.code, r.status, r.body)
		}
		var body struct {
			Error struct {
				Details []struct{ Path, Code string } `json:"details"`
			} `json:"error"`
		}
		_ = json.Unmarshal(r.body, &body)
		if len(body.Error.Details) != 1 || body.Error.Details[0].Code != "AI_RESEARCH_UNSUPPORTED" || body.Error.Details[0].Path != "/research" {
			t.Errorf("%s: details %+v", c.code, body.Error.Details)
		}
		if n := e.count(`SELECT count(*) FROM twin.ai_proposals WHERE organization_id=$1`, e.a.ID); n != 0 {
			t.Errorf("%s: %d proposals", c.code, n)
		}
		if n := e.count(`SELECT count(*) FROM twin.jobs WHERE organization_id=$1`, e.a.ID); n != 0 {
			t.Errorf("%s: %d jobs", c.code, n)
		}
		// Without research the same deployment takes the request.
		if r := e.propose(editor.Subject, e.a.ID, m.ID, "prop-research-off-02", researchBody(m.LatestVersion.ID, "edit", "Raise a runner.", false)); r.status != 202 {
			t.Errorf("%s: without research %d %s", c.code, r.status, r.body)
		}
	}

	e := setupProposals(t, api.WithResearchAI(proposals.Available))
	editor := e.a.Members[tenant.Editor]
	m := e.newModel(t)
	for _, bad := range []any{"yes", 1, nil} {
		if r := e.propose(editor.Subject, e.a.ID, m.ID, "prop-research-bad", researchBody(m.LatestVersion.ID, "edit", "x", bad)); r.status != 422 && r.status != 400 {
			t.Errorf("research %v: %d %s", bad, r.status, r.body)
		}
	}
	r := e.propose(editor.Subject, e.a.ID, m.ID, "prop-research-01", researchBody(m.LatestVersion.ID, "scenario", "Model a carton plant.", true))
	if r.status != 202 {
		t.Fatalf("%d %s", r.status, r.body)
	}
	id := decode[proposals.Accepted](t, r).ProposalID
	// The same key with research off is another request.
	if r := e.propose(editor.Subject, e.a.ID, m.ID, "prop-research-01", researchBody(m.LatestVersion.ID, "scenario", "Model a carton plant.", false)); r.status != 409 {
		t.Errorf("the key with research off: %d %s", r.status, r.body)
	}
	if n := e.count(`SELECT count(*) FROM twin.jobs WHERE organization_id=$1 AND ai_proposal_id=$2 AND payload->>'research' = 'true'`, e.a.ID, id); n != 1 {
		t.Errorf("%d jobs carry research", n)
	}
	if n := e.count(`SELECT count(*) FROM twin.audit_events WHERE organization_id=$1 AND target_id=$2 AND action='proposal.create'
		AND metadata->>'research' = 'true' AND NOT metadata ? 'intent'`, e.a.ID, id); n != 1 {
		t.Errorf("%d proposal.create events say research", n)
	}
	p := e.generated(t, editor.Subject, e.a.ID, id)
	if p.Status != "needs_input" {
		t.Fatalf("status %s", p.Status)
	}
	got := e.do("GET", "/v1/ai-proposals/"+id, editor.Subject, e.a.ID, nil, "")
	valid(t, got, "proposal.schema.json")
	var view struct {
		Sources []struct {
			URL   string `json:"url"`
			Title string `json:"title"`
		} `json:"sources"`
		GeneratedBy struct {
			Provider  string `json:"provider"`
			WebSearch *struct {
				Queries        int `json:"queries"`
				SourcesDropped int `json:"sources_dropped"`
			} `json:"web_search"`
		} `json:"generated_by"`
	}
	if err := json.Unmarshal(got.body, &view); err != nil {
		t.Fatal(err)
	}
	if len(view.Sources) != 2 || view.Sources[0].URL != fake.ResearchSources[0].URL || view.Sources[1].URL != fake.ResearchSources[1].URL ||
		view.GeneratedBy.WebSearch == nil || view.GeneratedBy.WebSearch.Queries != 2 || view.GeneratedBy.WebSearch.SourcesDropped != 1 {
		t.Errorf("the researched proposal: %s", got.body)
	}
	if req := e.fake.Requests(); len(req) != 1 || !req[0].Prompt.Research {
		t.Errorf("the provider was not asked with research: %d requests", len(req))
	}
	if n := e.count(`SELECT count(*) FROM twin.audit_events WHERE organization_id=$1 AND target_id=$2 AND action='proposal.generate'
		AND (metadata->>'web_search_queries')::int = 2 AND (metadata->>'sources_kept')::int = 2`, e.a.ID, id); n != 1 {
		t.Errorf("%d proposal.generate events count the web search", n)
	}
}
