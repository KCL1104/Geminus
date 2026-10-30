package main

import (
	"slices"
	"testing"

	"github.com/kcl1104/geminus/internal/config"
)

// H-049: the models a person may choose, decided from the settings without
// any key: with the fake, the contract's two; with the live Meta provider on
// under H-029, the approval's (Muse Spark 1.3 and Muse Wonder 1.0), for words
// and for photos alike; with Claude live, none (its adapter asks its one
// model, so a request that names one is refused); with AI off, none.
func TestAPIProposalModels(t *testing.T) {
	both := []string{"muse-spark-1.3", "muse-wonder-1.0"}
	for _, c := range []struct {
		name         string
		env          []string
		words, photo []string
	}{
		{"default", nil, nil, nil},
		{"disabled", []string{"AI_PROVIDER", "disabled"}, nil, nil},
		{"meta live", apiMeta, both, both},
		{"meta, the switch off", apiMeta[:6], nil, nil},
		{"meta without approval", []string{"AI_PROVIDER", "meta"}, nil, nil},
		{"anthropic live", apiClaude, nil, nil},
		{"anthropic live, meta for photos", apiClaudeMetaPhotos, nil, both},
		{"the fake", []string{"AI_PROVIDER", "fake", "APP_ENV", "test"}, both, both},
	} {
		ai, err := config.LoadAPIAI(with(c.env...))
		if err != nil {
			t.Fatalf("%s: %v", c.name, err)
		}
		words := proposalModels(ai.Provider, ai.LiveEnabled(), ai.Approval.ProposalModels())
		photo := proposalModels(ai.Vision.Provider, ai.Vision.LiveEnabled(), ai.Vision.Approval.ProposalModels())
		if !slices.Equal(words, c.words) || !slices.Equal(photo, c.photo) {
			t.Errorf("%s: words %q, photo %q; want %q, %q", c.name, words, photo, c.words, c.photo)
		}
	}
	// A live provider that is not on lists nothing, whatever the approval.
	meta, err := config.LoadAPIAI(with(apiMeta...))
	if err != nil {
		t.Fatal(err)
	}
	if got := proposalModels(config.AIProviderMeta, false, meta.Approval.ProposalModels()); got != nil {
		t.Errorf("off: %q", got)
	}
}
