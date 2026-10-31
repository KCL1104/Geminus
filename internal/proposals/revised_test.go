package proposals

import (
	"encoding/json/v2"
	"strings"
	"testing"

	"github.com/kcl1104/geminus/internal/model/validate"
	store "github.com/kcl1104/geminus/internal/storage/proposals"
)

// H-057: generated_by.revised_after_check is true exactly when the stored
// metadata says the proposal holds the AI's second answer (the generator's
// repair, outcome ready, needs_input or invalid); a repair whose call failed,
// an unknown outcome, no repair at all, or anything else of the repair's
// record shows nothing. The resource with the flag is proposal.schema.json's,
// and nothing of the second call (its hashes, cost, reservation, template)
// leaves the server.
func TestRevisedAfterCheck(t *testing.T) {
	call := `"call":{"provider":"meta","model":"muse-spark-1.3","credential_label":"meta","prompt_template_version":"modeler-0.2.0+repair-0.1.0",` +
		`"input_hash":"sha256:cc","cost_nano_usd":99,"spend_reservation_id":"asr_01M4YDQK00TA00000000000002"}`
	for _, c := range []struct {
		repair string
		want   bool
	}{
		{``, false},
		{`,"repair":{"outcome":"ready","issues":2,` + call + `}`, true},
		{`,"repair":{"outcome":"needs_input","issues":1,` + call + `}`, true},
		{`,"repair":{"outcome":"invalid","issues":1,` + call + `}`, true},
		{`,"repair":{"outcome":"failed","code":"AI_TIMEOUT","issues":1,` + call + `}`, false},
		{`,"repair":{"outcome":"revised","issues":1}`, false},
		{`,"repair":{"issues":1}`, false},
		{`,"repair":"ready"`, false},
	} {
		metadata := `{"provider":"meta","model":"muse-spark-1.3","prompt_template_version":"modeler-0.2.0"` + c.repair + `}`
		g := FromMetadata([]byte(metadata))
		if c.repair == `,"repair":"ready"` {
			// Metadata that does not read is no label at all.
			if g != nil {
				t.Errorf("%s: %+v", c.repair, g)
			}
			continue
		}
		if g == nil || g.RevisedAfterCheck != c.want {
			t.Errorf("%s: %+v", c.repair, g)
			continue
		}
		out, _ := json.Marshal(g)
		if c.want != strings.Contains(string(out), `"revised_after_check":true`) || strings.Contains(string(out), "false") {
			t.Errorf("%s: %s", c.repair, out)
		}
		for _, leak := range []string{"repair-0.1.0", "sha256", "asr_", "99", "AI_TIMEOUT", "issues"} {
			if strings.Contains(string(out), leak) {
				t.Errorf("%s: %s leaks %q", c.repair, out, leak)
			}
		}
	}
	p, err := Public(store.Proposal{ID: "aip_01M4YDQK00ADN261CME3WV5HZ6", OrganizationID: "org_01M4YDQK00Y5H2929M9WWZWTVH",
		ModelID: "mdl_01M4YDQK004AGVNEGJQJ1NMM0N", BaseVersionID: "mv_01M4YDQK00FRYWV7GS7X1QCXZR", Mode: "edit", Status: store.Invalid,
		CreatedBy: "usr_01M4YDQK0016KTK4JM6KAHZ6BR", Validation: []byte(`{"valid":false,"issues":[{"code":"ROUTING_INVALID","path":"/nodes/45",` +
			`"message":"the outgoing routing probabilities are not valid","severity":"error"}]}`),
		ProviderMetadata: []byte(`{"provider":"meta","model":"muse-spark-1.3","repair":{"outcome":"invalid","issues":1,` + call + `}}`)}, "")
	if err != nil {
		t.Fatal(err)
	}
	data, err := json.Marshal(p)
	if err != nil {
		t.Fatal(err)
	}
	if issues, err := validate.Document(data, "proposal.schema.json", "RESPONSE"); err != nil || len(issues) > 0 || !p.GeneratedBy.RevisedAfterCheck {
		t.Errorf("%s: %v %v", data, issues, err)
	}
	// An analysis's metadata never has a repair: its label never says so.
	if g := FromMetadata([]byte(`{"provider":"meta","model":"muse-spark-1.3","role":"analyst"}`)); g == nil || g.RevisedAfterCheck {
		t.Errorf("an analysis: %+v", g)
	}
}
