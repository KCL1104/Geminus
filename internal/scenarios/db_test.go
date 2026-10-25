package scenarios_test

// DB-backed tests of the scenario service (T-028; TEST-003, TEST-007,
// TEST-008, TEST-009, TEST-010): models created through internal/models
// and scenarios saved, read and listed through this package as the plain
// API login, on a disposable database. Without GEMINUS_TEST_DATABASE_URL
// they skip (NOT RUN).

import (
	"bytes"
	"context"
	"encoding/json/v2"
	"errors"
	"io"
	"os"
	"path/filepath"
	"slices"
	"sync"
	"testing"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/kcl1104/geminus/internal/domainpack"
	"github.com/kcl1104/geminus/internal/domainpack/softwaredelivery"
	"github.com/kcl1104/geminus/internal/ids"
	"github.com/kcl1104/geminus/internal/model"
	"github.com/kcl1104/geminus/internal/model/hashing"
	"github.com/kcl1104/geminus/internal/model/patch"
	"github.com/kcl1104/geminus/internal/model/validate"
	"github.com/kcl1104/geminus/internal/models"
	"github.com/kcl1104/geminus/internal/scenarios"
	"github.com/kcl1104/geminus/internal/storage/dbtest"
	"github.com/kcl1104/geminus/internal/storage/idempotency"
	"github.com/kcl1104/geminus/internal/storage/objects"
	"github.com/kcl1104/geminus/internal/storage/postgres"
	"github.com/kcl1104/geminus/internal/tenant"
)

const issuer = "https://test.invalid"

type env struct {
	admin   *pgxpool.Pool
	org     dbtest.Org
	other   dbtest.Org
	project string
	models  *models.Service
	svc     *scenarios.Service
	objects *objects.FS
	root    string
}

func setup(t *testing.T) *env {
	t.Helper()
	admin := dbtest.Admin(t)
	org := dbtest.NewOrg(t, admin, issuer, tenant.Owner, tenant.Editor, tenant.Viewer)
	other := dbtest.NewOrg(t, admin, issuer, tenant.Owner, tenant.Editor)
	api := dbtest.Pool(t, dbtest.LoginURL(t, dbtest.APILogin), 16, true)
	if err := tenant.CheckRuntimeRole(context.Background(), api, tenant.APIRole); err != nil {
		t.Fatal(err)
	}
	reg, err := domainpack.NewRegistry(softwaredelivery.Pack{})
	if err != nil {
		t.Fatal(err)
	}
	root := t.TempDir()
	fs, err := objects.NewFS(root, "test")
	if err != nil {
		t.Fatal(err)
	}
	db := tenant.New(api)
	e := &env{admin: admin, org: org, other: other, objects: fs, root: root,
		models: &models.Service{DB: db, Packs: reg},
		svc:    &scenarios.Service{DB: db, Packs: reg, Objects: fs}}
	e.project = ids.New("prj")
	e.owner(t, org.ID, func(ctx context.Context, tx pgx.Tx) error {
		_, err := tx.Exec(ctx, `INSERT INTO twin.projects(id,organization_id,name,created_by) VALUES ($1,$2,'P',$3)`,
			e.project, org.ID, org.Members[tenant.Owner].UserID)
		return err
	})
	return e
}

func (e *env) scope(r tenant.Role) tenant.Scope {
	return tenant.Scope{OrganizationID: e.org.ID, UserID: e.org.Members[r].UserID}
}

// owner runs fn as the schema owner in organization org's scope.
func (e *env) owner(t *testing.T, org string, fn func(ctx context.Context, tx pgx.Tx) error) {
	t.Helper()
	if err := postgres.OpenPool(e.admin).Tx(context.Background(), pgx.TxOptions{}, func(ctx context.Context, tx pgx.Tx) error {
		if _, err := tx.Exec(ctx, `SET LOCAL ROLE twin_owner`); err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `SELECT set_config('app.organization_id',$1,true)`, org); err != nil {
			return err
		}
		return fn(ctx, tx)
	}); err != nil {
		t.Fatal(err)
	}
}

func (e *env) count(t *testing.T, sql string, args ...any) int {
	t.Helper()
	var n int
	if err := e.admin.QueryRow(context.Background(), sql, args...).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}

