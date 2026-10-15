package softwaredelivery

import (
	"embed"
	"encoding/json/jsontext"
	"encoding/json/v2"
	"errors"
	"fmt"
	"regexp"
	"slices"
	"unicode/utf8"

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

// Template errors. Match them with errors.Is; an *InvalidError also carries
// the validation issues.
var (
	ErrUnknownTemplate      = errors.New("softwaredelivery: unknown template")
	ErrInvalidModelID       = errors.New("softwaredelivery: not a model ID")
	ErrInvalidTemplateInput = errors.New("softwaredelivery: the template is invalid with this input")
)

// InvalidError reports why the template is invalid for the given name; the
// issues point into the resulting IR (e.g. /name). Its message never quotes
// the input.
type InvalidError struct{ Issues []model.ValidationIssue }

func (e *InvalidError) Error() string {
	return fmt.Sprintf("%v (%d issue(s))", ErrInvalidTemplateInput, len(e.Issues))
}
func (e *InvalidError) Unwrap() error { return ErrInvalidTemplateInput }

// userMetadata replaces the fixture's metadata in a user's model: it keeps the
// synthetic assumptions so nobody mistakes the numbers for measurements, but
// the model is not a fixture.
func userMetadata(templateName string, fixture jsontext.Value) (jsontext.Value, error) {
	var src struct {
		Assumptions []string `json:"assumptions"`
	}
	if err := json.Unmarshal(fixture, &src); err != nil {
		return nil, err
	}
	return json.Marshal(map[string]any{
		"template":    templateName,
		"origin":      "Created from the synthetic default template (D-017); not customer data or measured performance.",
		"assumptions": src.Assumptions,
	}, json.Deterministic(true))
}

// Templates lists the template names this pack offers.
func Templates() []string { return []string{DefaultTemplate} }

// Template returns the named template as a fully validated IR for a new
// model: modelID and name replace the template's own, and the metadata says
// which template it came from and keeps its synthetic assumptions. Problems
// with the input are ErrUnknownTemplate, ErrInvalidModelID or an
// *InvalidError; any other error is internal.
func Template(templateName, newModelID, name string) (*model.ModelIR, error) {
	if !slices.Contains(Templates(), templateName) {
		return nil, ErrUnknownTemplate
	}
	if !modelID.MatchString(newModelID) {
		return nil, ErrInvalidModelID
	}
	if !utf8.ValidString(name) {
		return nil, &InvalidError{Issues: []model.ValidationIssue{
			model.NewIssue(validate.IRSchemaInvalid, "/name", "the name is not valid UTF-8", model.SeverityError)}}
	}
	raw, err := templates.ReadFile("templates/" + templateName + ".json")
	if err != nil {
		return nil, fmt.Errorf("softwaredelivery: reading template %s: %w", templateName, err)
	}
	var ir model.ModelIR
	if err := model.Decode(raw, &ir); err != nil {
		return nil, fmt.Errorf("softwaredelivery: template %s does not decode: %w", templateName, err)
	}
	if ir.Metadata, err = userMetadata(templateName, ir.Metadata); err != nil {
		return nil, fmt.Errorf("softwaredelivery: template %s metadata: %w", templateName, err)
	}
	ir.ModelID, ir.Name = newModelID, name
	data, err := model.Encode(&ir)
	if err != nil {
		return nil, fmt.Errorf("softwaredelivery: encoding template %s: %w", templateName, err)
	}
	res, err := validate.IR(data)
	if err != nil {
		return nil, fmt.Errorf("softwaredelivery: validating template %s: %w", templateName, err)
	}
	if !res.Valid() {
		return nil, &InvalidError{Issues: res.Issues}
	}
	return res.IR, nil
}
