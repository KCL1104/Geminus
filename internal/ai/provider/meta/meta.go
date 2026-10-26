// Package meta is the Meta Model API backend (T-055; H-029, H-033): Muse
// Spark for AI proposals through the API's Anthropic-compatible Messages
// endpoint, and SAM 3.1 for the photo path's segmentation (T-056) through
// its Responses endpoint, both over the standard library's HTTP client: no
// SDK (Meta ships none for Go; https://dev.meta.ai/docs/sdks), no
// dependency, no environment default read.
//
// Sources, all retrieved 2026-10-26 (the report checks/T-055_VALIDATION.md
// quotes them): https://dev.meta.ai/docs/protocols/messages,
// /docs/api-reference/messages/schemas, /docs/structured-output,
// /docs/image-understanding, /docs/reasoning, /docs/error-handling,
// /docs/pricing-rate-limits, /docs/authentication, /docs/models,
// /docs/sam/overview, /docs/sam/segmenting, /docs/sam/reading-segmentation,
// /docs/api-reference/responses/schemas, and the Meta Model API Terms of
// Service, Data Commitments and Model Designation Policy under
// https://dev.meta.ai/legal.
//
// It is built only from an approved live configuration: New and
// NewSegmenter require a valid aiapproval.Approval of the meta provider
// (H-029, amended by H-033) for its own model, segmentation model and key
// label (meta: GEMINUS_MUSE_API_KEY). Both are provider.Billable /
// provider.BillableSegmenter: only a metered client calls them, and they
// refuse a call without a spend reservation, and a role (or an image) the
// approval's data policy does not name, before any I/O.
//
// As implemented (PROPOSED D-121):
//   - The base URL is the constant https://api.meta.ai/; only the tests
//     change it (export_test.go). The key goes as a bearer token only, and
//     never with a redirect (none is followed).
//   - One key, so nothing to fail over to (D-018's two organizations are
//     Anthropic's): 401 and 403 disable it until the process restarts, 402
//     rests it for billingCooldown; both are credentials_exhausted. 429 and
//     the 5xx but 504 are retried on it with backoff and Retry-After, never
//     past the call's deadline; 504 is not retried; other 4xx are rejected.
//   - What may have been billed is counted (provider.Response.UnknownAttempts):
//     a lost connection, an attempt's timeout, a body cut short, every 5xx
//     (the docs do not say an error status is free), and an answer that
//     cannot be read (Undecoded: at least the whole reservation).
//   - Standard mode is the model ID (Model Designation Policy), and the
//     Terms of Service say "the system may route you to a Contributor
//     Model": an answer that names another model than the one asked for is
//     refused (invalid_output), its usage settled all the same.
//   - Errors name the HTTP status and an error type from the documented
//     list (errorTypes), any other "other"; never the provider's message,
//     the key or the URL.
package meta

import (
	"context"
	"encoding/base64"
	"encoding/json/v2"
	"errors"
	"net/http"
	"regexp"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/kcl1104/geminus/internal/ai/provider"
	"github.com/kcl1104/geminus/internal/ai/provider/structured"
	"github.com/kcl1104/geminus/internal/config/aiapproval"
	"github.com/kcl1104/geminus/internal/model"
	"github.com/kcl1104/geminus/internal/secret"
)

// productionURL is the provider's API ("Base URL | https://api.meta.ai",
// https://dev.meta.ai/docs/protocols/messages). Tests use a local server
// instead (export_test.go); no configuration changes it.
const productionURL = "https://api.meta.ai/"

const (
	// billingCooldown is how long a key refused with 402 rests before it
	// is tried again (ADR-027's rule for Anthropic's keys).
	billingCooldown = 15 * time.Minute
	// quoteOverheadTokens is the quote's allowance for what the API adds
	// of its own (D-111's rule, PROPOSED): "a short text-only request is
	// ~110 input tokens" (/docs/image-understanding), and the tokens Meta
	// injects are "not billed" (/docs/pricing-rate-limits).
	quoteOverheadTokens = 4096
	// contextWindowTokens is Muse Spark's context window ("have a
	// 1,048,576-token context window", /docs/models): no request is billed
	// for more input ("input_tokens + max_output_tokens must fit within
	// it", /docs/error-handling), so it bounds what an image costs, which
	// Meta documents no other bound for (image cost "scales with
	// resolution", the model "may resize images").
	contextWindowTokens = 1_048_576
	// maxResponseBytes bounds a response body the adapter reads; a larger
	// answer is not read (it may have been billed: Undecoded).
	maxResponseBytes = 8 << 20
)