const createBody = `{"name":"Pipeline","domain":"software_delivery","template":"default_delivery_pipeline"}`

// model creates a model from the synthetic delivery template in the
// organization of scope sc: version 1 and a clean draft.
func (e *env) model(t *testing.T, sc tenant.Scope, project string) models.Model {
	t.Helper()
	var req models.CreateRequest
	if err := json.Unmarshal([]byte(createBody), &req); err != nil {
		t.Fatal(err)
	}
	status, out, err := e.models.Create(context.Background(), sc, ids.New("req"), "key-"+ids.New("k"), project, []byte(createBody), req)
	if err != nil || status != 201 {
		t.Fatalf("create: %d %v", status, err)
	}
	var m models.Model
	if err := json.Unmarshal(out, &m); err != nil {
		t.Fatal(err)
	}
	return m
}

// publish publishes the model's draft with its name changed as the next
// version and returns that version's ID.
func (e *env) publish(t *testing.T, m models.Model, name string) string {
	t.Helper()
	ctx := context.Background()
	d, err := e.models.GetDraft(ctx, e.scope(tenant.Editor), m.ID)
	if err != nil {
		t.Fatal(err)
	}
	d, err = e.models.PatchDraft(ctx, e.scope(tenant.Editor), ids.New("req"), m.ID, d.Revision, []byte(`[{"op":"replace","path":"/name","value":"`+name+`"}]`))
	if err != nil {
		t.Fatal(err)
	}
	req := models.PublishRequest{ExpectedBaseVersionID: d.BaseVersionID}
	body, _ := json.Marshal(req)
	status, out, err := e.models.Publish(ctx, e.scope(tenant.Editor), ids.New("req"), key(), m.ID, d.Revision, body, req)
	if err != nil || status != 201 {
		t.Fatalf("publish: %d %v", status, err)
	}
	var v struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal(out, &v); err != nil {
		t.Fatal(err)
	}
	return v.ID
}

func key() string { return "key-" + ids.New("k") }

func (e *env) create(sc tenant.Scope, k, version, body string) (int, []byte, error) {
	var req scenarios.CreateRequest
	if err := json.Unmarshal([]byte(body), &req); err != nil {
		return 0, nil, err
	}
	return e.svc.Create(context.Background(), sc, ids.New("req"), k, version, []byte(body), req)
}

func scenarioBody(name, patch string) string {
	return `{"name":` + quote(name) + `,"patch":` + patch + `}`
}

func quote(s string) string {
	data, _ := json.Marshal(s)
	return string(data)
}

const (
	raiseCI = `[{"op":"test","path":"/resources/1/id","value":"ci_runner"},{"op":"replace","path":"/resources/1/capacity","value":6}]`
	eightCI = `[{"op":"test","path":"/resources/1/id","value":"ci_runner"},{"op":"replace","path":"/resources/1/capacity","value":8}]`
)

func matches(t *testing.T, ref string, out []byte) {
	t.Helper()
	if issues, err := validate.Document(out, ref, "RESPONSE"); err != nil || len(issues) > 0 {
		t.Errorf("%s does not match %s: %v %v", out, ref, issues, err)
	}
}

// snapshot is what a scenario must never change: the model's versions and
// latest, its draft, and the organization's jobs and runs.
type snapshot struct {
	latest, draftHash   string
	draftRevision       int64
	versions, jobs, run int
}

func (e *env) snap(t *testing.T, modelID string) snapshot {
	t.Helper()
	var s snapshot
	if err := e.admin.QueryRow(context.Background(), `SELECT m.latest_version_id, d.hash, d.revision,
		  (SELECT count(*) FROM twin.model_versions v WHERE v.model_id=m.id),
		  (SELECT count(*) FROM twin.jobs j WHERE j.organization_id=m.organization_id),
		  (SELECT count(*) FROM twin.simulation_runs r WHERE r.organization_id=m.organization_id)
		FROM twin.models m JOIN twin.model_drafts d ON d.model_id=m.id WHERE m.id=$1`, modelID).
		Scan(&s.latest, &s.draftHash, &s.draftRevision, &s.versions, &s.jobs, &s.run); err != nil {
		t.Fatal(err)
	}
	return s
}

