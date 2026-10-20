package review

import (
	"bytes"
	"encoding/json/jsontext"
	"encoding/json/v2"
	"fmt"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/kcl1104/geminus/internal/ai/idpool"
	"github.com/kcl1104/geminus/internal/ai/provider"
	"github.com/kcl1104/geminus/internal/contracttest"
	"github.com/kcl1104/geminus/internal/domainpack/softwaredelivery"
	"github.com/kcl1104/geminus/internal/model"
	"github.com/kcl1104/geminus/internal/model/hashing"
	"github.com/kcl1104/geminus/internal/model/patch"
	"github.com/kcl1104/geminus/internal/model/validate"
)

// The base of these tests is the synthetic default pipeline fixture. Its
// indices: nodes 0 change_arrival (source), 1 code_review, 2 ci, 3
// ci_result (decision), 4 ci_failure (failure), 5 ci_rework (rework), 6
// test, 7 deploy, 8 deployed (sink); flows 0 f1, 1 f2, 2 f3, 3 f_pass (0.8),
// 4 f_retry (0.2, into the failure node), 5 f_fail_rework, 6 f_rework_ci,
// 7 f_test_deploy, 8 f_deploy_sink; resources 0 reviewer (2), 1 ci_runner
// (3), 2 test_environment (1), 3 deployment_slot (1).
func base(t testing.TB) []byte {
	t.Helper()
	raw := contracttest.ReadFile(t, filepath.Join(contracttest.Root(t), "testdata", "golden-models", "default-delivery.json"))
	var doc map[string]any
	if err := json.Unmarshal(raw, &doc); err != nil {
		t.Fatal(err)
	}
	doc["model_id"] = "mdl_01M4YDQK00TA00000000000001"
	data, err := json.Marshal(doc)
	if err != nil {
		t.Fatal(err)
	}
	c, err := hashing.Canonical(data)
	if err != nil {
		t.Fatal(err)
	}
	return c
}

func decoded(t testing.TB, doc []byte) *model.ModelIR {
	t.Helper()
	var ir model.ModelIR
	if err := model.Decode(doc, &ir); err != nil {
		t.Fatal(err)
	}
	return &ir
}

func pool(t testing.TB, doc []byte) idpool.Pool {
	t.Helper()
	p, err := idpool.New(decoded(t, doc))
	if err != nil {
		t.Fatal(err)
	}
	return p
}

func ops(t testing.TB, text string) model.Patch {
	t.Helper()
	var p model.Patch
	if err := model.Decode([]byte(text), &p); err != nil {
		t.Fatalf("test patch: %v", err)
	}
	return p
}

func confidence(v float64) *float64 { return &v }

// proposed is a schema-valid provider answer with the given changes.
func proposed(changes model.Patch, claim string) provider.ProposalOutput {
	return provider.ProposalOutput{Status: "proposed", Summary: "Raise a capacity.", Assumptions: []string{"Nothing else changes."},
		RiskLevel: claim, Confidence: confidence(0.9), Changes: changes, ExpectedEffects: []string{"Waiting may fall; a simulation will tell."},
		RequiresConfirmation: true}
}

func input(t testing.TB, mode provider.Mode, out provider.ProposalOutput) Input {
	t.Helper()
	doc := base(t)
	return Input{Mode: mode, Base: doc, Pool: pool(t, doc), Pack: softwaredelivery.Pack{}, Output: out}
}

func run(t *testing.T, in Input) Outcome {
	t.Helper()
	o, err := Review(in)
	if err != nil {
		t.Fatalf("internal error: %v", err)
	}
	return o
}

func codes(o Outcome) map[string][]string {
	out := map[string][]string{}
	if o.Validation == nil {
		return out
	}
	for _, i := range o.Validation.Issues {
		out[i.Path] = append(out[i.Path], i.Code)
	}
	return out
}

// wantInvalid checks that o is invalid with code at path, and that an
// invalid outcome carries nothing the provider wrote: no changes, no
// prose, no risk (AI_SPEC §4: issues and metadata only), and issues whose
// messages are fixed sentences and whose paths hold only known tokens.
func wantInvalid(t *testing.T, o Outcome, code, path string) {
	t.Helper()
	if o.Status != Invalid {
		t.Fatalf("status %q, want invalid (%v)", o.Status, codes(o))
	}
	if o.Validation == nil || o.Validation.Valid || !slices.Contains(codes(o)[path], code) {
		t.Fatalf("want %s at %q, got %v", code, path, codes(o))
	}
	if o.Changes != nil || o.PatchHash != "" || o.Candidate != nil || o.Summary != "" || o.Assumptions != nil ||
		o.ExpectedEffects != nil || o.Questions != nil || o.Confidence != nil || o.ServerRisk != "" || o.ProviderRisk != "" || o.Risk != "" {
		t.Fatalf("an invalid outcome kept provider content: %+v", o)
	}
	wantClean(t, o)
}

