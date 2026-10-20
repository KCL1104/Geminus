package approval

// Tests without a database: the routes against the contract, the error
// mapping, the scenario name, the comment and, above all, recheck, the
// server's review of a stored proposal again at its approval, which trusts
// none of the stored results (AI_SPEC §6, SECURITY §5; D-069 (b)).

import (
	"context"
	"encoding/json/v2"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/kcl1104/geminus/internal/ai/idpool"
	"github.com/kcl1104/geminus/internal/ai/provider"
	"github.com/kcl1104/geminus/internal/ai/review"
	"github.com/kcl1104/geminus/internal/contracttest"
	"github.com/kcl1104/geminus/internal/domainpack"
	"github.com/kcl1104/geminus/internal/domainpack/softwaredelivery"
	"github.com/kcl1104/geminus/internal/httpapi"
	"github.com/kcl1104/geminus/internal/model"
	"github.com/kcl1104/geminus/internal/model/hashing"
	"github.com/kcl1104/geminus/internal/model/patch"
	"github.com/kcl1104/geminus/internal/model/validate"
	"github.com/kcl1104/geminus/internal/proposals"
	"github.com/kcl1104/geminus/internal/storage/idempotency"
	store "github.com/kcl1104/geminus/internal/storage/proposals"
	"github.com/kcl1104/geminus/internal/tenant"
)

