package generator

import (
	"bytes"
	"context"
	"encoding/json/v2"
	"errors"
	"fmt"
	"log/slog"
	"strconv"
	"strings"
	"sync"
	"testing"

	"github.com/kcl1104/geminus/internal/ai/provider"
	"github.com/kcl1104/geminus/internal/ai/provider/fake"
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
	// H-057: the line says which call of the attempt it was: 1, or 2 for
	// the one repair.
	for _, c := range []struct {
		name string
		meta provider.Metadata
		err  error
		code string
		call int
	}{
		{"ok", meta, nil, "OK", 1},
		{"the client's error", meta, provider.NewError(provider.CodeTimeout, 0, "org-b").WithCause(context.DeadlineExceeded), "AI_TIMEOUT", 1},
		{"another error", meta, errors.New(`decoding "PROMPT-SENTINEL": bad`), "ERROR", 2},
		{"labels the provider chose", odd, nil, "OK", 1},
		{"a repair", meta, nil, "OK", 2},
	} {
		buf.Reset()
		h.logCall(ctx, c.meta, c.err, c.call)
		var m map[string]any
		if err := json.Unmarshal(bytes.TrimSpace(buf.Bytes()), &m); err != nil {
			t.Fatalf("%s: %q: %v", c.name, buf.String(), err)
		}
		if m["call_attempt"] != float64(c.call) {
			t.Errorf("%s: call_attempt %v, want %d", c.name, m["call_attempt"], c.call)
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

// T-053 schema follow-up: a call the provider answered with an error
// status is a WARN line of its own, after the call's line: the status, the
// error type (the adapter's list of the API's types, or other), the code,
// the role, the credential and the provider's request ID; never the
// provider's message. A type outside the log's token shape is redacted by
// the filter (the adapter never sets one). A failure without a status (a
// timeout, a lost connection) is no such warning.
func TestTheProviderErrorLine(t *testing.T) {
	var buf bytes.Buffer
	h := &Handler{cfg: Config{Log: observability.NewLogger(&buf, "worker")}}
	meta := provider.Metadata{Provider: "anthropic", Model: "claude-opus-5-5", Credential: "org-a", Role: provider.RoleModeler,
		ProviderRequestID: "req_011CUcvD2y5nA6z5tYt7aCb1"}
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
	h.logCall(context.Background(), meta, &provider.Error{Code: provider.CodeRejected, Status: 400, ErrorType: "invalid_request_error", Credential: "org-a"}, 1)
	l := lines()
	if len(l) != 2 || l[0]["code"] != "AI_REQUEST_REJECTED" || l[1]["level"] != "WARN" || l[1]["msg"] != "the ai provider answered the call with an error status" ||
		l[1]["provider_status"] != float64(400) || l[1]["provider_error_type"] != "invalid_request_error" || l[1]["code"] != "AI_REQUEST_REJECTED" ||
		l[1]["role"] != "modeler" || l[1]["credential_label"] != "org-a" || l[1]["provider_request_id"] != "req_011CUcvD2y5nA6z5tYt7aCb1" {
		t.Errorf("an error status: %v", l)
	}
	h.logCall(context.Background(), meta, &provider.Error{Code: provider.CodeUnavailable, Status: 529, ErrorType: "Bearer SENTINEL.token", Credential: "org-a"}, 1)
	if raw := buf.String(); strings.Contains(raw, "SENTINEL") {
		t.Error(raw)
	}
	if l := lines(); len(l) != 2 || l[1]["provider_status"] != float64(529) || l[1]["provider_error_type"] != observability.Redacted {
		t.Errorf("an odd type: %v", l)
	}
	h.logCall(context.Background(), meta, provider.NewError(provider.CodeTimeout, 0, "org-a").WithCause(context.DeadlineExceeded), 1)
	if l := lines(); len(l) != 1 {
		t.Errorf("a failure without a status warned: %v", l)
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
	h.logCall(context.Background(), meta, nil, 1)
	if l := lines(); len(l) != 1 || l[0]["cost_nano_usd"] != float64(84_000_000) || l[0]["spend_reservation_id"] != meta.SpendReservation {
		t.Errorf("the call: %v", l)
	}
	h.logCall(context.Background(), provider.Metadata{Role: provider.RoleModeler}, &provider.Error{Code: provider.CodeQuotaExceeded, Limit: "month"}, 1)
	if l := lines(); len(l) != 2 || l[0]["code"] != "AI_QUOTA_EXCEEDED" || l[1]["level"] != "WARN" || l[1]["limit"] != "month" {
		t.Errorf("a refusal: %v", l)
	}
	unsettled := meta
	unsettled.SpendUnsettled = true
	h.logCall(context.Background(), unsettled, nil, 1)
	if l := lines(); len(l) != 2 || l[1]["level"] != "WARN" || l[1]["spend_reservation_id"] != meta.SpendReservation || l[1]["spend_unsettled"] != true {
		t.Errorf("unsettled: %v", l)
	}
	// T-053 review round 1: a call above its reservation and an answer
	// priced at the dearest geo are warned of; a plain call is not.
	over := meta
	over.SpendOverReservation = true
	h.logCall(context.Background(), over, nil, 1)
	if l := lines(); len(l) != 2 || l[1]["level"] != "WARN" || l[1]["spend_over_reservation"] != true || l[1]["cost_nano_usd"] != float64(84_000_000) {
		t.Errorf("over the reservation: %v", l)
	}
	unknown := meta
	unknown.InferenceGeo = "unknown"
	h.logCall(context.Background(), unknown, nil, 1)
	if l := lines(); len(l) != 2 || l[1]["level"] != "WARN" || l[1]["inference_geo"] != "unknown" || l[1]["spend_reservation_id"] != meta.SpendReservation {
		t.Errorf("an unknown geo: %v", l)
	}
	global := meta
	global.InferenceGeo = "global"
	h.logCall(context.Background(), global, nil, 1)
	if l := lines(); len(l) != 1 {
		t.Errorf("a priced geo is not a warning: %v", l)
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

// chargeLog is a meter that grants every charge and keeps it; with
// refuseAfter set, it refuses every charge after that many, as a spend cap
// would (quota_exceeded, organization_day). Its reservation IDs are "r" and
// the count, or ids (a format of the count) when set.
type chargeLog struct {
	mu          sync.Mutex
	charges     []provider.Charge
	refuseAfter int
	ids         string
}

func (m *chargeLog) Reserve(_ context.Context, c provider.Charge) (provider.Reservation, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.refuseAfter > 0 && len(m.charges) >= m.refuseAfter {
		return provider.Reservation{}, &provider.Error{Code: provider.CodeQuotaExceeded, Limit: provider.LimitOrganizationDay}
	}
	m.charges = append(m.charges, c)
	if m.ids != "" {
		return provider.Reservation{ID: fmt.Sprintf(m.ids, len(m.charges)), Charge: c}, nil
	}
	return provider.Reservation{ID: "r" + strconv.Itoa(len(m.charges)), Charge: c}, nil
}

func (m *chargeLog) Settle(context.Context, provider.Reservation, provider.Settlement) error {
	return nil
}

// T-053: generate charges a metered call to the target and request it was
// given, in the proposal's organization, as the modeler; a metered client
// refuses a call charged to nothing (mutation run 1: D8 was left to the
// DB-backed TestAMeteredProposal).
func TestAMeteredCallIsChargedToItsTarget(t *testing.T) {
	m := &chargeLog{}
	b := &billedFake{Backend: fake.New(), usage: provider.Usage{InputTokens: 100, OutputTokens: 10}}
	c, err := provider.NewMetered(b, provider.DefaultBudget(), m)
	if err != nil {
		t.Fatal(err)
	}
	h, err := New(Config{Provider: c, Packs: packs(t), Actor: "worker-1", Log: discard()})
	if err != nil {
		t.Fatal(err)
	}
	st := stored(t, "edit", "Improve it.")
	_, meta, err := outcomeOf(h.generate(context.Background(), st.proposal.OrganizationID, st, testAttribution))
	if err != nil || meta == nil || meta.SpendReservation != "r1" || b.calls.Load() != 1 {
		t.Fatalf("%v %+v, %d call(s)", err, meta, b.calls.Load())
	}
	if len(m.charges) != 1 || m.charges[0].Attribution != testAttribution || m.charges[0].OrganizationID != st.proposal.OrganizationID ||
		m.charges[0].Role != provider.RoleModeler || m.charges[0].NanoUSD <= 0 {
		t.Errorf("charges %+v", m.charges)
	}
}
