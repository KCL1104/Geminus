package aiapproval_test

import (
	"errors"
	"slices"
	"strings"
	"testing"

	"github.com/kcl1104/geminus/internal/config"
	"github.com/kcl1104/geminus/internal/config/aiapproval"
)

const keyA, keyB = "sk-ant-test-primary-0000000000", "sk-ant-test-secondary-1111111111"

// This build records no approval: no ID gives a valid Approval, and the
// zero value is none (H-017).
func TestNothingIsRecorded(t *testing.T) {
	for _, id := range []string{"", "H-001", "H-017", "D-018", "approved", " "} {
		if a, ok := aiapproval.Recorded(id); ok || a.Valid() || a.ID() != "" {
			t.Errorf("%q: %+v %v", id, a, ok)
		}
	}
	if (aiapproval.Approval{}).Valid() {
		t.Error("the zero Approval is valid")
	}
}

// An Approval is valid while its record is; a token kept after the record
// goes is not.
func TestRecorded(t *testing.T) {
	var kept aiapproval.Approval
	t.Run("recorded", func(t *testing.T) {
		aiapproval.RecordForTest(t, "H-TEST")
		a, ok := aiapproval.Recorded("H-TEST")
		if !ok || !a.Valid() || a.ID() != "H-TEST" {
			t.Fatalf("%+v %v", a, ok)
		}
		kept = a
	})
	if kept.Valid() {
		t.Error("an Approval outlives its record")
	}
}

type recorder struct {
	kv   map[string]string
	read []string
}

func (r *recorder) get(k string) string {
	r.read = append(r.read, k)
	return r.kv[k]
}

// With a recorded approval, config reads the keys (D-018: A primary, B
// optional, labelled org-a and org-b) and the model, and still never an
// ANTHROPIC_* variable.
func TestLiveAIApproved(t *testing.T) {
	aiapproval.RecordForTest(t, "H-TEST")
	r := &recorder{kv: map[string]string{"AI_PROVIDER": "anthropic", "AI_POLICY_APPROVAL": "H-TEST", "AI_MODEL": "approved-model",
		"GEMINUS_ANTHROPIC_API_KEY_A": keyA, "GEMINUS_ANTHROPIC_API_KEY_B": keyB, "ANTHROPIC_API_KEY": "must-not-be-read",
		"AI_MAX_OUTPUT_TOKENS": "4000", "AI_MAX_RETRIES": "0"}}
	ai, err := config.LoadAI(r.get)
	if err != nil {
		t.Fatal(err)
	}
	if !ai.LiveEnabled() || ai.Approval.ID() != "H-TEST" || ai.Model != "approved-model" || len(ai.Credentials) != 2 ||
		ai.Credentials[0].Label != "org-a" || ai.Credentials[0].Key.Reveal() != keyA ||
		ai.Credentials[1].Label != "org-b" || ai.Credentials[1].Key.Reveal() != keyB {
		t.Fatalf("%+v", ai)
	}
	if ai.Budget.MaxOutputTokens != 4000 || ai.Budget.MaxRetries == nil || *ai.Budget.MaxRetries != 0 {
		t.Errorf("budget %+v", ai.Budget)
	}
	if slices.Contains(r.read, "ANTHROPIC_API_KEY") {
		t.Error("ANTHROPIC_API_KEY was read")
	}
	// Only the primary key: no failover target, still valid.
	delete(r.kv, "GEMINUS_ANTHROPIC_API_KEY_B")
	if ai, err := config.LoadAI(r.get); err != nil || len(ai.Credentials) != 1 {
		t.Errorf("primary only: %+v %v", ai, err)
	}
}

// With a recorded approval, the model and the primary key are required.
func TestLiveAINeedsModelAndKey(t *testing.T) {
	aiapproval.RecordForTest(t, "H-TEST")
	for name, c := range map[string]struct {
		kv   map[string]string
		want string
	}{
		"no model":       {map[string]string{"GEMINUS_ANTHROPIC_API_KEY_A": keyA}, "AI_MODEL:required"},
		"no primary key": {map[string]string{"AI_MODEL": "m", "GEMINUS_ANTHROPIC_API_KEY_B": keyB}, "GEMINUS_ANTHROPIC_API_KEY_A:required"},
	} {
		c.kv["AI_PROVIDER"], c.kv["AI_POLICY_APPROVAL"] = "anthropic", "H-TEST"
		_, err := config.LoadAI(func(k string) string { return c.kv[k] })
		var ce *config.Error
		if !errors.As(err, &ce) || ce.Var+":"+ce.Code != c.want {
			t.Errorf("%s: %v, want %s", name, err, c.want)
		}
		if err != nil && (strings.Contains(err.Error(), keyA) || strings.Contains(err.Error(), keyB)) {
			t.Errorf("%s: the error leaks a key", name)
		}
	}
}
