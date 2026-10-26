// Package anthropic is the live provider backend (H-001, D-018): the
// Messages API of the owner's chosen provider through its official Go SDK,
// github.com/anthropics/anthropic-sdk-go v1.78.0 (MIT,
// docs/THIRD_PARTY_NOTICES.md, ADR-027).
//
// It is built only from an approved live configuration: New requires a
// valid aiapproval.Approval, which only a complete recorded approval
// yields (internal/config/aiapproval; the composition root copies
// Approval, Model and the credentials from internal/config's AI into
// Config), for the approval's own model and credentials. This build
// records H-018, which waits for the owner's confirmation of both provider
// organizations' data retention arrangement (T-053, D-113), so no valid
// Approval exists yet. It has been exercised against a local test server
// only; no request has been sent to the real API.
//
// T-053: the backend costs money, so it is provider.Billable: only a
// metered client (provider.NewMetered) calls it, it refuses a request
// without a spend reservation and a prompt role the approval's data policy
// does not name, before any I/O, it quotes each request's input bound, and
// it counts the attempts that ended without an answer (the provider may
// have billed them).
//
// D-018, as implemented:
//   - Keys are handed in explicitly. The SDK's environment defaults are not
//     used, so ANTHROPIC_API_KEY, ANTHROPIC_AUTH_TOKEN, ANTHROPIC_BASE_URL,
//     ANTHROPIC_CUSTOM_HEADERS and SDK profiles are never read.
//   - Credentials are tried in order, primary first. Only 401, 402 and 403
//     mark a credential unavailable and move to the next: after 401 or 403
//     (invalid or revoked) for the life of the process, after 402 (credits
//     used up) for billingCooldown, then it is tried again. 408, 409, 429,
//     5xx (529 included) and connection errors are retried on the same
//     credential with the SDK's backoff, up to the budget's retries, and
//     then fail without changing credential. A credential skipped because
//     it is unavailable is listed in FailedOver, on every call.
//   - The SDK reads a whole response body before the client's
//     MaxOutputBytes check applies; the body is bounded by max_tokens and
//     the attempt timeout, not by that check.
//   - A credential is named by its label only: in the response, the
//     metadata and every error. Errors never carry the provider's body.
//
// The request has no tools and no tool choice: the model can only answer
// with one JSON document, constrained by the projected output schema
// (projection.go) and checked by provider.Client against the full one.
// Thinking blocks in the answer are ignored, never stored.
package anthropic

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	sdk "github.com/anthropics/anthropic-sdk-go"
	"github.com/anthropics/anthropic-sdk-go/option"

	"github.com/kcl1104/geminus/internal/ai/provider"
	"github.com/kcl1104/geminus/internal/config/aiapproval"
	"github.com/kcl1104/geminus/internal/secret"
)

// productionURL is the provider's API. Tests use a local server instead
// (export_test.go); no configuration changes it.
const productionURL = "https://api.anthropic.com/"

// Config is an approved live configuration.
type Config struct {
	// Approval proves the owner's recorded approval the deployment named:
	// only aiapproval.Recorded makes a valid one (review L5).
	Approval aiapproval.Approval
	// Model is the approved model; there is no default.
	Model string
	// Credentials in failover order, primary first.
	Credentials []Credential
}

// Credential is an API key and its label (D-018).
type Credential struct {
	Label string
	Key   secret.Secret
}

type credential struct {
	label string
	key   secret.Secret

	mu sync.Mutex
	// disabled: refused with 401 or 403 (invalid or revoked); off until
	// the process restarts, with a rotated key.
	disabled bool
	// resting: refused with 402 (credits used up); tried again from then
	// on, since credits may be added.
	resting time.Time
}

func (c *credential) usable(now time.Time) bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	return !c.disabled && !now.Before(c.resting)
}

func (c *credential) refuse(status int, now time.Time) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if status == http.StatusPaymentRequired {
		c.resting = now.Add(billingCooldown)
		return
	}
	c.disabled = true
}

// billingCooldown is how long a credential refused with 402 rests before
// it is tried again (ADR-027).
const billingCooldown = 15 * time.Minute

// Backend is the live provider backend.
type Backend struct {
	model   string
	creds   []*credential
	baseURL string
	client  *http.Client
	now     func() time.Time

	projections sync.Map // output schema -> Projection
	allows      func(role string) bool
}

// New returns the live backend for an approved configuration: cfg.Approval
// must be a recorded, complete approval (internal/config/aiapproval),
// cfg.Model exactly its model, and the credentials exactly the ones it
// covers, in its order (T-053). The backend serves only the prompt roles
// the approval's data policy names.
func New(cfg Config) (*Backend, error) {
	if !cfg.Approval.Valid() {
		return nil, errors.New("anthropic: live AI is not approved (H-001, H-017, H-018)")
	}
	if cfg.Model != cfg.Approval.Model() {
		return nil, errors.New("anthropic: the model is not the approval's model")
	}
	labels := make([]string, len(cfg.Credentials))
	for i, c := range cfg.Credentials {
		labels[i] = c.Label
	}
	if !slices.Equal(labels, cfg.Approval.Credentials()) {
		return nil, errors.New("anthropic: the credentials are not the ones the approval covers")
	}
	return newBackend(cfg, productionURL, nil, cfg.Approval.AllowsRole)
}

