package config

import (
	"maps"
	"slices"
	"strings"
	"testing"

	"github.com/kcl1104/geminus/internal/ai/spend"
)

// T-055: AI_PROVIDER=meta turns live AI on with exactly this build's H-029
// record (amended by H-033), AI_MODEL its model, the operator's switch and
// the owner's key GEMINUS_MUSE_API_KEY (one key, labelled meta); anything
// missing keeps it off, and with the switch on refuses to start. The fake
// stays the development default; disabled the default of all.

// An obviously fake key in the documented shape (LLM_<id>_<secret>).
const museKey = "LLM_000000000000_test-key-not-a-real-key"

func metaEnv() map[string]string {
	return map[string]string{"AI_PROVIDER": "meta", "AI_POLICY_APPROVAL": "H-029", "AI_MODEL": "muse-spark-1.3",
		"GEMINUS_MUSE_API_KEY": museKey, "GEMINUS_AI_LIVE": "on",
		// None of these is ever read: no SDK default, no Anthropic key for a
		// Meta deployment.
		"MODEL_API_KEY": "LLM_111111_must-not-be-read", "GEMINUS_ANTHROPIC_API_KEY_A": keyA, "GEMINUS_ANTHROPIC_API_KEY_B": keyB}
}

func neverRead(t *testing.T, r *recorder, names ...string) {
	t.Helper()
	for _, k := range r.read {
		if slices.Contains(names, k) || strings.HasPrefix(k, "ANTHROPIC_") || k == "MODEL_API_KEY" || k == "GEMINUS_META_API_KEY" {
			t.Errorf("read %s", k)
		}
	}
}

func TestMetaLiveTurnsOn(t *testing.T) {
	r := &recorder{kv: metaEnv()}
	ai, err := LoadAI(r.get)
	if err != nil {
		t.Fatal(err)
	}
	if !ai.LiveEnabled() || ai.Provider != AIProviderMeta || ai.LiveOff != "" || ai.Approval.ID() != "H-029" || ai.Model != "muse-spark-1.3" ||
		len(ai.Credentials) != 1 || ai.Credentials[0].Label != "meta" || ai.Credentials[0].Key.Reveal() != museKey {
		t.Fatalf("%+v", ai)
	}
	if want := spend.DefaultLimits(100 * spend.NanoPerUSD); ai.Limits != want {
		t.Errorf("limits %+v, want %+v", ai.Limits, want)
	}
	// The photo path follows AI_PROVIDER (H-033): SAM 3.1 and Muse Spark,
	// the same approval and key.
	v := ai.Vision
	if !v.LiveEnabled() || v.Provider != AIProviderMeta || v.LiveOff != "" || v.Approval.ID() != "H-029" || v.Model != "muse-spark-1.3" ||
		v.SegmentationModel != "sam-3.1" || len(v.Credentials) != 1 || v.Credentials[0].Key.Reveal() != museKey {
		t.Errorf("vision %+v", v)
	}
	neverRead(t, r, "GEMINUS_ANTHROPIC_API_KEY_A", "GEMINUS_ANTHROPIC_API_KEY_B")
	// Load agrees: the provider and live.
	cfg, err := Load(env(maps.Clone(map[string]string{"DATABASE_URL": "postgres://u@h/db", "AI_PROVIDER": "meta", "AI_POLICY_APPROVAL": "H-029",
		"AI_MODEL": "muse-spark-1.3", "GEMINUS_MUSE_API_KEY": museKey, "GEMINUS_AI_LIVE": "on"})))
	if err != nil || cfg.AIProvider != AIProviderMeta || !cfg.AILive {
		t.Errorf("Load: %+v %v", cfg, err)
	}
}

