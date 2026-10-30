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
//   - A request with images is counted first (POST /v1/messages/count_tokens,
//     not billed) and reserved at the count and 10% (H-037); a count that
//     fails or cannot be trusted fails the call before anything is
//     reserved or sent. A text request is quoted from its bytes (D-111).
//   - Standard mode is the model ID (Model Designation Policy), and the
//     Terms of Service say "the system may route you to a Contributor
//     Model": an answer that names another model than the one asked for is
//     refused (invalid_output), its usage settled all the same.
//   - Errors name the HTTP status and an error type from the documented
//     list (errorTypes), any other "other"; never the provider's message,
//     the key or the URL.
//
// Research (H-046 (2); https://dev.meta.ai/docs/search-grounding and the
// Messages page's "Tools", retrieved 2026-10-29; the coordinator's probe of
// 2026-10-29): a research prompt of a role the approval names research for
// (aiapproval.Approval.AllowsResearch) is sent with the built-in tool
// {"type":"web_search"} and nothing else of it (no user_location: nothing
// says where the person is; allowed_domains, blocked_domains and max_uses
// are a 400). The provider searches, opens pages and finds text in them
// server-side within the one call; the answer lists each step as a
// server_tool_use block named web_search whose input says what it did
// ({"type":"search","query":...}, {"type":"open_page","url":...},
// {"type":"find_in_page","url":...,"pattern":...}), between narration text
// blocks, and no tool result block: a step says what the model asked the
// tool to do, never whether a page loaded or what it held. The adapter
// counts the steps (searches apart), keeps the URLs the model asked to open
// or search in for the client's source filter (provider.FilterSources) and
// the query texts for its identifier count (provider.StepsWithIDs), and
// takes the last text block, after the last step, as the answer; the
// narration is dropped unread. Any other tool, block or a step that is not web_search fails the
// answer (invalid_output), its usage and steps billed all the same. A
// research call is one attempt (provider.ResearchMaxRetries) with
// provider.ResearchMaxOutputTokens, reserved at the whole context window and
// researchMaxToolCalls steps (Quote): a reservation, not a ceiling (the
// attempt's timeout is the only hard limit). Nothing of a query, a page or a
// URL is logged or put in an error.
package meta

import (
	"context"
	"encoding/base64"
	"encoding/json/jsontext"
	"encoding/json/v2"
	"errors"
	"net/http"
	"regexp"
	"slices"
	"strconv"
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
	// it", /docs/error-handling), so no quote is larger, and no count of a
	// request is either (a larger one is not a count this build trusts).
	// Meta documents no other bound on an image's tokens (image cost
	// "scales with resolution", the model "may resize images"): a request
	// with images is counted (H-037).
	contextWindowTokens = 1_048_576
	// maxResponseBytes bounds a response body the adapter reads; a larger
	// answer is not read (it may have been billed: Undecoded).
	maxResponseBytes = 8 << 20
	// researchMaxToolCalls is the web search steps (searches, pages asked
	// for, finds in a page) one research attempt is reserved for (H-046
	// (2)). Meta documents no bound on its server-side loop (max_uses is a
	// 400), so this is an assumption, not a limit anyone keeps: the probe of
	// 2026-10-29 took 5 steps for a small question. A call that takes more
	// is settled at what it took (provider.Metadata.SpendOverReservation,
	// warned of in the log).
	researchMaxToolCalls = 64
	// webSearchTool is the built-in tool's type and the name of its steps
	// (server_tool_use.name).
	webSearchTool = "web_search"
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
	// research: the approval's data policy names web research for the role
	// (H-046 (2)); nil for none.
	research func(role string) bool

	// project is structured.Project; only the tests change it
	// (export_test.go), to send a schema the projection never makes.
	project     func(file string) (structured.Projection, error)
	projections sync.Map // output schema -> projected
}

