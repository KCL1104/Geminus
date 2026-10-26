package config

import (
	"maps"
	"slices"
	"strings"
	"testing"
)

// T-053 I5, the T-055 follow-up (the coordinator's decision): the API
// decides whether it accepts AI proposals from non-secret settings only,
// AI_PROVIDER, AI_POLICY_APPROVAL, AI_MODEL and GEMINUS_AI_LIVE, with the
// worker's live gate but for the keys: it never reads or requires one, and
// any AI key variable in its environment stops it, the error naming the
// variable, never its value.

// apiKeyNames are the AI key variables the API refuses (hard-coded here, so
// a name dropped from the code's list fails).
var apiKeyNames = []string{"GEMINUS_ANTHROPIC_API_KEY_A", "GEMINUS_ANTHROPIC_API_KEY_B", "GEMINUS_MUSE_API_KEY", "GEMINUS_META_API_KEY",
	"ANTHROPIC_API_KEY", "ANTHROPIC_AUTH_TOKEN", "AI_API_KEY", "MODEL_API_KEY"}

// apiSettings are all the API's AI loader may read besides the key
// variables, whose presence it checks.
var apiSettings = []string{"AI_PROVIDER", "AI_POLICY_APPROVAL", "AI_MODEL", "GEMINUS_AI_LIVE", "APP_ENV"}

// apiMeta and apiClaude are the API's settings of a live provider with the
// switch on: the record, its model, the switch; no key.
func apiMeta() map[string]string {
	return map[string]string{"AI_PROVIDER": "meta", "AI_POLICY_APPROVAL": "H-029", "AI_MODEL": "muse-spark-1.3", "GEMINUS_AI_LIVE": "on",
		"APP_ENV": "production"}
}

func apiClaude() map[string]string {
	return map[string]string{"AI_PROVIDER": "anthropic", "AI_POLICY_APPROVAL": "H-018", "AI_MODEL": "claude-opus-5-5", "GEMINUS_AI_LIVE": "on",
		"APP_ENV": "production"}
}

// readOnly fails for every variable r was asked for that is neither an API
// setting nor a key variable (whose presence only is checked).
func readOnly(t *testing.T, name string, r *recorder) {
	t.Helper()
	for _, k := range r.read {
		if !slices.Contains(apiSettings, k) && !slices.Contains(apiKeyNames, k) {
			t.Errorf("%s: the API read %s", name, k)
		}
	}
}

// With the switch on and the record's model, the API accepts proposals for
// both live providers without any key; the worker's LoadAI, with the same
// settings, still needs the keys (the worker side is unchanged). The worker's
// other settings (the photo path, the limits, the budget) are not the API's:
// it does not read them, even when they would not do for the worker.
func TestAPIAIIsKeyFree(t *testing.T) {
	for name, c := range map[string]struct {
		env             map[string]string
		provider        AIProvider
		approval, model string
		workerNeeds     []string
	}{
		"meta":      {apiMeta(), AIProviderMeta, "H-029", "muse-spark-1.3", []string{"GEMINUS_MUSE_API_KEY:required"}},
		"anthropic": {apiClaude(), AIProviderAnthropic, "H-018", "claude-opus-5-5", []string{"GEMINUS_ANTHROPIC_API_KEY_A:required", "GEMINUS_ANTHROPIC_API_KEY_B:required"}},
	} {
		kv := maps.Clone(c.env)
		maps.Copy(kv, map[string]string{"AI_VISION_PROVIDER": "openai", "AI_VISION_MODEL": "x", "AI_MONTHLY_LIMIT_USD": "1000000",
			"AI_MAX_RETRIES": "-1"})
		r := &recorder{kv: kv}
		a, err := LoadAPIAI(r.get)
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		if !a.LiveEnabled() || a.Provider != c.provider || a.LiveOff != "" || a.Approval.ID() != c.approval || a.Approval.Provider() != string(c.provider) ||
			a.Model != c.model {
			t.Errorf("%s: %+v", name, a)
		}
		readOnly(t, name, r)
		if _, err := LoadAI(env(c.env)); !slices.Equal(codes(t, err), c.workerNeeds) {
			t.Errorf("%s: the worker's gate: %v, want %v", name, err, c.workerNeeds)
		}
	}
	// LiveEnabled holds only for a valid approval of the provider itself.
	a, err := LoadAPIAI(env(apiMeta()))
	if err != nil {
		t.Fatal(err)
	}
	for name, v := range map[string]APIAI{
		"the zero value":            {},
		"no approval":               {Provider: AIProviderMeta, Model: "muse-spark-1.3"},
		"another provider":          {Provider: AIProviderAnthropic, Approval: a.Approval, Model: a.Model},
		"the fake with an approval": {Provider: AIProviderFake, Approval: a.Approval},
		"disabled with an approval": {Provider: AIProviderDisabled, Approval: a.Approval},
	} {
		if v.LiveEnabled() {
			t.Errorf("%s: live", name)
		}
	}
}