// wantClean checks that every issue of o is as Clean leaves it: a message
// from the code's fixed sentences (or capped's count) and a path of array
// indexes and schema member names only (H-003 review M1).
func wantClean(t *testing.T, o Outcome) {
	t.Helper()
	for _, is := range o.Validation.Issues {
		fixed := slices.Contains(sentences[is.Code], is.Message) || (is.Code == validate.LimitExceeded && moreIssues.MatchString(is.Message)) ||
			(sentences[is.Code] == nil && is.Message == otherSentence)
		if !fixed {
			t.Errorf("%s at %q: the message %q is not a fixed sentence", is.Code, is.Path, is.Message)
		}
		if knownPath(is.Path) != is.Path {
			t.Errorf("%s: the path %q holds a token the schemas do not know", is.Code, is.Path)
		}
		if Clean(is) != is {
			t.Errorf("%+v is not clean", is)
		}
	}
}

// TEST-003, REQ-008: what the patch profile refuses never becomes ready,
// and nothing of a refused patch is kept.
func TestRefusesUnsafeChanges(t *testing.T) {
	cases := []struct {
		name, changes, code, path string
	}{
		{"root", `[{"op":"replace","path":"","value":{}}]`, patch.SchemaInvalid, "/changes/0/path"},
		// model.Patch has no "from": a move or copy op is refused by its op.
		{"move", `[{"op":"move","path":"/name"}]`, patch.SchemaInvalid, "/changes/0/op"},
		{"copy", `[{"op":"copy","path":"/name"}]`, patch.SchemaInvalid, "/changes/0/op"},
		{"identity", `[{"op":"replace","path":"/model_id","value":"mdl_01M4YDQK00TB00000000000001"}]`, patch.PathForbidden, "/changes/0/path"},
		{"schema version", `[{"op":"replace","path":"/schema_version","value":"0.2"}]`, patch.PathForbidden, "/changes/0/path"},
		{"domain", `[{"op":"replace","path":"/domain","value":"other"}]`, patch.PathForbidden, "/changes/0/path"},
		{"views", `[{"op":"replace","path":"/views/2d/positions/ci","value":{"x":1,"y":2}}]`, patch.PathForbidden, "/changes/0/path"},
		{"escape confusion", `[{"op":"test","path":"/resources/1/id","value":"ci_runner"},{"op":"replace","path":"/resources~11~1capacity","value":6}]`, patch.PathForbidden, "/changes/1/path"},
		{"no test first", `[{"op":"replace","path":"/resources/1/capacity","value":6}]`, patch.TestRequired, "/changes/0/path"},
		{"wrong test", `[{"op":"test","path":"/resources/1/id","value":"reviewer"},{"op":"replace","path":"/resources/1/capacity","value":6}]`, patch.TestFailed, "/changes/0/value"},
		{"missing target", `[{"op":"test","path":"/resources/9/id","value":"x"},{"op":"replace","path":"/resources/9/capacity","value":6}]`, patch.TargetMissing, "/changes/0/path"},
		// The second op fails, so the first is not kept either (atomic).
		{"second op fails", `[{"op":"test","path":"/resources/1/id","value":"ci_runner"},{"op":"replace","path":"/resources/1/capacity","value":6},
			{"op":"test","path":"/resources/0/id","value":"nobody"},{"op":"replace","path":"/resources/0/capacity","value":3}]`, patch.TestFailed, "/changes/2/value"},
		{"whole catalog", `[{"op":"replace","path":"/resources","value":[]}]`, patch.TestRequired, "/changes/0/path"},
		// The candidate model is validated in full.
		{"zero capacity", `[{"op":"test","path":"/resources/1/id","value":"ci_runner"},{"op":"replace","path":"/resources/1/capacity","value":0}]`, validate.IRSchemaInvalid, "/resources/1/capacity"},
		{"fractional capacity", `[{"op":"test","path":"/resources/0/id","value":"reviewer"},{"op":"replace","path":"/resources/0/capacity","value":2.5}]`, validate.IRSchemaInvalid, "/resources/0/capacity"},
		{"probability over one", `[{"op":"test","path":"/flows/4/id","value":"f_retry"},{"op":"replace","path":"/flows/4/routing/probability","value":1.5}]`, validate.IRSchemaInvalid, "/flows/4/routing/probability"},
		{"probabilities do not sum", `[{"op":"test","path":"/flows/4/id","value":"f_retry"},{"op":"replace","path":"/flows/4/routing/probability","value":0.5}]`, validate.RoutingInvalid, "/nodes/3"},
		{"negative time", `[{"op":"test","path":"/nodes/2/id","value":"ci"},{"op":"replace","path":"/nodes/2/service_time/distribution","value":{"type":"constant","value":-5}}]`, validate.IRSchemaInvalid, "/nodes/2/service_time/distribution/value"},
		{"triangular out of order", `[{"op":"test","path":"/nodes/2/id","value":"ci"},{"op":"replace","path":"/nodes/2/service_time/distribution","value":{"type":"triangular","min":30,"mode":10,"max":20}}]`, validate.DistributionInvalid, "/nodes/2/service_time/distribution"},
		{"P1 distribution", `[{"op":"test","path":"/nodes/1/id","value":"code_review"},{"op":"replace","path":"/nodes/1/service_time/distribution","value":{"type":"mixture","mean":3,"cv":1}}]`, validate.IRSchemaInvalid, "/nodes/1/service_time/distribution/type"},
		// D-056 moved lognormal to P0; until T-048 runs it the primitive gate refuses it (D-063 PROPOSED).
		{"lognormal before its kernel", `[{"op":"test","path":"/nodes/1/id","value":"code_review"},{"op":"replace","path":"/nodes/1/service_time/distribution","value":{"type":"lognormal","mean":3,"cv":1}}]`, validate.PrimitiveNotSupported, "/nodes/1/service_time/distribution"},
		{"calendars before their kernel", `[{"op":"add","path":"/calendars","value":[{"id":"c","period":1440,"shifts":[{"start":0,"end":480}]}]}]`, validate.PrimitiveNotSupported, "/calendars"},
		{"dangling reference", `[{"op":"test","path":"/flows/0/id","value":"f1"},{"op":"replace","path":"/flows/0/to","value":"nowhere"}]`, validate.UnknownReference, "/flows/0/to"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			in := input(t, provider.ModeEdit, proposed(ops(t, c.changes), "low"))
			wantInvalid(t, run(t, in), c.code, c.path)
		})
	}
}

