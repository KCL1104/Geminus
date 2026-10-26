package proposals

import (
	"encoding/base64"
	"encoding/json/v2"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/kcl1104/geminus/internal/httpapi"
	"github.com/kcl1104/geminus/internal/ids"
	"github.com/kcl1104/geminus/internal/model"
	"github.com/kcl1104/geminus/internal/model/validate"
	"github.com/kcl1104/geminus/internal/storage/idempotency"
	store "github.com/kcl1104/geminus/internal/storage/proposals"
	"github.com/kcl1104/geminus/internal/tenant"
)

// The proposal routes are the contract's: method, path and roles
// (planning/api-operations.json, from openapi.yaml); approve and reject
// are T-026's.
func TestRoutesMatchContract(t *testing.T) {
	data, err := os.ReadFile(filepath.Join("..", "..", "planning", "api-operations.json"))
	if err != nil {
		t.Fatal(err)
	}
	type operation struct {
		Method      string   `json:"method"`
		Path        string   `json:"path"`
		OperationID string   `json:"operation_id"`
		Roles       []string `json:"roles"`
	}
	var doc struct {
		Operations []operation `json:"operations"`
	}
	if err := json.Unmarshal(data, &doc); err != nil {
		t.Fatal(err)
	}
	var got []string
	for _, r := range Routes {
		got = append(got, r.OperationID)
		i := slices.IndexFunc(doc.Operations, func(o operation) bool { return o.OperationID == r.OperationID })
		if i < 0 {
			t.Errorf("%s is not a contract operation", r.OperationID)
			continue
		}
		o := doc.Operations[i]
		var roles []string
		for _, x := range r.Roles {
			roles = append(roles, string(x))
		}
		if o.Method != r.Method || o.Path != r.Path || !slices.Equal(slices.Sorted(slices.Values(o.Roles)), slices.Sorted(slices.Values(roles))) {
			t.Errorf("%s: route %s %s %v, contract %s %s %v", r.OperationID, r.Method, r.Path, roles, o.Method, o.Path, o.Roles)
		}
	}
	if want := []string{"createProposal", "listProposals", "getProposal", "createImageProposal"}; !slices.Equal(got, want) {
		t.Errorf("routes %v, want %v", got, want)
	}
}

// Each service error has its contract code; AI being off is 503 with the
// code AI_SPEC §2 names.
func TestRefused(t *testing.T) {
	issue := model.NewIssue(validate.ConstraintFailed, "/intent", "white space", model.SeverityError)
	for err, want := range map[error]httpapi.Code{
		ErrNotFound:      httpapi.NotFound,
		ErrStaleBase:     httpapi.ModelVersionConflict,
		ErrInvalidCursor: httpapi.InvalidRequest,
		ErrInvalidLimit:  httpapi.InvalidRequest,
		&RequestError{Issues: []model.ValidationIssue{issue}}: httpapi.RequestValidationFailed,
		&UnavailableError{Availability: NotConfigured}:        httpapi.AIProviderNotConfigured,
		&UnavailableError{Availability: PolicyNotApproved}:    httpapi.AIPolicyNotApproved,
		idempotency.ErrConflict:                               httpapi.IdempotencyKeyConflict,
		idempotency.ErrInProgress:                             httpapi.IdempotencyKeyConflict,
	} {
		var he *httpapi.Error
		if !errors.As(Refused(err), &he) || he.Code != want {
			t.Errorf("%v: %v, want %s", err, Refused(err), want)
		}
	}
	var he *httpapi.Error
	if errors.As(Refused(&RequestError{Issues: []model.ValidationIssue{issue}}), &he); he.Details[0].Path != "/intent" || he.Details[0].Code != validate.ConstraintFailed {
		t.Errorf("details %+v", he.Details)
	}
	// Tenant and other errors pass through for httpapi to classify.
	if Refused(tenant.ErrForbidden) != tenant.ErrForbidden {
		t.Error("ErrForbidden was mapped")
	}
	// A service that was not told otherwise refuses (fail closed).
	if (Service{}).AI != NotConfigured {
		t.Error("the zero Service accepts proposals")
	}
}

func str(s string) *string { return &s }

func f64(v float64) *float64 { return &v }

// decidedAt is when row's decision took effect, in a zone that is not UTC.
var decidedAt = time.Date(2026, 10, 20, 10, 15, 0, 0, time.FixedZone("x", 3600))

