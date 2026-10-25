package scenarios

// Unit tests of the scenario service's pure parts (T-028; TEST-003,
// TEST-008, TEST-009): the scenario patch check and its parity with the AI
// scenario mode's review (D-021: one allowlist for both), the name rule,
// the Scenario resource, the idempotency fingerprint, the list cursor, the
// routes and the error mapping. No database: the DB-backed tests are in
// db_test.go.

import (
	"encoding/base64"
	"encoding/json/v2"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"testing"
	"time"

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
	"github.com/kcl1104/geminus/internal/storage/idempotency"
	store "github.com/kcl1104/geminus/internal/storage/scenarios"
	"github.com/kcl1104/geminus/internal/tenant"
)

// Responses must be UTC whatever the server's zone.
func init() { time.Local = time.FixedZone("UTC+8", 8*3600) }

// base is the synthetic delivery pipeline (D-017) in RFC 8785 form, the
// bytes a version stores (D-040): resources reviewer, ci_runner (3),
// test_environment, deployment_slot; node 3 is the CI result decision with
// flows 3 (f_pass, 0.8) and 4 (f_retry, 0.2).
func base(t *testing.T) []byte {
	t.Helper()
	data := contracttest.ReadFile(t, filepath.Join(contracttest.Root(t), "testdata", "golden-models", "default-delivery.json"))
	ir, err := hashing.Canonical(data)
	if err != nil {
		t.Fatal(err)
	}
	return ir
}

func packs(t *testing.T) PackLookup {
	t.Helper()
	reg, err := domainpack.NewRegistry(softwaredelivery.Pack{})
	if err != nil {
		t.Fatal(err)
	}
	return reg
}

// refusingPack is the delivery pack with one more rule, which refuses
// every model with an error (a pack's rules apply to scenarios too).
type refusingPack struct{ softwaredelivery.Pack }

func (refusingPack) Validate(*model.ModelIR) []model.ValidationIssue {
	return []model.ValidationIssue{model.NewIssue(softwaredelivery.DomainMismatch, "/domain", "this pack refuses every model", model.SeverityError)}
}

type lookup struct{ pack domainpack.DomainPack }

func (l lookup) Lookup(domain string) (domainpack.DomainPack, error) {
	if domain != l.pack.ID() {
		return nil, domainpack.ErrUnsupportedDomain
	}
	return l.pack, nil
}

type patchCase struct {
	name, patch string
	// code and path are the first refusal's, "" when the patch is accepted.
	code, path string
	// effect: the checked candidate, when accepted, holds this value here.
	effectPath string
	effect     any
}

const (
	testCI = `{"op":"test","path":"/resources/1/id","value":"ci_runner"}`
	// raiseCI is the AI tests' scenario: 6 CI runners.
	raiseCI = `[` + testCI + `,{"op":"replace","path":"/resources/1/capacity","value":6}]`
)