// D-021: a scenario only changes capacities, distributions and routing
// probabilities; it never adds or removes entities.
func TestScenarioScope(t *testing.T) {
	for name, changes := range map[string]string{
		"remove a node":   `[{"op":"test","path":"/nodes/2/id","value":"ci"},{"op":"remove","path":"/nodes/2"}]`,
		"rename":          `[{"op":"replace","path":"/name","value":"Other"}]`,
		"add a resource":  `[{"op":"add","path":"/resources/-","value":{"id":"resource_01m4ydqk00ta00000000000001","type":"generic","name":"r","capacity":1,"queue_discipline":"fifo"}}]`,
		"rewire a flow":   `[{"op":"test","path":"/flows/0/id","value":"f1"},{"op":"replace","path":"/flows/0/to","value":"ci"}]`,
		"change metadata": `[{"op":"add","path":"/metadata","value":{"a":1}}]`,
	} {
		t.Run(name, func(t *testing.T) {
			wantInvalid(t, run(t, input(t, provider.ModeScenario, proposed(ops(t, changes), "low"))), patch.PathForbidden, "/changes/"+lastOp(changes)+"/path")
		})
	}
	o := run(t, input(t, provider.ModeScenario, proposed(ops(t,
		`[{"op":"test","path":"/resources/1/id","value":"ci_runner"},{"op":"replace","path":"/resources/1/capacity","value":6}]`), "low")))
	if o.Status != Ready || o.ServerRisk != Low {
		t.Fatalf("a capacity scenario: %q %q %v", o.Status, o.ServerRisk, codes(o))
	}
}

func lastOp(changes string) string {
	var p []any
	_ = json.Unmarshal([]byte(changes), &p)
	return fmt.Sprint(len(p) - 1)
}

