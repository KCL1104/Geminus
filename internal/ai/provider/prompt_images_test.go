package provider

import (
	"bytes"
	"context"
	"encoding/json/v2"
	"errors"
	"slices"
	"testing"
	"time"

	"github.com/kcl1104/geminus/internal/ai/locale"
	"github.com/kcl1104/geminus/internal/model/hashing"
)

// T-055: a prompt may carry images (T-056's photo path): they are sealed
// into InputHash by type, digest and size, checked again whenever the
// prompt is, and sent only to a backend that takes images.

func imagePrompt(t *testing.T, images ...Image) Prompt {
	t.Helper()
	p, err := Seal(Prompt{OrganizationID: orgA, Role: RoleModeler, TemplateVersion: "modeler-test", Locale: locale.English, System: "rules",
		Blocks: []Block{{Kind: UntrustedData, Name: "USER_INTENT", Content: str("raise capacity")}}, Images: images})
	if err != nil {
		t.Fatal(err)
	}
	return p
}

func testImage(t *testing.T, w, h int) Image {
	t.Helper()
	im, err := NewImage(ImagePNG, pngOf(t, w, h))
	if err != nil {
		t.Fatal(err)
	}
	return im
}

// A prompt without images hashes as it did before images existed: the
// hash of every text prompt, and so every audit record's input hash, is
// unchanged (T-024's JCS form of organization, role, template, locale,
// system and user).
func TestATextPromptHashesAsBefore(t *testing.T) {
	p := imagePrompt(t)
	doc, err := json.Marshal(struct {
		OrganizationID  string        `json:"organization_id"`
		Role            Role          `json:"role"`
		TemplateVersion string        `json:"template_version"`
		Locale          locale.Locale `json:"locale"`
		System          string        `json:"system"`
		User            string        `json:"user"`
	}{p.OrganizationID, p.Role, p.TemplateVersion, p.Locale, p.System, p.User()})
	if err != nil {
		t.Fatal(err)
	}
	want, err := hashing.Of(doc)
	if err != nil || p.InputHash != want {
		t.Errorf("hash %s, want %s (%v)", p.InputHash, want, err)
	}
}

func TestImagesAreSealed(t *testing.T) {
	a, b := testImage(t, 3, 2), testImage(t, 4, 2)
	text, one, two, other := imagePrompt(t), imagePrompt(t, a), imagePrompt(t, a, b), imagePrompt(t, b)
	hashes := []string{text.InputHash, one.InputHash, two.InputHash, other.InputHash}
	slices.Sort(hashes)
	if len(slices.Compact(hashes)) != 4 {
		t.Errorf("the images do not change the hash: %v", hashes)
	}
	if err := two.Verify(); err != nil || len(two.Images) != 2 || two.Images[0].Digest != a.Digest {
		t.Fatalf("sealed: %v %+v", err, two.Images)
	}
	// Seal keeps its own copy of the bytes.
	in := testImage(t, 3, 2)
	p := imagePrompt(t, in)
	in.Data[len(in.Data)-1] ^= 0xff
	if err := p.Verify(); err != nil {
		t.Errorf("the caller's bytes changed the sealed prompt: %v", err)
	}
	// Changed after it was sealed: bytes, an image dropped or added.
	changed := imagePrompt(t, a)
	changed.Images[0].Data = bytes.Clone(changed.Images[0].Data)
	changed.Images[0].Data[len(changed.Images[0].Data)-6] ^= 1
	dropped := imagePrompt(t, a, b)
	dropped.Images = dropped.Images[:1]
	added := imagePrompt(t, a)
	added.Images = append(added.Images, b)
	for name, q := range map[string]Prompt{"changed bytes": changed, "dropped": dropped, "added": added} {
		if err := q.Verify(); !errors.Is(err, ErrInvalidPrompt) {
			t.Errorf("%s: %v", name, err)
		}
	}
	// Refused when sealing: more than MaxImagesPerPrompt, or an image that
	// is not one.
	many := make([]Image, MaxImagesPerPrompt+1)
	for i := range many {
		many[i] = testImage(t, i+1, 1)
	}
	for name, images := range map[string][]Image{"too many": many, "the zero image": {{}}, "a changed size": {{MediaType: a.MediaType, Data: a.Data, Width: 9, Height: 2, Digest: a.Digest}}} {
		_, err := Seal(Prompt{OrganizationID: orgA, Role: RoleModeler, TemplateVersion: "modeler-test", Locale: locale.English, System: "rules",
			Blocks: []Block{{Kind: UntrustedData, Name: "USER_INTENT", Content: str("x")}}, Images: images})
		if !errors.Is(err, ErrInvalidPrompt) {
			t.Errorf("%s: %v", name, err)
		}
	}
}

