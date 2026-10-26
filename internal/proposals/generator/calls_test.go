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

// testAttribution is what the tests' calls are charged to (T-053; the fake
// is not metered, so nothing is charged).
var testAttribution = provider.Attribution{TargetType: "ai_proposal", TargetID: "aip_01M4YDQK00TA00000000000001",
	RequestID: "req_01M4YDQK00TA00000000000001"}

// T-053: a metered call's line carries its cost and reservation; a spend
// limit's refusal and a settlement that failed are warnings of their own,
// with the limit or the reservation, and nothing else of the call.
func TestTheSpendLines(t *testing.T) {
	var buf bytes.Buffer
	h := &Handler{cfg: Config{Log: observability.NewLogger(&buf, "worker")}}
	meta := provider.Metadata{Provider: "anthropic", Model: "claude-opus-5-5", Role: provider.RoleModeler,
		CostNanoUSD: 84_000_000, SpendReservation: "asr_01M4YDQK00TA00000000000001"}
	lines := func() []map[string]any {
		var out []map[string]any
		for _, l := range bytes.Split(bytes.TrimSpace(buf.Bytes()), []byte("\n")) {
			var m map[string]any
			if err := json.Unmarshal(l, &m); err != nil {
				t.Fatalf("%q: %v", l, err)
			}
			out = append(out, m)
		}
		buf.Reset()
		return out
	}
	h.logCall(context.Background(), meta, nil)
	if l := lines(); len(l) != 1 || l[0]["cost_nano_usd"] != float64(84_000_000) || l[0]["spend_reservation_id"] != meta.SpendReservation {
		t.Errorf("the call: %v", l)
	}
	h.logCall(context.Background(), provider.Metadata{Role: provider.RoleModeler}, &provider.Error{Code: provider.CodeQuotaExceeded, Limit: "month"})
	if l := lines(); len(l) != 2 || l[0]["code"] != "AI_QUOTA_EXCEEDED" || l[1]["level"] != "WARN" || l[1]["limit"] != "month" {
		t.Errorf("a refusal: %v", l)
	}
	unsettled := meta
	unsettled.SpendUnsettled = true
	h.logCall(context.Background(), unsettled, nil)
	if l := lines(); len(l) != 2 || l[1]["level"] != "WARN" || l[1]["spend_reservation_id"] != meta.SpendReservation || l[1]["spend_unsettled"] != true {
		t.Errorf("unsettled: %v", l)
	}
	// The audit metadata of a metered call.
	am := auditMeta(meta)
	if am["cost_nano_usd"] != int64(84_000_000) || am["spend_reservation_id"] != meta.SpendReservation || am["spend_unsettled"] != false {
		t.Errorf("audit %v", am)
	}
	if _, ok := auditMeta(provider.Metadata{Provider: "fake"})["cost_nano_usd"]; ok {
		t.Error("an unmetered call's event names a cost")
	}
}