// D-046, D-016: a new node, flow or resource takes its ID from the
// proposal's pool, from the list of its own kind; the provider never mints
// one. The violation points at the ID and never quotes it.
func TestNewIDsComeFromThePool(t *testing.T) {
	doc := base(t)
	p := pool(t, doc)
	add := func(nodeID, flowID string) string {
		return `[{"op":"add","path":"/nodes/-","value":{"id":"` + nodeID + `","type":"queue","name":"Wait"}},
			{"op":"test","path":"/flows/3/id","value":"f_pass"},{"op":"replace","path":"/flows/3/to","value":"` + nodeID + `"},
			{"op":"add","path":"/flows/-","value":{"id":"` + flowID + `","from":"` + nodeID + `","to":"test","routing":{"type":"probability","probability":1}}}]`
	}
	in := Input{Mode: provider.ModeEdit, Base: doc, Pool: p, Pack: softwaredelivery.Pack{}}
	in.Output = proposed(ops(t, add(p.Node[0], p.Flow[0])), "low")
	o := run(t, in)
	if o.Status != Ready {
		t.Fatalf("pool IDs: %q %v", o.Status, codes(o))
	}
	if o.ServerRisk != Medium {
		t.Errorf("adding a node and a flow is at least medium, got %q", o.ServerRisk)
	}
	for name, c := range map[string]struct{ node, flow, path string }{
		"invented node ID": {"queue_before_test", p.Flow[0], "/nodes/9/id"},
		"invented flow ID": {p.Node[0], "f_new", "/flows/9/id"},
		"another kind's":   {p.Flow[1], p.Flow[0], "/nodes/9/id"},
		"a server-like ID": {"node_01m4ydqk00ta99999999999999", p.Flow[0], "/nodes/9/id"},
	} {
		t.Run(name, func(t *testing.T) {
			in.Output = proposed(ops(t, add(c.node, c.flow)), "low")
			o := run(t, in)
			wantInvalid(t, o, IDPoolViolation, c.path)
			for _, is := range o.Validation.Issues {
				if strings.Contains(is.Message, c.node) || strings.Contains(is.Message, c.flow) {
					t.Errorf("the issue quotes the provider's ID: %q", is.Message)
				}
			}
		})
	}
	// A changed ID is a new entity too.
	in.Output = proposed(ops(t, `[{"op":"test","path":"/resources/3/id","value":"deployment_slot"},{"op":"replace","path":"/resources/3/id","value":"slot2"},
		{"op":"test","path":"/nodes/7/id","value":"deploy"},{"op":"replace","path":"/nodes/7/requires/0/resource_id","value":"slot2"}]`), "low")
	wantInvalid(t, run(t, in), IDPoolViolation, "/resources/3/id")
}

// D-016, D-027: the server computes the risk from the base and the
// candidate; the provider's claim can raise it, never lower it.
func TestRiskIsTheServers(t *testing.T) {
	raise := `[{"op":"test","path":"/resources/1/id","value":"ci_runner"},{"op":"replace","path":"/resources/1/capacity","value":6}]`
	del := `[{"op":"test","path":"/flows/7/id","value":"f_test_deploy"},{"op":"replace","path":"/flows/7/to","value":"deployed"},
		{"op":"test","path":"/flows/8/id","value":"f_deploy_sink"},{"op":"remove","path":"/flows/8"},
		{"op":"test","path":"/nodes/7/id","value":"deploy"},{"op":"remove","path":"/nodes/7"},
		{"op":"remove","path":"/views/2d/positions/deploy"}]`
	for _, c := range []struct {
		name, changes, claim string
		server, risk         Risk
	}{
		{"low claimed, low computed", raise, "low", Low, Low},
		{"the claim raises", raise, "high", Low, High},
		{"a deletion claimed low", del, "low", High, High},
		{"a deletion claimed medium", del, "medium", High, High},
	} {
		t.Run(c.name, func(t *testing.T) {
			o := run(t, input(t, provider.ModeEdit, proposed(ops(t, c.changes), c.claim)))
			if o.Status != Ready || o.ServerRisk != c.server || o.ProviderRisk != Risk(c.claim) || o.Risk != c.risk {
				t.Fatalf("status %q server %q claim %q risk %q, want ready %q %q %q (%v)", o.Status, o.ServerRisk, o.ProviderRisk, o.Risk,
					c.server, c.claim, c.risk, codes(o))
			}
		})
	}
}