// row is a stored proposal in status, with every column a state might
// hold, so Public's choice of what to show is tested.
func row(status store.Status) store.Proposal {
	return store.Proposal{ID: "aip_01M4YDQK00ADN261CME3WV5HZ6", OrganizationID: "org_01M4YDQK00Y5H2929M9WWZWTVH",
		ModelID: "mdl_01M4YDQK004AGVNEGJQJ1NMM0N", BaseVersionID: "mv_01M4YDQK00FRYWV7GS7X1QCXZR", Mode: "edit", Status: status,
		Intent: "raise a capacity", OutputLocale: "en", IDPool: []byte(`{}`),
		Summary: str("Raise a capacity."), Assumptions: []string{"Nothing else changes."}, ExpectedEffects: []string{"Waiting may fall."},
		Questions: []string{"Which one?"}, Patch: []byte(`[{"op":"test","path":"/resources/1/id","value":"x"},{"op":"replace","path":"/resources/1/capacity","value":6}]`),
		PatchHash:  str("sha256:" + strings.Repeat("0", 64)),
		ServerRisk: str("low"), ProviderRisk: str("medium"), Risk: str("medium"), Confidence: f64(0.9),
		Validation:       []byte(`{"valid":true,"issues":[]}`),
		ProviderMetadata: []byte(`{"provider":"fake"}`),
		Failure:          []byte(`{"code":"AI_RATE_LIMITED","message":"the AI provider kept refusing the request for its rate limit","retryable":true}`),
		NewVersionID:     str("mv_01M4YDQK00FRYWV7GS7X1QCXZS"),
		DecidedBy:        str("usr_01M4YDQK0016KTK4JM6KAHZ6BS"), DecidedAt: &decidedAt, DecisionComment: str("Reviewed."),
		CreatedBy: "usr_01M4YDQK0016KTK4JM6KAHZ6BR", CreatedAt: time.Date(2026, 10, 20, 8, 30, 0, 0, time.FixedZone("x", 3600)),
		RequestID: "req_01M4YDQK00V6H8S0ST4F7510KR"}
}

func members(t *testing.T, p Proposal) map[string]bool {
	t.Helper()
	data, err := json.Marshal(p)
	if err != nil {
		t.Fatal(err)
	}
	if issues, err := validate.Document(data, "proposal.schema.json", "RESPONSE"); err != nil || len(issues) > 0 {
		t.Fatalf("%s does not match proposal.schema.json: %v %v", data, issues, err)
	}
	var m map[string]any
	if err := json.Unmarshal(data, &m); err != nil {
		t.Fatal(err)
	}
	out := map[string]bool{}
	for k := range m {
		out[k] = true
	}
	return out
}