// What keeps the API from accepting live proposals is what keeps the
// worker's live AI off (the same record checks): with the switch off the
// API starts and LiveOff says why; with it on the API refuses to start,
// with the condition's typed code, like the worker. No error names a value.
func TestAPIAIMismatches(t *testing.T) {
	for name, c := range map[string]struct {
		base func() map[string]string
		env  map[string]string
		off  string   // LiveOff with the switch off ("" when an error then too)
		on   []string // the codes with the switch on
	}{
		"meta, the Contributor model":       {apiMeta, map[string]string{"AI_MODEL": "muse-spark-1.3-contributor"}, LiveOffModelMismatch, []string{"AI_MODEL:model_mismatch"}},
		"meta, another version":             {apiMeta, map[string]string{"AI_MODEL": "muse-spark-1.2"}, LiveOffModelMismatch, []string{"AI_MODEL:model_mismatch"}},
		"meta, Claude's model":              {apiMeta, map[string]string{"AI_MODEL": "claude-opus-5-5"}, LiveOffModelMismatch, []string{"AI_MODEL:model_mismatch"}},
		"meta, no model":                    {apiMeta, map[string]string{"AI_MODEL": ""}, LiveOffModelMismatch, []string{"AI_MODEL:required"}},
		"meta, the Anthropic approval":      {apiMeta, map[string]string{"AI_POLICY_APPROVAL": "H-018"}, LiveOffProviderMismatch, []string{"AI_POLICY_APPROVAL:provider_mismatch"}},
		"meta, an unrecorded approval":      {apiMeta, map[string]string{"AI_POLICY_APPROVAL": "H-033"}, "", []string{"AI_POLICY_APPROVAL:not_approved"}},
		"meta, a lowercase approval":        {apiMeta, map[string]string{"AI_POLICY_APPROVAL": "h-029"}, "", []string{"AI_POLICY_APPROVAL:not_approved"}},
		"meta, no approval":                 {apiMeta, map[string]string{"AI_POLICY_APPROVAL": ""}, LiveOffNoApproval, []string{"GEMINUS_AI_LIVE:invalid"}},
		"anthropic, another model":          {apiClaude, map[string]string{"AI_MODEL": "claude-sonnet-5-5"}, LiveOffModelMismatch, []string{"AI_MODEL:model_mismatch"}},
		"anthropic, a dated model":          {apiClaude, map[string]string{"AI_MODEL": "claude-opus-5-5-20260401"}, LiveOffModelMismatch, []string{"AI_MODEL:model_mismatch"}},
		"anthropic, no model":               {apiClaude, map[string]string{"AI_MODEL": ""}, LiveOffModelMismatch, []string{"AI_MODEL:required"}},
		"anthropic, the Meta approval":      {apiClaude, map[string]string{"AI_POLICY_APPROVAL": "H-029", "AI_MODEL": "muse-spark-1.3"}, LiveOffProviderMismatch, []string{"AI_POLICY_APPROVAL:provider_mismatch"}},
		"anthropic, an unrecorded approval": {apiClaude, map[string]string{"AI_POLICY_APPROVAL": "H-017"}, "", []string{"AI_POLICY_APPROVAL:not_approved"}},
		"anthropic, no approval":            {apiClaude, map[string]string{"AI_POLICY_APPROVAL": ""}, LiveOffNoApproval, []string{"GEMINUS_AI_LIVE:invalid"}},
	} {
		kv := c.base()
		maps.Copy(kv, c.env)
		a, err := LoadAPIAI(env(kv))
		if got := codes(t, err); !slices.Equal(got, c.on) || a.LiveEnabled() || a != (APIAI{}) {
			t.Errorf("%s, switch on: %v %+v, want %v", name, got, a, c.on)
		}
		for _, sw := range []string{"", "off"} {
			kv["GEMINUS_AI_LIVE"] = sw
			a, err := LoadAPIAI(env(kv))
			if c.off == "" {
				if got := codes(t, err); !slices.Equal(got, c.on) {
					t.Errorf("%s, switch %q: %v, want %v", name, sw, got, c.on)
				}
				continue
			}
			if err != nil || a.LiveEnabled() || a.LiveOff != c.off || a.Approval.Valid() || a.Model != "" || a.Provider != AIProvider(kv["AI_PROVIDER"]) {
				t.Errorf("%s, switch %q: %+v %v, want %q", name, sw, a, err, c.off)
			}
		}
	}
	// The switch on with a match, then off: the reason is the switch.
	for _, base := range []func() map[string]string{apiMeta, apiClaude} {
		kv := base()
		kv["GEMINUS_AI_LIVE"] = "off"
		if a, err := LoadAPIAI(env(kv)); err != nil || a.LiveEnabled() || a.LiveOff != LiveOffSwitch || a.Approval.Valid() {
			t.Errorf("%s, the switch off: %+v %v", kv["AI_PROVIDER"], a, err)
		}
	}
}