func TestServerRisk(t *testing.T) {
	doc := base(t)
	p := pool(t, doc)
	for _, c := range []struct {
		name, changes string
		want          Risk
	}{
		{"raise a capacity", `[{"op":"test","path":"/resources/1/id","value":"ci_runner"},{"op":"replace","path":"/resources/1/capacity","value":6}]`, Low},
		{"lower a capacity", `[{"op":"test","path":"/resources/0/id","value":"reviewer"},{"op":"replace","path":"/resources/0/capacity","value":1}]`, Medium},
		{"rename", `[{"op":"replace","path":"/name","value":"Other name"}]`, Low},
		{"another distribution", `[{"op":"test","path":"/nodes/2/id","value":"ci"},{"op":"replace","path":"/nodes/2/service_time/distribution","value":{"type":"exponential","mean":18}}]`, Low},
		{"less failure routing", `[{"op":"test","path":"/flows/4/id","value":"f_retry"},{"op":"replace","path":"/flows/4/routing/probability","value":0.1},
			{"op":"test","path":"/flows/3/id","value":"f_pass"},{"op":"replace","path":"/flows/3/routing/probability","value":0.9}]`, Low},
		{"more failure routing", `[{"op":"test","path":"/flows/4/id","value":"f_retry"},{"op":"replace","path":"/flows/4/routing/probability","value":0.3},
			{"op":"test","path":"/flows/3/id","value":"f_pass"},{"op":"replace","path":"/flows/3/routing/probability","value":0.7}]`, Medium},
		{"more units per job", `[{"op":"test","path":"/nodes/2/id","value":"ci"},{"op":"replace","path":"/nodes/2/requires/0/units","value":2}]`, Medium},
		// Rework goes back to the review instead of the CI step: a cycle
		// through the review that the base does not have.
		{"a new cycle", `[{"op":"test","path":"/flows/6/id","value":"f_rework_ci"},{"op":"replace","path":"/flows/6/to","value":"code_review"}]`, Medium},
		{"remove a resource", `[{"op":"test","path":"/nodes/7/id","value":"deploy"},{"op":"replace","path":"/nodes/7/requires","value":[]},
			{"op":"test","path":"/resources/3/id","value":"deployment_slot"},{"op":"remove","path":"/resources/3"}]`, High},
		{"add a resource", `[{"op":"add","path":"/resources/-","value":{"id":"` + p.Resource[0] + `","type":"generic","name":"Spare","capacity":1,"queue_discipline":"fifo"}}]`, Medium},
		// Order is not identity: the same resource moved to the end is no deletion.
		{"reorder", `[{"op":"test","path":"/resources/0/id","value":"reviewer"},{"op":"remove","path":"/resources/0"},
			{"op":"add","path":"/resources/-","value":{"id":"reviewer","type":"human","name":"Reviewer","capacity":2,"queue_discipline":"fifo"}}]`, Low},
		{"remove a metric", `[{"op":"test","path":"/metrics/4/id","value":"reworks"},{"op":"remove","path":"/metrics/4"}]`, Medium},
	} {
		t.Run(c.name, func(t *testing.T) {
			res, err := patch.Apply(doc, []byte(c.changes), patch.AIEdit)
			if err != nil || !res.Valid() {
				t.Fatalf("test patch does not apply: %v %v %v", err, res.PatchIssues, res.Validation.Issues)
			}
			if got := ServerRisk(decoded(t, doc), res.Validation.IR); got != c.want {
				t.Fatalf("risk %q, want %q", got, c.want)
			}
		})
	}
}

func TestReady(t *testing.T) {
	changes := `[{"op":"test","path":"/resources/1/id","value":"ci_runner"},{"op":"replace","path":"/resources/1/capacity","value":6}]`
	in := input(t, provider.ModeEdit, proposed(ops(t, changes), "low"))
	before := bytes.Clone(in.Base)
	o := run(t, in)
	if o.Status != Ready || o.Validation == nil || !o.Validation.Valid || o.Candidate == nil {
		t.Fatalf("not ready: %q %v", o.Status, codes(o))
	}
	canonical, err := hashing.Canonical([]byte(changes))
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(o.Changes, canonical) {
		t.Errorf("changes %s, want the canonical patch %s", o.Changes, canonical)
	}
	if want, _ := hashing.Patch(canonical); o.PatchHash != want {
		t.Errorf("patch_hash %s, want %s (D-019)", o.PatchHash, want)
	}
	if o.Candidate.Resources[1].Capacity != 6 {
		t.Errorf("candidate capacity %d", o.Candidate.Resources[1].Capacity)
	}
	if o.Summary != "Raise a capacity." || len(o.Assumptions) != 1 || len(o.ExpectedEffects) != 1 || *o.Confidence != 0.9 || o.Questions != nil {
		t.Errorf("content: %+v", o)
	}
	if !bytes.Equal(in.Base, before) {
		t.Error("the review changed the base document")
	}
	again := run(t, input(t, provider.ModeEdit, proposed(ops(t, changes), "low")))
	a, _ := json.Marshal(o, json.Deterministic(true))
	b, _ := json.Marshal(again, json.Deterministic(true))
	if !bytes.Equal(a, b) {
		t.Errorf("the review is not deterministic:\n%s\n%s", a, b)
	}
}

