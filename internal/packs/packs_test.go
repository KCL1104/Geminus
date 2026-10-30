package packs

import (
	"encoding/json/v2"
	"path/filepath"
	"slices"
	"testing"

	"github.com/kcl1104/geminus/internal/contracttest"
	"github.com/kcl1104/geminus/internal/domainpack"
	"github.com/kcl1104/geminus/internal/model"
	"github.com/kcl1104/geminus/internal/model/validate"
)

// The deployment serves the electronics line pack (the P0 domain, H-016,
// T-051), the production and logistics pack (H-045) and the software
// delivery example pack; every call builds the same registry.
func TestRegistry(t *testing.T) {
	first, err := Registry()
	if err != nil {
		t.Fatal(err)
	}
	second, err := Registry()
	if err != nil {
		t.Fatal(err)
	}
	if got := first.Domains(); !slices.Equal(got, []string{"electronics_line", "production_logistics", "software_delivery"}) || !slices.Equal(got, second.Domains()) {
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
// that no served pack builds, fails here. An alternative names its one
// template with const, or a pack's several with enum (H-045).
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
							Const string   `json:"const"`
							Enum  []string `json:"enum"`
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
		tmpl := alt.Properties.Template
		if (tmpl.Const == "") == (len(tmpl.Enum) == 0) {
			t.Errorf("the %s alternative names its templates with neither or both of const and enum", alt.Properties.Domain.Const)
		}
		for _, name := range append([]string{tmpl.Const}, tmpl.Enum...) {
			if name != "" {
				contract = append(contract, alt.Properties.Domain.Const+"/"+name)
			}
		}
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
	if !slices.Equal(contract, served) || len(served) < 6 {
		t.Errorf("the contract offers %v, the served packs build %v", contract, served)
	}
}

// Every template a served pack builds is a schema-valid create-model
// request under its own domain, in each locale, and the pack builds it
// for that request; under any other served domain the contract refuses
// it at /template, as the API's request validation does (REQUEST_VALIDATION_FAILED),
// before any pack is asked (H-045).
func TestCreateModelRequestsPerDomain(t *testing.T) {
	r, err := Registry()
	if err != nil {
		t.Fatal(err)
	}
	const ref = "http.schema.json#/$defs/CreateModel"
	body := func(domain, template string, locale domainpack.Locale) []byte {
		b := map[string]any{"name": "My model", "domain": domain, "template": template}
		if locale != "" {
			b["locale"] = string(locale)
		}
		data, err := json.Marshal(b, json.Deterministic(true))
		if err != nil {
			t.Fatal(err)
		}
		return data
	}
	checked := 0
	for _, d := range r.Domains() {
		p, _ := r.Lookup(d)
		tp, ok := p.(domainpack.TemplateProvider)
		if !ok {
			continue
		}
		for _, name := range tp.Templates() {
			for _, locale := range append([]domainpack.Locale{""}, domainpack.Locales...) {
				if issues, err := validate.Document(body(d, name, locale), ref, "REQUEST_VALIDATION_FAILED"); err != nil || len(issues) != 0 {
					t.Errorf("%s/%s in %q: %v %v", d, name, locale, err, issues)
				}
				built := locale
				if built == "" {
					built = domainpack.English
				}
				if ir, err := tp.Template(name, "mdl_01M4YDQK00E1ECTR0N1CS00077", "My model", built); err != nil || ir.Domain != d {
					t.Errorf("%s/%s in %s: the pack does not build it: %v", d, name, built, err)
				}
			}
			for _, other := range r.Domains() {
				if other == d {
					continue
				}
				issues, err := validate.Document(body(other, name, ""), ref, "REQUEST_VALIDATION_FAILED")
				if err != nil || !slices.ContainsFunc(issues, func(i model.ValidationIssue) bool { return i.Path == "/template" }) {
					t.Errorf("%s's template %s under %s: %v %v", d, name, other, err, issues)
				}
			}
			checked++
		}
	}
	if checked < 6 {
		t.Errorf("%d templates checked", checked)
	}
}
