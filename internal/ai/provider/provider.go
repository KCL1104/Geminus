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
type ProposalContext struct {
	OrganizationID string
	Mode           Mode
	Prompt         Prompt
}

// MappingContext is a sanitized mapping suggestion request.
type MappingContext struct {
	OrganizationID string
	Prompt         Prompt
}

// AnalysisContext is a sanitized analysis request.
type AnalysisContext struct {
	OrganizationID string
	Prompt         Prompt
}

// Usage is the token accounting a provider reports.
type Usage struct {
	InputTokens              int64 `json:"input_tokens"`
	OutputTokens             int64 `json:"output_tokens"`
	CacheReadInputTokens     int64 `json:"cache_read_input_tokens,omitzero"`
	CacheCreationInputTokens int64 `json:"cache_creation_input_tokens,omitzero"`
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
}

type ProposalResult struct {
	Output   ProposalOutput
	Raw      jsontext.Value // the schema-valid answer, untrusted
	Metadata Metadata
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
}

// Response is a Backend's answer: untrusted text and accounting.
type Response struct {
	Text              []byte
	Model             string
	Credential        string   // label only
	FailedOver        []string // labels refused (401/402/403) during this call
	Usage             Usage
	ProviderRequestID string
}

// Backend is one provider mode. Complete returns *Error for every failure
// it can classify and honours ctx.
type Backend interface {
	Name() string
	Complete(ctx context.Context, req Request) (Response, error)
}

// Client checks every call the same way, whatever the backend: the
// prompt is sealed and fits the budget, the organization has a free slot,
// the call ends within the timeout, and the answer is one JSON document
// valid under the role's full output schema.
type Client struct {
	backend Backend
	budget  Budget
	mu      sync.Mutex
	active  map[string]int
}

// New returns a Client for b under budget.
func New(b Backend, budget Budget) (*Client, error) {
	if b == nil {
		return nil, errors.New("ai provider: no backend")
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

// call runs one checked request and returns the schema-valid answer.
func (c *Client) call(ctx context.Context, org string, role Role, p Prompt, out any) (jsontext.Value, Metadata, error) {
	if err := ctx.Err(); err != nil {
		return nil, Metadata{}, NewError(CodeCancelled, 0, "").WithCause(err)
	}
	if d, ok := c.backend.(Disabled); ok {
		return nil, Metadata{}, d.err()
	}
	if role == "" || !organizationID.MatchString(org) || p.OrganizationID != org || p.Role != role || p.Verify() != nil {
		return nil, Metadata{}, invalidRequest()
	}
	if len(p.System)+len(p.User()) > c.budget.MaxPromptBytes {
		return nil, Metadata{}, NewError(CodeContextTooLarge, 0, "")
	}
	if !c.acquire(org) {
		return nil, Metadata{}, NewError(CodeConcurrencyLimited, 0, "")
	}
	defer c.release(org)

	meta := Metadata{Provider: c.backend.Name(), Role: role, TemplateVersion: p.TemplateVersion, OutputSchema: role.OutputSchema(),
		Locale: string(p.Locale), InputHash: p.InputHash, Context: p.Disclosure}
	callCtx, cancel := context.WithTimeout(ctx, c.budget.Timeout)
	defer cancel()
	start := time.Now()
	resp, err := c.backend.Complete(callCtx, Request{Prompt: p, OutputSchema: role.OutputSchema(),
		MaxOutputTokens: c.budget.MaxOutputTokens, AttemptTimeout: c.budget.AttemptTimeout, MaxRetries: c.budget.MaxRetries})
	meta.LatencyMillis = time.Since(start).Milliseconds()
	meta.Model, meta.Credential, meta.FailedOver = resp.Model, resp.Credential, resp.FailedOver
	meta.Usage, meta.ProviderRequestID = resp.Usage, resp.ProviderRequestID
	switch {
	case ctx.Err() != nil:
		return nil, meta, NewError(CodeCancelled, 0, resp.Credential).WithCause(ctx.Err())
	case err != nil && errors.Is(callCtx.Err(), context.DeadlineExceeded):
		return nil, meta, NewError(CodeTimeout, 0, resp.Credential).WithCause(context.DeadlineExceeded)
	case err != nil:
		var pe *Error
		if errors.As(err, &pe) {
			return nil, meta, pe
		}
		return nil, meta, NewError(CodeUnavailable, 0, resp.Credential)
	}

	invalid := func(issues ...model.ValidationIssue) (jsontext.Value, Metadata, error) {
		e := NewError(CodeInvalidOutput, 0, resp.Credential)
		e.Issues = issues
		return nil, meta, e
	}
	if len(resp.Text) == 0 || len(resp.Text) > c.budget.MaxOutputBytes {
		return invalid(model.NewIssue(OutputInvalid, "", "the answer is empty or larger than the output budget", model.SeverityError))
	}
	issues, err := validate.Document(resp.Text, role.OutputSchema(), OutputInvalid)
	if err != nil {
		return nil, meta, NewError(CodeInvalidOutput, 0, resp.Credential)
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
	return raw, meta, nil
}

// GenerateModelProposal asks for an edit or scenario proposal.
func (c *Client) GenerateModelProposal(ctx context.Context, in ProposalContext) (ProposalResult, error) {
	var out ProposalOutput
	raw, meta, err := c.call(ctx, in.OrganizationID, in.Mode.Role(), in.Prompt, &out)
	if err != nil {
		return ProposalResult{Metadata: meta}, err
	}
	return ProposalResult{Output: out, Raw: raw, Metadata: meta}, nil
}

// ProposeImportMapping asks for column mapping suggestions.
func (c *Client) ProposeImportMapping(ctx context.Context, in MappingContext) (MappingResult, error) {
	var out MappingOutput
	raw, meta, err := c.call(ctx, in.OrganizationID, RoleImporter, in.Prompt, &out)
	if err != nil {
		return MappingResult{Metadata: meta}, err
	}
	return MappingResult{Output: out, Raw: raw, Metadata: meta}, nil
}

// AnalyzeSimulation asks for an analysis of supplied results.
func (c *Client) AnalyzeSimulation(ctx context.Context, in AnalysisContext) (AnalysisResult, error) {
	var out AnalysisOutput
	raw, meta, err := c.call(ctx, in.OrganizationID, RoleAnalyst, in.Prompt, &out)
	if err != nil {
		return AnalysisResult{Metadata: meta}, err
	}
	return AnalysisResult{Output: out, Raw: raw, Metadata: meta}, nil
}
