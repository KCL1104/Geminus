// Package investigator is the registry of the tools an AI investigation's
// planner may call (H-061 §7, PROPOSED pending the owner's confirmation;
// behind GEMINUS_INVESTIGATIONS): their names, the tools each mode offers,
// their arguments, the closed refusal codes, the caps, and the one strict
// decoder that the worker (before it records a call in the ledger) and the
// API's executor (before it carries one out) both use, so the two sides can
// never read a call differently.
//
// schemas/investigator.schema.json is the source of truth; the tests hold
// this package to it. Decode runs the schema itself (internal/model/
// validate) and adds what a schema cannot say: the strict Go decoding into
// the typed arguments and Unicode NFC for text. A decoded call is
// schema-valid, which is not authorized: the executor still checks it under
// the requester's scope and current role before it has any effect.
//
// Computation only: no storage, tenant scope, network or provider
// (architecture rules ai-suggests-only and ai-is-pure-but-the-adapter).
package investigator

import (
	"encoding/json/v2"
	"errors"
	"fmt"
	"reflect"
	"slices"
	"strings"

	"golang.org/x/text/unicode/norm"

	"github.com/kcl1104/geminus/internal/model"
	"github.com/kcl1104/geminus/internal/model/hashing"
	"github.com/kcl1104/geminus/internal/model/validate"
)

// Tool names one of the seven tools.
type Tool string

const (
	ProposeWhatIf    Tool = "propose_what_if"
	SaveScenario     Tool = "save_scenario"
	RunWhatIf        Tool = "run_what_if"
	ExplainResults   Tool = "explain_results"
	ProposeModelEdit Tool = "propose_model_edit"
	WriteToDraft     Tool = "write_to_draft"
	Finish           Tool = "finish"
)

// InvalidCall is the ledger's tool for model output that named no offered
// tool or did not decode (a refused step with UNKNOWN_TOOL or
// INVALID_ARGUMENTS and no stored arguments). No mode offers it.
const InvalidCall = "invalid_call"

// Tools is every tool, in the schema's order.
var Tools = []Tool{ProposeWhatIf, SaveScenario, RunWhatIf, ExplainResults, ProposeModelEdit, WriteToDraft, Finish}

// Mode is an investigation's mode.
type Mode string

const (
	Scenario Mode = "scenario" // what-ifs, saved and run against a baseline
	Edit     Mode = "edit"     // one change written into a clean draft
)

// Modes is every mode, in the schema's order.
var Modes = []Mode{Scenario, Edit}

var byMode = map[Mode][]Tool{
	Scenario: {ProposeWhatIf, SaveScenario, RunWhatIf, ExplainResults, Finish},
	Edit:     {ProposeModelEdit, WriteToDraft, Finish},
}

// ToolsOf is the tools mode offers (nil for an unknown mode). finish is in
// both; which of them a turn offers is narrowed further by the budget.
func ToolsOf(m Mode) []Tool { return slices.Clone(byMode[m]) }

// Offered reports whether mode m offers t.
func (t Tool) Offered(m Mode) bool { return slices.Contains(byMode[m], t) }

// Code is an InvestigationToolCode: why a call was refused or failed. A
// closed list of its own, not an HTTP ErrorCode.
type Code string

const (
	LimitReached         Code = "LIMIT_REACHED"
	BaseMoved            Code = "BASE_MOVED"
	DraftChanged         Code = "DRAFT_CHANGED"
	ProposalNotReady     Code = "PROPOSAL_NOT_READY"
	ProposalDecided      Code = "PROPOSAL_DECIDED"
	RecheckFailed        Code = "RECHECK_FAILED"
	RunNotCompleted      Code = "RUN_NOT_COMPLETED"
	AlreadyRun           Code = "ALREADY_RUN"
	NotInInvestigation   Code = "NOT_IN_INVESTIGATION"
	ModeMismatch         Code = "MODE_MISMATCH"
	UnknownTool          Code = "UNKNOWN_TOOL"
	InvalidArguments     Code = "INVALID_ARGUMENTS"
	ServiceUnavailable   Code = "SERVICE_UNAVAILABLE"
	DatasetStorageFailed Code = "DATASET_STORAGE_FAILED"
	Internal             Code = "INTERNAL"
)

// Codes is every code, in the schema's order.
var Codes = []Code{LimitReached, BaseMoved, DraftChanged, ProposalNotReady, ProposalDecided, RecheckFailed, RunNotCompleted,
	AlreadyRun, NotInInvestigation, ModeMismatch, UnknownTool, InvalidArguments, ServiceUnavailable, DatasetStorageFailed, Internal}

// Outcome is what the AI said when it called finish: kept and shown as its
// claim, never as the server's verdict.
type Outcome string

const (
	ImprovementFound Outcome = "improvement_found"
	NoImprovement    Outcome = "no_improvement"
	OutOfIdeas       Outcome = "out_of_ideas"
	NeedsPerson      Outcome = "needs_person"
)