// ErrNotApproved: New or NewSegmenter was given no recorded, complete
// approval of the meta provider (or the record changed after the token was
// made). It is checked first.
var ErrNotApproved = errors.New("meta: live AI is not approved (H-029, H-033)")

// Config is an approved live configuration.
type Config struct {
	// Approval is the owner's recorded approval (aiapproval.Recorded): a
	// meta one.
	Approval aiapproval.Approval
	// Model is the approval's model (New) or segmentation model
	// (NewSegmenter); there is no default.
	Model string
	// Credential is the one key the approval covers.
	Credential Credential
}

// Credential is the API key and its label.
type Credential struct {
	Label string
	Key   secret.Secret
}

// approved checks what New and NewSegmenter share: a valid meta approval
// whose one credential label is cfg's.
func approved(cfg Config) error {
	a := cfg.Approval
	if !a.Valid() || a.Provider() != "meta" {
		return ErrNotApproved
	}
	if !slices.Equal(a.Credentials(), []string{cfg.Credential.Label}) {
		return errors.New("meta: the credential is not the one the approval covers")
	}
	return nil
}

// Backend is the Muse Spark backend for AI proposals.
type Backend struct {
	model  string
	c      *conn
	allows func(role string) bool
	// images: the approval's data policy names images (demo images).
	images bool

	// project is structured.Project; only the tests change it
	// (export_test.go), to send a schema the projection never makes.
	project     func(file string) (structured.Projection, error)
	projections sync.Map // output schema -> projected
}

var (
	_ provider.Billable   = (*Backend)(nil)
	_ provider.ImageInput = (*Backend)(nil)
)

// New returns the Muse Spark backend for an approved configuration:
// cfg.Approval a recorded, complete meta approval, cfg.Model exactly its
// model, the credential its label.
func New(cfg Config) (*Backend, error) {
	if err := approved(cfg); err != nil {
		return nil, err
	}
	if cfg.Model != cfg.Approval.Model() {
		return nil, errors.New("meta: the model is not the approval's model")
	}
	return newBackend(cfg, productionURL, nil, cfg.Approval.AllowsRole, cfg.Approval.AllowsImages())
}

// newBackend checks the rest of cfg. Only New, which checks the approval,
// and the tests' local server (export_test.go) call it.
func newBackend(cfg Config, baseURL string, client *http.Client, allows func(string) bool, images bool) (*Backend, error) {
	if strings.TrimSpace(cfg.Model) == "" {
		return nil, errors.New("meta: no model; the owner approves one")
	}
	c, err := newConn(cfg.Credential, baseURL, client)
	if err != nil {
		return nil, err
	}
	return &Backend{model: cfg.Model, c: c, allows: allows, images: images, project: structured.Project}, nil
}

func (b *Backend) Name() string { return "meta" }

// AcceptsImages: the approval's data policy names images (H-029, H-033:
// demo images).
func (b *Backend) AcceptsImages() bool { return b.images }

// projected is an output schema's projection and its size as sent.
type projected struct {
	structured.Projection
	size int
}

// projection is the schema's projection (the one T-053's adapter sends,
// internal/ai/provider/structured), checked against Meta's documented
// limits (SchemaViolations): one outside them is never sent.
func (b *Backend) projection(schema string) (projected, error) {
	if p, ok := b.projections.Load(schema); ok {
		return p.(projected), nil
	}
	p, err := b.project(schema)
	if err != nil {
		return projected{}, err
	}
	if v := SchemaViolations(p.Schema); len(v) > 0 {
		return projected{}, errors.New("meta: the projected schema is outside the documented limits")
	}
	data, err := json.Marshal(p.Schema, json.Deterministic(true))
	if err != nil {
		return projected{}, err
	}
	pr := projected{Projection: p, size: len(data)}
	b.projections.Store(schema, pr)
	return pr, nil
}

