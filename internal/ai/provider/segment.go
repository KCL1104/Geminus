package provider

import (
	"cmp"
	"context"
	"errors"
	"math"
	"regexp"
	"slices"
	"strconv"
	"sync"
	"time"

	"github.com/kcl1104/geminus/internal/ai/spend"
	"github.com/kcl1104/geminus/internal/model"
)

// Segmentation (T-055, for T-056's photo path; H-033: SAM 3.1 finds the
// machines, conveyors and workstations of a demo photo, and Muse Spark
// judges stations and flow from the image and the boxes). A segmenter
// only locates: it returns boxes, never a model change. The proposal is
// still the provider's answer to a sealed prompt (Client), validated,
// risk-scored and approved by people as any other (AI_SPEC §4).
//
// Every backend goes through SegmentClient, which checks, whatever the
// backend:
//
//   - the request, before any call: the organization, a known role (the
//     role of the proposal the photo serves: its approval's data policy
//     and its spend attribution), the image (NewImage), and 1 to
//     MaxSegmentConcepts concepts the server chose, each a short noun
//     phrase of lowercase ASCII words (no list, punctuation or prompt
//     token: SAM takes one concept per request);
//   - every answer: one frame of the image's size (or its rotated size,
//     when the file's metadata turns it), at most MaxSegmentsPerConcept
//     objects per concept, distinct object IDs, each box's corners inside
//     the frame (inclusive) and each confidence a number from 0 to 1. An
//     answer outside them is invalid_output, with the place and a fixed
//     message, never the provider's text.
//
// A billable backend is called only through NewMeteredSegmentClient: each
// concept's call is reserved for at (1 + the budget's retries) images at
// the model's image price (spend.ImagePriceOf) and settled after it: the
// images the answer says it billed, at least the one it was sent; one
// image for every attempt that may have been billed without an answer; at
// least the whole reservation for an answer that came back but could not
// be read; nothing when every attempt was answered with an error status.

// The limits of a segmentation (PROPOSED D-120).
const (
	MaxSegmentConcepts     = 8
	MaxSegmentConceptBytes = 64
	MaxSegmentsPerConcept  = 100
	// maxObjectID bounds an object ID (SAM's ordinals are small integers).
	maxObjectID = 1 << 20
)

// Segmenter is what T-056's photo path depends on. *SegmentClient is one.
type Segmenter interface {
	Segment(ctx context.Context, req SegmentRequest) (Segmentation, error)
}

// SegmentRequest asks where the concepts are in the image.
type SegmentRequest struct {
	OrganizationID string
	// Role is the prompt role of the proposal the photo serves: the
	// backend serves it only if its approval's data policy names it, and
	// the spend is charged to it.
	Role        Role
	Attribution Attribution
	Image       Image
	// Concepts are short noun phrases the server chose ("machine",
	// "conveyor belt"), one provider request each.
	Concepts []string
}

// Box is a rectangle in the frame's pixels, its corners inclusive: X1 ≤
// X2 < width, Y1 ≤ Y2 < height.
type Box struct{ X1, Y1, X2, Y2 int }

// Segment is one object the segmenter found.
type Segment struct {
	// Concept is the concept of the request that found it.
	Concept string
	// ObjectID is the provider's object ordinal, distinct within a concept.
	ObjectID int
	Box      Box
	// Confidence is the provider's own confidence, 0 to 1: never grounds
	// for skipping a review.
	Confidence float64
}

// Segmentation is the segmenter's checked answer.
type Segmentation struct {
	// Width and Height are the frame of the boxes: the image's size, or
	// the size its metadata turns it to.
	Width, Height int
	// Segments in the request's concept order, then by object ID.
	Segments []Segment
	Metadata SegmentMetadata
}

// SegmentMetadata describes a segmentation for audit and logs: no image,
// no provider text, no key; a credential by label.
type SegmentMetadata struct {
	Provider    string   `json:"provider"`
	Model       string   `json:"model,omitzero"`
	Credential  string   `json:"credential_label,omitzero"`
	FailedOver  []string `json:"failed_over_from,omitzero"`
	Role        Role     `json:"role"`
	ImageDigest string   `json:"image_digest"`
	Concepts    int      `json:"concepts"`
	// Images is the images the provider billed, as it said (at least one
	// for each concept it answered).
	Images               int      `json:"images_billed"`
	CostNanoUSD          int64    `json:"cost_nano_usd,omitzero"`
	SpendReservations    []string `json:"spend_reservation_ids,omitzero"`
	SpendUnsettled       bool     `json:"spend_unsettled,omitzero"`
	SpendOverReservation bool     `json:"spend_over_reservation,omitzero"`
	LatencyMillis        int64    `json:"latency_ms"`
	ProviderRequestIDs   []string `json:"provider_request_ids,omitzero"`
}

