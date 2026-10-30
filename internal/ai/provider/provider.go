// Package provider is the LLM provider boundary (AI_SPEC §2, D-002, D-018):
// the three read-only operations an AI may perform, the sealed prompt they
// take, and a Client that every mode goes through. The modes are explicit:
// disabled (the default; every call fails with provider_not_configured or
// policy_not_approved), the deterministic fake (subpackage fake; tests and
// E2E) and the live adapter (subpackage anthropic; only after the owner's
// recorded approval of model, quota and data policy).
//
// A provider only suggests. Nothing here holds a database handle, a write
// service or a tool: the provider answers with untrusted JSON, the Client
// checks it against the full 2020-12 output schema and returns it, and the
// proposal and analysis services (T-025, T-030) validate it further. A
// schema-valid answer is not an authorized one (AGENTS.md §2).
package provider

import (
	"context"
	"encoding/json/jsontext"
	"errors"
	"regexp"
	"sync"
	"time"

	"github.com/kcl1104/geminus/internal/model"
	"github.com/kcl1104/geminus/internal/model/hashing"
	"github.com/kcl1104/geminus/internal/model/validate"
)

// LLMProvider is AI_SPEC §2's interface, refined: contexts are sealed
// prompts built by internal/ai/aicontext, results carry audit metadata.
type LLMProvider interface {
	GenerateModelProposal(context.Context, ProposalContext) (ProposalResult, error)
	ProposeImportMapping(context.Context, MappingContext) (MappingResult, error)
	AnalyzeSimulation(context.Context, AnalysisContext) (AnalysisResult, error)
}

// Mode is a proposal's mode, fixed by the server (D-004).
type Mode string

const (
	ModeEdit     Mode = "edit"
	ModeScenario Mode = "scenario"
)

// Role is the prompt role of the mode.
func (m Mode) Role() Role {
	switch m {
	case ModeEdit:
		return RoleModeler
	case ModeScenario:
		return RoleScenario
	}
	return ""
}

// ProposalContext is a sanitized proposal request for one organization.
// Attribution names what the call is for; a metered client requires it
// (T-053).
type ProposalContext struct {
	OrganizationID string
	Mode           Mode
	Prompt         Prompt
	Attribution    Attribution
	// Markers are the request's own identifiers (the organization's, the
	// model's, its version's and the proposal's IDs, the ID pool, the
	// model's distinctive entity IDs; internal/ai/aicontext) for a research
	// call (H-046 (2)): never sent. A source whose query string carries one
	// is dropped, and the steps that carry one are counted
	// (WebSearch.StepsWithIDs).
	Markers []string
	// Model is the model the person chose for the proposal (H-049), by its
	// exact ID; "" asks the backend's own model. Only a ModelChooser that
	// may ask it for the role is handed the call (model.go).
	Model string
}

// MappingContext is a sanitized mapping suggestion request.
type MappingContext struct {
	OrganizationID string
	Prompt         Prompt
	Attribution    Attribution
}

// AnalysisContext is a sanitized analysis request.
type AnalysisContext struct {
	OrganizationID string
	Prompt         Prompt
	Attribution    Attribution
}

// Usage is the token accounting a provider reports.
type Usage struct {
	InputTokens              int64 `json:"input_tokens"`
	OutputTokens             int64 `json:"output_tokens"`
	CacheReadInputTokens     int64 `json:"cache_read_input_tokens,omitzero"`
	CacheCreationInputTokens int64 `json:"cache_creation_input_tokens,omitzero"`
	// ThinkingTokens is the part of OutputTokens the model spent reasoning;
	// nil when the provider did not report it (not 0: AGENTS.md §2). Never
	// billed on top of OutputTokens.
	ThinkingTokens *int64 `json:"thinking_tokens,omitzero"`
}

