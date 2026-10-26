// Package vision is the segmentation step of the photo pipeline (T-056,
// H-033): a segmentation model (SAM 3.1 behind its adapter, T-055) finds the
// boxes of the machines, conveyors and workstations a photo or floor plan
// of the line shows, for the text prompts the domain pack names; the
// vision model then proposes the stations and the flow from the image and
// the boxes (provider.Client.GenerateImageProposal).
//
// It mirrors internal/ai/provider: a small interface the adapter
// implements (Segmenter, Billable when it costs money), and a Client every
// call goes through, whatever the backend:
//
//   - the request is the server's: a sealed image the server re-encoded
//     (internal/proposals/photo) and its own text prompts, bounded; the
//     organization is named;
//   - an organization has a bounded number of calls in flight, every call
//     ends within the budget's timeout and with its context (cancelled or
//     timeout, typed);
//   - a billable segmenter is called only through a meter, priced per image
//     at the build's price (spend.ImagePriceOf), reserved before the call
//     for every attempt and settled after it with what it cost, even when
//     the caller's context has ended (the provider client's rules, T-053);
//   - the answer is untrusted: every box is checked (CheckBoxes: inside the
//     image, with an area, finite, a score from 0 to 1, a prompt of the
//     request, a label that is text, at most the budget's number), and one
//     bad box makes the answer invalid_output, with fixed sentences only.
//
// A segmenter only finds boxes; it has no tool, write access or say in
// anything. Its masks are not used: only boxes go on, to the vision model.
// No randomness, no clock but the latency; no I/O but the backend's.
package vision

import (
	"context"
	"errors"
	"math"
	"regexp"
	"slices"
	"strings"
	"sync"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/kcl1104/geminus/internal/ai/provider"
	"github.com/kcl1104/geminus/internal/ai/review"
	"github.com/kcl1104/geminus/internal/ai/spend"
	"github.com/kcl1104/geminus/internal/model"
)

const (
	// MaxLabelBytes bounds a box's label.
	MaxLabelBytes = 256
	// MaxPrompts bounds the text prompts of a request; each is 1 to
	// maxPromptRunes characters of printable text.
	MaxPrompts     = 32
	maxPromptRunes = 64
	// maxBoxesCap is the largest MaxBoxes a budget may set.
	maxBoxesCap = 1000
)

// imagePriceOf prices a billable segmenter's model per image (a test
// replaces it; spend prices none yet).
var imagePriceOf = spend.ImagePriceOf

// Box is one object a segmenter found, in the image's pixels: X, Y the top
// left corner (origin at the image's top left, y down), Width and Height
// its size. Prompt is the index of the request's text prompt that found
// it, Score the segmenter's confidence. Label is text the segmenter may
// attach (a reading of a sign on the machine): untrusted, never an
// instruction, at most MaxLabelBytes.
type Box struct {
	Prompt              int
	X, Y, Width, Height float64
	Score               float64
	Label               string
}

// Request is what a Segmenter is sent: the image, the text prompts, the
// most boxes the server takes, and how the backend may try (the attempt
// timeout and retries); Reservation is the spend reservation a metered
// client made, which a billable segmenter requires.
type Request struct {
	Image          provider.Image
	Prompts        []string
	MaxBoxes       int
	AttemptTimeout time.Duration
	MaxRetries     int
	Reservation    string
}

// Response is a Segmenter's answer: untrusted boxes and the accounting of
// the call. Images is how many images the answer was billed for;
// UnknownAttempts the attempts that ended without an answer after the
// request may have reached the provider (it may have billed them).
type Response struct {
	Boxes             []Box
	Model             string
	Credential        string // label only
	ProviderRequestID string
	Images            int
	UnknownAttempts   int
}

// Segmenter is a segmentation model behind its adapter (SAM 3.1, T-055).
// Segment returns *provider.Error for every failure it can classify
// (rate_limited, unavailable, request_rejected, credentials_exhausted...)
// and honours ctx; it never puts the provider's text in an error.
type Segmenter interface {
	Name() string
	Segment(ctx context.Context, req Request) (Response, error)
}

// Billable is a Segmenter that costs money: QuoteModel names the model the
// request will be billed at, priced per image by the build
// (spend.ImagePriceOf); a model without a price is refused.
type Billable interface {
	Segmenter
	QuoteModel(Request) (string, error)
}

