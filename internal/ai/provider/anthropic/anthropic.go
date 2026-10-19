// Package anthropic is the live provider backend (H-001, D-018): the
// Messages API of the owner's chosen provider through its official Go SDK,
// github.com/anthropics/anthropic-sdk-go v1.78.0 (MIT,
// docs/THIRD_PARTY_NOTICES.md, ADR-027).
//
// It is built only from an approved live configuration: New requires a
// valid aiapproval.Approval, which only a recorded approval yields
// (internal/config/aiapproval; the composition root copies Approval, Model
// and the credentials from internal/config's AI into Config), and this
// build records none before the owner approves model, token quota and data
// policy (H-017). It has been exercised against a local test server only;
// no request has been sent to the real API.
//
// D-018, as implemented:
//   - Keys are handed in explicitly. The SDK's environment defaults are not
//     used, so ANTHROPIC_API_KEY, ANTHROPIC_AUTH_TOKEN, ANTHROPIC_BASE_URL,
//     ANTHROPIC_CUSTOM_HEADERS and SDK profiles are never read.
//   - Credentials are tried in order, primary first. Only 401, 402 and 403
//     mark a credential unavailable (for the life of the process) and move
//     to the next; 408, 409, 429, 5xx (529 included) and connection errors
//     are retried on the same credential with the SDK's backoff, up to the
//     budget's retries, and then fail without changing credential.
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
	"errors"
	"net/http"
	"strings"
	"sync"

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

	mu          sync.Mutex
	unavailable bool
}

func (c *credential) usable() bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	return !c.unavailable
}

func (c *credential) refuse() {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.unavailable = true
}

// Backend is the live provider backend.
type Backend struct {
	model   string
	creds   []*credential
	baseURL string
	client  *http.Client

	projections sync.Map // output schema -> Projection
}

// New returns the live backend for an approved configuration: cfg.Approval
// must be a recorded approval.
func New(cfg Config) (*Backend, error) {
	if !cfg.Approval.Valid() {
		return nil, errors.New("anthropic: live AI is not approved (H-001, H-017)")
	}
	return newBackend(cfg, productionURL, nil)
}

// newBackend checks the rest of cfg. Only New, which checks the approval,
// and the tests' local server (export_test.go) call it.
func newBackend(cfg Config, baseURL string, client *http.Client) (*Backend, error) {
	if strings.TrimSpace(cfg.Model) == "" {
		return nil, errors.New("anthropic: no model; the owner approves one")
	}
	if len(cfg.Credentials) == 0 {
		return nil, errors.New("anthropic: no credential")
	}
	b := &Backend{model: cfg.Model, baseURL: baseURL}
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
	b.client = client
	return b, nil
}

func (b *Backend) Name() string { return "anthropic" }

func (b *Backend) projection(schema string) (Projection, error) {
	if p, ok := b.projections.Load(schema); ok {
		return p.(Projection), nil
	}
	p, err := Project(schema)
	if err != nil {
		return Projection{}, err
	}
	b.projections.Store(schema, p)
	return p, nil
}

// Complete sends req, failing over between credentials as D-018 says.
func (b *Backend) Complete(ctx context.Context, req provider.Request) (provider.Response, error) {
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
		if !c.usable() {
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
			c.refuse()
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
