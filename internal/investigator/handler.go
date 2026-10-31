// Package investigator is the worker's side of an AI investigation (H-060,
// H-061 §5, approved in H-063; slice A6; behind GEMINUS_INVESTIGATIONS):
// the job handler of the durable `investigation` job, run only on a
// worker's agent lanes (cmd/worker). One attempt:
//
//   - reads the investigation, its tool-call ledger and the database's now
//     in one Checkpoint (H-061 §5.2); an investigation that has ended (a
//     person's stop, role_lost, the API's time_limit) or whose deadline has
//     passed is finished at once, with no model call;
//   - settles the ledger's last call if an earlier attempt left it
//     unresolved or unobserved (a call is never sent twice: H-061 §5.7);
//   - runs the planner agent (internal/aigraph/investigator over
//     einomodel.Planner and a separate investigator provider.Client on the
//     same spend ledger) in a goroutine with recover, until deadline_at − 5 s;
//     the agent's tool bodies send each call to this handler's goroutine,
//     which owns every Checkpoint (H-061 §5.5): it records the call under
//     the lease, polls it every 500 ms until the API's executor carries it
//     out, refuses or fails it, waits every second for what it made (the
//     proposal, the run pair, the analysis), writes the observation once
//     (H-061 §5.6) and hands it back; between calls it polls the
//     investigation every 500 ms, so a stop cancels a turn in flight;
//   - records an invalid turn as a refused invalid_call step (or not at all,
//     H-061 §7), starts a new ADK run from the digest after it, and ends the
//     investigation with invalid_answer after two invalid turns or three
//     refused steps in a row; waits 1 s and asks again after
//     concurrency_limited (nothing reserved); asks a failed turn again at
//     most twice (each its own one-attempt reservation, H-061 §12);
//   - ends with one Finish transaction (H-061 §5.8): the finish call when the
//     planner chose it, the investigation's end (finished_by_ai with its
//     claimed outcome, draft_written after a written draft, limit_reached,
//     time_limit, spend_cap, invalid_answer, ai_failed), the worker's audit
//     event investigation.end {reason, steps, planner_turns}, and the job's
//     completion; or only the event and the completion once the API ended it.
//
// A resume (the lease lapsed, the reaper requeued the job, another agent
// lane claimed it) starts over from the stored ledger: the transcript is
// deterministic from the stored observations, which fix what the planner
// saw. The handler writes nothing in a person's name (the API's executor
// does, under the stored acting-for scope), and no row of an existing
// table: only the ledger's calls and observations, the investigation's
// end, its job and its audit event (migration 0019's worker grants). It
// logs IDs, codes and counts, never a prompt, an answer, a note, an idea or
// the person's words.
package investigator

import (
	"context"
	"encoding/json/v2"
	"errors"
	"fmt"
	"log/slog"
	"regexp"
	"slices"
	"strings"
	"time"

	"github.com/kcl1104/geminus/internal/ai/aicontext"
	reg "github.com/kcl1104/geminus/internal/ai/investigator"
	"github.com/kcl1104/geminus/internal/ai/provider"
	"github.com/kcl1104/geminus/internal/aigraph/einomodel"
	agent "github.com/kcl1104/geminus/internal/aigraph/investigator"
	"github.com/kcl1104/geminus/internal/domainpack"
	"github.com/kcl1104/geminus/internal/model"
	"github.com/kcl1104/geminus/internal/model/hashing"
	inv "github.com/kcl1104/geminus/internal/storage/investigations"
	"github.com/kcl1104/geminus/internal/storage/jobs"
	"github.com/kcl1104/geminus/internal/worker/handler"
)

// PackLookup finds a model's domain pack; *domainpack.Registry is one.
type PackLookup interface {
	Lookup(domain string) (domainpack.DomainPack, error)
}