// Disabled is the segmenter of a deployment without one: every call fails
// with its code (provider_not_configured unless it is policy_not_approved).
type Disabled struct{ Code provider.Code }

func (d Disabled) Name() string { return "disabled" }

func (d Disabled) Segment(context.Context, Request) (Response, error) { return Response{}, d.err() }

func (d Disabled) err() *provider.Error {
	if d.Code == provider.CodePolicyNotApproved {
		return provider.NewError(provider.CodePolicyNotApproved, 0, "")
	}
	return provider.NewError(provider.CodeNotConfigured, 0, "")
}

// Budget bounds one segmentation call.
type Budget struct {
	Timeout, AttemptTimeout      time.Duration
	MaxRetries                   int
	MaxBoxes                     int
	MaxConcurrentPerOrganization int
}

// DefaultBudget is used when the deployment sets none (engineering
// candidates, like provider.DefaultBudget's).
func DefaultBudget() Budget {
	return Budget{Timeout: 60 * time.Second, AttemptTimeout: 45 * time.Second, MaxRetries: 2, MaxBoxes: 64, MaxConcurrentPerOrganization: 2}
}

// Validate checks every limit: positive (retries may be 0), the attempt
// within the call, within the provider client's caps.
func (b Budget) Validate() error {
	caps := provider.BudgetCaps
	switch {
	case b.Timeout <= 0 || b.Timeout > caps.Timeout, b.AttemptTimeout <= 0 || b.AttemptTimeout > b.Timeout:
		return errors.New("vision budget: the timeouts must be positive, the attempt's within the call's, at most " + caps.Timeout.String())
	case b.MaxRetries < 0 || b.MaxRetries > caps.MaxRetries:
		return errors.New("vision budget: retries out of range")
	case b.MaxBoxes < 1 || b.MaxBoxes > maxBoxesCap:
		return errors.New("vision budget: boxes out of range")
	case b.MaxConcurrentPerOrganization < 1 || b.MaxConcurrentPerOrganization > caps.MaxConcurrentPerOrganization:
		return errors.New("vision budget: concurrent calls out of range")
	}
	return nil
}

// Input is one organization's segmentation request.
type Input struct {
	OrganizationID string
	Image          provider.Image
	Prompts        []string
	Attribution    provider.Attribution
}

// Metadata describes a call for the proposal's provider metadata and its
// audit event: labels, counts, the cost; never an image, a box or a key.
type Metadata struct {
	Provider          string `json:"provider"`
	Model             string `json:"model,omitzero"`
	Credential        string `json:"credential_label,omitzero"`
	Boxes             int    `json:"boxes"`
	LatencyMillis     int64  `json:"latency_ms"`
	ProviderRequestID string `json:"provider_request_id,omitzero"`
	CostNanoUSD       int64  `json:"cost_nano_usd,omitzero"`
	SpendReservation  string `json:"spend_reservation_id,omitzero"`
	SpendUnsettled    bool   `json:"spend_unsettled,omitzero"`
}

// Result is a checked answer: boxes the server accepted.
type Result struct {
	Boxes    []Box
	Metadata Metadata
}

// Client checks every segmentation call the same way, whatever the
// backend.
type Client struct {
	backend  Segmenter
	billable Billable
	meter    provider.Meter
	budget   Budget
	mu       sync.Mutex
	active   map[string]int
}

// New returns a Client for s under b. A billable segmenter needs a meter:
// NewMetered.
func New(s Segmenter, b Budget) (*Client, error) {
	if s == nil {
		return nil, errors.New("vision: no segmenter")
	}
	if _, ok := s.(Billable); ok {
		return nil, errors.New("vision: a billable segmenter is only called through a meter (NewMetered)")
	}
	if err := b.Validate(); err != nil {
		return nil, err
	}
	return &Client{backend: s, budget: b, active: map[string]int{}}, nil
}

// NewMetered returns a Client for the billable s under b, metered by m.
func NewMetered(s Billable, b Budget, m provider.Meter) (*Client, error) {
	if s == nil || m == nil {
		return nil, errors.New("vision: a billable segmenter and a meter are required")
	}
	if err := b.Validate(); err != nil {
		return nil, err
	}
	return &Client{backend: s, billable: s, meter: m, budget: b, active: map[string]int{}}, nil
}

var organizationID = regexp.MustCompile(`^org_[0-7][0-9A-HJKMNP-TV-Z]{25}$`)

