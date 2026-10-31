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
	"bytes"
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
	// Prior is the model's answer as its backend took it, for one repair
	// (H-057, revision.go): present when the answer is the request's (the
	// model asked for, no web search it was not offered, within the output
	// budget) and either schema-valid or refused only for what it says (the
	// schema, or the backend's decoding of it: invalid_output). Absent for
	// every other failure. Withheld when printed; never store or log it.
	Prior PriorAnswer
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
	// Tools and Steps are a tool turn's (H-060, Client.Turn, tools.go): the
	// tools it offers and its transcript after the user message, checked.
	// Empty for every other request; only a ToolTurner's Turn is handed a
	// request that has them, never Complete.
	Tools []Tool
	Steps []ToolStep
}

// Response is a Backend's answer: untrusted text and accounting.
type Response struct {
	// Text is the answer as the backend decoded it (a projection's encoded
	// places decoded): what the client checks.
	Text []byte
	// Answer is the answer as the model wrote it: the text the backend took
	// as the answer, before decoding it (H-057). Untrusted, held only for one
	// repair (ProposalResult.Prior), never logged or stored. A backend sets
	// it whenever it took a complete answer's text, also when decoding that
	// text failed (invalid_output); nil when it took none.
	Answer            []byte
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
	// ToolCalls are a tool turn's calls as the answer made them (H-060,
	// ToolTurner.Turn), untrusted until the client checks them; Text is then
	// the answer's text (a plain answer, or what it wrote before its calls),
	// possibly empty. None for any other request.
	ToolCalls []ToolCall
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
// reserves before the call and settles after it (meter.go). A tool turn
// (tt, H-060; nil for every other call) is checked and metered the same way,
// sent through its backend's Turn, and its answer is checked against the
// tools it offered instead of an output schema (tools.go).
func (c *Client) call(ctx context.Context, org string, role Role, p Prompt, attr Attribution, markers []string, chosen string, out any, tt *toolTurn) (called, error) {
	if err := ctx.Err(); err != nil {
		return called{}, NewError(CodeCancelled, 0, "").WithCause(err)
	}
	if d, ok := c.backend.(Disabled); ok {
		return called{}, d.err()
	}
	if role == "" || !organizationID.MatchString(org) || p.OrganizationID != org || p.Role != role || p.Verify() != nil {
		return called{}, invalidRequest()
	}
	if c.meter != nil && !attr.complete() {
		return called{}, invalidRequest()
	}
	if tt != nil {
		// A tool turn (H-060) goes only to a backend that sends tools and may
		// for the role; no other backend ever sees it. It has no web search,
		// image or repair.
		t, ok := c.backend.(ToolTurner)
		if !ok {
			return called{}, NewError(CodeToolsUnsupported, 0, "")
		}
		if !t.AcceptsTools(role) {
			return called{}, NewError(CodePolicyNotApproved, 0, "")
		}
		if p.Research || len(p.Images) > 0 || p.Revision != nil {
			return called{}, invalidRequest()
		}
	}
	if len(p.Images) > 0 {
		// Images go only to a backend that sends them under its approval's
		// data policy (T-055); no other backend ever sees the call.
		if ii, ok := c.backend.(ImageInput); !ok || !ii.AcceptsImages() {
			return called{}, invalidRequest()
		}
	}
	if p.Research {
		// Web research goes only to a backend that can search and may for
		// the role (H-046 (2)); no other backend ever sees the call.
		r, ok := c.backend.(Researcher)
		if !ok {
			return called{}, NewError(CodeResearchUnsupported, 0, "")
		}
		if !r.AcceptsResearch(role) {
			return called{}, NewError(CodePolicyNotApproved, 0, "")
		}
	}
	if chosen != "" && !c.AcceptsModel(role, chosen) {
		// A model the backend cannot ask, or may not for the role (H-049):
		// never asked of another model instead.
		return called{}, NewError(CodeModelNotApproved, 0, "")
	}
	// A tool turn's tools and transcript count towards the prompt (H-060).
	toolBytes, schema := 0, role.OutputSchema()
	if tt != nil {
		toolBytes, schema = tt.size, ""
	}
	if len(p.System)+len(p.User())+toolBytes > c.budget.MaxPromptBytes {
		return called{}, NewError(CodeContextTooLarge, 0, "")
	}
	// H-057: a repair's second turn is bounded too: the prior answer by the
	// output budget the client took it under, the follow-up by its own
	// bound, so a repair is never refused for a size its first call passed.
	if r := p.Revision; r != nil && (r.Prior.Len() > c.budget.MaxOutputBytes || len(r.User()) > MaxRevisionBytes) {
		return called{}, NewError(CodeContextTooLarge, 0, "")
	}
	if !c.acquire(org) {
		return called{}, NewError(CodeConcurrencyLimited, 0, "")
	}
	defer c.release(org)

	meta := Metadata{Provider: c.backend.Name(), Role: role, TemplateVersion: p.TemplateVersion, OutputSchema: schema,
		Locale: string(p.Locale), InputHash: p.InputHash, Context: p.Disclosure}
	req := Request{Prompt: p, OutputSchema: schema, Model: chosen,
		MaxOutputTokens: c.budget.MaxOutputTokens, AttemptTimeout: c.budget.AttemptTimeout, MaxRetries: c.budget.MaxRetries}
	if tt != nil {
		// A tool turn's input hash covers its tools and transcript too.
		meta.InputHash, req.Tools, req.Steps = tt.hash, tt.tools, tt.steps
	}
	timeout := c.budget.Timeout
	if len(p.Images) > 0 {
		req.MaxOutputTokens, req.AttemptTimeout, timeout = ImageMaxOutputTokens, c.budget.ImageTimeout(req.AttemptTimeout), c.budget.ImageTimeout(timeout)
	}
	if p.Research || (p.Revision != nil && p.Revision.Researched) {
		// H-057: a research proposal's repair keeps its first call's output
		// bound and timeouts (its reasoning is not cut, H-055); it has no web
		// search, so no search loop a retry would bill again, and it keeps
		// the budget's retries.
		req.MaxOutputTokens, req.AttemptTimeout, timeout = ResearchMaxOutputTokens, c.budget.ResearchTimeout(req.AttemptTimeout), c.budget.ResearchTimeout(timeout)
	}
	if SlowModel(chosen) {
		// A slow model (H-065): its own output bound and timeouts on every
		// path, a photo, research and a repair included; the retries are the
		// call's own.
		req.MaxOutputTokens, req.AttemptTimeout, timeout = SlowModelMaxOutputTokens, SlowModelTimeout, SlowModelTimeout
	}
	if p.Research {
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
			return called{meta: meta}, err
		}
		req.Reservation = m.res.ID
	}
	start := time.Now()
	var resp Response
	var err error
	if tt != nil {
		resp, err = c.backend.(ToolTurner).Turn(callCtx, req)
	} else {
		resp, err = c.backend.Complete(callCtx, req)
	}
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
	// prior is the answer for a repair (H-057) when it is the request's own:
	// from the model asked for, with no web search it was not offered, within
	// the output budget. It goes with a schema-valid answer and with one
	// refused for what it says, never with any other failure.
	var prior PriorAnswer
	if tt == nil && len(resp.Answer) > 0 && len(resp.Answer) <= c.budget.MaxOutputBytes && (p.Research || resp.Research.None()) &&
		(req.Model == "" || c.meter == nil || resp.Model == req.Model) {
		prior = PriorAnswer{text: bytes.Clone(resp.Answer)}
	}
	switch {
	case ctx.Err() != nil:
		return called{meta: meta}, NewError(CodeCancelled, 0, resp.Credential).WithCause(ctx.Err())
	case err != nil && errors.Is(callCtx.Err(), context.DeadlineExceeded):
		return called{meta: meta}, NewError(CodeTimeout, 0, resp.Credential).WithCause(context.DeadlineExceeded)
	case err != nil:
		var pe *Error
		if errors.As(err, &pe) {
			if pe.Code == CodeInvalidOutput {
				// The backend took the answer's text and refused what it says
				// (an encoded place that does not decode): prior holds the text
				// only when the backend took one.
				return called{meta: meta, prior: prior}, pe
			}
			return called{meta: meta}, pe
		}
		return called{meta: meta}, NewError(CodeUnavailable, 0, resp.Credential)
	}

	// invalid refuses the answer; content says whether it was refused only
	// for what it says, so that a repair may send it back (H-057).
	invalid := func(content bool, issues ...model.ValidationIssue) (called, error) {
		e := NewError(CodeInvalidOutput, 0, resp.Credential)
		e.Issues = issues
		if !content {
			prior = PriorAnswer{}
		}
		return called{meta: meta, prior: prior}, e
	}
	if !p.Research && !resp.Research.None() {
		// A backend that searched for a call that did not ask: not an
		// answer this request may take (H-046 (2)).
		return invalid(false, model.NewIssue(OutputInvalid, "", "the answer used web search, which the request did not offer", model.SeverityError))
	}
	if req.Model != "" && c.meter != nil && resp.Model != req.Model {
		// A live answer from another model than the one the person chose
		// (H-049): never taken in its place (its usage is settled above).
		return invalid(false, model.NewIssue(OutputInvalid, "", "the answer is not from the model the request asked for", model.SeverityError))
	}
	if tt != nil {
		// A tool turn's answer (H-060): text, calls of the tools it offered,
		// or both, each call checked against its tool (tools.go).
		text, calls, issues := tt.answer(resp, c.budget)
		if len(issues) > 0 {
			return invalid(false, issues...)
		}
		r := called{meta: meta, turnText: text, turnCalls: calls}
		if h, err := turnOutputHash(text, calls); err == nil {
			r.meta.OutputHash = h
		}
		return r, nil
	}
	if len(resp.ToolCalls) > 0 {
		// A tool call in the answer of a request that offered no tool: not an
		// answer this request may take (H-060).
		return invalid(false, model.NewIssue(OutputInvalid, "", "the answer calls a tool, which the request did not offer", model.SeverityError))
	}
	if len(resp.Text) == 0 || len(resp.Text) > c.budget.MaxOutputBytes {
		return invalid(false, model.NewIssue(OutputInvalid, "", "the answer is empty or larger than the output budget", model.SeverityError))
	}
	issues, err := validate.Document(resp.Text, role.OutputSchema(), OutputInvalid)
	if err != nil {
		return called{meta: meta}, NewError(CodeInvalidOutput, 0, resp.Credential)
	}
	if len(issues) > 0 {
		// Keep where the answer is wrong, never what it says (AI_SPEC §4).
		clean := make([]model.ValidationIssue, 0, min(len(issues), 20))
		for _, is := range issues[:min(len(issues), 20)] {
			clean = append(clean, model.NewIssue(OutputInvalid, is.Path, "does not match "+role.OutputSchema(), model.SeverityError))
		}
		return invalid(true, clean...)
	}
	if err := unmarshalStrict(resp.Text, out); err != nil {
		// The schema and the Go types are kept equal by contract tests.
		return invalid(true, model.NewIssue(OutputInvalid, "", "does not decode", model.SeverityError))
	}
	raw := jsontext.Value(append([]byte(nil), resp.Text...))
	if h, err := hashing.Of(raw); err == nil {
		meta.OutputHash = h
	}
	return called{raw: raw, meta: meta, research: resp.Research, prior: prior}, nil
}