// SegmentCall is one concept's request to a backend.
type SegmentCall struct {
	Image          Image
	Concept        string
	Role           Role
	AttemptTimeout time.Duration
	MaxRetries     int
	// Reservation is the spend reservation of the call; a billable
	// backend refuses a call without one.
	Reservation string
}

// SegmentAnswer is a backend's answer to one concept, unchecked.
type SegmentAnswer struct {
	Model             string
	Credential        string   // label only
	FailedOver        []string // labels refused during this call
	ProviderRequestID string
	// Width and Height are the frame the provider reported (0 when it
	// found nothing).
	Width, Height int
	Segments      []Segment
	// ImagesProcessed is what the answer says it billed; 0 when it says
	// nothing.
	ImagesProcessed int
	// Answered: an answer came back and was read (its segments may still
	// be invalid).
	Answered bool
	// UnknownAttempts counts the attempts that may have been billed
	// without an answer the backend could read; Undecoded that one of them
	// was an answer that came back but could not be read.
	UnknownAttempts int
	Undecoded       bool
}

// SegmentBackend is one segmentation mode: the fake, the live SAM 3.1
// adapter, or Disabled. SegmentOne returns *Error for every failure it can
// classify and honours ctx.
type SegmentBackend interface {
	Name() string
	SegmentOne(ctx context.Context, call SegmentCall) (SegmentAnswer, error)
}

// BillableSegmenter is a segmentation backend that costs money, at
// SegmentModel's image price.
type BillableSegmenter interface {
	SegmentBackend
	SegmentModel() string
}

// SegmentOne makes Disabled a segmentation backend too: every call fails
// with its code.
func (d Disabled) SegmentOne(context.Context, SegmentCall) (SegmentAnswer, error) {
	return SegmentAnswer{}, d.err()
}

// SegmentClient checks every segmentation the same way, whatever the
// backend.
type SegmentClient struct {
	backend  SegmentBackend
	billable BillableSegmenter
	meter    Meter
	budget   Budget
	mu       sync.Mutex
	active   map[string]int
}

var _ Segmenter = (*SegmentClient)(nil)

// NewSegmentClient returns a client of b under budget (its timeouts,
// retries and concurrency). A billable backend needs a meter.
func NewSegmentClient(b SegmentBackend, budget Budget) (*SegmentClient, error) {
	if b == nil {
		return nil, errors.New("ai provider: no segmentation backend")
	}
	if _, ok := b.(BillableSegmenter); ok {
		return nil, errors.New("ai provider: a billable segmenter is only called through a meter (NewMeteredSegmentClient)")
	}
	if err := budget.Validate(); err != nil {
		return nil, err
	}
	return &SegmentClient{backend: b, budget: budget, active: map[string]int{}}, nil
}

// NewMeteredSegmentClient returns a client of the billable b under budget,
// metered by m.
func NewMeteredSegmentClient(b BillableSegmenter, budget Budget, m Meter) (*SegmentClient, error) {
	if b == nil || m == nil {
		return nil, errors.New("ai provider: a billable segmenter and a meter are required")
	}
	if err := budget.Validate(); err != nil {
		return nil, err
	}
	return &SegmentClient{backend: b, billable: b, meter: m, budget: budget, active: map[string]int{}}, nil
}

// concept is a short noun phrase: lowercase ASCII words and digits, single
// spaces or hyphens between them. No list (SAM's comma lists return
// nothing), no punctuation, no prompt token.
var concept = regexp.MustCompile(`^[a-z0-9]+(?:[ -][a-z0-9]+)*$`)

func (c *SegmentClient) acquire(org string) bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.active[org] >= c.budget.MaxConcurrentPerOrganization {
		return false
	}
	c.active[org]++
	return true
}

func (c *SegmentClient) release(org string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.active[org]--; c.active[org] <= 0 {
		delete(c.active, org)
	}
}

