package meta_test

import (
	"bytes"
	"context"
	"encoding/json"
	"encoding/json/jsontext"
	"fmt"
	"image"
	"image/png"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/kcl1104/geminus/internal/ai/locale"
	"github.com/kcl1104/geminus/internal/ai/provider"
	"github.com/kcl1104/geminus/internal/ai/provider/meta"
	"github.com/kcl1104/geminus/internal/secret"
)

// Every test here talks to a local httptest server with answers shaped as
// Meta's documentation shows them (https://dev.meta.ai/docs/protocols/messages,
// /docs/api-reference/messages/schemas, /docs/sam/reading-segmentation,
// retrieved 2026-10-26). No test sends a request to the provider's real API
// (live calls are NOT RUN: T-055, H-033).

const (
	org = "org_01M4YDQK00TA00000000000000"
	// An obviously fake key in the documented shape (LLM_<id>_<secret>):
	// no test here reads a real one.
	key     = "LLM_000000000000_test-key-SENTINEL-not-a-real-key"
	model   = "muse-spark-1.3"
	answerJ = `{"status":"needs_input","summary":"Which station?","assumptions":[],"questions":["Which station?"],"requires_confirmation":true}`
)

// reply is one scripted HTTP answer.
type reply struct {
	status int
	body   string
	header map[string]string
	delay  time.Duration
	// hangUp closes the connection without an answer; cutBody sends the
	// status and part of the body, then closes it.
	hangUp, cutBody bool
}

// message is a Messages API answer: the content blocks, the stop reason,
// the usage.
func message(stop, content string, usage string) reply {
	return reply{status: 200, header: map[string]string{"Content-Type": "application/json"},
		body: `{"id":"msg_test_1","type":"message","role":"assistant","model":"` + model + `","content":` + content +
			`,"stop_reason":"` + stop + `","stop_sequence":null,"usage":` + usage + `}`}
}

const usageJ = `{"input_tokens":120,"output_tokens":30,"cache_read_input_tokens":0,"output_tokens_details":{"thinking_tokens":12}}`

// ok is a Messages answer of text, with a thinking block the adapter must
// drop unread.
func ok(text string) reply {
	content, _ := json.Marshal([]map[string]any{
		{"type": "thinking", "thinking": "PRIVATE-REASONING-SENTINEL", "signature": "sig"},
		{"type": "text", "text": text},
	})
	return message("end_turn", string(content), usageJ)
}

// apiError is an error status in the Messages API's Anthropic envelope.
func apiError(status int, kind string) reply {
	return reply{status: status, header: map[string]string{"Retry-After": "0", "Content-Type": "application/json"},
		body: `{"type":"error","error":{"type":"` + kind + `","message":"BODY-SENTINEL details"}}`}
}

// server answers each path's requests from its own script; the last reply
// repeats.
type server struct {
	t       *testing.T
	mu      sync.Mutex
	scripts map[string][]reply
	hits    map[string]int
	bodies  []map[string]any
	raw     [][]byte
	headers []http.Header
	srv     *httptest.Server
}

func newServer(t *testing.T, scripts map[string][]reply) *server {
	s := &server{t: t, scripts: scripts, hits: map[string]int{}}
	s.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		data, _ := io.ReadAll(r.Body)
		var body map[string]any
		if err := json.Unmarshal(data, &body); err != nil {
			t.Errorf("request body: %v", err)
		}
		s.mu.Lock()
		s.bodies = append(s.bodies, body)
		s.raw = append(s.raw, data)
		s.headers = append(s.headers, r.Header.Clone())
		path := r.Method + " " + r.URL.Path
		n := s.hits[path]
		s.hits[path]++
		script := s.scripts[path]
		s.mu.Unlock()
		if len(script) == 0 {
			t.Errorf("unexpected %s", path)
			w.WriteHeader(http.StatusNotFound) // an unknown path: no body (error-handling page)
			return
		}
		rep := script[min(n, len(script)-1)]
		if rep.hangUp {
			conn, _, err := w.(http.Hijacker).Hijack()
			if err != nil {
				t.Error(err)
				return
			}
			_ = conn.Close()
			return
		}
		if rep.cutBody {
			w.Header().Set("Content-Type", "application/json")
			w.Header().Set("Content-Length", fmt.Sprint(len(rep.body)+100))
			w.WriteHeader(rep.status)
			_, _ = io.WriteString(w, rep.body[:len(rep.body)/2])
			w.(http.Flusher).Flush()
			panic(http.ErrAbortHandler)
		}
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
		w.WriteHeader(rep.status)
		_, _ = io.WriteString(w, rep.body)
	}))
	t.Cleanup(s.srv.Close)
	return s
}