// Outcomes is every outcome, in the schema's order.
var Outcomes = []Outcome{ImprovementFound, NoImprovement, OutOfIdeas, NeedsPerson}

// H-060's caps and the ledger's bounds (H-061 §11, §12).
const (
	MaxSteps        = 14 // finish included
	MaxWhatIfs      = 3
	MaxRuns         = 4 // the baseline (slot 0) and one per what-if
	MaxExplanations = 4
	Minutes         = 20 // from creation, queue time included
	// MaxArgumentBytes bounds a call's arguments as the model wrote them
	// (the ledger's arguments column holds at most 8 KiB).
	MaxArgumentBytes = 8 << 10
	// MaxNoteRunes bounds the AI's note on a step.
	MaxNoteRunes = 500
	// MaxObservationBytes bounds a result the planner is shown.
	MaxObservationBytes = 6 << 10
)

// Budget is what the investigation may still do, shown with every result.
type Budget struct {
	WhatIfsLeft      int `json:"what_ifs_left"`
	RunsLeft         int `json:"runs_left"`
	ExplanationsLeft int `json:"explanations_left"`
	StepsLeft        int `json:"steps_left"`
}

// Arguments is one tool's decoded arguments.
type Arguments interface {
	// Tool is the tool these arguments are for.
	Tool() Tool
}

// SuggestionRef names one of an analysis's suggested experiments.
type SuggestionRef struct {
	AnalysisID string `json:"analysis_id"`
	Index      int    `json:"index"`
}

// ProposeWhatIfArguments are propose_what_if's (scenario mode).
type ProposeWhatIfArguments struct {
	Idea           string         `json:"idea"` // AI text
	FromSuggestion *SuggestionRef `json:"from_suggestion"`
}

// SaveScenarioArguments are save_scenario's (scenario mode).
type SaveScenarioArguments struct {
	ProposalID string `json:"proposal_id"`
}

// RunWhatIfArguments are run_what_if's (scenario mode).
type RunWhatIfArguments struct {
	ScenarioID string `json:"scenario_id"`
}

// ExplainResultsArguments are explain_results' (scenario mode).
type ExplainResultsArguments struct {
	RunID    string  `json:"run_id"`
	Question *string `json:"question"` // AI text, or nil
}

// ProposeModelEditArguments are propose_model_edit's (edit mode).
type ProposeModelEditArguments struct {
	Idea string `json:"idea"` // AI text
}

// WriteToDraftArguments are write_to_draft's (edit mode).
type WriteToDraftArguments struct {
	ProposalID string `json:"proposal_id"`
}

// FinishArguments are finish's (both modes).
type FinishArguments struct {
	Outcome        Outcome `json:"outcome"`
	BestScenarioID *string `json:"best_scenario_id"`
}

func (ProposeWhatIfArguments) Tool() Tool    { return ProposeWhatIf }
func (SaveScenarioArguments) Tool() Tool     { return SaveScenario }
func (RunWhatIfArguments) Tool() Tool        { return RunWhatIf }
func (ExplainResultsArguments) Tool() Tool   { return ExplainResults }
func (ProposeModelEditArguments) Tool() Tool { return ProposeModelEdit }
func (WriteToDraftArguments) Tool() Tool     { return WriteToDraft }
func (FinishArguments) Tool() Tool           { return Finish }

// tools holds each tool's schema definition and Go arguments type.
var tools = map[Tool]struct {
	def string
	typ reflect.Type
}{
	ProposeWhatIf:    {"ProposeWhatIfArguments", reflect.TypeFor[ProposeWhatIfArguments]()},
	SaveScenario:     {"SaveScenarioArguments", reflect.TypeFor[SaveScenarioArguments]()},
	RunWhatIf:        {"RunWhatIfArguments", reflect.TypeFor[RunWhatIfArguments]()},
	ExplainResults:   {"ExplainResultsArguments", reflect.TypeFor[ExplainResultsArguments]()},
	ProposeModelEdit: {"ProposeModelEditArguments", reflect.TypeFor[ProposeModelEditArguments]()},
	WriteToDraft:     {"WriteToDraftArguments", reflect.TypeFor[WriteToDraftArguments]()},
	Finish:           {"FinishArguments", reflect.TypeFor[FinishArguments]()},
}

// Schema is the embedded schema of t's arguments ("" for an unknown tool),
// for the provider's input_schema and for validate.
func Schema(t Tool) string {
	if d, ok := tools[t]; ok {
		return "investigator.schema.json#/$defs/" + d.def
	}
	return ""
}

// Call is one decoded call.
type Call struct {
	Tool      Tool
	Arguments Arguments
	// Canonical is the arguments' RFC 8785 form: what the ledger stores
	// and the digest shows.
	Canonical []byte
	// Hash is sha256 of Canonical ("sha256:<hex>").
	Hash string
}

// Refusal is why a call was refused: a code and a JSON Pointer into its
// arguments. The pointer names only the tool's own members ("" for the
// call as a whole), never a member the model made up, and the error text
// never repeats the input.
type Refusal struct {
	Code Code
	Path string
}

