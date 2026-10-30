package main

import (
	"slices"
	"testing"

	"github.com/kcl1104/geminus/internal/config"
	"github.com/kcl1104/geminus/internal/proposals"
)

// H-046 (2): the API takes a request that asks for web research when the
// worker's provider may search for both proposal roles, decided from the
// settings without any key: the fake (development and tests), or the live
// Meta provider on with the H-029 approval, which names research for the
// modeler and scenario roles since H-046. A Meta provider that is off is
// PolicyNotApproved; Claude has no web search, and AI off is off:
// NotConfigured. Proposals without research are decided as before.
func TestAPIResearchAvailability(t *testing.T) {
	for _, c := range []struct {
		name           string
		env            []string
		text, research proposals.Availability
	}{
		{"default", nil, proposals.NotConfigured, proposals.NotConfigured},
		{"disabled", []string{"AI_PROVIDER", "disabled"}, proposals.NotConfigured, proposals.NotConfigured},
		{"meta live", apiMeta, proposals.Available, proposals.Available},
		{"meta, the switch off", apiMeta[:6], proposals.PolicyNotApproved, proposals.PolicyNotApproved},
		{"meta without approval", []string{"AI_PROVIDER", "meta"}, proposals.PolicyNotApproved, proposals.PolicyNotApproved},
		{"anthropic live: no web search", apiClaude, proposals.Available, proposals.NotConfigured},
		{"anthropic, the switch off", apiClaude[:6], proposals.PolicyNotApproved, proposals.NotConfigured},
		{"anthropic live, meta for photos", apiClaudeMetaPhotos, proposals.Available, proposals.NotConfigured},
		{"the fake", []string{"AI_PROVIDER", "fake", "APP_ENV", "test"}, proposals.Available, proposals.Available},
	} {
		ai, err := config.LoadAPIAI(with(c.env...))
		if err != nil {
			t.Fatalf("%s: %v", c.name, err)
		}
		if text, research := aiAvailability(ai), researchAvailability(ai); text != c.text || research != c.research {
			t.Errorf("%s: proposals %v, research %v; want %v, %v", c.name, text, research, c.text, c.research)
		}
	}
	// Only a valid approval of a live provider that is on and names research
	// makes research available, however the value was made.
	meta, err := config.LoadAPIAI(with(apiMeta...))
	if err != nil {
		t.Fatal(err)
	}
	claude, err := config.LoadAPIAI(with(apiClaude...))
	if err != nil {
		t.Fatal(err)
	}
	for name, v := range map[string]config.APIAI{
		"the zero value":           {},
		"meta, no approval":        {Provider: config.AIProviderMeta, Model: meta.Model},
		"anthropic with H-029":     {Provider: config.AIProviderAnthropic, Approval: meta.Approval, Model: meta.Model},
		"disabled with H-029":      {Provider: config.AIProviderDisabled, Approval: meta.Approval},
		"anthropic, its approval":  claude,
		"meta with Claude's H-018": {Provider: config.AIProviderMeta, Approval: claude.Approval, Model: claude.Model},
	} {
		if got := researchAvailability(v); got == proposals.Available {
			t.Errorf("%s: research available", name)
		}
	}
	if !slices.Contains([]proposals.Availability{proposals.Available}, researchAvailability(meta)) {
		t.Error("H-029 with the switch does not research")
	}
}
