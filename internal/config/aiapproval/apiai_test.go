package aiapproval_test

import (
	"maps"
	"slices"
	"strings"
	"testing"

	"github.com/kcl1104/geminus/internal/config"
	"github.com/kcl1104/geminus/internal/config/aiapproval"
)

// T-053 I5, the T-055 follow-up (the coordinator's decision): the API
// accepts AI proposals from the worker's live gate but for the keys. For
// every change of either record this build holds (H-018, H-029), and the
// model or the switch, the API's config.LoadAPIAI, without any key, and
// the worker's config.LoadAI, with the provider's keys, agree: both on (the
// same approval and model), both off for the same typed reason, or both
// refusing to start with the same codes. Only the tests of this directory
// can replace a record (RecordForTest).
func TestTheAPIsGateIsTheWorkers(t *testing.T) {
	type side struct {
		record   func(*testing.T) aiapproval.Record
		settings map[string]string // the API's: provider, approval, model
		keys     map[string]string // the worker's
		// changes of the record: the record's change and the model the
		// settings then name ("" for the record's).
		changes map[string]func(*aiapproval.Record) string
		// on is the typed reason (or code) each change leaves; "" for on.
		on map[string]string
	}
	shared := map[string]func(*aiapproval.Record) string{
		"as recorded":           func(*aiapproval.Record) string { return "" },
		"no approver":           func(r *aiapproval.Record) string { r.ApprovedBy = ""; return "" },
		"no date":               func(r *aiapproval.Record) string { r.ApprovedAt = ""; return "" },
		"no source":             func(r *aiapproval.Record) string { r.Source = ""; return "" },
		"no credential":         func(r *aiapproval.Record) string { r.Credentials = nil; return "" },
		"customer data":         func(r *aiapproval.Record) string { r.DataPolicy.Inputs = "customer_data"; return "" },
		"raw text in logs":      func(r *aiapproval.Record) string { r.DataPolicy.LogsKeepRawText = true; return "" },
		"the importer's role":   func(r *aiapproval.Record) string { r.DataPolicy.Roles = []string{"modeler", "importer"}; return "" },
		"no budget":             func(r *aiapproval.Record) string { r.MonthlyBudgetUSD = 0; return "" },
		"retention unconfirmed": func(r *aiapproval.Record) string { r.Retention = aiapproval.Retention{}; return "" },
		"retention undated":     func(r *aiapproval.Record) string { r.Retention.At = "2026-13-01"; return "" },
		"spend limits unconfirmed": func(r *aiapproval.Record) string {
			r.SpendLimits = aiapproval.Confirmation{}
			return ""
		},
		"another provider's record": func(r *aiapproval.Record) string { r.Provider = "openai"; return "" },
	}
	sides := map[string]side{
		"anthropic": {
			record:   complete,
			settings: map[string]string{"AI_PROVIDER": "anthropic", "AI_POLICY_APPROVAL": "H-018", "AI_MODEL": "claude-opus-5-5"},
			keys:     map[string]string{"GEMINUS_ANTHROPIC_API_KEY_A": keyA, "GEMINUS_ANTHROPIC_API_KEY_B": keyB},
			changes: map[string]func(*aiapproval.Record) string{
				"an unpriced model":    func(r *aiapproval.Record) string { r.Model = "claude-opus-5"; return "claude-opus-5" },
				"a segmentation model": func(r *aiapproval.Record) string { r.SegmentationModel = "sam-3.1"; return "" },
				"images": func(r *aiapproval.Record) string {
					r.DataPolicy.Inputs = aiapproval.InputsSyntheticDemoImagesAndUserText
					return ""
				},
			},
			on: map[string]string{"as recorded": "", "no approver": "record_incomplete", "no date": "record_incomplete", "no source": "record_incomplete",
				"no credential": "record_incomplete", "customer data": "policy_unsupported", "raw text in logs": "policy_unsupported",
				"the importer's role": "policy_unsupported", "no budget": "budget_missing", "retention unconfirmed": "retention_unconfirmed",
				"retention undated": "retention_unconfirmed", "spend limits unconfirmed": "spend_limits_unconfirmed",
				"another provider's record": "provider_mismatch", "an unpriced model": "model_unpriced", "a segmentation model": "record_incomplete",
				"images": "policy_unsupported"},
		},
		"meta": {
			record:   h029,
			settings: map[string]string{"AI_PROVIDER": "meta", "AI_POLICY_APPROVAL": "H-029", "AI_MODEL": "muse-spark-1.3"},
			keys:     map[string]string{"GEMINUS_MUSE_API_KEY": museKey},
			changes: map[string]func(*aiapproval.Record) string{
				"an unpriced Standard model": func(r *aiapproval.Record) string { r.Model = "muse-spark-1.2"; return "muse-spark-1.2" },
				"the Contributor model": func(r *aiapproval.Record) string {
					r.Model = "muse-spark-1.3-contributor"
					return "muse-spark-1.3-contributor"
				},
				"another segmenter": func(r *aiapproval.Record) string { r.SegmentationModel = "sam-3"; return "" },
				"text only, no segmentation": func(r *aiapproval.Record) string {
					r.SegmentationModel, r.DataPolicy.Inputs = "", aiapproval.InputsSyntheticAndUserText
					return ""
				},
				"Anthropic's arrangement": func(r *aiapproval.Record) string { r.Retention.Arrangement = aiapproval.RetentionStandard; return "" },
			},
			on: map[string]string{"as recorded": "", "no approver": "record_incomplete", "no date": "record_incomplete", "no source": "record_incomplete",
				"no credential": "record_incomplete", "customer data": "policy_unsupported", "raw text in logs": "policy_unsupported",
				"the importer's role": "policy_unsupported", "no budget": "budget_missing", "retention unconfirmed": "retention_unconfirmed",
				"retention undated": "retention_unconfirmed", "spend limits unconfirmed": "", "another provider's record": "provider_mismatch",
				"an unpriced Standard model": "model_unpriced", "the Contributor model": "model_not_standard", "another segmenter": "model_not_standard",
				"text only, no segmentation": "", "Anthropic's arrangement": "retention_unconfirmed"},
		},
	}
	// The settings besides the record's: the switch, and the model they name.
	variants := map[string]map[string]string{
		"the switch on":            {"GEMINUS_AI_LIVE": "on"},
		"the switch off":           {"GEMINUS_AI_LIVE": "off"},
		"the switch unset":         {},
		"another model, on":        {"GEMINUS_AI_LIVE": "on", "AI_MODEL": "some-other-model"},
		"another model, unset":     {"AI_MODEL": "some-other-model"},
		"no model, on":             {"GEMINUS_AI_LIVE": "on", "AI_MODEL": ""},
		"no model, unset":          {"AI_MODEL": ""},
		"no approval named, unset": {"AI_POLICY_APPROVAL": ""},
	}
	compared := 0
	for p, s := range sides {
		changes := maps.Clone(shared)
		maps.Copy(changes, s.changes)
		for change, edit := range changes {
			want, ok := s.on[change]
			if !ok {
				t.Fatalf("%s, %s: no expectation", p, change)
			}
			t.Run(p+"/"+change, func(t *testing.T) {
				r := s.record(t)
				model := edit(&r)
				aiapproval.RecordForTest(t, r)
				for variant, kv := range variants {
					settings := maps.Clone(s.settings)
					if model != "" {
						settings["AI_MODEL"] = model
					}
					maps.Copy(settings, kv)
					apiEnv := maps.Clone(settings)
					workerEnv := maps.Clone(settings)
					maps.Copy(workerEnv, s.keys)
					api, aerr := config.LoadAPIAI(func(k string) string { return apiEnv[k] })
					worker, werr := config.LoadAI(func(k string) string { return workerEnv[k] })
					compared++
					if a, w := codesOf(aerr), codesOf(werr); !slices.Equal(a, w) {
						t.Errorf("%s: the API %v, the worker %v", variant, a, w)
						continue
					}
					if aerr != nil {
						for _, leak := range []string{keyA, keyB, museKey} {
							if strings.Contains(aerr.Error(), leak) {
								t.Errorf("%s: the error leaks a key", variant)
							}
						}
						continue
					}
					if api.LiveEnabled() != worker.LiveEnabled() || api.LiveOff != worker.LiveOff || api.Provider != worker.Provider ||
						api.Approval.ID() != worker.Approval.ID() || api.Model != worker.Model {
						t.Errorf("%s: the API %+v, the worker %+v", variant, api, worker)
					}
				}
				// With the switch on and the settings matching, the record's
				// change alone decides: on, or the typed reason.
				settings := maps.Clone(s.settings)
				if model != "" {
					settings["AI_MODEL"] = model
				}
				settings["GEMINUS_AI_LIVE"] = "on"
				api, err := config.LoadAPIAI(func(k string) string { return settings[k] })
				switch {
				case want == "" && (err != nil || !api.LiveEnabled() || api.Approval.ID() != r.ID || api.Model != r.Model):
					t.Errorf("switch on: %+v %v, want on", api, err)
				case want != "" && (err == nil || !strings.Contains(strings.Join(codesOf(err), ","), want) || api.LiveEnabled()):
					t.Errorf("switch on: %+v %v, want %s", api, err, want)
				}
				delete(settings, "GEMINUS_AI_LIVE")
				off := want
				if want == "" {
					off = config.LiveOffSwitch
				}
				if api, err := config.LoadAPIAI(func(k string) string { return settings[k] }); err != nil || api.LiveEnabled() || api.LiveOff != off {
					t.Errorf("switch unset: %+v %v, want %s", api, err, off)
				}
			})
		}
	}
	if compared < 2*len(shared)*len(variants) {
		t.Errorf("only %d comparisons", compared)
	}
}
