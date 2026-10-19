package config

import (
	"encoding/json"
	"fmt"
	"log/slog"
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

// approve lists an approval for the duration of a test, as the owner's
// recorded decision would.
func approve(t *testing.T, id string) {
	t.Helper()
	approvedLivePolicies[id] = true
	t.Cleanup(func() { delete(approvedLivePolicies, id) })
}

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
		if ai.LiveEnabled() || ai.Approval != "" || ai.Model != "" || len(ai.Credentials) != 0 {
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

func TestLiveAIApproved(t *testing.T) {
	approve(t, "H-TEST")
	r := &recorder{kv: map[string]string{"AI_PROVIDER": "anthropic", "AI_POLICY_APPROVAL": "H-TEST", "AI_MODEL": "approved-model",
		"GEMINUS_ANTHROPIC_API_KEY_A": keyA, "GEMINUS_ANTHROPIC_API_KEY_B": keyB, "ANTHROPIC_API_KEY": "must-not-be-read",
		"AI_MAX_OUTPUT_TOKENS": "4000", "AI_MAX_RETRIES": "0"}}
	ai, err := LoadAI(r.get)
	if err != nil {
		t.Fatal(err)
	}
	if !ai.LiveEnabled() || ai.Model != "approved-model" || len(ai.Credentials) != 2 ||
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
	if ai, err := LoadAI(r.get); err != nil || len(ai.Credentials) != 1 {
		t.Errorf("primary only: %+v %v", ai, err)
	}
}

func TestLoadAIRejects(t *testing.T) {
	approve(t, "H-TEST")
	live := func(kv map[string]string) map[string]string {
		out := map[string]string{"AI_PROVIDER": "anthropic", "AI_POLICY_APPROVAL": "H-TEST", "AI_MODEL": "m", "GEMINUS_ANTHROPIC_API_KEY_A": keyA}
		for k, v := range kv {
			if v == "" {
				delete(out, k)
			} else {
				out[k] = v
			}
		}
		return out
	}
	cases := map[string]struct {
		kv   map[string]string
		want []string
	}{
		"unlisted approval":       {live(map[string]string{"AI_POLICY_APPROVAL": "H-017"}), []string{"AI_POLICY_APPROVAL:not_approved"}},
		"approval while disabled": {map[string]string{"AI_POLICY_APPROVAL": "H-TEST"}, []string{"AI_POLICY_APPROVAL:invalid"}},
		"approval with the fake":  {map[string]string{"AI_PROVIDER": "fake", "APP_ENV": "test", "AI_POLICY_APPROVAL": "H-TEST"}, []string{"AI_POLICY_APPROVAL:invalid"}},
		"no model":                {live(map[string]string{"AI_MODEL": ""}), []string{"AI_MODEL:required"}},
		"no primary key":          {live(map[string]string{"GEMINUS_ANTHROPIC_API_KEY_A": "", "GEMINUS_ANTHROPIC_API_KEY_B": keyB}), []string{"GEMINUS_ANTHROPIC_API_KEY_A:required"}},
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