// The provider and the switch are read as LoadAI reads them: disabled is
// the default; the fake only in development and tests; an unknown
// provider, a switch that is not on or off, the switch or an approval
// without a live provider stop the API.
func TestAPIAISelection(t *testing.T) {
	for name, c := range map[string]struct {
		env      map[string]string
		provider AIProvider
		want     []string
	}{
		"nothing set":              {map[string]string{}, AIProviderDisabled, nil},
		"disabled":                 {map[string]string{"AI_PROVIDER": "disabled"}, AIProviderDisabled, nil},
		"disabled, the switch off": {map[string]string{"AI_PROVIDER": "disabled", "GEMINUS_AI_LIVE": "off"}, AIProviderDisabled, nil},
		"fake in tests":            {map[string]string{"AI_PROVIDER": "fake", "APP_ENV": "test"}, AIProviderFake, nil},
		"fake in development":      {map[string]string{"AI_PROVIDER": "fake", "APP_ENV": "development"}, AIProviderFake, nil},
		"fake in production":       {map[string]string{"AI_PROVIDER": "fake", "APP_ENV": "production"}, "", []string{"AI_PROVIDER:invalid"}},
		"fake without APP_ENV":     {map[string]string{"AI_PROVIDER": "fake"}, "", []string{"AI_PROVIDER:invalid"}},
		"an unknown provider":      {map[string]string{"AI_PROVIDER": "openai"}, "", []string{"AI_PROVIDER:invalid"}},
		"a capitalised provider":   {map[string]string{"AI_PROVIDER": "Meta"}, "", []string{"AI_PROVIDER:invalid"}},
		"the switch, disabled":     {map[string]string{"GEMINUS_AI_LIVE": "on"}, "", []string{"GEMINUS_AI_LIVE:invalid"}},
		"the switch, the fake":     {map[string]string{"AI_PROVIDER": "fake", "APP_ENV": "test", "GEMINUS_AI_LIVE": "on"}, "", []string{"GEMINUS_AI_LIVE:invalid"}},
		"the switch and an approval, the fake": {map[string]string{"AI_PROVIDER": "fake", "APP_ENV": "test", "GEMINUS_AI_LIVE": "on", "AI_POLICY_APPROVAL": "H-029"},
			"", []string{"GEMINUS_AI_LIVE:invalid", "AI_POLICY_APPROVAL:invalid"}},
		"an approval, disabled":   {map[string]string{"AI_POLICY_APPROVAL": "H-029"}, "", []string{"AI_POLICY_APPROVAL:invalid"}},
		"an approval, the fake":   {map[string]string{"AI_PROVIDER": "fake", "APP_ENV": "test", "AI_POLICY_APPROVAL": "H-018"}, "", []string{"AI_POLICY_APPROVAL:invalid"}},
		"meta, nothing else":      {map[string]string{"AI_PROVIDER": "meta"}, AIProviderMeta, nil},
		"anthropic, nothing else": {map[string]string{"AI_PROVIDER": "anthropic"}, AIProviderAnthropic, nil},
	} {
		a, err := LoadAPIAI(env(c.env))
		if c.want != nil {
			if got := codes(t, err); !slices.Equal(got, c.want) || a != (APIAI{}) {
				t.Errorf("%s: %v %+v, want %v", name, got, a, c.want)
			}
			continue
		}
		if err != nil || a.Provider != c.provider || a.LiveEnabled() || a.Approval.Valid() || a.Model != "" {
			t.Errorf("%s: %+v %v", name, a, err)
		}
		if want := map[bool]string{true: LiveOffNoApproval, false: ""}[c.provider.live()]; a.LiveOff != want {
			t.Errorf("%s: LiveOff %q, want %q", name, a.LiveOff, want)
		}
	}
	for _, v := range []string{"ON", "true", "1", "yes", "on ", " on", "of"} {
		kv := apiMeta()
		kv["GEMINUS_AI_LIVE"] = v
		if _, err := LoadAPIAI(env(kv)); !slices.Equal(codes(t, err), []string{"GEMINUS_AI_LIVE:invalid"}) {
			t.Errorf("the switch %q: %v", v, err)
		}
	}
}