// Metadata describes a call for the audit record and provider_metadata
// (AI_SPEC §10): no prompt, no answer text, no key; a credential by label.
type Metadata struct {
	Provider          string     `json:"provider"`
	Model             string     `json:"model,omitzero"`
	Credential        string     `json:"credential_label,omitzero"`
	FailedOver        []string   `json:"failed_over_from,omitzero"`
	Role              Role       `json:"role"`
	TemplateVersion   string     `json:"prompt_template_version"`
	OutputSchema      string     `json:"schema"`
	Locale            string     `json:"output_locale"`
	InputHash         string     `json:"input_hash"`
	OutputHash        string     `json:"output_hash,omitzero"`
	Usage             Usage      `json:"usage"`
	LatencyMillis     int64      `json:"latency_ms"`
	ProviderRequestID string     `json:"provider_request_id,omitzero"`
	Context           Disclosure `json:"context"`
	// The spend of a metered call (T-053): what it cost in nano-USD, the
	// reservation it was charged to, and whether settling the reservation
	// failed (the reservation then stays counted at its bound).
	CostNanoUSD      int64  `json:"cost_nano_usd,omitzero"`
	SpendReservation string `json:"spend_reservation_id,omitzero"`
	SpendUnsettled   bool   `json:"spend_unsettled,omitzero"`
	// InferenceGeo is the geo the answer was priced at (global, us, or
	// unknown when it named none this build knows: priced at the dearest);
	// SpendOverReservation that the call cost more than its reservation
	// held, which is recorded all the same (T-053 review round 1, M2, M3).
	InferenceGeo         string `json:"inference_geo,omitzero"`
	SpendOverReservation bool   `json:"spend_over_reservation,omitzero"`
	// WebSearch is what a research call's web search did (H-046 (2)):
	// counts only, never a query, a page or its URL; nil for a call
	// without research.
	WebSearch *WebSearch `json:"web_search,omitzero"`
}

type ProposalResult struct {
	// Output is the schema-valid answer; its Sources are always empty: the
	// sources kept are Sources.
	Output   ProposalOutput
	Raw      jsontext.Value // the schema-valid answer, untrusted
	Metadata Metadata
	// Sources are the answer's sources the research call opened
	// (FilterSources, H-046 (2)); none for a call without research.
	Sources []WebSource
}

type MappingResult struct {
	Output   MappingOutput
	Raw      jsontext.Value
	Metadata Metadata
}

type AnalysisResult struct {
	Output   AnalysisOutput
	Raw      jsontext.Value
	Metadata Metadata
}

// Request is what a Backend sends.
type Request struct {
	Prompt          Prompt
	OutputSchema    string // the canonical schema file; a backend may project it
	MaxOutputTokens int64
	AttemptTimeout  time.Duration
	MaxRetries      int
	// Reservation is the spend reservation a metered client made for the
	// call (T-053); a billable backend refuses a request without one.
	Reservation string
	// Model is the model the call asks for (H-049, ProposalContext.Model);
	// "" for the backend's own. Only a ModelChooser is sent one.
	Model string
}

// Response is a Backend's answer: untrusted text and accounting.
type Response struct {
	Text              []byte
	Model             string
	Credential        string   // label only
	FailedOver        []string // labels refused (401/402/403) during this call
	Usage             Usage    // what the answer that came back was billed for
	ProviderRequestID string
	// UnknownAttempts counts the attempts that ended without an answer
	// after the request may have reached the provider (a timeout, a lost
	// connection, an answer that could not be read): the provider may have
	// billed them (T-053).
	UnknownAttempts int
	// InferenceGeo is where the provider says the answer's inference ran
	// (usage.inference_geo), as the provider wrote it: untrusted, priced
	// through spend.Geo, never logged as it is.
	InferenceGeo string
	// Undecoded: an answer came back (a 2xx status) but could not be read,
	// so its usage is unknown; it is one of the UnknownAttempts, and the
	// call settles at its whole reservation at least (review round 1, I1).
	Undecoded bool
	// Research is what the answer's web search did (a research call,
	// H-046 (2)); zero for a call without research. Its tool calls are
	// billed (meter.go) even when the answer is refused.
	Research Research
}

