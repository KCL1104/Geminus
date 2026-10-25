package models_test

// T-054 (H-022, D-069 (b)): publishVersion when the dataset binding stage
// cannot read or verify a snapshot the base binds. It answers 503
// SERVICE_UNAVAILABLE with details[0].code DATASET_STORAGE_FAILED, as
// approveProposal and createManualScenario do; the cause is logged, never
// sent; a request that ended keeps its context's error; nothing is written.

import (
	"bytes"
	"context"
	"encoding/json/v2"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/kcl1104/geminus/internal/auth"
	"github.com/kcl1104/geminus/internal/contracttest"
	"github.com/kcl1104/geminus/internal/domainpack"
	"github.com/kcl1104/geminus/internal/domainpack/softwaredelivery"
	"github.com/kcl1104/geminus/internal/ids"
	"github.com/kcl1104/geminus/internal/model"
	"github.com/kcl1104/geminus/internal/model/hashing"
	"github.com/kcl1104/geminus/internal/model/validate"
	"github.com/kcl1104/geminus/internal/models"
	"github.com/kcl1104/geminus/internal/storage/datasets"
	"github.com/kcl1104/geminus/internal/tenant"
)

// The cause a failing store gives: it must reach the log, never the client.
const storageCause = "open /srv/objects/tenant/secret: input/output error"

// A binding stage that fails with an error rather than with issues is a
// storage failure (DatasetStorageError, 503 DATASET_STORAGE_FAILED on
// publishVersion), unless the request ended or the error says it did:
// then the context's error stays, and httpapi answers it as it answers any
// ended request. The pattern of the approval's and the scenarios'
// TestBindingFailure.
func TestBindingFailure(t *testing.T) {
	cause := errors.New(storageCause)
	var se *models.DatasetStorageError
	if err := models.BindingFailure(context.Background(), cause); !errors.As(err, &se) || !errors.Is(err, cause) {
		t.Errorf("a read failure: %v", err)
	}
	cancelled, cancel := context.WithCancel(context.Background())
	cancel()
	expired, stop := context.WithDeadline(context.Background(), time.Now().Add(-time.Second))
	defer stop()
	for name, c := range map[string]struct {
		ctx  context.Context
		err  error
		want error
	}{
		"a cancelled request":                        {cancelled, fmt.Errorf("reading: %w", context.Canceled), context.Canceled},
		"a cancelled request, the store's own error": {cancelled, cause, cause},
		"a request past its deadline":                {expired, fmt.Errorf("reading: %w", context.DeadlineExceeded), context.DeadlineExceeded},
		"a request past its deadline, the store's":   {expired, cause, cause},
		// The request is alive, but the error says it ended: still the
		// context's error, not the store's failure.
		"a live request, a cancelled read": {context.Background(), fmt.Errorf("reading: %w", context.Canceled), context.Canceled},
		"a live request, a read past its deadline": {context.Background(), fmt.Errorf("reading: %w", context.DeadlineExceeded),
			context.DeadlineExceeded},
	} {
		err := models.BindingFailure(c.ctx, c.err)
		if errors.As(err, &se) || !errors.Is(err, c.want) {
			t.Errorf("%s: %v", name, err)
		}
	}
}

// unreadable is dataset storage whose every read fails with err.
type unreadable struct{ err error }

func (u unreadable) Open(context.Context, string, string) (io.ReadCloser, error) { return nil, u.err }

