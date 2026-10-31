package anthropic_test

import (
	"bytes"
	"context"
	"encoding/json/jsontext"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sync"
	"testing"

	"github.com/kcl1104/geminus/internal/ai/locale"
	"github.com/kcl1104/geminus/internal/ai/provider"
	"github.com/kcl1104/geminus/internal/ai/provider/anthropic"
)

// writeGoldens rewrites testdata/requests from the current code instead of
// comparing with it. Set it only on the code the goldens pin (E1 wrote them
// before tool calls were added, H-060).
const writeGoldens = "GEMINUS_WRITE_REQUEST_GOLDENS"

// capture is a local server that keeps every request body as it came and
// answers each with the next reply (the last repeats).
type capture struct {
	mu      sync.Mutex
	raw     [][]byte
	replies []reply
	srv     *httptest.Server
}

func newCapture(t *testing.T, replies ...reply) *capture {
	c := &capture{replies: replies}
	c.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		data, _ := io.ReadAll(r.Body)
		c.mu.Lock()
		c.raw = append(c.raw, data)
		rep := c.replies[min(len(c.raw)-1, len(c.replies)-1)]
		c.mu.Unlock()
		for k, v := range rep.header {
			w.Header().Set(k, v)
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(rep.status)
		_, _ = io.WriteString(w, rep.body)
	}))
	t.Cleanup(c.srv.Close)
	return c
}

func goldenSealed(t *testing.T, role provider.Role, name, text string) provider.Prompt {
	t.Helper()
	v, _ := jsontext.AppendQuote(nil, text)
	p, err := provider.Seal(provider.Prompt{OrganizationID: org, Role: role, TemplateVersion: string(role) + "-golden", Locale: locale.English,
		System: "rules of the " + string(role), Blocks: []provider.Block{{Kind: provider.UntrustedData, Name: name, Content: v}}})
	if err != nil {
		t.Fatal(err)
	}
	return p
}

var goldenAttribution = provider.Attribution{TargetType: "ai_proposal", TargetID: "aip_01M4YDQK00TA00000000000001", RequestID: "req_01M4YDQK00TA00000000000001"}

// H-060, slice E1: every request an existing role sends through the Claude
// adapter (an edit, a scenario, a repair, an explanation, a mapping) is
// byte for byte what it sent before tool calls were added (the goldens were
// written from the code before E1 changed it).
func TestRoleRequestGoldens(t *testing.T) {
	refused := `{"status":"proposed","summary":"Raise it.","assumptions":[],"risk_level":"low","confidence":0.9,` +
		`"changes":[{"op":"test","path":"/resources/0/id","value":"\"ict_tester\""},{"op":"replace","path":"/resources/0/capacity","value":"three"}],` +
		`"expected_effects":["The queue may fall; simulate to know."],"requires_confirmation":true}`
	proposal := func(mode provider.Mode, p provider.Prompt) func(*testing.T, *provider.Client) {
		return func(t *testing.T, c *provider.Client) {
			_, _ = c.GenerateModelProposal(context.Background(), provider.ProposalContext{OrganizationID: org, Mode: mode, Prompt: p, Attribution: goldenAttribution})
		}
	}
	cases := []struct {
		name    string
		replies []reply
		run     func(*testing.T, *provider.Client)
	}{
		{"modeler", []reply{ok(answerJ)}, proposal(provider.ModeEdit, goldenSealed(t, provider.RoleModeler, "USER_INTENT", "raise the ICT tester capacity"))},
		{"scenario", []reply{ok(answerJ)}, proposal(provider.ModeScenario, goldenSealed(t, provider.RoleScenario, "USER_INTENT", "add a second shift"))},
		{"modeler-repair", []reply{ok(refused), ok(answerJ)}, func(t *testing.T, c *provider.Client) {
			p := goldenSealed(t, provider.RoleModeler, "USER_INTENT", "raise the ICT tester capacity")
			first, _ := c.GenerateModelProposal(context.Background(), provider.ProposalContext{OrganizationID: org, Mode: provider.ModeEdit, Prompt: p, Attribution: goldenAttribution})
			if !first.Prior.Present() {
				t.Fatal("no prior answer to repair")
			}
			p.Revision = &provider.Revision{Prior: first.Prior, Rules: "The server refused your previous answer. Fix every issue listed.",
				Blocks: []provider.Block{{Kind: provider.ServerData, Name: "SERVER_REVIEW", Content: jsontext.Value(`{"issues":[{"code":"ROUTING_INVALID","path":"/nodes/45"}]}`)}}}
			p.TemplateVersion += "+repair-golden"
			p, err := provider.Seal(p)
			if err != nil {
				t.Fatal(err)
			}
			proposal(provider.ModeEdit, p)(t, c)
		}},
		{"analyst", []reply{ok(answerJ)}, func(t *testing.T, c *provider.Client) {
			_, _ = c.AnalyzeSimulation(context.Background(), provider.AnalysisContext{OrganizationID: org,
				Prompt: goldenSealed(t, provider.RoleAnalyst, "QUESTION", "why is the queue long?"), Attribution: goldenAttribution})
		}},
		{"importer", []reply{ok(answerJ)}, func(t *testing.T, c *provider.Client) {
			_, _ = c.ProposeImportMapping(context.Background(), provider.MappingContext{OrganizationID: org,
				Prompt: goldenSealed(t, provider.RoleImporter, "COLUMN_NAMES", "station"), Attribution: goldenAttribution})
		}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			s := newCapture(t, tc.replies...)
			b, err := anthropic.NewForTest(config(keyA), s.srv.URL+"/", s.srv.Client())
			if err != nil {
				t.Fatal(err)
			}
			c, err := provider.NewMetered(b, provider.DefaultBudget(), provider.NewMemoryMeter(1000*spendUSD))
			if err != nil {
				t.Fatal(err)
			}
			tc.run(t, c)
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
