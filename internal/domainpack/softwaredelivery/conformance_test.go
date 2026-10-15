package softwaredelivery_test

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/kcl1104/geminus/internal/contracttest"
	"github.com/kcl1104/geminus/internal/domainpack/conformance"
	"github.com/kcl1104/geminus/internal/domainpack/softwaredelivery"
	"github.com/kcl1104/geminus/internal/model"
	"github.com/kcl1104/geminus/internal/simulation"
)

// The P0 pack passes the DomainPack conformance suite (T-042) on its
// default template and every software-delivery golden model.
func TestConformance(t *testing.T) {
	tmpl, err := softwaredelivery.Template(softwaredelivery.DefaultTemplate, "mdl_01M4YDQK00CNF0RMANCE000000", "Conformance")
	if err != nil {
		t.Fatal(err)
	}
	models := []model.ModelIR{*tmpl}
	for _, name := range []string{"default-delivery.json", "deterministic-queue.json", "congested-queue.json", "report-example.json"} {
		var ir model.ModelIR
		if err := model.Decode(contracttest.ReadFile(t, filepath.Join(contracttest.Root(t), "testdata/golden-models", name)), &ir); err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		models = append(models, ir)
	}
	// report-example draws review time from an empirical dataset; these
	// synthetic samples stand in for the frozen dataset the worker resolves.
	review := simulation.ResolvedDataset{Binding: model.DatasetBinding{LogicalRef: "dataset:review_duration",
		DatasetVersionID: "dsv_01M4YDQK00NMKD3J34KSJZVY40", ContentHash: "sha256:" + strings.Repeat("0", 64), Unit: model.Minute, SampleCount: 3},
		Samples: []float64{120, 240, 360}}
	conformance.Run(t, conformance.Suite{Pack: softwaredelivery.Pack{}, Models: models,
		Horizon: simulation.Duration{Value: 3, Unit: model.Day}, Replications: 2,
		Datasets: map[string]simulation.ResolvedDataset{review.Binding.LogicalRef: review}})
}
