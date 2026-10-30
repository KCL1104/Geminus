package productionlogistics_test

import (
	"fmt"
	"testing"

	"github.com/kcl1104/geminus/internal/domainpack"
	"github.com/kcl1104/geminus/internal/domainpack/conformance"
	"github.com/kcl1104/geminus/internal/domainpack/productionlogistics"
	"github.com/kcl1104/geminus/internal/model"
	"github.com/kcl1104/geminus/internal/simulation"
)

// The pack passes the DomainPack conformance suite (T-042) on its four
// templates in both locales.
func TestConformance(t *testing.T) {
	var models []model.ModelIR
	for i, name := range productionlogistics.Templates() {
		for j, locale := range domainpack.Locales {
			ir, err := productionlogistics.Template(name, fmt.Sprintf("mdl_01M4YDQK00CNF0RMANCE0000%d%d", i, j), "Conformance", locale)
			if err != nil {
				t.Fatalf("%s in %s: %v", name, locale, err)
			}
			models = append(models, *ir)
		}
	}
	conformance.Run(t, conformance.Suite{Pack: productionlogistics.Pack{}, Models: models,
		Horizon: simulation.Duration{Value: 7, Unit: model.Day}, Replications: 2})
}
