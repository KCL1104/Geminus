package validate

import (
	"bytes"
	"errors"
	"fmt"
	"slices"
	"strings"
	"sync"

	"github.com/santhosh-tekuri/jsonschema/v6"
	"github.com/santhosh-tekuri/jsonschema/v6/kind"
	"golang.org/x/text/language"
	"golang.org/x/text/message"

	"github.com/kcl1104/geminus/internal/model"
	"github.com/kcl1104/geminus/schemas"
)

const schemaBase = "https://twin.example/spec/0.1/"

var printer = message.NewPrinter(language.English)

// compiled holds every embedded schema, compiled once. Schemas reference each
// other by $id; all resources are registered up front and any other URL is
// refused, so validation never touches the network or the filesystem.
var compiled = sync.OnceValues(func() (*jsonschema.Compiler, error) {
	c := jsonschema.NewCompiler()
	c.DefaultDraft(jsonschema.Draft2020)
	c.AssertFormat()
	c.UseLoader(refuseLoader{})
	entries, err := schemas.FS.ReadDir(".")
	if err != nil {
		return nil, err
	}
	for _, e := range entries {
		if !strings.HasSuffix(e.Name(), ".schema.json") {
			continue
		}
		data, err := schemas.FS.ReadFile(e.Name())
		if err != nil {
			return nil, err
		}
		doc, err := jsonschema.UnmarshalJSON(bytes.NewReader(data))
		if err != nil {
			return nil, fmt.Errorf("%s: %w", e.Name(), err)
		}
		if err := c.AddResource(schemaBase+e.Name(), doc); err != nil {
			return nil, err
		}
	}
	return c, nil
})

var schemaCache sync.Map // ref -> *jsonschema.Schema

// schemaFor compiles "model-ir.schema.json" or "simulation.schema.json#/$defs/RunConfig".
func schemaFor(ref string) (*jsonschema.Schema, error) {
	if s, ok := schemaCache.Load(ref); ok {
		return s.(*jsonschema.Schema), nil
	}
	c, err := compiled()
	if err != nil {
		return nil, err
	}
	s, err := c.Compile(schemaBase + ref)
	if err != nil {
		return nil, err
	}
	schemaCache.Store(ref, s)
	return s, nil
}

type refuseLoader struct{}

func (refuseLoader) Load(url string) (any, error) {
	return nil, fmt.Errorf("schema %s is not embedded; refusing to load it", url)
}

// Schema validates a decoded instance (from jsonschema.UnmarshalJSON) against
// ref and returns one issue per leaf failure, all with code.
func Schema(ref, code string, instance any) ([]model.ValidationIssue, error) {
	s, err := schemaFor(ref)
	if err != nil {
		return nil, err
	}
	err = s.Validate(instance)
	if err == nil {
		return nil, nil
	}
	var ve *jsonschema.ValidationError
	if !errors.As(err, &ve) {
		return nil, err
	}
	var out issues
	collect(ve, code, &out)
	return Sort(out), nil
}

// collect flattens a validation error tree into leaf issues. For oneOf/anyOf
// it keeps only the branches whose discriminator (a const member such as
// "type" or "op") matches the instance; when none matches it reports a
// single issue at the discriminator listing the allowed values.
func collect(e *jsonschema.ValidationError, code string, out *issues) {
	causes := e.Causes
	switch e.ErrorKind.(type) {
	case *kind.OneOf, *kind.AnyOf:
		matching := slices.DeleteFunc(slices.Clone(causes), func(b *jsonschema.ValidationError) bool {
			return discriminatorMismatch(b, len(e.InstanceLocation)) != nil
		})
		if len(matching) == 0 {
			if loc, want := discriminator(causes, len(e.InstanceLocation)); loc != nil {
				out.add(code, Pointer(loc...), "must be one of: "+strings.Join(want, ", "))
				return
			}
		} else {
			causes = matching
		}
	}
	if len(causes) == 0 {
		out.add(code, Pointer(e.InstanceLocation...), leafMessage(e.ErrorKind))
		return
	}
	for _, c := range causes {
		collect(c, code, out)
	}
}

// leafMessage renders a failure deterministically: the library lists member
// names in map order, which would make issue text differ between runs.
func leafMessage(k jsonschema.ErrorKind) string {
	switch k := k.(type) {
	case *kind.AdditionalProperties:
		return "members not allowed: " + strings.Join(slices.Sorted(slices.Values(k.Properties)), ", ")
	case *kind.Required:
		return "missing required members: " + strings.Join(slices.Sorted(slices.Values(k.Missing)), ", ")
	}
	return k.LocalizedString(printer)
}

// discriminatorMismatch returns the const error on a direct child member of
// the union's instance, if the branch has one.
func discriminatorMismatch(branch *jsonschema.ValidationError, depth int) *jsonschema.ValidationError {
	if _, ok := branch.ErrorKind.(*kind.Const); ok && len(branch.InstanceLocation) == depth+1 {
		return branch
	}
	for _, c := range branch.Causes {
		if m := discriminatorMismatch(c, depth); m != nil {
			return m
		}
	}
	return nil
}

func discriminator(branches []*jsonschema.ValidationError, depth int) ([]string, []string) {
	var loc []string
	var want []string
	for _, b := range branches {
		m := discriminatorMismatch(b, depth)
		if m == nil {
			return nil, nil
		}
		if loc != nil && !slices.Equal(loc, m.InstanceLocation) {
			return nil, nil
		}
		loc = m.InstanceLocation
		want = append(want, fmt.Sprint(m.ErrorKind.(*kind.Const).Want))
	}
	return loc, want
}
