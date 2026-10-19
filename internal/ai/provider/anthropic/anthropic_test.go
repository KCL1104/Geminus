package anthropic_test

import (
	"context"
	"encoding/json"
	"encoding/json/jsontext"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/kcl1104/geminus/internal/ai/locale"
	"github.com/kcl1104/geminus/internal/ai/provider"
	"github.com/kcl1104/geminus/internal/ai/provider/anthropic"
	"github.com/kcl1104/geminus/internal/secret"
)

// Every test here talks to a local httptest server. No test sends a
// request to the provider's real API (live calls are NOT RUN: H-001, H-017).

const (
	org     = "org_01M4YDQK00TA00000000000000"
	keyA    = "sk-ant-test-primary-AAAAAAAAAAAAAAAA"
	keyB    = "sk-ant-test-secondary-BBBBBBBBBBBBBB"
	model   = "approved-test-model"
	answerJ = `{"status":"needs_input","summary":"Which station?","assumptions":[],"questions":["Which station?"],"requires_confirmation":true}`
)

// reply is one scripted HTTP answer.
type reply struct {
	status int
	body   string
	header map[string]string
	delay  time.Duration
}

func ok(text string) reply {
	content, _ := json.Marshal([]map[string]any{
		{"type": "thinking", "thinking": "PRIVATE-REASONING-SENTINEL", "signature": "sig"},
		{"type": "text", "text": text},
	})
	return reply{status: 200, header: map[string]string{"request-id": "req_test_1"},
		body: `{"id":"msg_1","type":"message","role":"assistant","model":"` + model + `","content":` + string(content) +
			`,"stop_reason":"end_turn","stop_sequence":null,"usage":{"input_tokens":120,"output_tokens":30,"cache_creation_input_tokens":0,"cache_read_input_tokens":0}}`}
}

func apiError(status int, kind string) reply {
	return reply{status: status, header: map[string]string{"retry-after-ms": "1", "request-id": "req_err"},
		body: `{"type":"error","error":{"type":"` + kind + `","message":"BODY-SENTINEL details"},"request_id":"req_err"}`}
}

// server answers each key's requests from its own script; the last reply
// repeats.
type server struct {
	t       *testing.T
	mu      sync.Mutex
	scripts map[string][]reply
	hits    map[string]int
	bodies  []map[string]any
	headers []http.Header
	srv     *httptest.Server
}

func newServer(t *testing.T, scripts map[string][]reply) *server {
	s := &server{t: t, scripts: scripts, hits: map[string]int{}}
	s.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.URL.Path != "/v1/messages" {
			t.Errorf("unexpected %s %s", r.Method, r.URL.Path)
		}
		data, _ := io.ReadAll(r.Body)
		var body map[string]any
		if err := json.Unmarshal(data, &body); err != nil {
			t.Errorf("request body: %v", err)
		}
		key := r.Header.Get("X-Api-Key")
		s.mu.Lock()
		s.bodies = append(s.bodies, body)
		s.headers = append(s.headers, r.Header.Clone())
		n := s.hits[key]
		s.hits[key]++
		script := s.scripts[key]
		s.mu.Unlock()
		if len(script) == 0 {
			w.WriteHeader(http.StatusUnauthorized)
			_, _ = io.WriteString(w, `{"type":"error","error":{"type":"authentication_error","message":"unknown key"}}`)
			return
		}
		rep := script[min(n, len(script)-1)]
		if rep.delay > 0 {
			select {
			case <-time.After(rep.delay):
			case <-r.Context().Done():
				return
			}
		}
		for k, v := range rep.header {
			w.Header().Set(k, v)
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(rep.status)
		_, _ = io.WriteString(w, rep.body)
	}))
	t.Cleanup(s.srv.Close)
	return s
}

func (s *server) count(key string) int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.hits[key]
}

func config(keys ...string) anthropic.Config {
	cfg := anthropic.Config{Approval: "H-TEST", Model: model}
	for i, k := range keys {
		cfg.Credentials = append(cfg.Credentials, anthropic.Credential{Label: fmt.Sprintf("org-%c", 'a'+i), Key: secret.Secret(k)})
	}
	return cfg
}

