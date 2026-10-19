package models_test

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json/v2"
	"errors"
	"fmt"
	"io"
	"slices"
	"strings"
	"sync"
	"testing"
	"unicode/utf8"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/kcl1104/geminus/internal/domainpack"
	"github.com/kcl1104/geminus/internal/domainpack/softwaredelivery"
	"github.com/kcl1104/geminus/internal/ids"
	"github.com/kcl1104/geminus/internal/model"
	"github.com/kcl1104/geminus/internal/model/hashing"
	"github.com/kcl1104/geminus/internal/model/validate"
	"github.com/kcl1104/geminus/internal/models"
	"github.com/kcl1104/geminus/internal/storage/datasets"
	"github.com/kcl1104/geminus/internal/storage/dbtest"
	"github.com/kcl1104/geminus/internal/storage/idempotency"
	"github.com/kcl1104/geminus/internal/storage/postgres"
	"github.com/kcl1104/geminus/internal/tenant"
)

const issuer = "https://test.invalid"

type env struct {
	svc     *models.Service
	admin   *pgxpool.Pool
	org     dbtest.Org
	project string
}

func setup(t *testing.T) *env {
	t.Helper()
	admin := dbtest.Admin(t)
	pool := dbtest.Pool(t, dbtest.LoginURL(t, dbtest.APILogin), 8, true)
	reg, err := domainpack.NewRegistry(softwaredelivery.Pack{})
	if err != nil {
		t.Fatal(err)
	}
	e := &env{svc: &models.Service{DB: tenant.New(pool), Packs: reg}, admin: admin,
		org: dbtest.NewOrg(t, admin, issuer, tenant.Owner, tenant.Editor, tenant.Viewer)}
	e.project = e.newProject(t, e.org)
	return e
}

