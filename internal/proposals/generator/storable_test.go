package generator

import (
	"context"
	"encoding/json/v2"
	"strings"
	"testing"

	"github.com/kcl1104/geminus/internal/ai/review"
)

// H-003 review M2: an answer the database could not store (U+0000 in its
// text or its patch) is invalid at the review, before Finish, so the
// attempt ends once instead of failing its write and asking the provider
// again on every retry.
func TestUnstorableAnswers(t *testing.T) {
	raise := `[{"op":"test","path":"/resources/1/id","value":"ci_runner"},{"op":"replace","path":"/resources/1/capacity","value":6}]`
	for name, edit := range map[string]func(map[string]any){
		"summary":    func(a map[string]any) { a["summary"] = "a\x00b" },
		"assumption": func(a map[string]any) { a["assumptions"] = []string{"c\x00d"} },
		"patch value": func(a map[string]any) {
			var c any
			_ = json.Unmarshal([]byte(`[{"op":"replace","path":"/name","value":"x\u0000y"}]`), &c)
			a["changes"] = c
		},
	} {
		t.Run(name, func(t *testing.T) {
			a := reference(raise)
			edit(a)
			f := scripted(t, "Six runners.", a)
			h, err := New(Config{Provider: client(t, f), Packs: packs(t), Actor: "worker-1", Log: discard()})
			if err != nil {
				t.Fatal(err)
			}
			st := stored(t, "edit", "Six runners.")
			o, meta, err := outcomeOf(h.generate(context.Background(), st.proposal.OrganizationID, st))
			if err != nil || o.Status != review.Invalid || meta == nil {
				t.Fatalf("%+v %v", o, err)
			}
			g, err := generated(o, *meta, nil)
			if err != nil {
				t.Fatal(err)
			}
			row, _ := json.Marshal(g)
			if strings.Contains(string(row), `\u0000`) || g.Summary != nil || len(f.Requests()) != 1 {
				t.Errorf("the row %s, %d provider calls", row, len(f.Requests()))
			}
		})
	}
}
