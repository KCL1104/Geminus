package generator

import (
	"bytes"
	"context"
	"encoding/json/jsontext"
	"encoding/json/v2"
	"strings"
	"testing"

	"github.com/kcl1104/geminus/internal/ai/locale"
	"github.com/kcl1104/geminus/internal/ai/provider"
	"github.com/kcl1104/geminus/internal/ai/provider/fake"
	"github.com/kcl1104/geminus/internal/observability"
)

// turnBilledFake is the fake as a billable backend of a priced model, so a
// metered client reserves and settles a tool turn (H-060).
type turnBilledFake struct{ *fake.Backend }

func (b turnBilledFake) Quote(context.Context, provider.Request) (provider.Quote, error) {
	return provider.Quote{Model: "muse-spark-1.3", MaxInputTokens: 20_000}, nil
}

func (b turnBilledFake) Turn(ctx context.Context, req provider.Request) (provider.Response, error) {
	r, err := b.Backend.Turn(ctx, req)
	r.Model, r.Credential, r.ProviderRequestID = "muse-spark-1.3", "meta", "msg_turn_1"
	r.Usage = provider.Usage{InputTokens: 800, OutputTokens: 40}
	return r, err
}

// H-060, slice E1: a tool turn's metadata is a call's like any other, so the
// worker's operational line (logCall, T-035) and the audit metadata
// (auditMeta) read the same fields for it: provider, model, credential,
// role, latency, tokens, request ID, code, cost and reservation, and the
// turn's own input and output hashes; never the prompt, the model's text or
// its arguments. A refused turn is settled and logged with its code.
func TestTheCallsLineOfAToolTurn(t *testing.T) {
	const sentinel = "TURN-SENTINEL"
	f := fake.New()
	m := provider.NewMemoryMeter(1_000_000_000_000)
	c, err := provider.NewMetered(turnBilledFake{f}, provider.DefaultBudget(), m)
	if err != nil {
		t.Fatal(err)
	}
	intent, _ := jsontext.AppendQuote(nil, sentinel+" find a better shift plan")
	p, err := provider.Seal(provider.Prompt{OrganizationID: "org_01M4YDQK00TA00000000000000", Role: provider.RoleScenario, TemplateVersion: "scenario-test",
		Locale: locale.English, System: sentinel + " rules", Blocks: []provider.Block{{Kind: provider.UntrustedData, Name: fake.IntentBlock, Content: intent}}})
	if err != nil {
		t.Fatal(err)
	}
	tools := []provider.Tool{{Name: "propose_what_if", Description: "Propose a what-if.",
		InputSchema: jsontext.Value(`{"type":"object","properties":{"idea":{"type":"string"}},"required":["idea"],"additionalProperties":false}`)}}
	key := sentinel + " find a better shift plan"
	f.ScriptTools(provider.RoleScenario, key,
		fake.ToolAnswer{Text: sentinel + " narration", Calls: []provider.ToolCall{{ID: "call_1", Name: "propose_what_if", Input: jsontext.Value(`{"idea":"` + sentinel + `"}`)}}},
	)
	in := provider.TurnContext{OrganizationID: p.OrganizationID, Prompt: p, Tools: tools,
		Attribution: provider.Attribution{TargetType: "investigation", TargetID: "inv_01M4YDQK00TA00000000000001", RequestID: "req_01M4YDQK00TA00000000000001"}}
	res, err := c.Turn(context.Background(), in)
	if err != nil || len(res.Calls) != 1 {
		t.Fatalf("%+v %v", res, err)
	}
	var buf bytes.Buffer
	h := &Handler{cfg: Config{Log: observability.NewLogger(&buf, "worker")}}
	h.logCall(context.Background(), res.Metadata, nil, 1)
	var line map[string]any
	if err := json.Unmarshal(bytes.TrimSpace(buf.Bytes()), &line); err != nil {
		t.Fatalf("%q: %v", buf.String(), err)
	}
	if line["msg"] != "ai provider call ended" || line["code"] != "OK" || line["provider"] != "fake" || line["model"] != "muse-spark-1.3" ||
		line["credential_label"] != "meta" || line["role"] != "scenario" || line["input_tokens"] != float64(800) || line["output_tokens"] != float64(40) ||
		line["provider_request_id"] != "msg_turn_1" || line["cost_nano_usd"] == float64(0) || line["spend_reservation_id"] == "" {
		t.Errorf("the line %v", line)
	}
	a := auditMeta(res.Metadata)
	if a["input_hash"] == p.InputHash || a["input_hash"] == "" || a["output_hash"] == nil || a["schema"] != "" || a["spend_reservation_id"] == nil {
		t.Errorf("the audit metadata %v", a)
	}
	// A refused turn (a call of no tool offered): settled, and logged with
	// its code.
	f.ScriptTools(provider.RoleScenario, key, fake.ToolAnswer{Calls: []provider.ToolCall{{ID: "call_2", Name: "publish_version", Input: jsontext.Value(`{}`)}}})
	refused, err := c.Turn(context.Background(), in)
	if provider.CodeOf(err) != provider.CodeInvalidOutput {
		t.Fatalf("a refused turn: %v", err)
	}
	first := buf.String()
	buf.Reset()
	h.logCall(context.Background(), refused.Metadata, err, 1)
	line = nil
	if err := json.Unmarshal(bytes.TrimSpace(buf.Bytes()), &line); err != nil {
		t.Fatalf("%q: %v", buf.String(), err)
	}
	if line["code"] != "AI_INVALID_OUTPUT" || line["cost_nano_usd"] == float64(0) {
		t.Errorf("the refused turn's line %v", line)
	}
	auditJSON, _ := json.Marshal(a)
	for _, s := range []string{first, buf.String(), string(auditJSON)} {
		if strings.Contains(s, sentinel) {
			t.Errorf("the line or the audit metadata carries the prompt or the model's text: %s", s)
		}
	}
}