// checkRequest reports whether req is one the client sends.
func (c *SegmentClient) checkRequest(req SegmentRequest) bool {
	if !organizationID.MatchString(req.OrganizationID) || req.Role.OutputSchema() == "" || req.Image.Check() != nil {
		return false
	}
	if c.meter != nil && !req.Attribution.complete() {
		return false
	}
	if len(req.Concepts) == 0 || len(req.Concepts) > MaxSegmentConcepts {
		return false
	}
	seen := map[string]bool{}
	for _, k := range req.Concepts {
		if len(k) > MaxSegmentConceptBytes || !concept.MatchString(k) || seen[k] {
			return false
		}
		seen[k] = true
	}
	return true
}

// Segment asks where each concept is in the image, one backend call per
// concept, and returns the checked segments of all of them; any failure
// fails the whole segmentation (what was spent is settled all the same).
func (c *SegmentClient) Segment(ctx context.Context, req SegmentRequest) (Segmentation, error) {
	if err := ctx.Err(); err != nil {
		return Segmentation{}, NewError(CodeCancelled, 0, "").WithCause(err)
	}
	if d, ok := c.backend.(Disabled); ok {
		return Segmentation{}, d.err()
	}
	if !c.checkRequest(req) {
		return Segmentation{}, invalidRequest()
	}
	if !c.acquire(req.OrganizationID) {
		return Segmentation{}, NewError(CodeConcurrencyLimited, 0, "")
	}
	defer c.release(req.OrganizationID)

	out := Segmentation{Metadata: SegmentMetadata{Provider: c.backend.Name(), Role: req.Role, ImageDigest: req.Image.Digest,
		Concepts: len(req.Concepts)}}
	meta := &out.Metadata
	callCtx, cancel := context.WithTimeout(ctx, c.budget.Timeout)
	defer cancel()
	start := time.Now()
	defer func() { meta.LatencyMillis = time.Since(start).Milliseconds() }()
	frame := [2]int{}
	for i, k := range req.Concepts {
		call := SegmentCall{Image: req.Image, Concept: k, Role: req.Role, AttemptTimeout: c.budget.AttemptTimeout, MaxRetries: c.budget.MaxRetries}
		var m *segmentMetering
		if c.meter != nil {
			var err error
			if m, err = c.reserve(ctx, req, call); err != nil {
				return out, err
			}
			call.Reservation = m.res.ID
			meta.SpendReservations = append(meta.SpendReservations, m.res.ID)
		}
		ans, err := c.backend.SegmentOne(callCtx, call)
		if m != nil {
			c.settle(ctx, m, m.settlement(ans), meta)
		}
		if ans.Model != "" {
			meta.Model = ans.Model
		}
		if ans.Credential != "" {
			meta.Credential = ans.Credential
		}
		meta.FailedOver = ans.FailedOver
		if ans.ProviderRequestID != "" {
			meta.ProviderRequestIDs = append(meta.ProviderRequestIDs, ans.ProviderRequestID)
		}
		switch {
		case ans.Answered && m != nil:
			meta.Images += max(1, ans.ImagesProcessed) // as settled: at least the image sent
		case ans.Answered:
			meta.Images += max(0, ans.ImagesProcessed)
		}
		switch {
		case ctx.Err() != nil:
			return out, NewError(CodeCancelled, 0, ans.Credential).WithCause(ctx.Err())
		case err != nil && errors.Is(callCtx.Err(), context.DeadlineExceeded):
			return out, NewError(CodeTimeout, 0, ans.Credential).WithCause(context.DeadlineExceeded)
		case err != nil:
			var pe *Error
			if errors.As(err, &pe) {
				return out, pe
			}
			return out, NewError(CodeUnavailable, 0, ans.Credential)
		}
		if issues := checkAnswer(req.Image, k, i, ans, &frame); len(issues) > 0 {
			e := NewError(CodeInvalidOutput, 0, ans.Credential)
			e.Issues = issues[:min(len(issues), 20)]
			return out, e
		}
		segs := slices.Clone(ans.Segments)
		slices.SortFunc(segs, func(a, b Segment) int { return cmp.Compare(a.ObjectID, b.ObjectID) })
		out.Segments = append(out.Segments, segs...)
	}
	out.Width, out.Height = req.Image.Width, req.Image.Height
	if frame != [2]int{} {
		out.Width, out.Height = frame[0], frame[1]
	}
	return out, nil
}