// Quote is the model and the most input tokens one attempt of req can be
// billed for: the prompt's and the projected schema's bytes (every token
// covers at least one byte) and quoteOverheadTokens; with an image, the
// whole context window (no documented bound on an image's tokens); never
// more than the context window.
func (b *Backend) Quote(req provider.Request) (provider.Quote, error) {
	proj, err := b.projection(req.OutputSchema)
	if err != nil {
		return provider.Quote{}, err
	}
	n := int64(len(req.Prompt.System)+len(req.Prompt.User())+proj.size) + quoteOverheadTokens
	if len(req.Prompt.Images) > 0 {
		n = contextWindowTokens
	}
	return provider.Quote{Model: b.model, MaxInputTokens: min(n, contextWindowTokens)}, nil
}

// Complete sends req as one Messages call. It refuses, before any I/O, a
// request without a spend reservation, a role the approval's data policy
// does not name and images it does not name.
func (b *Backend) Complete(ctx context.Context, req provider.Request) (provider.Response, error) {
	resp := provider.Response{Model: b.model, Credential: b.c.label}
	if req.Reservation == "" {
		return provider.Response{}, provider.NewError(provider.CodeInvalidRequest, 0, "")
	}
	if b.allows == nil || !b.allows(string(req.Prompt.Role)) || (len(req.Prompt.Images) > 0 && !b.images) {
		return provider.Response{}, provider.NewError(provider.CodePolicyNotApproved, 0, "")
	}
	proj, err := b.projection(req.OutputSchema)
	if err != nil {
		return provider.Response{}, provider.NewError(provider.CodeInvalidRequest, 0, "")
	}
	body, err := messagesBody(b.model, req, proj.Schema)
	if err != nil {
		return provider.Response{}, provider.NewError(provider.CodeInvalidRequest, 0, "")
	}
	t := &tally{}
	ex, failed, err := b.c.post(ctx, t, "v1/messages", body, req.AttemptTimeout, req.MaxRetries)
	resp.FailedOver = failed
	if err != nil {
		resp.UnknownAttempts, resp.Undecoded = t.unknown, t.undecoded
		return resp, err
	}
	msg, ok := decodeMessage(ex.body)
	if !ok {
		// An answer came back but cannot be read: it may have been billed,
		// for a usage nobody knows (T-053 review round 1, I1).
		t.unknown++
		t.undecoded = true
		resp.UnknownAttempts, resp.Undecoded = t.unknown, t.undecoded
		return resp, provider.NewError(provider.CodeUnavailable, 0, b.c.label)
	}
	out, err := b.answer(msg, resp, proj.Projection)
	out.UnknownAttempts, out.Undecoded = t.unknown, t.undecoded
	return out, err
}

// The Messages request (https://dev.meta.ai/docs/protocols/messages,
// "Request fields"): model, max_tokens, system as one text block, one user
// message of the prompt's images (base64 image blocks, first) and its text,
// and output_config.format, the projected schema. Nothing else: no tools,
// no thinking ("disabled" is a 400; the model's own default decides how
// much it reasons), no sampling, no metadata, no stop_sequences, top_k,
// container or inference_geo (each a 400), no cache_control (caching is
// automatic), no service_tier, no stream.
type messagesRequest struct {
	Model        string        `json:"model"`
	MaxTokens    int64         `json:"max_tokens"`
	System       []textBlock   `json:"system"`
	Messages     []userMessage `json:"messages"`
	OutputConfig outputConfig  `json:"output_config"`
}

type textBlock struct {
	Type string `json:"type"`
	Text string `json:"text"`
}

type imageBlock struct {
	Type   string      `json:"type"`
	Source imageSource `json:"source"`
}

type imageSource struct {
	Type      string `json:"type"`
	MediaType string `json:"media_type"`
	Data      string `json:"data"`
}

type userMessage struct {
	Role    string `json:"role"`
	Content []any  `json:"content"`
}

type outputConfig struct {
	Format jsonFormat `json:"format"`
}

type jsonFormat struct {
	Type   string         `json:"type"`
	Schema map[string]any `json:"schema"`
}

