package provider

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json/v2"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/kcl1104/geminus/internal/ai/locale"
)

// T-056: a request may carry one image (a photo or floor plan the server
// re-encoded), sealed into its prompt by the IMAGE block, and only a
// backend that sends images is ever given one.

// imageBackend is a test backend that takes images.
type imageBackend struct {
	backendFunc
	calls atomic.Int32
	seen  []Request
}

func (b *imageBackend) AcceptsImages() {}
func (b *imageBackend) Complete(ctx context.Context, req Request) (Response, error) {
	b.calls.Add(1)
	b.seen = append(b.seen, req)
	return b.backendFunc(ctx, req)
}

// countingBackend takes no images.
type countingBackend struct {
	backendFunc
	calls atomic.Int32
}

func (b *countingBackend) Complete(ctx context.Context, req Request) (Response, error) {
	b.calls.Add(1)
	return b.backendFunc(ctx, req)
}

func testImage() Image {
	data := append([]byte("\x89PNG\r\n\x1a\n"), bytes.Repeat([]byte("pixels"), 20)...)
	s := sha256.Sum256(data)
	return Image{MediaType: "image/png", Data: data, Width: 640, Height: 480, SHA256: "sha256:" + hex.EncodeToString(s[:])}
}

// imagePrompt is a sealed modeler prompt whose IMAGE block names ref.
func imagePrompt(t *testing.T, ref any) Prompt {
	t.Helper()
	content, err := json.Marshal(ref)
	if err != nil {
		t.Fatal(err)
	}
	p, err := Seal(Prompt{OrganizationID: orgA, Role: RoleModeler, TemplateVersion: "photo-test", Locale: locale.English, System: "rules",
		Blocks: []Block{{Kind: ServerData, Name: ImageBlock, Content: content}, {Kind: UntrustedData, Name: "SEGMENTS", Content: str("[]")}}})
	if err != nil {
		t.Fatal(err)
	}
	return p
}

func askImage(t *testing.T, c *Client, im Image, p Prompt) (ProposalResult, error) {
	return c.GenerateImageProposal(context.Background(), ImageProposalContext{OrganizationID: orgA, Prompt: p, Image: im, Attribution: attribution})
}

// The image goes to the backend with the prompt that names it, as the
// server re-encoded it; the answer is checked like any other.
func TestAnImageGoesWithItsPrompt(t *testing.T) {
	im := testImage()
	b := &imageBackend{backendFunc: answer(validProposal)}
	res, err := askImage(t, client(t, b), im, imagePrompt(t, im.Ref()))
	if err != nil {
		t.Fatal(err)
	}
	if b.calls.Load() != 1 || b.seen[0].Image == nil || !bytes.Equal(b.seen[0].Image.Data, im.Data) || b.seen[0].Image.SHA256 != im.SHA256 {
		t.Fatalf("the backend was sent %+v", b.seen)
	}
	if res.Output.Status != "proposed" || res.Metadata.Role != RoleModeler || res.Metadata.TemplateVersion != "photo-test" ||
		res.Metadata.OutputSchema != "proposal-output.schema.json" {
		t.Errorf("%+v %+v", res.Output, res.Metadata)
	}
	// An answer that is not a proposal is invalid_output, as for any call.
	b = &imageBackend{backendFunc: answer(`{"status":"approved"}`)}
	if _, err := askImage(t, client(t, b), im, imagePrompt(t, im.Ref())); CodeOf(err) != CodeInvalidOutput {
		t.Errorf("an invalid answer: %v", err)
	}
	// A text call carries no image.
	b = &imageBackend{backendFunc: answer(validProposal)}
	if _, err := client(t, b).GenerateModelProposal(context.Background(), ProposalContext{OrganizationID: orgA, Mode: ModeEdit,
		Prompt: prompt(t, RoleModeler)}); err != nil || b.seen[0].Image != nil {
		t.Errorf("a text call: %v %+v", err, b.seen)
	}
}

// A backend that does not send images is never given one: the image would
// be dropped and the model would answer about a photo it never saw. The
// call fails before anything is sent, and a disabled deployment says it is
// disabled.
func TestABackendWithoutImagesIsNeverSentOne(t *testing.T) {
	im := testImage()
	b := &countingBackend{backendFunc: answer(validProposal)}
	_, err := askImage(t, client(t, b), im, imagePrompt(t, im.Ref()))
	if CodeOf(err) != CodeImageUnsupported || b.calls.Load() != 0 {
		t.Fatalf("%v after %d calls", err, b.calls.Load())
	}
	if pe := err.(*Error); pe.Retryable() {
		t.Error("an unsupported image is retryable")
	}
	for _, code := range []Code{CodeNotConfigured, CodePolicyNotApproved} {
		if _, err := askImage(t, client(t, Disabled{Code: code}), im, imagePrompt(t, im.Ref())); CodeOf(err) != code {
			t.Errorf("disabled %s: %v", code, err)
		}
	}
	// A billable backend without images is refused before any reservation.
	m := &meter{}
	bb := &billable{quote: Quote{Model: pricedModel, MaxInputTokens: 10}, respond: billed(validProposal, Usage{InputTokens: 1, OutputTokens: 1})}
	if _, err := askImage(t, metered(t, bb, m), im, imagePrompt(t, im.Ref())); CodeOf(err) != CodeImageUnsupported || len(m.charges) != 0 || bb.calls.Load() != 0 {
		t.Errorf("billable: %v, %d charges, %d calls", err, len(m.charges), bb.calls.Load())
	}
}

