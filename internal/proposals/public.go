package proposals

import (
	"encoding/json/jsontext"
	"encoding/json/v2"
	"fmt"
	"regexp"
	"slices"

	"github.com/kcl1104/geminus/internal/ai/provider"
	"github.com/kcl1104/geminus/internal/model"
	"github.com/kcl1104/geminus/internal/model/hashing"
	"github.com/kcl1104/geminus/internal/proposals/photo"
	"github.com/kcl1104/geminus/internal/storage/jobs"
	store "github.com/kcl1104/geminus/internal/storage/proposals"
)

// Proposal is proposal.schema.json: the server's resource. Members a state
// does not hold are left out.
type Proposal struct {
	ProposalID           string                 `json:"proposal_id"`
	OrganizationID       string                 `json:"organization_id"`
	ModelID              string                 `json:"model_id"`
	BaseVersionID        string                 `json:"base_version_id"`
	Mode                 string                 `json:"mode"`
	Status               string                 `json:"status"`
	CreatedBy            string                 `json:"created_by"`
	CreatedAt            string                 `json:"created_at"`
	RequiresConfirmation bool                   `json:"requires_confirmation"`
	Summary              *string                `json:"summary,omitzero"`
	Assumptions          []string               `json:"assumptions,omitzero"`
	RiskLevel            *string                `json:"risk_level,omitzero"`
	Confidence           *float64               `json:"confidence,omitzero"`
	Changes              jsontext.Value         `json:"changes,omitzero"`
	ExpectedEffects      []string               `json:"expected_effects,omitzero"`
	Questions            []string               `json:"questions,omitzero"`
	Validation           jsontext.Value         `json:"validation,omitzero"`
	NewVersionID         *string                `json:"new_version_id,omitzero"`
	ScenarioID           *string                `json:"scenario_id,omitzero"`
	PatchHash            *string                `json:"patch_hash,omitzero"`
	Error                *ErrorEnvelope         `json:"error,omitzero"`
	DatasetBindings      []model.DatasetBinding `json:"dataset_bindings,omitzero"`
	ServerRiskLevel      *string                `json:"server_risk_level,omitzero"`
	ProviderRiskClaim    *string                `json:"provider_risk_claim,omitzero"`
	// The decision record (T-026, D-068 APPROVED): approved, rejected and
	// conflicted only.
	DecidedBy       *string `json:"decided_by,omitzero"`
	DecidedAt       *string `json:"decided_at,omitzero"`
	DecisionComment *string `json:"decision_comment,omitzero"`
	// GeneratedBy is which provider and model wrote it (H-043), when the
	// row has the provider's metadata.
	GeneratedBy *GeneratedBy `json:"generated_by,omitzero"`
	// SourceImage is the photo the proposal was asked for with (T-056), in
	// every state; nil for a proposal asked for in words.
	SourceImage *SourceImage `json:"source_image,omitzero"`
	// Sources are the pages a research call opened and the answer relied
	// on (H-046 (2)), in the states that keep what the provider wrote.
	Sources []provider.WebSource `json:"sources,omitzero"`
	// RequestedModel is the model the request named (H-065), in every
	// state; empty when it named none.
	RequestedModel string `json:"requested_model,omitzero"`
}

// GeneratedBy is common.schema.json's GeneratedBy (H-043): which provider
// and model wrote an AI result and, for a photo, which segmenter read it,
// how many objects it found and (H-052) their boxes. Only these come from
// the stored metadata: never a credential label, cost, reservation, request
// ID, hash, latency or object ID.
type GeneratedBy struct {
	Provider  string              `json:"provider"`
	Model     string              `json:"model,omitzero"`
	Segmenter *GeneratedSegmenter `json:"segmenter,omitzero"`
	// WebSearch: the call ran with research on (H-046 (2)).
	WebSearch *GeneratedWebSearch `json:"web_search,omitzero"`
	// RevisedAfterCheck: the server refused the AI's first answer and the
	// proposal holds its second, written after it saw the server's issues
	// (H-057's one repair); left out (false) otherwise.
	RevisedAfterCheck bool `json:"revised_after_check,omitzero"`
}

// GeneratedWebSearch is GeneratedBy's web_search: how many searches the
// call made and how many of the answer's sources the server did not keep.
type GeneratedWebSearch struct {
	Queries        int `json:"queries"`
	SourcesDropped int `json:"sources_dropped"`
}

