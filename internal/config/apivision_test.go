package config

import (
	"fmt"
	"maps"
	"slices"
	"strings"
	"testing"
)

// The T-056 follow-up (checks/T-056_VALIDATION.md §9 question 6 and the
// coordinator's answer 6; D-124): the API decides whether it takes photos
// as the worker decides whether its photo path is on, from the photo path's
// settings that are not secret, AI_VISION_PROVIDER, AI_VISION_POLICY_APPROVAL
// and AI_VISION_MODEL, and the proposals' settings they follow; never from a
// key. One decision for both: for every combination, the API without any
// key and the worker with every key decide alike.

// apiClaudeMetaPhotos is the API's settings of Claude for the proposals and
// Meta for the photo path, the switch on: no key.
func apiClaudeMetaPhotos() map[string]string {
	kv := apiClaude()
	maps.Copy(kv, map[string]string{"AI_VISION_PROVIDER": "meta", "AI_VISION_POLICY_APPROVAL": "H-029", "AI_VISION_MODEL": "muse-spark-1.3"})
	return kv
}

// workerKeys are every key the worker could read, so that only the keys
// set the API and the worker apart.
var workerKeys = map[string]string{"GEMINUS_ANTHROPIC_API_KEY_A": keyA, "GEMINUS_ANTHROPIC_API_KEY_B": keyB, "GEMINUS_MUSE_API_KEY": museKey}

// errorCodes is codes for any error, nil for none.
func errorCodes(t *testing.T, err error) []string {
	t.Helper()
	if err == nil {
		return nil
	}
	return codes(t, err)
}

// photoOutcome names what the API decided of photos (for the sweep's
// coverage).
func photoOutcome(a APIAI) string {
	v := a.Vision
	switch {
	case v.Provider == AIProviderFake:
		return "the fake"
	case v.LiveEnabled() && v.Provider == a.Provider:
		return "on: the proposals' provider"
	case v.LiveEnabled():
		return "on: another provider"
	case !v.Provider.live():
		return "off: " + string(v.Provider)
	}
	return "off: " + v.LiveOff
}