var patchCases = []patchCase{
	{name: "a capacity", patch: raiseCI, effectPath: "/resources/1/capacity", effect: 6.0},
	{name: "routing probabilities", patch: `[{"op":"test","path":"/flows/3/id","value":"f_pass"},{"op":"replace","path":"/flows/3/routing/probability","value":0.75},` +
		`{"op":"test","path":"/flows/4/id","value":"f_retry"},{"op":"replace","path":"/flows/4/routing/probability","value":0.25}]`,
		effectPath: "/flows/4/routing/probability", effect: 0.25},
	{name: "a service time", patch: `[{"op":"test","path":"/nodes/2/id","value":"ci"},{"op":"replace","path":"/nodes/2/service_time/distribution","value":{"type":"constant","value":9}}]`,
		effectPath: "/nodes/2/service_time/distribution/value", effect: 9.0},
	{name: "removing a node", patch: `[{"op":"test","path":"/nodes/5/id","value":"ci_rework"},{"op":"remove","path":"/nodes/5"}]`,
		code: patch.PathForbidden, path: "/patch/1/path"},
	{name: "adding a resource", patch: `[{"op":"add","path":"/resources/-","value":{"id":"qa","type":"human","name":"QA","capacity":1,"queue_discipline":"fifo"}}]`,
		code: patch.PathForbidden, path: "/patch/0/path"},
	{name: "renaming the model", patch: `[{"op":"replace","path":"/name","value":"Another"}]`, code: patch.PathForbidden, path: "/patch/0/path"},
	{name: "a flow's target", patch: `[{"op":"test","path":"/flows/3/id","value":"f_pass"},{"op":"replace","path":"/flows/3/to","value":"deploy"}]`,
		code: patch.PathForbidden, path: "/patch/1/path"},
	{name: "no test before the change", patch: `[{"op":"replace","path":"/resources/1/capacity","value":6}]`, code: patch.TestRequired, path: "/patch/0/path"},
	{name: "a stale test", patch: `[{"op":"test","path":"/resources/1/id","value":"reviewer"},{"op":"replace","path":"/resources/1/capacity","value":6}]`,
		code: patch.TestFailed, path: "/patch/0/value"},
	{name: "a resource the base does not have", patch: `[{"op":"test","path":"/resources/9/id","value":"ci_runner"},{"op":"replace","path":"/resources/9/capacity","value":6}]`,
		code: patch.TargetMissing, path: "/patch/0/path"},
	{name: "a new dataset", patch: `[{"op":"test","path":"/nodes/2/id","value":"ci"},` +
		`{"op":"replace","path":"/nodes/2/service_time/distribution","value":{"type":"empirical","samples_ref":"dataset:invented"}}]`,
		code: patch.PathForbidden, path: "/patch/1/value"},
	{name: "probabilities that do not sum to 1", patch: `[{"op":"test","path":"/flows/3/id","value":"f_pass"},{"op":"replace","path":"/flows/3/routing/probability","value":0.5}]`,
		code: validate.RoutingInvalid, path: "/nodes/3"},
	{name: "no capacity", patch: `[` + testCI + `,{"op":"replace","path":"/resources/1/capacity","value":0}]`,
		code: validate.IRSchemaInvalid, path: "/resources/1/capacity"},
}

func at(t *testing.T, ir []byte, pointer string) any {
	t.Helper()
	var v any
	if err := json.Unmarshal(ir, &v); err != nil {
		t.Fatal(err)
	}
	for _, tok := range strings.Split(pointer, "/")[1:] {
		switch c := v.(type) {
		case map[string]any:
			v = c[tok]
		case []any:
			var i int
			fmt.Sscan(tok, &i)
			v = c[i]
		}
	}
	return v
}

// TEST-003, D-020, D-021: a scenario patch changes only the parameters
// the scenario allowlist names, after a test of the element, and the
// patched model must be valid; issues point into the request (/patch/...)
// or into the patched model.
func TestCheck(t *testing.T) {
	b, reg := base(t), packs(t)
	for _, c := range patchCases {
		t.Run(c.name, func(t *testing.T) {
			got, err := check(b, []byte(c.patch), reg)
			if c.code == "" {
				if err != nil {
					t.Fatalf("refused: %v", err)
				}
				want, _ := hashing.Canonical([]byte(c.patch))
				hash, _ := hashing.Patch(want)
				if string(got.patch) != string(want) || got.hash != hash || got.ir == nil {
					t.Errorf("checked %s %s, want %s %s", got.patch, got.hash, want, hash)
				}
				encoded, err := model.Encode(got.ir)
				if err != nil {
					t.Fatal(err)
				}
				if v := at(t, encoded, c.effectPath); v != c.effect {
					t.Errorf("%s is %v, want %v", c.effectPath, v, c.effect)
				}
				return
			}
			var ie *InvalidError
			if !errors.As(err, &ie) || len(ie.Issues) == 0 {
				t.Fatalf("not refused as invalid: %v", err)
			}
			if !slices.ContainsFunc(ie.Issues, func(is model.ValidationIssue) bool {
				return is.Code == c.code && is.Path == c.path && is.Severity == model.SeverityError
			}) {
				t.Errorf("issues %+v, want %s at %s", ie.Issues, c.code, c.path)
			}
		})
	}
}

