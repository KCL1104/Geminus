package fake_test

import (
	"context"
	"testing"

	"github.com/kcl1104/geminus/internal/ai/locale"
	"github.com/kcl1104/geminus/internal/ai/provider"
	"github.com/kcl1104/geminus/internal/ai/provider/fake"
)

var _ provider.ModelChooser = (*fake.Backend)(nil)

// H-049: the fake takes a proposal request that names a model and answers
// as it always does, as the fake: it runs no model, so it never claims to be
// the one chosen (its answer's model is "fake"). The request it was handed
// names the model, so a test can see the choice arrive. The analyst and the
// importer never take one.
func TestFakeTakesAChosenModel(t *testing.T) {
	b := fake.New()
	c := client(t, b)
	if !c.AcceptsModel(provider.RoleModeler, "muse-wonder-1.0") || !c.AcceptsModel(provider.RoleScenario, "muse-spark-1.3") ||
		c.AcceptsModel(provider.RoleAnalyst, "muse-wonder-1.0") || c.AcceptsModel(provider.RoleImporter, "muse-spark-1.3") {
		t.Error("AcceptsModel")
	}
	res, err := c.GenerateModelProposal(context.Background(), provider.ProposalContext{OrganizationID: org, Mode: provider.ModeScenario,
		Prompt: prompt(t, provider.RoleScenario, locale.English, provider.Block{Kind: provider.UntrustedData, Name: fake.IntentBlock,
			Content: quote("add a second oven")}), Model: "muse-wonder-1.0"})
	if err != nil {
		t.Fatal(err)
	}
	if res.Metadata.Model != "fake" || res.Metadata.Provider != "fake" {
		t.Errorf("answered as %q of %q", res.Metadata.Model, res.Metadata.Provider)
	}
	reqs := b.Requests()
	if len(reqs) != 1 || reqs[0].Model != "muse-wonder-1.0" {
		t.Errorf("requests %+v", reqs)
	}
}