// Validation warnings stay on a ready proposal, for the reviewer to see.
func TestWarningsStay(t *testing.T) {
	doc := base(t)
	p := pool(t, doc)
	// A queue no flow enters: UNREACHABLE_NODE, a warning.
	changes := `[{"op":"add","path":"/nodes/-","value":{"id":"` + p.Node[0] + `","type":"queue"}},
		{"op":"add","path":"/flows/-","value":{"id":"` + p.Flow[0] + `","from":"` + p.Node[0] + `","to":"test","routing":{"type":"probability","probability":1}}}]`
	o := run(t, Input{Mode: provider.ModeEdit, Base: doc, Pool: p, Pack: softwaredelivery.Pack{}, Output: proposed(ops(t, changes), "low")})
	if o.Status != Ready || !slices.Contains(codes(o)["/nodes/9"], validate.UnreachableNode) {
		t.Fatalf("%q %v", o.Status, codes(o))
	}
}

// A patch that leaves the model as it is is no proposal.
func TestNoChange(t *testing.T) {
	o := run(t, input(t, provider.ModeEdit, proposed(ops(t, `[{"op":"test","path":"/resources/1/id","value":"ci_runner"}]`), "low")))
	wantInvalid(t, o, OutputInvalid, "/changes")
}

// AI_SPEC §5: expected effects are qualitative; a number that is not a
// value of the base or the candidate is a fabricated prediction, and the
// proposal is not ready. The review does not see the intent, so a number
// the person states is no exception: a demanded outcome restated as an
// effect is still a prediction (eval MOD-050); what the person supplies
// belongs in the assumptions.
func TestExpectedEffectsAreQualitative(t *testing.T) {
	changes := ops(t, `[{"op":"test","path":"/resources/1/id","value":"ci_runner"},{"op":"replace","path":"/resources/1/capacity","value":6}]`)
	for _, c := range []struct {
		effects []string
		ok      bool
	}{
		{[]string{"Waiting at the step may fall; simulate to know by how much."}, true},
		{[]string{"Capacity goes from 3 to 6."}, true},
		{[]string{"Arrivals stay at a mean of 48 minutes."}, true},
		{[]string{"The failure share stays at 20%."}, true},
		{[]string{"Fits the 30 changes a day the intent states."}, false},
		{[]string{"Lead time drops 11%, as asked."}, false},
		{[]string{"Waiting may fall.", "Throughput rises by 17 a day."}, false},
		{[]string{"等待時間將減少 11%。"}, false},
		{[]string{"交期縮短四十七分鐘。"}, false},
	} {
		out := proposed(changes, "low")
		out.ExpectedEffects = c.effects
		o := run(t, input(t, provider.ModeEdit, out))
		switch {
		case c.ok && o.Status != Ready:
			t.Errorf("%q: %q %v", c.effects, o.Status, codes(o))
		case !c.ok:
			wantInvalid(t, o, OutputInvalid, fmt.Sprintf("/expected_effects/%d", len(c.effects)-1))
		}
	}
}