// newBackend checks the rest of cfg. Only New, which checks the approval,
// and the tests' local server (export_test.go) call it. allows says which
// prompt roles the backend serves.
func newBackend(cfg Config, baseURL string, client *http.Client, allows func(string) bool) (*Backend, error) {
	if strings.TrimSpace(cfg.Model) == "" {
		return nil, errors.New("anthropic: no model; the owner approves one")
	}
	if len(cfg.Credentials) == 0 {
		return nil, errors.New("anthropic: no credential")
	}
	b := &Backend{model: cfg.Model, baseURL: baseURL, now: time.Now, allows: allows}
	seen := map[string]bool{}
	for _, c := range cfg.Credentials {
		if c.Label == "" || seen[c.Label] || c.Key.Reveal() == "" {
			return nil, errors.New("anthropic: every credential needs a key and a label of its own")
		}
		seen[c.Label] = true
		b.creds = append(b.creds, &credential{label: c.Label, key: c.Key})
	}
	if client == nil {
		client = &http.Client{Transport: http.DefaultTransport.(*http.Transport).Clone()}
	} else {
		copied := *client
		client = &copied
	}
	// Never follow a redirect: the key header would go with it.
	client.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	// Count the attempts that end without an answer (T-053).
	base := client.Transport
	if base == nil {
		base = http.DefaultTransport
	}
	client.Transport = tallying{base: base}
	b.client = client
	return b, nil
}

func (b *Backend) Name() string { return "anthropic" }

// projected is an output schema's projection and its size as sent.
type projected struct {
	Projection
	size int
}

func (b *Backend) projection(schema string) (projected, error) {
	if p, ok := b.projections.Load(schema); ok {
		return p.(projected), nil
	}
	p, err := Project(schema)
	if err != nil {
		return projected{}, err
	}
	data, err := json.Marshal(p.Schema)
	if err != nil {
		return projected{}, err
	}
	pr := projected{Projection: p, size: len(data)}
	b.projections.Store(schema, pr)
	return pr, nil
}

// quoteOverheadTokens is the quote's allowance for the input the Messages
// API adds of its own: role framing and the structured-output instructions
// it compiles from the schema (beyond the schema's own bytes). The usage
// settles the real number; the allowance only keeps the reservation an
// upper bound (D-111, PROPOSED).
const quoteOverheadTokens = 4096

// Quote is the model and the most input tokens one attempt of req can be
// billed for: the prompt's and the projected schema's bytes (every token
// of the tokenizer covers at least one byte) and quoteOverheadTokens. The
// adapter sends no cache_control, so no input token is a cache write.
func (b *Backend) Quote(req provider.Request) (provider.Quote, error) {
	proj, err := b.projection(req.OutputSchema)
	if err != nil {
		return provider.Quote{}, err
	}
	n := int64(len(req.Prompt.System)+len(req.Prompt.User())+proj.size) + quoteOverheadTokens
	return provider.Quote{Model: b.model, MaxInputTokens: n}, nil
}

// tally counts one call's attempts that ended without an answer after the
// request may have reached the provider: the transport failed (a lost
// connection, an attempt's timeout, a cancellation) or the body could not
// be read to its end. The provider may have billed them (T-053).
type tally struct{ unknown atomic.Int32 }

type tallyKey struct{}

func tallyOf(ctx context.Context) *tally {
	t, _ := ctx.Value(tallyKey{}).(*tally)
	return t
}

// tallying is the transport that keeps the tally of the call a request
// belongs to.
type tallying struct{ base http.RoundTripper }

func (tr tallying) RoundTrip(req *http.Request) (*http.Response, error) {
	resp, err := tr.base.RoundTrip(req)
	t := tallyOf(req.Context())
	if t == nil {
		return resp, err
	}
	if err != nil {
		t.unknown.Add(1)
		return resp, err
	}
	resp.Body = &tallyBody{ReadCloser: resp.Body, tally: t}
	return resp, nil
}

// tallyBody counts a body that could not be read to its end, once.
type tallyBody struct {
	io.ReadCloser
	tally *tally
	cut   bool
}

func (b *tallyBody) Read(p []byte) (int, error) {
	n, err := b.ReadCloser.Read(p)
	if err != nil && !errors.Is(err, io.EOF) && !b.cut {
		b.cut = true
		b.tally.unknown.Add(1)
	}
	return n, err
}