// REQ-010, TEST-008, D-004, D-067: an editor saves a scenario on version
// 1, which is no longer the latest: one immutable row for the editor with
// the canonical patch and its hash and no source proposal; no version,
// no draft change, no job and no run; one audit event without the name or
// the patch; the same Scenario from getScenario and in version 1's list
// only; the same key and body replay the answer and write nothing.
func TestCreateManualScenario(t *testing.T) {
	e := setup(t)
	ctx := context.Background()
	m := e.model(t, e.scope(tenant.Owner), e.project)
	v1 := m.LatestVersion.ID
	v2 := e.publish(t, m, "Pipeline v2")
	before := e.snap(t, m.ID)
	k := key()
	body := "{ \"patch\" : " + eightCI + ", \"name\":\"Eight CI runners\" }"
	status, out, err := e.create(e.scope(tenant.Editor), k, v1, body)
	if err != nil || status != 201 {
		t.Fatalf("create: %d %v", status, err)
	}
	matches(t, "scenario.schema.json", out)
	var s scenarios.Scenario
	if err := json.Unmarshal(out, &s); err != nil {
		t.Fatal(err)
	}
	canonical, _ := hashing.Canonical([]byte(eightCI))
	hash, _ := hashing.Patch(canonical)
	if s.BaseVersionID != v1 || s.ModelID != m.ID || s.OrganizationID != e.org.ID || s.Name != "Eight CI runners" || string(s.Patch) != string(canonical) ||
		s.PatchHash != hash || s.SourceProposalID != nil || s.CreatedBy != e.org.Members[tenant.Editor].UserID {
		t.Errorf("the scenario %+v", s)
	}
	if after := e.snap(t, m.ID); after != before || after.latest != v2 {
		t.Errorf("a scenario changed %+v into %+v", before, after)
	}
	var stored string
	if err := e.admin.QueryRow(ctx, `SELECT patch::text FROM twin.scenarios WHERE id=$1 AND organization_id=$2 AND base_version_id=$3
		AND patch_hash=$4 AND source_proposal_id IS NULL AND created_by=$5`, s.ID, e.org.ID, v1, hash, s.CreatedBy).Scan(&stored); err != nil {
		t.Fatalf("the stored row: %v", err)
	}
	if again, _ := hashing.Canonical([]byte(stored)); string(again) != string(canonical) {
		t.Errorf("stored %s", stored)
	}
	if n := e.count(t, `SELECT count(*) FROM twin.audit_events WHERE organization_id=$1 AND target_id=$2 AND action='scenario.create'
		AND target_type='scenario' AND actor_type='user' AND actor_id=$3 AND metadata->>'patch_hash'=$4 AND metadata->>'base_version_id'=$5
		AND metadata->>'model_id'=$6 AND (metadata->>'operations')::int=2 AND position('Eight' in metadata::text)=0
		AND position('capacity' in metadata::text)=0`, e.org.ID, s.ID, s.CreatedBy, hash, v1, m.ID); n != 1 {
		t.Errorf("%d scenario.create events", n)
	}
	got, err := e.svc.Get(ctx, e.scope(tenant.Viewer), s.ID)
	if err != nil {
		t.Fatal(err)
	}
	data, _ := json.Marshal(got)
	if c, _ := hashing.Canonical(data); !bytes.Equal(c, out) {
		t.Errorf("getScenario %s, created %s", c, out)
	}
	page, err := e.svc.List(ctx, e.scope(tenant.Viewer), v1, "", 50)
	if err != nil || len(page.Items) != 1 || page.Items[0].ID != s.ID || page.NextCursor != nil {
		t.Errorf("version 1's scenarios: %+v %v", page, err)
	}
	if page, err := e.svc.List(ctx, e.scope(tenant.Viewer), v2, "", 50); err != nil || len(page.Items) != 0 {
		t.Errorf("version 2's scenarios: %+v %v", page, err)
	}
	// D-011: the same key and body (any spelling) replay; nothing more.
	for _, b := range []string{body, scenarioBody("Eight CI runners", eightCI)} {
		again, out2, err := e.create(e.scope(tenant.Editor), k, v1, b)
		if err != nil || again != 201 || !bytes.Equal(out, out2) {
			t.Errorf("replay: %d %v %s", again, err, out2)
		}
	}
	if n := e.count(t, `SELECT count(*) FROM twin.scenarios WHERE model_id=$1`, m.ID); n != 1 {
		t.Errorf("%d scenarios after the replays", n)
	}
	if n := e.count(t, `SELECT count(*) FROM twin.audit_events WHERE organization_id=$1 AND action='scenario.create'`, e.org.ID); n != 1 {
		t.Errorf("the replays audited again: %d", n)
	}
	// [] saves a baseline, on the latest version this time (D-078 (b)).
	status, out, err = e.create(e.scope(tenant.Owner), key(), v2, scenarioBody("Baseline", `[]`))
	if err != nil || status != 201 {
		t.Fatalf("baseline: %d %v", status, err)
	}
	if err := json.Unmarshal(out, &s); err != nil || string(s.Patch) != "[]" || s.BaseVersionID != v2 || s.CreatedBy != e.org.Members[tenant.Owner].UserID {
		t.Errorf("the baseline %s %v", out, err)
	}
	if after := e.snap(t, m.ID); after != before {
		t.Errorf("a baseline changed %+v into %+v", before, after)
	}
}