func client(t *testing.T, s *server, cfg anthropic.Config, mutate ...func(*provider.Budget)) (*provider.Client, *anthropic.Backend) {
	t.Helper()
	b, err := anthropic.NewForTest(cfg, s.srv.URL+"/", s.srv.Client())
	if err != nil {
		t.Fatal(err)
	}
	budget := provider.DefaultBudget()
	budget.MaxRetries = 2
	for _, m := range mutate {
		m(&budget)
	}
	c, err := provider.New(b, budget)
	if err != nil {
		t.Fatal(err)
	}
	return c, b
}

func prompt(t *testing.T) provider.Prompt {
	t.Helper()
	intent, _ := jsontext.AppendQuote(nil, "raise the ICT tester capacity")
	p, err := provider.Seal(provider.Prompt{Role: provider.RoleModeler, TemplateVersion: "modeler-test", Locale: locale.English,
		System: "rules", Blocks: []provider.Block{{Kind: provider.UntrustedData, Name: "USER_INTENT", Content: intent}}})
	if err != nil {
		t.Fatal(err)
	}
	return p
}

func ask(t *testing.T, c *provider.Client) (provider.ProposalResult, error) {
	return askCtx(t, context.Background(), c)
}

func askCtx(t *testing.T, ctx context.Context, c *provider.Client) (provider.ProposalResult, error) {
	return c.GenerateModelProposal(ctx, provider.ProposalContext{OrganizationID: org, Mode: provider.ModeEdit, Prompt: prompt(t)})
}

// The adapter sends the injected key only, to the configured server only:
// the SDK's environment defaults (ANTHROPIC_API_KEY, ANTHROPIC_AUTH_TOKEN,
// ANTHROPIC_BASE_URL, ANTHROPIC_CUSTOM_HEADERS, profiles) play no part
// (D-018). The request is the sealed prompt, the approved model, the
// budget's max_tokens and the projected schema, and offers no tool.
func TestRequestShape(t *testing.T) {
	trap := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		t.Error("a request went to ANTHROPIC_BASE_URL")
	}))
	defer trap.Close()
	// The SDK's default chain stops at the first credential it finds, so
	// each source is tried on its own as well as all together.
	for name, env := range map[string]map[string]string{
		"all": {"ANTHROPIC_API_KEY": "sk-ant-from-the-environment", "ANTHROPIC_AUTH_TOKEN": "token-from-the-environment",
			"ANTHROPIC_BASE_URL": trap.URL, "ANTHROPIC_CUSTOM_HEADERS": "X-Leak: environment", "ANTHROPIC_PROFILE": "environment-profile"},
		"auth token":     {"ANTHROPIC_AUTH_TOKEN": "token-from-the-environment", "ANTHROPIC_BASE_URL": trap.URL},
		"custom headers": {"ANTHROPIC_CUSTOM_HEADERS": "X-Leak: environment\nAuthorization: Bearer leaked", "ANTHROPIC_BASE_URL": trap.URL},
		"none":           {},
	} {
		t.Run(name, func(t *testing.T) {
			t.Setenv("ANTHROPIC_CONFIG_DIR", t.TempDir())
			for _, k := range []string{"ANTHROPIC_API_KEY", "ANTHROPIC_AUTH_TOKEN", "ANTHROPIC_BASE_URL", "ANTHROPIC_CUSTOM_HEADERS", "ANTHROPIC_PROFILE"} {
				t.Setenv(k, env[k])
			}
			requestShape(t)
		})
	}
}