// D-019, D-040: what a scenario stores and hashes is the patch's RFC 8785
// form, whatever the request's spelling: member order, spacing, number
// spelling and escapes.
func TestCheckStoresTheCanonicalPatch(t *testing.T) {
	spelled := "[ {\"value\" : \"ci_\\u0072unner\", \"path\":\"/resources/1/id\", \"op\":\"test\"},\n" +
		"  {\"path\":\"/resources/1/capacity\",\"op\":\"replace\",\"value\":6.0E0} ]"
	got, err := check(base(t), []byte(spelled), packs(t))
	if err != nil {
		t.Fatal(err)
	}
	if string(got.patch) != raiseCI {
		t.Errorf("stored %s, want %s", got.patch, raiseCI)
	}
	if want, _ := hashing.Patch([]byte(raiseCI)); got.hash != want {
		t.Errorf("hash %s, want %s", got.hash, want)
	}
}

// REQ-010, D-078 (b): only [] is the baseline (the empty patch). A patch
// that is not empty but changes nothing is accepted as an ordinary
// scenario: its candidate is the base, but it is stored as given, never
// as the baseline's [].
func TestCheckKeepsABaseline(t *testing.T) {
	b, reg := base(t), packs(t)
	for name, doc := range map[string]string{
		"the baseline":                           `[]`,
		"an ordinary scenario, a value as it is": `[` + testCI + `,{"op":"replace","path":"/resources/1/capacity","value":3}]`,
		"an ordinary scenario, only a test":      `[` + testCI + `]`,
	} {
		got, err := check(b, []byte(doc), reg)
		if err != nil {
			t.Errorf("%s: %v", name, err)
			continue
		}
		encoded, _ := model.Encode(got.ir)
		if again, _ := hashing.Canonical(encoded); string(again) != string(b) {
			t.Errorf("%s: the candidate is not the base", name)
		}
		want, _ := hashing.Canonical([]byte(doc))
		if string(got.patch) != string(want) {
			t.Errorf("%s: stored %s, want %s", name, got.patch, want)
		}
	}
}

// The other refusals: the patch profile (move is not P0, D-014), a patch
// larger than the IR may be (D-029), the domain pack's rules, a domain no
// pack serves (a server fault, not the person's), a base that is not JSON
// (a server fault); and an older base that a later rule refuses gets the
// rule's issue, as createSimulation answers (D-067 allows any version).
func TestCheckRefusals(t *testing.T) {
	b, reg := base(t), packs(t)
	var ie *InvalidError
	if _, err := check(b, []byte(`[{"op":"move","from":"/resources/1","path":"/resources/0"}]`), reg); !errors.As(err, &ie) ||
		ie.Issues[0].Code != patch.SchemaInvalid || !strings.HasPrefix(ie.Issues[0].Path, "/patch") {
		t.Errorf("move: %v", err)
	}
	big := `[` + strings.Repeat(testCI+`,`, validate.MaxIRBytes/len(testCI)) + testCI + `]`
	if _, err := check(b, []byte(big), reg); !errors.As(err, &ie) || ie.Issues[0].Code != validate.LimitExceeded || ie.Issues[0].Path != "/patch" {
		t.Errorf("a patch over the limit: %v", err)
	}
	if _, err := check(b, []byte(raiseCI), lookup{refusingPack{}}); !errors.As(err, &ie) ||
		ie.Issues[0].Code != softwaredelivery.DomainMismatch || ie.Issues[0].Path != "/domain" {
		t.Errorf("the pack's rules: %v", err)
	}
	if _, err := check(b, []byte(raiseCI), lookup{otherPack{}}); err == nil || errors.As(err, &ie) {
		t.Errorf("no pack serves the domain: %v", err)
	}
	if _, err := check([]byte(`{"not":`), []byte(raiseCI), reg); err == nil || errors.As(err, &ie) {
		t.Errorf("a base that is not JSON: %v", err)
	}
	// The base with a decision whose probabilities no longer sum to 1, as a
	// version a later rule refuses would be: a baseline on it is refused
	// with that rule's issue.
	var doc map[string]any
	if err := json.Unmarshal(b, &doc); err != nil {
		t.Fatal(err)
	}
	doc["flows"].([]any)[3].(map[string]any)["routing"].(map[string]any)["probability"] = 0.5
	raw, _ := json.Marshal(doc)
	stale, _ := hashing.Canonical(raw)
	if _, err := check(stale, []byte(`[]`), reg); !errors.As(err, &ie) || ie.Issues[0].Code != validate.RoutingInvalid || ie.Issues[0].Path != "/nodes/3" {
		t.Errorf("a base a later rule refuses: %v", err)
	}
}

