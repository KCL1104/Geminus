package fake_test

import (
	"context"
	"testing"

	"github.com/kcl1104/geminus/internal/ai/locale"
	"github.com/kcl1104/geminus/internal/ai/provider"
	"github.com/kcl1104/geminus/internal/ai/provider/fake"
)

// T-056: a photo proposal's prompt has no intent, so the fake matches it on
// its first image's digest (T-055's provider.Image); a prompt's intent,
// question or columns still come first; an unscripted photo gets the safe
// answer in the request's locale.

func photoPrompt(t *testing.T, loc locale.Locale, blocks []provider.Block, images ...provider.Image) provider.Prompt {
	t.Helper()
	p, err := provider.Seal(provider.Prompt{OrganizationID: org, Role: provider.RoleModeler, TemplateVersion: "photo-t", Locale: loc, System: "rules",
		Blocks: blocks, Images: images})
	if err != nil {
		t.Fatal(err)
	}
	return p
}

func TestAPhotoIsMatchedOnItsImage(t *testing.T) {
	ctx := context.Background()
	line, other := testImage(t, 64, 32), testImage(t, 32, 64)
	segments := []provider.Block{{Kind: provider.UntrustedData, Name: "SEGMENTS", Content: []byte(`[]`)}}
	answer := []byte(`{"status":"needs_input","summary":"Which station is first?","assumptions":[],"questions":["Which one?"],"requires_confirmation":true}`)
	b := fake.New()
	b.Script(provider.RoleModeler, line.Digest, answer)
	c := client(t, b)
	ask := func(p provider.Prompt) (provider.ProposalResult, error) {
		return c.GenerateModelProposal(ctx, provider.ProposalContext{OrganizationID: org, Mode: provider.ModeEdit, Prompt: p})
	}
	if res, err := ask(photoPrompt(t, locale.English, segments, line)); err != nil || res.Output.Summary != "Which station is first?" {
		t.Fatalf("scripted: %+v %v", res.Output, err)
	}
	// The first image is the key.
	if res, err := ask(photoPrompt(t, locale.English, segments, line, other)); err != nil || res.Output.Summary != "Which station is first?" {
		t.Errorf("two images: %+v %v", res.Output, err)
	}
	// Unscripted: the safe answer, in the request's locale.
	for _, loc := range locale.All {
		res, err := ask(photoPrompt(t, loc, segments, other))
		if err != nil || res.Output.Status != "needs_input" || len(res.Output.Changes) != 0 {
			t.Errorf("%s unscripted: %+v %v", loc, res.Output, err)
		}
	}
	// An intent is still the key of a prompt that has one.
	intent := []provider.Block{{Kind: provider.UntrustedData, Name: fake.IntentBlock, Content: quote("add a tester")}}
	if res, err := ask(photoPrompt(t, locale.English, intent, line)); err != nil || res.Output.Summary == "Which station is first?" {
		t.Errorf("an intent with an image: %+v %v", res.Output, err)
	}
	if seen := b.Requests(); len(seen) != 3+len(locale.All) || len(seen[0].Prompt.Images) != 1 || seen[0].Prompt.Images[0].Digest != line.Digest {
		t.Errorf("requests %d", len(seen))
	}
}