func requestShape(t *testing.T) {
	s := newServer(t, map[string][]reply{keyA: {ok(answerJ)}})
	c, _ := client(t, s, config(keyA, keyB), func(b *provider.Budget) { b.MaxOutputTokens = 1234 })
	res, err := ask(t, c)
	if err != nil {
		t.Fatal(err)
	}
	if res.Output.Status != "needs_input" {
		t.Errorf("output %+v", res.Output)
	}
	h, body := s.headers[0], s.bodies[0]
	if h.Get("X-Api-Key") != keyA || h.Get("Authorization") != "" || h.Get("X-Leak") != "" {
		t.Errorf("headers: injected key %v, authorization %q, leak %q", h.Get("X-Api-Key") == keyA, h.Get("Authorization"), h.Get("X-Leak"))
	}
	p := prompt(t)
	if body["model"] != model || body["max_tokens"] != float64(1234) {
		t.Errorf("model %v max_tokens %v", body["model"], body["max_tokens"])
	}
	sys, _ := body["system"].([]any)
	if len(sys) != 1 || sys[0].(map[string]any)["text"] != p.System {
		t.Errorf("system %v", body["system"])
	}
	msgs, _ := body["messages"].([]any)
	if len(msgs) != 1 || msgs[0].(map[string]any)["role"] != "user" ||
		msgs[0].(map[string]any)["content"].([]any)[0].(map[string]any)["text"] != p.User() {
		t.Errorf("messages %v", body["messages"])
	}
	for _, absent := range []string{"tools", "tool_choice", "thinking", "temperature", "top_p", "top_k", "metadata", "stop_sequences", "mcp_servers", "container"} {
		if _, ok := body[absent]; ok {
			t.Errorf("the request has %s", absent)
		}
	}
	format := body["output_config"].(map[string]any)["format"].(map[string]any)
	proj, err := anthropic.Project("proposal-output.schema.json")
	if err != nil {
		t.Fatal(err)
	}
	want, _ := json.Marshal(proj.Schema)
	var wantSchema any
	_ = json.Unmarshal(want, &wantSchema)
	if format["type"] != "json_schema" || !reflect.DeepEqual(format["schema"], wantSchema) {
		t.Errorf("format %v", format)
	}
	m := res.Metadata
	if m.Provider != "anthropic" || m.Model != model || m.Credential != "org-a" || len(m.FailedOver) != 0 ||
		m.Usage.InputTokens != 120 || m.Usage.OutputTokens != 30 || m.ProviderRequestID != "req_test_1" {
		t.Errorf("metadata %+v", m)
	}
	meta, _ := json.Marshal(m)
	for _, leak := range []string{keyA, keyB, "PRIVATE-REASONING-SENTINEL"} {
		if strings.Contains(string(meta), leak) || strings.Contains(string(res.Raw), leak) {
			t.Errorf("%q in the metadata or the answer", leak)
		}
	}
}

// D-018: 402 billing_error, 401 and 403 mark the credential unavailable
// and move to the next; later calls skip it.
func TestFailover(t *testing.T) {
	for _, r := range []reply{apiError(402, "billing_error"), apiError(401, "authentication_error"), apiError(403, "permission_error")} {
		t.Run(fmt.Sprint(r.status), func(t *testing.T) {
			s := newServer(t, map[string][]reply{keyA: {r}, keyB: {ok(answerJ)}})
			c, _ := client(t, s, config(keyA, keyB))
			res, err := ask(t, c)
			if err != nil {
				t.Fatal(err)
			}
			if res.Metadata.Credential != "org-b" || !reflect.DeepEqual(res.Metadata.FailedOver, []string{"org-a"}) {
				t.Errorf("metadata %+v", res.Metadata)
			}
			if s.count(keyA) != 1 || s.count(keyB) != 1 {
				t.Errorf("hits a=%d b=%d", s.count(keyA), s.count(keyB))
			}
			if _, err := ask(t, c); err != nil {
				t.Fatal(err)
			}
			if s.count(keyA) != 1 || s.count(keyB) != 2 {
				t.Errorf("the refused credential was tried again: a=%d b=%d", s.count(keyA), s.count(keyB))
			}
		})
	}
	s := newServer(t, map[string][]reply{keyA: {apiError(402, "billing_error")}, keyB: {apiError(401, "authentication_error")}})
	c, _ := client(t, s, config(keyA, keyB))
	res, err := ask(t, c)
	var pe *provider.Error
	if !errors.As(err, &pe) || pe.Code != provider.CodeCredentialsExhausted || pe.Retryable() || !reflect.DeepEqual(res.Metadata.FailedOver, []string{"org-a", "org-b"}) {
		t.Errorf("both refused: %v %+v", err, res.Metadata)
	}
	if _, err := ask(t, c); provider.CodeOf(err) != provider.CodeCredentialsExhausted || s.count(keyA) != 1 || s.count(keyB) != 1 {
		t.Errorf("after both were refused: %v a=%d b=%d", err, s.count(keyA), s.count(keyB))
	}
}