// TEST-003, D-020, D-021: every refusal is typed, points into the request
// or the patched model, and writes nothing: no scenario, no audit event,
// no claimed key (the same key then works).
func TestCreateRefusals(t *testing.T) {
	e := setup(t)
	m := e.model(t, e.scope(tenant.Owner), e.project)
	v := m.LatestVersion.ID
	before := e.snap(t, m.ID)
	k := key()
	var re *scenarios.RequestError
	var ie *scenarios.InvalidError
	for _, c := range []struct {
		name, version, body string
		check               func(error) bool
	}{
		{"a name of only white space", v, scenarioBody(" \t ", raiseCI), func(err error) bool {
			return errors.As(err, &re) && re.Issues[0].Path == "/name" && re.Issues[0].Code == validate.ConstraintFailed
		}},
		{"removing a node", v, scenarioBody("x", `[{"op":"test","path":"/nodes/5/id","value":"ci_rework"},{"op":"remove","path":"/nodes/5"}]`), func(err error) bool {
			return errors.As(err, &ie) && ie.Issues[0].Code == patch.PathForbidden && ie.Issues[0].Path == "/patch/1/path"
		}},
		{"no test", v, scenarioBody("x", `[{"op":"replace","path":"/resources/1/capacity","value":6}]`), func(err error) bool {
			return errors.As(err, &ie) && ie.Issues[0].Code == patch.TestRequired && ie.Issues[0].Path == "/patch/0/path"
		}},
		{"a stale test", v, scenarioBody("x", `[{"op":"test","path":"/resources/1/id","value":"reviewer"},{"op":"replace","path":"/resources/1/capacity","value":6}]`), func(err error) bool {
			return errors.As(err, &ie) && ie.Issues[0].Code == patch.TestFailed && ie.Issues[0].Path == "/patch/0/value"
		}},
		{"an invalid model", v, scenarioBody("x", `[{"op":"test","path":"/flows/3/id","value":"f_pass"},{"op":"replace","path":"/flows/3/routing/probability","value":0.5}]`), func(err error) bool {
			return errors.As(err, &ie) && ie.Issues[0].Code == validate.RoutingInvalid && ie.Issues[0].Path == "/nodes/3"
		}},
		{"a new dataset", v, scenarioBody("x", `[{"op":"test","path":"/nodes/2/id","value":"ci"},{"op":"replace","path":"/nodes/2/service_time/distribution","value":{"type":"empirical","samples_ref":"dataset:invented"}}]`), func(err error) bool {
			return errors.As(err, &ie) && ie.Issues[0].Code == patch.PathForbidden && ie.Issues[0].Path == "/patch/1/value"
		}},
		{"no such version", ids.New("mv"), scenarioBody("x", raiseCI), func(err error) bool { return errors.Is(err, scenarios.ErrNotFound) }},
	} {
		if _, _, err := e.create(e.scope(tenant.Editor), k, c.version, c.body); !c.check(err) {
			t.Errorf("%s: %v", c.name, err)
		}
	}
	if s := e.snap(t, m.ID); s != before {
		t.Errorf("refusals changed %+v into %+v", before, s)
	}
	if n := e.count(t, `SELECT count(*) FROM twin.scenarios WHERE organization_id=$1`, e.org.ID); n != 0 {
		t.Errorf("%d scenarios after refusals", n)
	}
	if n := e.count(t, `SELECT count(*) FROM twin.audit_events WHERE organization_id=$1 AND action='scenario.create'`, e.org.ID); n != 0 {
		t.Errorf("%d audit events after refusals", n)
	}
	if n := e.count(t, `SELECT count(*) FROM twin.idempotency_keys WHERE organization_id=$1 AND key=$2`, e.org.ID, k); n != 0 {
		t.Errorf("a refused request kept its key")
	}
	if status, _, err := e.create(e.scope(tenant.Editor), k, v, scenarioBody("Now valid", raiseCI)); err != nil || status != 201 {
		t.Errorf("the key of refused requests: %d %v", status, err)
	}
}