// Backend is one provider mode. Complete returns *Error for every failure
// it can classify and honours ctx.
type Backend interface {
	Name() string
	Complete(ctx context.Context, req Request) (Response, error)
}

// ImageInput is a backend that sends a prompt's images (T-055): the
// client hands a prompt with images to no other backend, nor to one whose
// AcceptsImages is false (its approval's data policy names no images), and
// refuses the call before it (invalid_request).
type ImageInput interface {
	Backend
	AcceptsImages() bool
}

// Client checks every call the same way, whatever the backend: the
// prompt is sealed and fits the budget, the organization has a free slot,
// the call ends within the timeout, and the answer is one JSON document
// valid under the role's full output schema.
type Client struct {
	backend Backend
	// billable and meter are set for a billable backend (NewMetered).
	billable Billable
	meter    Meter
	budget   Budget
	mu       sync.Mutex
	active   map[string]int
}

// New returns a Client for b under budget. A billable backend needs a
// meter: NewMetered (T-053).
func New(b Backend, budget Budget) (*Client, error) {
	if b == nil {
		return nil, errors.New("ai provider: no backend")
	}
	if _, ok := b.(Billable); ok {
		return nil, errors.New("ai provider: a billable backend is only called through a meter (NewMetered)")
	}
	if err := budget.Validate(); err != nil {
		return nil, err
	}
	return &Client{backend: b, budget: budget, active: map[string]int{}}, nil
}

// Disabled is the backend of a deployment without live AI: every call
// fails with its code, before anything else is looked at.
type Disabled struct{ Code Code }

func (d Disabled) Name() string { return "disabled" }

func (d Disabled) Complete(context.Context, Request) (Response, error) {
	return Response{}, d.err()
}

func (d Disabled) err() *Error {
	if d.Code == CodePolicyNotApproved {
		return NewError(CodePolicyNotApproved, 0, "")
	}
	return NewError(CodeNotConfigured, 0, "")
}

var organizationID = regexp.MustCompile(`^org_[0-7][0-9A-HJKMNP-TV-Z]{25}$`)

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

func invalidRequest() *Error { return NewError(CodeInvalidRequest, 0, "") }