// D-018: 429, 529 and 5xx are retried on the same credential only, then
// fail; the other organization's credential is never used for them.
func TestSameCredentialRetries(t *testing.T) {
	s := newServer(t, map[string][]reply{keyA: {apiError(429, "rate_limit_error"), ok(answerJ)}, keyB: {ok(answerJ)}})
	c, _ := client(t, s, config(keyA, keyB))
	res, err := ask(t, c)
	if err != nil || res.Metadata.Credential != "org-a" || s.count(keyA) != 2 || s.count(keyB) != 0 {
		t.Errorf("429 then 200: %v %+v a=%d b=%d", err, res.Metadata, s.count(keyA), s.count(keyB))
	}
	for _, c := range []struct {
		r    reply
		code provider.Code
	}{
		{apiError(429, "rate_limit_error"), provider.CodeRateLimited},
		{apiError(529, "overloaded_error"), provider.CodeUnavailable},
		{apiError(500, "api_error"), provider.CodeUnavailable},
		{apiError(503, "api_error"), provider.CodeUnavailable},
	} {
		s := newServer(t, map[string][]reply{keyA: {c.r}, keyB: {ok(answerJ)}})
		cl, _ := client(t, s, config(keyA, keyB))
		_, err := ask(t, cl)
		var pe *provider.Error
		if !errors.As(err, &pe) || pe.Code != c.code || pe.Status != c.r.status || pe.Credential != "org-a" || !pe.Retryable() {
			t.Errorf("%d: %v", c.r.status, err)
		}
		if s.count(keyA) != 3 || s.count(keyB) != 0 {
			t.Errorf("%d: hits a=%d b=%d, want 1+2 retries on a and none on b", c.r.status, s.count(keyA), s.count(keyB))
		}
	}
	// A request the provider rejects is neither retried nor failed over.
	s = newServer(t, map[string][]reply{keyA: {apiError(400, "invalid_request_error")}, keyB: {ok(answerJ)}})
	cl, _ := client(t, s, config(keyA, keyB))
	if _, err := ask(t, cl); provider.CodeOf(err) != provider.CodeRejected || s.count(keyA) != 1 || s.count(keyB) != 0 {
		t.Errorf("400: %v a=%d b=%d", err, s.count(keyA), s.count(keyB))
	}
	// Zero retries means one attempt.
	s = newServer(t, map[string][]reply{keyA: {apiError(529, "overloaded_error")}})
	cl, _ = client(t, s, config(keyA), func(b *provider.Budget) { b.MaxRetries = 0 })
	if _, err := ask(t, cl); provider.CodeOf(err) != provider.CodeUnavailable || s.count(keyA) != 1 {
		t.Errorf("no retries: %v a=%d", err, s.count(keyA))
	}
}

// Errors name the credential by label and carry neither the key nor the
// provider's response body.
func TestErrorsAreSanitized(t *testing.T) {
	for _, r := range []reply{apiError(400, "invalid_request_error"), apiError(529, "overloaded_error"), apiError(402, "billing_error")} {
		s := newServer(t, map[string][]reply{keyA: {r}})
		c, _ := client(t, s, config(keyA))
		_, err := ask(t, c)
		if err == nil {
			t.Fatal("no error")
		}
		msg := fmt.Sprintf("%v %+v %#v", err, err, err)
		for _, leak := range []string{keyA, "BODY-SENTINEL", s.srv.URL} {
			if strings.Contains(msg, leak) {
				t.Errorf("%d: the error carries %q: %s", r.status, leak, msg)
			}
		}
	}
}

func message(stop string, content string) reply {
	return reply{status: 200, body: `{"id":"msg_2","type":"message","role":"assistant","model":"` + model + `","content":` + content +
		`,"stop_reason":"` + stop + `","stop_sequence":null,"usage":{"input_tokens":1,"output_tokens":1,"cache_creation_input_tokens":0,"cache_read_input_tokens":0}}`}
}

