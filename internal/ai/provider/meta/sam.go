package meta

import (
	"context"
	"encoding/base64"
	"encoding/json/v2"
	"errors"
	"net/http"
	"strings"

	"github.com/kcl1104/geminus/internal/ai/provider"
	"github.com/kcl1104/geminus/internal/model"
)

// Segmenter is the SAM 3.1 backend of the photo path (T-056, H-033): one
// concept (a short noun phrase) and one image per request, over the
// Responses API, the boxes of every match back (https://dev.meta.ai/docs/
// sam/overview, /docs/sam/segmenting, /docs/sam/reading-segmentation,
// retrieved 2026-10-26). It is a provider.BillableSegmenter, priced per
// image (spend.ImagePriceOf): only provider.NewMeteredSegmentClient calls
// it, and that client checks every answer (boxes inside the image, bounded
// counts, finite confidences) whatever the backend.
//
// The request asks for confidence values and the smallest masks
// (metadata include_confidence "true", mask_encoding one_bit), stores
// nothing (store false: the Responses API stores a response by default)
// and does not stream. The answer is read strictly by SAM's documented
// grammar (grammar.go); the masks are checked for their shape, never
// decoded, and dropped.
type Segmenter struct {
	model  string
	c      *conn
	allows func(role string) bool
	images bool
}

var _ provider.BillableSegmenter = (*Segmenter)(nil)

// NewSegmenter returns the SAM backend for an approved configuration:
// cfg.Approval a recorded, complete meta approval whose data policy names
// images, cfg.Model exactly its segmentation model, the credential its
// label.
func NewSegmenter(cfg Config) (*Segmenter, error) {
	if err := approved(cfg); err != nil {
		return nil, err
	}
	a := cfg.Approval
	if a.SegmentationModel() == "" || !a.AllowsImages() {
		return nil, ErrNotApproved
	}
	if cfg.Model != a.SegmentationModel() {
		return nil, errors.New("meta: the model is not the approval's segmentation model")
	}
	return newSegmenter(cfg, productionURL, nil, a.AllowsRole, true)
}

func newSegmenter(cfg Config, baseURL string, client *http.Client, allows func(string) bool, images bool) (*Segmenter, error) {
	if strings.TrimSpace(cfg.Model) == "" {
		return nil, errors.New("meta: no segmentation model; the owner approves one")
	}
	c, err := newConn(cfg.Credential, baseURL, client)
	if err != nil {
		return nil, err
	}
	return &Segmenter{model: cfg.Model, c: c, allows: allows, images: images}, nil
}

func (s *Segmenter) Name() string { return "meta" }

// SegmentModel is the model each call is priced at.
func (s *Segmenter) SegmentModel() string { return s.model }

// The Responses request of a segmentation (/docs/sam/segmenting, "Build
// the request": "A request pairs the prompt with one media part inside a
// single user message").
type responsesRequest struct {
	Model    string            `json:"model"`
	Input    []inputMessage    `json:"input"`
	Store    bool              `json:"store"`
	Stream   bool              `json:"stream"`
	Metadata map[string]string `json:"metadata"`
}

type inputMessage struct {
	Type    string `json:"type"`
	Role    string `json:"role"`
	Content []any  `json:"content"`
}

type inputText struct {
	Type string `json:"type"`
	Text string `json:"text"`
}

type inputImage struct {
	Type     string `json:"type"`
	ImageURL string `json:"image_url"`
}

// responsesAnswer is a Responses answer as this build reads it ("Response",
// /docs/api-reference/responses/schemas); other members are ignored.
type responsesAnswer struct {
	ID     string       `json:"id"`
	Object string       `json:"object"`
	Status string       `json:"status"`
	Model  string       `json:"model"`
	Output []outputItem `json:"output"`
	Usage  *struct {
		ImagesProcessed int64 `json:"images_processed"`
	} `json:"usage"`
}

type outputItem struct {
	Type    string `json:"type"`
	Role    string `json:"role"`
	Content []struct {
		Type string `json:"type"`
		Text string `json:"text"`
	} `json:"content"`
}

// SegmentOne asks SAM 3.1 where call.Concept is in call.Image. It
// refuses, before any I/O, a call without a reservation, an empty concept,
// an image that does not check, a role the approval does not serve and an
// approval whose data policy names no image.
func (s *Segmenter) SegmentOne(ctx context.Context, call provider.SegmentCall) (provider.SegmentAnswer, error) {
	if call.Reservation == "" || call.Concept == "" || call.Image.Check() != nil {
		return provider.SegmentAnswer{}, provider.NewError(provider.CodeInvalidRequest, 0, "")
	}
	if s.allows == nil || !s.allows(string(call.Role)) || !s.images {
		return provider.SegmentAnswer{}, provider.NewError(provider.CodePolicyNotApproved, 0, "")
	}
	body, err := json.Marshal(responsesRequest{Model: s.model, Store: false, Stream: false,
		Metadata: map[string]string{"include_confidence": "true", "mask_encoding": "one_bit"},
		Input: []inputMessage{{Type: "message", Role: "user", Content: []any{inputText{Type: "input_text", Text: call.Concept},
			inputImage{Type: "input_image", ImageURL: "data:" + call.Image.MediaType + ";base64," + base64.StdEncoding.EncodeToString(call.Image.Data)}}}},
	}, json.Deterministic(true))
	if err != nil {
		return provider.SegmentAnswer{}, provider.NewError(provider.CodeInvalidRequest, 0, "")
	}
	ans := provider.SegmentAnswer{Model: s.model, Credential: s.c.label}
	t := &tally{}
	ex, failed, err := s.c.post(ctx, t, "v1/responses", body, call.AttemptTimeout, call.MaxRetries)
	ans.FailedOver = failed
	if err != nil {
		ans.UnknownAttempts, ans.Undecoded = t.unknown, t.undecoded
		return ans, err
	}
	var r responsesAnswer
	if json.Unmarshal(ex.body, &r) != nil || r.Object != "response" || (r.Usage != nil && r.Usage.ImagesProcessed < 0) {
		// An answer that cannot be read: it may have been billed.
		ans.UnknownAttempts, ans.Undecoded = t.unknown+1, true
		return ans, provider.NewError(provider.CodeUnavailable, 0, s.c.label)
	}
	ans.UnknownAttempts, ans.Answered, ans.ProviderRequestID = t.unknown, true, safeID(r.ID)
	if r.Usage != nil {
		ans.ImagesProcessed = int(min(r.Usage.ImagesProcessed, 1<<20))
	}
	invalid := func(msg string) (provider.SegmentAnswer, error) {
		e := provider.NewError(provider.CodeInvalidOutput, 0, s.c.label)
		e.Issues = []model.ValidationIssue{model.NewIssue(provider.OutputInvalid, "", msg, model.SeverityError)}
		return ans, e
	}
	switch {
	case r.Model != s.model:
		return invalid("the segmentation is not from the Standard-mode model the request named")
	case r.Status != "completed":
		return invalid("the segmentation did not complete")
	}
	var text strings.Builder
	for _, item := range r.Output {
		if item.Type != "message" || item.Role != "assistant" {
			return invalid("the segmentation answer holds something other than its output text")
		}
		for _, part := range item.Content {
			if part.Type != "output_text" {
				return invalid("the segmentation answer holds something other than its output text")
			}
			text.WriteString(part.Text)
		}
	}
	w, h, segs, err := parseSegmentation(text.String(), call.Concept)
	if err != nil {
		return invalid("the output does not follow the segmentation grammar")
	}
	ans.Width, ans.Height, ans.Segments = w, h, segs
	return ans, nil
}