// The image must be the one the sealed prompt names, and well formed: a
// changed byte, another hash, size or type, no IMAGE block, an empty or
// too large image is the server's own mistake, refused before any call.
func TestTheImageMustBeTheSealedOne(t *testing.T) {
	im := testImage()
	changed := im
	changed.Data = append([]byte{}, im.Data...)
	changed.Data[20] ^= 1
	big := im
	big.Data = append(append([]byte{}, im.Data...), make([]byte, MaxImageBytes)...)
	s := sha256.Sum256(big.Data)
	big.SHA256 = "sha256:" + hex.EncodeToString(s[:])
	other := im.Ref()
	other.SHA256 = "sha256:" + strings.Repeat("0", 64)
	wider := im.Ref()
	wider.Width++
	gif := im
	gif.MediaType = "image/gif"
	for name, c := range map[string]struct {
		image  Image
		prompt Prompt
	}{
		"a changed byte":         {changed, imagePrompt(t, im.Ref())},
		"another hash named":     {im, imagePrompt(t, other)},
		"another width named":    {im, imagePrompt(t, wider)},
		"an unknown member":      {im, imagePrompt(t, map[string]any{"media_type": "image/png", "width": 640, "height": 480, "sha256": im.SHA256, "url": "x"})},
		"no IMAGE block":         {im, prompt(t, RoleModeler)},
		"a GIF":                  {gif, imagePrompt(t, gif.Ref())},
		"no bytes":               {Image{MediaType: "image/png", Width: 1, Height: 1, SHA256: im.SHA256}, imagePrompt(t, im.Ref())},
		"more than MaxImageSize": {big, imagePrompt(t, big.Ref())},
		"no size":                {Image{MediaType: im.MediaType, Data: im.Data, SHA256: im.SHA256}, imagePrompt(t, im.Ref())},
	} {
		b := &imageBackend{backendFunc: answer(validProposal)}
		if _, err := askImage(t, client(t, b), c.image, c.prompt); CodeOf(err) != CodeInvalidRequest || b.calls.Load() != 0 {
			t.Errorf("%s: %v after %d calls", name, err, b.calls.Load())
		}
	}
	// The prompt is checked as for any call: another organization's, or a
	// role that is not the modeler's.
	b := &imageBackend{backendFunc: answer(validProposal)}
	p := imagePrompt(t, im.Ref())
	if _, err := client(t, b).GenerateImageProposal(context.Background(), ImageProposalContext{OrganizationID: orgB, Prompt: p, Image: im}); CodeOf(err) != CodeInvalidRequest {
		t.Errorf("another organization: %v", err)
	}
	if b.calls.Load() != 0 {
		t.Error("the backend was called")
	}
}

// billableImages is a billable backend that takes images; its quote sees
// the image, which it must bound too.
type billableImages struct {
	billable
	quoted []bool
}

func (b *billableImages) AcceptsImages() {}
func (b *billableImages) Quote(req Request) (Quote, error) {
	b.quoted = append(b.quoted, req.Image != nil)
	return b.billable.Quote(req)
}

// A metered image call reserves before it is made, from a quote that saw
// the image, and settles after it, like any metered call.
func TestAMeteredImageCallReserves(t *testing.T) {
	im := testImage()
	b := &billableImages{billable: billable{quote: Quote{Model: pricedModel, MaxInputTokens: 9000},
		respond: billed(validProposal, Usage{InputTokens: 2500, OutputTokens: 400})}}
	m := &meter{}
	res, err := askImage(t, metered(t, b, m), im, imagePrompt(t, im.Ref()))
	if err != nil {
		t.Fatal(err)
	}
	if len(b.quoted) != 1 || !b.quoted[0] || len(m.charges) != 1 || m.charges[0].Role != RoleModeler || m.charges[0].Attribution != attribution {
		t.Errorf("quoted %v, charges %+v", b.quoted, m.charges)
	}
	if len(b.reqs) != 1 || b.reqs[0].Reservation != "r1" || b.reqs[0].Image == nil {
		t.Errorf("requests %+v", b.reqs)
	}
	if len(m.settled) != 1 || res.Metadata.SpendReservation != "r1" || res.Metadata.CostNanoUSD == 0 {
		t.Errorf("settled %+v, metadata %+v", m.settled, res.Metadata)
	}
	// A refused reservation is a refused call.
	m = &meter{reserve: func(Charge) (Reservation, error) {
		return Reservation{}, &Error{Code: CodeQuotaExceeded, Limit: LimitDay}
	}}
	b.calls.Store(0)
	if _, err := askImage(t, metered(t, b, m), im, imagePrompt(t, im.Ref())); CodeOf(err) != CodeQuotaExceeded || b.calls.Load() != 0 {
		t.Errorf("refused: %v after %d calls", err, b.calls.Load())
	}
}