func TestStopReasons(t *testing.T) {
	text := `[{"type":"text","text":` + string(mustQuote(answerJ)) + `}]`
	cases := map[string]struct {
		r    reply
		code provider.Code
	}{
		"max tokens": {message("max_tokens", text), provider.CodeTruncated},
		"refusal":    {message("refusal", `[]`), provider.CodeRefused},
		"tool use":   {message("tool_use", `[{"type":"tool_use","id":"t1","name":"sql","input":{"q":"DELETE FROM twin.models"}}]`), provider.CodeInvalidOutput},
		// A tool_use stop is refused for its stop reason alone, even when
		// the content holds nothing but a valid answer.
		"tool use, text only": {message("tool_use", text), provider.CodeInvalidOutput},
		"pause turn":          {message("pause_turn", text), provider.CodeInvalidOutput},
		"tool block":          {message("end_turn", `[{"type":"tool_use","id":"t1","name":"sql","input":{}}]`), provider.CodeInvalidOutput},
		"no text":             {message("end_turn", `[]`), provider.CodeInvalidOutput},
		"prose":               {message("end_turn", `[{"type":"text","text":"I approved it."}]`), provider.CodeInvalidOutput},
	}
	for name, c := range cases {
		s := newServer(t, map[string][]reply{keyA: {c.r}})
		cl, _ := client(t, s, config(keyA))
		if _, err := ask(t, cl); provider.CodeOf(err) != c.code {
			t.Errorf("%s: %v, want %s", name, err, c.code)
		}
	}
}

func mustQuote(s string) []byte {
	b, err := json.Marshal(s)
	if err != nil {
		panic(err)
	}
	return b
}

// The budget's timeouts bound the call, retries included; cancellation
// passes through.
func TestTimeoutsAndCancellation(t *testing.T) {
	slow := reply{status: 200, body: ok(answerJ).body, delay: 2 * time.Second}
	s := newServer(t, map[string][]reply{keyA: {slow}})
	c, _ := client(t, s, config(keyA), func(b *provider.Budget) {
		b.Timeout, b.AttemptTimeout, b.MaxRetries = 300*time.Millisecond, 100*time.Millisecond, 1
	})
	start := time.Now()
	_, err := ask(t, c)
	if code := provider.CodeOf(err); code != provider.CodeTimeout && code != provider.CodeUnavailable {
		t.Errorf("slow provider: %v", err)
	}
	if d := time.Since(start); d > time.Second {
		t.Errorf("the call took %v, over its budget", d)
	}
	c, _ = client(t, s, config(keyA), func(b *provider.Budget) { b.Timeout, b.AttemptTimeout = 200*time.Millisecond, 200*time.Millisecond })
	if _, err := ask(t, c); provider.CodeOf(err) != provider.CodeTimeout || !errors.Is(err, context.DeadlineExceeded) {
		t.Errorf("total timeout: %v", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	time.AfterFunc(50*time.Millisecond, cancel)
	c, _ = client(t, s, config(keyA))
	if _, err := askCtx(t, ctx, c); provider.CodeOf(err) != provider.CodeCancelled || !errors.Is(err, context.Canceled) {
		t.Errorf("cancelled: %v", err)
	}
}

// The key never follows a redirect to another host.
func TestNoRedirects(t *testing.T) {
	other := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Errorf("the redirect was followed (key sent: %v)", r.Header.Get("X-Api-Key") != "")
	}))
	defer other.Close()
	s := newServer(t, map[string][]reply{keyA: {{status: http.StatusTemporaryRedirect, header: map[string]string{"Location": other.URL + "/v1/messages"}}}})
	c, _ := client(t, s, config(keyA))
	if _, err := ask(t, c); err == nil {
		t.Error("a redirect answered the call")
	}
}

func TestNewRefuses(t *testing.T) {
	for name, cfg := range map[string]anthropic.Config{
		"not approved":     {Model: model, Credentials: config(keyA).Credentials},
		"no model":         {Approval: "H-TEST", Credentials: config(keyA).Credentials},
		"no credential":    {Approval: "H-TEST", Model: model},
		"empty key":        {Approval: "H-TEST", Model: model, Credentials: []anthropic.Credential{{Label: "org-a"}}},
		"no label":         {Approval: "H-TEST", Model: model, Credentials: []anthropic.Credential{{Key: secret.Secret(keyA)}}},
		"duplicate labels": {Approval: "H-TEST", Model: model, Credentials: []anthropic.Credential{{Label: "org-a", Key: secret.Secret(keyA)}, {Label: "org-a", Key: secret.Secret(keyB)}}},
	} {
		if _, err := anthropic.New(cfg); err == nil {
			t.Errorf("%s: accepted", name)
		} else if strings.Contains(err.Error(), keyA) {
			t.Errorf("%s: the error leaks the key", name)
		}
	}
	if anthropic.ProductionURL != "https://api.anthropic.com/" {
		t.Errorf("production URL %q", anthropic.ProductionURL)
	}
}