// The binding stage inside check, where publishVersion meets it twice (the
// candidate, then the draft that follows the new version): storage that
// cannot be read is a DatasetStorageError; a snapshot that is not there is
// an issue of the data, not an error; a request that ended keeps the
// context's error.
func TestCheckStorageFailure(t *testing.T) {
	var doc map[string]any
	if err := json.Unmarshal(contracttest.ReadFile(t, filepath.Join(contracttest.Root(t), "testdata", "golden-models", "default-delivery.json")), &doc); err != nil {
		t.Fatal(err)
	}
	raw, _ := json.Marshal(doc)
	canonical, err := hashing.Canonical(raw)
	if err != nil {
		t.Fatal(err)
	}
	res, err := validate.IR(canonical)
	if err != nil || res.IR == nil {
		t.Fatalf("the golden model: %v %v", err, res.Issues)
	}
	reg, err := domainpack.NewRegistry(softwaredelivery.Pack{})
	if err != nil {
		t.Fatal(err)
	}
	org := "org_01M4YDQK00Y5H2929M9WWZWTVH"
	// Bound and not used by the IR: every binding of a manifest is read
	// (TWIN_IR_SPEC §5.2).
	manifest := []model.DatasetBinding{{LogicalRef: "dataset:unused", DatasetVersionID: ids.New("dsv"),
		ContentHash: "sha256:" + strings.Repeat("0", 64), Unit: model.Minute, SampleCount: 3}}
	svc := &models.Service{Packs: reg}
	cause := errors.New(storageCause)
	var se *models.DatasetStorageError

	svc.Datasets = unreadable{cause}
	if _, err := svc.Check(context.Background(), org, res.IR, manifest); !errors.As(err, &se) || !errors.Is(err, cause) {
		t.Errorf("storage that fails: %v", err)
	}
	svc.Datasets = unreadable{datasets.ErrNotFound}
	issues, err := svc.Check(context.Background(), org, res.IR, manifest)
	if err != nil || !slices.ContainsFunc(issues, func(i model.ValidationIssue) bool {
		return i.Code == validate.DatasetBindingInvalid && i.Path == "/dataset_bindings/0/dataset_version_id"
	}) {
		t.Errorf("a snapshot that is not there: %v %v", issues, err)
	}
	svc.Datasets = unreadable{fmt.Errorf("reading: %w", context.DeadlineExceeded)}
	if _, err := svc.Check(context.Background(), org, res.IR, manifest); errors.As(err, &se) || !errors.Is(err, context.DeadlineExceeded) {
		t.Errorf("a read past its deadline: %v", err)
	}
	cancelled, cancel := context.WithCancel(context.Background())
	cancel()
	svc.Datasets = unreadable{cause}
	if _, err := svc.Check(cancelled, org, res.IR, manifest); errors.As(err, &se) || !errors.Is(err, context.Canceled) {
		t.Errorf("a cancelled request: %v", err)
	}
	// Without a manifest nothing is read, so nothing can fail.
	if issues, err := svc.Check(context.Background(), org, res.IR, nil); err != nil || slices.ContainsFunc(issues, func(i model.ValidationIssue) bool {
		return i.Severity == model.SeverityError
	}) {
		t.Errorf("no manifest: %v %v", issues, err)
	}
}

// subjects verifies a token that is the subject itself, of the test issuer.
type subjects struct{}

func (subjects) Verify(_ context.Context, token string) (auth.Identity, error) {
	return auth.Identity{Issuer: issuer, Subject: token, Expiry: time.Now().Add(time.Hour)}, nil
}

// flaky is dataset storage that serves snapshots, or fails with err from
// its from-th read on (1 is the first) once fail sets it.
type flaky struct {
	snapshots
	mu          sync.Mutex
	reads, from int
	err         error
}

func (f *flaky) Open(ctx context.Context, org, dsv string) (io.ReadCloser, error) {
	f.mu.Lock()
	f.reads++
	failing := f.err != nil && f.reads >= f.from
	err := f.err
	f.mu.Unlock()
	if failing {
		return nil, err
	}
	return f.snapshots.Open(ctx, org, dsv)
}

func (f *flaky) fail(from int, err error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.reads, f.from, f.err = 0, from, err
}

func (f *flaky) count() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.reads
}

// written is what a model write can change: the model's versions and
// latest version, its draft, the organization's audit events of the three
// writes that read snapshots, and the publish Idempotency-Key's row.
type written struct {
	versions, publishes, updates, resets, keys int
	latest, hash                               string
	revision                                   int64
}

func (e *env) written(t *testing.T, modelID, key string) written {
	t.Helper()
	ctx := context.Background()
	var p written
	audits := `SELECT count(*) FROM twin.audit_events WHERE organization_id=$1 AND action=$2`
	for _, q := range []struct {
		sql  string
		args []any
		dst  []any
	}{
		{`SELECT count(*) FROM twin.model_versions WHERE organization_id=$1 AND model_id=$2`, []any{e.org.ID, modelID}, []any{&p.versions}},
		{`SELECT latest_version_id FROM twin.models WHERE organization_id=$1 AND id=$2`, []any{e.org.ID, modelID}, []any{&p.latest}},
		{`SELECT revision, hash FROM twin.model_drafts WHERE organization_id=$1 AND model_id=$2`, []any{e.org.ID, modelID}, []any{&p.revision, &p.hash}},
		{audits, []any{e.org.ID, "model.publish"}, []any{&p.publishes}},
		{audits, []any{e.org.ID, "model.draft.update"}, []any{&p.updates}},
		{audits, []any{e.org.ID, "model.draft.reset"}, []any{&p.resets}},
		{`SELECT count(*) FROM twin.idempotency_keys WHERE organization_id=$1 AND operation_scope='publishVersion' AND key=$2`, []any{e.org.ID, key}, []any{&p.keys}},
	} {
		if err := e.admin.QueryRow(ctx, q.sql, q.args...).Scan(q.dst...); err != nil {
			t.Fatalf("%s: %v", q.sql, err)
		}
	}
	return p
}

