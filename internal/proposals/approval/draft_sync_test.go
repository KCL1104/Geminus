package approval_test

// T-054 round 2: H-022 Q4 ("一律改為 503"), which the coordinator extended
// to the draft sync of an approval. DB-backed; without
// GEMINUS_TEST_DATABASE_URL it skips (NOT RUN).

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
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/kcl1104/geminus/internal/auth"
	"github.com/kcl1104/geminus/internal/proposals/approval"
	"github.com/kcl1104/geminus/internal/storage/objects"
	"github.com/kcl1104/geminus/internal/tenant"
)

// subjects verifies a token that is the subject itself, of the test issuer.
type subjects struct{}

func (subjects) Verify(_ context.Context, token string) (auth.Identity, error) {
	return auth.Identity{Issuer: issuer, Subject: token, Expiry: time.Now().Add(time.Hour)}, nil
}

// failingFrom is object storage that serves objects, or, once fail sets
// err, fails from its from-th read on (1 is the first).
type failingFrom struct {
	objects.Store
	mu          sync.Mutex
	reads, from int
	err         error
}

func (f *failingFrom) Get(ctx context.Context, key string) (io.ReadCloser, error) {
	f.mu.Lock()
	f.reads++
	failing := f.err != nil && f.reads >= f.from
	err := f.err
	f.mu.Unlock()
	if failing {
		return nil, err
	}
	return f.Store.Get(ctx, key)
}

func (f *failingFrom) fail(from int, err error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.reads, f.from, f.err = 0, from, err
}

func (f *failingFrom) count() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.reads
}