func messagesBody(modelID string, req provider.Request, schema map[string]any) ([]byte, error) {
	var content []any
	for _, im := range req.Prompt.Images {
		if im.Check() != nil {
			return nil, provider.ErrInvalidImage
		}
		content = append(content, imageBlock{Type: "image", Source: imageSource{Type: "base64", MediaType: im.MediaType,
			Data: base64.StdEncoding.EncodeToString(im.Data)}})
	}
	content = append(content, textBlock{Type: "text", Text: req.Prompt.User()})
	return json.Marshal(messagesRequest{Model: modelID, MaxTokens: req.MaxOutputTokens, System: []textBlock{{Type: "text", Text: req.Prompt.System}},
		Messages: []userMessage{{Role: "user", Content: content}}, OutputConfig: outputConfig{Format: jsonFormat{Type: "json_schema", Schema: schema}}},
		json.Deterministic(true))
}

// message is a Messages answer as this build reads it ("Anthropic
// Message", /docs/api-reference/messages/schemas); other members are
// ignored.
type message struct {
	ID         string         `json:"id"`
	Type       string         `json:"type"`
	Role       string         `json:"role"`
	Model      string         `json:"model"`
	Content    []contentBlock `json:"content"`
	StopReason string         `json:"stop_reason"`
	Usage      *usage         `json:"usage"`
}

type contentBlock struct {
	Type string `json:"type"`
	Text string `json:"text"`
}

type usage struct {
	InputTokens              int64 `json:"input_tokens"`
	OutputTokens             int64 `json:"output_tokens"`
	CacheCreationInputTokens int64 `json:"cache_creation_input_tokens"`
	CacheReadInputTokens     int64 `json:"cache_read_input_tokens"`
}

// decodeMessage reads a 200 answer: one strict JSON document (valid UTF-8,
// no duplicate member) that is an assistant message with a usage whose
// counts are not negative. Anything else cannot be read.
func decodeMessage(body []byte) (message, bool) {
	var m message
	if err := json.Unmarshal(body, &m); err != nil {
		return message{}, false
	}
	u := m.Usage
	if m.Type != "message" || m.Role != "assistant" || u == nil || u.InputTokens < 0 || u.OutputTokens < 0 ||
		u.CacheCreationInputTokens < 0 || u.CacheReadInputTokens < 0 {
		return message{}, false
	}
	return m, true
}

// requestID is a provider's ID kept for the audit: only of a token's shape.
var requestID = regexp.MustCompile(`^[A-Za-z0-9_.:-]{1,128}$`)

func safeID(id string) string {
	if requestID.MatchString(id) {
		return id
	}
	return ""
}

// answer takes the text of a complete message from the model asked for;
// reasoning blocks are dropped unread. The text's encoded places are
// decoded (structured.Projection.Decode, fail closed).
func (b *Backend) answer(m message, resp provider.Response, proj structured.Projection) (provider.Response, error) {
	resp.Usage = provider.Usage{InputTokens: m.Usage.InputTokens, OutputTokens: m.Usage.OutputTokens,
		CacheReadInputTokens: m.Usage.CacheReadInputTokens, CacheCreationInputTokens: m.Usage.CacheCreationInputTokens}
	resp.ProviderRequestID = safeID(m.ID)
	invalid := func(msg string) (provider.Response, error) {
		e := provider.NewError(provider.CodeInvalidOutput, 0, b.c.label)
		if msg != "" {
			e.Issues = []model.ValidationIssue{model.NewIssue(provider.OutputInvalid, "", msg, model.SeverityError)}
		}
		return resp, e
	}
	if m.Model != b.model {
		// Standard mode is the model ID; the Terms say a request may be
		// routed to a Contributor model. Refused, whatever it says.
		return invalid("the answer is not from the Standard-mode model the request named")
	}
	switch m.StopReason {
	case "end_turn":
	case "max_tokens":
		return resp, provider.NewError(provider.CodeTruncated, 0, b.c.label)
	case "refusal":
		return resp, provider.NewError(provider.CodeRefused, 0, b.c.label)
	default: // tool_use, pause_turn, stop_sequence (none was sent) or a reason this build does not know
		return invalid("")
	}
	var text strings.Builder
	for _, blk := range m.Content {
		switch blk.Type {
		case "text":
			text.WriteString(blk.Text)
		case "thinking", "redacted_thinking":
		default: // a tool call or a server tool: the request offered none
			return invalid("")
		}
	}
	decoded, issues := proj.Decode([]byte(text.String()))
	if len(issues) > 0 {
		e := provider.NewError(provider.CodeInvalidOutput, 0, b.c.label)
		e.Issues = issues
		return resp, e
	}
	resp.Text = decoded
	return resp, nil
}
