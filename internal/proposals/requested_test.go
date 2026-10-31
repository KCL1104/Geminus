package proposals

import (
	"encoding/json/v2"
	"strings"
	"testing"

	"github.com/kcl1104/geminus/internal/model/validate"
	store "github.com/kcl1104/geminus/internal/storage/proposals"
)

// H-065: the proposal names the model its request named (requested_model,
// the job payload's model as the store reads it), in every state, so the
// proposal page can say Muse Wonder can take several minutes while it is
// generating. Only one of the contract's models (ProposalModels) is shown;
// none, or anything else the row holds, is left out rather than shown. The
// resource is proposal.schema.json's either way.
func TestRequestedModel(t *testing.T) {
	row := func(status store.Status, requested string) store.Proposal {
		p := store.Proposal{ID: "aip_01M4YDQK00ADN261CME3WV5HZ6", OrganizationID: "org_01M4YDQK00Y5H2929M9WWZWTVH",
			ModelID: "mdl_01M4YDQK004AGVNEGJQJ1NMM0N", BaseVersionID: "mv_01M4YDQK00FRYWV7GS7X1QCXZR", Mode: "scenario", Status: status,
			CreatedBy: "usr_01M4YDQK0016KTK4JM6KAHZ6BR", RequestID: "req_01M4YDQK00ADN261CME3WV5HZ6", RequestedModel: requested}
		switch status {
		case store.Invalid:
			p.Validation = []byte(`{"valid":false,"issues":[{"code":"ROUTING_INVALID","path":"/nodes/45","message":"the outgoing routing probabilities are not valid","severity":"error"}]}`)
		case store.Failed:
			p.Failure = []byte(`{"code":"AI_TIMEOUT","message":"The AI provider did not answer in time.","retryable":false}`)
		}
		return p
	}
	for _, c := range []struct {
		status    store.Status
		requested string
		want      string
	}{
		{store.Generating, "muse-wonder-1.0", "muse-wonder-1.0"},
		{store.Generating, "muse-spark-1.3", "muse-spark-1.3"},
		{store.Generating, "", ""},
		{store.Generating, "muse-wonder-1.0-contributor", ""},
		{store.Generating, "Muse Wonder 1.0", ""},
		{store.Invalid, "muse-wonder-1.0", "muse-wonder-1.0"},
		{store.Failed, "muse-wonder-1.0", "muse-wonder-1.0"},
	} {
		p, err := Public(row(c.status, c.requested), "")
		if err != nil {
			t.Fatalf("%s %q: %v", c.status, c.requested, err)
		}
		if p.RequestedModel != c.want {
			t.Errorf("%s %q: requested_model %q, want %q", c.status, c.requested, p.RequestedModel, c.want)
		}
		data, err := json.Marshal(p)
		if err != nil {
			t.Fatal(err)
		}
		if issues, err := validate.Document(data, "proposal.schema.json", "RESPONSE"); err != nil || len(issues) > 0 {
			t.Errorf("%s %q: %s: %v %v", c.status, c.requested, data, issues, err)
		}
		if c.want == "" && strings.Contains(string(data), "requested_model") {
			t.Errorf("%s %q: %s", c.status, c.requested, data)
		}
	}
}