// H-003 review M3: the numbers in names count only as the base has them. A
// name is the provider's own text when its patch writes it, so a number
// written into a name the patch adds or renames is no value of the model:
// the effect that states it is still a fabricated prediction. A number in a
// name of the base, and every numeric parameter of the candidate, count.
func TestNamesDoNotSupplyNumbers(t *testing.T) {
	raise := `{"op":"test","path":"/resources/1/id","value":"ci_runner"},{"op":"replace","path":"/resources/1/capacity","value":6}`
	doc := base(t)
	p := pool(t, doc)
	for _, c := range []struct {
		name, changes, effect string
	}{
		{"the model's name", `[{"op":"replace","path":"/name","value":"Pipeline 11"},` + raise + `]`, "Lead time drops 11%."},
		{"a resource's name", `[` + raise + `,{"op":"replace","path":"/resources/1/name","value":"Runner pool 12"}]`, "Waiting falls by 12 minutes."},
		{"an added node's name", `[{"op":"add","path":"/nodes/-","value":{"id":"` + p.Node[0] + `","type":"queue","name":"Queue 37"}},` +
			`{"op":"add","path":"/flows/-","value":{"id":"` + p.Flow[0] + `","from":"` + p.Node[0] + `","to":"test","routing":{"type":"probability","probability":1}}}]`,
			"Throughput rises by 37 a day."},
	} {
		t.Run(c.name, func(t *testing.T) {
			out := proposed(ops(t, c.changes), "low")
			out.ExpectedEffects = []string{c.effect}
			wantInvalid(t, run(t, Input{Mode: provider.ModeEdit, Base: doc, Pool: p, Pack: softwaredelivery.Pack{}, Output: out}),
				OutputInvalid, "/expected_effects/0")
		})
	}
	// A base whose resource name holds a number: an effect may name it,
	// and the candidate's new capacity.
	named, err := patch.Apply(doc, []byte(`[{"op":"test","path":"/resources/1/id","value":"ci_runner"},{"op":"replace","path":"/resources/1/name","value":"Runner pool 4"}]`), patch.ManualDraft)
	if err != nil || !named.Valid() {
		t.Fatalf("%v %v", err, named.PatchIssues)
	}
	out := proposed(ops(t, `[`+raise+`]`), "low")
	out.ExpectedEffects = []string{"Runner pool 4 waits less with 6 runners."}
	if o := run(t, Input{Mode: provider.ModeEdit, Base: named.Candidate, Pool: pool(t, named.Candidate), Pack: softwaredelivery.Pack{}, Output: out}); o.Status != Ready {
		t.Fatalf("a base name's number: %q %v", o.Status, codes(o))
	}
}

func TestNeedsInput(t *testing.T) {
	out := provider.ProposalOutput{Status: "needs_input", Summary: "Which step?", Assumptions: []string{}, Questions: []string{"Which step should change?"},
		RequiresConfirmation: true}
	o := run(t, input(t, provider.ModeEdit, out))
	if o.Status != NeedsInput || o.Summary != "Which step?" || !slices.Equal(o.Questions, []string{"Which step should change?"}) {
		t.Fatalf("%+v", o)
	}
	if o.Changes != nil || o.Validation != nil || o.Risk != "" || o.Confidence != nil || o.Candidate != nil {
		t.Fatalf("needs_input carries no executable change, validation or risk: %+v", o)
	}
	// Changes smuggled into needs_input are refused, not dropped.
	out.Changes = ops(t, `[{"op":"replace","path":"/name","value":"x"}]`)
	wantInvalid(t, run(t, input(t, provider.ModeEdit, out)), OutputInvalid, "/changes")
}

// Answers the schema already refuses never get past the review either.
func TestRefusesWhatTheSchemaRefuses(t *testing.T) {
	ok := ops(t, `[{"op":"test","path":"/resources/1/id","value":"ci_runner"},{"op":"replace","path":"/resources/1/capacity","value":6}]`)
	for name, c := range map[string]struct {
		edit func(*provider.ProposalOutput)
		path string
	}{
		"no confirmation":         {func(o *provider.ProposalOutput) { o.RequiresConfirmation = false }, "/requires_confirmation"},
		"approved":                {func(o *provider.ProposalOutput) { o.Status = "approved" }, "/status"},
		"ready":                   {func(o *provider.ProposalOutput) { o.Status = "ready" }, "/status"},
		"no changes":              {func(o *provider.ProposalOutput) { o.Changes = nil }, "/changes"},
		"no confidence":           {func(o *provider.ProposalOutput) { o.Confidence = nil }, "/confidence"},
		"unknown risk":            {func(o *provider.ProposalOutput) { o.RiskLevel = "none" }, "/risk_level"},
		"questions on a proposal": {func(o *provider.ProposalOutput) { o.Questions = []string{"?"} }, "/questions"},
	} {
		t.Run(name, func(t *testing.T) {
			out := proposed(ok, "low")
			c.edit(&out)
			wantInvalid(t, run(t, input(t, provider.ModeEdit, out)), OutputInvalid, c.path)
		})
	}
}

// T-024's invalid_output issues become the proposal's validation, cleaned
// like the review's own: a fixed message, and a path cut at the first
// member the schemas do not have (an "approved" member is the provider's
// word, not the schema's), so nothing of the answer is kept.
func TestInvalidOutput(t *testing.T) {
	o := InvalidOutput([]model.ValidationIssue{
		model.NewIssue(provider.OutputInvalid, "/approved", "does not match proposal-output.schema.json", model.SeverityError),
		model.NewIssue(provider.OutputInvalid, "/changes/0/op", "does not match proposal-output.schema.json", model.SeverityError)})
	wantInvalid(t, o, OutputInvalid, "")
	wantInvalid(t, o, OutputInvalid, "/changes/0/op")
	for _, is := range o.Validation.Issues {
		if is.Message != sentNotAProposal {
			t.Errorf("%q: the message %q is not the code's sentence", is.Path, is.Message)
		}
	}
	o = InvalidOutput(nil)
	wantInvalid(t, o, OutputInvalid, "")
}

