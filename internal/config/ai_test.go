package config

import (
	"encoding/json"
	"fmt"
	"log/slog"
	"maps"
	"slices"
	"strings"
	"testing"

	"github.com/kcl1104/geminus/internal/secret"
)

// recorder is a getenv that remembers every variable it was asked for.
type recorder struct {
	kv   map[string]string
	read []string
}

func (r *recorder) get(k string) string {
	r.read = append(r.read, k)
	return r.kv[k]
}

const keyA, keyB = "sk-ant-test-primary-0000000000", "sk-ant-test-secondary-1111111111"

// The tests that need a recorded approval (the keys read once live AI is
// approved, a missing model or key) are in internal/config/aiapproval,
// whose tests alone can record one (review L5).

// Live AI is off by default and stays off without the owner's recorded
// approval; keys are never read then, and no ANTHROPIC_* variable ever is
// (D-018, H-017).
func TestLiveAIStaysOff(t *testing.T) {
	for name, kv := range map[string]map[string]string{
		"nothing set":            {},
		"disabled":               {"AI_PROVIDER": "disabled", "GEMINUS_ANTHROPIC_API_KEY_A": keyA},
		"anthropic, no approval": {"AI_PROVIDER": "anthropic", "AI_MODEL": "some-model", "GEMINUS_ANTHROPIC_API_KEY_A": keyA},
		"fake in tests":          {"AI_PROVIDER": "fake", "APP_ENV": "test", "GEMINUS_ANTHROPIC_API_KEY_A": keyA},
	} {
		r := &recorder{kv: kv}
		ai, err := LoadAI(r.get)
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		if ai.LiveEnabled() || ai.Approval.Valid() || ai.Approval.ID() != "" || ai.Model != "" || len(ai.Credentials) != 0 {
			t.Errorf("%s: live AI enabled: %+v", name, ai)
		}
		for _, k := range r.read {
			if strings.HasPrefix(k, "GEMINUS_ANTHROPIC_API_KEY") || strings.HasPrefix(k, "ANTHROPIC_") {
				t.Errorf("%s: read %s", name, k)
			}
		}
	}
	if ai, _ := LoadAI(env(nil)); ai.Provider != AIProviderDisabled {
		t.Errorf("default provider %q", ai.Provider)
	}
}

// T-053: this build records H-018, which waits for the owner's retention
// confirmation, so even with every variable live AI needs set it stays off:
// with the switch on the process refuses to start, without it LiveOff says
// why; no key is read either way.
func TestThisBuildKeepsLiveOff(t *testing.T) {
	all := map[string]string{"AI_PROVIDER": "anthropic", "AI_POLICY_APPROVAL": "H-018", "AI_MODEL": "claude-opus-5-5",
		"GEMINUS_ANTHROPIC_API_KEY_A": keyA, "GEMINUS_ANTHROPIC_API_KEY_B": keyB, "GEMINUS_AI_LIVE": "on"}
	r := &recorder{kv: all}
	_, err := LoadAI(r.get)
	if got := codes(t, err); !slices.Equal(got, []string{"GEMINUS_AI_LIVE:retention_unconfirmed"}) {
		t.Errorf("switch on: %v", got)
	}
	off := maps.Clone(all)
	delete(off, "GEMINUS_AI_LIVE")
	r2 := &recorder{kv: off}
	ai, err := LoadAI(r2.get)
	if err != nil || ai.LiveEnabled() || ai.LiveOff != "retention_unconfirmed" || len(ai.Credentials) != 0 || ai.Model != "" {
		t.Errorf("switch off: %+v %v", ai, err)
	}
	for _, k := range append(r.read, r2.read...) {
		if strings.HasPrefix(k, "GEMINUS_ANTHROPIC_API_KEY") || strings.HasPrefix(k, "ANTHROPIC_") {
			t.Errorf("read %s", k)
		}
	}
	// Naming no approval is a reason of its own.
	if ai, err := LoadAI(env(map[string]string{"AI_PROVIDER": "anthropic"})); err != nil || ai.LiveOff != LiveOffNoApproval {
		t.Errorf("no approval: %+v %v", ai, err)
	}
}

