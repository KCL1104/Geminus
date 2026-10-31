package anthropic_test

import (
	"context"
	"encoding/json/jsontext"
	"testing"

	"github.com/kcl1104/geminus/internal/ai/provider"
	"github.com/kcl1104/geminus/internal/ai/provider/anthropic"
)

// H-060: the Claude adapter offers no tool. It is no provider.ToolTurner, so
// the client refuses a tool turn with tools_unsupported before any I/O and
// reserves nothing; Complete handed tools refuses them the same way rather
// than send the request without them.
func TestToolTurnsAreRefused(t *testing.T) {
	var _ provider.Backend = (*anthropic.Backend)(nil)
	if _, ok := any((*anthropic.Backend)(nil)).(provider.ToolTurner); ok {
		t.Fatal("the Claude adapter is a ToolTurner")
	}
	s := newCapture(t, ok(answerJ))
	b, err := anthropic.NewForTest(config(keyA), s.srv.URL+"/", s.srv.Client())
	if err != nil {
		t.Fatal(err)
	}
	m := provider.NewMemoryMeter(1000 * spendUSD)
	c, err := provider.NewMetered(b, provider.DefaultBudget(), m)
	if err != nil {
		t.Fatal(err)
	}
	tools := []provider.Tool{{Name: "finish", InputSchema: jsontext.Value(`{"type":"object","properties":{},"additionalProperties":false}`)}}
	p := goldenSealed(t, provider.RoleScenario, "USER_INTENT", "add a second shift")
	if c.AcceptsTools(provider.RoleScenario) {
		t.Error("the client says the Claude adapter takes tools")
	}
	_, err = c.Turn(context.Background(), provider.TurnContext{OrganizationID: org, Prompt: p, Attribution: goldenAttribution, Tools: tools})
	if provider.CodeOf(err) != provider.CodeToolsUnsupported {
		t.Errorf("a tool turn: %v", err)
	}
	_, err = b.Complete(context.Background(), provider.Request{Prompt: p, OutputSchema: "proposal-output.schema.json", Tools: tools, Reservation: "r1"})
	if provider.CodeOf(err) != provider.CodeToolsUnsupported {
		t.Errorf("Complete handed tools: %v", err)
	}
	if len(s.raw) != 0 || m.Committed() != 0 {
		t.Errorf("sent %d requests, committed %d", len(s.raw), m.Committed())
	}
}
