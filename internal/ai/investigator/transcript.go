package investigator

import (
	"bytes"
	"encoding/json/jsontext"
	"errors"
	"fmt"
	"slices"

	"github.com/kcl1104/geminus/internal/ai/provider"
)

// The planner's transcript (H-061 §5.4, slice A5), built from the ledger
// only:
//
//   - every turn's user message is the brief (internal/ai/aicontext
//     .Investigation: the investigation, and the server-written digest of
//     the steps recorded before this ADK run: each step's seq, tool,
//     canonical arguments and stored result, or its error's code and path;
//     never a note);
//   - within one ADK run (TranscriptReplay), each later turn replays the
//     earlier turns of the run: the model's content as returned (its
//     TurnReplay, in memory only) and the server's tool_result;
//   - a new run, with a fresh brief and digest, starts at every resume
//     (NewTranscript), after any invalid turn (Invalid), when a replayed
//     turn's tool is no longer offered, and at every turn in
//     TranscriptDigest, which needs only the single-turn tool_use verified
//     live for both planner models (H-060).
//
// Which one is H-061 A1's probe P1 (a replayed turn with its reasoning, then
// a tool_result, then a second tool_use) and the deployment's
// INVESTIGATOR_TRANSCRIPT; P1 is NOT RUN, so the default is the digest.

// TranscriptMode is how a run's later turns are built.
type TranscriptMode string

const (
	// TranscriptDigest: every turn is a fresh brief with the digest of
	// every step so far (the default).
	TranscriptDigest TranscriptMode = "digest"
	// TranscriptReplay: within a run, each later turn replays the run's
	// earlier turns as returned (only once probe P1 passes).
	TranscriptReplay TranscriptMode = "replay"
)

// ParseTranscriptMode reads INVESTIGATOR_TRANSCRIPT: "digest" (also when
// unset) or "replay"; any other value is an error.
func ParseTranscriptMode(s string) (TranscriptMode, error) {
	switch TranscriptMode(s) {
	case "", TranscriptDigest:
		return TranscriptDigest, nil
	case TranscriptReplay:
		return TranscriptReplay, nil
	}
	return "", errors.New("investigator: INVESTIGATOR_TRANSCRIPT is digest or replay")
}

// StepStatus is a ledger step's state (investigation.schema.json
// InvestigationStep.status).
type StepStatus string

const (
	StepPending StepStatus = "pending"
	StepDone    StepStatus = "done"
	StepRefused StepStatus = "refused"
	StepFailed  StepStatus = "failed"
)

// LedgerStep is one row of the investigation's tool-call ledger as the
// transcript and the digest read it: its seq, its tool (one of the seven or
// InvalidCall), its canonical arguments (none for invalid_call), its state,
// its stored observation (what the model was shown; none until observed, and
// none for finish), and its error for a refused or failed step. Never the
// note.
type LedgerStep struct {
	Seq         int
	Tool        string
	Arguments   []byte
	Status      StepStatus
	Observation []byte
	Error       *ToolError
}

// ToolError is a step's refusal or failure: a code and a JSON Pointer into
// its arguments.
type ToolError struct {
	Code Code   `json:"code"`
	Path string `json:"path"`
}

// Brief builds the sealed prompt of a run's first turn from the steps
// recorded so far (aicontext.Investigation with the investigation's brief
// and this digest). The transcript calls it, never anything else, for every
// prompt it sends.
type Brief func(ledger []LedgerStep) (provider.Prompt, error)

// Transcript is one attempt's planner transcript: the investigation's mode,
// the ledger mirror (every step recorded before and during this attempt),
// and in a replay the current run's prompt and its turns since.
type Transcript struct {
	mode   TranscriptMode
	inv    Mode
	brief  Brief
	ledger []LedgerStep
	// prompt and steps are the current run's (TranscriptReplay): its first
	// turn's prompt and the turns since, each one call and its result.
	prompt  provider.Prompt
	steps   []provider.ToolStep
	started bool
}

// NewTranscript starts a run (an attempt, or a resume) for an investigation
// in mode m from the ledger's steps, which must be the ledger's in order
// (seq 1, 2, ...), each resolved and observed but finish.
func NewTranscript(mode TranscriptMode, m Mode, brief Brief, ledger []LedgerStep) (*Transcript, error) {
	if _, err := ParseTranscriptMode(string(mode)); err != nil || mode == "" {
		return nil, errors.New("investigator: a transcript mode is required")
	}
	if _, ok := byMode[m]; !ok || brief == nil {
		return nil, errors.New("investigator: a mode and a brief are required")
	}
	t := &Transcript{mode: mode, inv: m, brief: brief}
	for _, s := range ledger {
		if err := t.check(s); err != nil {
			return nil, err
		}
		t.ledger = append(t.ledger, clone(s))
	}
	return t, nil
}

// Ledger is a copy of the ledger mirror.
func (t *Transcript) Ledger() []LedgerStep {
	out := make([]LedgerStep, len(t.ledger))
	for i, s := range t.ledger {
		out[i] = clone(s)
	}
	return out
}

func clone(s LedgerStep) LedgerStep {
	s.Arguments, s.Observation = slices.Clone(s.Arguments), slices.Clone(s.Observation)
	if s.Error != nil {
		e := *s.Error
		s.Error = &e
	}
	return s
}