// Defense in depth: any AI key variable in the API's environment stops it,
// whatever else is set (the default, the fake, a live provider with the
// switch on or off), with a typed code per variable; the error names the
// variable, never its value, and the API accepts nothing.
func TestAPIAIRefusesAnyKey(t *testing.T) {
	const value = "SENTINEL-value-of-a-key-0123456789"
	bases := map[string]map[string]string{
		"nothing set":    {},
		"the fake":       {"AI_PROVIDER": "fake", "APP_ENV": "test"},
		"meta, on":       apiMeta(),
		"meta, off":      func() map[string]string { kv := apiMeta(); delete(kv, "GEMINUS_AI_LIVE"); return kv }(),
		"anthropic, on":  apiClaude(),
		"anthropic, off": func() map[string]string { kv := apiClaude(); kv["GEMINUS_AI_LIVE"] = "off"; return kv }(),
	}
	for base, kv := range bases {
		for _, k := range apiKeyNames {
			env := maps.Clone(kv)
			env[k] = value
			a, err := LoadAPIAI(func(v string) string { return env[v] })
			if got := codes(t, err); !slices.Equal(got, []string{k + ":" + CodeKeyForbidden}) || a != (APIAI{}) {
				t.Errorf("%s, %s: %v %+v", base, k, got, a)
			}
			if err != nil && (strings.Contains(err.Error(), value) || strings.Contains(err.Error(), "SENTINEL") || !strings.Contains(err.Error(), k)) {
				t.Errorf("%s, %s: the error is %q", base, k, err)
			}
		}
	}
	// Every key at once: one error each, in the list's order, before the
	// settings' own.
	all := map[string]string{"AI_PROVIDER": "openai"}
	var want []string
	for _, k := range apiKeyNames {
		all[k] = value
		want = append(want, k+":"+CodeKeyForbidden)
	}
	want = append(want, "AI_PROVIDER:invalid")
	if _, err := LoadAPIAI(env(all)); !slices.Equal(codes(t, err), want) || strings.Contains(err.Error(), value) {
		t.Errorf("every key: %v, want %v", err, want)
	}
}

// LoadAPI is Load for the API: the same configuration with the key-free AI
// settings. The switch on with the record's model and no key is accepted
// (live: the API accepts the proposals) where Load, the worker's, needs the
// key; a key stops it, with the rest of the configuration's problems.
func TestLoadAPI(t *testing.T) {
	kv := apiMeta()
	kv["DATABASE_URL"] = validDB
	cfg, err := LoadAPI(env(kv))
	if err != nil || cfg.AIProvider != AIProviderMeta || !cfg.AILive || cfg.Env != "production" || cfg.DatabaseURL != validDB {
		t.Errorf("LoadAPI: %+v %v", cfg, err)
	}
	if _, err := Load(env(kv)); !slices.Equal(codes(t, err), []string{"GEMINUS_MUSE_API_KEY:required"}) {
		t.Errorf("Load: %v", err)
	}
	kv["GEMINUS_AI_LIVE"] = "off"
	if cfg, err := LoadAPI(env(kv)); err != nil || cfg.AIProvider != AIProviderMeta || cfg.AILive {
		t.Errorf("the switch off: %+v %v", cfg, err)
	}
	kv["GEMINUS_MUSE_API_KEY"], kv["DATABASE_URL"] = museKey, "mysql://db/twin"
	if _, err := LoadAPI(env(kv)); !slices.Equal(codes(t, err), []string{"DATABASE_URL:invalid", "GEMINUS_MUSE_API_KEY:" + CodeKeyForbidden}) ||
		strings.Contains(err.Error(), museKey) {
		t.Errorf("a key and a bad database URL: %v", err)
	}
	if cfg, err := LoadAPI(env(map[string]string{"DATABASE_URL": validDB, "AI_PROVIDER": "fake", "APP_ENV": "test"})); err != nil ||
		cfg.AIProvider != AIProviderFake || cfg.AILive {
		t.Errorf("the fake: %+v %v", cfg, err)
	}
}
