package approval_test

// Without a database: the answers the DB-backed tests script into the fake
// provider, asked the way the generator asks them. The fake answers a
// proposal request only for the prompt role its script names, and a
// request is asked as its mode's role (provider.Mode.Role: modeler for
// edit, scenario for scenario); a script under another role is never
// read, and the fake's safe needs_input answer comes back instead. Each
// answer a DB-backed test expects ready must also be ready on the base it
// is asked on: the server refuses a patch that leaves the model as it is.

import (
	"context"
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

// scriptAnswer makes the fake answer text to a proposal request in mode
// whose intent is intent: under the role the generator asks that mode as.
// Every DB-backed test here scripts its ready answers through it.
func scriptAnswer(f *fake.Backend, mode, intent string, text []byte) {
	role := provider.Mode(mode).Role()
	if role == "" {
		panic("scriptAnswer: no prompt role for mode " + mode)
	}
	f.Script(role, intent, text)
}

const (
	orgID      = "org_01M4YDQK00Y5H2929M9WWZWTVH"
	proposalID = "aip_01M4YDQK00ADN261CME3WV5HZ6"
	versionID  = "mv_01M4YDQK00ADN261CME3WV5HZ6"
	rename     = `[{"op":"replace","path":"/name","value":"Published"}]`
)

// templateIR is the canonical IR of the synthetic delivery template, the
// version 1 of every model the DB-backed tests create (the pack keeps the
// template equal to this golden model).
func templateIR(t *testing.T) []byte {
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
	return ir
}

// after is base with the patches applied as a person's draft edits and
// publishes, or an approved edit, would: the base a later proposal is
// asked on.
func after(t *testing.T, base []byte, patches ...string) []byte {
	t.Helper()
	for _, p := range patches {
		res, err := patch.Apply(base, []byte(p), patch.ManualDraft)
		if err != nil || !res.Valid() {
			t.Fatalf("applying %s: %v %+v", p, err, res.Validation)
		}
		if base, err = hashing.Canonical(res.Candidate); err != nil {
			t.Fatal(err)
		}
	}
	return base
}

// askAsTheGenerator asks f, through the provider client, for a proposal in
// mode on base, with the context the generator builds, and reviews the
// answer as the generator does.
func askAsTheGenerator(t *testing.T, f *fake.Backend, mode string, base []byte, intent string) (provider.ProposalResult, review.Outcome) {
	t.Helper()
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
		Scope:        aicontext.Scope{OrganizationID: orgID, ModelID: ir.ModelID},
		Mode:         m,
		Base:         aicontext.Version{OrganizationID: orgID, ModelID: ir.ModelID, VersionID: versionID, IR: &ir},
		Domain:       pack.PromptContext(),
		Intent:       intent,
		Pool:         aicontext.Pool{OrganizationID: orgID, ProposalID: proposalID, IDs: pool},
		OutputLocale: "en",
		Budget:       provider.DefaultBudget(),
	})
	if err != nil {
		t.Fatal(err)
	}
	client, err := provider.New(f, provider.DefaultBudget())
	if err != nil {
		t.Fatal(err)
	}
	res, err := client.GenerateModelProposal(context.Background(), pc)
	if err != nil {
		t.Fatalf("%s: %v", mode, err)
	}
	o, err := review.Review(review.Input{Mode: m, Base: base, Pool: pool, Pack: pack, Output: res.Output})
	if err != nil {
		t.Fatal(err)
	}
	return res, o
}

// The helper scripts the role the request is asked as, in both modes.
func TestScriptedAnswersReachEachMode(t *testing.T) {
	for _, mode := range []string{"edit", "scenario"} {
		f := fake.New()
		intent := "Change it in " + mode + " mode."
		scriptAnswer(f, mode, intent, answer(t, "Raise the CI runner capacity.", raiseCI))
		res, o := askAsTheGenerator(t, f, mode, templateIR(t), intent)
		if seen := f.Requests(); len(seen) != 1 || seen[0].Prompt.Role != provider.Mode(mode).Role() {
			t.Fatalf("%s: the request was not asked as %s: %+v", mode, provider.Mode(mode).Role(), seen)
		}
		if res.Output.Status != "proposed" || o.Status != review.Ready {
			t.Errorf("%s: the scripted answer was not read: %s %+v, questions %v", mode, res.Output.Status, o.Status, res.Output.Questions)
		}
	}
}

// Each answer a DB-backed test expects ready, on the base it is asked on.
func TestTheDBTestsAnswersAreReady(t *testing.T) {
	v1 := templateIR(t)
	approved := after(t, v1, raiseCI)       // an approved raiseCI
	published := after(t, approved, rename) // then a published rename
	for _, c := range []struct {
		name, mode string
		base       []byte
		changes    string
	}{
		{"most tests: edit on version 1", "edit", v1, raiseCI},
		{"TestApproveScenario, TestDecisionsWaitForTheModelRow", "scenario", v1, raiseCI},
		{"TestApprovalOnAMovedBase second", "edit", v1, moreQA},
		{"TestApprovalOnAMovedBase scenario", "scenario", v1, moreQA},
		{"TestApprovalOnAMovedBase third", "edit", approved, lowerCI},
		{"TestApprovalOnAMovedBase fourth", "edit", published, lowerCI},
		{"TestDecisionRaces q", "edit", approved, moreQA},
		{"TestApprovalReviewsAgain lowered", "edit", v1, lowerCI},
		{"TestDecisionsTakeTheModelRowFirst, TestTheKeyNamesTheProposal second", "edit", v1, moreQA},
		{"TestTheKeyNamesTheProposal third", "edit", approved, lowerCI},
		{"TestTheKeyNamesTheProposal fourth", "edit", approved, moreQA},
	} {
		f := fake.New()
		scriptAnswer(f, c.mode, c.name, answer(t, "Raise the CI runner capacity.", c.changes))
		if res, o := askAsTheGenerator(t, f, c.mode, c.base, c.name); o.Status != review.Ready {
			var issues []model.ValidationIssue
			if o.Validation != nil {
				issues = o.Validation.Issues
			}
			t.Errorf("%s: %s (answer %s) %+v", c.name, o.Status, res.Output.Status, issues)
		}
	}
}