// errorEnvelope is common.schema.json#/$defs/Error.
type errorEnvelope struct {
	Error struct {
		Code      string        `json:"code"`
		Message   string        `json:"message"`
		Details   []errorDetail `json:"details"`
		RequestID string        `json:"request_id"`
	} `json:"error"`
}

type errorDetail struct {
	Path   string `json:"path"`
	Reason string `json:"reason"`
	Code   string `json:"code"`
}

// handler is the model routes' HTTP handler, models.Register's (the one
// api.Handler mounts), over e.svc, logging into logs.
func (e *env) handler(logs *bytes.Buffer) http.Handler {
	mux := http.NewServeMux()
	models.Register(mux, slog.New(slog.NewJSONHandler(logs, nil)), subjects{}, e.svc.DB, e.svc)
	return mux
}

// call sends one request to h as the editor; an error answer must be the
// contract's envelope, its request_id the X-Request-ID.
func (e *env) call(t *testing.T, h http.Handler, method, path, contentType string, headers map[string]string, body string) (*httptest.ResponseRecorder, errorEnvelope) {
	t.Helper()
	r := httptest.NewRequest(method, path, strings.NewReader(body))
	r.Header.Set("Authorization", "Bearer "+e.org.Members[tenant.Editor].Subject)
	r.Header.Set("X-Organization-ID", e.org.ID)
	r.Header.Set("Content-Type", contentType)
	for k, v := range headers {
		r.Header.Set(k, v)
	}
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	var env errorEnvelope
	if w.Code >= 400 {
		if issues, err := validate.Document(w.Body.Bytes(), "common.schema.json#/$defs/Error", "RESPONSE"); err != nil || len(issues) > 0 {
			t.Errorf("%s is not a contract error: %v %v", w.Body, issues, err)
		}
		if err := json.Unmarshal(w.Body.Bytes(), &env); err != nil {
			t.Fatal(err)
		}
		if env.Error.RequestID != w.Header().Get("X-Request-ID") || env.Error.RequestID == "" {
			t.Errorf("request_id %q, X-Request-ID %q", env.Error.RequestID, w.Header().Get("X-Request-ID"))
		}
	}
	return w, env
}

// storageFailed checks a 503 DATASET_STORAGE_FAILED answer: the code, the
// message and the details; the cause is not sent but is in the log, with
// the request and the model.
func storageFailed(t *testing.T, what string, w *httptest.ResponseRecorder, env errorEnvelope, logs, modelID string) {
	t.Helper()
	if w.Code != 503 || env.Error.Code != "SERVICE_UNAVAILABLE" ||
		!slices.Equal(env.Error.Details, []errorDetail{{"", "the dataset storage failed", "DATASET_STORAGE_FAILED"}}) ||
		env.Error.Message != "the dataset snapshots could not be read; nothing was changed (retry later)" {
		t.Errorf("%s: %d %s", what, w.Code, w.Body)
	}
	if strings.Contains(w.Body.String(), "secret") {
		t.Errorf("%s: the cause is sent: %s", what, w.Body)
	}
	if !slices.ContainsFunc(strings.Split(strings.TrimSpace(logs), "\n"), func(line string) bool {
		var entry map[string]any
		return json.Unmarshal([]byte(line), &entry) == nil && entry["level"] == "WARN" && entry["request_id"] == env.Error.RequestID &&
			entry["model_id"] == modelID && strings.Contains(fmt.Sprint(entry["error"]), storageCause)
	}) {
		t.Errorf("%s: the cause is not logged with the request: %s", what, logs)
	}
}

// ended checks the 503 of a read that says the request ended: httpapi's
// answer to any ended request, without details, and no storage failure in
// the log.
func ended(t *testing.T, what string, w *httptest.ResponseRecorder, env errorEnvelope, logs string) {
	t.Helper()
	if w.Code != 503 || env.Error.Code != "SERVICE_UNAVAILABLE" || len(env.Error.Details) != 0 {
		t.Errorf("%s, a read past its deadline: %d %s", what, w.Code, w.Body)
	}
	if strings.Contains(logs, "DATASET_STORAGE_FAILED") || strings.Contains(logs, "dataset snapshots failed") {
		t.Errorf("%s: an ended request is logged as a storage failure: %s", what, logs)
	}
}