func (e *env) newProject(t *testing.T, org dbtest.Org) string {
	t.Helper()
	id := ids.New("prj")
	err := postgres.OpenPool(e.admin).Tx(context.Background(), pgx.TxOptions{}, func(ctx context.Context, tx pgx.Tx) error {
		_, err := tx.Exec(ctx, `INSERT INTO twin.projects(id,organization_id,name,created_by) VALUES ($1,$2,'P',$3)`, id, org.ID, org.Members[tenant.Owner].UserID)
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	return id
}

func (e *env) scope(r tenant.Role) tenant.Scope {
	return tenant.Scope{OrganizationID: e.org.ID, UserID: e.org.Members[r].UserID}
}

func requestID() string { return ids.New("req") }

func key() string { return "key-" + ids.New("k") }

// matches checks out against an http.schema.json definition.
func matches(t *testing.T, def string, out []byte) {
	t.Helper()
	issues, err := validate.Document(out, "http.schema.json#/$defs/"+def, "SCHEMA")
	if err != nil || len(issues) > 0 {
		t.Errorf("%s does not match the contract: %v %v\n%s", def, err, issues, out)
	}
}

func mustJSON(t *testing.T, v any) []byte {
	t.Helper()
	data, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	return data
}

const createBody = `{"name":"Pipeline","domain":"software_delivery","template":"default_delivery_pipeline"}`

func (e *env) create(t *testing.T, sc tenant.Scope, body string) models.Model {
	t.Helper()
	var req models.CreateRequest
	if err := json.Unmarshal([]byte(body), &req); err != nil {
		t.Fatal(err)
	}
	status, out, err := e.svc.Create(context.Background(), sc, requestID(), key(), e.project, []byte(body), req)
	if err != nil || status != 201 {
		t.Fatalf("create: %d %v", status, err)
	}
	matches(t, "Model", out)
	var m models.Model
	if err := json.Unmarshal(out, &m); err != nil {
		t.Fatal(err)
	}
	return m
}

func (e *env) publish(sc tenant.Scope, modelID string, revision int64, base, k string) (int, []byte, error) {
	body := fmt.Sprintf(`{"expected_base_version_id":%q}`, base)
	return e.svc.Publish(context.Background(), sc, requestID(), k, modelID, revision, []byte(body), models.PublishRequest{ExpectedBaseVersionID: base})
}

// rename is a manual-draft patch that changes the model name.
func rename(name string) []byte {
	return fmt.Appendf(nil, `[{"op":"replace","path":"/name","value":%q}]`, name)
}

// Create builds version 1 and draft revision 1 from the template in one
// transaction, writes its audit event, and is idempotent per key.
func TestCreate(t *testing.T) {
	e := setup(t)
	ctx := context.Background()
	sc := e.scope(tenant.Editor)
	var req models.CreateRequest
	if err := json.Unmarshal([]byte(createBody), &req); err != nil {
		t.Fatal(err)
	}
	k, rid := key(), requestID()
	status, out, err := e.svc.Create(ctx, sc, rid, k, e.project, []byte(createBody), req)
	if err != nil || status != 201 {
		t.Fatalf("%d %v", status, err)
	}
	matches(t, "Model", out)
	var m models.Model
	if err := json.Unmarshal(out, &m); err != nil {
		t.Fatal(err)
	}
	if m.ProjectID != e.project || m.Name != "Pipeline" || m.Domain != "software_delivery" || m.LatestVersion.Version != 1 ||
		m.Draft.Revision != 1 || m.Draft.BaseVersionID != m.LatestVersion.ID || m.Draft.OutOfDate || m.OrganizationID != e.org.ID {
		t.Errorf("model %+v", m)
	}
	v, err := e.svc.GetVersion(ctx, sc, m.LatestVersion.ID)
	if err != nil {
		t.Fatal(err)
	}
	matches(t, "ModelVersion", mustJSON(t, v))
	d, err := e.svc.GetDraft(ctx, sc, m.ID)
	if err != nil {
		t.Fatal(err)
	}
	matches(t, "Draft", mustJSON(t, d))
	if v.ParentVersionID != nil || v.Version != 1 || len(v.DatasetBindings) != 0 || d.Hash != v.Hash || !bytes.Equal(d.IR, v.IR) {
		t.Errorf("v1 %+v draft hash %s", v, d.Hash)
	}
	if slices.ContainsFunc(d.Issues, func(i model.ValidationIssue) bool { return i.Severity == model.SeverityError }) {
		t.Errorf("the template draft has errors: %v", d.Issues)
	}
	var ir model.ModelIR
	if err := model.Decode(v.IR, &ir); err != nil || ir.ModelID != m.ID || ir.Name != "Pipeline" {
		t.Errorf("v1 IR %v %+v", err, ir.ModelID)
	}
	if got := e.audit(t, rid); len(got) != 1 || got[0] != "model.create model "+m.ID {
		t.Errorf("audit %v", got)
	}
	// The same key and body replay the same response; another body is a conflict.
	status2, out2, err := e.svc.Create(ctx, sc, requestID(), k, e.project, []byte(" "+createBody), req)
	if err != nil || status2 != 201 || !bytes.Equal(out, out2) {
		t.Errorf("replay: %d %v %s", status2, err, out2)
	}
	other := strings.Replace(createBody, "Pipeline", "Other", 1)
	if _, _, err := e.svc.Create(ctx, sc, requestID(), k, e.project, []byte(other), req); !errors.Is(err, idempotency.ErrConflict) {
		t.Errorf("same key, other body: %v", err)
	}
	// The same key for another project is another request.
	p2 := e.newProject(t, e.org)
	if _, _, err := e.svc.Create(ctx, sc, requestID(), k, p2, []byte(createBody), req); !errors.Is(err, idempotency.ErrConflict) {
		t.Errorf("same key and body, other project: %v", err)
	}
	// Traditional Chinese default names (REQ-022): same IDs, other names.
	zh := e.create(t, sc, `{"name":"流程","domain":"software_delivery","template":"default_delivery_pipeline","locale":"zh-Hant"}`)
	zv, err := e.svc.GetVersion(ctx, sc, zh.LatestVersion.ID)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Contains(zv.IR, []byte("審查者")) || !bytes.Contains(zv.IR, []byte(`"id":"reviewer"`)) {
		t.Errorf("zh-Hant template: %s", zv.IR)
	}
}

// audit lists the audit events of one request as "action type target".
func (e *env) audit(t *testing.T, requestID string) []string {
	t.Helper()
	var out []string
	err := postgres.OpenPool(e.admin).Tx(context.Background(), pgx.TxOptions{AccessMode: pgx.ReadOnly}, func(ctx context.Context, tx pgx.Tx) error {
		rows, err := tx.Query(ctx, `SELECT action||' '||target_type||' '||target_id FROM twin.audit_events WHERE organization_id=$1 AND request_id=$2 ORDER BY created_at`, e.org.ID, requestID)
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			var s string
			if err := rows.Scan(&s); err != nil {
				return err
			}
			out = append(out, s)
		}
		return rows.Err()
	})
	if err != nil {
		t.Fatal(err)
	}
	return out
}

// Roles and tenancy: viewers read but do not write; other organizations'
// models and projects are not found; non-members are refused.
func TestRolesAndTenancy(t *testing.T) {
	e := setup(t)
	ctx := context.Background()
	m := e.create(t, e.scope(tenant.Owner), createBody)
	viewer := e.scope(tenant.Viewer)
	var req models.CreateRequest
	_ = json.Unmarshal([]byte(createBody), &req)
	if _, _, err := e.svc.Create(ctx, viewer, requestID(), key(), e.project, []byte(createBody), req); !errors.Is(err, tenant.ErrForbidden) {
		t.Errorf("viewer create: %v", err)
	}
	if _, err := e.svc.PatchDraft(ctx, viewer, requestID(), m.ID, 1, rename("x")); !errors.Is(err, tenant.ErrForbidden) {
		t.Errorf("viewer patch: %v", err)
	}
	if _, _, err := e.publish(viewer, m.ID, 1, m.LatestVersion.ID, key()); !errors.Is(err, tenant.ErrForbidden) {
		t.Errorf("viewer publish: %v", err)
	}
	if _, err := e.svc.ResetDraft(ctx, viewer, requestID(), m.ID, 1, models.ResetRequest{ExpectedLatestVersionID: m.LatestVersion.ID, ConfirmDiscard: true}); !errors.Is(err, tenant.ErrForbidden) {
		t.Errorf("viewer reset: %v", err)
	}
	if _, err := e.svc.GetModel(ctx, viewer, m.ID); err != nil {
		t.Errorf("viewer read: %v", err)
	}
	other := dbtest.NewOrg(t, e.admin, issuer, tenant.Owner)
	os := tenant.Scope{OrganizationID: other.ID, UserID: other.Members[tenant.Owner].UserID}
	for name, err := range map[string]error{
		"get":      func() error { _, err := e.svc.GetModel(ctx, os, m.ID); return err }(),
		"draft":    func() error { _, err := e.svc.GetDraft(ctx, os, m.ID); return err }(),
		"version":  func() error { _, err := e.svc.GetVersion(ctx, os, m.LatestVersion.ID); return err }(),
		"versions": func() error { _, err := e.svc.ListVersions(ctx, os, m.ID, "", 10); return err }(),
		"models":   func() error { _, err := e.svc.ListModels(ctx, os, e.project, "", 10); return err }(),
		"patch":    func() error { _, err := e.svc.PatchDraft(ctx, os, requestID(), m.ID, 1, rename("x")); return err }(),
		"publish":  func() error { _, _, err := e.publish(os, m.ID, 1, m.LatestVersion.ID, key()); return err }(),
		"create": func() error {
			_, _, err := e.svc.Create(ctx, os, requestID(), key(), e.project, []byte(createBody), req)
			return err
		}(),
	} {
		if !errors.Is(err, models.ErrNotFound) {
			t.Errorf("%s in another organization: %v", name, err)
		}
	}
	stranger := dbtest.NewUser(t, e.admin, issuer)
	if _, err := e.svc.GetModel(ctx, tenant.Scope{OrganizationID: e.org.ID, UserID: stranger.UserID}, m.ID); !errors.Is(err, tenant.ErrNoMembership) {
		t.Errorf("non-member: %v", err)
	}
}

// A patch applies only at the revision the caller read; a schema-invalid
// result is refused and nothing is saved; a schema-valid one is saved with
// its semantic issues (D-028), and its audit event is written.
func TestPatchDraft(t *testing.T) {
	e := setup(t)
	ctx := context.Background()
	sc := e.scope(tenant.Editor)
	m := e.create(t, sc, createBody)
	rid := requestID()
	d, err := e.svc.PatchDraft(ctx, sc, rid, m.ID, 1, rename("Renamed"))
	if err != nil {
		t.Fatal(err)
	}
	matches(t, "Draft", mustJSON(t, d))
	if d.Revision != 2 || !bytes.Contains(d.IR, []byte(`"name":"Renamed"`)) || d.OutOfDate || d.BaseVersionID != m.LatestVersion.ID {
		t.Errorf("draft %+v", d)
	}
	if got := e.audit(t, rid); len(got) != 1 || got[0] != "model.draft.update model "+m.ID {
		t.Errorf("audit %v", got)
	}
	// A person may move the 2D layout; an AI edit may not (TWIN_IR_SPEC §6).
	moved, err := e.svc.PatchDraft(ctx, sc, requestID(), m.ID, 2, []byte(`[{"op":"replace","path":"/views/2d/positions/ci","value":{"x":410,"y":120}}]`))
	if err != nil || moved.Revision != 3 || !bytes.Contains(moved.IR, []byte(`"ci":{"x":410,"y":120}`)) {
		t.Fatalf("moving a node in the 2D view: %v", err)
	}
	d = moved
	if _, err := e.svc.PatchDraft(ctx, sc, requestID(), m.ID, 1, rename("Stale")); !errors.Is(err, models.ErrConflict) {
		t.Errorf("stale revision: %v", err)
	}
	var invalid *models.InvalidError
	for name, doc := range map[string]string{
		"not a patch":      `{"op":"replace"}`,
		"forbidden path":   `[{"op":"replace","path":"/model_id","value":"mdl_01M4YDQK00TA00000000000077"}]`,
		"no id test":       `[{"op":"replace","path":"/nodes/1/name","value":"Review"}]`,
		"schema-invalid":   `[{"op":"replace","path":"/name","value":""}]`,
		"missing target":   `[{"op":"remove","path":"/metadata/nothing"}]`,
		"test that fails":  `[{"op":"test","path":"/name","value":"Other"}]`,
		"wrong value type": `[{"op":"replace","path":"/time_unit","value":"week"}]`,
	} {
		if _, err := e.svc.PatchDraft(ctx, sc, requestID(), m.ID, 3, []byte(doc)); !errors.As(err, &invalid) || len(invalid.Issues) == 0 {
			t.Errorf("%s: %v", name, err)
		}
	}
	after, err := e.svc.GetDraft(ctx, sc, m.ID)
	if err != nil || after.Revision != 3 || after.Hash != d.Hash {
		t.Errorf("a refused patch changed the draft: %+v %v", after, err)
	}
	// Removing the sink's incoming flow leaves no reachable sink: saved,
	// with the error among the issues.
	broken, err := e.svc.PatchDraft(ctx, sc, requestID(), m.ID, 3, []byte(`[{"op":"test","path":"/nodes/8/id","value":"deployed"},{"op":"remove","path":"/nodes/8"}]`))
	if err != nil {
		t.Fatal(err)
	}
	if broken.Revision != 4 || !slices.ContainsFunc(broken.Issues, func(i model.ValidationIssue) bool { return i.Severity == model.SeverityError }) {
		t.Errorf("semantic errors are saved with the draft: %+v", broken.Issues)
	}
}

// Two writers from the same revision: exactly one patch applies and every
// other one is a conflict; nothing is lost or applied twice (TEST-005).
func TestConcurrentPatches(t *testing.T) {
	e := setup(t)
	ctx := context.Background()
	sc := e.scope(tenant.Editor)
	m := e.create(t, sc, createBody)
	const n = 8
	errs := make([]error, n)
	var wg sync.WaitGroup
	for i := range n {
		wg.Go(func() {
			_, errs[i] = e.svc.PatchDraft(ctx, sc, requestID(), m.ID, 1, rename(fmt.Sprintf("Writer %d", i)))
		})
	}
	wg.Wait()
	won := -1
	for i, err := range errs {
		switch {
		case err == nil:
			if won >= 0 {
				t.Errorf("writers %d and %d both applied", won, i)
			}
			won = i
		case !errors.Is(err, models.ErrConflict):
			t.Errorf("writer %d: %v", i, err)
		}
	}
	d, err := e.svc.GetDraft(ctx, sc, m.ID)
	if err != nil || won < 0 || d.Revision != 2 || !bytes.Contains(d.IR, fmt.Appendf(nil, `"name":"Writer %d"`, won)) {
		t.Errorf("after the race: winner %d, draft %d %v", won, d.Revision, err)
	}
}

// Publish makes the draft version 2 with version 1 as its parent and the
// draft following it; stale requests, a clean draft and an invalid model
// are refused without writing anything (TEST-004, D-003).
func TestPublish(t *testing.T) {
	e := setup(t)
	ctx := context.Background()
	sc := e.scope(tenant.Editor)
	m := e.create(t, sc, createBody)
	v1 := m.LatestVersion.ID
	if _, _, err := e.publish(sc, m.ID, 1, v1, key()); !errors.Is(err, models.ErrNoChanges) {
		t.Errorf("a clean draft: %v", err)
	}
	if _, err := e.svc.PatchDraft(ctx, sc, requestID(), m.ID, 1, rename("Published name")); err != nil {
		t.Fatal(err)
	}
	if _, _, err := e.publish(sc, m.ID, 1, v1, key()); !errors.Is(err, models.ErrConflict) {
		t.Errorf("stale revision: %v", err)
	}
	if _, _, err := e.publish(sc, m.ID, 2, ids.New("mv"), key()); !errors.Is(err, models.ErrConflict) {
		t.Errorf("wrong expected base: %v", err)
	}
	k, rid := key(), requestID()
	status, out, err := e.svc.Publish(ctx, sc, rid, k, m.ID, 2, fmt.Appendf(nil, `{"expected_base_version_id":%q}`, v1), models.PublishRequest{ExpectedBaseVersionID: v1})
	if err != nil || status != 201 {
		t.Fatalf("publish: %d %v", status, err)
	}
	matches(t, "ModelVersion", out)
	var v2 models.Version
	if err := json.Unmarshal(out, &v2); err != nil {
		t.Fatal(err)
	}
	if v2.Version != 2 || v2.ParentVersionID == nil || *v2.ParentVersionID != v1 || v2.ModelID != m.ID || !bytes.Contains(v2.IR, []byte("Published name")) {
		t.Errorf("v2 %+v", v2)
	}
	if got := e.audit(t, rid); len(got) != 1 || got[0] != "model.publish model_version "+v2.ID {
		t.Errorf("audit %v", got)
	}
	got, err := e.svc.GetModel(ctx, sc, m.ID)
	if err != nil || got.LatestVersion.ID != v2.ID || got.LatestVersion.Version != 2 || got.Name != "Published name" ||
		got.Draft.BaseVersionID != v2.ID || got.Draft.Revision != 3 || got.Draft.OutOfDate {
		t.Errorf("model after publish: %+v %v", got, err)
	}
	// The same key replays the response; publishing again finds no changes.
	if s2, o2, err := e.publish(sc, m.ID, 2, v1, k); err != nil || s2 != 201 || !bytes.Equal(o2, out) {
		t.Errorf("replay: %d %v", s2, err)
	}
	if _, _, err := e.publish(sc, m.ID, 3, v2.ID, key()); !errors.Is(err, models.ErrNoChanges) {
		t.Errorf("publishing the published draft: %v", err)
	}
	// An invalid draft is saved but not published.
	if _, err := e.svc.PatchDraft(ctx, sc, requestID(), m.ID, 3, []byte(`[{"op":"test","path":"/nodes/8/id","value":"deployed"},{"op":"remove","path":"/nodes/8"}]`)); err != nil {
		t.Fatal(err)
	}
	var invalid *models.InvalidError
	// Without the sink, flows refer to a node that is gone; the validator
	// stops at that stage (D-023).
	if _, _, err := e.publish(sc, m.ID, 4, v2.ID, key()); !errors.As(err, &invalid) || !slices.ContainsFunc(invalid.Issues, func(i model.ValidationIssue) bool { return i.Code == validate.UnknownReference }) {
		t.Errorf("invalid draft: %v", err)
	}
	page, err := e.svc.ListVersions(ctx, sc, m.ID, "", 10)
	if err != nil || len(page.Items) != 2 || page.Items[0].ID != v2.ID || page.Items[1].ID != v1 {
		t.Errorf("versions after refusals: %+v %v", page, err)
	}
	matches(t, "Versions", mustJSON(t, page))
}

// D-041: a version keeps the comment it was published with, as given; one
// published with an empty comment, and the first version, have none. A
// comment is at most 2000 characters (not bytes) and cannot hold U+0000,
// which PostgreSQL text cannot store, or invalid UTF-8.
func TestPublishKeepsTheComment(t *testing.T) {
	e := setup(t)
	ctx := context.Background()
	sc := e.scope(tenant.Editor)
	m := e.create(t, sc, createBody)
	if first, err := e.svc.GetVersion(ctx, sc, m.LatestVersion.ID); err != nil || first.Comment != nil {
		t.Errorf("the first version's comment: %v %v", first.Comment, err)
	}
	base, rev := m.LatestVersion.ID, int64(1)
	publish := func(comment string) (models.Version, error) {
		body := fmt.Appendf(nil, `{"expected_base_version_id":%q}`, base)
		if utf8.ValidString(comment) { // the HTTP layer never passes invalid UTF-8
			body = fmt.Appendf(nil, `{"expected_base_version_id":%q,"comment":%s}`, base, mustJSON(t, comment))
		}
		_, out, err := e.svc.Publish(ctx, sc, requestID(), key(), m.ID, rev, body, models.PublishRequest{ExpectedBaseVersionID: base, Comment: comment})
		if err != nil {
			return models.Version{}, err
		}
		matches(t, "ModelVersion", out)
		var v models.Version
		return v, json.Unmarshal(out, &v)
	}
	long := strings.Repeat("審", 2000)
	for i, comment := range []string{"Ready for review: QA has two testers now.", long, ""} {
		d, err := e.svc.PatchDraft(ctx, sc, requestID(), m.ID, rev, rename(fmt.Sprintf("Name %d", i)))
		if err != nil {
			t.Fatal(err)
		}
		rev = d.Revision
		v, err := publish(comment)
		if err != nil {
			t.Fatalf("publishing with %d characters: %v", len([]rune(comment)), err)
		}
		stored, err := e.svc.GetVersion(ctx, sc, v.ID)
		if err != nil {
			t.Fatal(err)
		}
		for what, got := range map[string]*string{"published": v.Comment, "read back": stored.Comment} {
			if comment == "" && got != nil || comment != "" && (got == nil || *got != comment) {
				t.Errorf("%d characters, %s: %v", len([]rune(comment)), what, got)
			}
		}
		base, rev = v.ID, rev+1
	}
	page, err := e.svc.ListVersions(ctx, sc, m.ID, "", 10)
	// Newest first: the empty comment, the long one, the short one, none.
	if err != nil || len(page.Items) != 4 || page.Items[0].Comment != nil || page.Items[1].Comment == nil || *page.Items[1].Comment != long ||
		page.Items[3].Comment != nil {
		t.Errorf("listed versions: %v", err)
	}
	d, err := e.svc.PatchDraft(ctx, sc, requestID(), m.ID, rev, rename("Refused"))
	if err != nil {
		t.Fatal(err)
	}
	rev = d.Revision
	for name, comment := range map[string]string{"2001 characters": long + "審", "U+0000": "a\x00b", "invalid UTF-8": "a\xffb"} {
		if _, err := publish(comment); !errors.Is(err, models.ErrInvalidComment) {
			t.Errorf("%s: %v", name, err)
		}
	}
}

// Two publishes of the same draft race: one creates version 2, the other
// is a conflict, and there are exactly two versions (TEST-005).
func TestPublishRace(t *testing.T) {
	e := setup(t)
	ctx := context.Background()
	sc := e.scope(tenant.Editor)
	m := e.create(t, sc, createBody)
	if _, err := e.svc.PatchDraft(ctx, sc, requestID(), m.ID, 1, rename("Raced")); err != nil {
		t.Fatal(err)
	}
	const n = 6
	errs := make([]error, n)
	var wg sync.WaitGroup
	for i := range n {
		wg.Go(func() { _, _, errs[i] = e.publish(sc, m.ID, 2, m.LatestVersion.ID, key()) })
	}
	wg.Wait()
	wins := 0
	for i, err := range errs {
		if err == nil {
			wins++
		} else if !errors.Is(err, models.ErrConflict) {
			t.Errorf("publisher %d: %v", i, err)
		}
	}
	page, err := e.svc.ListVersions(ctx, sc, m.ID, "", 10)
	if wins != 1 || err != nil || len(page.Items) != 2 {
		t.Errorf("%d publishes won, %d versions, %v", wins, len(page.Items), err)
	}
}

// When a version comes from somewhere other than the draft (an approval,
// T-026): a clean draft follows it, a dirty draft is kept as it is and is
// out of date, and only an explicit reset discards it (D-003).
func TestDraftWhenAnotherVersionArrives(t *testing.T) {
	e := setup(t)
	ctx := context.Background()
	sc := e.scope(tenant.Editor)
	m := e.create(t, sc, createBody)
	v1, err := e.svc.GetVersion(ctx, sc, m.LatestVersion.ID)
	if err != nil {
		t.Fatal(err)
	}
	approved := bytes.Replace(v1.IR, []byte(`"name":"Pipeline"`), []byte(`"name":"Approved"`), 1)
	v2, err := e.svc.CommitVersion(ctx, sc, m.ID, approved, nil)
	if err != nil {
		t.Fatal(err)
	}
	d, err := e.svc.GetDraft(ctx, sc, m.ID)
	if err != nil || d.BaseVersionID != v2 || d.Revision != 2 || d.OutOfDate || !bytes.Contains(d.IR, []byte(`"name":"Approved"`)) {
		t.Errorf("clean draft did not follow: %+v %v", d, err)
	}
	// Now dirty: an edit, then another approval.
	edited, err := e.svc.PatchDraft(ctx, sc, requestID(), m.ID, 2, rename("My edit"))
	if err != nil {
		t.Fatal(err)
	}
	v3, err := e.svc.CommitVersion(ctx, sc, m.ID, bytes.Replace(approved, []byte(`"Approved"`), []byte(`"Approved again"`), 1), nil)
	if err != nil {
		t.Fatal(err)
	}
	kept, err := e.svc.GetDraft(ctx, sc, m.ID)
	if err != nil || kept.Revision != edited.Revision || kept.Hash != edited.Hash || kept.BaseVersionID != v2 || !kept.OutOfDate {
		t.Errorf("dirty draft not kept: %+v %v", kept, err)
	}
	if summary, err := e.svc.GetModel(ctx, sc, m.ID); err != nil || !summary.Draft.OutOfDate || summary.LatestVersion.ID != v3 || summary.Draft.BaseVersionID != v2 {
		t.Errorf("model summary of an out-of-date draft: %+v %v", summary, err)
	}
	// Out of date: publish refuses it instead of overwriting v3.
	if _, _, err := e.publish(sc, m.ID, kept.Revision, v2, key()); !errors.Is(err, models.ErrConflict) {
		t.Errorf("publishing an out-of-date draft: %v", err)
	}
	if _, _, err := e.publish(sc, m.ID, kept.Revision, v3, key()); !errors.Is(err, models.ErrConflict) {
		t.Errorf("publishing an out-of-date draft as if it were current: %v", err)
	}
	// Reset needs confirmation, the latest version and the current revision.
	if _, err := e.svc.ResetDraft(ctx, sc, requestID(), m.ID, kept.Revision, models.ResetRequest{ExpectedLatestVersionID: v3}); !errors.Is(err, models.ErrNotConfirmed) {
		t.Errorf("unconfirmed reset: %v", err)
	}
	if _, err := e.svc.ResetDraft(ctx, sc, requestID(), m.ID, kept.Revision, models.ResetRequest{ExpectedLatestVersionID: v2, ConfirmDiscard: true}); !errors.Is(err, models.ErrConflict) {
		t.Errorf("reset to a version that is not the latest: %v", err)
	}
	if _, err := e.svc.ResetDraft(ctx, sc, requestID(), m.ID, kept.Revision-1, models.ResetRequest{ExpectedLatestVersionID: v3, ConfirmDiscard: true}); !errors.Is(err, models.ErrConflict) {
		t.Errorf("reset at a stale revision: %v", err)
	}
	rid := requestID()
	reset, err := e.svc.ResetDraft(ctx, sc, rid, m.ID, kept.Revision, models.ResetRequest{ExpectedLatestVersionID: v3, ConfirmDiscard: true})
	if err != nil || reset.BaseVersionID != v3 || reset.OutOfDate || reset.Revision != kept.Revision+1 || !bytes.Contains(reset.IR, []byte("Approved again")) {
		t.Errorf("reset: %+v %v", reset, err)
	}
	matches(t, "Draft", mustJSON(t, reset))
	if got := e.audit(t, rid); len(got) != 1 || got[0] != "model.draft.reset model "+m.ID {
		t.Errorf("audit %v", got)
	}
}

// Versions cannot be changed or removed by the API login (TEST-004), and a
// version's parent is a version of the same model.
func TestVersionsAreImmutable(t *testing.T) {
	e := setup(t)
	ctx := context.Background()
	sc := e.scope(tenant.Owner)
	m := e.create(t, sc, createBody)
	for _, stmt := range []string{
		`UPDATE twin.model_versions SET hash=hash WHERE id=$1`,
		`DELETE FROM twin.model_versions WHERE id=$1`,
	} {
		err := e.svc.DB.Run(ctx, sc, tenant.ReadWrite, func(ctx context.Context, tx tenant.Tx, _ tenant.Membership) error {
			_, err := tx.Exec(ctx, stmt, m.LatestVersion.ID)
			return err
		})
		if err == nil || !strings.Contains(err.Error(), "permission denied") {
			t.Errorf("%s: %v", stmt, err)
		}
	}
	other := e.create(t, sc, createBody)
	err := e.svc.DB.Run(ctx, sc, tenant.ReadWrite, func(ctx context.Context, tx tenant.Tx, m2 tenant.Membership) error {
		v, err := e.svc.GetVersion(ctx, sc, other.LatestVersion.ID)
		if err != nil {
			return err
		}
		_, err = tx.Exec(ctx, `INSERT INTO twin.model_versions(id,organization_id,model_id,version,schema_version,parent_version_id,ir,hash,created_by)
			VALUES ($1,$2,$3,2,'0.1',$4,$5,$6,$7)`, ids.New("mv"), m2.OrganizationID, m.ID, other.LatestVersion.ID,
			bytes.Replace(v.IR, []byte(other.ID), []byte(m.ID), 1), v.Hash, m2.UserID)
		return err
	})
	if err == nil {
		t.Error("a version took another model's version as its parent")
	}
}

// snapshots is an in-memory stand-in for dataset object storage (T-016)
// that serves frozen snapshots by organization and version.
type snapshots map[string][]byte

func (s snapshots) Open(_ context.Context, org, dsv string) (io.ReadCloser, error) {
	data, ok := s[org+"/"+dsv]
	if !ok {
		return nil, datasets.ErrNotFound
	}
	return io.NopCloser(bytes.NewReader(data)), nil
}

// Empirical data (TEST-014): a draft that uses a dataset its base does not
// bind gets the issue and does not publish; a publish verifies the frozen
// snapshots (a wrong hash fails); a version keeps every binding of its base,
// used or not (TWIN_IR_SPEC §5.2; T-012 review M3), and newer data for the
// same dataset changes nothing already published.
func TestDatasets(t *testing.T) {
	e := setup(t)
	ctx := context.Background()
	sc := e.scope(tenant.Editor)
	m := e.create(t, sc, createBody)
	empirical := []byte(`[{"op":"test","path":"/nodes/1/id","value":"code_review"},{"op":"replace","path":"/nodes/1/service_time/distribution","value":{"type":"empirical","samples_ref":"dataset:review_duration"}}]`)
	d, err := e.svc.PatchDraft(ctx, sc, requestID(), m.ID, 1, empirical)
	if err != nil {
		t.Fatal(err)
	}
	if !slices.ContainsFunc(d.Issues, func(i model.ValidationIssue) bool {
		return i.Code == validate.DatasetBindingInvalid && i.Path == "/nodes/1/service_time/distribution/samples_ref"
	}) {
		t.Errorf("an unbound dataset is not among the issues: %v", d.Issues)
	}
	var invalid *models.InvalidError
	if _, _, err := e.publish(sc, m.ID, 2, m.LatestVersion.ID, key()); !errors.As(err, &invalid) {
		t.Errorf("publishing an unbound dataset: %v", err)
	}

	// A frozen dataset version, and a base version that binds it (as an
	// approved import would, T-018) along with one it does not use.
	snap, binding, unused := e.dataset(t, "dataset:review_duration"), model.DatasetBinding{}, model.DatasetBinding{}
	binding = snap.binding
	unusedSnap := e.dataset(t, "dataset:unused")
	unused = unusedSnap.binding
	store := snapshots{e.org.ID + "/" + binding.DatasetVersionID: snap.data, e.org.ID + "/" + unused.DatasetVersionID: unusedSnap.data}
	v1, err := e.svc.GetVersion(ctx, sc, m.LatestVersion.ID)
	if err != nil {
		t.Fatal(err)
	}
	base, err := e.svc.CommitVersion(ctx, sc, m.ID, bytes.Replace(v1.IR, []byte(`"name":"Pipeline"`), []byte(`"name":"With data"`), 1), []model.DatasetBinding{binding, unused})
	if err != nil {
		t.Fatal(err)
	}
	// The base binds data its IR does not use yet; its bindings are checked
	// all the same, so without dataset storage nothing can be checked.
	if _, err := e.svc.ResetDraft(ctx, sc, requestID(), m.ID, 2, models.ResetRequest{ExpectedLatestVersionID: base, ConfirmDiscard: true}); !errors.Is(err, models.ErrDatasetsUnavailable) {
		t.Errorf("checking bindings without dataset storage: %v", err)
	}
	e.svc.Datasets = store
	if _, err := e.svc.ResetDraft(ctx, sc, requestID(), m.ID, 2, models.ResetRequest{ExpectedLatestVersionID: base, ConfirmDiscard: true}); err != nil {
		t.Fatal(err)
	}
	d, err = e.svc.PatchDraft(ctx, sc, requestID(), m.ID, 3, empirical)
	if err != nil || slices.ContainsFunc(d.Issues, func(i model.ValidationIssue) bool { return i.Code == validate.DatasetBindingInvalid }) {
		t.Fatalf("a bound dataset: %v %v", d.Issues, err)
	}
	// A snapshot that is not what the binding froze fails the publish.
	e.svc.Datasets = snapshots{e.org.ID + "/" + binding.DatasetVersionID: append(slices.Clone(snap.data), ' '), e.org.ID + "/" + unused.DatasetVersionID: unusedSnap.data}
	if _, _, err := e.publish(sc, m.ID, 4, base, key()); !errors.As(err, &invalid) ||
		!slices.ContainsFunc(invalid.Issues, func(i model.ValidationIssue) bool { return i.Code == validate.DatasetBindingInvalid }) {
		t.Errorf("a changed snapshot: %v", err)
	}
	e.svc.Datasets = store
	_, out, err := e.publish(sc, m.ID, 4, base, key())
	if err != nil {
		t.Fatal(err)
	}
	var v models.Version
	if err := json.Unmarshal(out, &v); err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(v.DatasetBindings, []model.DatasetBinding{binding, unused}) {
		t.Errorf("the version froze %v, want its base's bindings", v.DatasetBindings)
	}
	// Newer data for the same dataset: the published version is unchanged.
	e.datasetVersion(t, snap.dataset, 2, "dataset:review_duration")
	again, err := e.svc.GetVersion(ctx, sc, v.ID)
	if err != nil || !slices.Equal(again.DatasetBindings, v.DatasetBindings) || again.Hash != v.Hash {
		t.Errorf("published version changed: %+v %v", again, err)
	}
}

type snapshotFixture struct {
	dataset string
	data    []byte
	binding model.DatasetBinding
}

// dataset stores a dataset with one frozen version of three samples.
func (e *env) dataset(t *testing.T, ref string) snapshotFixture {
	t.Helper()
	ds := ids.New("ds")
	err := postgres.OpenPool(e.admin).Tx(context.Background(), pgx.TxOptions{}, func(ctx context.Context, tx pgx.Tx) error {
		_, err := tx.Exec(ctx, `INSERT INTO twin.datasets(id,organization_id,project_id,kind,name,created_by) VALUES ($1,$2,$3,'empirical_samples','D',$4)`,
			ds, e.org.ID, e.project, e.org.Members[tenant.Owner].UserID)
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	return e.datasetVersion(t, ds, 1, ref)
}

func (e *env) datasetVersion(t *testing.T, ds string, n int, ref string) snapshotFixture {
	t.Helper()
	dsv := ids.New("dsv")
	data := fmt.Appendf(nil, `{"dataset_version_id":%q,"organization_id":%q,"logical_ref":%q,"unit":"minute","samples":[120,240,360]}`, dsv, e.org.ID, ref)
	h := hashing.ContentHash(data)
	err := postgres.OpenPool(e.admin).Tx(context.Background(), pgx.TxOptions{}, func(ctx context.Context, tx pgx.Tx) error {
		_, err := tx.Exec(ctx, `INSERT INTO twin.dataset_versions(id,organization_id,dataset_id,version,object_key,content_hash,content_type,row_count,unit,created_by)
			VALUES ($1,$2,$3,$4,$5,$6,'application/json',3,'minute',$7)`, dsv, e.org.ID, ds, n, "tenant/"+e.org.ID+"/datasets/"+dsv+".json", h, e.org.Members[tenant.Owner].UserID)
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	return snapshotFixture{dataset: ds, data: data, binding: model.DatasetBinding{LogicalRef: ref, DatasetVersionID: dsv, ContentHash: h, Unit: model.Minute, SampleCount: 3}}
}

// Lists page without repeats, and a cursor only works for the list it
// came from.
func TestLists(t *testing.T) {
	e := setup(t)
	ctx := context.Background()
	sc := e.scope(tenant.Viewer)
	var want []string
	for range 5 {
		want = append(want, e.create(t, e.scope(tenant.Editor), createBody).ID)
	}
	var got []string
	cursor := ""
	for {
		page, err := e.svc.ListModels(ctx, sc, e.project, cursor, 2)
		if err != nil {
			t.Fatal(err)
		}
		matches(t, "Models", mustJSON(t, page))
		for _, m := range page.Items {
			got = append(got, m.ID)
		}
		if page.NextCursor == nil {
			break
		}
		cursor = *page.NextCursor
	}
	if !slices.Equal(got, want) {
		t.Errorf("models %v, want %v", got, want)
	}
	first, err := e.svc.ListModels(ctx, sc, e.project, "", 2)
	if err != nil || first.NextCursor == nil {
		t.Fatal(err)
	}
	p2 := e.newProject(t, e.org)
	// The same cursor with its JSON spaced out decodes to the same values
	// but is not one this service issued.
	raw, err := base64.RawURLEncoding.DecodeString(*first.NextCursor)
	if err != nil {
		t.Fatal(err)
	}
	spaced := base64.RawURLEncoding.EncodeToString(bytes.ReplaceAll(raw, []byte(`,"`), []byte(`, "`)))
	for name, c := range map[string]string{"another project's": *first.NextCursor, "garbage": "x", "padded": *first.NextCursor + "A", "spaced": spaced} {
		project := e.project
		if name == "another project's" {
			project = p2
		}
		if _, err := e.svc.ListModels(ctx, sc, project, c, 2); !errors.Is(err, models.ErrInvalidCursor) {
			t.Errorf("%s cursor: %v", name, err)
		}
	}
	if _, err := e.svc.ListModels(ctx, sc, ids.New("prj"), "", 2); !errors.Is(err, models.ErrNotFound) {
		t.Errorf("unknown project: %v", err)
	}
	// Versions, newest first, in pages of one.
	m := e.create(t, e.scope(tenant.Editor), createBody)
	for i := range 2 {
		cur, _ := e.svc.GetModel(ctx, sc, m.ID)
		if _, err := e.svc.PatchDraft(ctx, e.scope(tenant.Editor), requestID(), m.ID, cur.Draft.Revision, rename(fmt.Sprintf("V%d", i+2))); err != nil {
			t.Fatal(err)
		}
		if _, _, err := e.publish(e.scope(tenant.Editor), m.ID, cur.Draft.Revision+1, cur.LatestVersion.ID, key()); err != nil {
			t.Fatal(err)
		}
	}
	var numbers []int64
	cursor = ""
	for {
		page, err := e.svc.ListVersions(ctx, sc, m.ID, cursor, 1)
		if err != nil {
			t.Fatal(err)
		}
		for _, v := range page.Items {
			numbers = append(numbers, v.Version)
		}
		if page.NextCursor == nil {
			break
		}
		cursor = *page.NextCursor
		if _, err := e.svc.ListVersions(ctx, sc, want[0], cursor, 1); !errors.Is(err, models.ErrInvalidCursor) {
			t.Errorf("another model's version cursor: %v", err)
		}
	}
	if !slices.Equal(numbers, []int64{3, 2, 1}) {
		t.Errorf("versions %v", numbers)
	}
}

// rulePack is the software delivery pack with one rule of its own: a model
// named "Forbidden" is an error. The service saves pack issues with the
// draft and refuses to publish them.
type rulePack struct{ softwaredelivery.Pack }

func (rulePack) Validate(ir *model.ModelIR) []model.ValidationIssue {
	if ir != nil && ir.Name == "Forbidden" {
		return []model.ValidationIssue{model.NewIssue("PACK_NAME_RULE", "/name", "this name is not allowed", model.SeverityError)}
	}
	return softwaredelivery.Pack{}.Validate(ir)
}

type rulePacks struct{}

func (rulePacks) Lookup(domain string) (domainpack.DomainPack, error) {
	if domain != softwaredelivery.ID {
		return nil, domainpack.ErrUnsupportedDomain
	}
	return rulePack{}, nil
}

func TestPackRules(t *testing.T) {
	e := setup(t)
	e.svc.Packs = rulePacks{}
	ctx := context.Background()
	sc := e.scope(tenant.Editor)
	m := e.create(t, sc, createBody)
	d, err := e.svc.PatchDraft(ctx, sc, requestID(), m.ID, 1, rename("Forbidden"))
	if err != nil {
		t.Fatal(err)
	}
	if !slices.ContainsFunc(d.Issues, func(i model.ValidationIssue) bool { return i.Code == "PACK_NAME_RULE" && i.Path == "/name" }) {
		t.Errorf("the pack's issue is not saved: %v", d.Issues)
	}
	var invalid *models.InvalidError
	if _, _, err := e.publish(sc, m.ID, 2, m.LatestVersion.ID, key()); !errors.As(err, &invalid) ||
		!slices.ContainsFunc(invalid.Issues, func(i model.ValidationIssue) bool { return i.Code == "PACK_NAME_RULE" }) {
		t.Errorf("publishing against the pack's rule: %v", err)
	}
}