// check is the rule every recorded step keeps: the next seq, a known tool
// or invalid_call, arguments as Decode reads them (canonical) for a tool and
// none for invalid_call, a resolved status, an error exactly when refused or
// failed (invalid_call: refused with UNKNOWN_TOOL or INVALID_ARGUMENTS), and
// an observation for every step but finish and invalid_call.
func (t *Transcript) check(s LedgerStep) error {
	fail := func(why string) error { return fmt.Errorf("investigator: ledger step %d: %s", s.Seq, why) }
	if s.Seq != len(t.ledger)+1 || s.Seq > MaxSteps {
		return fail("not the next step")
	}
	switch s.Status {
	case StepDone:
		if s.Error != nil {
			return fail("a done step with an error")
		}
	case StepRefused, StepFailed:
		if s.Error == nil || !slices.Contains(Codes, s.Error.Code) {
			return fail("a refused or failed step without its code")
		}
	default:
		return fail("not resolved")
	}
	if s.Tool == InvalidCall {
		if len(s.Arguments) > 0 || s.Status != StepRefused || (s.Error.Code != UnknownTool && s.Error.Code != InvalidArguments) || len(s.Observation) > 0 {
			return fail("an invalid_call step with arguments, a result or another code")
		}
		return nil
	}
	c, err := Decode(t.inv, s.Tool, s.Arguments)
	if err != nil || !bytes.Equal(c.Canonical, s.Arguments) {
		return fail("arguments the registry does not read as stored")
	}
	if Tool(s.Tool) == Finish {
		if len(s.Observation) > 0 || s.Status != StepDone {
			return fail("a finish with a result")
		}
		return nil
	}
	if len(s.Observation) == 0 || len(s.Observation) > MaxObservationBytes || !jsontext.Value(s.Observation).IsValid() {
		return fail("no observation, or one larger than its bound")
	}
	return nil
}

// Next is the turn the planner is asked now: the run's prompt (a fresh
// brief in a digest, or at a run's start), the tools the budget leaves, the
// run's earlier turns in a replay, and the registry's decoder. A replayed
// turn whose tool the budget no longer offers starts a new run.
func (t *Transcript) Next(org string, attr provider.Attribution, planner string, b Budget) (provider.PlanContext, error) {
	offered := Offered(t.inv, b)
	tools, err := ProviderTools(offered)
	if err != nil {
		return provider.PlanContext{}, err
	}
	for _, s := range t.steps {
		if !slices.Contains(offered, Tool(s.Calls[0].Name)) {
			t.started = false
		}
	}
	if t.mode == TranscriptDigest || !t.started {
		p, err := t.brief(t.Ledger())
		if err != nil {
			return provider.PlanContext{}, err
		}
		if p.Role != Role {
			return provider.PlanContext{}, errors.New("investigator: the brief is not the investigator's")
		}
		t.prompt, t.steps, t.started = p, nil, true
	}
	return provider.PlanContext{OrganizationID: org, Prompt: t.prompt, Attribution: attr, Model: planner, Tools: tools,
		Steps: slices.Clone(t.steps), Decoder: Decoder{Mode: t.inv}}, nil
}

// Record adds the step the turn's call became, once observed (finish: once
// done), to the ledger mirror; in a replay its turn joins the run. The step
// must be the call's: the next seq, its tool and its canonical arguments.
func (t *Transcript) Record(res provider.PlanResult, s LedgerStep) error {
	if res.Call.Name != s.Tool || s.Tool == InvalidCall {
		return errors.New("investigator: the step is not the call's")
	}
	if !bytes.Equal(res.Call.Input, s.Arguments) {
		return errors.New("investigator: the step's arguments are not the call's")
	}
	if err := t.check(s); err != nil {
		return err
	}
	t.ledger = append(t.ledger, clone(s))
	if t.mode == TranscriptReplay && t.started && Tool(s.Tool) != Finish {
		t.steps = append(t.steps, provider.ToolStep{Calls: []provider.ToolCall{res.Call},
			Results: []provider.ToolResult{{CallID: res.Call.ID, Content: jsontext.Value(slices.Clone(s.Observation)),
				IsError: s.Status != StepDone}}, Replay: res.Replay})
	}
	return nil
}

// Invalid ends the run after an invalid turn (H-061 §7): a recorded one's
// invalid_call step joins the ledger mirror (step is nil for one the ledger
// does not record), and the next turn starts a new run from the digest.
func (t *Transcript) Invalid(step *LedgerStep) error {
	if step != nil {
		if step.Tool != InvalidCall {
			return errors.New("investigator: an invalid turn's step is invalid_call")
		}
		if err := t.check(*step); err != nil {
			return err
		}
		t.ledger = append(t.ledger, clone(*step))
	}
	t.started, t.steps = false, nil
	return nil
}

// InvalidStep is the refused invalid_call step an invalid turn becomes when
// the ledger records it (provider.InvalidTurn.Recorded), at seq; nil for an
// unrecorded one.
func InvalidStep(seq int, inv provider.InvalidTurn) *LedgerStep {
	if !inv.Recorded() {
		return nil
	}
	return &LedgerStep{Seq: seq, Tool: InvalidCall, Status: StepRefused, Error: &ToolError{Code: Code(inv.Code), Path: inv.Path}}
}