// For every combination of the photo path's settings and the proposals'
// settings they depend on (the provider, its approval and model, the
// switch, APP_ENV), the API's LoadAPIAI without any key and the worker's
// LoadAI with every key agree on the photo path: the same errors (both
// refuse to start), or the same provider, on or off for the same typed
// reason, with the same approval, model and segmentation model; and on the
// proposals, as TestTheAPIsGateIsTheWorkers shows for the records'
// changes. The API reads no other variable and no key's value.
func TestTheAPIsPhotoDecisionIsTheWorkers(t *testing.T) {
	texts := map[string]map[string]string{
		"nothing set":             {},
		"disabled":                {"AI_PROVIDER": "disabled"},
		"the fake":                {"AI_PROVIDER": "fake"},
		"claude":                  {"AI_PROVIDER": "anthropic", "AI_POLICY_APPROVAL": "H-018", "AI_MODEL": "claude-opus-5-5"},
		"claude, no approval":     {"AI_PROVIDER": "anthropic"},
		"meta":                    {"AI_PROVIDER": "meta", "AI_POLICY_APPROVAL": "H-029", "AI_MODEL": "muse-spark-1.3"},
		"meta, another model":     {"AI_PROVIDER": "meta", "AI_POLICY_APPROVAL": "H-029", "AI_MODEL": "muse-spark-1.3-contributor"},
		"meta, Claude's approval": {"AI_PROVIDER": "meta", "AI_POLICY_APPROVAL": "H-018", "AI_MODEL": "muse-spark-1.3"},
	}
	switches := []string{"", "off", "on"}
	appEnvs := []string{"test", "production"}
	providers := []string{"", "disabled", "fake", "anthropic", "meta", "Meta"}
	approvals := []string{"", "H-018", "H-029", "H-033"}
	models := []string{"", "claude-opus-5-5", "muse-spark-1.3", "muse-spark-1.3-contributor"}
	seen := map[string]int{}
	compared := 0
	for text, base := range texts {
		for _, sw := range switches {
			for _, appEnv := range appEnvs {
				for _, vp := range providers {
					for _, va := range approvals {
						for _, vm := range models {
							settings := maps.Clone(base)
							settings["APP_ENV"] = appEnv
							for k, v := range map[string]string{"GEMINUS_AI_LIVE": sw, "AI_VISION_PROVIDER": vp, "AI_VISION_POLICY_APPROVAL": va, "AI_VISION_MODEL": vm} {
								if v != "" {
									settings[k] = v
								}
							}
							worker := maps.Clone(settings)
							maps.Copy(worker, workerKeys)
							name := fmt.Sprintf("%s, switch %q, %s, photo %q/%q/%q", text, sw, appEnv, vp, va, vm)
							r := &recorder{kv: settings}
							api, aerr := LoadAPIAI(r.get)
							w, werr := LoadAI(env(worker))
							compared++
							readOnly(t, name, r)
							if a, b := errorCodes(t, aerr), errorCodes(t, werr); !slices.Equal(a, b) {
								t.Errorf("%s: the API %v, the worker %v", name, a, b)
								continue
							}
							if aerr != nil {
								for _, leak := range []string{keyA, keyB, museKey} {
									if strings.Contains(aerr.Error(), leak) {
										t.Errorf("%s: the error leaks a key", name)
									}
								}
								if api != (APIAI{}) {
									t.Errorf("%s: refused, yet %+v", name, api)
								}
								seen["refused to start"]++
								continue
							}
							v, wv := api.Vision, w.Vision
							if v.Provider != wv.Provider || v.LiveEnabled() != wv.LiveEnabled() || v.LiveOff != wv.LiveOff ||
								v.Approval.ID() != wv.Approval.ID() || v.Model != wv.Model || v.SegmentationModel != wv.SegmentationModel {
								t.Errorf("%s: the API's photo path %+v, the worker's %+v", name, v, wv)
							}
							if api.Provider != w.Provider || api.LiveEnabled() != w.LiveEnabled() || api.LiveOff != w.LiveOff ||
								api.Approval.ID() != w.Approval.ID() || api.Model != w.Model {
								t.Errorf("%s: the API %+v, the worker %+v", name, api, w)
							}
							seen[photoOutcome(api)]++
						}
					}
				}
			}
		}
	}
	if want := len(texts) * len(switches) * len(appEnvs) * len(providers) * len(approvals) * len(models); compared != want {
		t.Errorf("%d comparisons, want %d", compared, want)
	}
	// Every kind of decision is among them (the sweep is not vacuous).
	for _, want := range []string{"refused to start", "the fake", "off: disabled", "on: the proposals' provider", "on: another provider",
		"off: " + LiveOffImagesNotApproved, "off: " + LiveOffSwitch, "off: " + LiveOffNoApproval, "off: " + LiveOffModelMismatch,
		"off: " + LiveOffProviderMismatch} {
		if seen[want] == 0 {
			t.Errorf("no combination decided %q: %v", want, seen)
		}
	}
	t.Logf("%d combinations: %v", compared, seen)
}

