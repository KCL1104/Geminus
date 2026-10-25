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

// published is what a publish writes: the model's versions and latest
// version, its draft, the organization's model.publish audit events and
// the Idempotency-Key's row.
type published struct {
	versions, audits, keys int
	latest, hash           string
	revision               int64
}

func (e *env) published(t *testing.T, modelID, key string) published {
	t.Helper()
	ctx := context.Background()
	var p published
	for _, q := range []struct {
		sql  string
		args []any
		dst  []any
	}{
		{`SELECT count(*) FROM twin.model_versions WHERE organization_id=$1 AND model_id=$2`, []any{e.org.ID, modelID}, []any{&p.versions}},
		{`SELECT latest_version_id FROM twin.models WHERE organization_id=$1 AND id=$2`, []any{e.org.ID, modelID}, []any{&p.latest}},
		{`SELECT revision, hash FROM twin.model_drafts WHERE organization_id=$1 AND model_id=$2`, []any{e.org.ID, modelID}, []any{&p.revision, &p.hash}},
		{`SELECT count(*) FROM twin.audit_events WHERE organization_id=$1 AND action='model.publish'`, []any{e.org.ID}, []any{&p.audits}},
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
	ctx := context.Background()
	sc := e.scope(tenant.Editor)
	editor := e.org.Members[tenant.Editor]
	m := e.create(t, sc, createBody)
	snap := e.dataset(t, "dataset:review_duration")
	store := &flaky{snapshots: snapshots{e.org.ID + "/" + snap.binding.DatasetVersionID: snap.data}}
	e.svc.Datasets = store
	// A base that binds the dataset, as an approved import will (T-018);
	// the clean draft follows it. Then a change to publish.
	v1, err := e.svc.GetVersion(ctx, sc, m.LatestVersion.ID)
	if err != nil {
		t.Fatal(err)
	}
	base, err := e.svc.CommitVersion(ctx, sc, m.ID, bytes.Replace(v1.IR, []byte(`"name":"Pipeline"`), []byte(`"name":"With data"`), 1), []model.DatasetBinding{snap.binding})
	if err != nil {
		t.Fatal(err)
	}
	d, err := e.svc.PatchDraft(ctx, sc, requestID(), m.ID, 2, rename("Renamed"))
	if err != nil || d.Revision != 3 || d.BaseVersionID != base {
		t.Fatalf("the draft to publish: %+v %v", d, err)
	}

	var logs bytes.Buffer
	mux := http.NewServeMux()
	models.Register(mux, slog.New(slog.NewJSONHandler(&logs, nil)), subjects{}, e.svc.DB, e.svc)
	publish := func(k string) (*httptest.ResponseRecorder, errorEnvelope) {
		t.Helper()
		r := httptest.NewRequest("POST", "/v1/models/"+m.ID+"/versions", strings.NewReader(fmt.Sprintf(`{"expected_base_version_id":%q}`, base)))
		r.Header.Set("Authorization", "Bearer "+editor.Subject)
		r.Header.Set("X-Organization-ID", e.org.ID)
		r.Header.Set("Idempotency-Key", k)
		r.Header.Set("If-Match", `"draft-revision-3"`)
		r.Header.Set("Content-Type", "application/json")
		w := httptest.NewRecorder()
		mux.ServeHTTP(w, r)
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
	k := key()
	before := e.published(t, m.ID, k)
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
		logs.Reset()
		w, env := publish(k)
		if w.Code != 503 || env.Error.Code != "SERVICE_UNAVAILABLE" ||
			!slices.Equal(env.Error.Details, []errorDetail{{"", "the dataset storage failed", "DATASET_STORAGE_FAILED"}}) ||
			env.Error.Message != "the dataset snapshots could not be read; nothing was published (retry later)" {
			t.Errorf("%s: %d %s", c.name, w.Code, w.Body)
		}
		if n := store.count(); n != c.reads {
			t.Errorf("%s: %d reads, want %d", c.name, n, c.reads)
		}
		if strings.Contains(w.Body.String(), "secret") {
			t.Errorf("%s: the cause is sent: %s", c.name, w.Body)
		}
		// The cause, with the request, is in the log.
		if !slices.ContainsFunc(strings.Split(strings.TrimSpace(logs.String()), "\n"), func(line string) bool {
			var entry map[string]any
			return json.Unmarshal([]byte(line), &entry) == nil && entry["level"] == "WARN" && entry["request_id"] == env.Error.RequestID &&
				entry["model_id"] == m.ID && strings.Contains(fmt.Sprint(entry["error"]), storageCause)
		}) {
			t.Errorf("%s: the cause is not logged with the request: %s", c.name, logs.String())
		}
		if after := e.published(t, m.ID, k); after != before {
			t.Errorf("%s: a refused publish wrote %+v (before %+v)", c.name, after, before)
		}
	}

	// A read that says the request ended: the context's error, which
	// httpapi answers as it answers any ended request.
	store.fail(1, fmt.Errorf("reading: %w", context.DeadlineExceeded))
	logs.Reset()
	if w, env := publish(k); w.Code != 503 || env.Error.Code != "SERVICE_UNAVAILABLE" || len(env.Error.Details) != 0 {
		t.Errorf("a read past its deadline: %d %s", w.Code, w.Body)
	}
	if strings.Contains(logs.String(), "DATASET_STORAGE_FAILED") || strings.Contains(logs.String(), "dataset snapshots failed") {
		t.Errorf("an ended request is logged as a storage failure: %s", logs.String())
	}
	if after := e.published(t, m.ID, k); after != before {
		t.Errorf("a publish past its deadline wrote %+v (before %+v)", after, before)
	}

	// Storage answers again: the same request with the same key publishes.
	store.fail(0, nil)
	w, _ := publish(k)
	if w.Code != 201 {
		t.Fatalf("after storage recovers: %d %s", w.Code, w.Body)
	}
	matches(t, "ModelVersion", w.Body.Bytes())
	var v models.Version
	if err := json.Unmarshal(w.Body.Bytes(), &v); err != nil {
		t.Fatal(err)
	}
	after := e.published(t, m.ID, k)
	if after.versions != 3 || after.latest != v.ID || v.Version != 3 || after.audits != before.audits+1 || after.keys != 1 ||
		!slices.Equal(v.DatasetBindings, []model.DatasetBinding{snap.binding}) {
		t.Errorf("the publish after recovery: %+v %+v", after, v)
	}
}
