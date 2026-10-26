package generator

import (
	"bytes"
	"context"
	"encoding/json/v2"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"testing"

	"github.com/kcl1104/geminus/internal/ai/provider"
	"github.com/kcl1104/geminus/internal/observability"
)

// T-035 (D-095 (a)): each provider call is one operational line through
// the worker's filtered log: the provider, model and credential labels,
// the role, latency, tokens, the provider's request ID and an outcome code,
// with the job's correlation from the context; never the prompt, the
// answer or a key, even when the provider's error or labels carry them.
func TestTheCallsLine(t *testing.T) {
	var buf bytes.Buffer
	h := &Handler{cfg: Config{Log: observability.NewLogger(&buf, "worker")}}
	ctx := observability.With(context.Background(), slog.String("job_id", "job_01M4YDQK00ADN261CME3WV5HZ6"),
		slog.String("proposal_id", "aip_01M4YDQK00ADN261CME3WV5HZ6"))
	meta := provider.Metadata{Provider: "anthropic", Model: "claude-opus-5-5", Credential: "org-b", Role: provider.RoleScenario,
		Usage: provider.Usage{InputTokens: 1200, OutputTokens: 300}, LatencyMillis: 2500, ProviderRequestID: "req_011CUcvD2y5nA6z5tYt7aCb1"}
	odd := meta
	odd.Model, odd.ProviderRequestID = "sk-ant-api03-SENTINELsentinel0123456789", "Bearer SENTINEL.token"
	for _, c := range []struct {
		name string
		meta provider.Metadata
		err  error
		code string
	}{
		{"ok", meta, nil, "OK"},
		{"the client's error", meta, provider.NewError(provider.CodeTimeout, 0, "org-b").WithCause(context.DeadlineExceeded), "AI_TIMEOUT"},
		{"another error", meta, errors.New(`decoding "PROMPT-SENTINEL": bad`), "ERROR"},
		{"labels the provider chose", odd, nil, "OK"},
	} {
		buf.Reset()
		h.logCall(ctx, c.meta, c.err)
		var m map[string]any
		if err := json.Unmarshal(bytes.TrimSpace(buf.Bytes()), &m); err != nil {
			t.Fatalf("%s: %q: %v", c.name, buf.String(), err)
		}
		if m["msg"] != "ai provider call ended" || m["level"] != "INFO" || m["component"] != "worker" || m["code"] != c.code ||
			m["provider"] != "anthropic" || m["credential_label"] != "org-b" || m["role"] != "scenario" || m["latency_ms"] != float64(2500) ||
			m["input_tokens"] != float64(1200) || m["output_tokens"] != float64(300) ||
			m["job_id"] != "job_01M4YDQK00ADN261CME3WV5HZ6" || m["proposal_id"] != "aip_01M4YDQK00ADN261CME3WV5HZ6" {
			t.Errorf("%s: %v", c.name, m)
		}
		if c.meta.Model == meta.Model && (m["model"] != "claude-opus-5-5" || m["provider_request_id"] != "req_011CUcvD2y5nA6z5tYt7aCb1") {
			t.Errorf("%s: model and request ID %v", c.name, m)
		}
		if strings.Contains(buf.String(), "SENTINEL") {
			t.Errorf("%s: %s", c.name, buf.String())
		}
		if _, ok := m["error"]; ok {
			t.Errorf("%s: the line carries the error's text: %v", c.name, fmt.Sprint(m["error"]))
		}
	}
}