func (s *server) count(path string) int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.hits[path]
}

const messagesPath, responsesPath = "POST /v1/messages", "POST /v1/responses"

func config() meta.Config {
	return meta.Config{Model: model, Credential: meta.Credential{Label: "meta", Key: secret.Secret(key)}}
}

// waits records the backend's waits between attempts instead of sleeping.
type waits struct {
	mu sync.Mutex
	d  []time.Duration
}

func (w *waits) sleep(ctx context.Context, d time.Duration) error {
	w.mu.Lock()
	w.d = append(w.d, d)
	w.mu.Unlock()
	return ctx.Err()
}

func (w *waits) all() []time.Duration {
	w.mu.Lock()
	defer w.mu.Unlock()
	return append([]time.Duration(nil), w.d...)
}

const spendUSD = 1_000_000_000 // nano-USD

// backend is the Muse Spark backend on the local server, its waits
// recorded instead of slept.
func backend(t *testing.T, s *server) (*meta.Backend, *waits) {
	t.Helper()
	b, err := meta.NewForTest(config(), s.srv.URL+"/", s.srv.Client())
	if err != nil {
		t.Fatal(err)
	}
	w := &waits{}
	meta.SetSleep(b, w.sleep)
	return b, w
}

// meteredClient is a metered client of the backend on the local server.
func meteredClient(t *testing.T, s *server, limit int64, mutate ...func(*provider.Budget)) (*provider.Client, *meta.Backend, *provider.MemoryMeter, *waits) {
	t.Helper()
	b, w := backend(t, s)
	budget := provider.DefaultBudget()
	for _, m := range mutate {
		m(&budget)
	}
	m := provider.NewMemoryMeter(limit)
	c, err := provider.NewMetered(b, budget, m)
	if err != nil {
		t.Fatal(err)
	}
	return c, b, m, w
}

func client(t *testing.T, s *server, mutate ...func(*provider.Budget)) (*provider.Client, *meta.Backend) {
	c, b, _, _ := meteredClient(t, s, 1000*spendUSD, mutate...)
	return c, b
}

func sealed(t *testing.T, images ...provider.Image) provider.Prompt {
	t.Helper()
	intent, _ := jsontext.AppendQuote(nil, "raise the ICT tester capacity")
	p, err := provider.Seal(provider.Prompt{OrganizationID: org, Role: provider.RoleModeler, TemplateVersion: "modeler-test", Locale: locale.English,
		System: "rules", Blocks: []provider.Block{{Kind: provider.UntrustedData, Name: "USER_INTENT", Content: intent}}, Images: images})
	if err != nil {
		t.Fatal(err)
	}
	return p
}

var attribution = provider.Attribution{TargetType: "ai_proposal", TargetID: "aip_01M4YDQK00TA00000000000001", RequestID: "req_01M4YDQK00TA00000000000001"}

func ask(t *testing.T, c *provider.Client, images ...provider.Image) (provider.ProposalResult, error) {
	return askCtx(t, context.Background(), c, images...)
}

func askCtx(t *testing.T, ctx context.Context, c *provider.Client, images ...provider.Image) (provider.ProposalResult, error) {
	return c.GenerateModelProposal(ctx, provider.ProposalContext{OrganizationID: org, Mode: provider.ModeEdit, Prompt: sealed(t, images...),
		Attribution: attribution})
}

func pngImage(t *testing.T, w, h int) provider.Image {
	t.Helper()
	var b bytes.Buffer
	if err := png.Encode(&b, image.NewGray(image.Rect(0, 0, w, h))); err != nil {
		t.Fatal(err)
	}
	im, err := provider.NewImage(provider.ImagePNG, b.Bytes())
	if err != nil {
		t.Fatal(err)
	}
	return im
}

// chargeLog is a meter that grants every charge and records its amount.
type chargeLog struct {
	mu      sync.Mutex
	charges []int64
}

func (l *chargeLog) Reserve(_ context.Context, c provider.Charge) (provider.Reservation, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.charges = append(l.charges, c.NanoUSD)
	return provider.Reservation{ID: "r1", Charge: c}, nil
}

func (l *chargeLog) Settle(context.Context, provider.Reservation, provider.Settlement) error {
	return nil
}

// leaks fails t when any of the sentinels is in s.
func leaks(t *testing.T, what, s string, sentinels ...string) {
	t.Helper()
	for _, x := range append([]string{key, "test-key-SENTINEL", "BODY-SENTINEL", "PRIVATE-REASONING-SENTINEL"}, sentinels...) {
		if strings.Contains(s, x) {
			t.Errorf("%s carries %q: %s", what, x, s)
		}
	}
}