// TEST-009: editors and owners save scenarios, viewers read them; another
// organization sees none of them and cannot use this organization's
// versions; a role taken away before a replay refuses it (D-011).
func TestScenarioRoles(t *testing.T) {
	e := setup(t)
	ctx := context.Background()
	m := e.model(t, e.scope(tenant.Owner), e.project)
	v := m.LatestVersion.ID
	if _, _, err := e.create(e.scope(tenant.Viewer), key(), v, scenarioBody("x", raiseCI)); !errors.Is(err, tenant.ErrForbidden) {
		t.Errorf("a viewer creates: %v", err)
	}
	k := key()
	_, out, err := e.create(e.scope(tenant.Editor), k, v, scenarioBody("Six CI runners", raiseCI))
	if err != nil {
		t.Fatal(err)
	}
	var s scenarios.Scenario
	if err := json.Unmarshal(out, &s); err != nil {
		t.Fatal(err)
	}
	for _, r := range []tenant.Role{tenant.Owner, tenant.Editor, tenant.Viewer} {
		if got, err := e.svc.Get(ctx, e.scope(r), s.ID); err != nil || got.ID != s.ID {
			t.Errorf("%s reads: %v", r, err)
		}
		if page, err := e.svc.List(ctx, e.scope(r), v, "", 10); err != nil || len(page.Items) != 1 {
			t.Errorf("%s lists: %+v %v", r, page, err)
		}
	}
	// Another organization's member, in its own organization: none of a's.
	theirs := tenant.Scope{OrganizationID: e.other.ID, UserID: e.other.Members[tenant.Owner].UserID}
	if _, err := e.svc.Get(ctx, theirs, s.ID); !errors.Is(err, scenarios.ErrNotFound) {
		t.Errorf("another organization reads: %v", err)
	}
	if _, err := e.svc.List(ctx, theirs, v, "", 10); !errors.Is(err, scenarios.ErrNotFound) {
		t.Errorf("another organization lists: %v", err)
	}
	if _, _, err := e.create(theirs, key(), v, scenarioBody("x", raiseCI)); !errors.Is(err, scenarios.ErrNotFound) {
		t.Errorf("another organization saves on a's version: %v", err)
	}
	// The same member naming organization a, which it is no member of.
	stranger := tenant.Scope{OrganizationID: e.org.ID, UserID: e.other.Members[tenant.Owner].UserID}
	if _, err := e.svc.Get(ctx, stranger, s.ID); !errors.Is(err, tenant.ErrNoMembership) {
		t.Errorf("a non-member reads: %v", err)
	}
	if _, _, err := e.create(stranger, key(), v, scenarioBody("x", raiseCI)); !errors.Is(err, tenant.ErrNoMembership) {
		t.Errorf("a non-member creates: %v", err)
	}
	// The editor, demoted, gets no replay of its earlier answer.
	if _, err := e.admin.Exec(ctx, `UPDATE twin.memberships SET role='viewer' WHERE organization_id=$1 AND user_id=$2`,
		e.org.ID, e.org.Members[tenant.Editor].UserID); err != nil {
		t.Fatal(err)
	}
	if _, _, err := e.create(e.scope(tenant.Editor), k, v, scenarioBody("Six CI runners", raiseCI)); !errors.Is(err, tenant.ErrForbidden) {
		t.Errorf("a demoted editor's replay: %v", err)
	}
	if n := e.count(t, `SELECT count(*) FROM twin.scenarios WHERE organization_id IN ($1,$2)`, e.org.ID, e.other.ID); n != 1 {
		t.Errorf("%d scenarios", n)
	}
}

