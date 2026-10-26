package fake_test

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json/v2"
	"testing"

	"github.com/kcl1104/geminus/internal/ai/locale"
	"github.com/kcl1104/geminus/internal/ai/provider"
	"github.com/kcl1104/geminus/internal/ai/provider/fake"
)

// T-056: the fake takes images, as the live vision backend does; an image
// request is keyed by the SHA-256 its IMAGE block names, and the fake
// refuses an image its prompt does not name, as a live provider would see
// it.

func image(data string) provider.Image {
	s := sha256.Sum256([]byte(data))
	return provider.Image{MediaType: "image/png", Data: []byte(data), Width: 64, Height: 32, SHA256: "sha256:" + hex.EncodeToString(s[:])}
}

func imagePrompt(t *testing.T, im provider.Image, loc locale.Locale) provider.Prompt {
	t.Helper()
	ref, _ := json.Marshal(im.Ref())
	return prompt(t, provider.RoleModeler, loc, provider.Block{Kind: provider.ServerData, Name: fake.ImageBlock, Content: ref},
		provider.Block{Kind: provider.UntrustedData, Name: "SEGMENTS", Content: []byte(`[]`)})
}

func TestImageRequests(t *testing.T) {
	ctx := context.Background()
	var _ provider.ImageBackend = fake.New()
	im := image("\x89PNG\r\n\x1a\nline")
	b := fake.New()
	answer := []byte(`{"status":"needs_input","summary":"Which station is first?","assumptions":[],"questions":["Which one?"],"requires_confirmation":true}`)
	b.Script(provider.RoleModeler, im.SHA256, answer)
	c := client(t, b)
	res, err := c.GenerateImageProposal(ctx, provider.ImageProposalContext{OrganizationID: org, Prompt: imagePrompt(t, im, locale.English), Image: im})
	if err != nil || res.Output.Summary != "Which station is first?" {
		t.Fatalf("scripted: %+v %v", res.Output, err)
	}
	seen := b.Requests()
	if len(seen) != 1 || seen[0].Image == nil || !bytes.Equal(seen[0].Image.Data, im.Data) {
		t.Fatalf("requests %+v", seen)
	}
	// Unscripted: the safe answer, in the request's locale.
	other := image("\x89PNG\r\n\x1a\nanother line")
	for _, loc := range locale.All {
		res, err = c.GenerateImageProposal(ctx, provider.ImageProposalContext{OrganizationID: org, Prompt: imagePrompt(t, other, loc), Image: other})
		if err != nil || res.Output.Status != "needs_input" || len(res.Output.Changes) != 0 {
			t.Errorf("%s unscripted: %+v %v", loc, res.Output, err)
		}
	}
	// A request whose image is not the one its IMAGE block names (the
	// client checks it first; the fake checks it again, as the bytes a
	// provider gets).
	req := provider.Request{Prompt: imagePrompt(t, im, locale.English), OutputSchema: "proposal-output.schema.json", Image: &other}
	if _, err := b.Complete(ctx, req); provider.CodeOf(err) != provider.CodeRejected {
		t.Errorf("a mismatched image: %v", err)
	}
	req.Image = nil
	if _, err := b.Complete(ctx, req); provider.CodeOf(err) != provider.CodeRejected {
		t.Errorf("an IMAGE block without its image: %v", err)
	}
}
