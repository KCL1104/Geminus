package models_test

// Edge cases from the independent review of T-012 (round 1): what is
// stored is not what is validated, more issues than a draft can hold,
// manifests, snapshots, locks, idempotency scope and input limits.

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json/v2"
	"errors"
	"fmt"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/kcl1104/geminus/internal/domainpack"
	"github.com/kcl1104/geminus/internal/domainpack/softwaredelivery"
	"github.com/kcl1104/geminus/internal/ids"
	"github.com/kcl1104/geminus/internal/model"
	"github.com/kcl1104/geminus/internal/model/validate"
	"github.com/kcl1104/geminus/internal/models"
	"github.com/kcl1104/geminus/internal/storage/idempotency"
	"github.com/kcl1104/geminus/internal/storage/postgres"
	"github.com/kcl1104/geminus/internal/tenant"
)

// adminExec runs sql as the table owner in the test organization's scope.
func (e *env) adminExec(t *testing.T, sql string, args ...any) {
	t.Helper()
	err := postgres.OpenPool(e.admin).Tx(context.Background(), pgx.TxOptions{}, func(ctx context.Context, tx pgx.Tx) error {
		if _, err := tx.Exec(ctx, `SET LOCAL ROLE twin_owner`); err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `SELECT set_config('app.organization_id',$1,true)`, e.org.ID); err != nil {
			return err
		}
		_, err := tx.Exec(ctx, sql, args...)
		return err
	})
	if err != nil {
		t.Fatalf("%s: %v", sql, err)
	}
}

// sinks is a patch adding n sink nodes, each with domain attributes that
// hold count copies of number.
func sinks(prefix string, n, count int, number string) []byte {
	values := strings.TrimSuffix(strings.Repeat(number+",", count), ",")
	var ops []string
	for i := range n {
		ops = append(ops, fmt.Sprintf(`{"op":"add","path":"/nodes/-","value":{"id":"%s%d","type":"sink","domain_attributes":{"a":[%s]}}}`, prefix, i, values))
	}
	return []byte("[" + strings.Join(ops, ",") + "]")
}