// call runs one checked request and returns the schema-valid answer and,
// for a research call, what its web search did. A metered client also
// reserves before the call and settles after it (meter.go).
func (c *Client) call(ctx context.Context, org string, role Role, p Prompt, attr Attribution, markers []string, chosen string, out any) (jsontext.Value, Metadata, Research, error) {
	if err := ctx.Err(); err != nil {
		return nil, Metadata{}, Research{}, NewError(CodeCancelled, 0, "").WithCause(err)
	}
	if d, ok := c.backend.(Disabled); ok {
		return nil, Metadata{}, Research{}, d.err()
	}
	if role == "" || !organizationID.MatchString(org) || p.OrganizationID != org || p.Role != role || p.Verify() != nil {
		return nil, Metadata{}, Research{}, invalidRequest()
	}
	if c.meter != nil && !attr.complete() {
		return nil, Metadata{}, Research{}, invalidRequest()
	}
	if len(p.Images) > 0 {
		// Images go only to a backend that sends them under its approval's
		// data policy (T-055); no other backend ever sees the call.
		if ii, ok := c.backend.(ImageInput); !ok || !ii.AcceptsImages() {
			return nil, Metadata{}, Research{}, invalidRequest()
		}
	}
	if p.Research {
		// Web research goes only to a backend that can search and may for
		// the role (H-046 (2)); no other backend ever sees the call.
		r, ok := c.backend.(Researcher)
		if !ok {
			return nil, Metadata{}, Research{}, NewError(CodeResearchUnsupported, 0, "")
		}
		if !r.AcceptsResearch(role) {
			return nil, Metadata{}, Research{}, NewError(CodePolicyNotApproved, 0, "")
		}
	}
	if chosen != "" && !c.AcceptsModel(role, chosen) {
		// A model the backend cannot ask, or may not for the role (H-049):
		// never asked of another model instead.
		return nil, Metadata{}, Research{}, NewError(CodeModelNotApproved, 0, "")
	}
	if len(p.System)+len(p.User()) > c.budget.MaxPromptBytes {
		return nil, Metadata{}, Research{}, NewError(CodeContextTooLarge, 0, "")
	}
	if !c.acquire(org) {
		return nil, Metadata{}, Research{}, NewError(CodeConcurrencyLimited, 0, "")
	}
	defer c.release(org)

	meta := Metadata{Provider: c.backend.Name(), Role: role, TemplateVersion: p.TemplateVersion, OutputSchema: role.OutputSchema(),
		Locale: string(p.Locale), InputHash: p.InputHash, Context: p.Disclosure}
	req := Request{Prompt: p, OutputSchema: role.OutputSchema(), Model: chosen,
		MaxOutputTokens: c.budget.MaxOutputTokens, AttemptTimeout: c.budget.AttemptTimeout, MaxRetries: c.budget.MaxRetries}
	timeout := c.budget.Timeout
	if SlowModel(chosen) {
		// A slow model (H-049): a research call's timeouts, at most D-007's
		// job timeout; the output bound and retries are the call's own.
		req.AttemptTimeout, timeout = c.budget.ResearchTimeout(req.AttemptTimeout), c.budget.ResearchTimeout(timeout)
	}
	if len(p.Images) > 0 {
		req.MaxOutputTokens, req.AttemptTimeout, timeout = ImageMaxOutputTokens, c.budget.ImageTimeout(req.AttemptTimeout), c.budget.ImageTimeout(timeout)
	}
	if p.Research {
		req.MaxOutputTokens, req.AttemptTimeout, timeout = ResearchMaxOutputTokens, c.budget.ResearchTimeout(req.AttemptTimeout), c.budget.ResearchTimeout(timeout)
		req.MaxRetries = ResearchMaxRetries
	}
	// One deadline for the whole call: a billable backend's quote (which may
	// ask the provider, H-037) and the call itself.
	callCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	var m *metering
	if c.meter != nil {
		var err error
		if m, err = c.reserve(ctx, callCtx, org, role, attr, req); err != nil {
			return nil, meta, Research{}, err
		}
		req.Reservation = m.res.ID
	}
	start := time.Now()
	resp, err := c.backend.Complete(callCtx, req)
	meta.LatencyMillis = time.Since(start).Milliseconds()
	if m != nil {
		c.settle(ctx, m, m.settlement(resp), &meta)
	}
	meta.Model, meta.Credential, meta.FailedOver = resp.Model, resp.Credential, resp.FailedOver
	meta.Usage, meta.ProviderRequestID = resp.Usage, resp.ProviderRequestID
	if p.Research {
		meta.WebSearch = &WebSearch{Queries: max(0, resp.Research.Queries), ToolCalls: max(0, resp.Research.ToolCalls),
			PagesOpened: pagesOpened(resp.Research.Opened), StepsWithIDs: StepsWithIDs(resp.Research, markers)}
	}
	switch {
	case ctx.Err() != nil:
		return nil, meta, Research{}, NewError(CodeCancelled, 0, resp.Credential).WithCause(ctx.Err())
	case err != nil && errors.Is(callCtx.Err(), context.DeadlineExceeded):
		return nil, meta, Research{}, NewError(CodeTimeout, 0, resp.Credential).WithCause(context.DeadlineExceeded)
	case err != nil:
		var pe *Error
		if errors.As(err, &pe) {
			return nil, meta, Research{}, pe
		}
		return nil, meta, Research{}, NewError(CodeUnavailable, 0, resp.Credential)
	}

	invalid := func(issues ...model.ValidationIssue) (jsontext.Value, Metadata, Research, error) {
		e := NewError(CodeInvalidOutput, 0, resp.Credential)
		e.Issues = issues
		return nil, meta, Research{}, e
	}
	if !p.Research && !resp.Research.None() {
		// A backend that searched for a call that did not ask: not an
		// answer this request may take (H-046 (2)).
		return invalid(model.NewIssue(OutputInvalid, "", "the answer used web search, which the request did not offer", model.SeverityError))
	}
	if req.Model != "" && c.meter != nil && resp.Model != req.Model {
		// A live answer from another model than the one the person chose
		// (H-049): never taken in its place (its usage is settled above).
		return invalid(model.NewIssue(OutputInvalid, "", "the answer is not from the model the request asked for", model.SeverityError))
	}
	if len(resp.Text) == 0 || len(resp.Text) > c.budget.MaxOutputBytes {
		return invalid(model.NewIssue(OutputInvalid, "", "the answer is empty or larger than the output budget", model.SeverityError))
	}
	issues, err := validate.Document(resp.Text, role.OutputSchema(), OutputInvalid)
	if err != nil {
		return nil, meta, Research{}, NewError(CodeInvalidOutput, 0, resp.Credential)
	}
	if len(issues) > 0 {
		// Keep where the answer is wrong, never what it says (AI_SPEC §4).
		clean := make([]model.ValidationIssue, 0, min(len(issues), 20))
		for _, is := range issues[:min(len(issues), 20)] {
			clean = append(clean, model.NewIssue(OutputInvalid, is.Path, "does not match "+role.OutputSchema(), model.SeverityError))
		}
		return invalid(clean...)
	}
	if err := unmarshalStrict(resp.Text, out); err != nil {
		// The schema and the Go types are kept equal by contract tests.
		return invalid(model.NewIssue(OutputInvalid, "", "does not decode", model.SeverityError))
	}
	raw := jsontext.Value(append([]byte(nil), resp.Text...))
	if h, err := hashing.Of(raw); err == nil {
		meta.OutputHash = h
	}
	return raw, meta, resp.Research, nil
}

