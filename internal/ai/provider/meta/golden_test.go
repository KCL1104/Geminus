package meta_test

import (
	"bytes"
	"context"
	"encoding/json/jsontext"
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"github.com/kcl1104/geminus/internal/ai/locale"
	"github.com/kcl1104/geminus/internal/ai/provider"
	"github.com/kcl1104/geminus/internal/ai/provider/meta"
)

// writeGoldens rewrites testdata/requests from the current code instead of
// comparing with it. Set it only on the code the goldens pin (E1 wrote them
// before tool calls were added, H-060).
const writeGoldens = "GEMINUS_WRITE_REQUEST_GOLDENS"

// roleSealed is a sealed prompt of role whose untrusted block is name: text.
func roleSealed(t *testing.T, role provider.Role, name, text string, images ...provider.Image) provider.Prompt {
	t.Helper()
	v, _ := jsontext.AppendQuote(nil, text)
	p, err := provider.Seal(provider.Prompt{OrganizationID: org, Role: role, TemplateVersion: string(role) + "-golden", Locale: locale.English,
		System: "rules of the " + string(role), Blocks: []provider.Block{{Kind: provider.UntrustedData, Name: name, Content: v}}, Images: images})
	if err != nil {
		t.Fatal(err)
	}
	return p
}

// H-060, slice E1: every request an existing role sends (an edit, a
// scenario, one on the model the person chose, a research call, one with an
// image and its count, a repair, an explanation, a mapping) is byte for byte
// what the adapter sent before tool calls were added. The goldens were
// written from the code before E1 changed it (GEMINUS_WRITE_REQUEST_GOLDENS=1
// on that commit); a change to any of them is a change to a role's request
// and needs its own decision (H-059: behaviour unchanged).
//
// H-068 is such a decision, for Muse Wonder 1.0 only: its proposal requests
// send no output_config and end the system text with the plainjson template
// and the native schema. So scenario-chosen-model (a Muse Wonder scenario)
// was written again on H-068's code, and the wonder-* goldens are new (an
// edit, research mode, a photo and its count, a repair); every Muse Spark
// golden, the analyst's and the importer's are E1's, unchanged.
func TestRoleRequestGoldens(t *testing.T) {
	refused := `{"status":"proposed","summary":"Raise it.","assumptions":[],"risk_level":"low","confidence":0.9,` +
		`"changes":[{"op":"test","path":"/resources/0/id","value":"\"ict_tester\""},{"op":"replace","path":"/resources/0/capacity","value":"three"}],` +
		`"expected_effects":["The queue may fall; simulate to know."],"requires_confirmation":true}`
	// refusedNative is a Muse Wonder answer the adapter refuses (H-068): its
	// test's value is a JSON text in a string, where the value itself is due.
	refusedNative := `{"status":"proposed","summary":"Raise it.","assumptions":[],"risk_level":"low","confidence":0.9,` +
		`"changes":[{"op":"test","path":"/resources/0/id","value":"\"ict_tester\""},{"op":"replace","path":"/resources/0/capacity","value":3}],` +
		`"expected_effects":["The queue may fall; simulate to know."],"requires_confirmation":true}`
	proposal := func(mode provider.Mode, p provider.Prompt, model string) func(*testing.T, *provider.Client, *meta.Backend) {
		return func(t *testing.T, c *provider.Client, _ *meta.Backend) {
			_, _ = c.GenerateModelProposal(context.Background(), provider.ProposalContext{OrganizationID: org, Mode: mode, Prompt: p,
				Attribution: attribution, Model: model})
		}
	}
	onWonder := func(mode provider.Mode, p provider.Prompt) func(*testing.T, *provider.Client, *meta.Backend) {
		return func(t *testing.T, c *provider.Client, b *meta.Backend) {
			meta.SetModels(b, "muse-wonder-1.0")
			proposal(mode, p, "muse-wonder-1.0")(t, c, b)
		}
	}
	cases := []struct {
		name    string
		replies []reply
		run     func(*testing.T, *provider.Client, *meta.Backend)
	}{
		{"modeler", []reply{ok(answerJ)}, proposal(provider.ModeEdit, roleSealed(t, provider.RoleModeler, "USER_INTENT", "raise the ICT tester capacity"), "")},
		{"scenario", []reply{ok(answerJ)}, proposal(provider.ModeScenario, roleSealed(t, provider.RoleScenario, "USER_INTENT", "add a second shift"), "")},
		{"scenario-chosen-model", []reply{ok(answerJ)}, func(t *testing.T, c *provider.Client, b *meta.Backend) {
			meta.SetModels(b, "muse-wonder-1.0")
			proposal(provider.ModeScenario, roleSealed(t, provider.RoleScenario, "USER_INTENT", "add a second shift"), "muse-wonder-1.0")(t, c, b)
		}},
		{"research", []reply{ok(answerJ)}, proposal(provider.ModeScenario, researchSealed(t, provider.RoleScenario), "")},
		{"modeler-image", []reply{ok(answerJ)}, proposal(provider.ModeEdit, roleSealed(t, provider.RoleModeler, "USER_INTENT", "the line in the photo", pngImage(t, 8, 8)), "")},
		{"modeler-repair", []reply{ok(refused), ok(answerJ)}, func(t *testing.T, c *provider.Client, _ *meta.Backend) {
			p := roleSealed(t, provider.RoleModeler, "USER_INTENT", "raise the ICT tester capacity")
			first, _ := c.GenerateModelProposal(context.Background(), provider.ProposalContext{OrganizationID: org, Mode: provider.ModeEdit, Prompt: p, Attribution: attribution})
			if !first.Prior.Present() {
				t.Fatal("no prior answer to repair")
			}
			proposal(provider.ModeEdit, reviseSealed(t, p, first.Prior), "")(t, c, nil)
		}},
		{"analyst", []reply{ok(answerJ)}, func(t *testing.T, c *provider.Client, _ *meta.Backend) {
			_, _ = c.AnalyzeSimulation(context.Background(), provider.AnalysisContext{OrganizationID: org,
				Prompt: roleSealed(t, provider.RoleAnalyst, "QUESTION", "why is the queue long?"), Attribution: attribution})
		}},
		{"importer", []reply{ok(answerJ)}, func(t *testing.T, c *provider.Client, _ *meta.Backend) {
			_, _ = c.ProposeImportMapping(context.Background(), provider.MappingContext{OrganizationID: org,
				Prompt: roleSealed(t, provider.RoleImporter, "COLUMN_NAMES", "station"), Attribution: attribution})
		}},
		// H-068: Muse Wonder 1.0's proposal requests.
		{"wonder-modeler", []reply{fromWonder(ok(answerJ))}, onWonder(provider.ModeEdit, roleSealed(t, provider.RoleModeler, "USER_INTENT", "raise the ICT tester capacity"))},
		{"wonder-research", []reply{fromWonder(ok(answerJ))}, onWonder(provider.ModeScenario, researchSealed(t, provider.RoleScenario))},
		{"wonder-modeler-image", []reply{fromWonder(ok(answerJ))}, onWonder(provider.ModeEdit,
			roleSealed(t, provider.RoleModeler, "USER_INTENT", "the line in the photo", pngImage(t, 8, 8)))},
		{"wonder-modeler-repair", []reply{fromWonder(ok(refusedNative)), fromWonder(ok(answerJ))}, func(t *testing.T, c *provider.Client, b *meta.Backend) {
			meta.SetModels(b, "muse-wonder-1.0")
			p := roleSealed(t, provider.RoleModeler, "USER_INTENT", "raise the ICT tester capacity")
			first, _ := c.GenerateModelProposal(context.Background(), provider.ProposalContext{OrganizationID: org, Mode: provider.ModeEdit, Prompt: p,
				Attribution: attribution, Model: "muse-wonder-1.0"})
			if !first.Prior.Present() {
				t.Fatal("no prior answer to repair")
			}
			proposal(provider.ModeEdit, reviseSealed(t, p, first.Prior), "muse-wonder-1.0")(t, c, b)
		}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			s := newServer(t, map[string][]reply{messagesPath: tc.replies, countPath: {counted("1500")}})
			c, b := client(t, s)
			tc.run(t, c, b)
			if len(s.raw) == 0 {
				t.Fatal("no request was sent")
			}
			for i, raw := range s.raw {
				file := filepath.Join("testdata", "requests", fmt.Sprintf("%s-%d.json", tc.name, i))
				if os.Getenv(writeGoldens) == "1" {
					if err := os.MkdirAll(filepath.Dir(file), 0o755); err != nil {
						t.Fatal(err)
					}
					if err := os.WriteFile(file, raw, 0o644); err != nil {
						t.Fatal(err)
					}
					continue
				}
				want, err := os.ReadFile(file)
				if err != nil {
					t.Fatalf("%s: %v", file, err)
				}
				if !bytes.Equal(raw, want) {
					t.Errorf("%s: the request is not byte for byte its golden:\n got %s\nwant %s", file, raw, want)
				}
			}
			if os.Getenv(writeGoldens) != "1" {
				if _, err := os.Stat(filepath.Join("testdata", "requests", fmt.Sprintf("%s-%d.json", tc.name, len(s.raw)))); err == nil {
					t.Errorf("a golden request was not sent (%d sent)", len(s.raw))
				}
			}
		})
	}
}