// A request with images may answer with ImageMaxOutputTokens (reasoning
// over an image used all of 8192 live, twice) with timeouts scaled as that
// bound is, at most BudgetCaps.Timeout; one without keeps the budget's.
func TestImagesGetTheirOwnOutputBound(t *testing.T) {
	b := &imageBackend{accepts: true}
	c := client(t, b, func(bu *Budget) {
		bu.MaxOutputTokens, bu.Timeout, bu.AttemptTimeout = 1234, 100*time.Second, 50*time.Second
	})
	for _, p := range []Prompt{imagePrompt(t, testImage(t, 3, 2)), imagePrompt(t)} {
		if _, err := c.GenerateModelProposal(context.Background(), ProposalContext{OrganizationID: orgA, Mode: ModeEdit, Prompt: p}); err != nil {
			t.Fatal(err)
		}
	}
	if len(b.seen) != 2 || b.seen[0].MaxOutputTokens != ImageMaxOutputTokens || b.seen[1].MaxOutputTokens != 1234 {
		t.Errorf("max output tokens: %+v", b.seen)
	}
	if len(b.seen) != 2 || b.seen[0].AttemptTimeout != BudgetCaps.Timeout || b.seen[1].AttemptTimeout != 50*time.Second ||
		len(b.left) != 2 || b.left[0] <= 100*time.Second || b.left[0] > BudgetCaps.Timeout || b.left[1] > 100*time.Second {
		t.Errorf("timeouts: attempts %v, %v; the call's time left %v", b.seen[0].AttemptTimeout, b.seen[1].AttemptTimeout, b.left)
	}
	if ImageMaxOutputTokens > BudgetCaps.MaxOutputTokens {
		t.Errorf("ImageMaxOutputTokens %d is over the cap %d", ImageMaxOutputTokens, BudgetCaps.MaxOutputTokens)
	}
	// Scaled: the default budget's 90 s attempt is 351 s for an image, so the cap; a small one keeps its ratio.
	small := Budget{MaxOutputTokens: 8000}
	if d := DefaultBudget().ImageTimeout(DefaultBudget().AttemptTimeout); d != BudgetCaps.Timeout {
		t.Errorf("the default image attempt timeout %v, want the cap", d)
	}
	if d := small.ImageTimeout(time.Second); d != 4*time.Second {
		t.Errorf("a 1 s timeout at 8000 tokens is %v for an image, want 4 s", d)
	}
}

// imageBackend is a test backend that takes images when accepts says so.
type imageBackend struct {
	accepts bool
	seen    []Request
	left    []time.Duration // the call's time left when it reached the backend
}

func (b *imageBackend) Name() string        { return "test-images" }
func (b *imageBackend) AcceptsImages() bool { return b.accepts }
func (b *imageBackend) Complete(ctx context.Context, req Request) (Response, error) {
	b.seen = append(b.seen, req)
	if d, ok := ctx.Deadline(); ok {
		b.left = append(b.left, time.Until(d))
	}
	return Response{Text: []byte(validNeedsInput), Model: "test-model"}, nil
}

// The client hands a prompt's images only to a backend that takes them;
// any other backend never sees the call (invalid_request, before it).
func TestImagesGoOnlyToABackendThatTakesThem(t *testing.T) {
	p := imagePrompt(t, testImage(t, 3, 2))
	ask := func(c *Client) error {
		_, err := c.GenerateModelProposal(context.Background(), ProposalContext{OrganizationID: orgA, Mode: ModeEdit, Prompt: p})
		return err
	}
	calls := 0
	plain := backendFunc(func(context.Context, Request) (Response, error) {
		calls++
		return Response{Text: []byte(validNeedsInput)}, nil
	})
	if err := ask(client(t, plain)); CodeOf(err) != CodeInvalidRequest || calls != 0 {
		t.Errorf("a backend without images: %v (%d calls)", err, calls)
	}
	no := &imageBackend{accepts: false}
	if err := ask(client(t, no)); CodeOf(err) != CodeInvalidRequest || len(no.seen) != 0 {
		t.Errorf("a backend that refuses images: %v (%d calls)", err, len(no.seen))
	}
	yes := &imageBackend{accepts: true}
	if err := ask(client(t, yes)); err != nil || len(yes.seen) != 1 || len(yes.seen[0].Prompt.Images) != 1 {
		t.Errorf("a backend that takes images: %v %+v", err, yes.seen)
	}
	// A text prompt goes to any backend, as before.
	calls = 0
	if _, err := proposal(t, client(t, plain)); err != nil || calls != 1 {
		t.Errorf("text: %v (%d calls)", err, calls)
	}
	// Disabled answers with its code first, whatever the prompt holds.
	if err := ask(client(t, Disabled{Code: CodePolicyNotApproved})); CodeOf(err) != CodePolicyNotApproved {
		t.Errorf("disabled: %v", err)
	}
}