type otherPack struct{ softwaredelivery.Pack }

func (otherPack) ID() string { return "another_domain" }

// D-021: a person's scenario and an AI scenario take one allowlist. Every
// patch the AI scenario mode's review accepts, check accepts, and every
// patch it refuses, check refuses with the same code; only the provider's
// own rule differs: the AI must change something (AI_OUTPUT_INVALID); a
// person's patch that changes nothing is an ordinary scenario (D-078 (b)).
func TestTheAIScenarioModeTakesTheSameRules(t *testing.T) {
	b, reg := base(t), packs(t)
	var ir model.ModelIR
	if err := model.Decode(b, &ir); err != nil {
		t.Fatal(err)
	}
	pool, err := idpool.New(&ir)
	if err != nil {
		t.Fatal(err)
	}
	pack, err := reg.Lookup(ir.Domain)
	if err != nil {
		t.Fatal(err)
	}
	ai := func(t *testing.T, doc string) review.Outcome {
		t.Helper()
		var changes model.Patch
		if err := json.Unmarshal([]byte(doc), &changes); err != nil {
			t.Fatal(err)
		}
		confidence := 0.9
		o, err := review.Review(review.Input{Mode: provider.ModeScenario, Base: b, Pool: pool, Pack: pack, Output: provider.ProposalOutput{
			Status: "proposed", Summary: "A what-if.", Assumptions: []string{}, RiskLevel: "low", Confidence: &confidence, Changes: changes,
			ExpectedEffects: []string{"Waiting may change; a simulation will tell."}, RequiresConfirmation: true}})
		if err != nil {
			t.Fatal(err)
		}
		return o
	}
	for _, c := range patchCases {
		t.Run(c.name, func(t *testing.T) {
			o := ai(t, c.patch)
			_, err := check(b, []byte(c.patch), reg)
			if c.code == "" {
				if o.Status != review.Ready || err != nil {
					t.Errorf("AI %s %+v, manual %v", o.Status, o.Validation, err)
				}
				return
			}
			var ie *InvalidError
			if o.Status != review.Invalid || !errors.As(err, &ie) {
				t.Fatalf("AI %s, manual %v", o.Status, err)
			}
			codes := func(issues []model.ValidationIssue) []string {
				var out []string
				for _, is := range issues {
					if is.Severity == model.SeverityError {
						out = append(out, is.Code)
					}
				}
				slices.Sort(out)
				return slices.Compact(out)
			}
			if a, m := codes(o.Validation.Issues), codes(ie.Issues); !slices.Equal(a, m) {
				t.Errorf("AI refuses with %v, manual with %v", a, m)
			}
		})
	}
	for _, doc := range []string{`[` + testCI + `,{"op":"replace","path":"/resources/1/capacity","value":3}]`} {
		if o := ai(t, doc); o.Status != review.Invalid || o.Validation.Issues[0].Code != review.OutputInvalid {
			t.Errorf("the AI's no-op: %+v", o)
		}
		if _, err := check(b, []byte(doc), reg); err != nil {
			t.Errorf("a person's no-op: %v", err)
		}
	}
}

// D-078 (d): the name is the person's text, kept as given; only white
// space says nothing.
func TestNameIssues(t *testing.T) {
	for _, name := range []string{" ", "\t\n", "　", strings.Repeat(" ", 200)} {
		issues := nameIssues(name)
		if len(issues) != 1 || issues[0].Path != "/name" || issues[0].Code != validate.ConstraintFailed || issues[0].Severity != model.SeverityError {
			t.Errorf("%q: %+v", name, issues)
		}
	}
	for _, name := range []string{"Eight CI runners", " Eight  runners ", "八台 CI runner", "x"} {
		if issues := nameIssues(name); len(issues) != 0 {
			t.Errorf("%q: %+v", name, issues)
		}
	}
}

