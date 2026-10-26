package provider

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json/v2"
	"errors"
)

// Image input (T-056, H-033): an edit proposal may be asked for with a
// photo or floor plan of the line. The image is the one the server stored
// (internal/proposals/photo: a PNG or JPEG decoded and encoded again, so no
// EXIF or other metadata), and it is sealed into the prompt: the IMAGE
// server_data block names its media type, size in pixels and SHA-256, so
// the prompt's InputHash covers the image too and the client refuses an
// image the prompt does not name. Only an ImageBackend is ever given an
// image, so no backend can silently drop it and answer about a photo it
// never saw.
//
// For the backend that implements it (the T-055 Meta adapter, or the
// Claude fallback):
//   - AcceptsImages marks it; Complete sends req.Image with the user message
//     (for the Messages API, an image content block of the media type, base64
//     data, before the text block), and nothing else of it.
//   - A Billable one bounds the image in its Quote: MaxInputTokens includes
//     the most input tokens the provider can bill for the image as sent
//     (req.Image.Width, Height and Data), so the reservation stays an upper
//     bound (D-111).
//   - The image is data like any untrusted block: text in the picture is
//     never an instruction (the photo template and the server rules say so).
// The Anthropic adapter (T-053) does not implement it yet: an image request
// to it fails with image_unsupported before any reservation or I/O.

// ImageBlock is the server_data block that names a request's image.
const ImageBlock = "IMAGE"

// MaxImageBytes bounds an image a request may carry (the stored image's
// limit, internal/proposals/photo.MaxBytes).
const MaxImageBytes = 10_000_000

// CodeImageUnsupported: the request carries an image and the configured
// backend does not send images; nothing was sent or reserved. Not
// retryable: the deployment's provider has to change.
const CodeImageUnsupported Code = "image_unsupported"

// Image is an image a request sends with its prompt.
type Image struct {
	MediaType     string // image/png or image/jpeg
	Data          []byte
	Width, Height int    // pixels
	SHA256        string // "sha256:<hex>" of Data
}

// ImageRef is the IMAGE block's content: what the prompt says of its image.
type ImageRef struct {
	MediaType string `json:"media_type"`
	Width     int    `json:"width"`
	Height    int    `json:"height"`
	SHA256    string `json:"sha256"`
}

// Ref is what the IMAGE block of im's prompt holds.
func (im Image) Ref() ImageRef {
	return ImageRef{MediaType: im.MediaType, Width: im.Width, Height: im.Height, SHA256: im.SHA256}
}

var errImage = errors.New("ai provider: invalid image")

// Check reports whether im is an image a request may carry: PNG or JPEG, a
// size in pixels, at most MaxImageBytes, and its own hash.
func (im Image) Check() error {
	if (im.MediaType != "image/png" && im.MediaType != "image/jpeg") || im.Width <= 0 || im.Height <= 0 ||
		len(im.Data) == 0 || len(im.Data) > MaxImageBytes {
		return errImage
	}
	s := sha256.Sum256(im.Data)
	if im.SHA256 != "sha256:"+hex.EncodeToString(s[:]) {
		return errImage
	}
	return nil
}

// names reports whether p's IMAGE block is a server_data block that names
// im exactly (and nothing else).
func (p Prompt) names(im Image) bool {
	want, err := json.Marshal(im.Ref(), json.Deterministic(true))
	if err != nil {
		return false
	}
	for _, b := range p.Blocks {
		if b.Name != ImageBlock {
			continue
		}
		var got ImageRef
		if b.Kind != ServerData || unmarshalStrict(b.Content, &got) != nil {
			return false
		}
		again, err := json.Marshal(got, json.Deterministic(true))
		return err == nil && bytes.Equal(again, want)
	}
	return false
}

// ImageBackend is a backend that sends a request's image with its prompt.
type ImageBackend interface {
	Backend
	// AcceptsImages marks the backend; it has no effect.
	AcceptsImages()
}

// ImageProposalContext is a sanitized edit proposal request about one
// image: the prompt (role modeler; its IMAGE block names the image) and
// the image.
type ImageProposalContext struct {
	OrganizationID string
	Prompt         Prompt
	Image          Image
	Attribution    Attribution
}

// ImageProposer asks for an edit proposal from an image; *Client is one.
type ImageProposer interface {
	GenerateImageProposal(context.Context, ImageProposalContext) (ProposalResult, error)
}

var _ ImageProposer = (*Client)(nil)

// GenerateImageProposal asks for an edit proposal from in.Image, checked as
// every call is (the answer is one proposal-output document), metered when
// the backend is billable.
func (c *Client) GenerateImageProposal(ctx context.Context, in ImageProposalContext) (ProposalResult, error) {
	var out ProposalOutput
	image := in.Image
	raw, meta, err := c.call(ctx, in.OrganizationID, RoleModeler, in.Prompt, in.Attribution, &image, &out)
	if err != nil {
		return ProposalResult{Metadata: meta}, err
	}
	return ProposalResult{Output: out, Raw: raw, Metadata: meta}, nil
}