// TEST-007, D-011, D-079 (b): one key, one effect. Another body or another
// version with the key is a conflict; concurrent requests with one key
// make one scenario and get one answer; concurrent requests with their
// own keys each make theirs.
func TestScenarioIdempotency(t *testing.T) {
	e := setup(t)
	m := e.model(t, e.scope(tenant.Owner), e.project)
	v1 := m.LatestVersion.ID
	v2 := e.publish(t, m, "Pipeline v2")
	k := key()
	body := scenarioBody("Six CI runners", raiseCI)
	if status, _, err := e.create(e.scope(tenant.Editor), k, v1, body); err != nil || status != 201 {
		t.Fatalf("%d %v", status, err)
	}
	if _, _, err := e.create(e.scope(tenant.Editor), k, v1, scenarioBody("Eight CI runners", eightCI)); !errors.Is(err, idempotency.ErrConflict) {
		t.Errorf("another body: %v", err)
	}
	if _, _, err := e.create(e.scope(tenant.Editor), k, v2, body); !errors.Is(err, idempotency.ErrConflict) {
		t.Errorf("the same body on another version: %v", err)
	}
	// Keys are the caller's own: the owner's same key is another request.
	if status, _, err := e.create(e.scope(tenant.Owner), k, v1, body); err != nil || status != 201 {
		t.Errorf("another user's key: %d %v", status, err)
	}

	k2 := key()
	const n = 8
	outs := make([][]byte, n)
	errs := make([]error, n)
	var wg sync.WaitGroup
	for i := range n {
		wg.Go(func() {
			_, outs[i], errs[i] = e.create(e.scope(tenant.Editor), k2, v2, scenarioBody("Eight CI runners", eightCI))
		})
	}
	wg.Wait()
	for i := range n {
		if errs[i] != nil || !bytes.Equal(outs[i], outs[0]) {
			t.Errorf("request %d: %v %s", i, errs[i], outs[i])
		}
	}
	if c := e.count(t, `SELECT count(*) FROM twin.scenarios WHERE base_version_id=$1`, v2); c != 1 {
		t.Errorf("%d scenarios from one key", c)
	}
	for i := range n {
		wg.Go(func() { _, _, errs[i] = e.create(e.scope(tenant.Editor), key(), v2, scenarioBody("Mine", raiseCI)) })
	}
	wg.Wait()
	for i := range n {
		if errs[i] != nil {
			t.Errorf("request %d with its own key: %v", i, errs[i])
		}
	}
	if c := e.count(t, `SELECT count(*) FROM twin.scenarios WHERE base_version_id=$1`, v2); c != 1+n {
		t.Errorf("%d scenarios, want %d", c, 1+n)
	}
}

