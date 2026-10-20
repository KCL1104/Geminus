package api_test

// Without a database: the answers TestApprovalClosedLoop scripts into the
// fake provider, asked the way the generator asks them. The fake answers a
// proposal request only for the prompt role its script names, and a
// request is asked as its mode's role (provider.Mode.Role: modeler for
// edit, scenario for scenario); a script under another role is never read
// and the fake's safe needs_input answer comes back instead.

import (
	"context"
	"encoding/json/jsontext"
	"encoding/json/v2"
	"path/filepath"
	"testing"

	"github.com/kcl1104/geminus/internal/ai/aicontext"
	"github.com/kcl1104/geminus/internal/ai/idpool"
	"github.com/kcl1104/geminus/internal/ai/provider"
	"github.com/kcl1104/geminus/internal/ai/provider/fake"
	"github.com/kcl1104/geminus/internal/ai/review"
	"github.com/kcl1104/geminus/internal/contracttest"
	"github.com/kcl1104/geminus/internal/domainpack"
	"github.com/kcl1104/geminus/internal/domainpack/softwaredelivery"
	"github.com/kcl1104/geminus/internal/model"
	"github.com/kcl1104/geminus/internal/model/hashing"
	"github.com/kcl1104/geminus/internal/model/patch"
)

// scriptProposal makes the fake answer text to a proposal request in mode
// whose intent is intent, under the role the generator asks that mode as;
// readyOver scripts through it.
func scriptProposal(f *fake.Backend, mode, intent string, text []byte) {
	role := provider.Mode(mode).Role()
	if role == "" {
		panic("scriptProposal: no prompt role for mode " + mode)
	}
	f.Script(role, intent, text)
}

// eightRunners is the closed loop's scenario answer.
const eightRunners = `{"status":"proposed","summary":"Try 8 CI runners.","assumptions":[],"risk_level":"low","confidence":0.8,` +
	`"changes":[{"op":"test","path":"/resources/1/id","value":"ci_runner"},{"op":"replace","path":"/resources/1/capacity","value":8}],` +
	`"expected_effects":["Waiting may fall; a simulation will tell."],"requires_confirmation":true}`

// askedAsTheGenerator scripts answer for intent in mode, asks the fake for
// it on base through the provider client with the context the generator
// builds, and reviews the answer as the generator does. It returns the
// request's role, the answer's status and the review's.
func askedAsTheGenerator(t *testing.T, mode string, base []byte, intent, answer string) (provider.Role, string, review.Outcome) {
	t.Helper()
	const org = "org_01M4YDQK00Y5H2929M9WWZWTVH"
	var ir model.ModelIR
	if err := model.Decode(base, &ir); err != nil {
		t.Fatal(err)
	}
	reg, err := domainpack.NewRegistry(softwaredelivery.Pack{})
	if err != nil {
		t.Fatal(err)
	}
	pack, err := reg.Lookup(ir.Domain)
	if err != nil {
		t.Fatal(err)
	}
	pool, err := idpool.New(&ir)
	if err != nil {
		t.Fatal(err)
	}
	m := provider.Mode(mode)
	pc, err := aicontext.Proposal(aicontext.ProposalInput{
		Scope:        aicontext.Scope{OrganizationID: org, ModelID: ir.ModelID},
		Mode:         m,
		Base:         aicontext.Version{OrganizationID: org, ModelID: ir.ModelID, VersionID: "mv_01M4YDQK00ADN261CME3WV5HZ6", IR: &ir},
		Domain:       pack.PromptContext(),
		Intent:       intent,
		Pool:         aicontext.Pool{OrganizationID: org, ProposalID: "aip_01M4YDQK00ADN261CME3WV5HZ6", IDs: pool},
		OutputLocale: "en",
		Budget:       provider.DefaultBudget(),
	})
	if err != nil {
		t.Fatal(err)
	}
	f := fake.New()
	scriptProposal(f, mode, intent, []byte(answer))
	client, err := provider.New(f, provider.DefaultBudget())
	if err != nil {
		t.Fatal(err)
	}
	res, err := client.GenerateModelProposal(context.Background(), pc)
	if err != nil {
		t.Fatalf("%s: %v", intent, err)
	}
	o, err := review.Review(review.Input{Mode: m, Base: base, Pool: pool, Pack: pack, Output: res.Output})
	if err != nil {
		t.Fatal(err)
	}
	var role provider.Role
	if seen := f.Requests(); len(seen) == 1 {
		role = seen[0].Prompt.Role
	}
	return role, res.Output.Status, o
}

// changesOf is the changes of a scripted answer.
func changesOf(t *testing.T, answer string) []byte {
	t.Helper()
	var a struct {
		Changes jsontext.Value `json:"changes"`
	}
	if err := json.Unmarshal([]byte(answer), &a); err != nil {
		t.Fatal(err)
	}
	return a.Changes
}

// Each answer TestApprovalClosedLoop expects ready, on the base it is
// asked on: version 1 of the delivery template, then version 2, its
// approved raiseCI.
func TestTheClosedLoopsAnswersAreReady(t *testing.T) {
	var doc map[string]any
	if err := json.Unmarshal(contracttest.ReadFile(t, filepath.Join(contracttest.Root(t), "testdata", "golden-models", "default-delivery.json")), &doc); err != nil {
		t.Fatal(err)
	}
	doc["model_id"] = "mdl_01M4YDQK00ADN261CME3WV5HZ6"
	raw, _ := json.Marshal(doc)
	v1, err := hashing.Canonical(raw)
	if err != nil {
		t.Fatal(err)
	}
	res, err := patch.Apply(v1, changesOf(t, raiseCI), patch.ManualDraft)
	if err != nil || !res.Valid() {
		t.Fatalf("%v %+v", err, res.Validation)
	}
	v2, err := hashing.Canonical(res.Candidate)
	if err != nil {
		t.Fatal(err)
	}
	for _, c := range []struct {
		mode, intent, answer string
		base                 []byte
	}{
		{"edit", "Raise the CI runners to 6.", raiseCI, v1},
		{"scenario", "What if there were 8 runners?", eightRunners, v2},
		{"edit", "Lower the runners.", lowerRunners, v2},
	} {
		role, answered, o := askedAsTheGenerator(t, c.mode, c.base, c.intent, c.answer)
		if role != provider.Mode(c.mode).Role() || answered != "proposed" || o.Status != review.Ready {
			t.Errorf("%s (%s): asked as %q, answered %s, reviewed %s", c.intent, c.mode, role, answered, o.Status)
		}
	}
}
