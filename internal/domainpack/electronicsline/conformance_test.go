package electronicsline_test

import (
	"testing"

	"github.com/kcl1104/geminus/internal/domainpack"
	"github.com/kcl1104/geminus/internal/domainpack/conformance"
	"github.com/kcl1104/geminus/internal/domainpack/electronicsline"
	"github.com/kcl1104/geminus/internal/model"
	"github.com/kcl1104/geminus/internal/simulation"
)

// The pack passes the DomainPack conformance suite (T-042), its model
// metrics included, on its template in both locales and on the hand line.
func TestConformance(t *testing.T) {
	en, err := electronicsline.Template(electronicsline.DefaultTemplate, "mdl_01M4YDQK00CNF0RMANCE000000", "Conformance", domainpack.English)
	if err != nil {
		t.Fatal(err)
	}
	zh, err := electronicsline.Template(electronicsline.DefaultTemplate, "mdl_01M4YDQK00CNF0RMANCE000001", "一致性", domainpack.TraditionalChinese)
	if err != nil {
		t.Fatal(err)
	}
	conformance.Run(t, conformance.Suite{Pack: electronicsline.Pack{}, Models: []model.ModelIR{*en, *zh, *handIR(t)},
		Horizon: simulation.Duration{Value: 7, Unit: model.Day}, Replications: 2})
}