// boundModel is a model on whose base a dataset is bound (as an approved
// import will bind one, T-018), its clean draft following that base at
// revision 2; the dataset storage is f.
func (e *env) boundModel(t *testing.T) (m models.Model, base string, snap snapshotFixture, f *flaky) {
	t.Helper()
	ctx := context.Background()
	sc := e.scope(tenant.Editor)
	m = e.create(t, sc, createBody)
	snap = e.dataset(t, "dataset:review_duration")
	f = &flaky{snapshots: snapshots{e.org.ID + "/" + snap.binding.DatasetVersionID: snap.data}}
	e.svc.Datasets = f
	v1, err := e.svc.GetVersion(ctx, sc, m.LatestVersion.ID)
	if err != nil {
		t.Fatal(err)
	}
	base, err = e.svc.CommitVersion(ctx, sc, m.ID, bytes.Replace(v1.IR, []byte(`"name":"Pipeline"`), []byte(`"name":"With data"`), 1), []model.DatasetBinding{snap.binding})
	if err != nil {
		t.Fatal(err)
	}
	return m, base, snap, f
}

// H-022, D-069 (b), TEST-009: publishVersion through its HTTP handler, on
// a base that binds a dataset, when the binding stage cannot read the
// snapshot: in the candidate's check, or in the check of the draft that
// follows the new version (after the version's INSERT, in the same
// transaction). 503 SERVICE_UNAVAILABLE, details[0].code
// DATASET_STORAGE_FAILED, the cause logged and not sent; a read that says
// the request ended is the context's 503, without details; nothing is
// written either way (no version, no move of the model or the draft, no
// audit event, no answer kept for the key), so the same request with the
// same key publishes once storage answers again.
//
// The handler is models.Register's, the one api.Handler mounts; it is
// given a model service with dataset storage here, which api.Handler does
// not give it yet (T-018), so the route is exercised as it will run then.
func TestPublishSnapshotStorageFailure(t *testing.T) {
	e := setup(t)
	m, base, snap, store := e.boundModel(t)
	d, err := e.svc.PatchDraft(context.Background(), e.scope(tenant.Editor), requestID(), m.ID, 2, rename("Renamed"))
	if err != nil || d.Revision != 3 || d.BaseVersionID != base {
		t.Fatalf("the draft to publish: %+v %v", d, err)
	}
	var logs bytes.Buffer
	h := e.handler(&logs)
	publish := func(k string) (*httptest.ResponseRecorder, errorEnvelope) {
		t.Helper()
		logs.Reset()
		return e.call(t, h, "POST", "/v1/models/"+m.ID+"/versions", "application/json",
			map[string]string{"Idempotency-Key": k, "If-Match": `"draft-revision-3"`}, fmt.Sprintf(`{"expected_base_version_id":%q}`, base))
	}
	k := key()
	before := e.written(t, m.ID, k)
	if before.versions != 2 || before.latest != base || before.revision != 3 || before.keys != 0 {
		t.Fatalf("before: %+v", before)
	}

	for _, c := range []struct {
		name  string
		from  int // the read that fails
		reads int // the reads of the request
	}{
		{"the candidate's check", 1, 1},
		{"the check of the draft that follows the new version", 2, 2},
	} {
		store.fail(c.from, errors.New(storageCause))
		w, env := publish(k)
		storageFailed(t, c.name, w, env, logs.String(), m.ID)
		if n := store.count(); n != c.reads {
			t.Errorf("%s: %d reads, want %d", c.name, n, c.reads)
		}
		if after := e.written(t, m.ID, k); after != before {
			t.Errorf("%s: a refused publish wrote %+v (before %+v)", c.name, after, before)
		}
	}

	// A read that says the request ended: the context's error, which
	// httpapi answers as it answers any ended request.
	store.fail(1, fmt.Errorf("reading: %w", context.DeadlineExceeded))
	w, env := publish(k)
	ended(t, "publishVersion", w, env, logs.String())
	if after := e.written(t, m.ID, k); after != before {
		t.Errorf("a publish past its deadline wrote %+v (before %+v)", after, before)
	}

	// Storage answers again: the same request with the same key publishes.
	store.fail(0, nil)
	w, _ = publish(k)
	if w.Code != 201 {
		t.Fatalf("after storage recovers: %d %s", w.Code, w.Body)
	}
	matches(t, "ModelVersion", w.Body.Bytes())
	var v models.Version
	if err := json.Unmarshal(w.Body.Bytes(), &v); err != nil {
		t.Fatal(err)
	}
	after := e.written(t, m.ID, k)
	if after.versions != 3 || after.latest != v.ID || v.Version != 3 || after.publishes != before.publishes+1 || after.keys != 1 ||
		!slices.Equal(v.DatasetBindings, []model.DatasetBinding{snap.binding}) {
		t.Errorf("the publish after recovery: %+v %+v", after, v)
	}
}

