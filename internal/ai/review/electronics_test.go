package review

import (
	"strconv"
	"testing"

	"github.com/kcl1104/geminus/internal/ai/provider"
	"github.com/kcl1104/geminus/internal/domainpack"
	"github.com/kcl1104/geminus/internal/domainpack/electronicsline"
	"github.com/kcl1104/geminus/internal/model"
	"github.com/kcl1104/geminus/internal/model/hashing"
)

// electronicsBase is the electronics line template (T-051) as a stored
// version's canonical IR.
func electronicsBase(t *testing.T) ([]byte, *model.ModelIR) {
	t.Helper()
	ir, err := electronicsline.Template(electronicsline.DefaultTemplate, "mdl_01M4YDQK00TA00000000000001", "Line", domainpack.English)
	if err != nil {
		t.Fatal(err)
	}
	data, err := model.Encode(ir)
	if err != nil {
		t.Fatal(err)
	}
	c, err := hashing.Canonical(data)
	if err != nil {
		t.Fatal(err)
	}
	return c, ir
}

func nodeAt(t *testing.T, ir *model.ModelIR, id string) string {
	t.Helper()
	for i, n := range ir.Nodes {
		if n.ID == id {
			return strconv.Itoa(i)
		}
	}
	t.Fatalf("no node %s", id)
	return ""
}

// The server's review with the electronics line pack (T-051, H-029): the
// demo's scenario "burn-in in lots of 48" is ready at the server's risk for
// a batch change (D-057: at least medium, whatever the AI claims); an edit
// that breaks a pack rule (FCT's tester without its role) is invalid with the
// pack's code and a fixed sentence (D-062 (c)).
func TestElectronicsLineReview(t *testing.T) {
	doc, ir := electronicsBase(t)
	pack := electronicsline.Pack{}
	burn := nodeAt(t, ir, "burn_in")
	lots := `[{"op":"test","path":"/nodes/` + burn + `/id","value":"burn_in"},{"op":"replace","path":"/nodes/` + burn + `/batch/max_size","value":48}]`
	o := run(t, Input{Mode: provider.ModeScenario, Base: doc, Pool: pool(t, doc), Pack: pack, Output: proposed(ops(t, lots), "low")})
	if o.Status != Ready || o.ServerRisk != Medium || o.Risk != Medium {
		t.Fatalf("lots of 48: status %q, server risk %q, risk %q (%v)", o.Status, o.ServerRisk, o.Risk, codes(o))
	}
	// A lot larger than the buffer before burn-in (120) cannot form: the
	// generic validator refuses it before the pack's rules run.
	big := `[{"op":"test","path":"/nodes/` + burn + `/id","value":"burn_in"},{"op":"replace","path":"/nodes/` + burn + `/batch/max_size","value":121}]`
	wantInvalid(t, run(t, Input{Mode: provider.ModeScenario, Base: doc, Pool: pool(t, doc), Pack: pack, Output: proposed(ops(t, big), "low")}),
		"BATCH_INVALID", "/nodes/"+burn+"/batch/max_size")
	// Giving FCT another station's tester would also make a blocking cycle
	// (the generic validator's, first); a tester without its role breaks
	// only the pack's rule.
	fct := nodeAt(t, ir, "fct")
	tester := ""
	for i, r := range ir.Resources {
		if r.ID == "fct_tester" {
			tester = strconv.Itoa(i)
		}
	}
	wrong := `[{"op":"test","path":"/resources/` + tester + `/id","value":"fct_tester"},{"op":"replace","path":"/resources/` + tester + `/role","value":"generic"}]`
	bad := run(t, Input{Mode: provider.ModeEdit, Base: doc, Pool: pool(t, doc), Pack: pack, Output: proposed(ops(t, wrong), "low")})
	wantInvalid(t, bad, electronicsline.StationResourceInvalid, "/nodes/"+fct+"/requires")
	for _, is := range bad.Validation.Issues {
		if is.Code == electronicsline.StationResourceInvalid && is.Message != sentences[electronicsline.StationResourceInvalid][0] {
			t.Errorf("the pack's message is stored: %q", is.Message)
		}
	}
}