func fixture(t *testing.T, name string) []byte {
	t.Helper()
	return contracttest.ReadFile(t, filepath.Join(contracttest.Root(t), "testdata", "contracts", name))
}

func sameJSON(t *testing.T, got any, want []byte) {
	t.Helper()
	data, err := json.Marshal(got)
	if err != nil {
		t.Fatal(err)
	}
	a, err := hashing.Canonical(data)
	if err != nil {
		t.Fatal(err)
	}
	b, err := hashing.Canonical(want)
	if err != nil {
		t.Fatal(err)
	}
	if string(a) != string(b) {
		t.Errorf("%s\nwant %s", a, b)
	}
}

// The Scenario resource is scenario.schema.json for both kinds: the patch
// in RFC 8785 form, whatever spacing the database returns (jsonb), the
// creation time in UTC, source_proposal_id null for a person's. A row
// whose patch is not the one its hash covers is a server fault, never
// shown (D-079 (f)).
func TestPublic(t *testing.T) {
	contracttest.LoadSchemas(t).Parity(t, reflect.TypeFor[Scenario](), "scenario.schema.json")
	contracttest.LoadSchemas(t).Parity(t, reflect.TypeFor[Page](), "http.schema.json#/$defs/Scenarios")
	contracttest.LoadSchemas(t).Parity(t, reflect.TypeFor[CreateRequest](), "http.schema.json#/$defs/CreateScenario")
	at := time.Date(2026, 10, 25, 17, 0, 0, 0, time.Local) // 09:00 UTC
	proposal := "aip_01M4YDQK00Q10EM95Z6H4XK0Y8"
	rows := map[string]store.Scenario{
		"valid-manual-scenario.json": {ID: "scn_01M4YDQK00J4MJS82YRG0PCEC1", OrganizationID: "org_01M4YDQK00Y5H2929M9WWZWTVH",
			ModelID: "mdl_01M4YDQK004AGVNEGJQJ1NMM0N", BaseVersionID: "mv_01M4YDQK00FRYWV7GS7X1QCXZR", Name: "Eight CI runners",
			Patch:     []byte(`[{"op": "test", "path": "/resources/1/id", "value": "ci_runner"}, {"op": "replace", "path": "/resources/1/capacity", "value": 8}]`),
			PatchHash: "sha256:b53fb1b85f008ecc70f56dcbfe23909e48a77e4304b36b575d16d2ce6bf52fb3", CreatedBy: "usr_01M4YDQK0016KTK4JM6KAHZ6BS", CreatedAt: at},
		"valid-scenario.json": {ID: "scn_01M4YDQK00J4MJS82YRG0PCEC0", OrganizationID: "org_01M4YDQK00Y5H2929M9WWZWTVH",
			ModelID: "mdl_01M4YDQK004AGVNEGJQJ1NMM0N", BaseVersionID: "mv_01M4YDQK00FRYWV7GS7X1QCXZR", Name: "Double CI Capacity",
			Patch: []byte(raiseCI), PatchHash: "sha256:9986280e38a861a2ca7eef1a03ca94a02b1ee066019b65d7fb5003b431884205",
			SourceProposalID: &proposal, CreatedBy: "usr_01M4YDQK0016KTK4JM6KAHZ6BR", CreatedAt: time.Date(2026, 10, 15, 8, 30, 0, 0, time.UTC)},
	}
	for name, row := range rows {
		p, err := Public(row)
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		sameJSON(t, p, fixture(t, name))
		// The patch itself in RFC 8785 form, not jsonb's spelling.
		if want, _ := hashing.Canonical(row.Patch); string(p.Patch) != string(want) {
			t.Errorf("%s: patch %s, want %s", name, p.Patch, want)
		}
		data, _ := json.Marshal(p)
		if issues, err := validate.Document(data, "scenario.schema.json", "RESPONSE"); err != nil || len(issues) > 0 {
			t.Errorf("%s: %v %v", name, issues, err)
		}
	}
	page := Page{Items: []Scenario{}}
	for _, name := range []string{"valid-scenario.json", "valid-manual-scenario.json"} {
		p, _ := Public(rows[name])
		page.Items = append(page.Items, p)
	}
	sameJSON(t, page, fixture(t, "valid-scenarios-page.json"))
	if data, _ := json.Marshal(Page{Items: []Scenario{}}); string(data) != `{"items":[],"next_cursor":null}` {
		t.Errorf("an empty page: %s", data)
	}

	manual := rows["valid-manual-scenario.json"]
	for name, bad := range map[string]func(*store.Scenario){
		"another patch":    func(s *store.Scenario) { s.Patch = []byte(raiseCI) },
		"another hash":     func(s *store.Scenario) { s.PatchHash = "sha256:" + strings.Repeat("0", 64) },
		"not JSON":         func(s *store.Scenario) { s.Patch = []byte(`[{"op":`) },
		"not an array":     func(s *store.Scenario) { s.Patch = []byte(`{"op":"test"}`) },
		"a duplicate name": func(s *store.Scenario) { s.Patch = []byte(`[{"op":"test","op":"test","path":"/name","value":1}]`) },
		"a malformed hash": func(s *store.Scenario) {
			s.PatchHash = "b53fb1b85f008ecc70f56dcbfe23909e48a77e4304b36b575d16d2ce6bf52fb3"
		},
		"an empty source":   func(s *store.Scenario) { empty := ""; s.SourceProposalID = &empty },
		"a missing creator": func(s *store.Scenario) { s.CreatedBy = "" },
	} {
		row := manual
		bad(&row)
		if _, err := Public(row); err == nil {
			t.Errorf("%s: shown", name)
		} else if name == "another patch" || name == "another hash" {
			if !errors.Is(err, ErrCorrupt) {
				t.Errorf("%s: %v, want ErrCorrupt", name, err)
			}
		}
	}
}

