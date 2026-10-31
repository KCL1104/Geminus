package electronicsline

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

// The create-model templates (http.schema.json CreateModel.template).
const (
	// DefaultTemplate is the full line of §4.
	DefaultTemplate = "electronics_assembly_test_line"
	// PhotoStartTemplate is the bare line a new model starts from when it
	// is made from a photo or floor plan (§4.6, H-062): a source, a buffer,
	// one SMT step and the sink, the IR of testdata/photos/bare-line.json
	// that the live photo runs passed on (H-054). The page then sends the
	// photo as the model's first photo proposal; nothing here reads it.
	PhotoStartTemplate = "photo_start"
)

// The full template is ELECTRONICS_LINE §4.1 with §4.2's parameters and
// §4.4's two product families and SMT changeovers, the photo start §4.6's
// bare line, all synthetic (H-018). Their default names are English; <template>.<locale>.names.json gives them in
// another locale by ID, for every entity type, resource and node
// (REQ-022). IDs and everything else are the same in every locale.
//
//go:embed templates/*.json
var templates embed.FS

var modelID = regexp.MustCompile(`^mdl_[0-7][0-9A-HJKMNP-TV-Z]{25}$`)

// Template errors. Match them with errors.Is; an *InvalidError also carries
// the validation issues.
var (
	ErrUnknownTemplate      = errors.New("electronicsline: unknown template")
	ErrInvalidModelID       = errors.New("electronicsline: not a model ID")
	ErrInvalidTemplateInput = errors.New("electronicsline: the template is invalid with this input")
	ErrUnknownLocale        = errors.New("electronicsline: unknown locale")
)

// InvalidError reports why the template is invalid for the given name; the
// issues point into the resulting IR (e.g. /name). Its message never quotes
// the input.
type InvalidError struct{ Issues []model.ValidationIssue }

func (e *InvalidError) Error() string {
	return fmt.Sprintf("%v (%d issue(s))", ErrInvalidTemplateInput, len(e.Issues))
}
func (e *InvalidError) Unwrap() error { return ErrInvalidTemplateInput }

// templateMetadata is the template file's metadata: §3.4 rule 4's
// synthetic labelling, which a model created from it keeps.
type templateMetadata struct {
	Synthetic   bool     `json:"synthetic"`
	Source      string   `json:"source"`
	Origin      string   `json:"origin"`
	Assumptions []string `json:"assumptions"`
}

// userMetadata is a created model's metadata: the template's synthetic
// labelling and the template it came from.
func userMetadata(templateName string, raw jsontext.Value) (jsontext.Value, error) {
	var src templateMetadata
	if err := json.Unmarshal(raw, &src, json.RejectUnknownMembers(true)); err != nil {
		return nil, err
	}
	if !src.Synthetic || src.Source == "" || src.Origin == "" || len(src.Assumptions) == 0 {
		return nil, errors.New("the template is not labelled synthetic")
	}
	return json.Marshal(map[string]any{
		"template":    templateName,
		"synthetic":   true,
		"source":      src.Source,
		"origin":      src.Origin,
		"assumptions": src.Assumptions,
	}, json.Deterministic(true))
}

// Templates lists the template names this pack offers: the full line
// first, then the photo start.
func Templates() []string { return []string{DefaultTemplate, PhotoStartTemplate} }

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
		return fmt.Errorf("electronicsline: %s names of template %s: %w", locale, templateName, err)
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
		return fmt.Errorf("electronicsline: %s names of template %s: %w", locale, templateName, err)
	}
	type item struct {
		id   string
		name *string
	}
	var entityTypes, res, nodeNames []item
	for i := range ir.EntityTypes {
		entityTypes = append(entityTypes, item{ir.EntityTypes[i].ID, &ir.EntityTypes[i].Name})
	}
	for i := range ir.Resources {
		res = append(res, item{ir.Resources[i].ID, &ir.Resources[i].Name})
	}
	for i := range ir.Nodes {
		nodeNames = append(nodeNames, item{ir.Nodes[i].ID, &ir.Nodes[i].Name})
	}
	for _, c := range []struct {
		kind  string
		from  map[string]string
		items []item
	}{{"entity type", names.EntityTypes, entityTypes}, {"resource", names.Resources, res}, {"node", names.Nodes, nodeNames}} {
		used := map[string]bool{}
		for _, it := range c.items {
			if *it.name == "" {
				continue // unnamed in the template, unnamed in every locale
			}
			n, ok := c.from[it.id]
			if !ok || strings.TrimSpace(n) == "" {
				return fmt.Errorf("electronicsline: template %s has no %s name for %s %s", templateName, locale, c.kind, it.id)
			}
			// A bad name is the names file's fault, so it is an internal
			// error here rather than an InvalidError about the caller's input.
			if strings.ContainsFunc(n, unicode.IsControl) || utf8.RuneCountInString(n) > maxNameLength {
				return fmt.Errorf("electronicsline: template %s has an unusable %s name for %s %s", templateName, locale, c.kind, it.id)
			}
			*it.name, used[it.id] = n, true
		}
		if len(used) != len(c.from) {
			return fmt.Errorf("electronicsline: the %s names of template %s name a %s it does not have", locale, templateName, c.kind)
		}
	}
	return nil
}

// Template returns the named template as a fully validated IR for a new
// model: modelID and name replace the template's own, its default names
// are in locale, and the metadata says which template it came from and
// keeps its synthetic labelling. Problems with the input are
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
		return nil, fmt.Errorf("electronicsline: reading template %s: %w", templateName, err)
	}
	var ir model.ModelIR
	if err := model.Decode(raw, &ir); err != nil {
		return nil, fmt.Errorf("electronicsline: template %s does not decode: %w", templateName, err)
	}
	if ir.Metadata, err = userMetadata(templateName, ir.Metadata); err != nil {
		return nil, fmt.Errorf("electronicsline: template %s metadata: %w", templateName, err)
	}
	if err := localize(read, &ir, templateName, locale); err != nil {
		return nil, err
	}
	ir.ModelID, ir.Name = newModelID, name
	data, err := model.Encode(&ir)
	if err != nil {
		return nil, fmt.Errorf("electronicsline: encoding template %s: %w", templateName, err)
	}
	res, err := validate.IR(data)
	if err != nil {
		return nil, fmt.Errorf("electronicsline: validating template %s: %w", templateName, err)
	}
	if !res.Valid() {
		return nil, &InvalidError{Issues: res.Issues}
	}
	return res.IR, nil
}