// Config is the handler's setup.
type Config struct {
	// Planner is the investigator's provider.Client (its own client on the
	// worker's spend ledger, MaxConcurrentPerOrganization the agent lanes).
	Planner einomodel.PlanTurner
	// Budget bounds the brief the handler builds; it is the planner
	// client's.
	Budget provider.Budget
	Packs  PackLookup
	// Actor names the worker in audit events (WORKER_ID).
	Actor string
	Log   *slog.Logger
	// Transcript is INVESTIGATOR_TRANSCRIPT (digest by default).
	Transcript reg.TranscriptMode
	// PlannerModels are the planner models this worker runs
	// (INVESTIGATION_PLANNER_MODELS; muse-spark-1.3 by default, H-061 §14):
	// an investigation of another planner ends ai_failed before any call.
	PlannerModels []string
	// Poll is how often a pending call and the investigation are read
	// (500 ms); ChildPoll how often what a call made is (1 s).
	Poll, ChildPoll time.Duration
	// Sleep waits d or until ctx ends (a timer by default).
	Sleep func(ctx context.Context, d time.Duration) error
}

// Handler runs investigation jobs (handler.Handler).
type Handler struct{ cfg Config }

var _ handler.Handler = (*Handler)(nil)

var (
	actorRef = regexp.MustCompile(`^[A-Za-z][A-Za-z0-9_.:-]{0,127}$`)
	invIDs   = regexp.MustCompile(`^inv_[0-7][0-9A-HJKMNP-TV-Z]{25}$`)
	reqIDs   = regexp.MustCompile(`^req_[0-7][0-9A-HJKMNP-TV-Z]{25}$`)
)

// Failure codes of an investigation job (jobs.Failure.Code).
const (
	// InputInvalid: the job does not name its investigation and request, or
	// its run cannot end its job as an investigation needs (never retried).
	InputInvalid = "INVESTIGATION_INPUT_INVALID"
	// Interrupted: an attempt stopped without an end of its own (a turn
	// that kept failing, an internal error): retried, the next attempt
	// resumes from the ledger.
	Interrupted = "INVESTIGATION_INTERRUPTED"
)

// The loop's bounds (H-061 §12, §13).
const (
	maxPlannerTurns = 20
	maxTurnRetries  = 2
	// deadlineMargin: the agent's context ends this long before deadline_at.
	deadlineMargin = 5 * time.Second
)

// New checks cfg.
func New(cfg Config) (*Handler, error) {
	switch {
	case cfg.Planner == nil || cfg.Packs == nil || cfg.Log == nil:
		return nil, errors.New("investigator: a planner client, packs and a log are required")
	case !actorRef.MatchString(cfg.Actor):
		return nil, errors.New("investigator: the worker's name must start with a letter and use only letters, digits, '_', '.', ':' and '-' (at most 128)")
	}
	if cfg.Budget == (provider.Budget{}) {
		cfg.Budget = provider.DefaultBudget()
	}
	if err := cfg.Budget.Validate(); err != nil {
		return nil, fmt.Errorf("investigator: %w", err)
	}
	if cfg.Transcript == "" {
		cfg.Transcript = reg.TranscriptDigest
	}
	if _, err := reg.ParseTranscriptMode(string(cfg.Transcript)); err != nil {
		return nil, err
	}
	if len(cfg.PlannerModels) == 0 {
		cfg.PlannerModels = []string{"muse-spark-1.3"}
	}
	if cfg.Poll <= 0 {
		cfg.Poll = 500 * time.Millisecond
	}
	if cfg.ChildPoll <= 0 {
		cfg.ChildPoll = time.Second
	}
	if cfg.Sleep == nil {
		cfg.Sleep = sleep
	}
	if _, err := reg.ProviderTools(reg.Tools); err != nil {
		return nil, err
	}
	return &Handler{cfg: cfg}, nil
}

func sleep(ctx context.Context, d time.Duration) error {
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-t.C:
		return nil
	}
}

func failure(code, message string, retryable bool) error {
	return &jobs.Failure{Code: code, Message: message, Retryable: retryable}
}

// payload is the job's reference (createInvestigation).
type payload struct {
	InvestigationID string `json:"investigation_id"`
	RequestID       string `json:"request_id"`
}