// D-011, D-079 (b): the fingerprint is the version of the URL and the RFC
// 8785 form of the body: spelling does not matter, the version and the
// body do.
func TestFingerprint(t *testing.T) {
	v1, v2 := "mv_01M4YDQK00FRYWV7GS7X1QCXZR", "mv_01M4YDQK00FRYWV7GS7X1QCXZS"
	body := `{"name":"Eight CI runners","patch":[]}`
	a, err := fingerprint(v1, []byte(body))
	if err != nil {
		t.Fatal(err)
	}
	for name, c := range map[string]struct {
		version, body string
		same          bool
	}{
		"the same request":    {v1, body, true},
		"another spelling":    {v1, "{ \"patch\" : [ ], \"name\":\"Eight CI\\u0020runners\" }\n", true},
		"another version":     {v2, body, false},
		"another name":        {v1, `{"name":"Nine CI runners","patch":[]}`, false},
		"another patch":       {v1, `{"name":"Eight CI runners","patch":[` + testCI + `]}`, false},
		"another white space": {v1, `{"name":"Eight CI runners ","patch":[]}`, false},
	} {
		b, err := fingerprint(c.version, []byte(c.body))
		if err != nil || (a == b) != c.same {
			t.Errorf("%s: %s vs %s (%v)", name, a, b, err)
		}
	}
	if _, err := fingerprint(v1, []byte(`{"name":`)); err == nil {
		t.Error("a body that is not JSON has a fingerprint")
	}
}