// called is what one checked call returned: the schema-valid answer, the
// call's metadata, a research call's web search, and the answer for a
// repair (H-057).
type called struct {
	raw      jsontext.Value
	meta     Metadata
	research Research
	prior    PriorAnswer
	// turnText and turnCalls are a tool turn's checked answer (H-060).
	turnText  string
	turnCalls []ToolCall
}

// GenerateModelProposal asks for an edit or scenario proposal. For a
// research call the answer's sources are kept only when the call opened
// them (FilterSources); a call without research keeps none, whatever the
// answer names. The result's Prior is the answer for one repair (H-057),
// with an invalid_output error too.
func (c *Client) GenerateModelProposal(ctx context.Context, in ProposalContext) (ProposalResult, error) {
	var out ProposalOutput
	r, err := c.call(ctx, in.OrganizationID, in.Mode.Role(), in.Prompt, in.Attribution, in.Markers, in.Model, &out, nil)
	meta := r.meta
	if err != nil {
		return ProposalResult{Metadata: meta, Prior: r.prior}, err
	}
	var kept []WebSource
	if meta.WebSearch != nil {
		var dropped int
		kept, dropped = FilterSources(out.Sources, r.research.Opened, in.Markers...)
		meta.WebSearch.SourcesKept, meta.WebSearch.SourcesDropped = len(kept), dropped
	}
	out.Sources = nil
	return ProposalResult{Output: out, Raw: r.raw, Metadata: meta, Sources: kept, Prior: r.prior}, nil
}

// ProposeImportMapping asks for column mapping suggestions.
func (c *Client) ProposeImportMapping(ctx context.Context, in MappingContext) (MappingResult, error) {
	var out MappingOutput
	r, err := c.call(ctx, in.OrganizationID, RoleImporter, in.Prompt, in.Attribution, nil, "", &out, nil)
	if err != nil {
		return MappingResult{Metadata: r.meta}, err
	}
	return MappingResult{Output: out, Raw: r.raw, Metadata: r.meta}, nil
}

// AnalyzeSimulation asks for an analysis of supplied results.
func (c *Client) AnalyzeSimulation(ctx context.Context, in AnalysisContext) (AnalysisResult, error) {
	var out AnalysisOutput
	r, err := c.call(ctx, in.OrganizationID, RoleAnalyst, in.Prompt, in.Attribution, nil, "", &out, nil)
	if err != nil {
		return AnalysisResult{Metadata: r.meta}, err
	}
	return AnalysisResult{Output: out, Raw: r.raw, Metadata: r.meta}, nil
}