// Handle runs one attempt of an investigation job.
func (h *Handler) Handle(ctx context.Context, hr handler.Run) error {
	job := hr.Job()
	var p payload
	if err := json.Unmarshal(job.Payload, &p, json.RejectUnknownMembers(true)); err != nil || p.InvestigationID != job.Resource.InvestigationID ||
		!invIDs.MatchString(p.InvestigationID) || !reqIDs.MatchString(p.RequestID) {
		return failure(InputInvalid, "the job does not name its investigation and request", false)
	}
	ender, ok := hr.(handler.Ender)
	if !ok {
		return failure(InputInvalid, "the worker's run cannot end an investigation's job", false)
	}
	l := &dbLedger{run: hr, ender: ender, org: job.OrganizationID, id: p.InvestigationID, requestID: p.RequestID, actor: h.cfg.Actor}
	return h.attempt(ctx, l, job, p)
}

// attempt runs one attempt over the ledger l.
func (h *Handler) attempt(ctx context.Context, l Ledger, job jobs.Job, p payload) error {
	st, err := l.Load(ctx)
	if err != nil {
		return err
	}
	s := &session{h: h, l: l, job: job, inv: st.Investigation, calls: st.Calls, turns: st.PlannerTurns,
		attr:  provider.Attribution{TargetType: provider.PlannerTarget, TargetID: p.InvestigationID, RequestID: p.RequestID},
		clock: newClock(st.Now)}
	i := st.Investigation
	switch {
	case i.Status != inv.Running:
		reason := inv.AIFailed
		if i.Reason != nil {
			reason = *i.Reason
		}
		return s.finish(ctx, Ending{Reason: reason, Already: true})
	case !st.Now.Before(i.DeadlineAt):
		return s.finish(ctx, Ending{Reason: inv.TimeLimit})
	case !slices.Contains(h.cfg.PlannerModels, i.PlannerModel):
		// H-061 §14: Muse Wonder 1.0 is a planner only where the deployment
		// lists it (default off).
		h.cfg.Log.WarnContext(ctx, "an investigation's planner is not offered by this worker", "organization_id", i.OrganizationID,
			"investigation_id", i.ID, "code", "PLANNER_NOT_OFFERED")
		return s.finish(ctx, Ending{Reason: inv.AIFailed})
	}
	if err := s.prepare(st); err != nil {
		h.cfg.Log.WarnContext(ctx, "an investigation's brief cannot be built", "organization_id", i.OrganizationID, "investigation_id", i.ID,
			"code", "INVESTIGATION_CONTEXT_INVALID")
		return s.finish(ctx, Ending{Reason: inv.AIFailed})
	}
	return s.loop(ctx)
}

// clock maps the database's time onto this process's monotonic clock: the
// deadline is the database's (deadline_at), the waits are local.
type clock struct {
	db    time.Time
	local time.Time
}

func newClock(db time.Time) clock { return clock{db: db, local: time.Now()} }

// until is the local time at which the database's t is reached.
func (c clock) until(t time.Time) time.Time { return c.local.Add(t.Sub(c.db)) }

// session is one attempt's state. Its fields are the handler goroutine's;
// the agent's Env methods (run in Eino's goroutines while the handler
// goroutine waits for them) reach them only through the request channel or
// under mu.
type session struct {
	h     *Handler
	l     Ledger
	job   jobs.Job
	inv   inv.Investigation
	calls []inv.Call
	attr  provider.Attribution
	clock clock

	mode    reg.Mode
	tools   []provider.Tool
	brief   aicontext.InvestigationInput
	tr      *reg.Transcript
	turns   int
	retries int
	// invalidRow and refusedRow count invalid turns and refused steps in a
	// row (H-061 §7).
	invalidRow, refusedRow int
	// ended is the investigation's end once a poll saw it (the API's stop,
	// role_lost or time_limit).
	ended    *inv.Reason
	reqs     chan request
	runTurns int
}

// request is the agent's ask of the handler's goroutine: a turn's context
// (next), or a tool body's call to carry out.
type request struct {
	ctx   context.Context
	next  bool
	res   provider.PlanResult
	reply chan reply
}

type reply struct {
	obs agent.Observed
	pc  *provider.PlanContext
	err error
}