// Every state's resource matches proposal.schema.json and holds what the
// state does, nothing more: a generating proposal only the server-bound
// fields, needs_input no changes, invalid only its validation (nothing the
// provider wrote), failed only its error (AI_SPEC §4); only a decided one
// its decision record, and a conflicted one no comment (T-026, D-068).
func TestPublicMatchesTheContract(t *testing.T) {
	base := []string{"proposal_id", "organization_id", "model_id", "base_version_id", "mode", "status", "created_by", "created_at", "requires_confirmation"}
	reviewed := append(slices.Clone(base), "summary", "assumptions", "risk_level", "confidence", "changes", "expected_effects", "validation",
		"patch_hash", "dataset_bindings", "server_risk_level", "provider_risk_claim")
	asked := append(slices.Clone(base), "summary", "assumptions", "questions")
	decided := []string{"decided_by", "decided_at", "decision_comment"}
	for _, c := range []struct {
		status store.Status
		patch  bool
		want   []string
	}{
		{store.Generating, true, base},
		{store.NeedsInput, false, asked},
		{store.Ready, true, reviewed},
		{store.Conflicted, true, append(slices.Clone(reviewed), "decided_by", "decided_at")},
		{store.Approved, true, slices.Concat(reviewed, []string{"new_version_id"}, decided)},
		{store.Rejected, true, slices.Concat(reviewed, decided)},
		{store.Rejected, false, slices.Concat(asked, decided)},
		{store.Invalid, false, append(slices.Clone(base), "validation")},
		{store.Failed, false, append(slices.Clone(base), "error")},
	} {
		r := row(c.status)
		if !c.patch {
			r.Patch = nil
		}
		if c.status == store.Invalid {
			r.Validation = []byte(`{"valid":false,"issues":[{"code":"ID_POOL_VIOLATION","path":"/nodes/9/id","message":"m","severity":"error"}]}`)
		}
		p, err := Public(r, "req_01M4YDQK00V6H8S0ST4F7510KS")
		if err != nil {
			t.Fatalf("%s: %v", c.status, err)
		}
		got := members(t, p)
		var names []string
		for k := range got {
			names = append(names, k)
		}
		if slices.Sort(names); !slices.Equal(names, slices.Sorted(slices.Values(c.want))) {
			t.Errorf("%s (patch %v): members %v, want %v", c.status, c.patch, names, slices.Sorted(slices.Values(c.want)))
		}
		if p.CreatedAt != "2026-10-20T07:30:00Z" {
			t.Errorf("created_at %s is not UTC", p.CreatedAt)
		}
		if p.DecidedAt != nil && *p.DecidedAt != "2026-10-20T09:15:00Z" {
			t.Errorf("decided_at %s is not UTC", *p.DecidedAt)
		}
	}
	// A decided proposal without its decision record is the server's
	// fault, never served without one (0001, 0013 require it).
	for _, s := range []store.Status{store.Approved, store.Rejected, store.Conflicted} {
		r := row(s)
		r.DecidedBy = nil
		if _, err := Public(r, ""); err == nil {
			t.Errorf("%s without its decider was served", s)
		}
		r = row(s)
		r.DecidedAt = nil
		if _, err := Public(r, ""); err == nil {
			t.Errorf("%s without its time was served", s)
		}
	}
	// Without a comment, a decision shows none.
	r0 := row(store.Rejected)
	r0.DecisionComment = nil
	if p, err := Public(r0, ""); err != nil || p.DecisionComment != nil {
		t.Errorf("%+v %v", p.DecisionComment, err)
	} else {
		members(t, p)
	}
	// The stored patch is served canonical (RFC 8785): jsonb's spacing and
	// member order never reach the client.
	r := row(store.Ready)
	r.Patch = []byte(`[{"path": "/resources/1/id", "value": "x", "op": "test"}, {"op": "replace", "value": 6, "path": "/resources/1/capacity"}]`)
	p, err := Public(r, "")
	if err != nil {
		t.Fatal(err)
	}
	if want := `[{"op":"test","path":"/resources/1/id","value":"x"},{"op":"replace","path":"/resources/1/capacity","value":6}]`; string(p.Changes) != want {
		t.Errorf("changes %s", p.Changes)
	}
	if _, err := Public(row("publishing"), ""); err == nil {
		t.Error("an unknown status was served")
	}
}

