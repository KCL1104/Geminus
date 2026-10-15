// Package patch applies the P0 JSON Patch profile to a Model IR (D-014,
// TWIN_IR_SPEC §6): decoded-pointer allowlists per mode, the test-before-
// modify rule (D-020), atomic application on a copy, then full validation of
// the candidate.
//
// Patch issues point into the patch document ("/3/path"); candidate issues
// point into the resulting IR. A patch either applies completely or not at
// all: the base document is never modified.
package patch

import (
	"bytes"
	"encoding/json/v2"
	"fmt"

	"github.com/santhosh-tekuri/jsonschema/v6"

	"github.com/kcl1104/geminus/internal/model"
	"github.com/kcl1104/geminus/internal/model/validate"
)

// Issue codes for patch documents (D-023).
const (
	SchemaInvalid = "PATCH_SCHEMA_INVALID" // fails json-patch.schema.json (incl. move/copy, root path)
	PathInvalid   = "PATCH_PATH_INVALID"   // pointer not decodable or not a valid array index
	PathForbidden = "PATCH_PATH_FORBIDDEN" // decoded path outside the mode's allowlist
	TestRequired  = "PATCH_TEST_REQUIRED"  // D-020: array element changed without a prior id test
	TargetMissing = "PATCH_TARGET_MISSING" // apply: target or parent does not exist
	TestFailed    = "PATCH_TEST_FAILED"    // apply: test value differs
)

// Mode selects the allowlist (TWIN_IR_SPEC §6, D-021, D-022).
type Mode int

const (
	ManualDraft Mode = iota // a human editing a draft
	AIEdit                  // an AI proposal in edit mode
	Scenario                // any scenario patch, human or AI (D-021)
)

func (m Mode) valid() bool { return m >= ManualDraft && m <= Scenario }

func (m Mode) String() string {
	if !m.valid() {
		return fmt.Sprintf("mode(%d)", int(m))
	}
	return [...]string{"manual draft", "AI edit", "scenario"}[m]
}

// Result of applying a patch.
type Result struct {
	// PatchIssues point into the patch document. When any is an error the
	// patch did not apply and Candidate is nil.
	PatchIssues []model.ValidationIssue
	// Candidate is the patched IR (canonical member order), nil if not applied.
	Candidate []byte
	// Validation is the full validation of Candidate.
	Validation validate.Result
}

// Applied reports whether the patch applied (validation may still fail).
func (r Result) Applied() bool { return r.Candidate != nil }

// Valid reports whether the patch applied and the candidate is valid.
func (r Result) Valid() bool { return r.Applied() && r.Validation.Valid() }

// Apply applies patchDoc to base under mode. The error is reserved for
// internal failures; problems with the patch are issues.
func Apply(base, patchDoc []byte, mode Mode) (Result, error) {
	if !mode.valid() {
		return Result{}, fmt.Errorf("patch: unknown mode %d", int(mode))
	}
	// A patch never needs to be larger than the document it edits (D-029).
	if len(patchDoc) > validate.MaxIRBytes {
		return Result{PatchIssues: []model.ValidationIssue{{Code: validate.LimitExceeded, Severity: model.SeverityError,
			Message: fmt.Sprintf("the patch is %d bytes; the limit is %d", len(patchDoc), validate.MaxIRBytes)}}}, nil
	}
	ops, issues, err := parse(patchDoc)
	if err != nil || len(issues) > 0 {
		return Result{PatchIssues: issues}, err
	}
	if issues := check(ops, mode); len(issues) > 0 {
		return Result{PatchIssues: validate.Sort(issues)}, nil
	}
	var tree any
	if err := json.Unmarshal(base, &tree); err != nil {
		return Result{}, fmt.Errorf("base document: %w", err)
	}
	baseRefs := samplesRefs(tree)
	for i, op := range ops {
		if issue := wholeCatalog(tree, op, i); issue != nil {
			return Result{PatchIssues: []model.ValidationIssue{*issue}}, nil
		}
		if issue := apply(&tree, op, i); issue != nil {
			return Result{PatchIssues: []model.ValidationIssue{*issue}}, nil
		}
	}
	if issues := afterApply(tree, baseRefs, ops, mode); len(issues) > 0 {
		return Result{PatchIssues: validate.Sort(issues)}, nil
	}
	candidate, err := json.Marshal(tree, json.Deterministic(true))
	if err != nil {
		return Result{}, err
	}
	v, err := validate.IR(candidate)
	if err != nil {
		return Result{}, err
	}
	return Result{Candidate: candidate, Validation: v}, nil
}

func parse(doc []byte) (model.Patch, []model.ValidationIssue, error) {
	var probe any
	if err := json.Unmarshal(doc, &probe); err != nil {
		return nil, []model.ValidationIssue{{Code: validate.JSONInvalid, Message: err.Error(), Severity: model.SeverityError}}, nil
	}
	inst, err := jsonschema.UnmarshalJSON(bytes.NewReader(doc))
	if err != nil {
		return nil, nil, err
	}
	issues, err := validate.Schema("json-patch.schema.json", SchemaInvalid, inst)
	if err != nil || len(issues) > 0 {
		return nil, issues, err
	}
	var ops model.Patch
	if err := model.Decode(doc, &ops); err != nil {
		return nil, nil, fmt.Errorf("schema-valid patch does not decode: %w", err)
	}
	return ops, nil, nil
}

func issue(code string, op int, member, message string) model.ValidationIssue {
	return model.ValidationIssue{Code: code, Path: validate.Pointer(fmt.Sprint(op), member), Message: message,
		Severity: model.SeverityError}
}