// H-022 Q4, which the coordinator extended to the draft routes: patchDraft
// and resetDraft through their HTTP handler, on a base that binds a
// dataset, when the binding stage cannot read the snapshot (the patched
// draft's issues against the base's manifest; the latest version's
// content, which a reset makes the draft, against that version's). 503
// DATASET_STORAGE_FAILED, the cause logged and not sent; a read that says
// the request ended is the context's 503 without details; nothing is
// written either way (no new draft revision, no audit event, no version),
// so the same request with the same If-Match succeeds once storage
// answers again. Neither route takes an Idempotency-Key.
func TestDraftSnapshotStorageFailure(t *testing.T) {
	e := setup(t)
	m, base, _, store := e.boundModel(t)
	baseVersion, err := e.svc.GetVersion(context.Background(), e.scope(tenant.Viewer), base)
	if err != nil {
		t.Fatal(err)
	}
	var logs bytes.Buffer
	h := e.handler(&logs)
	patchDraft := func() (*httptest.ResponseRecorder, errorEnvelope) {
		t.Helper()
		logs.Reset()
		return e.call(t, h, "PATCH", "/v1/models/"+m.ID+"/draft", "application/json-patch+json",
			map[string]string{"If-Match": `"draft-revision-2"`}, string(rename("Renamed")))
	}
	resetDraft := func() (*httptest.ResponseRecorder, errorEnvelope) {
		t.Helper()
		logs.Reset()
		return e.call(t, h, "POST", "/v1/models/"+m.ID+"/draft/reset", "application/json",
			map[string]string{"If-Match": `"draft-revision-3"`}, fmt.Sprintf(`{"expected_latest_version_id":%q,"confirm_discard":true}`, base))
	}
	before := e.written(t, m.ID, "")
	if before.versions != 2 || before.latest != base || before.revision != 2 || before.hash != baseVersion.Hash {
		t.Fatalf("before: %+v", before)
	}

	for _, route := range []struct {
		name  string
		send  func() (*httptest.ResponseRecorder, errorEnvelope)
		etag  string
		check func(before, after written) bool
	}{
		{"patchDraft", patchDraft, `"draft-revision-3"`, func(before, after written) bool {
			return after.updates == before.updates+1 && after.resets == before.resets && after.hash != baseVersion.Hash
		}},
		{"resetDraft", resetDraft, `"draft-revision-4"`, func(before, after written) bool {
			return after.resets == before.resets+1 && after.updates == before.updates && after.hash == baseVersion.Hash
		}},
	} {
		store.fail(1, errors.New(storageCause))
		w, env := route.send()
		storageFailed(t, route.name, w, env, logs.String(), m.ID)
		if n := store.count(); n != 1 {
			t.Errorf("%s: %d reads, want 1", route.name, n)
		}
		if after := e.written(t, m.ID, ""); after != before {
			t.Errorf("%s: a refused write wrote %+v (before %+v)", route.name, after, before)
		}
		store.fail(1, fmt.Errorf("reading: %w", context.DeadlineExceeded))
		w, env = route.send()
		ended(t, route.name, w, env, logs.String())
		if after := e.written(t, m.ID, ""); after != before {
			t.Errorf("%s past its deadline wrote %+v (before %+v)", route.name, after, before)
		}
		// Storage answers again: the same request succeeds, one revision later.
		store.fail(0, nil)
		w, _ = route.send()
		if w.Code != 200 || w.Header().Get("ETag") != route.etag {
			t.Fatalf("%s after storage recovers: %d %s %s", route.name, w.Code, w.Header().Get("ETag"), w.Body)
		}
		matches(t, "Draft", w.Body.Bytes())
		after := e.written(t, m.ID, "")
		if after.revision != before.revision+1 || after.versions != before.versions || after.latest != base || !route.check(before, after) {
			t.Errorf("%s after recovery: %+v (before %+v)", route.name, after, before)
		}
		before = after
	}
}
