package api_test

import (
	"context"
	"fmt"
	"net/http"
	"testing"

	"github.com/kcl1104/geminus/internal/api"
	"github.com/kcl1104/geminus/internal/storage/dbtest"
	"github.com/kcl1104/geminus/internal/storage/objects"
	"github.com/kcl1104/geminus/internal/tenant"
)

// state is what an organization's business writes change, as one string:
// a refused write must leave every part of it as it was.
func (e *env) state(org string) string {
	e.t.Helper()
	var s string
	if err := e.admin.QueryRow(context.Background(), `SELECT concat_ws(' | ',
		(SELECT string_agg(id, ',' ORDER BY id) FROM twin.projects WHERE organization_id=$1),
		(SELECT string_agg(id||':'||coalesce(latest_version_id,'')||':'||name, ',' ORDER BY id) FROM twin.models WHERE organization_id=$1),
		(SELECT string_agg(id, ',' ORDER BY id) FROM twin.model_versions WHERE organization_id=$1),
		(SELECT string_agg(model_id||':'||revision||':'||base_version_id, ',' ORDER BY model_id) FROM twin.model_drafts WHERE organization_id=$1),
		(SELECT string_agg(id, ',' ORDER BY id) FROM twin.scenarios WHERE organization_id=$1),
		(SELECT string_agg(id||':'||status||':'||cancellation_requested, ',' ORDER BY id) FROM twin.simulation_runs WHERE organization_id=$1),
		(SELECT string_agg(id||':'||status, ',' ORDER BY id) FROM twin.jobs WHERE organization_id=$1),
		(SELECT string_agg(id||':'||status, ',' ORDER BY id) FROM twin.ai_proposals WHERE organization_id=$1),
		(SELECT string_agg(user_id||':'||role, ',' ORDER BY user_id) FROM twin.memberships WHERE organization_id=$1),
		(SELECT string_agg(id||':'||status, ',' ORDER BY id) FROM twin.imports WHERE organization_id=$1),
		(SELECT count(*)::text FROM twin.idempotency_keys WHERE organization_id=$1),
		(SELECT count(*)::text FROM twin.audit_events WHERE organization_id=$1))`, org).Scan(&s); err != nil {
		e.t.Fatal(err)
	}
	return s
}