func (r *Refusal) Error() string { return fmt.Sprintf("investigator: %s at %q", r.Code, r.Path) }

// ErrRefused is what every *Refusal is (errors.Is).
var ErrRefused = errors.New("investigator: the call is refused")

func (r *Refusal) Unwrap() error { return ErrRefused }

// Decode reads the model's call of tool name with arguments raw for an
// investigation in mode m: the tool must be one m offers, raw must be one
// JSON object of at most MaxArgumentBytes that the tool's schema takes
// (every member, nothing else; no duplicate member, invalid UTF-8 or
// trailing data), and its text must be NFC. Any other input is a *Refusal.
func Decode(m Mode, name string, raw []byte) (Call, error) {
	if _, ok := byMode[m]; !ok {
		return Call{}, &Refusal{Internal, ""}
	}
	t := Tool(name)
	d, ok := tools[t]
	switch {
	case !ok:
		return Call{}, &Refusal{UnknownTool, ""}
	case !t.Offered(m):
		return Call{}, &Refusal{ModeMismatch, ""}
	case len(raw) > MaxArgumentBytes:
		return Call{}, &Refusal{InvalidArguments, ""}
	}
	issues, err := validate.Document(raw, Schema(t), string(InvalidArguments))
	if err != nil {
		return Call{}, &Refusal{Internal, ""}
	}
	known := pointers(d.typ)
	if len(issues) > 0 {
		return Call{}, &Refusal{InvalidArguments, refusedAt(issues, known)}
	}
	v := reflect.New(d.typ)
	if err := model.Decode(raw, v.Interface()); err != nil {
		// Schema-valid but not the Go shape: an integer written 1.0, which
		// 2020-12 counts as an integer and Go's strict decoding refuses.
		at := ""
		var se *json.SemanticError
		if errors.As(err, &se) {
			at = nearest(string(se.JSONPointer), known)
		}
		return Call{}, &Refusal{InvalidArguments, at}
	}
	if path, ok := notNFC(v.Elem(), ""); ok {
		return Call{}, &Refusal{InvalidArguments, path}
	}
	args := v.Elem().Interface().(Arguments)
	enc, err := model.Encode(args)
	if err != nil {
		return Call{}, &Refusal{Internal, ""}
	}
	canonical, err := hashing.Canonical(enc)
	if err != nil {
		return Call{}, &Refusal{Internal, ""}
	}
	return Call{Tool: t, Arguments: args, Canonical: canonical, Hash: hashing.Sum(canonical)}, nil
}

// Encode is the call the server holds arguments a for, checked by Decode,
// so it never yields what Decode would refuse.
func Encode(m Mode, a Arguments) (Call, error) {
	if a == nil || reflect.ValueOf(a).Kind() != reflect.Struct {
		return Call{}, &Refusal{Internal, ""}
	}
	raw, err := model.Encode(a)
	if err != nil {
		return Call{}, &Refusal{Internal, ""}
	}
	return Decode(m, string(a.Tool()), raw)
}

// refusedAt is where a refused document is refused: the deepest issue, as
// the nearest member the tool declares. A malformed document is refused
// as a whole.
func refusedAt(issues []model.ValidationIssue, known []string) string {
	best := ""
	for _, i := range issues {
		if i.Code == validate.JSONInvalid {
			return ""
		}
		if p := nearest(i.Path, known); strings.Count(p, "/") > strings.Count(best, "/") {
			best = p
		}
	}
	return best
}

// nearest is the longest of known that is path or one of its ancestors.
func nearest(path string, known []string) string {
	best := ""
	for _, k := range known {
		if (path == k || strings.HasPrefix(path, k+"/")) && len(k) > len(best) {
			best = k
		}
	}
	return best
}

// pointers is every member pointer of an arguments type, nested objects
// included.
func pointers(t reflect.Type) []string {
	var out []string
	var walk func(t reflect.Type, at string)
	walk = func(t reflect.Type, at string) {
		for t.Kind() == reflect.Pointer {
			t = t.Elem()
		}
		if t.Kind() != reflect.Struct {
			return
		}
		for i := range t.NumField() {
			f := t.Field(i)
			name, _, _ := strings.Cut(f.Tag.Get("json"), ",")
			out = append(out, at+"/"+name)
			walk(f.Type, at+"/"+name)
		}
	}
	walk(t, "")
	return out
}

// notNFC finds a string member of v that is not in Unicode NFC.
func notNFC(v reflect.Value, at string) (string, bool) {
	switch v.Kind() {
	case reflect.Pointer:
		if v.IsNil() {
			return "", false
		}
		return notNFC(v.Elem(), at)
	case reflect.String:
		return at, !norm.NFC.IsNormalString(v.String())
	case reflect.Struct:
		for i := range v.NumField() {
			name, _, _ := strings.Cut(v.Type().Field(i).Tag.Get("json"), ",")
			if p, bad := notNFC(v.Field(i), at+"/"+name); bad {
				return p, true
			}
		}
	}
	return "", false
}
