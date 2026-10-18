package softwaredelivery

import (
	"embed"
	"encoding/json/jsontext"
	"encoding/json/v2"
	"errors"
	"fmt"
	"regexp"
	"slices"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/kcl1104/geminus/internal/domainpack"
	"github.com/kcl1104/geminus/internal/model"
	"github.com/kcl1104/geminus/internal/model/validate"
)

// DefaultTemplate is the create-model template (http.schema.json CreateModel.template).
const DefaultTemplate = "default_delivery_pipeline"

// The template is the D-017 synthetic full pipeline
// (testdata/golden-models/default-delivery.json; a test keeps them equal).
// Its default names are English; <template>.<locale>.names.json gives them
// in another locale by ID, for every named entity type, resource and node
// (REQ-022). IDs and everything else are the same in every locale.
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
	ErrUnknownLocale        = errors.New("softwaredelivery: unknown locale")
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

// Templates and Template make the pack a domainpack.TemplateProvider.
func (Pack) Templates() []string { return Templates() }

func (Pack) Template(templateName, modelID, name string, locale domainpack.Locale) (*model.ModelIR, error) {
	return Template(templateName, modelID, name, locale)
}

// maxNameLength is model-ir.schema.json's maxLength for a name, in characters.
const maxNameLength = 200

// readFile reads one template file by its path; Template reads the
// embedded ones. A function rather than an io/fs value: a pack does no I/O
// of its own (docs/ARCHITECTURE.md §2–§3).
type readFile func(name string) ([]byte, error)

// localize gives ir the template's default names in locale.
func localize(read readFile, ir *model.ModelIR, templateName string, locale domainpack.Locale) error {
	if locale == domainpack.English {
		return nil
	}
	raw, err := read("templates/" + templateName + "." + string(locale) + ".names.json")
	if err != nil {
		return fmt.Errorf("softwaredelivery: %s names of template %s: %w", locale, templateName, err)
	}
	return applyNames(ir, templateName, locale, raw)
}

// applyNames replaces ir's names with those of a names file. A file that
// misses a named element of the template, or names one it does not have, is
// an internal error, so no locale shows a mix of languages.
func applyNames(ir *model.ModelIR, templateName string, locale domainpack.Locale, raw []byte) error {
	var names struct {
		EntityTypes map[string]string `json:"entity_types"`
		Resources   map[string]string `json:"resources"`
		Nodes       map[string]string `json:"nodes"`
	}
	if err := json.Unmarshal(raw, &names, json.RejectUnknownMembers(true)); err != nil {
		return fmt.Errorf("softwaredelivery: %s names of template %s: %w", locale, templateName, err)
	}
	apply := func(kind, id string, name *string, from map[string]string, used map[string]bool) error {
		if *name == "" {
			return nil // unnamed in the template, unnamed in every locale
		}
		n, ok := from[id]
		if !ok || strings.TrimSpace(n) == "" {
			return fmt.Errorf("softwaredelivery: template %s has no %s name for %s %s", templateName, locale, kind, id)
		}
		// A bad name is the names file's fault, so it is an internal error
		// here rather than an InvalidError about the caller's input later
		// (T-044 review L6).
		if strings.ContainsFunc(n, unicode.IsControl) || utf8.RuneCountInString(n) > maxNameLength {
			return fmt.Errorf("softwaredelivery: template %s has an unusable %s name for %s %s", templateName, locale, kind, id)
		}
		*name, used[id] = n, true
		return nil
	}
	for _, c := range []struct {
		kind  string
		from  map[string]string
		items func(func(id string, name *string) error) error
	}{
		{"entity type", names.EntityTypes, func(f func(string, *string) error) error {
			for i := range ir.EntityTypes {
				if err := f(ir.EntityTypes[i].ID, &ir.EntityTypes[i].Name); err != nil {
					return err
				}
			}
			return nil
		}},
		{"resource", names.Resources, func(f func(string, *string) error) error {
			for i := range ir.Resources {
				if err := f(ir.Resources[i].ID, &ir.Resources[i].Name); err != nil {
					return err
				}
			}
			return nil
		}},
		{"node", names.Nodes, func(f func(string, *string) error) error {
			for i := range ir.Nodes {
				if err := f(ir.Nodes[i].ID, &ir.Nodes[i].Name); err != nil {
					return err
				}
			}
			return nil
		}},
	} {
		used := map[string]bool{}
		if err := c.items(func(id string, name *string) error { return apply(c.kind, id, name, c.from, used) }); err != nil {
			return err
		}
		if len(used) != len(c.from) {
			return fmt.Errorf("softwaredelivery: the %s names of template %s name a %s it does not have", locale, templateName, c.kind)
		}
	}
	return nil
}

// Template returns the named template as a fully validated IR for a new
// model: modelID and name replace the template's own, its default names
// are in locale, and the metadata says which template it came from and
// keeps its synthetic assumptions. Problems with the input are
// ErrUnknownTemplate, ErrUnknownLocale, ErrInvalidModelID or an
// *InvalidError; any other error is internal.
func Template(templateName, newModelID, name string, locale domainpack.Locale) (*model.ModelIR, error) {
	return templateFrom(templates.ReadFile, templateName, newModelID, name, locale)
}

func templateFrom(read readFile, templateName, newModelID, name string, locale domainpack.Locale) (*model.ModelIR, error) {
	if !slices.Contains(Templates(), templateName) {
		return nil, ErrUnknownTemplate
	}
	if !slices.Contains(domainpack.Locales, locale) {
		return nil, ErrUnknownLocale
	}
	if !modelID.MatchString(newModelID) {
		return nil, ErrInvalidModelID
	}
	if !utf8.ValidString(name) {
		return nil, &InvalidError{Issues: []model.ValidationIssue{
			model.NewIssue(validate.IRSchemaInvalid, "/name", "the name is not valid UTF-8", model.SeverityError)}}
	}
	raw, err := read("templates/" + templateName + ".json")
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
	if err := localize(read, &ir, templateName, locale); err != nil {
		return nil, err
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