// Without the switch the key is never read and the reason is typed; the
// photo path is off for the same reason.
func TestMetaWithoutTheSwitch(t *testing.T) {
	for _, sw := range []string{"", "off"} {
		kv := metaEnv()
		kv["GEMINUS_AI_LIVE"] = sw
		r := &recorder{kv: kv}
		ai, err := LoadAI(r.get)
		if err != nil {
			t.Fatalf("%q: %v", sw, err)
		}
		if ai.LiveEnabled() || ai.LiveOff != LiveOffSwitch || len(ai.Credentials) != 0 || ai.Vision.LiveEnabled() || ai.Vision.LiveOff != LiveOffSwitch ||
			len(ai.Vision.Credentials) != 0 {
			t.Errorf("%q: %+v", sw, ai)
		}
		neverRead(t, r, "GEMINUS_MUSE_API_KEY", "GEMINUS_ANTHROPIC_API_KEY_A", "GEMINUS_ANTHROPIC_API_KEY_B")
	}
}

// What keeps Meta live off: with the switch on, the process refuses to
// start with the condition's typed code; off, LiveOff says the same. The
// key is read only once everything else holds, and checked: present,
// without white space or control characters, in the documented LLM_ shape
// (https://dev.meta.ai/docs/authentication: "API keys start with LLM_").
// No error carries a key.
func TestMetaMismatchesKeepLiveOff(t *testing.T) {
	for name, c := range map[string]struct {
		env  map[string]string
		on   []string
		off  string
		read bool // the key is read
	}{
		"the Contributor model":    {map[string]string{"AI_MODEL": "muse-spark-1.3-contributor"}, []string{"AI_MODEL:model_mismatch"}, LiveOffModelMismatch, false},
		"another version":          {map[string]string{"AI_MODEL": "muse-spark-1.2"}, []string{"AI_MODEL:model_mismatch"}, LiveOffModelMismatch, false},
		"no model":                 {map[string]string{"AI_MODEL": ""}, []string{"AI_MODEL:required"}, LiveOffModelMismatch, false},
		"the Anthropic approval":   {map[string]string{"AI_POLICY_APPROVAL": "H-018"}, []string{"AI_POLICY_APPROVAL:provider_mismatch"}, LiveOffProviderMismatch, false},
		"an unrecorded approval":   {map[string]string{"AI_POLICY_APPROVAL": "H-033"}, []string{"AI_POLICY_APPROVAL:not_approved"}, "", false},
		"no key":                   {map[string]string{"GEMINUS_MUSE_API_KEY": ""}, []string{"GEMINUS_MUSE_API_KEY:required"}, "", true},
		"an Anthropic key":         {map[string]string{"GEMINUS_MUSE_API_KEY": keyA}, []string{"GEMINUS_MUSE_API_KEY:invalid"}, "", true},
		"a trailing newline":       {map[string]string{"GEMINUS_MUSE_API_KEY": museKey + "\n"}, []string{"GEMINUS_MUSE_API_KEY:invalid"}, "", true},
		"a space before":           {map[string]string{"GEMINUS_MUSE_API_KEY": " " + museKey}, []string{"GEMINUS_MUSE_API_KEY:invalid"}, "", true},
		"a control character":      {map[string]string{"GEMINUS_MUSE_API_KEY": "LLM_000_\x00x"}, []string{"GEMINUS_MUSE_API_KEY:invalid"}, "", true},
		"only the prefix":          {map[string]string{"GEMINUS_MUSE_API_KEY": "LLM_"}, []string{"GEMINUS_MUSE_API_KEY:invalid"}, "", true},
		"the old variable instead": {map[string]string{"GEMINUS_MUSE_API_KEY": "", "GEMINUS_META_API_KEY": museKey}, []string{"GEMINUS_MUSE_API_KEY:required"}, "", true},
	} {
		kv := metaEnv()
		maps.Copy(kv, c.env)
		r := &recorder{kv: kv}
		ai, err := LoadAI(r.get)
		if got := codes(t, err); !slices.Equal(got, c.on) || ai.LiveEnabled() {
			t.Errorf("%s, switch on: %v, want %v", name, got, c.on)
		}
		if err != nil && (strings.Contains(err.Error(), museKey) || strings.Contains(err.Error(), "LLM_0") || strings.Contains(err.Error(), "sk-ant")) {
			t.Errorf("%s: the error leaks a key: %v", name, err)
		}
		if read := slices.Contains(r.read, "GEMINUS_MUSE_API_KEY"); read != c.read {
			t.Errorf("%s: key read %v, want %v", name, read, c.read)
		}
		neverRead(t, r, "GEMINUS_ANTHROPIC_API_KEY_A", "GEMINUS_ANTHROPIC_API_KEY_B")
		if c.off == "" {
			continue
		}
		delete(kv, "GEMINUS_AI_LIVE")
		r = &recorder{kv: kv}
		ai, err = LoadAI(r.get)
		if err != nil || ai.LiveEnabled() || ai.LiveOff != c.off {
			t.Errorf("%s, switch off: %+v %v, want %q", name, ai, err, c.off)
		}
		neverRead(t, r, "GEMINUS_MUSE_API_KEY")
	}
	// AI_PROVIDER=anthropic naming the Meta approval is the same mismatch.
	kv := map[string]string{"AI_PROVIDER": "anthropic", "AI_POLICY_APPROVAL": "H-029", "AI_MODEL": "muse-spark-1.3", "GEMINUS_AI_LIVE": "on"}
	if _, err := LoadAI(env(kv)); !slices.Equal(codes(t, err), []string{"AI_POLICY_APPROVAL:provider_mismatch"}) {
		t.Errorf("anthropic with H-029: %v", err)
	}
	// The switch needs a live provider and its approval.
	for _, p := range []string{"disabled", "fake"} {
		kv := map[string]string{"AI_PROVIDER": p, "APP_ENV": "test", "GEMINUS_AI_LIVE": "on"}
		if _, err := LoadAI(env(kv)); !slices.Equal(codes(t, err), []string{"GEMINUS_AI_LIVE:invalid"}) {
			t.Errorf("%s with the switch: %v", p, err)
		}
	}
}