// What the API decides of photos, setting by setting: the photo path
// follows the proposals' provider unless AI_VISION_PROVIDER names another
// (or disabled, or, in development and tests, the fake); a live one is on
// only with an approval of its own that names images (H-029; H-018 names
// none: images_not_approved), its model and the switch; the proposals are
// decided on their own.
func TestAPIAIReadsThePhotoSettings(t *testing.T) {
	with := withEnv
	for name, c := range map[string]struct {
		env      map[string]string
		textLive bool       // the API accepts live proposals in words
		photo    AIProvider // the photo path's provider
		off      string     // why a live photo path is off ("" for on, or not live)
		approval string     // the photo path's approval, when on
	}{
		"nothing set":                       {map[string]string{}, false, AIProviderDisabled, "", ""},
		"claude: no image approved":         {apiClaude(), true, AIProviderAnthropic, LiveOffImagesNotApproved, ""},
		"claude for words, meta for photos": {apiClaudeMetaPhotos(), true, AIProviderMeta, "", "H-029"},
		"meta for both":                     {apiMeta(), true, AIProviderMeta, "", "H-029"},
		"meta, its photo path named":        {with(apiMeta(), "AI_VISION_PROVIDER", "meta"), true, AIProviderMeta, "", "H-029"},
		"meta, the photo path disabled":     {with(apiMeta(), "AI_VISION_PROVIDER", "disabled"), true, AIProviderDisabled, "", ""},
		"meta, the switch off":              {with(apiMeta(), "GEMINUS_AI_LIVE", "off"), false, AIProviderMeta, LiveOffSwitch, ""},
		"meta, another model":               {with(apiMeta(), "GEMINUS_AI_LIVE", "", "AI_MODEL", "muse-spark-1.2"), false, AIProviderMeta, LiveOffModelMismatch, ""},
		"meta for words, claude for photos": {with(apiMeta(), "AI_VISION_PROVIDER", "anthropic", "AI_VISION_POLICY_APPROVAL", "H-018",
			"AI_VISION_MODEL", "claude-opus-5-5"), true, AIProviderAnthropic, LiveOffImagesNotApproved, ""},
		"claude, meta for photos, the switch off": {with(apiClaudeMetaPhotos(), "GEMINUS_AI_LIVE", ""), false, AIProviderMeta, LiveOffSwitch, ""},
		"claude, meta for photos, no approval, the switch off": {with(apiClaude(), "GEMINUS_AI_LIVE", "", "AI_VISION_PROVIDER", "meta"),
			false, AIProviderMeta, LiveOffNoApproval, ""},
		"claude, meta for photos with Claude's approval, the switch off": {with(apiClaudeMetaPhotos(), "GEMINUS_AI_LIVE", "off",
			"AI_VISION_POLICY_APPROVAL", "H-018"), false, AIProviderMeta, LiveOffProviderMismatch, ""},
		"the fake":                  {map[string]string{"AI_PROVIDER": "fake", "APP_ENV": "test"}, false, AIProviderFake, "", ""},
		"the fake, photos disabled": {map[string]string{"AI_PROVIDER": "fake", "APP_ENV": "test", "AI_VISION_PROVIDER": "disabled"}, false, AIProviderDisabled, "", ""},
		"disabled, fake photos":     {map[string]string{"APP_ENV": "development", "AI_VISION_PROVIDER": "fake"}, false, AIProviderFake, "", ""},
	} {
		r := &recorder{kv: c.env}
		a, err := LoadAPIAI(r.get)
		if err != nil {
			t.Errorf("%s: %v", name, err)
			continue
		}
		readOnly(t, name, r)
		if !slices.Contains(r.read, "AI_VISION_PROVIDER") {
			t.Errorf("%s: the photo path's provider was not read", name)
		}
		v := a.Vision
		if a.LiveEnabled() != c.textLive || v.Provider != c.photo || v.LiveOff != c.off || v.Approval.ID() != c.approval {
			t.Errorf("%s: %+v, the photo path %+v", name, a, v)
		}
		if on := c.approval != ""; v.LiveEnabled() != on || (on && (v.Model != "muse-spark-1.3" || v.SegmentationModel != "sam-3.1")) ||
			(!on && (v.Approval.Valid() || v.Model != "" || v.SegmentationModel != "")) {
			t.Errorf("%s: the photo path %+v", name, v)
		}
	}
	// What would stop the worker stops the API, with the same typed codes,
	// whether or not the photo path's provider is the proposals'.
	for name, c := range map[string]struct {
		env  map[string]string
		want []string
	}{
		"an unknown photo provider":      {with(apiMeta(), "AI_VISION_PROVIDER", "openai"), []string{"AI_VISION_PROVIDER:invalid"}},
		"a capitalised photo provider":   {with(apiMeta(), "AI_VISION_PROVIDER", "Meta"), []string{"AI_VISION_PROVIDER:invalid"}},
		"fake photos in production":      {with(apiMeta(), "AI_VISION_PROVIDER", "fake"), []string{"AI_VISION_PROVIDER:invalid"}},
		"fake photos without APP_ENV":    {map[string]string{"AI_VISION_PROVIDER": "fake"}, []string{"AI_VISION_PROVIDER:invalid"}},
		"a photo approval to follow":     {with(apiMeta(), "AI_VISION_POLICY_APPROVAL", "H-029"), []string{"AI_VISION_POLICY_APPROVAL:invalid"}},
		"a photo model to follow":        {with(apiMeta(), "AI_VISION_MODEL", "muse-spark-1.3"), []string{"AI_VISION_MODEL:invalid"}},
		"a photo approval while off":     {map[string]string{"AI_VISION_PROVIDER": "disabled", "AI_VISION_POLICY_APPROVAL": "H-029"}, []string{"AI_VISION_POLICY_APPROVAL:invalid"}},
		"meta photos, no approval":       {with(apiClaudeMetaPhotos(), "AI_VISION_POLICY_APPROVAL", ""), []string{"AI_VISION_POLICY_APPROVAL:required"}},
		"meta photos, no model":          {with(apiClaudeMetaPhotos(), "AI_VISION_MODEL", ""), []string{"AI_VISION_MODEL:required"}},
		"meta photos, the Contributor":   {with(apiClaudeMetaPhotos(), "AI_VISION_MODEL", "muse-spark-1.3-contributor"), []string{"AI_VISION_MODEL:model_mismatch"}},
		"meta photos, Claude's approval": {with(apiClaudeMetaPhotos(), "AI_VISION_POLICY_APPROVAL", "H-018"), []string{"AI_VISION_POLICY_APPROVAL:provider_mismatch"}},
		"meta photos, unrecorded":        {with(apiClaudeMetaPhotos(), "AI_VISION_POLICY_APPROVAL", "H-033"), []string{"AI_VISION_POLICY_APPROVAL:not_approved"}},
		"meta photos unrecorded, off":    {with(apiClaudeMetaPhotos(), "AI_VISION_POLICY_APPROVAL", "H-033", "GEMINUS_AI_LIVE", ""), []string{"AI_VISION_POLICY_APPROVAL:not_approved"}},
		"claude photos, no approval":     {with(apiMeta(), "AI_VISION_PROVIDER", "anthropic"), []string{"AI_VISION_POLICY_APPROVAL:required"}},
		"claude photos, Meta's approval": {with(apiMeta(), "AI_VISION_PROVIDER", "anthropic", "AI_VISION_POLICY_APPROVAL", "H-029", "AI_VISION_MODEL", "muse-spark-1.3"),
			[]string{"AI_VISION_POLICY_APPROVAL:provider_mismatch"}},
		"both wrong, after the proposals'": {with(apiClaudeMetaPhotos(), "AI_MODEL", "", "AI_VISION_MODEL", ""), []string{"AI_MODEL:required", "AI_VISION_MODEL:required"}},
	} {
		a, err := LoadAPIAI(env(c.env))
		if got := errorCodes(t, err); !slices.Equal(got, c.want) || a != (APIAI{}) {
			t.Errorf("%s: %v %+v, want %v", name, got, a, c.want)
		}
		worker := maps.Clone(c.env)
		maps.Copy(worker, workerKeys)
		if _, werr := LoadAI(env(worker)); !slices.Equal(errorCodes(t, werr), c.want) {
			t.Errorf("%s: the worker: %v, want %v", name, werr, c.want)
		}
	}
}