// Numbers are stored as written: jsonb printed 1e300 as 301 digits and
// made this 55 KB draft 2.8 MB of text (T-012 review M1). Stored as its
// canonical text (D-040), the draft is the size that was checked, and it
// publishes; a reset to the same content as a version works.
func TestNumbersAreStoredAsWritten(t *testing.T) {
	e := setup(t)
	ctx := context.Background()
	sc := e.scope(tenant.Editor)

	m := e.create(t, sc, createBody)
	d, err := e.svc.PatchDraft(ctx, sc, requestID(), m.ID, 1, sinks("b", 4, 2300, "1e300"))
	if err != nil {
		t.Fatal(err)
	}
	if stored, _ := e.storedIR(t, `SELECT ir, hash FROM twin.model_drafts WHERE model_id=$1`, m.ID); len(stored) != len(d.IR) {
		t.Fatalf("the stored draft is %d bytes, its canonical form %d", len(stored), len(d.IR))
	}
	if _, _, err := e.publish(sc, m.ID, 2, m.LatestVersion.ID, key()); err != nil {
		t.Errorf("publishing a %d-byte draft: %v", len(d.IR), err)
	}
	// The same content as a version from another path; the draft is dirty,
	// so it stays behind, and a reset to that version works.
	edited, err := e.svc.PatchDraft(ctx, sc, requestID(), m.ID, 3, rename("Mine"))
	if err != nil {
		t.Fatal(err)
	}
	v3, err := e.svc.CommitVersion(ctx, sc, m.ID, bytes.Replace(d.IR, []byte(`"name":"Pipeline"`), []byte(`"name":"Approved"`), 1), nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := e.svc.ResetDraft(ctx, sc, requestID(), m.ID, edited.Revision, models.ResetRequest{ExpectedLatestVersionID: v3, ConfirmDiscard: true}); err != nil {
		t.Errorf("reset to a large version: %v", err)
	}
}

// A schema-valid draft with more issues than a draft holds is saved with
// the first ones and a LIMIT_EXCEEDED issue that says how many are left
// out (D-028; migration 0003 and Error.details hold at most 1000).
func TestMoreIssuesThanADraftHolds(t *testing.T) {
	e := setup(t)
	ctx := context.Background()
	sc := e.scope(tenant.Editor)
	m := e.create(t, sc, createBody)
	var ops []string
	for i := range 991 {
		ops = append(ops, fmt.Sprintf(`{"op":"add","path":"/flows/-","value":{"id":"g%d","from":"nowhere_a%d","to":"nowhere_b%d","routing":{"type":"probability","probability":1}}}`, i, i, i))
	}
	d, err := e.svc.PatchDraft(ctx, sc, requestID(), m.ID, 1, []byte("["+strings.Join(ops, ",")+"]"))
	if err != nil {
		t.Fatal(err)
	}
	if len(d.Issues) != 1000 || d.Issues[0].Code != validate.LimitExceeded || d.Issues[0].Path != "" || d.Issues[0].Severity != model.SeverityError ||
		!slices.Equal(validate.Sort(d.Issues), d.Issues) {
		t.Errorf("%d issues, first %+v", len(d.Issues), d.Issues[0])
	}
	matches(t, "Draft", mustJSON(t, d))
	var invalid *models.InvalidError
	if _, _, err := e.publish(sc, m.ID, 2, m.LatestVersion.ID, key()); !errors.As(err, &invalid) || len(invalid.Issues) != 1000 {
		t.Errorf("publish: %v", err)
	}
	// A patch whose result is not schema-valid, with as many issues: the
	// 422 holds at most 1000 too (Error.details; T-012 review 2 N1).
	ops = ops[:0]
	for i := range 996 {
		ops = append(ops, fmt.Sprintf(`{"op":"add","path":"/resources/-","value":{"id":"r%d","type":"human","name":"","capacity":0,"queue_discipline":"fifo"}}`, i))
	}
	invalid = nil
	_, err = e.svc.PatchDraft(ctx, sc, requestID(), m.ID, 2, []byte("["+strings.Join(ops, ",")+"]"))
	if !errors.As(err, &invalid) || len(invalid.Issues) != 1000 || invalid.Issues[0].Code != validate.LimitExceeded {
		n := 0
		if invalid != nil {
			n = len(invalid.Issues)
		}
		t.Errorf("a schema-invalid patch: %d issues, %v", n, err)
	}
	// A patch document with as many errors of its own (move is not allowed).
	ops = ops[:0]
	for i := range 1100 {
		ops = append(ops, fmt.Sprintf(`{"op":"move","from":"/name","path":"/x%d"}`, i))
	}
	invalid = nil
	_, err = e.svc.PatchDraft(ctx, sc, requestID(), m.ID, 2, []byte("["+strings.Join(ops, ",")+"]"))
	if !errors.As(err, &invalid) || len(invalid.Issues) != 1000 || invalid.Issues[0].Code != validate.LimitExceeded {
		n := 0
		if invalid != nil {
			n = len(invalid.Issues)
		}
		t.Errorf("a patch with errors of its own: %d issues, %v", n, err)
	}
}

// Up to 1000 issues are kept as they are; from 1001 the first 999 follow a
// LIMIT_EXCEEDED that counts the rest, an error if any left out is.
func TestCappedBoundary(t *testing.T) {
	list := func(n int, last model.Severity) []model.ValidationIssue {
		var out []model.ValidationIssue
		for i := range n {
			severity := model.SeverityWarning
			if i == n-1 {
				severity = last
			}
			out = append(out, model.NewIssue(validate.UnreachableNode, fmt.Sprintf("/nodes/%d", i), "x", severity))
		}
		return out
	}
	if got := models.Capped(list(1000, model.SeverityError)); len(got) != 1000 || got[0].Code == validate.LimitExceeded {
		t.Errorf("1000 issues: %d, first %+v", len(got), got[0])
	}
	got := models.Capped(list(1001, model.SeverityError))
	if len(got) != 1000 || got[0].Code != validate.LimitExceeded || got[0].Severity != model.SeverityError || got[0].Message != "2 more issues are not listed" || got[999].Path != "/nodes/998" {
		t.Errorf("1001 issues: %d, first %+v, last %+v", len(got), got[0], got[len(got)-1])
	}
	if got := models.Capped(list(1001, model.SeverityWarning)); got[0].Severity != model.SeverityWarning {
		t.Errorf("only warnings left out: %+v", got[0])
	}
}

// A manual publish keeps the base's bindings, so data a draft stops using
// for a while can be used again (TWIN_IR_SPEC §5.2; T-012 review M3).
func TestPublishKeepsUnusedBindings(t *testing.T) {
	e := setup(t)
	ctx := context.Background()
	sc := e.scope(tenant.Editor)
	m := e.create(t, sc, createBody)
	review := e.dataset(t, "dataset:review_duration")
	e.svc.Datasets = snapshots{e.org.ID + "/" + review.binding.DatasetVersionID: review.data}
	v1, err := e.svc.GetVersion(ctx, sc, m.LatestVersion.ID)
	if err != nil {
		t.Fatal(err)
	}
	withData := bytes.Replace(v1.IR, []byte(`{"type":"constant","value":240}`), []byte(`{"type":"empirical","samples_ref":"dataset:review_duration"}`), 1)
	base, err := e.svc.CommitVersion(ctx, sc, m.ID, withData, []model.DatasetBinding{review.binding})
	if err != nil {
		t.Fatal(err)
	}
	set := func(dist string) []byte {
		return fmt.Appendf(nil, `[{"op":"test","path":"/nodes/1/id","value":"code_review"},{"op":"replace","path":"/nodes/1/service_time/distribution","value":%s}]`, dist)
	}
	d, err := e.svc.PatchDraft(ctx, sc, requestID(), m.ID, 2, set(`{"type":"constant","value":240}`))
	if err != nil {
		t.Fatal(err)
	}
	_, out, err := e.publish(sc, m.ID, d.Revision, base, key())
	if err != nil {
		t.Fatal(err)
	}
	var v models.Version
	if err := json.Unmarshal(out, &v); err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(v.DatasetBindings, []model.DatasetBinding{review.binding}) {
		t.Errorf("the version froze %v", v.DatasetBindings)
	}
	d, err = e.svc.PatchDraft(ctx, sc, requestID(), m.ID, d.Revision+1, set(`{"type":"empirical","samples_ref":"dataset:review_duration"}`))
	if err != nil || slices.ContainsFunc(d.Issues, func(i model.ValidationIssue) bool { return i.Code == validate.DatasetBindingInvalid }) {
		t.Errorf("using the data again: %v %v", d.Issues, err)
	}
}

// Dataset issue paths index the base version's manifest (T-012 review L2).
func TestDatasetIssuePaths(t *testing.T) {
	e := setup(t)
	ctx := context.Background()
	sc := e.scope(tenant.Editor)
	m := e.create(t, sc, createBody)
	review, unused := e.dataset(t, "dataset:review_duration"), e.dataset(t, "dataset:a_unused") // a_unused sorts first
	v1, err := e.svc.GetVersion(ctx, sc, m.LatestVersion.ID)
	if err != nil {
		t.Fatal(err)
	}
	// review_duration's snapshot is not what its binding froze.
	e.svc.Datasets = snapshots{e.org.ID + "/" + review.binding.DatasetVersionID: append(slices.Clone(review.data), ' '), e.org.ID + "/" + unused.binding.DatasetVersionID: unused.data}
	base, err := e.svc.CommitVersion(ctx, sc, m.ID, bytes.Replace(v1.IR, []byte(`"name":"Pipeline"`), []byte(`"name":"With data"`), 1),
		[]model.DatasetBinding{review.binding, unused.binding})
	if err != nil {
		t.Fatal(err)
	}
	d, err := e.svc.PatchDraft(ctx, sc, requestID(), m.ID, 2, rename("Changed"))
	if err != nil {
		t.Fatal(err)
	}
	var invalid *models.InvalidError
	_, _, err = e.publish(sc, m.ID, 3, base, key())
	if !errors.As(err, &invalid) {
		t.Fatalf("publish: %v", err)
	}
	for name, issues := range map[string][]model.ValidationIssue{"draft": d.Issues, "publish": invalid.Issues} {
		if !slices.ContainsFunc(issues, func(i model.ValidationIssue) bool {
			return i.Code == validate.DatasetBindingInvalid && strings.HasPrefix(i.Path, "/dataset_bindings/1/")
		}) || slices.ContainsFunc(issues, func(i model.ValidationIssue) bool { return strings.HasPrefix(i.Path, "/dataset_bindings/0") }) {
			t.Errorf("%s issues: %v", name, issues)
		}
	}
}

// A read sees one snapshot: with manual edits only, a draft is never out
// of date, however reads and publishes interleave (T-012 review L1).
func TestReadsSeeOneSnapshot(t *testing.T) {
	e := setup(t)
	ctx := context.Background()
	sc, viewer := e.scope(tenant.Editor), e.scope(tenant.Viewer)
	m := e.create(t, sc, createBody)
	var stop atomic.Bool
	var torn atomic.Int64
	var wg sync.WaitGroup
	for range 3 {
		wg.Go(func() {
			for !stop.Load() {
				d, err := e.svc.GetDraft(ctx, viewer, m.ID)
				mm, err2 := e.svc.GetModel(ctx, viewer, m.ID)
				list, err3 := e.svc.ListModels(ctx, viewer, e.project, "", 10)
				if err := errors.Join(err, err2, err3); err != nil {
					t.Error(err)
					return
				}
				if d.OutOfDate || mm.Draft.OutOfDate || slices.ContainsFunc(list.Items, func(x models.Model) bool { return x.Draft.OutOfDate }) {
					torn.Add(1)
				}
			}
		})
	}
	for i := range 30 {
		cur, err := e.svc.GetModel(ctx, sc, m.ID)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := e.svc.PatchDraft(ctx, sc, requestID(), m.ID, cur.Draft.Revision, rename(fmt.Sprintf("N%d", i))); err != nil {
			t.Fatal(err)
		}
		if _, _, err := e.publish(sc, m.ID, cur.Draft.Revision+1, cur.LatestVersion.ID, key()); err != nil {
			t.Fatal(err)
		}
	}
	stop.Store(true)
	wg.Wait()
	if n := torn.Load(); n > 0 {
		t.Errorf("%d reads saw a draft out of date", n)
	}
}

// A draft with no changes against its own base is clean even when it is
// behind; the next version moves it (ADR-022; T-012 review L3).
func TestCleanAgainstItsOwnBase(t *testing.T) {
	e := setup(t)
	ctx := context.Background()
	sc := e.scope(tenant.Editor)
	m := e.create(t, sc, createBody)
	v1, err := e.svc.GetVersion(ctx, sc, m.LatestVersion.ID)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := e.svc.PatchDraft(ctx, sc, requestID(), m.ID, 1, rename("Mine")); err != nil {
		t.Fatal(err)
	}
	approved := func(name string) []byte {
		return bytes.Replace(v1.IR, []byte(`"name":"Pipeline"`), fmt.Appendf(nil, `"name":%q`, name), 1)
	}
	if _, err := e.svc.CommitVersion(ctx, sc, m.ID, approved("A2"), nil); err != nil {
		t.Fatal(err)
	}
	reverted, err := e.svc.PatchDraft(ctx, sc, requestID(), m.ID, 2, rename("Pipeline"))
	if err != nil || reverted.Hash != v1.Hash || !reverted.OutOfDate {
		t.Fatalf("reverted: %+v %v", reverted, err)
	}
	v3, err := e.svc.CommitVersion(ctx, sc, m.ID, approved("A3"), nil)
	if err != nil {
		t.Fatal(err)
	}
	if d, err := e.svc.GetDraft(ctx, sc, m.ID); err != nil || d.OutOfDate || d.BaseVersionID != v3 || d.Revision != 4 {
		t.Errorf("the clean draft did not follow: %+v %v", d, err)
	}
}

// warnPack's template has a node no source reaches (a generic warning),
// and the pack warns about a model named "Warned"; both are saved.
type warnPack struct{ rulePack }

func (warnPack) Template(name, modelID, modelName string, locale domainpack.Locale) (*model.ModelIR, error) {
	ir, err := softwaredelivery.Pack{}.Template(name, modelID, modelName, locale)
	if err == nil {
		ir.Nodes = append(ir.Nodes, model.Node{ID: "unreached", Type: model.NodeSink})
	}
	return ir, err
}

func (warnPack) Validate(ir *model.ModelIR) []model.ValidationIssue {
	out := rulePack{}.Validate(ir)
	if ir != nil && ir.Name == "Warned" {
		out = append(out, model.NewIssue("PACK_NAME_HINT", "/name", "consider another name", model.SeverityWarning))
	}
	return out
}

// brokenPack's template has a flow to a node that does not exist, or with
// schema, a resource with no capacity.
type brokenPack struct {
	softwaredelivery.Pack
	schema bool
}

func (p brokenPack) Template(name, modelID, modelName string, locale domainpack.Locale) (*model.ModelIR, error) {
	ir, err := softwaredelivery.Pack{}.Template(name, modelID, modelName, locale)
	if err == nil && p.schema {
		ir.Resources[0].Capacity = 0
	} else if err == nil {
		ir.Flows[0].To = "nowhere"
	}
	return ir, err
}

type packOf struct{ pack domainpack.DomainPack }

func (p packOf) Lookup(string) (domainpack.DomainPack, error) { return p.pack, nil }

// Create saves the template's warnings and refuses a template with errors:
// that is the pack's fault, so nothing is created (API_DATABASE §4;
// T-012 review L4, L7).
func TestCreateChecksTheTemplate(t *testing.T) {
	e := setup(t)
	ctx := context.Background()
	sc := e.scope(tenant.Editor)
	e.svc.Packs = packOf{warnPack{}}
	m := e.create(t, sc, `{"name":"Warned","domain":"software_delivery","template":"default_delivery_pipeline"}`)
	d, err := e.svc.GetDraft(ctx, sc, m.ID)
	if err != nil {
		t.Fatal(err)
	}
	codes := func(issues []model.ValidationIssue) []string {
		var out []string
		for _, i := range issues {
			out = append(out, i.Code)
		}
		return out
	}
	if got := codes(d.Issues); !slices.Equal(got, []string{"PACK_NAME_HINT", validate.UnreachableNode}) {
		t.Errorf("template issues: %v", d.Issues)
	}

	var req models.CreateRequest
	if err := json.Unmarshal([]byte(createBody), &req); err != nil {
		t.Fatal(err)
	}
	var invalid *models.InvalidError
	for _, p := range []domainpack.DomainPack{brokenPack{}, brokenPack{schema: true}} {
		e.svc.Packs = packOf{p}
		if _, _, err := e.svc.Create(ctx, sc, requestID(), key(), e.project, []byte(createBody), req); err == nil || errors.As(err, &invalid) {
			t.Errorf("an invalid template (%+v): %v", p, err)
		}
	}
	if page, err := e.svc.ListModels(ctx, sc, e.project, "", 10); err != nil || len(page.Items) != 1 {
		t.Errorf("models after the refused create: %d %v", len(page.Items), err)
	}
}

// Issues come sorted (D-023) across stages, and the pack's stage does not
// run after generic errors (T-012 review L7, I1).
func TestIssueOrderAndStages(t *testing.T) {
	e := setup(t)
	e.svc.Packs = packOf{rulePack{}}
	ctx := context.Background()
	sc := e.scope(tenant.Editor)
	m := e.create(t, sc, createBody)
	// A node no source reaches (a warning at /nodes/…) and a name the pack
	// refuses (/name, which sorts first).
	doc := []byte(`[{"op":"add","path":"/nodes/-","value":{"id":"unreached","type":"sink"}},{"op":"replace","path":"/name","value":"Forbidden"}]`)
	d, err := e.svc.PatchDraft(ctx, sc, requestID(), m.ID, 1, doc)
	if err != nil {
		t.Fatal(err)
	}
	if len(d.Issues) != 2 || d.Issues[0].Code != "PACK_NAME_RULE" || d.Issues[1].Code != validate.UnreachableNode {
		t.Errorf("issues: %v", d.Issues)
	}
	// With a generic error the pack's rules are not run.
	doc = []byte(`[{"op":"add","path":"/flows/-","value":{"id":"dangling","from":"nowhere","to":"ci","routing":{"type":"probability","probability":1}}}]`)
	d, err = e.svc.PatchDraft(ctx, sc, requestID(), m.ID, 2, doc)
	if err != nil {
		t.Fatal(err)
	}
	if slices.ContainsFunc(d.Issues, func(i model.ValidationIssue) bool { return i.Code == "PACK_NAME_RULE" }) ||
		!slices.ContainsFunc(d.Issues, func(i model.ValidationIssue) bool { return i.Code == validate.UnknownReference }) {
		t.Errorf("issues after a generic error: %v", d.Issues)
	}
	// A reset recomputes them for the version it goes back to.
	r, err := e.svc.ResetDraft(ctx, sc, requestID(), m.ID, 3, models.ResetRequest{ExpectedLatestVersionID: m.LatestVersion.ID, ConfirmDiscard: true})
	if err != nil || len(r.Issues) != 0 {
		t.Errorf("issues after a reset: %v %v", r.Issues, err)
	}
}

// U+0000 cannot be stored in PostgreSQL text or jsonb: a patch that puts
// it in a string or a member name is refused with a pointer to it
// (T-012 review L5).
func TestNULIsRefused(t *testing.T) {
	e := setup(t)
	ctx := context.Background()
	sc := e.scope(tenant.Editor)
	m := e.create(t, sc, createBody)
	for doc, path := range map[string]string{
		`[{"op":"replace","path":"/name","value":"a\u0000b"}]`:            "/name",
		`[{"op":"add","path":"/metadata/note","value":["ok","x\u0000"]}]`: "/metadata/note/1",
		`[{"op":"add","path":"/metadata/n\u0000te","value":1}]`:           "/metadata", // a name: its object, as httpapi
	} {
		var invalid *models.InvalidError
		_, err := e.svc.PatchDraft(ctx, sc, requestID(), m.ID, 1, []byte(doc))
		if !errors.As(err, &invalid) || len(invalid.Issues) != 1 || invalid.Issues[0].Path != path || invalid.Issues[0].Code != validate.IRSchemaInvalid {
			t.Errorf("%s: %v", doc, err)
		}
	}
	if d, err := e.svc.GetDraft(ctx, sc, m.ID); err != nil || d.Revision != 1 {
		t.Errorf("draft after the refused patches: %+v %v", d, err)
	}
}

// Page sizes outside 1–100 are refused, not a panic or a database error
// (T-012 review L6).
func TestListLimits(t *testing.T) {
	e := setup(t)
	ctx := context.Background()
	sc := e.scope(tenant.Viewer)
	m := e.create(t, e.scope(tenant.Editor), createBody)
	for _, limit := range []int{0, -5, 101} {
		if _, err := e.svc.ListModels(ctx, sc, e.project, "", limit); !errors.Is(err, models.ErrInvalidLimit) {
			t.Errorf("ListModels(%d): %v", limit, err)
		}
		if _, err := e.svc.ListVersions(ctx, sc, m.ID, "", limit); !errors.Is(err, models.ErrInvalidLimit) {
			t.Errorf("ListVersions(%d): %v", limit, err)
		}
	}
	for _, limit := range []int{1, 100} {
		if _, err := e.svc.ListModels(ctx, sc, e.project, "", limit); err != nil {
			t.Errorf("ListModels(%d): %v", limit, err)
		}
	}
}

// Every write waits for the model row: while another transaction holds it,
// a patch and a publish do not proceed (ADR-022; T-012 review L7: the race
// tests alone show this only by timing).
func TestWritesWaitForTheModelRow(t *testing.T) {
	e := setup(t)
	ctx := context.Background()
	sc := e.scope(tenant.Editor)
	m := e.create(t, sc, createBody)
	hold := func() (commit func()) {
		tx, err := e.admin.Begin(ctx)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := tx.Exec(ctx, `SELECT 1 FROM twin.models WHERE id=$1 FOR UPDATE`, m.ID); err != nil {
			t.Fatal(err)
		}
		return func() {
			if err := tx.Commit(ctx); err != nil {
				t.Fatal(err)
			}
		}
	}
	for _, w := range []struct {
		name  string
		write func() error
	}{
		{"patch", func() error { _, err := e.svc.PatchDraft(ctx, sc, requestID(), m.ID, 1, rename("Waited")); return err }},
		{"publish", func() error { _, _, err := e.publish(sc, m.ID, 2, m.LatestVersion.ID, key()); return err }},
	} {
		name, write := w.name, w.write
		release := hold()
		done := make(chan error, 1)
		go func() { done <- write() }()
		select {
		case err := <-done:
			t.Errorf("%s did not wait for the model row: %v", name, err)
			release()
			continue
		case <-time.After(500 * time.Millisecond):
		}
		release()
		select {
		case err := <-done:
			if err != nil {
				t.Errorf("%s after the row was released: %v", name, err)
			}
		case <-time.After(30 * time.Second):
			t.Fatalf("%s still waiting", name)
		}
	}
}

// A publish's idempotency fingerprint includes If-Match, and a replay
// still needs the role (D-011; T-012 review L7).
func TestPublishIdempotencyScope(t *testing.T) {
	e := setup(t)
	ctx := context.Background()
	sc := e.scope(tenant.Editor)
	m := e.create(t, sc, createBody)
	if _, err := e.svc.PatchDraft(ctx, sc, requestID(), m.ID, 1, rename("Changed")); err != nil {
		t.Fatal(err)
	}
	k := key()
	if _, _, err := e.publish(sc, m.ID, 2, m.LatestVersion.ID, k); err != nil {
		t.Fatal(err)
	}
	if _, _, err := e.publish(sc, m.ID, 3, m.LatestVersion.ID, k); !errors.Is(err, idempotency.ErrConflict) {
		t.Errorf("the same key with another If-Match: %v", err)
	}
	e.adminExec(t, `UPDATE twin.memberships SET role='viewer' WHERE organization_id=$1 AND user_id=$2`, e.org.ID, sc.UserID)
	if _, _, err := e.publish(sc, m.ID, 2, m.LatestVersion.ID, k); !errors.Is(err, tenant.ErrForbidden) {
		t.Errorf("a replay after the role was taken away: %v", err)
	}
}

// A cursor names its organization, and a version cursor has no ID member:
// either changed, it is refused (T-012 review L7).
func TestCursorBindings(t *testing.T) {
	e := setup(t)
	ctx := context.Background()
	sc := e.scope(tenant.Viewer)
	m := e.create(t, e.scope(tenant.Editor), createBody)
	cursor := func(doc string) string { return base64.RawURLEncoding.EncodeToString([]byte(doc)) }
	other := ids.New("org")
	if _, err := e.svc.ListModels(ctx, sc, e.project, cursor(fmt.Sprintf(`{"v":1,"o":%q,"l":"models","p":%q,"t":"2026-10-18T00:00:00Z","i":%q}`, other, e.project, m.ID)), 10); !errors.Is(err, models.ErrInvalidCursor) {
		t.Errorf("another organization's cursor: %v", err)
	}
	if _, err := e.svc.ListModels(ctx, sc, e.project, cursor(fmt.Sprintf(`{"v":1,"o":%q,"l":"models","p":%q,"t":"2026-10-18T00:00:00Z","i":%q}`, e.org.ID, e.project, m.ID)), 10); err != nil {
		t.Errorf("the same cursor in its organization: %v", err)
	}
	if _, err := e.svc.ListVersions(ctx, sc, m.ID, cursor(fmt.Sprintf(`{"v":1,"o":%q,"l":"versions","p":%q,"t":"2","i":"x"}`, e.org.ID, m.ID)), 10); !errors.Is(err, models.ErrInvalidCursor) {
		t.Errorf("a version cursor with an ID: %v", err)
	}
	if _, err := e.svc.ListVersions(ctx, sc, m.ID, cursor(fmt.Sprintf(`{"v":1,"o":%q,"l":"versions","p":%q,"t":"2"}`, e.org.ID, m.ID)), 10); err != nil {
		t.Errorf("a version cursor: %v", err)
	}
}