// prepare builds the brief's inputs, the mode's tools and the transcript
// over the ledger's settled steps.
func (s *session) prepare(st State) error {
	i := st.Investigation
	s.mode = reg.Mode(i.Mode)
	tools, err := reg.ProviderTools(reg.ToolsOf(s.mode))
	if err != nil || len(tools) == 0 {
		return errors.New("investigator: the mode has no tools")
	}
	s.tools = tools
	var ir model.ModelIR
	if err := model.Decode(st.Version.IR, &ir); err != nil {
		return err
	}
	pack, err := s.h.cfg.Packs.Lookup(ir.Domain)
	if err != nil {
		return err
	}
	in := aicontext.InvestigationInput{
		Scope:   aicontext.Scope{OrganizationID: i.OrganizationID, ModelID: i.ModelID},
		Version: aicontext.Version{OrganizationID: i.OrganizationID, ModelID: st.Version.ModelID, VersionID: st.Version.ID, IR: &ir},
		Domain:  pack.PromptContext(), InvestigationID: i.ID, Mode: string(i.Mode), Caps: aicontext.InvestigationBudget(capsOf(i)),
		OutputLocale: i.OutputLocale, Budget: s.h.cfg.Budget,
	}
	if i.Intent != nil {
		in.Request = *i.Intent
	}
	var c struct {
		Selected []string `json:"selected_entity_ids"`
	}
	if len(i.Context) > 0 && json.Unmarshal(i.Context, &c) == nil {
		in.SelectedEntityIDs = c.Selected
	}
	if i.Mode == inv.Scenario {
		if len(i.Objective) > 0 {
			var o aicontext.Objective
			if err := json.Unmarshal(i.Objective, &o); err != nil {
				return err
			}
			in.Objective = &o
		}
		in.RunSettings = i.RunConfig
	}
	if i.SourceAnalysisID != nil {
		for n, sg := range st.Suggestions {
			if n >= 10 {
				break
			}
			in.Seeds = append(in.Seeds, aicontext.Seed{AnalysisID: *i.SourceAnalysisID, Index: n, Name: sg.Name, Hypothesis: sg.Hypothesis})
		}
	}
	s.brief = in
	return nil
}

// newTranscript starts the transcript over the settled steps.
func (s *session) newTranscript() error {
	brief := func(ledger []reg.LedgerStep) (provider.Prompt, error) {
		b := s.brief
		b.Digest, b.Left = BriefSteps(ledger), aicontext.InvestigationBudget(s.budget())
		return aicontext.Investigation(b)
	}
	result := func(observation []byte) (jsontextValue, error) {
		return aicontext.InvestigationResult(s.brief, observation)
	}
	steps, err := mirror(s.calls)
	if err != nil {
		return err
	}
	s.tr, err = reg.NewTranscript(s.h.cfg.Transcript, s.mode, brief, result, steps)
	return err
}

// loop settles the ledger's last call, then runs the agent until the
// investigation ends.
func (s *session) loop(ctx context.Context) error {
	if n := len(s.calls); n > 0 {
		last := s.calls[n-1]
		if last.Status == inv.Pending || last.ObservedAt == nil {
			// A call an earlier attempt recorded and did not see through: it is
			// waited for, never sent again (H-061 §5.2, §5.7).
			step, err := s.settle(ctx, last.Seq)
			if err != nil {
				return s.end(ctx, err)
			}
			s.calls[n-1] = step
		}
	}
	if err := s.newTranscript(); err != nil {
		s.h.cfg.Log.WarnContext(ctx, "an investigation's ledger cannot be read into a transcript", "organization_id", s.inv.OrganizationID,
			"investigation_id", s.inv.ID, "code", "INVESTIGATION_LEDGER_INVALID")
		return s.finish(ctx, Ending{Reason: inv.AIFailed})
	}
	if n := len(s.calls); n > 0 {
		if last := s.calls[n-1]; reg.Tool(last.Tool) == reg.WriteToDraft && last.Status == inv.Done {
			return s.finish(ctx, Ending{Reason: inv.DraftWritten})
		}
	}
	// H-061 §7 counts on from the stored ledger, so the end does not depend
	// on whether a worker restarted between the steps.
	s.invalidRow, s.refusedRow = inARow(s.calls)
	if s.invalidRow >= 2 || s.refusedRow >= 3 {
		return s.finish(ctx, Ending{Reason: inv.InvalidAnswer})
	}
	for {
		b := s.budget()
		switch {
		case b.StepsLeft <= 0 || s.turns >= maxPlannerTurns:
			return s.finish(ctx, Ending{Reason: inv.LimitReached})
		case s.ended != nil:
			return s.finish(ctx, Ending{Reason: *s.ended, Already: true})
		}
		o := s.run(ctx, b.StepsLeft)
		s.turns += o.Turns
		if err := ctx.Err(); err != nil {
			return err // the attempt is stopping: the worker records it
		}
		switch o.Kind {
		case agent.Finished:
			return s.finishWith(ctx, *o.Finish)
		case agent.Ended:
			return s.end(ctx, o.End)
		case agent.LimitReached:
			return s.finish(ctx, Ending{Reason: inv.LimitReached})
		case agent.Invalid:
			if err := s.invalid(ctx, o.Invalid); err != nil {
				return s.end(ctx, err)
			}
			if s.invalidRow >= 2 || s.refusedRow >= 3 {
				return s.finish(ctx, Ending{Reason: inv.InvalidAnswer})
			}
		case agent.Failed:
			if again, err := s.failed(ctx, o.Err); !again {
				return err
			}
		case agent.Cancelled:
			// The agent's context ended: a stop seen by a poll, or the
			// deadline.
			if s.ended != nil {
				return s.finish(ctx, Ending{Reason: *s.ended, Already: true})
			}
			return s.finish(ctx, Ending{Reason: inv.TimeLimit})
		default:
			s.h.cfg.Log.WarnContext(ctx, "an investigation's agent run failed", "organization_id", s.inv.OrganizationID,
				"investigation_id", s.inv.ID, "code", "INVESTIGATION_AGENT_FAILED")
			return s.retryable(ctx, Interrupted, "the investigation's agent failed; the next attempt resumes from its ledger")
		}
	}
}