// The photo path's providers (H-033: SAM 3.1 and Muse Spark; Claude as the
// fallback): AI_VISION_PROVIDER unset follows AI_PROVIDER; disabled turns
// the photo path off; fake is for development and tests; the other live
// provider needs its own approval and model (AI_VISION_POLICY_APPROVAL,
// AI_VISION_MODEL), with its own key and the same switch. A provider whose
// approval names no image (H-018) leaves the photo path off
// (images_not_approved) without stopping the proposals.
func TestTheVisionPath(t *testing.T) {
	h018 := map[string]string{"AI_PROVIDER": "anthropic", "AI_POLICY_APPROVAL": "H-018", "AI_MODEL": "claude-opus-5-5",
		"GEMINUS_ANTHROPIC_API_KEY_A": keyA, "GEMINUS_ANTHROPIC_API_KEY_B": keyB, "GEMINUS_AI_LIVE": "on"}
	ai, err := LoadAI(env(h018))
	if err != nil || !ai.LiveEnabled() || ai.Vision.Provider != AIProviderAnthropic || ai.Vision.LiveEnabled() ||
		ai.Vision.LiveOff != LiveOffImagesNotApproved || len(ai.Vision.Credentials) != 0 {
		t.Errorf("anthropic: %+v %v", ai.Vision, err)
	}
	// Claude for proposals, Meta for the photo path.
	mixed := maps.Clone(h018)
	maps.Copy(mixed, map[string]string{"AI_VISION_PROVIDER": "meta", "AI_VISION_POLICY_APPROVAL": "H-029", "AI_VISION_MODEL": "muse-spark-1.3",
		"GEMINUS_MUSE_API_KEY": museKey})
	r := &recorder{kv: mixed}
	ai, err = LoadAI(r.get)
	if err != nil || !ai.LiveEnabled() || ai.Approval.ID() != "H-018" || len(ai.Credentials) != 2 {
		t.Fatalf("mixed proposals: %+v %v", ai, err)
	}
	if v := ai.Vision; !v.LiveEnabled() || v.Provider != AIProviderMeta || v.Approval.ID() != "H-029" || v.Model != "muse-spark-1.3" ||
		v.SegmentationModel != "sam-3.1" || len(v.Credentials) != 1 || v.Credentials[0].Label != "meta" {
		t.Errorf("mixed vision: %+v", v)
	}
	if want := spend.DefaultLimits(100 * spend.NanoPerUSD); ai.Limits != want {
		t.Errorf("limits %+v", ai.Limits)
	}
	// Meta for proposals, Claude for the photo path: the approval names no
	// image, so the photo path stays off; the Anthropic keys are not read.
	other := metaEnv()
	maps.Copy(other, map[string]string{"AI_VISION_PROVIDER": "anthropic", "AI_VISION_POLICY_APPROVAL": "H-018", "AI_VISION_MODEL": "claude-opus-5-5"})
	r = &recorder{kv: other}
	ai, err = LoadAI(r.get)
	if err != nil || !ai.LiveEnabled() || ai.Vision.LiveEnabled() || ai.Vision.LiveOff != LiveOffImagesNotApproved {
		t.Errorf("meta, then anthropic for photos: %+v %v", ai.Vision, err)
	}
	for name, c := range map[string]struct {
		env  map[string]string
		want []string
		off  string
		prov AIProvider
	}{
		"disabled":                   {map[string]string{"AI_VISION_PROVIDER": "disabled"}, nil, "", AIProviderDisabled},
		"the same provider, named":   {map[string]string{"AI_VISION_PROVIDER": "meta"}, nil, "", AIProviderMeta},
		"an unknown provider":        {map[string]string{"AI_VISION_PROVIDER": "openai"}, []string{"AI_VISION_PROVIDER:invalid"}, "", ""},
		"the fake in production":     {map[string]string{"AI_VISION_PROVIDER": "fake", "APP_ENV": "production"}, []string{"AI_VISION_PROVIDER:invalid"}, "", ""},
		"an approval to follow":      {map[string]string{"AI_VISION_POLICY_APPROVAL": "H-029"}, []string{"AI_VISION_POLICY_APPROVAL:invalid"}, "", ""},
		"a model to follow":          {map[string]string{"AI_VISION_MODEL": "muse-spark-1.3"}, []string{"AI_VISION_MODEL:invalid"}, "", ""},
		"another provider, no model": {map[string]string{"AI_VISION_PROVIDER": "anthropic", "AI_VISION_POLICY_APPROVAL": "H-018"}, []string{"AI_VISION_MODEL:required"}, "", ""},
		"another provider, no approval": {map[string]string{"AI_VISION_PROVIDER": "anthropic", "AI_VISION_MODEL": "claude-opus-5-5"},
			[]string{"AI_VISION_POLICY_APPROVAL:required"}, "", ""},
		"another provider, its approval wrong": {map[string]string{"AI_VISION_PROVIDER": "anthropic", "AI_VISION_POLICY_APPROVAL": "H-029", "AI_VISION_MODEL": "muse-spark-1.3"},
			[]string{"AI_VISION_POLICY_APPROVAL:provider_mismatch"}, "", ""},
	} {
		kv := metaEnv()
		maps.Copy(kv, c.env)
		ai, err := LoadAI(env(kv))
		var got []string
		if err != nil {
			got = codes(t, err)
		}
		if !slices.Equal(got, c.want) {
			t.Errorf("%s: %v, want %v", name, got, c.want)
			continue
		}
		if c.want == nil && (ai.Vision.Provider != c.prov || ai.Vision.LiveOff != c.off) {
			t.Errorf("%s: vision %+v", name, ai.Vision)
		}
	}
	// The fake photo path in tests, beside a disabled provider.
	if ai, err := LoadAI(env(map[string]string{"APP_ENV": "test", "AI_VISION_PROVIDER": "fake"})); err != nil || ai.Vision.Provider != AIProviderFake {
		t.Errorf("fake vision: %+v %v", ai.Vision, err)
	}
	// Defaults: nothing set is disabled everywhere; the fake follows.
	if ai, _ := LoadAI(env(nil)); ai.Vision.Provider != AIProviderDisabled {
		t.Errorf("default vision %q", ai.Vision.Provider)
	}
	if ai, _ := LoadAI(env(map[string]string{"AI_PROVIDER": "fake", "APP_ENV": "test"})); ai.Vision.Provider != AIProviderFake {
		t.Errorf("fake vision %q", ai.Vision.Provider)
	}
}