// API_DATABASE §3: pages of at most limit, oldest first, continued by a
// cursor bound to the organization and the version.
func TestListPages(t *testing.T) {
	e := setup(t)
	ctx := context.Background()
	m := e.model(t, e.scope(tenant.Owner), e.project)
	v1 := m.LatestVersion.ID
	v2 := e.publish(t, m, "Pipeline v2")
	var created []string
	for _, name := range []string{"one", "two", "three"} {
		_, out, err := e.create(e.scope(tenant.Editor), key(), v1, scenarioBody(name, raiseCI))
		if err != nil {
			t.Fatal(err)
		}
		var s scenarios.Scenario
		if err := json.Unmarshal(out, &s); err != nil {
			t.Fatal(err)
		}
		created = append(created, s.ID)
	}
	first, err := e.svc.List(ctx, e.scope(tenant.Viewer), v1, "", 2)
	if err != nil || len(first.Items) != 2 || first.NextCursor == nil {
		t.Fatalf("the first page: %+v %v", first, err)
	}
	data, _ := json.Marshal(first)
	matches(t, "http.schema.json#/$defs/Scenarios", data)
	second, err := e.svc.List(ctx, e.scope(tenant.Viewer), v1, *first.NextCursor, 2)
	if err != nil || len(second.Items) != 1 || second.NextCursor != nil {
		t.Fatalf("the second page: %+v %v", second, err)
	}
	var got []string
	for _, s := range append(first.Items, second.Items...) {
		got = append(got, s.ID)
	}
	if !slices.Equal(got, created) {
		t.Errorf("listed %v, created %v", got, created)
	}
	if _, err := e.svc.List(ctx, e.scope(tenant.Viewer), v2, *first.NextCursor, 2); !errors.Is(err, scenarios.ErrInvalidCursor) {
		t.Errorf("version 1's cursor on version 2: %v", err)
	}
	theirs := tenant.Scope{OrganizationID: e.other.ID, UserID: e.other.Members[tenant.Owner].UserID}
	if _, err := e.svc.List(ctx, theirs, v1, *first.NextCursor, 2); !errors.Is(err, scenarios.ErrInvalidCursor) {
		t.Errorf("a's cursor in another organization: %v", err)
	}
	for _, limit := range []int{0, 101} {
		if _, err := e.svc.List(ctx, e.scope(tenant.Viewer), v1, "", limit); !errors.Is(err, scenarios.ErrInvalidLimit) {
			t.Errorf("limit %d: %v", limit, err)
		}
	}
}

// D-006, D-033, D-078 (a): on a base that binds datasets, the binding
// stage runs with the snapshots read again: without object storage 503,
// storage that fails 503 with the cause kept out, a snapshot that no
// longer verifies 422; when they verify, the scenario is saved.
func TestScenarioDatasets(t *testing.T) {
	e := setup(t)
	m := e.model(t, e.scope(tenant.Owner), e.project)
	base, binding := e.datasetVersion(t, m)
	body := scenarioBody("Six CI runners", raiseCI)

	noStorage := *e.svc
	noStorage.Objects = nil
	var req scenarios.CreateRequest
	_ = json.Unmarshal([]byte(body), &req)
	if _, _, err := noStorage.Create(context.Background(), e.scope(tenant.Editor), ids.New("req"), key(), base, []byte(body), req); !errors.Is(err, scenarios.ErrStorageUnavailable) {
		t.Errorf("without object storage: %v", err)
	}
	broken := *e.svc
	broken.Objects = brokenObjects{e.objects}
	var storage *scenarios.DatasetStorageError
	if _, _, err := broken.Create(context.Background(), e.scope(tenant.Editor), ids.New("req"), key(), base, []byte(body), req); !errors.As(err, &storage) {
		t.Errorf("object storage that fails: %v", err)
	}
	objKey, _ := objects.NewKey(e.org.ID, objects.Datasets, binding.DatasetVersionID+".json")
	path := filepath.Join(e.root, filepath.FromSlash(objKey))
	original, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(`{"tampered":true}`), 0o600); err != nil {
		t.Fatal(err)
	}
	var ie *scenarios.InvalidError
	if _, _, err := e.create(e.scope(tenant.Editor), key(), base, body); !errors.As(err, &ie) ||
		!slices.ContainsFunc(ie.Issues, func(is model.ValidationIssue) bool { return is.Code == validate.DatasetBindingInvalid }) {
		t.Errorf("a snapshot that no longer verifies: %v", err)
	}
	if n := e.count(t, `SELECT count(*) FROM twin.scenarios WHERE organization_id=$1`, e.org.ID); n != 0 {
		t.Errorf("%d scenarios after refusals", n)
	}
	if err := os.WriteFile(path, original, 0o600); err != nil {
		t.Fatal(err)
	}
	// The empirical review time becomes a constant: the base's binding is
	// still checked (and still frozen by a run of this scenario).
	constant := `[{"op":"test","path":"/nodes/1/id","value":"code_review"},{"op":"replace","path":"/nodes/1/service_time/distribution","value":{"type":"constant","value":30}}]`
	for _, p := range []string{raiseCI, constant} {
		if status, _, err := e.create(e.scope(tenant.Editor), key(), base, scenarioBody("Saved", p)); err != nil || status != 201 {
			t.Errorf("%s: %d %v", p, status, err)
		}
	}
}

