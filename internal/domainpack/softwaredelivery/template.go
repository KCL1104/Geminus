package softwaredelivery

import (
	"embed"
	"fmt"
	"regexp"
	"slices"

	"github.com/kcl1104/geminus/internal/model"
	"github.com/kcl1104/geminus/internal/model/validate"
)

// DefaultTemplate is the create-model template (http.schema.json CreateModel.template).
const DefaultTemplate = "default_delivery_pipeline"

// The template is the D-017 synthetic full pipeline
// (testdata/golden-models/default-delivery.json; a test keeps them equal).
//
//go:embed templates/*.json
var templates embed.FS

var modelID = regexp.MustCompile(`^mdl_[0-7][0-9A-HJKMNP-TV-Z]{25}$`)

// Templates lists the template names this pack offers.
func Templates() []string { return []string{DefaultTemplate} }

// Template returns the named template as a fully validated IR for a new
// model: modelID and name replace the template's own; everything else,
// including the synthetic-assumption metadata, is kept.
func Template(templateName, newModelID, name string) (*model.ModelIR, error) {
	if !slices.Contains(Templates(), templateName) {
		return nil, fmt.Errorf("softwaredelivery: unknown template %q", templateName)
	}
	if !modelID.MatchString(newModelID) {
		return nil, fmt.Errorf("softwaredelivery: %q is not a model ID", newModelID)
	}
	raw, err := templates.ReadFile("templates/" + templateName + ".json")
	if err != nil {
		return nil, err
	}
	var ir model.ModelIR
	if err := model.Decode(raw, &ir); err != nil {
		return nil, fmt.Errorf("softwaredelivery: template %s does not decode: %w", templateName, err)
	}
	ir.ModelID, ir.Name = newModelID, name
	data, err := model.Encode(&ir)
	if err != nil {
		return nil, err
	}
	res, err := validate.IR(data)
	if err != nil {
		return nil, err
	}
	if !res.Valid() {
		return nil, fmt.Errorf("softwaredelivery: template %s is invalid for name %q: %v", templateName, name, res.Issues)
	}
	return res.IR, nil
}