// The dataset binding stage runs in the adapter; its errors make a ready
// outcome invalid and drop everything the provider wrote.
func TestRefuse(t *testing.T) {
	o := run(t, input(t, provider.ModeEdit, proposed(ops(t,
		`[{"op":"test","path":"/resources/1/id","value":"ci_runner"},{"op":"replace","path":"/resources/1/capacity","value":6}]`), "low")))
	warn := model.NewIssue(validate.DatasetBindingInvalid, "/x", "a warning", model.SeverityWarning)
	if kept := o.Refuse([]model.ValidationIssue{warn}); kept.Status != Ready || len(kept.Validation.Issues) != 1 {
		t.Fatalf("a warning alone keeps the outcome ready: %+v", kept.Validation)
	}
	bad := model.NewIssue(validate.DatasetBindingInvalid, "/nodes/2/service_time/distribution/samples_ref", "unbound", model.SeverityError)
	wantInvalid(t, o.Refuse([]model.ValidationIssue{bad}), validate.DatasetBindingInvalid, "/nodes/2/service_time/distribution/samples_ref")
}

// Error.details and Validation.issues hold at most 1000 issues; the first
// one says how many are left out.
func TestIssuesAreCapped(t *testing.T) {
	var issues []model.ValidationIssue
	for i := range 1500 {
		issues = append(issues, model.NewIssue(provider.OutputInvalid, fmt.Sprintf("/x/%d", i), "m", model.SeverityError))
	}
	o := InvalidOutput(issues)
	if n := len(o.Validation.Issues); n != 1000 || o.Validation.Issues[0].Code != validate.LimitExceeded {
		t.Fatalf("%d issues, first %v", n, o.Validation.Issues[0])
	}
}

func TestInputRefused(t *testing.T) {
	good := input(t, provider.ModeEdit, proposed(ops(t, `[{"op":"replace","path":"/name","value":"x"}]`), "low"))
	for name, edit := range map[string]func(*Input){
		"unknown mode": func(in *Input) { in.Mode = "publish" },
		"no pack":      func(in *Input) { in.Pack = nil },
		"no base":      func(in *Input) { in.Base = nil },
		"invalid base": func(in *Input) { in.Base = []byte(`{"schema_version":"0.1"}`) },
		"bad pool":     func(in *Input) { in.Pool = idpool.Pool{} },
		"pool in base": func(in *Input) { in.Pool.Node[0] = "ci" },
	} {
		t.Run(name, func(t *testing.T) {
			in := good
			in.Pool = idpool.Pool{Node: slices.Clone(good.Pool.Node), Flow: good.Pool.Flow, Resource: good.Pool.Resource}
			edit(&in)
			if _, err := Review(in); err == nil {
				t.Fatal("accepted")
			}
		})
	}
}

// The codes are the contract's (common.schema.json ValidationCode), and
// the invalid-output code is the one the provider client issues.
func TestCodes(t *testing.T) {
	if OutputInvalid != provider.OutputInvalid {
		t.Fatalf("%s != %s", OutputInvalid, provider.OutputInvalid)
	}
	var doc struct {
		Defs struct {
			ValidationCode struct {
				Enum []string `json:"enum"`
			} `json:"ValidationCode"`
		} `json:"$defs"`
	}
	if err := json.Unmarshal(contracttest.ReadFile(t, filepath.Join(contracttest.Root(t), "schemas", "common.schema.json")), &doc); err != nil {
		t.Fatal(err)
	}
	for _, c := range []string{IDPoolViolation, OutputInvalid} {
		if !slices.Contains(doc.Defs.ValidationCode.Enum, c) {
			t.Errorf("%s is not in the ValidationCode catalogue", c)
		}
	}
}

// The outcome's JSON never includes the candidate (it is for the adapter).
func TestOutcomeJSON(t *testing.T) {
	o := run(t, input(t, provider.ModeEdit, proposed(ops(t, `[{"op":"replace","path":"/name","value":"x"}]`), "low")))
	data, err := json.Marshal(o)
	if err != nil {
		t.Fatal(err)
	}
	var m map[string]jsontext.Value
	if err := json.Unmarshal(data, &m); err != nil {
		t.Fatal(err)
	}
	if _, ok := m["Candidate"]; ok {
		t.Error("the candidate is encoded")
	}
}