// GenerateModelProposal asks for an edit or scenario proposal. For a
// research call the answer's sources are kept only when the call opened
// them (FilterSources); a call without research keeps none, whatever the
// answer names.
func (c *Client) GenerateModelProposal(ctx context.Context, in ProposalContext) (ProposalResult, error) {
	var out ProposalOutput
	raw, meta, research, err := c.call(ctx, in.OrganizationID, in.Mode.Role(), in.Prompt, in.Attribution, in.Markers, in.Model, &out)
	if err != nil {
		return ProposalResult{Metadata: meta}, err
	}
	var kept []WebSource
	if meta.WebSearch != nil {
		var dropped int
		kept, dropped = FilterSources(out.Sources, research.Opened, in.Markers...)
		meta.WebSearch.SourcesKept, meta.WebSearch.SourcesDropped = len(kept), dropped
	}
	out.Sources = nil
	return ProposalResult{Output: out, Raw: raw, Metadata: meta, Sources: kept}, nil
}

// ProposeImportMapping asks for column mapping suggestions.
func (c *Client) ProposeImportMapping(ctx context.Context, in MappingContext) (MappingResult, error) {
	var out MappingOutput
	raw, meta, _, err := c.call(ctx, in.OrganizationID, RoleImporter, in.Prompt, in.Attribution, nil, "", &out)
	if err != nil {
		return MappingResult{Metadata: meta}, err
	}
	return MappingResult{Output: out, Raw: raw, Metadata: meta}, nil
}

// AnalyzeSimulation asks for an analysis of supplied results.
func (c *Client) AnalyzeSimulation(ctx context.Context, in AnalysisContext) (AnalysisResult, error) {
	var out AnalysisOutput
	raw, meta, _, err := c.call(ctx, in.OrganizationID, RoleAnalyst, in.Prompt, in.Attribution, nil, "", &out)
	if err != nil {
		return AnalysisResult{Metadata: meta}, err
	}
	return AnalysisResult{Output: out, Raw: raw, Metadata: meta}, nil
}
