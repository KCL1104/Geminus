package main

import (
	"fmt"
	"slices"
	"testing"

	"github.com/kcl1104/geminus/internal/ai/provider"
	"github.com/kcl1104/geminus/internal/config"
)

// The T-056 follow-up (checks/T-056_VALIDATION.md, the coordinator's answer
// 6; D-124): the API takes a photo exactly when this worker's photo path
// would. For every combination of the photo path's settings and the
// proposals' settings they follow, the API's decision from the settings
// alone (config.LoadAPIAI, without any key: the fake, or a live photo
// provider whose approval names images, its model and the switch, which is
// when cmd/api's photoAvailability is Available) is whether the judge this
// worker builds from the same settings and every key (photoPath) takes
// images, which the generator asks before anything is spent
// (AI_IMAGE_UNSUPPORTED otherwise); or both refuse to start. Nothing is
// called: the live clients are built only.
func TestTheAPIsPhotoDecisionIsThePhotoPaths(t *testing.T) {
	keys := []string{"GEMINUS_ANTHROPIC_API_KEY_A", "sk-ant-test-fake-aaaaaaaaaaaa", "GEMINUS_ANTHROPIC_API_KEY_B", "sk-ant-test-fake-bbbbbbbbbbbb",
		"GEMINUS_MUSE_API_KEY", museKey}
	texts := map[string][]string{
		"nothing set":         nil,
		"disabled":            {"AI_PROVIDER", "disabled"},
		"the fake":            {"AI_PROVIDER", "fake"},
		"claude":              {"AI_PROVIDER", "anthropic", "AI_POLICY_APPROVAL", "H-018", "AI_MODEL", "claude-opus-5-5"},
		"meta":                {"AI_PROVIDER", "meta", "AI_POLICY_APPROVAL", "H-029", "AI_MODEL", "muse-spark-1.3"},
		"meta, another model": {"AI_PROVIDER", "meta", "AI_POLICY_APPROVAL", "H-029", "AI_MODEL", "muse-spark-1.2"},
	}
	photos := map[string][]string{
		"following":               nil,
		"disabled":                {"AI_VISION_PROVIDER", "disabled"},
		"the fake":                {"AI_VISION_PROVIDER", "fake"},
		"meta":                    {"AI_VISION_PROVIDER", "meta", "AI_VISION_POLICY_APPROVAL", "H-029", "AI_VISION_MODEL", "muse-spark-1.3"},
		"meta, no approval":       {"AI_VISION_PROVIDER", "meta"},
		"meta, Claude's approval": {"AI_VISION_PROVIDER", "meta", "AI_VISION_POLICY_APPROVAL", "H-018", "AI_VISION_MODEL", "muse-spark-1.3"},
		"claude":                  {"AI_VISION_PROVIDER", "anthropic", "AI_VISION_POLICY_APPROVAL", "H-018", "AI_VISION_MODEL", "claude-opus-5-5"},
	}
	seen := map[string]int{}
	for text, ts := range texts {
		for photo, ps := range photos {
			for _, sw := range []string{"", "off", "on"} {
				for _, appEnv := range []string{"test", "production"} {
					name := fmt.Sprintf("%s, photos %s, switch %q, %s", text, photo, sw, appEnv)
					settings := append(append(slices.Clone(ts), ps...), "GEMINUS_AI_LIVE", sw, "APP_ENV", appEnv)
					api, aerr := config.LoadAPIAI(with(settings...))
					w, werr := config.LoadAI(with(append(slices.Clone(settings), keys...)...))
					if (aerr == nil) != (werr == nil) {
						t.Errorf("%s: the API %v, the worker %v", name, aerr, werr)
						continue
					}
					if aerr != nil {
						seen["both refuse to start"]++
						continue
					}
					p, err := photoPath(w, provider.DefaultBudget(), unusedLedger(t), "test-worker")
					if err != nil {
						t.Errorf("%s: the photo path: %v", name, err)
						continue
					}
					takes := p.Judge.AcceptsImages()
					apiTakes := api.Vision.Provider == config.AIProviderFake || api.Vision.LiveEnabled()
					if takes != apiTakes {
						t.Errorf("%s: the worker's judge takes images: %v; the API takes photos: %v (%+v)", name, takes, apiTakes, api.Vision)
					}
					switch {
					case takes && w.Vision.Provider == config.AIProviderFake:
						seen["both take photos: the fake"]++
					case takes:
						seen["both take photos: live"]++
					default:
						seen["neither takes photos"]++
					}
				}
			}
		}
	}
	for _, want := range []string{"both refuse to start", "both take photos: the fake", "both take photos: live", "neither takes photos"} {
		if seen[want] == 0 {
			t.Errorf("no combination: %s (%v)", want, seen)
		}
	}
	t.Logf("%v", seen)
}