// checkAnswer lists what is wrong with one concept's answer (the i-th
// concept of the request), as issues at /concepts/<i>/...; frame is the
// first frame an answer with segments named, which every later one must
// name too.
func checkAnswer(img Image, k string, i int, a SegmentAnswer, frame *[2]int) []model.ValidationIssue {
	at := "/concepts/" + strconv.Itoa(i)
	var issues []model.ValidationIssue
	add := func(path, msg string) {
		issues = append(issues, model.NewIssue(OutputInvalid, path, msg, model.SeverityError))
	}
	if len(a.Segments) == 0 {
		return nil // nothing found is an answer
	}
	if len(a.Segments) > MaxSegmentsPerConcept {
		add(at+"/segments", "more objects than "+strconv.Itoa(MaxSegmentsPerConcept))
		return issues
	}
	w, h := a.Width, a.Height
	switch {
	case !((w == img.Width && h == img.Height) || (w == img.Height && h == img.Width)):
		add(at+"/frame", "the frame is not the image's size")
		return issues
	case *frame == [2]int{}:
		*frame = [2]int{w, h}
	case *frame != [2]int{w, h}:
		add(at+"/frame", "the frame is not the one the other concepts named")
		return issues
	}
	ids := map[int]bool{}
	for j, s := range a.Segments {
		p := at + "/segments/" + strconv.Itoa(j)
		b := s.Box
		switch {
		case s.Concept != k:
			add(p+"/concept", "not the concept the request named")
		case s.ObjectID < 0 || s.ObjectID > maxObjectID || ids[s.ObjectID]:
			add(p+"/object_id", "not a distinct object ID")
		case b.X1 < 0 || b.Y1 < 0 || b.X1 > b.X2 || b.Y1 > b.Y2 || b.X2 >= w || b.Y2 >= h:
			add(p+"/box", "the box is not inside the frame")
		case math.IsNaN(s.Confidence) || s.Confidence < 0 || s.Confidence > 1:
			add(p+"/confidence", "the confidence is not a number from 0 to 1")
		}
		ids[s.ObjectID] = true
	}
	return issues
}

// segmentMetering is one concept call's reservation.
type segmentMetering struct {
	price int64 // one image
	res   Reservation
}

// reserve prices one concept's call and reserves for it.
func (c *SegmentClient) reserve(ctx context.Context, req SegmentRequest, call SegmentCall) (*segmentMetering, error) {
	model := c.billable.SegmentModel()
	price, ok := spend.ImagePriceOf(model)
	if !ok {
		return nil, invalidRequest()
	}
	most := mulSat(price, int64(1+max(0, call.MaxRetries)))
	res, err := c.meter.Reserve(ctx, Charge{OrganizationID: req.OrganizationID, Role: req.Role, Model: model, Attribution: req.Attribution, NanoUSD: most})
	if err != nil {
		var pe *Error
		if errors.As(err, &pe) && pe.Code == CodeQuotaExceeded {
			return nil, &Error{Code: CodeQuotaExceeded, Limit: pe.Limit}
		}
		return nil, NewError(CodeSpendUnavailable, 0, "")
	}
	if res.ID == "" {
		return nil, NewError(CodeSpendUnavailable, 0, "")
	}
	return &segmentMetering{price: price, res: res}, nil
}

// settlement is what the call that answered a cost.
func (m *segmentMetering) settlement(a SegmentAnswer) Settlement {
	s := Settlement{Credential: a.Credential, UnknownAttempts: a.UnknownAttempts}
	var cost int64
	if a.Answered {
		cost = mulSat(int64(max(1, a.ImagesProcessed)), m.price)
	}
	switch {
	case a.UnknownAttempts > 0:
		s.Outcome = SpendUnknown
		s.NanoUSD = addSat(cost, mulSat(int64(a.UnknownAttempts), m.price))
		if a.Undecoded {
			s.NanoUSD = max(s.NanoUSD, m.res.Charge.NanoUSD)
		}
	case a.Answered:
		s.Outcome, s.NanoUSD = SpendUsage, cost
	default:
		s.Outcome = SpendNone
	}
	return s
}

// settle records s for the reservation, whether or not ctx has ended.
func (c *SegmentClient) settle(ctx context.Context, m *segmentMetering, s Settlement, meta *SegmentMetadata) {
	sctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), settleTimeout)
	defer cancel()
	if err := c.meter.Settle(sctx, m.res, s); err != nil {
		meta.SpendUnsettled = true
	}
	meta.CostNanoUSD = addSat(meta.CostNanoUSD, s.NanoUSD)
	if s.NanoUSD > m.res.Charge.NanoUSD {
		meta.SpendOverReservation = true
	}
}