// inARow is what the ledger's last calls count toward invalid_answer
// (H-061 §7), as an attempt counts them while it records them: the
// recorded invalid turns at its end (invalid_call rows; an invalid turn with
// no tool_use leaves no row, so an earlier attempt's is not counted) and the
// refused or failed steps at its end (invalid_call among them). A done step
// ends both rows.
func inARow(calls []inv.Call) (invalid, refused int) {
	for n := len(calls) - 1; n >= 0 && calls[n].Status != inv.Done; n-- {
		if calls[n].Tool == inv.InvalidCall && invalid == refused {
			invalid++
		}
		refused++
	}
	return invalid, refused
}

// failed handles a failed planner turn (H-061 §13): again when the turn is
// asked again, otherwise the attempt's end (the error Handle returns).
func (s *session) failed(ctx context.Context, err error) (again bool, _ error) {
	code := provider.CodeOf(err)
	switch code {
	case provider.CodeQuotaExceeded:
		return false, s.finish(ctx, Ending{Reason: inv.SpendCap})
	case provider.CodeConcurrencyLimited:
		// Nothing was reserved or asked: wait 1 s and ask again.
		if err := s.h.cfg.Sleep(ctx, time.Second); err != nil {
			return false, err
		}
		return true, nil
	case provider.CodeTimeout, provider.CodeUnavailable, provider.CodeRateLimited, provider.CodeSpendUnavailable:
		if s.retries < maxTurnRetries {
			s.retries++
			return true, nil
		}
		return false, s.retryable(ctx, "AI_"+strings.ToUpper(string(code)), "the planner kept failing; the next attempt resumes from its ledger")
	}
	// Not configured, not approved, refused by the policy, or a request
	// the server should not have built: the investigation cannot go on.
	s.h.cfg.Log.WarnContext(ctx, "an investigation's planner turn failed", "organization_id", s.inv.OrganizationID,
		"investigation_id", s.inv.ID, "code", "AI_"+strings.ToUpper(string(code)))
	return false, s.finish(ctx, Ending{Reason: inv.AIFailed})
}

// retryable ends the attempt as a retryable failure, or, on the job's last
// attempt, ends the investigation with ai_failed itself (so the end has its
// worker event).
func (s *session) retryable(ctx context.Context, code, message string) error {
	if s.job.MaxAttempts > 0 && s.job.Attempt >= s.job.MaxAttempts {
		return s.finish(ctx, Ending{Reason: inv.AIFailed})
	}
	return failure(code, message, true)
}