// Complete sends req, failing over between credentials as D-018 says. It
// refuses, before any I/O, a request without a spend reservation (only a
// metered client calls it, T-053) and a prompt role the approval's data
// policy does not name (PROPOSED D-112).
func (b *Backend) Complete(ctx context.Context, req provider.Request) (provider.Response, error) {
	if req.Reservation == "" {
		return provider.Response{}, provider.NewError(provider.CodeInvalidRequest, 0, "")
	}
	if b.allows == nil || !b.allows(string(req.Prompt.Role)) {
		return provider.Response{}, provider.NewError(provider.CodePolicyNotApproved, 0, "")
	}
	t := &tally{}
	resp, err := b.complete(context.WithValue(ctx, tallyKey{}, t), req)
	resp.UnknownAttempts = int(t.unknown.Load())
	return resp, err
}

func (b *Backend) complete(ctx context.Context, req provider.Request) (provider.Response, error) {
	proj, err := b.projection(req.OutputSchema)
	if err != nil {
		return provider.Response{}, provider.NewError(provider.CodeInvalidRequest, 0, "")
	}
	params := sdk.MessageNewParams{
		Model:        sdk.Model(b.model),
		MaxTokens:    req.MaxOutputTokens,
		System:       []sdk.TextBlockParam{{Text: req.Prompt.System}},
		Messages:     []sdk.MessageParam{sdk.NewUserMessage(sdk.NewTextBlock(req.Prompt.User()))},
		OutputConfig: sdk.OutputConfigParam{Format: sdk.JSONOutputFormatParam{Schema: proj.Schema}},
	}
	var failed []string
	for _, c := range b.creds {
		if !c.usable(b.now()) {
			// Refused earlier and still off: named, so the metadata and the
			// audit show every call that did not use the primary.
			failed = append(failed, c.label)
			continue
		}
		svc := sdk.NewMessageService(
			option.WithBaseURL(b.baseURL),
			option.WithHTTPClient(b.client),
			option.WithAPIKey(c.key.Reveal()),
			option.WithMaxRetries(max(0, req.MaxRetries)),
			option.WithRequestTimeout(req.AttemptTimeout),
		)
		var raw *http.Response
		msg, err := svc.New(ctx, params, option.WithResponseInto(&raw))
		resp := provider.Response{Model: b.model, Credential: c.label, FailedOver: failed}
		if raw != nil {
			resp.ProviderRequestID = raw.Header.Get("request-id")
		}
		if err == nil {
			return answer(msg, resp)
		}
		if ctxErr := ctx.Err(); ctxErr != nil {
			code := provider.CodeCancelled
			if errors.Is(ctxErr, context.DeadlineExceeded) {
				code = provider.CodeTimeout
			}
			return resp, provider.NewError(code, 0, c.label).WithCause(ctxErr)
		}
		var apiErr *sdk.Error
		if !errors.As(err, &apiErr) {
			// No response after the retries: a connection problem, not the
			// credential's.
			return resp, provider.NewError(provider.CodeUnavailable, 0, c.label)
		}
		if apiErr.RequestID != "" {
			resp.ProviderRequestID = apiErr.RequestID
		}
		switch status := apiErr.StatusCode; {
		case status == http.StatusUnauthorized, status == http.StatusPaymentRequired, status == http.StatusForbidden:
			c.refuse(status, b.now())
			failed = append(failed, c.label)
			continue
		case status == http.StatusTooManyRequests:
			return resp, provider.NewError(provider.CodeRateLimited, status, c.label)
		case status >= 500, status == http.StatusRequestTimeout, status == http.StatusConflict:
			return resp, provider.NewError(provider.CodeUnavailable, status, c.label)
		default:
			return resp, provider.NewError(provider.CodeRejected, status, c.label)
		}
	}
	return provider.Response{Model: b.model, FailedOver: failed}, provider.NewError(provider.CodeCredentialsExhausted, 0, "")
}

// answer takes the text of a complete message; thinking blocks are
// dropped unread.
func answer(msg *sdk.Message, resp provider.Response) (provider.Response, error) {
	if msg.Model != "" {
		resp.Model = string(msg.Model)
	}
	resp.Usage = provider.Usage{InputTokens: msg.Usage.InputTokens, OutputTokens: msg.Usage.OutputTokens,
		CacheReadInputTokens: msg.Usage.CacheReadInputTokens, CacheCreationInputTokens: msg.Usage.CacheCreationInputTokens}
	switch msg.StopReason {
	case sdk.StopReasonEndTurn, sdk.StopReasonStopSequence:
	case sdk.StopReasonMaxTokens:
		return resp, provider.NewError(provider.CodeTruncated, 0, resp.Credential)
	case sdk.StopReasonRefusal:
		return resp, provider.NewError(provider.CodeRefused, 0, resp.Credential)
	default: // tool_use, pause_turn or a reason this build does not know
		return resp, provider.NewError(provider.CodeInvalidOutput, 0, resp.Credential)
	}
	var text strings.Builder
	for _, block := range msg.Content {
		switch block.Type {
		case "text":
			text.WriteString(block.Text)
		case "thinking", "redacted_thinking":
		default: // a tool call or tool result: the request offered no tool
			return resp, provider.NewError(provider.CodeInvalidOutput, 0, resp.Credential)
		}
	}
	resp.Text = []byte(text.String())
	return resp, nil
}