// brokenObjects is object storage whose reads fail.
type brokenObjects struct{ objects.Store }

func (brokenObjects) Get(context.Context, string) (io.ReadCloser, error) {
	return nil, errors.New("the object store did not answer")
}

// datasetVersion gives model m a version 2 whose code review takes an
// empirical service time from a dataset snapshot in object storage, and
// returns the version and its binding.
func (e *env) datasetVersion(t *testing.T, m models.Model) (string, model.DatasetBinding) {
	t.Helper()
	ctx := context.Background()
	var v1 string
	e.owner(t, e.org.ID, func(ctx context.Context, tx pgx.Tx) error {
		return tx.QueryRow(ctx, `SELECT ir FROM twin.model_versions WHERE id=$1`, m.LatestVersion.ID).Scan(&v1)
	})
	var doc map[string]any
	if err := json.Unmarshal([]byte(v1), &doc); err != nil {
		t.Fatal(err)
	}
	nodes := doc["nodes"].([]any)
	nodes[1].(map[string]any)["service_time"] = map[string]any{"distribution": map[string]any{"type": "empirical", "samples_ref": "dataset:review_minutes"}}
	raw, _ := json.Marshal(doc)
	ir, err := hashing.Canonical(raw)
	if err != nil {
		t.Fatal(err)
	}
	ds, dsv, mv := ids.New("ds"), ids.New("dsv"), ids.New("mv")
	snap, _ := json.Marshal(map[string]any{"dataset_version_id": dsv, "organization_id": e.org.ID, "logical_ref": "dataset:review_minutes",
		"unit": "minute", "samples": []float64{20, 30, 45}})
	objKey, err := objects.NewKey(e.org.ID, objects.Datasets, dsv+".json")
	if err != nil {
		t.Fatal(err)
	}
	obj, err := e.objects.Put(ctx, objKey, bytes.NewReader(snap), 1<<20)
	if err != nil {
		t.Fatal(err)
	}
	b := model.DatasetBinding{LogicalRef: "dataset:review_minutes", DatasetVersionID: dsv, ContentHash: obj.SHA256, Unit: "minute", SampleCount: 3}
	manifest, _ := json.Marshal([]model.DatasetBinding{b})
	owner := e.org.Members[tenant.Owner].UserID
	e.owner(t, e.org.ID, func(ctx context.Context, tx pgx.Tx) error {
		for _, q := range []struct {
			sql  string
			args []any
		}{
			{`INSERT INTO twin.datasets(id,organization_id,project_id,kind,name,created_by) VALUES ($1,$2,$3,'empirical_samples','review minutes',$4)`,
				[]any{ds, e.org.ID, e.project, owner}},
			{`INSERT INTO twin.dataset_versions(id,organization_id,dataset_id,version,object_key,content_hash,content_type,row_count,unit,created_by)
				VALUES ($1,$2,$3,1,$4,$5,'application/json',3,'minute',$6)`, []any{dsv, e.org.ID, ds, objKey, obj.SHA256, owner}},
			{`INSERT INTO twin.model_versions(id,organization_id,model_id,version,schema_version,parent_version_id,ir,hash,dataset_bindings,created_by)
				VALUES ($1,$2,$3,2,'0.1',$4,$5,$6,$7,$8)`, []any{mv, e.org.ID, m.ID, m.LatestVersion.ID, string(ir), hashing.Sum(ir), string(manifest), owner}},
			{`INSERT INTO twin.model_version_datasets(organization_id,model_id,model_version_id,logical_ref,dataset_version_id,content_hash,unit,sample_count)
				VALUES ($1,$2,$3,$4,$5,$6,'minute',3)`, []any{e.org.ID, m.ID, mv, b.LogicalRef, dsv, obj.SHA256}},
			{`UPDATE twin.models SET latest_version_id=$1 WHERE id=$2`, []any{mv, m.ID}},
		} {
			if _, err := tx.Exec(ctx, q.sql, q.args...); err != nil {
				return err
			}
		}
		return nil
	})
	return mv, b
}
