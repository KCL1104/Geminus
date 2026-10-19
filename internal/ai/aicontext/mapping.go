package aicontext

import (
	"encoding/json/jsontext"
	"fmt"
	"slices"
	"strings"
	"unicode/utf8"

	"github.com/kcl1104/geminus/internal/ai/provider"
)

// ImportRef is the import whose columns are mapped, read in the scope.
type ImportRef struct {
	OrganizationID string
	ImportID       string
}

// MappingInput is a mapping suggestion's input: the import's column names
// and preview rows (D-043: raw values keyed by column name), all untrusted.
type MappingInput struct {
	Scope        Scope
	Import       ImportRef
	Columns      []string
	SampleRows   []map[string]jsontext.Value
	OutputLocale string
	Budget       provider.Budget
}

// TargetFields and Transforms are ai-mapping.schema.json's enums (a test
// keeps them equal); the mapper may use nothing else (D-009).
var (
	TargetFields = []string{"case_id", "event_type", "timestamp", "stage", "duration_ms", "resource_id", "outcome",
		"deployment_id", "is_failure", "is_rework", "metadata"}
	Transforms = []string{"identity", "rfc3339", "integer_ms", "boolean_literal"}
)

// Mapping builds the context of a mapping suggestion: the column names
// and a bounded, redacted sample (AI_SPEC §8: never the whole file).
func Mapping(in MappingInput) (provider.MappingContext, error) {
	loc, err := parseLocale(in.OutputLocale)
	if err != nil {
		return provider.MappingContext{}, err
	}
	if err := in.Budget.Validate(); err != nil {
		return provider.MappingContext{}, &Error{Code: InvalidInput, Path: "/budget", cause: err}
	}
	if err := checkScope(in.Scope, false); err != nil {
		return provider.MappingContext{}, err
	}
	if in.Import.OrganizationID != in.Scope.OrganizationID {
		return provider.MappingContext{}, fail(CrossTenant, "/import")
	}
	if len(in.Columns) == 0 || len(in.Columns) > maxColumns {
		return provider.MappingContext{}, fail(InvalidInput, "/columns")
	}
	for i, c := range in.Columns {
		if c == "" || utf8.RuneCountInString(c) > 256 || slices.Index(in.Columns, c) != i {
			return provider.MappingContext{}, fail(InvalidInput, fmt.Sprintf("/columns/%d", i))
		}
	}
	for i, row := range in.SampleRows {
		for k := range row {
			if !slices.Contains(in.Columns, k) {
				return provider.MappingContext{}, fail(InvalidInput, fmt.Sprintf("/sample_rows/%d", i))
			}
		}
	}

	text, version, err := template(provider.RoleImporter)
	if err != nil {
		return provider.MappingContext{}, err
	}
	system := text + serverRules(provider.RoleImporter, "", loc) +
		"- CANONICAL_SCHEMA target fields (target_field, or null when unsure): " + strings.Join(TargetFields, ", ") + ".\n" +
		"- ALLOWED_TRANSFORMS: " + strings.Join(Transforms, ", ") + ". Nothing else: no code, expression, SQL, shell or URL.\n" +
		"- Answer one mapping for each name in COLUMN_NAMES, with source_column copied exactly. requires_confirmation is true when confidence is below 0.80 or target_field is null.\n"

	z := newSanitizer(maxCellRunes)
	om := &omissions{}
	names := make([]string, len(in.Columns))
	for i, c := range in.Columns {
		names[i] = z.textBound(c, 256)
	}
	rows := in.SampleRows
	if len(rows) > maxSampleRows {
		om.add("sample_rows", len(rows)-maxSampleRows, ReasonBounded)
		rows = rows[:maxSampleRows]
	}
	sample := make([]any, 0, len(rows))
	for i, row := range rows {
		out := map[string]any{}
		for k, v := range row {
			x, err := z.json(v)
			if err != nil {
				return provider.MappingContext{}, &Error{Code: InvalidInput, Path: fmt.Sprintf("/sample_rows/%d", i), cause: err}
			}
			out[z.textBound(k, 256)] = x
		}
		sample = append(sample, out)
	}
	cols, err := block(provider.UntrustedData, "COLUMN_NAMES", names)
	if err != nil {
		return provider.MappingContext{}, err
	}
	rowsBlock, err := block(provider.UntrustedData, "SAMPLE_ROWS", sample)
	if err != nil {
		return provider.MappingContext{}, err
	}
	p, err := assemble(provider.RoleImporter, loc, system, version, []provider.Block{cols, rowsBlock}, om, z)
	if err != nil {
		return provider.MappingContext{}, err
	}
	if !fits(p, in.Budget) {
		return provider.MappingContext{}, fail(TooLarge, "")
	}
	return provider.MappingContext{OrganizationID: in.Scope.OrganizationID, Prompt: p}, nil
}