// CB-40 (c), D-061 PROPOSED: a failed proposal's error is its job's
// failure in the envelope; the failure's own code stays in
// details[0].code, error.code is the closed catalogue's.
func TestFailureErrors(t *testing.T) {
	for code, want := range map[string]string{
		"AI_PROVIDER_NOT_CONFIGURED": "AI_PROVIDER_NOT_CONFIGURED",
		"AI_POLICY_NOT_APPROVED":     "AI_POLICY_NOT_APPROVED",
		"AI_RATE_LIMITED":            "RATE_LIMITED",
		"AI_CONCURRENCY_LIMITED":     "RATE_LIMITED",
		"AI_TIMEOUT":                 "SERVICE_UNAVAILABLE",
		"AI_UNAVAILABLE":             "SERVICE_UNAVAILABLE",
		"AI_CREDENTIALS_EXHAUSTED":   "SERVICE_UNAVAILABLE",
		"LEASE_EXPIRED":              "SERVICE_UNAVAILABLE",
		"WORKER_STOPPED":             "SERVICE_UNAVAILABLE",
		"DATASET_STORAGE_FAILED":     "SERVICE_UNAVAILABLE",
		"AI_CONTEXT_TOO_LARGE":       "PAYLOAD_TOO_LARGE",
		"AI_QUOTA_EXCEEDED":          "RATE_LIMITED",        // T-053, D-111: a spend limit; try again later
		"AI_SPEND_UNAVAILABLE":       "SERVICE_UNAVAILABLE", // T-053: the ledger could not reserve
		// T-056: the photo pipeline's failures.
		"PHOTO_STORAGE_FAILED":   "SERVICE_UNAVAILABLE",        // the stored image could not be read (retried)
		"PHOTO_NOT_SUPPORTED":    "REQUEST_VALIDATION_FAILED",  // the model's domain takes no photos
		"AI_IMAGE_UNSUPPORTED":   "AI_PROVIDER_NOT_CONFIGURED", // the configured provider sends no images
		"PHOTO_CORRUPT":          "INTERNAL_ERROR",             // the stored bytes are not the record's
		"AI_REFUSED":             "INTERNAL_ERROR",
		"AI_OUTPUT_TRUNCATED":    "INTERNAL_ERROR",
		"AI_REQUEST_REJECTED":    "INTERNAL_ERROR",
		"AI_INVALID_REQUEST":     "INTERNAL_ERROR",
		"PROPOSAL_INPUT_INVALID": "INTERNAL_ERROR",
		"JOB_CANCELLED":          "INTERNAL_ERROR",
		"INTERNAL":               "INTERNAL_ERROR",
		"NO_RESULT":              "INTERNAL_ERROR",
	} {
		r := row(store.Failed)
		r.Failure = []byte(`{"code":"` + code + `","message":"m","retryable":false}`)
		p, err := Public(r, "")
		if err != nil {
			t.Fatalf("%s: %v", code, err)
		}
		members(t, p)
		if p.Error.Error.Code != want || p.Error.Error.Details[0].Code != code || p.Error.Error.RequestID != r.RequestID {
			t.Errorf("%s: %+v, want %s", code, p.Error.Error, want)
		}
		if _, ok := httpapi.Status[httpapi.Code(want)]; !ok {
			t.Errorf("%s is not a contract error code", want)
		}
	}
	// The creating request when the job's payload is gone; a generic
	// message when the failure has none.
	r := row(store.Failed)
	r.RequestID, r.Failure = "", []byte(`{"code":"INTERNAL","message":"","retryable":false}`)
	if p, err := Public(r, "req_01M4YDQK00V6H8S0ST4F7510KS"); err != nil || p.Error.Error.RequestID != "req_01M4YDQK00V6H8S0ST4F7510KS" || p.Error.Error.Message == "" {
		t.Errorf("%+v %v", p.Error, err)
	}
	// A stored failure that is not one is an internal error, not a guess.
	for _, bad := range []string{`{}`, `{"code":"lower","message":"m","retryable":false}`, `[]`} {
		r.Failure = []byte(bad)
		if _, err := Public(r, ""); err == nil {
			t.Errorf("%s was served", bad)
		}
	}
}

func TestCursor(t *testing.T) {
	org, mdl := ids.New("org"), ids.New("mdl")
	pos := store.Position{CreatedAt: time.Date(2026, 10, 20, 1, 2, 3, 456789000, time.UTC), ID: ids.New("aip")}
	c := encodeCursor(org, mdl, pos)
	got, err := decodeCursor(c, org, mdl)
	if err != nil || !got.CreatedAt.Equal(pos.CreatedAt) || got.ID != pos.ID {
		t.Fatalf("%v %v", got, err)
	}
	for name, bad := range map[string]func() (store.Position, error){
		"another organization": func() (store.Position, error) { return decodeCursor(c, ids.New("org"), mdl) },
		"another model":        func() (store.Position, error) { return decodeCursor(c, org, ids.New("mdl")) },
		"not base64":           func() (store.Position, error) { return decodeCursor("!!", org, mdl) },
		"a run cursor": func() (store.Position, error) {
			data, _ := json.Marshal(cursor{Version: 1, Org: org, List: "simulations", Model: mdl, After: timestamp(pos.CreatedAt), ID: pos.ID})
			return decodeCursor(base64.RawURLEncoding.EncodeToString(data), org, mdl)
		},
		"another kind of ID": func() (store.Position, error) {
			data, _ := json.Marshal(cursor{Version: 1, Org: org, List: cursorList, Model: mdl, After: timestamp(pos.CreatedAt), ID: ids.New("sim")})
			return decodeCursor(base64.RawURLEncoding.EncodeToString(data), org, mdl)
		},
		"an extra member": func() (store.Position, error) {
			data := []byte(`{"v":1,"o":"` + org + `","l":"ai-proposals","m":"` + mdl + `","t":"` + timestamp(pos.CreatedAt) + `","i":"` + pos.ID + `","x":1}`)
			return decodeCursor(base64.RawURLEncoding.EncodeToString(data), org, mdl)
		},
	} {
		if _, err := bad(); !errors.Is(err, ErrInvalidCursor) {
			t.Errorf("%s: %v", name, err)
		}
	}
}