// API_DATABASE §3: a cursor names its organization, version and list; any
// other is refused.
func TestCursor(t *testing.T) {
	org, other, v := "org_01M4YDQK00Y5H2929M9WWZWTVH", "org_01M4YDQK00Y5H2929M9WWZWTVJ", "mv_01M4YDQK00FRYWV7GS7X1QCXZR"
	pos := store.Position{CreatedAt: time.Date(2026, 10, 25, 9, 0, 0, 123456000, time.UTC), ID: "scn_01M4YDQK00J4MJS82YRG0PCEC1"}
	c := encodeCursor(org, v, pos)
	got, err := decodeCursor(c, org, v)
	if err != nil || !got.CreatedAt.Equal(pos.CreatedAt) || got.ID != pos.ID {
		t.Errorf("round trip: %+v %v", got, err)
	}
	for name, s := range map[string]string{
		"another organization": encodeCursor(other, v, pos),
		"another version":      encodeCursor(org, "mv_01M4YDQK00FRYWV7GS7X1QCXZS", pos),
		"not base64":           "!!",
		"not JSON":             "bm90IGpzb24",
		"a proposal ID":        encodeCursor(org, v, store.Position{CreatedAt: pos.CreatedAt, ID: "aip_01M4YDQK00Q10EM95Z6H4XK0Y8"}),
	} {
		if _, err := decodeCursor(s, org, v); !errors.Is(err, ErrInvalidCursor) {
			t.Errorf("%s: %v", name, err)
		}
	}
	// Another list's cursor of the same organization and version.
	data, _ := json.Marshal(map[string]any{"v": 1, "o": org, "l": "simulations", "b": v, "t": pos.CreatedAt.Format(time.RFC3339Nano), "i": pos.ID})
	if _, err := decodeCursor(encodeRaw(data), org, v); !errors.Is(err, ErrInvalidCursor) {
		t.Errorf("another list's cursor: %v", err)
	}
}

// The routes are the contract's operations with its roles (TEST-009):
// a viewer reads scenarios and creates none.
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
	if want := []string{"createManualScenario", "listScenarios", "getScenario"}; !slices.Equal(got, want) {
		t.Fatalf("routes %v, want %v", got, want)
	}
	if slices.Contains(Routes[0].Roles, tenant.Viewer) || !slices.Contains(Routes[1].Roles, tenant.Viewer) {
		t.Errorf("roles %v", Routes)
	}
}

// Each refusal is the contract's code, with the JSON Pointer of what was
// refused and, for a storage failure, never its cause.
func TestRefused(t *testing.T) {
	issue := model.NewIssue(patch.PathForbidden, "/patch/1/path", "the scenario allowlist does not include /nodes/5", model.SeverityError)
	name := model.NewIssue(validate.ConstraintFailed, "/name", "only white space", model.SeverityError)
	for _, c := range []struct {
		err  error
		code httpapi.Code
		path string
	}{
		{ErrNotFound, httpapi.NotFound, ""},
		{&RequestError{Issues: []model.ValidationIssue{name}}, httpapi.RequestValidationFailed, "/name"},
		{&InvalidError{Issues: []model.ValidationIssue{issue}}, httpapi.ModelValidationFailed, "/patch/1/path"},
		{ErrStorageUnavailable, httpapi.ServiceUnavailable, ""},
		{&DatasetStorageError{Err: errors.New("open /srv/objects/tenant/secret: i/o error")}, httpapi.ServiceUnavailable, ""},
		{ErrInvalidCursor, httpapi.InvalidRequest, ""},
		{ErrInvalidLimit, httpapi.InvalidRequest, ""},
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
		// What the client is sent: the code, the message and the details.
		if sent := fmt.Sprintf("%s %s %+v", he.Code, he.Message, he.Details); strings.Contains(sent, "secret") {
			t.Errorf("%v: the cause is sent: %s", c.err, sent)
		}
	}
	var he *httpapi.Error
	if errors.As(Refused(&InvalidError{Issues: []model.ValidationIssue{issue}}), &he); he.Details[0].Code != patch.PathForbidden ||
		he.Details[0].Severity != "error" || he.Details[0].Reason != issue.Message {
		t.Errorf("an issue is lost: %+v", he.Details)
	}
	he = nil
	if errors.As(Refused(&DatasetStorageError{Err: errors.New("x")}), &he); len(he.Details) != 1 || he.Details[0].Code != "DATASET_STORAGE_FAILED" {
		t.Errorf("a storage failure: %+v", he)
	}
	for _, err := range []error{tenant.ErrForbidden, tenant.ErrNoMembership, ErrCorrupt, errors.New("a server fault")} {
		if Refused(err) != err {
			t.Errorf("%v was mapped", err)
		}
	}
}

func encodeRaw(data []byte) string { return base64.RawURLEncoding.EncodeToString(data) }