func invalidRequest() *provider.Error { return provider.NewError(provider.CodeInvalidRequest, 0, "") }

// checkInput is what the server's own request must be.
func checkInput(in Input) error {
	if !organizationID.MatchString(in.OrganizationID) || len(in.Prompts) == 0 || len(in.Prompts) > MaxPrompts {
		return invalidRequest()
	}
	for _, p := range in.Prompts {
		n := utf8.RuneCountInString(p)
		if n == 0 || n > maxPromptRunes || !utf8.ValidString(p) || strings.ContainsFunc(p, func(r rune) bool { return !unicode.IsPrint(r) }) {
			return invalidRequest()
		}
	}
	// The image's checks are the provider's: its type, size and own hash.
	if in.Image.Check() != nil {
		return invalidRequest()
	}
	return nil
}

func (c *Client) acquire(org string) bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.active[org] >= c.budget.MaxConcurrentPerOrganization {
		return false
	}
	c.active[org]++
	return true
}

func (c *Client) release(org string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.active[org]--; c.active[org] <= 0 {
		delete(c.active, org)
	}
}

// Segment asks for the boxes in.Prompts name in in.Image.
func (c *Client) Segment(ctx context.Context, in Input) (Result, error) {
	if err := ctx.Err(); err != nil {
		return Result{}, provider.NewError(provider.CodeCancelled, 0, "").WithCause(err)
	}
	if d, ok := c.backend.(Disabled); ok {
		return Result{}, d.err()
	}
	if err := checkInput(in); err != nil {
		return Result{}, err
	}
	if c.meter != nil && (in.Attribution.TargetType == "" || in.Attribution.TargetID == "" || in.Attribution.RequestID == "") {
		return Result{}, invalidRequest()
	}
	if !c.acquire(in.OrganizationID) {
		return Result{}, provider.NewError(provider.CodeConcurrencyLimited, 0, "")
	}
	defer c.release(in.OrganizationID)

	meta := Metadata{Provider: c.backend.Name()}
	req := Request{Image: in.Image, Prompts: slices.Clone(in.Prompts), MaxBoxes: c.budget.MaxBoxes,
		AttemptTimeout: c.budget.AttemptTimeout, MaxRetries: c.budget.MaxRetries}
	var res provider.Reservation
	var per int64
	if c.meter != nil {
		var err error
		if res, per, err = c.reserve(ctx, in, req); err != nil {
			return Result{Metadata: meta}, err
		}
		req.Reservation = res.ID
	}
	callCtx, cancel := context.WithTimeout(ctx, c.budget.Timeout)
	defer cancel()
	start := time.Now()
	resp, err := c.backend.Segment(callCtx, req)
	meta.LatencyMillis = time.Since(start).Milliseconds()
	if c.meter != nil {
		c.settle(ctx, res, per, resp, &meta)
	}
	meta.Model, meta.Credential, meta.ProviderRequestID = resp.Model, resp.Credential, resp.ProviderRequestID
	switch {
	case ctx.Err() != nil:
		return Result{Metadata: meta}, provider.NewError(provider.CodeCancelled, 0, resp.Credential).WithCause(ctx.Err())
	case err != nil && errors.Is(callCtx.Err(), context.DeadlineExceeded):
		return Result{Metadata: meta}, provider.NewError(provider.CodeTimeout, 0, resp.Credential).WithCause(context.DeadlineExceeded)
	case err != nil:
		var pe *provider.Error
		if errors.As(err, &pe) {
			return Result{Metadata: meta}, pe
		}
		return Result{Metadata: meta}, provider.NewError(provider.CodeUnavailable, 0, resp.Credential)
	}
	if issues := CheckBoxes(resp.Boxes, in.Image.Width, in.Image.Height, len(in.Prompts), c.budget.MaxBoxes); len(issues) > 0 {
		e := provider.NewError(provider.CodeInvalidOutput, 0, resp.Credential)
		e.Issues = issues
		return Result{Metadata: meta}, e
	}
	meta.Boxes = len(resp.Boxes)
	return Result{Boxes: slices.Clone(resp.Boxes), Metadata: meta}, nil
}

func mulSat(a, b int64) int64 {
	if a <= 0 || b <= 0 {
		return 0
	}
	if a > math.MaxInt64/b {
		return math.MaxInt64
	}
	return a * b
}