func TestRoutesMatchContract(t *testing.T) {
	data, err := os.ReadFile(filepath.Join("..", "..", "..", "planning", "api-operations.json"))
	if err != nil {
		t.Fatal(err)
	}
	var doc struct {
		Operations []struct {
			Method      string   `json:"method"`
			Path        string   `json:"path"`
			OperationID string   `json:"operation_id"`
			Roles       []string `json:"roles"`
		} `json:"operations"`
	}
	if err := json.Unmarshal(data, &doc); err != nil {
		t.Fatal(err)
	}
	var got []string
	for _, r := range Routes {
		got = append(got, r.OperationID)
		i := slices.IndexFunc(doc.Operations, func(o struct {
			Method      string   `json:"method"`
			Path        string   `json:"path"`
			OperationID string   `json:"operation_id"`
			Roles       []string `json:"roles"`
		}) bool {
			return o.OperationID == r.OperationID
		})
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
	if want := []string{"approveProposal", "rejectProposal"}; !slices.Equal(got, want) {
		t.Errorf("routes %v, want %v", got, want)
	}
	// Only editors and owners decide; a viewer never (SECURITY §2).
	for _, r := range Routes {
		if slices.Contains(r.Roles, tenant.Viewer) {
			t.Errorf("%s lets a viewer decide", r.OperationID)
		}
	}
}

// Each service error has its contract code and JSON Pointer.
func TestRefused(t *testing.T) {
	issue := model.NewIssue(validate.DatasetBindingInvalid, "/dataset_bindings/0/content_hash", "fixed", model.SeverityError)
	for _, c := range []struct {
		err  error
		code httpapi.Code
		path string
	}{
		{ErrNotFound, httpapi.NotFound, ""},
		{&DecidedError{Status: store.Approved}, httpapi.ProposalAlreadyDecided, ""},
		{&DecidedError{Status: store.Conflicted}, httpapi.ProposalAlreadyDecided, ""},
		{&StateError{Status: store.NeedsInput, Decision: "approve"}, httpapi.LifecycleConflict, ""},
		{&StateError{Status: store.Generating, Decision: "reject"}, httpapi.LifecycleConflict, ""},
		{ErrOtherBase, httpapi.ModelVersionConflict, "/expected_base_version_id"},
		{&RiskChangedError{Path: "/risk_level", Reviewed: "low", Now: "medium"}, httpapi.LifecycleConflict, "/risk_level"},
		{&InvalidError{Issues: []model.ValidationIssue{issue}}, httpapi.ModelValidationFailed, "/dataset_bindings/0/content_hash"},
		{ErrInvalidComment, httpapi.RequestValidationFailed, "/comment"},
		{ErrDatasetsUnavailable, httpapi.ServiceUnavailable, ""},
		{idempotency.ErrConflict, httpapi.IdempotencyKeyConflict, ""},
		{idempotency.ErrInProgress, httpapi.IdempotencyKeyConflict, ""},
	} {
		var he *httpapi.Error
		if !errors.As(Refused(c.err), &he) || he.Code != c.code {
			t.Errorf("%v: %v, want %s", c.err, Refused(c.err), c.code)
			continue
		}
		if c.path != "" && (len(he.Details) == 0 || he.Details[0].Path != c.path) {
			t.Errorf("%v: details %+v, want the path %s", c.err, he.Details, c.path)
		}
		if _, ok := httpapi.Status[he.Code]; !ok {
			t.Errorf("%s is not a contract code", he.Code)
		}
	}
	var he *httpapi.Error
	if errors.As(Refused(&InvalidError{Issues: []model.ValidationIssue{issue}}), &he); he.Details[0].Code != validate.DatasetBindingInvalid {
		t.Errorf("an issue's code is lost: %+v", he.Details)
	}
	// I2, D-069 (b): a snapshot that cannot be read is 503 with the
	// generator's code (D-061 (c)), never the cause's text.
	he = nil
	storage := &DatasetStorageError{Err: errors.New("open /srv/objects/tenant/secret: input/output error")}
	if !errors.As(Refused(storage), &he) || he.Code != httpapi.ServiceUnavailable || len(he.Details) != 1 ||
		he.Details[0].Code != "DATASET_STORAGE_FAILED" || strings.Contains(fmt.Sprint(he), "secret") {
		t.Errorf("a storage failure: %+v", he)
	}
	// Tenant and other errors pass through for httpapi to classify.
	for _, err := range []error{tenant.ErrForbidden, tenant.ErrNoMembership, errors.New("a server fault")} {
		if Refused(err) != err {
			t.Errorf("%v was mapped", err)
		}
	}
}

// D-067 (proposed): the scenario is named after the summary the person
// reviewed, in one line, at most 200 characters.
func TestScenarioName(t *testing.T) {
	id := "aip_01M4YDQK00ADN261CME3WV5HZ6"
	long := strings.Repeat("é", 250)
	for _, c := range []struct{ summary, want string }{
		{"Raise the CI runners to 6.", "Raise the CI runners to 6."},
		{"  Two\nlines,\tand  spaces ", "Two lines, and spaces"},
		{" \n\t ", id},
		{long, strings.Repeat("é", 199) + "…"},
		{strings.Repeat("a", 198) + " " + "bcd", strings.Repeat("a", 198) + "…"},
		{strings.Repeat("a", 200), strings.Repeat("a", 200)},
	} {
		got := scenarioName(c.summary, id)
		if got != c.want {
			t.Errorf("%q: %q, want %q", c.summary, got, c.want)
		}
		if n := utf8.RuneCountInString(got); n < 1 || n > maxName {
			t.Errorf("%q: %d characters", c.summary, n)
		}
	}
}

// The idempotency fingerprint (D-011, D-069 (a), API_DATABASE §7) is the
// proposal of the URL and the normalized parsed body. A key's scope
// (organization, actor, operation, key) does not hold the proposal, so
// the fingerprint alone tells the same key used on another proposal apart
// (review M1); an empty comment is no comment (D-068), so {} and
// {"comment":""} are one request (review I3).
func TestFingerprint(t *testing.T) {
	const (
		a, b  = "aip_01M4YDQK00ADN261CME3WV5HZ6", "aip_01M4YDQK00TA00000000000003"
		base  = `{"expected_base_version_id":"mv_01M4YDQK00ADN261CME3WV5HZ6"}`
		other = `{"expected_base_version_id":"mv_01M4YDQK00TA00000000000002"}`
	)
	fp := func(id, body string) string {
		t.Helper()
		h, err := fingerprint(id, []byte(body))
		if err != nil {
			t.Fatalf("%s: %v", body, err)
		}
		return h
	}
	for _, body := range []string{base, `{}`, `{"comment":"No."}`} {
		if fp(a, body) == fp(b, body) {
			t.Errorf("%s: another proposal has the same fingerprint", body)
		}
	}
	for _, same := range [][2]string{
		{base, `{ "expected_base_version_id" : "mv_01M4YDQK00ADN261CME3WV5HZ6" }`},
		{base, `{"comment":"","expected_base_version_id":"mv_01M4YDQK00ADN261CME3WV5HZ6"}`},
		{`{}`, `{"comment":""}`},
		{`{"comment":"No.","expected_base_version_id":"mv_01M4YDQK00ADN261CME3WV5HZ6"}`,
			`{"expected_base_version_id":"mv_01M4YDQK00ADN261CME3WV5HZ6","comment":"No."}`},
	} {
		if fp(a, same[0]) != fp(a, same[1]) {
			t.Errorf("%s and %s are one request", same[0], same[1])
		}
	}
	for _, differ := range [][2]string{
		{base, other},
		{base, `{"expected_base_version_id":"mv_01M4YDQK00ADN261CME3WV5HZ6","comment":"x"}`},
		{`{}`, `{"comment":"No."}`},
		{`{"comment":"No."}`, `{"comment":"no."}`},
	} {
		if fp(a, differ[0]) == fp(a, differ[1]) {
			t.Errorf("%s and %s are two requests", differ[0], differ[1])
		}
	}
	// A member the schemas do not have is refused, not left out.
	if _, err := fingerprint(a, []byte(`{"comment":"","approved_by":"usr_01M4YDQK00ADN261CME3WV5HZ6"}`)); err == nil {
		t.Error("an unknown member was fingerprinted")
	}
}

// Review L1, D-069 (a): a replay is the stored answer byte for byte,
// except that an error answer names the request that gets it, as every
// error answer does (error.request_id is the response's X-Request-ID).
func TestReplayed(t *testing.T) {
	const first, again = "req_01M4YDQK00TA00000000000001", "req_01M4YDQK00TA00000000000002"
	created, _ := hashing.Canonical([]byte(`{"proposal_id":"aip_01M4YDQK00ADN261CME3WV5HZ6","status":"approved","mode":"scenario",` +
		`"scenario_id":"scn_01M4YDQK00ADN261CME3WV5HZ6","patch_hash":"sha256:` + strings.Repeat("a", 64) + `"}`))
	if out, err := replayed(idempotency.Response{Status: 201, Body: created}, again); err != nil || string(out) != string(created) {
		t.Errorf("a 201 replays as %s %v", out, err)
	}
	stored, _ := json.Marshal(proposals.ErrorEnvelope{Error: proposals.ErrorBody{Code: "MODEL_VERSION_CONFLICT", RequestID: first,
		Message: "the model's latest version is no longer this proposal's base",
		Details: []proposals.ErrorDetail{{Path: "/expected_base_version_id", Reason: "the proposal's base is not the model's latest version"}}}})
	stored, _ = hashing.Canonical(stored)
	out, err := replayed(idempotency.Response{Status: 409, Body: stored}, again)
	if err != nil {
		t.Fatal(err)
	}
	var e proposals.ErrorEnvelope
	if err := json.Unmarshal(out, &e, json.RejectUnknownMembers(true)); err != nil || e.Error.RequestID != again {
		t.Fatalf("the replayed 409 names %q: %s %v", e.Error.RequestID, out, err)
	}
	if want := strings.Replace(string(stored), first, again, 1); string(out) != want {
		t.Errorf("the replayed 409 is\n%s\nwant\n%s", out, want)
	}
}

// Review I2: a dataset binding stage that cannot read the snapshots is a
// storage failure (503 DATASET_STORAGE_FAILED); a request that ended is
// not.
func TestBindingFailure(t *testing.T) {
	cause := errors.New("the object store did not answer")
	var se *DatasetStorageError
	if err := bindingFailure(context.Background(), cause); !errors.As(err, &se) || !errors.Is(err, cause) {
		t.Errorf("a read failure: %v", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := bindingFailure(ctx, fmt.Errorf("reading: %w", context.Canceled)); errors.As(err, &se) || !errors.Is(err, context.Canceled) {
		t.Errorf("a cancelled request: %v", err)
	}
	// The request ended, whatever error the store gave (review round 2 N3).
	if err := bindingFailure(ctx, cause); errors.As(err, &se) || !errors.Is(err, cause) {
		t.Errorf("a cancelled request with the store's own error: %v", err)
	}
}

func TestDecisionComment(t *testing.T) {
	if c, err := decisionComment(""); c != nil || err != nil {
		t.Errorf("empty: %v %v", c, err)
	}
	if c, err := decisionComment(strings.Repeat("é", 2000)); c == nil || err != nil {
		t.Errorf("2000 characters: %v", err)
	}
	for _, bad := range []string{strings.Repeat("x", 2001), "a\x00b", "\xff"} {
		if _, err := decisionComment(bad); !errors.Is(err, ErrInvalidComment) {
			t.Errorf("%q: %v", bad, err)
		}
	}
}

// The base of the recheck tests: the synthetic default delivery pipeline.
func baseIR(t *testing.T) ([]byte, *model.ModelIR) {
	t.Helper()
	var doc map[string]any
	if err := json.Unmarshal(contracttest.ReadFile(t, filepath.Join(contracttest.Root(t), "testdata", "golden-models", "default-delivery.json")), &doc); err != nil {
		t.Fatal(err)
	}
	doc["model_id"] = "mdl_01M4YDQK00ADN261CME3WV5HZ6"
	raw, _ := json.Marshal(doc)
	ir, err := hashing.Canonical(raw)
	if err != nil {
		t.Fatal(err)
	}
	var decoded model.ModelIR
	if err := model.Decode(ir, &decoded); err != nil {
		t.Fatal(err)
	}
	return ir, &decoded
}

func pack(t *testing.T) domainpack.DomainPack {
	t.Helper()
	reg, err := domainpack.NewRegistry(softwaredelivery.Pack{})
	if err != nil {
		t.Fatal(err)
	}
	p, err := reg.Lookup("software_delivery")
	if err != nil {
		t.Fatal(err)
	}
	return p
}

// readyProposal is the proposal the generator stores for an answer the
// review makes ready: what recheck reads back.
func readyProposal(t *testing.T, base []byte, ir *model.ModelIR, mode, changes, claim string, effects ...string) store.Proposal {
	t.Helper()
	pool, err := idpool.New(ir)
	if err != nil {
		t.Fatal(err)
	}
	return readyWithPool(t, base, pool, mode, changes, claim, effects...)
}

// readyWithPool is readyProposal with the proposal's ID pool given.
func readyWithPool(t *testing.T, base []byte, pool idpool.Pool, mode, changes, claim string, effects ...string) store.Proposal {
	t.Helper()
	var ir model.ModelIR
	if err := model.Decode(base, &ir); err != nil {
		t.Fatal(err)
	}
	poolJSON, _ := json.Marshal(pool)
	var c model.Patch
	if err := json.Unmarshal([]byte(changes), &c); err != nil {
		t.Fatal(err)
	}
	if effects == nil {
		effects = []string{"Waiting may fall; a simulation will tell."}
	}
	confidence := 0.9
	o, err := review.Review(review.Input{Mode: provider.Mode(mode), Base: base, Pool: pool, Pack: pack(t), Output: provider.ProposalOutput{
		Status: "proposed", Summary: "Change one parameter.", Assumptions: []string{}, RiskLevel: claim, Confidence: &confidence,
		Changes: c, ExpectedEffects: effects, RequiresConfirmation: true}})
	if err != nil || o.Status != review.Ready {
		t.Fatalf("the reference answer is not ready: %v %+v", err, o.Validation)
	}
	summary, hash := o.Summary, o.PatchHash
	server, providerClaim, risk := string(o.ServerRisk), string(o.ProviderRisk), string(o.Risk)
	return store.Proposal{ID: "aip_01M4YDQK00ADN261CME3WV5HZ6", OrganizationID: "org_01M4YDQK00Y5H2929M9WWZWTVH", ModelID: ir.ModelID,
		BaseVersionID: "mv_01M4YDQK00ADN261CME3WV5HZ6", Mode: mode, Status: store.Ready, IDPool: poolJSON,
		Summary: &summary, Assumptions: o.Assumptions, ExpectedEffects: o.ExpectedEffects, Patch: o.Changes, PatchHash: &hash,
		ServerRisk: &server, ProviderRisk: &providerClaim, Risk: &risk, Confidence: o.Confidence}
}

const (
	raiseCI = `[{"op":"test","path":"/resources/1/id","value":"ci_runner"},{"op":"replace","path":"/resources/1/capacity","value":6}]`
	lowerCI = `[{"op":"test","path":"/resources/1/id","value":"ci_runner"},{"op":"replace","path":"/resources/1/capacity","value":2}]`
)

func str(s string) *string { return &s }

// The stored proposal passes again, and the version the approval creates
// is the base with exactly the reviewed changes.
func TestRecheckAppliesTheReviewedChanges(t *testing.T) {
	base, ir := baseIR(t)
	for _, mode := range []string{"edit", "scenario"} {
		p := readyProposal(t, base, ir, mode, raiseCI, "low")
		r, err := recheck(p, base, pack(t))
		if err != nil {
			t.Fatalf("%s: %v", mode, err)
		}
		want, err := patch.Apply(base, p.Patch, patch.Scenario)
		if err != nil || !want.Valid() {
			t.Fatal(err)
		}
		canonical, _ := hashing.Canonical(want.Candidate)
		if string(r.candidate) != string(canonical) || r.hash != hashing.Sum(canonical) || r.ir.Resources[1].Capacity != 6 {
			t.Errorf("%s: the candidate is not the base with the changes", mode)
		}
		if r.outcome.PatchHash != *p.PatchHash || string(r.outcome.Changes) != string(p.Patch) {
			t.Errorf("%s: the reviewed changes changed", mode)
		}
	}
}

// Nothing stored is trusted: a patch that is not its hash's is a server
// fault; content the review refuses now is invalid (cleaned); a stored
// risk below the server's is refused; another pool refuses new IDs; the
// mode's allowlist holds; the numbers of the effects are checked again.
func TestRecheckTrustsNoStoredResult(t *testing.T) {
	base, ir := baseIR(t)
	sd := pack(t)
	edit := func() store.Proposal { return readyProposal(t, base, ir, "edit", raiseCI, "low") }

	// The patch was changed after review, its hash kept: never approved.
	p := edit()
	p.Patch = []byte(lowerCI)
	var ie *InvalidError
	var re *RiskChangedError
	if _, err := recheck(p, base, sd); err == nil || errors.As(err, &ie) || errors.As(err, &re) {
		t.Errorf("a patch its hash does not cover: %v", err)
	}
	// The hash was changed, the patch kept: never approved either.
	p = edit()
	p.PatchHash = str("sha256:" + strings.Repeat("0", 64))
	if _, err := recheck(p, base, sd); err == nil || errors.As(err, &ie) || errors.As(err, &re) {
		t.Errorf("a hash that does not cover its patch: %v", err)
	}

	// The patch and its hash were both rewritten into a change the review
	// refuses (a capacity of 0): invalid, and the issues quote nothing.
	p = edit()
	zero := `[{"op":"test","path":"/resources/1/id","value":"ci_runner"},{"op":"replace","path":"/resources/1/capacity","value":0}]`
	canonical, _ := hashing.Canonical([]byte(zero))
	h, _ := hashing.Patch(canonical)
	p.Patch, p.PatchHash = canonical, &h
	_, err := recheck(p, base, sd)
	if !errors.As(err, &ie) || len(ie.Issues) == 0 {
		t.Fatalf("a refused change: %v", err)
	}
	for _, is := range ie.Issues {
		if review.Clean(is) != is {
			t.Errorf("an issue is not cleaned: %+v", is)
		}
	}

	// The stored risk says low; the change lowers a capacity, which the
	// server rates medium (D-016): the person reviewed another risk.
	p = readyProposal(t, base, ir, "edit", lowerCI, "low")
	if *p.ServerRisk != "medium" {
		t.Fatalf("lowering a capacity is %s", *p.ServerRisk)
	}
	p.ServerRisk, p.Risk = str("low"), str("low")
	if _, err := recheck(p, base, sd); !errors.As(err, &re) || re.Path != "/risk_level" || re.Reviewed != "low" || re.Now != "medium" {
		t.Errorf("a lowered stored risk: %v", err)
	}
	// The provider claimed high: the effective risk stays high, but the
	// server's own stored value is checked too.
	p = readyProposal(t, base, ir, "edit", lowerCI, "high")
	p.ServerRisk = str("low")
	if _, err := recheck(p, base, sd); !errors.As(err, &re) || re.Path != "/server_risk_level" || re.Now != "medium" {
		t.Errorf("a lowered stored server risk: %v", err)
	}

	// A new resource takes an ID of the proposal's pool (D-046); checked
	// against another pool, it is a violation.
	pool, _ := idpool.New(ir)
	add := `[{"op":"add","path":"/resources/-","value":{"id":"` + pool.IDs(idpool.Resource)[0] +
		`","type":"machine","name":"Spare runner","capacity":1,"queue_discipline":"fifo"}}]`
	p = readyWithPool(t, base, pool, "edit", add, "low")
	other, _ := idpool.New(ir)
	p.IDPool, _ = json.Marshal(other)
	_, err = recheck(p, base, sd)
	if !errors.As(err, &ie) || !slices.ContainsFunc(ie.Issues, func(is model.ValidationIssue) bool { return is.Code == review.IDPoolViolation }) {
		t.Errorf("a new ID outside the pool: %v", err)
	}

	// A scenario proposal may change parameters only (D-021): a stored
	// scenario patch that adds a resource is refused at approval as well.
	p = edit()
	p.Mode = "scenario"
	_, err = recheck(p, base, sd)
	if err != nil {
		t.Fatalf("a parameter change in scenario mode: %v", err)
	}
	p = readyWithPool(t, base, pool, "edit", add, "low")
	p.Mode = "scenario"
	if _, err := recheck(p, base, sd); !errors.As(err, &ie) ||
		!slices.ContainsFunc(ie.Issues, func(is model.ValidationIssue) bool { return is.Code == patch.PathForbidden }) {
		t.Errorf("an added resource in scenario mode: %v", err)
	}

	// An expected effect with a number no model value supplies (AI_SPEC §5).
	p = edit()
	p.ExpectedEffects = []string{"Lead time falls 37%."}
	if _, err := recheck(p, base, sd); !errors.As(err, &ie) || ie.Issues[0].Code != review.OutputInvalid {
		t.Errorf("a fabricated number: %v", err)
	}

	// A proposal without its reviewed content is the server's fault.
	p = edit()
	p.PatchHash = nil
	if _, err := recheck(p, base, sd); err == nil || errors.As(err, &ie) {
		t.Errorf("no patch hash: %v", err)
	}
	p = edit()
	p.Mode = "publish"
	if _, err := recheck(p, base, sd); err == nil || errors.As(err, &ie) {
		t.Errorf("an unknown mode: %v", err)
	}
}

// strictPack is the pack with a rule it did not have when the proposal was
// reviewed: the approval applies today's rules (D-069 (b)).
type strictPack struct{ domainpack.DomainPack }

func (p strictPack) Validate(ir *model.ModelIR) []model.ValidationIssue {
	issues := p.DomainPack.Validate(ir)
	for i, r := range ir.Resources {
		if r.Capacity > 5 {
			issues = append(issues, model.NewIssue("DOMAIN_MISMATCH", "/resources/"+string(rune('0'+i))+"/capacity", "a capacity of "+r.Name, model.SeverityError))
		}
	}
	return issues
}

func TestRecheckAppliesTodaysRules(t *testing.T) {
	base, ir := baseIR(t)
	p := readyProposal(t, base, ir, "edit", raiseCI, "low")
	var ie *InvalidError
	_, err := recheck(p, base, strictPack{pack(t)})
	if !errors.As(err, &ie) || ie.Issues[0].Path != "/resources/1/capacity" {
		t.Fatalf("a rule added since review: %v", err)
	}
	if strings.Contains(ie.Issues[0].Message, "CI") || review.Clean(ie.Issues[0]) != ie.Issues[0] {
		t.Errorf("the pack's message is kept: %+v", ie.Issues[0])
	}
}