// end ends the attempt after err, an *agent.End or the error of a ledger
// access.
func (s *session) end(ctx context.Context, err error) error {
	var e *agent.End
	if errors.As(err, &e) {
		r := inv.Reason(e.Reason)
		if s.ended != nil && *s.ended == r {
			return s.finish(ctx, Ending{Reason: r, Already: true})
		}
		return s.finish(ctx, Ending{Reason: r})
	}
	return err
}

// finishWith ends the investigation with the planner's finish call.
func (s *session) finishWith(ctx context.Context, res provider.PlanResult) error {
	var a reg.FinishArguments
	if err := json.Unmarshal(res.Call.Input, &a); err != nil {
		return s.finish(ctx, Ending{Reason: inv.AIFailed})
	}
	outcome := string(a.Outcome)
	hash := hashing.Sum(res.Call.Input)
	call := &inv.NewCall{Seq: len(s.calls) + 1, Tool: string(reg.Finish), Arguments: []byte(res.Call.Input), ArgumentsHash: &hash,
		Note: noteOf(res.Note), Status: inv.Done}
	return s.finish(ctx, Ending{Reason: inv.FinishedByAI, Finish: call, Outcome: &outcome})
}

// finish writes the end (Ledger.Finish) with its counts. When the
// investigation has ended meanwhile (the database refuses the end or the
// finish call), its own end is recorded instead.
func (s *session) finish(ctx context.Context, e Ending) error {
	e.Steps, e.PlannerTurns = len(s.calls), s.turns
	if e.Finish != nil {
		e.Steps++
	}
	err := s.l.Finish(ctx, e)
	if err == nil || e.Already || ctx.Err() != nil {
		return err
	}
	p, perr := s.l.Poll(ctx, 0)
	if perr != nil || p.Status == inv.Running || p.Reason == nil {
		return err
	}
	return s.l.Finish(ctx, Ending{Reason: *p.Reason, Already: true, Steps: len(s.calls), PlannerTurns: s.turns})
}

func noteOf(n string) *string {
	if n == "" {
		return nil
	}
	return &n
}

func hashOf(b []byte) string { return hashing.Sum(b) }

// capsOf are the investigation's caps as a budget.
func capsOf(i inv.Investigation) reg.Budget {
	return reg.Budget{WhatIfsLeft: i.Caps.WhatIfs, RunsLeft: i.Caps.Runs, ExplanationsLeft: i.Caps.Explanations, StepsLeft: i.Caps.Steps}
}

// budget is what the investigation may still do, counted from its ledger
// (H-061 §7, §12): each done propose call one what-if, each done run call
// one run and the investigation's own baseline one more, each done
// explanation one, every recorded call a step.
func (s *session) budget() reg.Budget {
	b := capsOf(s.inv)
	baseline := false
	for _, c := range s.calls {
		b.StepsLeft--
		if c.Status != inv.Done {
			continue
		}
		switch reg.Tool(c.Tool) {
		case reg.ProposeWhatIf, reg.ProposeModelEdit:
			b.WhatIfsLeft--
		case reg.RunWhatIf:
			b.RunsLeft--
			if !baseline && s.inv.BaselineRunID == nil {
				b.RunsLeft--
				baseline = true
			}
		case reg.ExplainResults:
			b.ExplanationsLeft--
		}
	}
	b.WhatIfsLeft, b.RunsLeft, b.ExplanationsLeft, b.StepsLeft = max(b.WhatIfsLeft, 0), max(b.RunsLeft, 0), max(b.ExplanationsLeft, 0),
		max(b.StepsLeft, 0)
	return b
}

// index is the place of the proposal the done propose call seq made: one
// more than the done propose calls before it (the database assigns the
// index in that order).
func (s *session) index(seq int) int {
	n := 0
	for _, c := range s.calls {
		if c.Seq <= seq && c.Status == inv.Done && (reg.Tool(c.Tool) == reg.ProposeWhatIf || reg.Tool(c.Tool) == reg.ProposeModelEdit) {
			n++
		}
	}
	return max(n, 1)
}

// objective is the scenario investigation's objective metric path.
func (s *session) objective() string {
	if s.brief.Objective != nil {
		return s.brief.Objective.MetricPath
	}
	return ""
}