// APIVision.LiveEnabled holds only for a valid approval of the photo path's
// own live provider that names images, however the value was made.
func TestAPIVisionLiveEnabled(t *testing.T) {
	meta, err := LoadAPIAI(env(apiMeta()))
	if err != nil || !meta.Vision.LiveEnabled() {
		t.Fatalf("meta: %+v %v", meta.Vision, err)
	}
	claude, err := LoadAPIAI(env(apiClaude()))
	if err != nil || !claude.LiveEnabled() {
		t.Fatalf("claude: %+v %v", claude, err)
	}
	h029, h018 := meta.Vision.Approval, claude.Approval
	if !(APIVision{Provider: AIProviderMeta, Approval: h029}).LiveEnabled() {
		t.Error("H-029 does not turn Meta's photo path on")
	}
	for name, v := range map[string]APIVision{
		"the zero value":              {},
		"meta, no approval":           {Provider: AIProviderMeta, Model: "muse-spark-1.3"},
		"anthropic, Meta's approval":  {Provider: AIProviderAnthropic, Approval: h029},
		"anthropic, H-018 (no image)": {Provider: AIProviderAnthropic, Approval: h018},
		"meta, Claude's approval":     {Provider: AIProviderMeta, Approval: h018},
		"the fake with an approval":   {Provider: AIProviderFake, Approval: h029},
		"disabled with an approval":   {Provider: AIProviderDisabled, Approval: h029},
	} {
		if v.LiveEnabled() {
			t.Errorf("%s: live", name)
		}
	}
}