// GeneratedSegmenter is GeneratedBy's segmenter.
type GeneratedSegmenter struct {
	Provider string `json:"provider"`
	Model    string `json:"model,omitzero"`
	Objects  int    `json:"objects"`
	// Boxes are the objects themselves (H-052), when the stored ones pass
	// boxesOf's checks; nil otherwise.
	Boxes []SegmentBox `json:"boxes,omitzero"`
}

// SegmentBox is common.schema.json's SegmentBox (H-052): one object the
// segmenter found, by the concept that found it, its box's corners in the
// source image's pixels (inclusive) and the segmenter's confidence.
type SegmentBox struct {
	Concept    string  `json:"concept"`
	X1         int     `json:"x1"`
	Y1         int     `json:"y1"`
	X2         int     `json:"x2"`
	Y2         int     `json:"y2"`
	Confidence float64 `json:"confidence"`
}

// boxesOf are a photo proposal's stored boxes (H-052: the generator's
// segmenter.boxes) when there are as many as objects and every one passes
// the contract's checks again: a concept as the segmenter client takes one
// (provider.CheckConcepts), corners in order inside the stored image's
// frame (image.width and image.height, MinSide to MaxSide), a confidence
// from 0 to 1; and the frame is the size of rec, the proposal's own image
// record (review round 1, L6: the boxes are drawn on that photo). Otherwise
// nil, so that part of the boxes is never shown: a proposal stored before
// the boxes were kept shows its object count only, and so does a row
// without a record (an analysis).
func boxesOf(raw, image jsontext.Value, objects int, rec *store.Image) []SegmentBox {
	if len(raw) == 0 || objects < 1 || objects > maxSegments || rec == nil {
		return nil
	}
	var frame struct {
		Width  int `json:"width"`
		Height int `json:"height"`
	}
	if json.Unmarshal(image, &frame) != nil || frame.Width < photo.MinSide || frame.Width > photo.MaxSide ||
		frame.Height < photo.MinSide || frame.Height > photo.MaxSide || frame.Width != rec.Width || frame.Height != rec.Height {
		return nil
	}
	var boxes []SegmentBox
	if json.Unmarshal(raw, &boxes) != nil || len(boxes) != objects {
		return nil
	}
	for _, b := range boxes {
		if provider.CheckConcepts([]string{b.Concept}) != nil || b.X1 < 0 || b.Y1 < 0 || b.X1 > b.X2 || b.Y1 > b.Y2 ||
			b.X2 >= frame.Width || b.Y2 >= frame.Height || !(b.Confidence >= 0 && b.Confidence <= 1) {
			return nil
		}
	}
	return boxes
}