var (
	_ provider.Billable   = (*Backend)(nil)
	_ provider.ImageInput = (*Backend)(nil)
	_ provider.Researcher = (*Backend)(nil)
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
	b, err := newBackend(cfg, productionURL, nil, cfg.Approval.AllowsRole, cfg.Approval.AllowsImages())
	if err != nil {
		return nil, err
	}
	b.research = cfg.Approval.AllowsResearch
	return b, nil
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

// AcceptsResearch: the approval's data policy names web research for role,
// a proposal role it serves (H-046 (2)).
func (b *Backend) AcceptsResearch(role provider.Role) bool {
	return role.Researches() && b.allows != nil && b.allows(string(role)) && b.research != nil && b.research(string(role))
}

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
// billed for, never more than the context window:
//
//   - a text request: the prompt's and the projected schema's bytes (every
//     token covers at least one byte) and quoteOverheadTokens (D-111 (a));
//     nothing is asked of the provider;
//   - a request with images (Meta documents no bound on an image's tokens):
//     the provider's count of it (count) and 10%, rounded up (H-037, the
//     owner's answer to T-055 review round 1, M1). A count that fails, or
//     whose answer is not a sane count, fails the quote, so the call is
//     never reserved for less, and nothing is sent.
//
// ctx is the call's (its deadline and the caller's cancellation).
func (b *Backend) Quote(ctx context.Context, req provider.Request) (provider.Quote, error) {
	proj, err := b.projection(req.OutputSchema)
	if err != nil {
		return provider.Quote{}, err
	}
	if req.Prompt.Research {
		// A research call (H-046 (2)): the pages the provider reads are
		// input tokens of the call that this build never sees before it is
		// billed, so no count of the request bounds them. One attempt is
		// reserved at the whole context window and researchMaxToolCalls
		// steps. This is NOT a ceiling: the context window bounds one
		// sampling step ("input_tokens + max_output_tokens must fit within
		// it", /docs/error-handling), but the loop's usage is summed over its
		// steps, each of which reads the context again (the probe's 41,356
		// input tokens for 5 steps), so one call can cost more than its
		// reservation, and more than an organization's daily cap. Meta states
		// no bound on how many steps the loop takes; the only hard limit is
		// the attempt's timeout (provider.Budget.ResearchTimeout). A call that
		// costs more is recorded at what it cost (docs/AI_SPEC.md §2).
		if !b.AcceptsResearch(req.Prompt.Role) {
			return provider.Quote{}, provider.NewError(provider.CodePolicyNotApproved, 0, "")
		}
		return provider.Quote{Model: b.model, MaxInputTokens: contextWindowTokens, MaxToolCalls: researchMaxToolCalls}, nil
	}
	if len(req.Prompt.Images) == 0 {
		n := int64(len(req.Prompt.System)+len(req.Prompt.User())+proj.size) + quoteOverheadTokens
		return provider.Quote{Model: b.model, MaxInputTokens: min(n, contextWindowTokens)}, nil
	}
	n, err := b.count(ctx, req, proj)
	if err != nil {
		return provider.Quote{}, err
	}
	return provider.Quote{Model: b.model, MaxInputTokens: min(n+(n+countMarginDivisor-1)/countMarginDivisor, contextWindowTokens), Counted: n}, nil
}

// countPath is the Messages endpoint's token count ("Endpoints | POST
// /v1/messages, POST /v1/messages/count_tokens", /docs/protocols/messages).
const countPath = "v1/messages/count_tokens"

// countMarginDivisor: a request with images is reserved at its count and
// a tenth of it, rounded up (H-037: "以該數加 10% 預留").
const countMarginDivisor = 10

// count asks the provider how many input tokens req is (H-037): POST
// /v1/messages/count_tokens with the body Complete sends but max_tokens
// ("Send the same body as POST /v1/messages minus the max_tokens
// requirement", /docs/protocols/messages; the count's schema has no
// max_tokens field). It goes through the call's connection and rules
// (conn.post): the key as a bearer token only, no redirect followed, the
// response bound, the call's deadline, the call's attempts and retries, the
// documented error types only, never the provider's text. It refuses
// first, before any I/O, what Complete refuses: a role or images the
// approval's data policy does not name.
//
// The count is not billed: the docs list no price for it, and the count
// endpoints tokenize only ("It does not generate output, execute hosted
// tools, create or store a response, or record generation usage",
// /docs/token-counting) while billing is by usage ("Model API bills text
// models per token", /docs/pricing-rate-limits). So none of its attempts
// is counted as one that may have been billed (PROPOSED D-121 (b); live:
// H-037's still open item).
func (b *Backend) count(ctx context.Context, req provider.Request, proj projected) (int64, error) {
	if b.allows == nil || !b.allows(string(req.Prompt.Role)) || !b.images {
		return 0, provider.NewError(provider.CodePolicyNotApproved, 0, "")
	}
	body, err := countBody(b.model, req, proj.Schema)
	if err != nil {
		return 0, provider.NewError(provider.CodeInvalidRequest, 0, "")
	}
	ex, _, err := b.c.post(ctx, &tally{}, countPath, body, req.AttemptTimeout, req.MaxRetries)
	if err != nil {
		return 0, err
	}
	n, ok := decodeCount(ex.body)
	if !ok {
		return 0, provider.NewError(provider.CodeUnavailable, 0, b.c.label)
	}
	return n, nil
}

// decodeCount reads a count answer ("AnthropicCountTokensResponse":
// input_tokens, an integer, required; /docs/api-reference/messages/schemas):
// one strict JSON document (valid UTF-8, no duplicate member) that is an
// object whose input_tokens is written as an integer from 1 to the context
// window (no fraction, no exponent, not a string). Other members are not
// read. Anything else is not a count this build trusts.
func decodeCount(body []byte) (int64, bool) {
	var c struct {
		InputTokens jsontext.Value `json:"input_tokens"`
	}
	if json.Unmarshal(body, &c) != nil {
		return 0, false
	}
	n, err := strconv.ParseInt(string(c.InputTokens), 10, 64)
	if err != nil || n < 1 || n > contextWindowTokens {
		return 0, false
	}
	return n, true
}

// Complete sends req as one Messages call. It refuses, before any I/O, a
// request without a spend reservation, a role the approval's data policy
// does not name and images it does not name.
func (b *Backend) Complete(ctx context.Context, req provider.Request) (provider.Response, error) {
	resp := provider.Response{Model: b.model, Credential: b.c.label}
	if req.Reservation == "" {
		return provider.Response{}, provider.NewError(provider.CodeInvalidRequest, 0, "")
	}
	if b.allows == nil || !b.allows(string(req.Prompt.Role)) || (len(req.Prompt.Images) > 0 && !b.images) ||
		(req.Prompt.Research && !b.AcceptsResearch(req.Prompt.Role)) {
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
	out, err := b.answer(msg, resp, proj.Projection, req.Prompt.Research)
	out.UnknownAttempts, out.Undecoded = t.unknown, t.undecoded
	return out, err
}

// The Messages request (https://dev.meta.ai/docs/protocols/messages,
// "Request fields"): model, max_tokens, system as one text block, one user
// message of the prompt's images (base64 image blocks, first) and its text,
// and output_config.format, the projected schema; for a research prompt
// (H-046 (2)) also tools, the one built-in web_search tool, without
// user_location or any other member. Nothing else: no other tool and no
// tool_choice (the model decides whether to search), no thinking
// ("disabled" is a 400; the model's own default decides how much it
// reasons), no sampling, no metadata, no stop_sequences, top_k, container or
// inference_geo (each a 400), no cache_control (caching is automatic), no
// service_tier, no stream.
type messagesRequest struct {
	Model        string        `json:"model"`
	MaxTokens    int64         `json:"max_tokens"`
	System       []textBlock   `json:"system"`
	Messages     []userMessage `json:"messages"`
	OutputConfig outputConfig  `json:"output_config"`
	Tools        []builtinTool `json:"tools,omitzero"`
}

// builtinTool is a built-in tool of the request: {"type":"web_search"}.
type builtinTool struct {
	Type string `json:"type"`
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

// countRequest is the count's body: the Messages request's fields but
// max_tokens ("AnthropicCountTokensRequest", /docs/api-reference/messages/
// schemas, has no max_tokens field).
type countRequest struct {
	Model        string        `json:"model"`
	System       []textBlock   `json:"system"`
	Messages     []userMessage `json:"messages"`
	OutputConfig outputConfig  `json:"output_config"`
}

func messagesOf(modelID string, req provider.Request, schema map[string]any) (messagesRequest, error) {
	var content []any
	for _, im := range req.Prompt.Images {
		if im.Check() != nil {
			return messagesRequest{}, provider.ErrInvalidImage
		}
		content = append(content, imageBlock{Type: "image", Source: imageSource{Type: "base64", MediaType: im.MediaType,
			Data: base64.StdEncoding.EncodeToString(im.Data)}})
	}
	content = append(content, textBlock{Type: "text", Text: req.Prompt.User()})
	m := messagesRequest{Model: modelID, MaxTokens: req.MaxOutputTokens, System: []textBlock{{Type: "text", Text: req.Prompt.System}},
		Messages: []userMessage{{Role: "user", Content: content}}, OutputConfig: outputConfig{Format: jsonFormat{Type: "json_schema", Schema: schema}}}
	if req.Prompt.Research {
		m.Tools = []builtinTool{{Type: webSearchTool}}
	}
	return m, nil
}

func messagesBody(modelID string, req provider.Request, schema map[string]any) ([]byte, error) {
	m, err := messagesOf(modelID, req, schema)
	if err != nil {
		return nil, err
	}
	return json.Marshal(m, json.Deterministic(true))
}

// countBody is the body of the count of req: exactly what messagesBody
// sends, but max_tokens.
func countBody(modelID string, req provider.Request, schema map[string]any) ([]byte, error) {
	m, err := messagesOf(modelID, req, schema)
	if err != nil {
		return nil, err
	}
	return json.Marshal(countRequest{Model: m.Model, System: m.System, Messages: m.Messages, OutputConfig: m.OutputConfig}, json.Deterministic(true))
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
	// Name and Input are a server_tool_use block's (a research call's web
	// search step, H-046 (2)); untrusted, never logged.
	Name  string         `json:"name"`
	Input jsontext.Value `json:"input"`
}

// toolStep is what a web search step's input says the model asked for, as
// this build reads it: the kind (search, open_page, find_in_page or
// another), the URL of the two that name one, and a search's query text.
// Its pattern is not read. None of it is logged.
type toolStep struct {
	Type  string `json:"type"`
	URL   string `json:"url"`
	Query string `json:"query"`
}

// research reads a research answer's web search steps: every
// server_tool_use block is one step (each priced as a query, an upper
// bound), a search one a query (its text kept for the identifier count),
// and an open_page or find_in_page one names a URL the model asked for (no
// tool result says whether it loaded). ok is false when a step is not the
// web_search tool's: the request offered no other.
func research(m message) (r provider.Research, ok bool) {
	ok = true
	for _, blk := range m.Content {
		if blk.Type != "server_tool_use" {
			continue
		}
		r.ToolCalls++
		if blk.Name != webSearchTool {
			ok = false
			continue
		}
		var st toolStep
		if json.Unmarshal(blk.Input, &st) != nil {
			continue
		}
		switch st.Type {
		case "search":
			r.Queries++
			if st.Query != "" {
				r.Searched = append(r.Searched, st.Query)
			}
		case "open_page", "find_in_page":
			if st.URL != "" {
				r.Opened = append(r.Opened, st.URL)
			}
		}
	}
	return r, ok
}

type usage struct {
	InputTokens              int64 `json:"input_tokens"`
	OutputTokens             int64 `json:"output_tokens"`
	CacheCreationInputTokens int64 `json:"cache_creation_input_tokens"`
	CacheReadInputTokens     int64 `json:"cache_read_input_tokens"`
	OutputTokensDetails      struct {
		ThinkingTokens *int64 `json:"thinking_tokens"` // optional in the schema
	} `json:"output_tokens_details"`
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
		u.CacheCreationInputTokens < 0 || u.CacheReadInputTokens < 0 ||
		(u.OutputTokensDetails.ThinkingTokens != nil && (*u.OutputTokensDetails.ThinkingTokens < 0 || *u.OutputTokensDetails.ThinkingTokens > u.OutputTokens)) {
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
// decoded (structured.Projection.Decode, fail closed). A research answer
// (H-046 (2)) has its web search steps read first, so they are billed
// whatever becomes of the answer, and its answer is its last text block,
// which must come after its last step; the narration before is dropped
// unread.
func (b *Backend) answer(m message, resp provider.Response, proj structured.Projection, researched bool) (provider.Response, error) {
	resp.Usage = provider.Usage{InputTokens: m.Usage.InputTokens, OutputTokens: m.Usage.OutputTokens,
		CacheReadInputTokens: m.Usage.CacheReadInputTokens, CacheCreationInputTokens: m.Usage.CacheCreationInputTokens,
		ThinkingTokens: m.Usage.OutputTokensDetails.ThinkingTokens}
	resp.ProviderRequestID = safeID(m.ID)
	stepsOK := true
	if researched {
		resp.Research, stepsOK = research(m)
	}
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
	if researched {
		return b.researchAnswer(m, resp, proj, stepsOK)
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
	return b.decoded(resp, proj, text.String())
}

// researchAnswer is answer's end for a research call: the last text block,
// after the last web search step, is the answer; earlier text is narration.
// A block of another type (a tool of the developer's, a tool result, an
// unknown one) or a step of another tool is not an answer this build takes.
func (b *Backend) researchAnswer(m message, resp provider.Response, proj structured.Projection, stepsOK bool) (provider.Response, error) {
	invalid := func() (provider.Response, error) {
		return resp, provider.NewError(provider.CodeInvalidOutput, 0, b.c.label)
	}
	if !stepsOK {
		return invalid()
	}
	last, lastStep := -1, -1
	for i, blk := range m.Content {
		switch blk.Type {
		case "text":
			last = i
		case "server_tool_use":
			lastStep = i
		case "thinking", "redacted_thinking":
		default:
			return invalid()
		}
	}
	if last < 0 || last < lastStep {
		return invalid()
	}
	return b.decoded(resp, proj, m.Content[last].Text)
}

// decoded is resp with text, the answer, decoded from the projection.
func (b *Backend) decoded(resp provider.Response, proj structured.Projection, text string) (provider.Response, error) {
	decoded, issues := proj.Decode([]byte(text))
	if len(issues) > 0 {
		e := provider.NewError(provider.CodeInvalidOutput, 0, b.c.label)
		e.Issues = issues
		return resp, e
	}
	resp.Text = decoded
	return resp, nil
}
