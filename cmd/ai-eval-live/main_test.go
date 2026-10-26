package main

import (
	"fmt"
	"testing"

	"github.com/kcl1104/geminus/internal/config"
)

// T-055: the live eval's backend is AI_PROVIDER's adapter: Muse Spark for
// meta (H-029), Claude for anthropic (H-018); nothing for a provider that
// is not live. The backends are built here, never called (live runs are
// NOT RUN in this task); the keys are placeholders.
func TestTheBackendIsTheProvidersAdapter(t *testing.T) {
	env := func(kv ...string) func(string) string {
		m := map[string]string{}
		for i := 0; i+1 < len(kv); i += 2 {
			m[kv[i]] = kv[i+1]
		}
		return func(k string) string { return m[k] }
	}
	for _, c := range []struct {
		name string
		env  func(string) string
		want string
	}{
		{"meta", env("AI_PROVIDER", "meta", "AI_POLICY_APPROVAL", "H-029", "AI_MODEL", "muse-spark-1.3",
			"GEMINUS_MUSE_API_KEY", "LLM_000000000000_test-key-not-a-real-key", "GEMINUS_AI_LIVE", "on"), "*meta.Backend"},
		{"anthropic", env("AI_PROVIDER", "anthropic", "AI_POLICY_APPROVAL", "H-018", "AI_MODEL", "claude-opus-5-5",
			"GEMINUS_ANTHROPIC_API_KEY_A", "sk-ant-test-fake-aaaaaaaaaaaa", "GEMINUS_ANTHROPIC_API_KEY_B", "sk-ant-test-fake-bbbbbbbbbbbb",
			"GEMINUS_AI_LIVE", "on"), "*anthropic.Backend"},
	} {
		ai, err := config.LoadAI(c.env)
		if err != nil || !ai.LiveEnabled() {
			t.Fatalf("%s: %+v %v", c.name, ai, err)
		}
		b, err := liveBackend(ai)
		if err != nil || fmt.Sprintf("%T", b) != c.want {
			t.Errorf("%s: %T %v", c.name, b, err)
		}
	}
	if b, err := liveBackend(config.AI{Provider: config.AIProviderFake}); err == nil {
		t.Errorf("a live backend for the fake: %T", b)
	}
}
