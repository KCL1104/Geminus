package proposals

import (
	"encoding/json/jsontext"
	"encoding/json/v2"
	"fmt"
	"regexp"

	"github.com/kcl1104/geminus/internal/model"
	"github.com/kcl1104/geminus/internal/model/hashing"
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
}

// GeneratedBy is common.schema.json's GeneratedBy (H-043): which provider
// and model wrote an AI result and, for a photo, which segmenter read it and
// how many objects it found. Only these come from the stored metadata:
// never a credential label, cost, reservation, request ID, hash or latency.
type GeneratedBy struct {
	Provider  string              `json:"provider"`
	Model     string              `json:"model,omitzero"`
	Segmenter *GeneratedSegmenter `json:"segmenter,omitzero"`
}

// GeneratedSegmenter is GeneratedBy's segmenter.
type GeneratedSegmenter struct {
	Provider string `json:"provider"`
	Model    string `json:"model,omitzero"`
	Objects  int    `json:"objects"`
}

var (
	providerName = regexp.MustCompile(`^[a-z][a-z0-9_-]{0,31}$`)
	modelName    = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._:-]{0,63}$`)
)

// maxSegments is D-131 (c)'s limit on a photo's objects.
const maxSegments = 64

// FromMetadata is the GeneratedBy of a row's provider metadata (a proposal's
// or an analysis's), or nil when it names no provider the contract can show
// (a generating row's {} included). A model or segmenter outside the
// contract's patterns is left out rather than shown.
func FromMetadata(metadata []byte) *GeneratedBy {
	var m struct {
		Provider  string `json:"provider"`
		Model     string `json:"model"`
		Segmenter *struct {
			Provider string `json:"provider"`
			Model    string `json:"model"`
			Segments int    `json:"segments"`
		} `json:"segmenter"`
	}
	if len(metadata) == 0 || json.Unmarshal(metadata, &m) != nil || !providerName.MatchString(m.Provider) {
		return nil
	}
	out := &GeneratedBy{Provider: m.Provider}
	if modelName.MatchString(m.Model) {
		out.Model = m.Model
	}
	if s := m.Segmenter; s != nil && providerName.MatchString(s.Provider) && s.Segments >= 0 && s.Segments <= maxSegments {
		out.Segmenter = &GeneratedSegmenter{Provider: s.Provider, Objects: s.Segments}
		if modelName.MatchString(s.Model) {
			out.Segmenter.Model = s.Model
		}
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
	// Which model answered, once there is an answer: not while generating,
	// and not for a failure, whose resource holds only its error (AI_SPEC §4).
	if p.Status != store.Generating && p.Status != store.Failed {
		out.GeneratedBy = FromMetadata(p.ProviderMetadata)
	}
	needsInput := func() {
		out.Summary, out.Assumptions, out.Questions = p.Summary, nonNil(p.Assumptions), p.Questions
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