var (
	providerName = regexp.MustCompile(`^[a-z][a-z0-9_-]{0,31}$`)
	modelName    = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._:-]{0,63}$`)
)

// maxSegments is D-131 (c)'s limit on a photo's objects.
const maxSegments = 64

// revisedOutcomes are the stored repair outcomes (internal/ai/repair) of a
// proposal that holds the AI's second answer: the server reviewed it,
// whatever it found. A failed repair call left the first answer's outcome.
var revisedOutcomes = map[string]bool{"ready": true, "needs_input": true, "invalid": true}

// maxQueries bounds the queries GeneratedBy shows (common.schema.json):
// a count outside it leaves web_search out rather than shown.
const maxQueries = 10000

// FromMetadata is the GeneratedBy of a row's provider metadata (a proposal's
// or an analysis's), or nil when it names no provider the contract can show
// (a generating row's {} included). A model or segmenter outside the
// contract's patterns is left out rather than shown. It never shows boxes:
// they are a proposal's, drawn on its own photo (generatedBy).
func FromMetadata(metadata []byte) *GeneratedBy {
	return generatedBy(metadata, nil)
}

// generatedBy is FromMetadata for a row whose image record is rec (nil for
// none): a photo proposal's shows its boxes too (boxesOf).
func generatedBy(metadata []byte, rec *store.Image) *GeneratedBy {
	var m struct {
		Provider string `json:"provider"`
		Model    string `json:"model"`
		// Image and Segmenter.Boxes are read only by boxesOf, so boxes it
		// refuses never hide the rest.
		Image     jsontext.Value `json:"image"`
		Segmenter *struct {
			Provider string         `json:"provider"`
			Model    string         `json:"model"`
			Segments int            `json:"segments"`
			Boxes    jsontext.Value `json:"boxes"`
		} `json:"segmenter"`
		WebSearch *provider.WebSearch `json:"web_search"`
		// Repair is the generator's record of the one repair (H-057); only
		// its outcome is read.
		Repair *struct {
			Outcome string `json:"outcome"`
		} `json:"repair"`
	}
	if len(metadata) == 0 || json.Unmarshal(metadata, &m) != nil || !providerName.MatchString(m.Provider) {
		return nil
	}
	out := &GeneratedBy{Provider: m.Provider}
	if r := m.Repair; r != nil && revisedOutcomes[r.Outcome] {
		out.RevisedAfterCheck = true
	}
	if modelName.MatchString(m.Model) {
		out.Model = m.Model
	}
	if s := m.Segmenter; s != nil && providerName.MatchString(s.Provider) && s.Segments >= 0 && s.Segments <= maxSegments {
		out.Segmenter = &GeneratedSegmenter{Provider: s.Provider, Objects: s.Segments, Boxes: boxesOf(s.Boxes, m.Image, s.Segments, rec)}
		if modelName.MatchString(s.Model) {
			out.Segmenter.Model = s.Model
		}
	}
	if w := m.WebSearch; w != nil && w.Queries >= 0 && w.Queries <= maxQueries && w.SourcesDropped >= 0 && w.SourcesDropped <= provider.MaxSources {
		out.WebSearch = &GeneratedWebSearch{Queries: w.Queries, SourcesDropped: w.SourcesDropped}
	}
	return out
}

// SourcesOf are the sources a row's provider metadata keeps (H-046 (2):
// "sources", written by the generator from provider.FilterSources), checked
// again as the contract states them: only when the resource shows the web
// search (FromMetadata's generated_by.web_search, by the same checks, so
// proposal.schema.json's "sources only with generated_by.web_search" holds;
// review round 1, L1), at most provider.MaxSources, each URL as
// provider.SourceURL writes it and each title as provider.SourceTitle does;
// anything else is left out rather than shown.
func SourcesOf(metadata []byte) []provider.WebSource {
	if g := FromMetadata(metadata); g == nil || g.WebSearch == nil {
		return nil
	}
	var m struct {
		Sources []provider.WebSource `json:"sources"`
	}
	if json.Unmarshal(metadata, &m) != nil {
		return nil
	}
	var out []provider.WebSource
	for _, s := range m.Sources {
		if len(out) == provider.MaxSources {
			break
		}
		if s.URL == "" || provider.SourceURL(s.URL) != s.URL || provider.SourceTitle(s.Title) != s.Title {
			continue
		}
		out = append(out, s)
	}
	return out
}

// SourceImage is proposal.schema.json's source_image: what the server
// stored of the photo, never its key or the upload's name.
type SourceImage struct {
	MediaType   string `json:"media_type"`
	Width       int    `json:"width"`
	Height      int    `json:"height"`
	SizeBytes   int64  `json:"size_bytes"`
	ContentHash string `json:"content_hash"`
}

func nonNil[T any](s []T) []T {
	if s == nil {
		return []T{}
	}
	return s
}

// Public is the resource of row p: what its state holds, and nothing the
// provider wrote that the state does not keep. A failed proposal's error is
// its job's failure in the contract's envelope, under the request that
// created it (requestID only if that is unknown).
func Public(p store.Proposal, requestID string) (Proposal, error) {
	out := Proposal{ProposalID: p.ID, OrganizationID: p.OrganizationID, ModelID: p.ModelID, BaseVersionID: p.BaseVersionID,
		Mode: p.Mode, Status: string(p.Status), CreatedBy: p.CreatedBy, CreatedAt: timestamp(p.CreatedAt), RequiresConfirmation: true}
	if im := p.Image; im != nil {
		out.SourceImage = &SourceImage{MediaType: im.MediaType, Width: im.Width, Height: im.Height, SizeBytes: im.Size, ContentHash: im.SHA256}
	}
	// The model the request named (H-065), when it is one of the contract's:
	// anything else the job's payload holds is left out rather than shown.
	if slices.Contains(ProposalModels, p.RequestedModel) {
		out.RequestedModel = p.RequestedModel
	}
	// Which model answered, once there is an answer: not while generating,
	// and not for a failure, whose resource holds only its error (AI_SPEC §4).
	if p.Status != store.Generating && p.Status != store.Failed {
		out.GeneratedBy = generatedBy(p.ProviderMetadata, p.Image)
	}
	needsInput := func() {
		out.Summary, out.Assumptions, out.Questions = p.Summary, nonNil(p.Assumptions), p.Questions
		out.Sources = SourcesOf(p.ProviderMetadata)
	}
	reviewed := func() error {
		changes, err := hashing.Canonical(p.Patch)
		if err != nil {
			return fmt.Errorf("proposal %s: stored patch: %w", p.ID, err)
		}
		validation, err := hashing.Canonical(p.Validation)
		if err != nil {
			return fmt.Errorf("proposal %s: stored validation: %w", p.ID, err)
		}
		out.Summary, out.Assumptions, out.ExpectedEffects = p.Summary, nonNil(p.Assumptions), nonNil(p.ExpectedEffects)
		out.Changes, out.PatchHash, out.Validation, out.Confidence = changes, p.PatchHash, validation, p.Confidence
		out.ServerRiskLevel, out.ProviderRiskClaim, out.RiskLevel = p.ServerRisk, p.ProviderRisk, p.Risk
		out.DatasetBindings = nonNil(p.DatasetBindings)
		out.Sources = SourcesOf(p.ProviderMetadata)
		return nil
	}
	// decision is the record of who decided and when (0001 and 0013 require
	// both of every decided proposal), with the comment unless the decision
	// was a conflict, which has none.
	decision := func(comment bool) error {
		if p.DecidedBy == nil || p.DecidedAt == nil {
			return fmt.Errorf("proposal %s: %s without its decision record", p.ID, p.Status)
		}
		at := timestamp(*p.DecidedAt)
		out.DecidedBy, out.DecidedAt = p.DecidedBy, &at
		if comment {
			out.DecisionComment = p.DecisionComment
		}
		return nil
	}
	switch p.Status {
	case store.Generating:
	case store.NeedsInput:
		needsInput()
	case store.Ready:
		if err := reviewed(); err != nil {
			return Proposal{}, err
		}
	case store.Conflicted:
		if err := reviewed(); err != nil {
			return Proposal{}, err
		}
		if err := decision(false); err != nil {
			return Proposal{}, err
		}
	case store.Approved:
		if err := reviewed(); err != nil {
			return Proposal{}, err
		}
		if err := decision(true); err != nil {
			return Proposal{}, err
		}
		out.NewVersionID, out.ScenarioID = p.NewVersionID, p.ScenarioID
	case store.Rejected:
		// Rejected after review (it has changes) or after asking for
		// input (it has questions).
		if p.Patch != nil {
			if err := reviewed(); err != nil {
				return Proposal{}, err
			}
		} else {
			needsInput()
		}
		if err := decision(true); err != nil {
			return Proposal{}, err
		}
	case store.Invalid:
		validation, err := hashing.Canonical(p.Validation)
		if err != nil {
			return Proposal{}, fmt.Errorf("proposal %s: stored validation: %w", p.ID, err)
		}
		out.Validation = validation
	case store.Failed:
		id := p.RequestID
		if id == "" {
			id = requestID
		}
		e, err := failureError(p.Failure, id)
		if err != nil {
			return Proposal{}, fmt.Errorf("proposal %s: %w", p.ID, err)
		}
		out.Error = e
	default:
		return Proposal{}, fmt.Errorf("proposal %s: unknown status %q", p.ID, p.Status)
	}
	return out, nil
}

// listed is p as listProposals shows it (H-052 review round 1, L7): without
// the photo's boxes, which only the proposal's own page draws (getProposal);
// the segmenter's provider, model and object count stay.
func listed(p Proposal) Proposal {
	if g := p.GeneratedBy; g != nil && g.Segmenter != nil && g.Segmenter.Boxes != nil {
		gb, s := *g, *g.Segmenter
		s.Boxes = nil
		gb.Segmenter = &s
		p.GeneratedBy = &gb
	}
	return p
}

// ErrorEnvelope is common.schema.json#/$defs/Error.
type ErrorEnvelope struct {
	Error ErrorBody `json:"error"`
}

// ErrorBody is ErrorEnvelope.error.
type ErrorBody struct {
	Code      string        `json:"code"`
	Message   string        `json:"message"`
	Details   []ErrorDetail `json:"details"`
	RequestID string        `json:"request_id"`
}

// ErrorDetail is one ErrorBody.details entry.
type ErrorDetail struct {
	Path   string `json:"path"`
	Reason string `json:"reason"`
	Code   string `json:"code,omitzero"`
}

var failureCode = regexp.MustCompile(`^[A-Z][A-Z0-9_]{0,63}$`)

// errorCodes maps a proposal job's failure code to the contract's closed
// ErrorCode list (CB-40 (c), D-061 PROPOSED); the failure's own code stays
// in details[0].code. The provider client's codes (internal/ai/provider)
// are the job's failure codes with an AI_ prefix, upper case
// (internal/proposals/generator).
var errorCodes = map[string]string{
	"AI_PROVIDER_NOT_CONFIGURED": "AI_PROVIDER_NOT_CONFIGURED",
	"AI_POLICY_NOT_APPROVED":     "AI_POLICY_NOT_APPROVED",
	"AI_RATE_LIMITED":            "RATE_LIMITED",
	"AI_CONCURRENCY_LIMITED":     "RATE_LIMITED",
	"AI_TIMEOUT":                 "SERVICE_UNAVAILABLE",
	"AI_UNAVAILABLE":             "SERVICE_UNAVAILABLE",
	"AI_CREDENTIALS_EXHAUSTED":   "SERVICE_UNAVAILABLE",
	"DATASET_STORAGE_FAILED":     "SERVICE_UNAVAILABLE",
	jobs.LeaseExpired:            "SERVICE_UNAVAILABLE",
	"WORKER_STOPPED":             "SERVICE_UNAVAILABLE",
	"AI_CONTEXT_TOO_LARGE":       "PAYLOAD_TOO_LARGE",
	// T-053 (D-111, PROPOSED): a spend limit refused the call before it was
	// made (try again when the day or month ends); the ledger could not
	// reserve for it.
	"AI_QUOTA_EXCEEDED":    "RATE_LIMITED",
	"AI_SPEND_UNAVAILABLE": "SERVICE_UNAVAILABLE",
	// T-056: the photo pipeline. The stored image could not be read
	// (retried); the model's domain takes no photos (the request is not
	// acceptable for it); the configured provider sends no images (image
	// input is not configured). PHOTO_CORRUPT (the stored bytes are not
	// the record's) is the server's: INTERNAL_ERROR.
	"PHOTO_STORAGE_FAILED": "SERVICE_UNAVAILABLE",
	"PHOTO_NOT_SUPPORTED":  "REQUEST_VALIDATION_FAILED",
	"AI_IMAGE_UNSUPPORTED": "AI_PROVIDER_NOT_CONFIGURED",
	// H-046 (2): the configured provider does not research (the API
	// refuses such a request first; a worker set up otherwise refuses it).
	"AI_RESEARCH_UNSUPPORTED": "AI_PROVIDER_NOT_CONFIGURED",
	// H-049: the configured provider may not ask the model the person
	// chose (the API refuses such a request first).
	"AI_MODEL_NOT_APPROVED": "AI_POLICY_NOT_APPROVED",
}

// FailureError is failureError for the other AI jobs' resources (T-030's
// analyses), so the code table above is the only one.
func FailureError(failure []byte, requestID string) (*ErrorEnvelope, error) {
	return failureError(failure, requestID)
}

// failureError shows a stored job failure as the contract's error. A code
// the table does not name (AI_REFUSED, AI_OUTPUT_TRUNCATED,
// AI_REQUEST_REJECTED, AI_INVALID_REQUEST, PROPOSAL_INPUT_INVALID,
// PROPOSAL_OUTPUT_UNSTORABLE, INTERNAL, NO_RESULT, JOB_CANCELLED...) is
// INTERNAL_ERROR.
func failureError(failure []byte, requestID string) (*ErrorEnvelope, error) {
	var f jobs.Failure
	if err := json.Unmarshal(failure, &f); err != nil || !failureCode.MatchString(f.Code) {
		return nil, fmt.Errorf("the stored failure is not a job failure")
	}
	message := f.Message
	if message == "" {
		message = "the proposal could not be generated"
	}
	code, ok := errorCodes[f.Code]
	if !ok {
		code = "INTERNAL_ERROR"
	}
	return &ErrorEnvelope{Error: ErrorBody{Code: code, Message: message, RequestID: requestID,
		Details: []ErrorDetail{{Path: "", Reason: message, Code: f.Code}}}}, nil
}