// T-035 (D-091 (c)), TEST-027, REQ-018: a business write and its audit
// event commit together, on every audited API route. While the database
// refuses the organization's audit events, each request answers 500 and
// commits nothing at all, its Idempotency-Key included; once the refusal
// ends, the same request (same key) commits, with exactly one event of its
// action under the response's request ID.
func TestAnUnwritableAuditEventCommitsNothing(t *testing.T) {
	fs, err := objects.NewFS(t.TempDir(), "test")
	if err != nil {
		t.Fatal(err)
	}
	e := setupProposals(t, api.WithObjects(fs))
	owner, editor := e.a.Members[tenant.Owner], e.a.Members[tenant.Editor]
	stranger := dbtest.NewUser(t, e.admin, e.p.Issuer)
	members := "/v1/organizations/" + e.a.ID + "/memberships"

	// step sends req while the audit events are refused, then again after.
	step := func(action string, want int, req func() resp) resp {
		t.Helper()
		before := e.state(e.a.ID)
		stop := dbtest.RefuseAudit(t, e.admin, e.a.ID)
		refused := req()
		stop()
		if refused.status != 500 || code(t, refused) != "INTERNAL_ERROR" {
			t.Errorf("%s while refused: %d %s", action, refused.status, refused.body)
		}
		if after := e.state(e.a.ID); after != before {
			t.Errorf("%s committed while its audit event was refused:\nbefore %s\nafter  %s", action, before, after)
		}
		ok := req()
		if ok.status != want {
			t.Fatalf("%s: %d %s", action, ok.status, ok.body)
		}
		if n := e.count(`SELECT count(*) FROM twin.audit_events WHERE organization_id=$1 AND request_id=$2 AND action=$3`,
			e.a.ID, ok.header.Get("X-Request-ID"), action); n != 1 {
			t.Errorf("%s: %d events under its request", action, n)
		}
		if n := e.count(`SELECT count(*) FROM twin.audit_events WHERE organization_id=$1 AND request_id=$2`, e.a.ID, refused.header.Get("X-Request-ID")); n != 0 {
			t.Errorf("%s: %d events under the refused request", action, n)
		}
		return ok
	}
	key := func(name string) map[string]string {
		return map[string]string{"Idempotency-Key": "audit-" + name + "-0001"}
	}

	prj := decode[project](t, step("project.create", 201, func() resp { return e.create(owner.Subject, e.a.ID, "audit-prj-0001", `{"name":"Audited"}`) }))
	m := decode[modelSummary](t, step("model.create", 201, func() resp {
		return e.do("POST", "/v1/projects/"+prj.ID+"/models", editor.Subject, e.a.ID, key("mdl"), createModelBody)
	}))
	v1 := m.LatestVersion.ID
	step("model.draft.update", 200, func() resp {
		return e.patch(editor.Subject, m.ID, etag(1), `[{"op":"replace","path":"/name","value":"Renamed"}]`)
	})
	v2 := decode[versionBody](t, step("model.publish", 201, func() resp {
		return e.publish(editor.Subject, m.ID, "audit-pub-0001", etag(2), fmt.Sprintf(`{"expected_base_version_id":%q}`, v1))
	})).ID
	if r := e.patch(editor.Subject, m.ID, etag(3), `[{"op":"replace","path":"/name","value":"Scratch"}]`); r.status != 200 {
		t.Fatalf("patch: %d %s", r.status, r.body)
	}
	step("model.draft.reset", 200, func() resp {
		return e.do("POST", "/v1/models/"+m.ID+"/draft/reset", owner.Subject, e.a.ID, map[string]string{"If-Match": etag(4)},
			fmt.Sprintf(`{"expected_latest_version_id":%q,"confirm_discard":true}`, v2))
	})
	step("scenario.create", 201, func() resp { return e.saveScenario(editor.Subject, e.a.ID, v2, "audit-scn-0001", sixRunners) })
	run := decode[struct {
		ID string `json:"id"`
	}](t, step("simulation.create", 202, func() resp {
		return e.do("POST", "/v1/simulations", editor.Subject, e.a.ID, key("sim"), runRequest(v2, 1, "none", nil))
	})).ID
	step("simulation.cancel", 200, func() resp {
		return e.do("POST", "/v1/simulations/"+run+"/cancel", editor.Subject, e.a.ID, key("cancel"), "{}")
	})
	step("proposal.create", 202, func() resp {
		return e.propose(editor.Subject, e.a.ID, m.ID, "audit-aip-0001", proposalBody(v2, "edit", "Ask for nothing in particular."))
	})
	ready := e.readyOver(t, m.ID, v2, "edit", "Raise the CI runners to 6.", raiseCI)
	other := e.readyOver(t, m.ID, v2, "edit", "Raise the CI runners again.", raiseCI)
	step("proposal.reject", 200, func() resp {
		return e.decide(editor.Subject, e.a.ID, other.ProposalID, "reject", "audit-rej-0001", `{}`)
	})
	step("proposal.approve", 201, func() resp {
		return e.decide(editor.Subject, e.a.ID, ready.ProposalID, "approve", "audit-appr-0001", fmt.Sprintf(`{"expected_base_version_id":%q}`, v2))
	})
	step("membership.create", 201, func() resp {
		return e.do("POST", members, owner.Subject, e.a.ID, key("mem"), fmt.Sprintf(`{"user_id":%q,"role":"viewer"}`, stranger.UserID))
	})
	step("membership.update", 200, func() resp {
		return e.do("PATCH", members+"/"+stranger.UserID, owner.Subject, e.a.ID, nil, `{"role":"editor"}`)
	})
	step("membership.delete", 204, func() resp { return e.do("DELETE", members+"/"+stranger.UserID, owner.Subject, e.a.ID, nil, "") })
	step("import.upload", 202, func() resp {
		body, ct := multipartBody(t, fields(m, "csv_events", "events.csv", "case_id,event_type,timestamp\nC-1,start,2026-10-01T00:00:00Z\n"))
		req, err := http.NewRequest("POST", e.srv.URL+"/v1/imports", body)
		if err != nil {
			t.Fatal(err)
		}
		req.Header.Set("Authorization", "Bearer "+e.token(editor.Subject))
		req.Header.Set("X-Organization-ID", e.a.ID)
		req.Header.Set("Content-Type", ct)
		req.Header.Set("Idempotency-Key", "audit-imp-0001")
		return e.send(req)
	})
	// The model's latest version moved twice (publish, approval); nothing
	// else did.
	if got := decode[modelSummary](t, e.do("GET", "/v1/models/"+m.ID, editor.Subject, e.a.ID, nil, "")); got.LatestVersion.Version != 3 {
		t.Errorf("the model is at version %d", got.LatestVersion.Version)
	}
}