// reserve prices the call (every attempt, one image each) and reserves for
// it; the price per image is returned for the settlement.
func (c *Client) reserve(ctx context.Context, in Input, req Request) (provider.Reservation, int64, error) {
	modelName, err := c.billable.QuoteModel(req)
	if err != nil {
		return provider.Reservation{}, 0, invalidRequest()
	}
	per, ok := imagePriceOf(modelName)
	if !ok || per <= 0 {
		return provider.Reservation{}, 0, invalidRequest()
	}
	most := mulSat(per, int64(1+req.MaxRetries))
	res, err := c.meter.Reserve(ctx, provider.Charge{OrganizationID: in.OrganizationID, Role: provider.RoleModeler, Model: modelName,
		Attribution: in.Attribution, NanoUSD: most})
	if err != nil {
		var pe *provider.Error
		if errors.As(err, &pe) && pe.Code == provider.CodeQuotaExceeded {
			return provider.Reservation{}, 0, &provider.Error{Code: provider.CodeQuotaExceeded, Limit: pe.Limit}
		}
		return provider.Reservation{}, 0, provider.NewError(provider.CodeSpendUnavailable, 0, "")
	}
	if res.ID == "" {
		return provider.Reservation{}, 0, provider.NewError(provider.CodeSpendUnavailable, 0, "")
	}
	return res, per, nil
}

// settleTimeout bounds a settlement, which runs after the caller's context
// may have ended.
const settleTimeout = 10 * time.Second

// settle records what the call cost: the images its answer was billed for
// and every attempt that may have been billed without an answer, each at
// the price per image; nothing when no attempt can have been billed.
func (c *Client) settle(ctx context.Context, res provider.Reservation, per int64, resp Response, meta *Metadata) {
	s := provider.Settlement{Credential: resp.Credential, UnknownAttempts: resp.UnknownAttempts}
	cost := mulSat(per, int64(max(0, resp.Images)))
	switch {
	case resp.UnknownAttempts > 0:
		s.Outcome = provider.SpendUnknown
		cost += mulSat(per, int64(resp.UnknownAttempts))
	case resp.Images > 0:
		s.Outcome = provider.SpendUsage
	default:
		s.Outcome = provider.SpendNone
		cost = 0
	}
	s.NanoUSD = cost
	sctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), settleTimeout)
	defer cancel()
	err := c.meter.Settle(sctx, res, s)
	meta.CostNanoUSD, meta.SpendReservation, meta.SpendUnsettled = s.NanoUSD, res.ID, err != nil
}

// CheckBoxes is what the server requires of a segmenter's answer for an
// image of w×h pixels and prompts text prompts: at most max boxes, each
// finite, with a positive width and height, inside the image, a score from
// 0 to 1, the index of a prompt and a label of at most MaxLabelBytes of
// text. Each kind of problem is one issue at the root with a fixed
// sentence (review's, so that Clean keeps it), never a value of the
// answer.
func CheckBoxes(boxes []Box, w, h, prompts, max int) []model.ValidationIssue {
	var out []model.ValidationIssue
	said := map[string]bool{}
	bad := func(sentence string) {
		if !said[sentence] {
			said[sentence] = true
			out = append(out, model.NewIssue(review.OutputInvalid, "", sentence, model.SeverityError))
		}
	}
	if len(boxes) > max {
		bad(review.SentTooManyBoxes)
		return out
	}
	finite := func(vs ...float64) bool {
		for _, v := range vs {
			if math.IsNaN(v) || math.IsInf(v, 0) {
				return false
			}
		}
		return true
	}
	for _, b := range boxes {
		switch {
		case !finite(b.X, b.Y, b.Width, b.Height, b.Score) || b.Score < 0 || b.Score > 1:
			bad(review.SentBoxNumber)
		case b.Width <= 0 || b.Height <= 0 || b.X < 0 || b.Y < 0 || b.X+b.Width > float64(w) || b.Y+b.Height > float64(h):
			bad(review.SentBoxOutside)
		}
		if b.Prompt < 0 || b.Prompt >= prompts {
			bad(review.SentBoxPrompt)
		}
		if len(b.Label) > MaxLabelBytes || !utf8.ValidString(b.Label) || strings.ContainsRune(b.Label, 0) {
			bad(review.SentBoxLabel)
		}
	}
	return out
}