// H-022 Q4, extended by the coordinator to the draft sync: an editor
// approves, through the HTTP handler, an edit proposal on a base that
// binds a dataset. The revalidation reads the snapshot (the 1st read);
// the clean draft's move to the new version reads it again (the 2nd, after
// the version's INSERT, in the same transaction) and fails. 503
// DATASET_STORAGE_FAILED as for the revalidation, the cause logged and not
// sent; nothing is committed: no version, the model and its draft as they
// were, the proposal still ready and undecided, no proposal.approve audit
// event, no answer kept for the key. A read that says the request ended is
// the context's 503, without details, and commits nothing either. Once
// storage answers, the same request with the same key approves.
func TestApprovalDraftSyncStorageFailure(t *testing.T) {
	e := setup(t)
	m := e.model(t)
	base, _ := e.datasetVersion(t, m)
	p := e.ready(t, m.ID, base, "edit", raiseCI)
	before := e.snap(t, m.ID)
	if before.latest != base || before.draftBase != base {
		t.Fatalf("the draft is not clean on the base: %+v", before)
	}
	store := &failingFrom{Store: e.objects}
	svc := *e.svc
	svc.Objects = store
	var logs bytes.Buffer
	mux := http.NewServeMux()
	approval.Register(mux, slog.New(slog.NewJSONHandler(&logs, nil)), subjects{}, svc.DB, &svc)
	k := key()
	type envelope struct {
		Error struct {
			Code    string `json:"code"`
			Message string `json:"message"`
			Details []struct {
				Path   string `json:"path"`
				Reason string `json:"reason"`
				Code   string `json:"code"`
			} `json:"details"`
			RequestID string `json:"request_id"`
		} `json:"error"`
	}
	approve := func() (*httptest.ResponseRecorder, envelope) {
		t.Helper()
		logs.Reset()
		r := httptest.NewRequest("POST", "/v1/ai-proposals/"+p.ProposalID+"/approve", strings.NewReader(fmt.Sprintf(`{"expected_base_version_id":%q}`, base)))
		r.Header.Set("Authorization", "Bearer "+e.org.Members[tenant.Editor].Subject)
		r.Header.Set("X-Organization-ID", e.org.ID)
		r.Header.Set("Idempotency-Key", k)
		r.Header.Set("Content-Type", "application/json")
		w := httptest.NewRecorder()
		mux.ServeHTTP(w, r)
		var env envelope
		if w.Code >= 400 {
			matches(t, "common.schema.json#/$defs/Error", w.Body.Bytes())
			if err := json.Unmarshal(w.Body.Bytes(), &env); err != nil {
				t.Fatal(err)
			}
			if env.Error.RequestID != w.Header().Get("X-Request-ID") || env.Error.RequestID == "" {
				t.Errorf("request_id %q, X-Request-ID %q", env.Error.RequestID, w.Header().Get("X-Request-ID"))
			}
		}
		return w, env
	}
	nothing := func(what string) {
		t.Helper()
		if s := e.snap(t, m.ID); s != before {
			t.Errorf("%s: the model changed: %+v (before %+v)", what, s, before)
		}
		if got := e.get(t, p.ProposalID); got.Status != "ready" || got.DecidedBy != nil {
			t.Errorf("%s: the proposal moved: %+v", what, got)
		}
		if n := e.count(t, `SELECT count(*) FROM twin.audit_events WHERE organization_id=$1 AND target_id=$2 AND action='proposal.approve'`,
			e.org.ID, p.ProposalID); n != 0 {
			t.Errorf("%s: %d approve audit events", what, n)
		}
		if n := e.count(t, `SELECT count(*) FROM twin.idempotency_keys WHERE organization_id=$1 AND operation_scope='approveProposal' AND key=$2`,
			e.org.ID, k); n != 0 {
			t.Errorf("%s: the key keeps an answer", what)
		}
	}

	const cause = "open /srv/objects/tenant/secret: input/output error"
	store.fail(2, errors.New(cause))
	w, env := approve()
	if w.Code != 503 || env.Error.Code != "SERVICE_UNAVAILABLE" || len(env.Error.Details) != 1 ||
		env.Error.Details[0].Path != "" || env.Error.Details[0].Reason != "the dataset storage failed" || env.Error.Details[0].Code != "DATASET_STORAGE_FAILED" ||
		env.Error.Message != "the dataset snapshots could not be read; nothing was changed (retry later)" {
		t.Errorf("the draft sync's read fails: %d %s", w.Code, w.Body)
	}
	if n := store.count(); n != 2 {
		t.Errorf("%d reads, want 2 (the revalidation's, then the draft sync's)", n)
	}
	if strings.Contains(w.Body.String(), "secret") {
		t.Errorf("the cause is sent: %s", w.Body)
	}
	if !slices.ContainsFunc(strings.Split(strings.TrimSpace(logs.String()), "\n"), func(line string) bool {
		var entry map[string]any
		return json.Unmarshal([]byte(line), &entry) == nil && entry["level"] == "WARN" && entry["request_id"] == env.Error.RequestID &&
			entry["proposal_id"] == p.ProposalID && strings.Contains(fmt.Sprint(entry["error"]), cause)
	}) {
		t.Errorf("the cause is not logged with the request: %s", logs.String())
	}
	nothing("the draft sync's read fails")

	store.fail(2, fmt.Errorf("reading: %w", context.DeadlineExceeded))
	if w, env := approve(); w.Code != 503 || env.Error.Code != "SERVICE_UNAVAILABLE" || len(env.Error.Details) != 0 {
		t.Errorf("the draft sync's read past its deadline: %d %s", w.Code, w.Body)
	}
	nothing("the draft sync's read past its deadline")

	// Storage answers again: the same request with the same key approves.
	store.fail(0, nil)
	w, _ = approve()
	if w.Code != 201 {
		t.Fatalf("after storage recovers: %d %s", w.Code, w.Body)
	}
	var a approval.EditApproval
	if err := json.Unmarshal(w.Body.Bytes(), &a); err != nil {
		t.Fatal(err)
	}
	after := e.snap(t, m.ID)
	if after.latest != a.NewVersionID || after.versions != before.versions+1 || after.draftBase != a.NewVersionID || a.DraftOutOfDate {
		t.Errorf("the approval after recovery: %+v %+v", after, a)
	}
	if got := e.get(t, p.ProposalID); got.Status != "approved" {
		t.Errorf("the proposal after recovery: %+v", got)
	}
}