func TestLoadAIRejects(t *testing.T) {
	live := map[string]string{"AI_PROVIDER": "anthropic", "AI_MODEL": "m", "GEMINUS_ANTHROPIC_API_KEY_A": keyA}
	with := func(kv map[string]string) map[string]string {
		out := maps.Clone(live)
		maps.Copy(out, kv)
		return out
	}
	cases := map[string]struct {
		kv   map[string]string
		want []string
	}{
		"unlisted approval":       {with(map[string]string{"AI_POLICY_APPROVAL": "H-017"}), []string{"AI_POLICY_APPROVAL:not_approved"}},
		"approval while disabled": {map[string]string{"AI_POLICY_APPROVAL": "H-TEST"}, []string{"AI_POLICY_APPROVAL:invalid"}},
		"approval with the fake":  {map[string]string{"AI_PROVIDER": "fake", "APP_ENV": "test", "AI_POLICY_APPROVAL": "H-TEST"}, []string{"AI_POLICY_APPROVAL:invalid"}},
		"fake without APP_ENV":    {map[string]string{"AI_PROVIDER": "fake"}, []string{"AI_PROVIDER:invalid"}},
		"fake in production":      {map[string]string{"AI_PROVIDER": "fake", "APP_ENV": "production"}, []string{"AI_PROVIDER:invalid"}},
		"unknown provider":        {map[string]string{"AI_PROVIDER": "openai"}, []string{"AI_PROVIDER:invalid"}},
		"legacy key variable":     {map[string]string{"AI_API_KEY": keyA}, []string{"AI_API_KEY:invalid"}},
		"bad budget":              {map[string]string{"AI_MAX_OUTPUT_TOKENS": "lots", "AI_TIMEOUT_SECONDS": "0", "AI_MAX_RETRIES": "-1"}, []string{"AI_MAX_OUTPUT_TOKENS:invalid", "AI_TIMEOUT_SECONDS:invalid", "AI_MAX_RETRIES:invalid"}},
	}
	for name, c := range cases {
		ai, err := LoadAI(env(c.kv))
		if err == nil {
			t.Errorf("%s: accepted %+v", name, ai)
			continue
		}
		if got := codes(t, err); !slices.Equal(got, c.want) {
			t.Errorf("%s: codes %v, want %v", name, got, c.want)
		}
		for _, s := range []string{keyA, keyB} {
			if strings.Contains(err.Error(), s) {
				t.Errorf("%s: the error leaks a key", name)
			}
		}
	}
}

// A Secret never prints, marshals or logs its value.
func TestSecretNeverPrints(t *testing.T) {
	c := AICredential{Label: "org-a", Key: secret.Secret(keyA)}
	ai := AI{Provider: AIProviderAnthropic, Credentials: []AICredential{c}}
	var b strings.Builder
	for _, verb := range []string{"%v", "%+v", "%#v", "%s", "%q", "%x", "%X", "%d"} {
		fmt.Fprintf(&b, verb+"\n", ai)
		fmt.Fprintf(&b, verb+"\n", c.Key)
	}
	j, err := json.Marshal(ai)
	if err != nil {
		t.Fatal(err)
	}
	b.Write(j)
	logger := slog.New(slog.NewJSONHandler(&b, nil))
	logger.Info("config", "ai", ai, "key", c.Key)
	out := b.String()
	if strings.Contains(out, "sk-ant") || strings.Contains(out, "primary-0000") || strings.Contains(out, "736b2d616e74") {
		t.Errorf("a key is printed:\n%s", out)
	}
	if !strings.Contains(out, "[redacted]") || !strings.Contains(out, "org-a") {
		t.Errorf("the label or the marker is missing:\n%s", out)
	}
}
