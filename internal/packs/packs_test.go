package packs

import (
	"encoding/json/v2"
	"path/filepath"
	"slices"
	"testing"

	"github.com/kcl1104/geminus/internal/contracttest"
	"github.com/kcl1104/geminus/internal/domainpack"
)

// The deployment serves the electronics line pack (the P0 domain, H-016,
// T-051) and the software delivery example pack; every call builds the
// same registry.
func TestRegistry(t *testing.T) {
	first, err := Registry()
	if err != nil {
		t.Fatal(err)
	}
	second, err := Registry()
	if err != nil {
		t.Fatal(err)
	}
	if got := first.Domains(); !slices.Equal(got, []string{"electronics_line", "software_delivery"}) || !slices.Equal(got, second.Domains()) {
		t.Errorf("domains %v, then %v", got, second.Domains())
	}
	for _, d := range first.Domains() {
		if p, err := first.Lookup(d); err != nil || p.ID() != d {
			t.Errorf("%s: %v %v", d, p, err)
		}
	}
}

// The create-model contract (http.schema.json CreateModel) offers exactly
// the templates the served packs build, each with its own domain (T-051):
// a template a pack adds without the contract, or one the contract names
// that no served pack builds, fails here.
func TestCreateModelContractNamesTheTemplates(t *testing.T) {
	var doc struct {
		Defs struct {
			CreateModel struct {
				OneOf []struct {
					Properties struct {
						Domain struct {
							Const string `json:"const"`
						} `json:"domain"`
						Template struct {
							Const string `json:"const"`
						} `json:"template"`
					} `json:"properties"`
				} `json:"oneOf"`
			} `json:"CreateModel"`
		} `json:"$defs"`
	}
	if err := json.Unmarshal(contracttest.ReadFile(t, filepath.Join(contracttest.Root(t), "schemas/http.schema.json")), &doc); err != nil {
		t.Fatal(err)
	}
	var contract []string
	for _, alt := range doc.Defs.CreateModel.OneOf {
		contract = append(contract, alt.Properties.Domain.Const+"/"+alt.Properties.Template.Const)
	}
	r, err := Registry()
	if err != nil {
		t.Fatal(err)
	}
	var served []string
	for _, d := range r.Domains() {
		p, _ := r.Lookup(d)
		if tp, ok := p.(domainpack.TemplateProvider); ok {
			for _, name := range tp.Templates() {
				served = append(served, d+"/"+name)
			}
		}
	}
	slices.Sort(contract)
	slices.Sort(served)
	if !slices.Equal(contract, served) || len(served) < 2 {
		t.Errorf("the contract offers %v, the served packs build %v", contract, served)
	}
}
