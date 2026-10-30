package fake_test

import (
	"context"
	"slices"
	"testing"

	"github.com/kcl1104/geminus/internal/ai/locale"
	"github.com/kcl1104/geminus/internal/ai/provider"
	"github.com/kcl1104/geminus/internal/ai/provider/fake"
)

var _ provider.Researcher = (*fake.Backend)(nil)

func researchPrompt(t *testing.T, role provider.Role, intent string) provider.Prompt {
	t.Helper()
	p, err := provider.Seal(provider.Prompt{OrganizationID: org, Role: role, TemplateVersion: "t+research-t", Locale: locale.English, System: "rules",
		Blocks: []provider.Block{{Kind: provider.UntrustedData, Name: fake.IntentBlock, Content: quote(intent)}}, Research: true})
	if err != nil {
		t.Fatal(err)
	}
	return p
}

// H-046 (2): the fake researches for the proposal roles with its fixed
// steps and test pages, deterministically; its safe answer names the two
// pages it opened and one it did not, so the client keeps two and drops
// one. No page is fetched.
func TestFakeResearch(t *testing.T) {
	b := fake.New()
	c := client(t, b)
	if !c.AcceptsResearch(provider.RoleModeler) || !c.AcceptsResearch(provider.RoleScenario) || c.AcceptsResearch(provider.RoleAnalyst) {
		t.Error("AcceptsResearch")
	}
	for _, mode := range []provider.Mode{provider.ModeEdit, provider.ModeScenario} {
		res, err := c.GenerateModelProposal(context.Background(), provider.ProposalContext{OrganizationID: org, Mode: mode,
			Prompt: researchPrompt(t, mode.Role(), "model a shoe factory")})
		if err != nil {
			t.Fatal(err)
		}
		want := []provider.WebSource{{URL: fake.ResearchSources[0].URL, Title: fake.ResearchSources[0].Title},
			{URL: fake.ResearchSources[1].URL, Title: fake.ResearchSources[1].Title}}
		if res.Output.Status != "needs_input" || !slices.Equal(res.Sources, want) {
			t.Errorf("%s: %+v, sources %+v", mode, res.Output, res.Sources)
		}
		if w := res.Metadata.WebSearch; w == nil || *w != (provider.WebSearch{Queries: 2, ToolCalls: 4, PagesOpened: 2, SourcesKept: 2, SourcesDropped: 1}) {
			t.Errorf("%s: web search %+v", mode, w)
		}
	}
	// A scripted answer and scripted steps: only what the steps opened is
	// kept.
	b.Script(provider.RoleModeler, "carton plant", []byte(`{"status":"needs_input","summary":"Which flute?","assumptions":[],"questions":["Which?"],`+
		`"sources":[{"url":"https://example.com/a"},{"url":"https://example.com/b"}],"requires_confirmation":true}`))
	b.ScriptResearch("carton plant", provider.Research{Queries: 1, ToolCalls: 2, Opened: []string{"https://example.com/b"}})
	res, err := c.GenerateModelProposal(context.Background(), provider.ProposalContext{OrganizationID: org, Mode: provider.ModeEdit,
		Prompt: researchPrompt(t, provider.RoleModeler, "carton plant")})
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(res.Sources, []provider.WebSource{{URL: "https://example.com/b"}}) || res.Metadata.WebSearch.SourcesDropped != 1 {
		t.Errorf("scripted: %+v %+v", res.Sources, res.Metadata.WebSearch)
	}
	// Without research the fake searches nothing and names no source.
	res, err = c.GenerateModelProposal(context.Background(), provider.ProposalContext{OrganizationID: org, Mode: provider.ModeEdit,
		Prompt: prompt(t, provider.RoleModeler, locale.English, provider.Block{Kind: provider.UntrustedData, Name: fake.IntentBlock, Content: quote("x")})})
	if err != nil || res.Sources != nil || res.Metadata.WebSearch != nil {
		t.Errorf("without research: %v %+v %+v", err, res.Sources, res.Metadata.WebSearch)
	}
	// Every page is a test page (RFC 2606), never a real source.
	for _, s := range fake.ResearchSources {
		if provider.SourceURL(s.URL) != s.URL || len(s.URL) < len("https://example.com/") || s.URL[:len("https://example.com/")] != "https://example.com/" {
			t.Errorf("%s is not an example.com test page", s.URL)
		}
	}
}
