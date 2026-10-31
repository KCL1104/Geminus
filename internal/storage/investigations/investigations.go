// Package investigations is the SQL of twin.investigations and its
// tool-call ledger, twin.investigation_tool_calls (H-061 §5, §6, §11;
// migration 0019). Every statement runs in a scoped transaction and also
// filters by organization (AGENTS.md §2); RLS confines it as well.
//
// The API's statements (internal/investigations, its executor):
//   - Insert, in the requester's own createInvestigation transaction, with
//     the investigation's one job (jobs.Enqueue, type investigation,
//     MaxAttempts) and its audit event; the database refuses a second
//     running investigation of the person (ErrRunning) and the 11th of the
//     organization's UTC day (ErrDailyLimit);
//   - Get, Share, Lock, List, RunningOf and Calls, the reads;
//   - Stop (a person: the requester or an owner) and EndStale (the time
//     limit, once no worker runs it);
//   - ClaimCall (FOR UPDATE SKIP LOCKED: another executor pass skips it) and
//     Resolve, in the investigation's acting-for transaction (tenant.Scope
//     ActingFor), the call's one result.
//
// The worker's statements (internal/investigator, under the job's lease):
// Snapshot, InsertCall, Observe and End.
//
// What each may write is the database's to refuse (protect_investigation,
// protect_investigation_tool_call); a refusal is returned as the database's
// error, never retried here. Rows hold IDs, codes and the server's own JSON;
// the person's intent and the AI's idea and note are never logged.
package investigations

import (
	"context"
	"encoding/json/v2"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
)

// Mode is what the investigation tries.
type Mode string

const (
	Scenario Mode = "scenario" // what-ifs, saved and run against a baseline
	Edit     Mode = "edit"     // one change, written into a clean draft
)

// Source is where it was asked for.
type Source string

const (
	FromRequest  Source = "request"  // the model page (entry A)
	FromAnalysis Source = "analysis" // a finished analysis (entry B)
)

// Status is an investigation's state.
type Status string

const (
	Running  Status = "running"
	Finished Status = "finished"
	Stopped  Status = "stopped"
)

// Reason is why an investigation ended (common.schema.json InvestigationReason).
type Reason string

const (
	FinishedByAI    Reason = "finished_by_ai"
	DraftWritten    Reason = "draft_written"
	LimitReached    Reason = "limit_reached"
	TimeLimit       Reason = "time_limit"
	SpendCap        Reason = "spend_cap"
	InvalidAnswer   Reason = "invalid_answer"
	AIFailed        Reason = "ai_failed"
	RoleLost        Reason = "role_lost"
	StoppedByPerson Reason = "stopped_by_person"
)

// Reasons are the nine, in the schema's order.
var Reasons = []Reason{FinishedByAI, DraftWritten, LimitReached, TimeLimit, SpendCap, InvalidAnswer, AIFailed, RoleLost, StoppedByPerson}

// StatusOf is the status an end with reason r leaves.
func StatusOf(r Reason) Status {
	switch r {
	case FinishedByAI, DraftWritten, LimitReached:
		return Finished
	}
	return Stopped
}

// MaxAttempts is the investigation job's attempts (H-061 §5.1): a deploy
// (WORKER_STOPPED) uses one, and a 20-minute investigation may see several.
const MaxAttempts = 5

// Minutes is an investigation's wall time from its creation, queue time
// included (H-060); the database holds deadline_at to it.
const Minutes = 20

// Caps are H-060's caps, frozen at creation (investigation.schema.json
// InvestigationCaps); the database holds them to CapsFor.
type Caps struct {
	WhatIfs      int `json:"what_ifs"`
	Runs         int `json:"runs"`
	Explanations int `json:"explanations"`
	Steps        int `json:"steps"`
	Minutes      int `json:"minutes"`
}

// CapsFor are the caps of an investigation in mode m: 3 what-ifs, 4 runs
// (3 when a person's baseline is reused), 4 explanations; edit mode one
// proposal and nothing to run; 14 steps and 20 minutes for both.
func CapsFor(m Mode, reusedBaseline bool) Caps {
	switch {
	case m == Edit:
		return Caps{WhatIfs: 1, Runs: 0, Explanations: 0, Steps: 14, Minutes: Minutes}
	case reusedBaseline:
		return Caps{WhatIfs: 3, Runs: 3, Explanations: 4, Steps: 14, Minutes: Minutes}
	}
	return Caps{WhatIfs: 3, Runs: 4, Explanations: 4, Steps: 14, Minutes: Minutes}
}

// Investigation is a row of twin.investigations: the stored acting-for
// scope (H-061 §8) and its end.
type Investigation struct {
	ID, OrganizationID, ModelID, BaseVersionID string
	Mode                                       Mode
	Source                                     Source
	SourceAnalysisID                           *string
	// Intent is the person's words (untrusted); nil from an analysis.
	Intent *string
	// Context is the request's context (JSON object; {} when none).
	Context      []byte
	OutputLocale string
	Research     bool
	// ProposalModel is the person's proposal model (H-049); PlannerModel
	// the model that picks the calls (H-060).
	ProposalModel, PlannerModel, PromptTemplateVersion string
	// Objective and RunConfig are the server's JSON (nil in edit mode);
	// RunConfigHash is the runs' config hash (hashing.Config).
	Objective, RunConfig []byte
	RunConfigHash        *string
	BaselineName         *string
	BaselineRunID        *string
	Caps                 Caps
	Status               Status
	Reason               *Reason
	AIOutcome            *string
	StopRequestedBy      *string
	DeadlineAt           time.Time
	CreatedBy            string
	CreatedAt            time.Time
	FinishedAt           *time.Time
	// JobID is its one investigation job ("" before Insert's caller
	// enqueued it).
	JobID string
}

// CallStatus is a ledger row's state.
type CallStatus string

const (
	Pending CallStatus = "pending"
	Done    CallStatus = "done"
	Refused CallStatus = "refused"
	Failed  CallStatus = "failed"
)

// InvalidCall is the tool of a step whose model output named no offered
// tool or did not decode (investigator.InvalidCall).
const InvalidCall = "invalid_call"

// Effect is what a carried-out call made (references only).
type Effect struct {
	ProposalID, ScenarioID, BaselineRunID, RunID, AnalysisID *string
	DraftRevision                                            *int64
}

// ToolError is why a call was refused or failed: an InvestigationToolCode
// and a JSON Pointer into the call's arguments.
type ToolError struct {
	Code string `json:"code"`
	Path string `json:"path"`
}

// Call is a row of the ledger.
type Call struct {
	OrganizationID, InvestigationID string
	Seq                             int
	Tool                            string
	// Arguments is the canonical form of the decoded arguments
	// (investigator.Call.Canonical); nil for an invalid call.
	Arguments     []byte
	ArgumentsHash *string
	// Note is the AI's note (sanitized AI text, shown labelled).
	Note        *string
	JobID       string
	LeaseToken  int64
	Status      CallStatus
	Effect      Effect
	Error       *ToolError
	Observation []byte
	// ObservationHash is the hash of the canonical observation.
	ObservationHash        *string
	RequestedAt            time.Time
	ResolvedAt, ObservedAt *time.Time
}

var (
	// ErrNotFound: no such investigation in the organization (or no access).
	ErrNotFound = errors.New("investigations: not found")
	// ErrRunning: the person already has a running investigation in the
	// organization (409 LIFECYCLE_CONFLICT).
	ErrRunning = errors.New("investigations: the person already has a running investigation")
	// ErrDailyLimit: the organization started 10 investigations this UTC day
	// (429 RATE_LIMITED).
	ErrDailyLimit = errors.New("investigations: the organization's investigations of the day are used up")
	// ErrEnded: the investigation is no longer running; nothing was written.
	ErrEnded = errors.New("investigations: the investigation has ended")
	// ErrResolved: the call was already carried out (or is not pending);
	// nothing was written.
	ErrResolved = errors.New("investigations: the call is not pending")
	// ErrObserved: what the AI was shown is already recorded, or the call is
	// not carried out yet; nothing was written.
	ErrObserved = errors.New("investigations: the call is observed already or not carried out")
)

func nullable(b []byte) *string {
	if b == nil {
		return nil
	}
	s := string(b)
	return &s
}

// Insert stores the investigation, running, created now with its deadline
// 20 minutes on (both the database's time), in the requester's own
// transaction; the caller enqueues its job and writes its audit event in
// the same transaction (the database refuses one without its job at
// commit). The person's second running investigation is ErrRunning, the
// organization's 11th of the UTC day ErrDailyLimit.
func Insert(ctx context.Context, tx pgx.Tx, inv Investigation) (Investigation, error) {
	caps, err := json.Marshal(inv.Caps, json.Deterministic(true))
	if err != nil {
		return Investigation{}, fmt.Errorf("encoding the caps: %w", err)
	}
	if inv.Context == nil {
		inv.Context = []byte("{}")
	}
	err = tx.QueryRow(ctx, `INSERT INTO twin.investigations(id,organization_id,model_id,base_version_id,mode,source,source_analysis_id,intent,
		  context,output_locale,research,proposal_model,planner_model,prompt_template_version,objective,run_config,run_config_hash,baseline_name,
		  baseline_run_id,caps,status,deadline_at,created_by)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15,$16,$17,$18,$19,$20,'running',now()+$21::bigint*interval '1 minute',$22)
		RETURNING created_at, deadline_at`,
		inv.ID, inv.OrganizationID, inv.ModelID, inv.BaseVersionID, string(inv.Mode), string(inv.Source), inv.SourceAnalysisID, inv.Intent,
		string(inv.Context), inv.OutputLocale, inv.Research, inv.ProposalModel, inv.PlannerModel, inv.PromptTemplateVersion,
		nullable(inv.Objective), nullable(inv.RunConfig), inv.RunConfigHash, inv.BaselineName, inv.BaselineRunID, string(caps), Minutes,
		inv.CreatedBy).Scan(&inv.CreatedAt, &inv.DeadlineAt)
	if err != nil {
		var pg *pgconn.PgError
		if errors.As(err, &pg) {
			switch pg.ConstraintName {
			case "investigations_one_running":
				return Investigation{}, ErrRunning
			case "investigations_daily_limit":
				return Investigation{}, ErrDailyLimit
			}
		}
		return Investigation{}, err
	}
	inv.Status, inv.Reason, inv.AIOutcome, inv.StopRequestedBy, inv.FinishedAt = Running, nil, nil, nil, nil
	return inv, nil
}

const columns = `i.id, i.model_id, i.base_version_id, i.mode, i.source, i.source_analysis_id, i.intent, i.context, i.output_locale, i.research,
	i.proposal_model, i.planner_model, i.prompt_template_version, i.objective, i.run_config, i.run_config_hash, i.baseline_name,
	i.baseline_run_id, i.caps, i.status, i.reason, i.ai_outcome, i.stop_requested_by, i.deadline_at, i.created_by, i.created_at,
	i.finished_at, coalesce(j.id, '')`

const from = ` FROM twin.investigations i
	LEFT JOIN twin.jobs j ON j.organization_id=i.organization_id AND j.investigation_id=i.id`

func scan(row pgx.Row, org string) (Investigation, error) {
	inv := Investigation{OrganizationID: org}
	var mode, source, status string
	var reason *string
	var caps []byte
	err := row.Scan(&inv.ID, &inv.ModelID, &inv.BaseVersionID, &mode, &source, &inv.SourceAnalysisID, &inv.Intent, &inv.Context,
		&inv.OutputLocale, &inv.Research, &inv.ProposalModel, &inv.PlannerModel, &inv.PromptTemplateVersion, &inv.Objective, &inv.RunConfig,
		&inv.RunConfigHash, &inv.BaselineName, &inv.BaselineRunID, &caps, &status, &reason, &inv.AIOutcome, &inv.StopRequestedBy,
		&inv.DeadlineAt, &inv.CreatedBy, &inv.CreatedAt, &inv.FinishedAt, &inv.JobID)
	if errors.Is(err, pgx.ErrNoRows) {
		return Investigation{}, ErrNotFound
	}
	if err != nil {
		return Investigation{}, err
	}
	if err := json.Unmarshal(caps, &inv.Caps, json.RejectUnknownMembers(true)); err != nil {
		return Investigation{}, fmt.Errorf("investigation %s: stored caps: %w", inv.ID, err)
	}
	inv.Mode, inv.Source, inv.Status = Mode(mode), Source(source), Status(status)
	if reason != nil {
		r := Reason(*reason)
		inv.Reason = &r
	}
	return inv, nil
}

// Get returns the investigation id of organization org.
func Get(ctx context.Context, tx pgx.Tx, org, id string) (Investigation, error) {
	return scan(tx.QueryRow(ctx, `SELECT `+columns+from+` WHERE i.organization_id=$1 AND i.id=$2`, org, id), org)
}

// Share returns the investigation and holds it FOR SHARE until the
// transaction ends: the executor's lock after the model and the draft
// (H-061 §6.3), so a stop waits for the call and then refuses the next.
func Share(ctx context.Context, tx pgx.Tx, org, id string) (Investigation, error) {
	return scan(tx.QueryRow(ctx, `SELECT `+columns+from+` WHERE i.organization_id=$1 AND i.id=$2 FOR SHARE OF i`, org, id), org)
}

// Lock returns the investigation and holds it FOR UPDATE until the
// transaction ends (the stop, after the agent job's SKIP LOCKED cancel).
func Lock(ctx context.Context, tx pgx.Tx, org, id string) (Investigation, error) {
	return scan(tx.QueryRow(ctx, `SELECT `+columns+from+` WHERE i.organization_id=$1 AND i.id=$2 FOR UPDATE OF i`, org, id), org)
}

// RunningOf returns user's running investigation in the organization; ok
// is false when there is none (one at most, the database's index).
func RunningOf(ctx context.Context, tx pgx.Tx, org, user string) (inv Investigation, ok bool, err error) {
	inv, err = scan(tx.QueryRow(ctx, `SELECT `+columns+from+` WHERE i.organization_id=$1 AND i.created_by=$2 AND i.status='running'`,
		org, user), org)
	if errors.Is(err, ErrNotFound) {
		return Investigation{}, false, nil
	}
	return inv, err == nil, err
}

// Position is a place in the (created_at, id) order of a model's
// investigations.
type Position struct {
	CreatedAt time.Time
	ID        string
}

// List returns up to limit investigations of the model in (created_at, id)
// order, after the position if one is given.
func List(ctx context.Context, tx pgx.Tx, org, modelID string, after *Position, limit int) ([]Investigation, error) {
	var rows pgx.Rows
	var err error
	if after == nil {
		rows, err = tx.Query(ctx, `SELECT `+columns+from+` WHERE i.organization_id=$1 AND i.model_id=$2
			ORDER BY i.created_at, i.id LIMIT $3`, org, modelID, limit)
	} else {
		rows, err = tx.Query(ctx, `SELECT `+columns+from+` WHERE i.organization_id=$1 AND i.model_id=$2
			AND (i.created_at, i.id) > ($3, $4) ORDER BY i.created_at, i.id LIMIT $5`, org, modelID, after.CreatedAt, after.ID, limit)
	}
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Investigation
	for rows.Next() {
		inv, err := scan(rows, org)
		if err != nil {
			return nil, err
		}
		out = append(out, inv)
	}
	return out, rows.Err()
}

// Stop ends the running investigation as stopped by person by (the
// requester or an owner, the scoped user; the database refuses anyone
// else), now. An ended investigation is ErrEnded.
func Stop(ctx context.Context, tx pgx.Tx, org, id, by string) error {
	return ended(tx.Exec(ctx, `UPDATE twin.investigations SET status='stopped', reason='stopped_by_person', stop_requested_by=$3
		WHERE organization_id=$1 AND id=$2 AND status='running'`, org, id, by))
}

// EndStale ends the running investigation at its time limit (the API, 2
// minutes after its deadline, while no worker runs its job: the database
// refuses it otherwise); it reports whether it did.
func EndStale(ctx context.Context, tx pgx.Tx, org, id string) (bool, error) {
	tag, err := tx.Exec(ctx, `UPDATE twin.investigations SET status='stopped', reason='time_limit'
		WHERE organization_id=$1 AND id=$2 AND status='running' AND deadline_at + interval '2 minutes' < now()
		  AND NOT EXISTS (SELECT 1 FROM twin.jobs j WHERE j.organization_id=$1 AND j.investigation_id=$2 AND j.status='running')`, org, id)
	if err != nil {
		return false, err
	}
	return tag.RowsAffected() == 1, nil
}

// End ends the worker's own running investigation with reason (and, for
// finished_by_ai, the AI's claimed outcome), in the transaction that ends
// its job, after the job (the database refuses it otherwise). An ended
// investigation is ErrEnded.
func End(ctx context.Context, tx pgx.Tx, org, id string, reason Reason, outcome *string) error {
	return ended(tx.Exec(ctx, `UPDATE twin.investigations SET status=$3, reason=$4, ai_outcome=$5
		WHERE organization_id=$1 AND id=$2 AND status='running'`, org, id, string(StatusOf(reason)), string(reason), outcome))
}

func ended(tag pgconn.CommandTag, err error) error {
	if err != nil {
		return err
	}
	if tag.RowsAffected() != 1 {
		return ErrEnded
	}
	return nil
}

const callColumns = `c.investigation_id, c.seq, c.tool, c.arguments, c.arguments_hash, c.note, c.job_id, c.lease_token, c.status,
	c.proposal_id, c.scenario_id, c.baseline_run_id, c.run_id, c.analysis_id, c.draft_revision, c.error, c.observation,
	c.observation_hash, c.requested_at, c.resolved_at, c.observed_at`

func scanCall(row pgx.Row, org string) (Call, error) {
	c := Call{OrganizationID: org}
	var status string
	var toolError []byte
	err := row.Scan(&c.InvestigationID, &c.Seq, &c.Tool, &c.Arguments, &c.ArgumentsHash, &c.Note, &c.JobID, &c.LeaseToken, &status,
		&c.Effect.ProposalID, &c.Effect.ScenarioID, &c.Effect.BaselineRunID, &c.Effect.RunID, &c.Effect.AnalysisID, &c.Effect.DraftRevision,
		&toolError, &c.Observation, &c.ObservationHash, &c.RequestedAt, &c.ResolvedAt, &c.ObservedAt)
	if err != nil {
		return Call{}, err
	}
	c.Status = CallStatus(status)
	if toolError != nil {
		c.Error = &ToolError{}
		if err := json.Unmarshal(toolError, c.Error, json.RejectUnknownMembers(true)); err != nil {
			return Call{}, fmt.Errorf("call %s/%d: stored error: %w", c.InvestigationID, c.Seq, err)
		}
	}
	return c, nil
}

// Calls returns the investigation's ledger in seq order.
func Calls(ctx context.Context, tx pgx.Tx, org, id string) ([]Call, error) {
	rows, err := tx.Query(ctx, `SELECT `+callColumns+` FROM twin.investigation_tool_calls c
		WHERE c.organization_id=$1 AND c.investigation_id=$2 ORDER BY c.seq`, org, id)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Call
	for rows.Next() {
		c, err := scanCall(rows, org)
		if err != nil {
			return nil, err
		}
		out = append(out, c)
	}
	return out, rows.Err()
}

// ClaimCall locks the pending call (id, seq) FOR UPDATE SKIP LOCKED, the
// executor's second lock after the requester's membership (H-061 §6.3): ok
// is false when it is not pending any more or another transaction holds
// it, and nothing is written.
func ClaimCall(ctx context.Context, tx pgx.Tx, org, id string, seq int) (c Call, ok bool, err error) {
	c, err = scanCall(tx.QueryRow(ctx, `SELECT `+callColumns+` FROM twin.investigation_tool_calls c
		WHERE c.organization_id=$1 AND c.investigation_id=$2 AND c.seq=$3 AND c.status='pending'
		FOR UPDATE SKIP LOCKED`, org, id, seq), org)
	if errors.Is(err, pgx.ErrNoRows) {
		return Call{}, false, nil
	}
	return c, err == nil, err
}

// Resolution is a call's one result: done with what it made, or refused or
// failed with its error.
type Resolution struct {
	Status CallStatus
	Effect Effect
	Error  *ToolError
}

// Resolve records the call's result, in the investigation's acting-for
// transaction for its requester (the database refuses any other) while it
// runs, the effect and its audit events in the same transaction. A call
// that is not pending is ErrResolved.
func Resolve(ctx context.Context, tx pgx.Tx, org, id string, seq int, r Resolution) error {
	if r.Status == Pending || (r.Status == Done) != (r.Error == nil) {
		return fmt.Errorf("investigations: a call is resolved done with its effect, or refused or failed with its error")
	}
	var toolError *string
	if r.Error != nil {
		b, err := json.Marshal(r.Error, json.Deterministic(true))
		if err != nil {
			return err
		}
		toolError = nullable(b)
	}
	e := r.Effect
	tag, err := tx.Exec(ctx, `UPDATE twin.investigation_tool_calls SET status=$4, proposal_id=$5, scenario_id=$6, baseline_run_id=$7,
		  run_id=$8, analysis_id=$9, draft_revision=$10, error=$11, resolved_at=now()
		WHERE organization_id=$1 AND investigation_id=$2 AND seq=$3 AND status='pending'`,
		org, id, seq, string(r.Status), e.ProposalID, e.ScenarioID, e.BaselineRunID, e.RunID, e.AnalysisID, e.DraftRevision, toolError)
	if err != nil {
		return err
	}
	if tag.RowsAffected() != 1 {
		return ErrResolved
	}
	return nil
}

// Snapshot is what one worker Checkpoint reads (H-061 §5.2): the
// investigation, its ledger and the database's now().
func Snapshot(ctx context.Context, tx pgx.Tx, org, id string) (inv Investigation, calls []Call, now time.Time, err error) {
	if inv, err = Get(ctx, tx, org, id); err != nil {
		return Investigation{}, nil, time.Time{}, err
	}
	if calls, err = Calls(ctx, tx, org, id); err != nil {
		return Investigation{}, nil, time.Time{}, err
	}
	if err = tx.QueryRow(ctx, `SELECT now()`).Scan(&now); err != nil {
		return Investigation{}, nil, time.Time{}, err
	}
	return inv, calls, now, nil
}

// NewCall is a call the worker records, under the investigation job's
// current lease (JobID, LeaseToken): pending, except invalid model output
// (InvalidCall, refused at once with its error) and finish (done, the
// investigation's last).
type NewCall struct {
	OrganizationID, InvestigationID string
	Seq                             int
	Tool                            string
	Arguments                       []byte
	ArgumentsHash                   *string
	Note                            *string
	JobID                           string
	LeaseToken                      int64
	Status                          CallStatus
	Error                           *ToolError
}

// InsertCall records the call (the worker). The database refuses one out
// of sequence, under a stale lease, after the deadline or the end, or of
// another mode's tool.
func InsertCall(ctx context.Context, tx pgx.Tx, n NewCall) (Call, error) {
	var toolError *string
	if n.Error != nil {
		b, err := json.Marshal(n.Error, json.Deterministic(true))
		if err != nil {
			return Call{}, err
		}
		toolError = nullable(b)
	}
	return scanCall(tx.QueryRow(ctx, `INSERT INTO twin.investigation_tool_calls(organization_id,investigation_id,seq,tool,arguments,
		  arguments_hash,note,job_id,lease_token,status,error)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11)
		RETURNING `+callColumnsUnqualified, n.OrganizationID, n.InvestigationID, n.Seq, n.Tool, nullable(n.Arguments), n.ArgumentsHash, n.Note,
		n.JobID, n.LeaseToken, string(n.Status), toolError), n.OrganizationID)
}

const callColumnsUnqualified = `investigation_id, seq, tool, arguments, arguments_hash, note, job_id, lease_token, status,
	proposal_id, scenario_id, baseline_run_id, run_id, analysis_id, draft_revision, error, observation,
	observation_hash, requested_at, resolved_at, observed_at`

// Observe records what the AI was shown for the carried-out call (the
// worker, once, under the job's lease). A call already observed, or not
// carried out yet, is ErrObserved.
func Observe(ctx context.Context, tx pgx.Tx, org, id string, seq int, observation []byte, hash string) error {
	tag, err := tx.Exec(ctx, `UPDATE twin.investigation_tool_calls SET observation=$4, observation_hash=$5, observed_at=now()
		WHERE organization_id=$1 AND investigation_id=$2 AND seq=$3 AND status<>'pending' AND observed_at IS NULL`,
		org, id, seq, string(observation), hash)
	if err != nil {
		return err
	}
	if tag.RowsAffected() != 1 {
		return ErrObserved
	}
	return nil
}

// PlannerTurns is how many planner turns the investigation's spend ledger
// holds (its investigator reservations, open or settled): the worker's count
// of the 20 (H-061 §12) across attempts.
func PlannerTurns(ctx context.Context, tx pgx.Tx, org, id string) (int, error) {
	var n int
	err := tx.QueryRow(ctx, `SELECT count(*) FROM twin.ai_spend_reservations r
		WHERE r.organization_id=$1 AND r.investigation_id=$2 AND r.role='investigator'`, org, id).Scan(&n)
	return n, err
}

// Labelled is a kind of row an investigation labels whose AI call a job
// makes: an AI proposal or an analysis.
type Labelled string

const (
	LabelledProposal Labelled = "ai_proposal"
	LabelledAnalysis Labelled = "ai_analysis"
)

// LabelOf reports whether the row id of kind k carries an investigation's
// label, and whether that investigation still runs before its deadline (the
// generator and the analyzer fail fast, before the paid call, once it does
// not: H-061 §12). ErrNotFound when there is no such row.
func LabelOf(ctx context.Context, tx pgx.Tx, org string, k Labelled, id string) (labelled, running bool, err error) {
	var table string
	switch k {
	case LabelledProposal:
		table = "twin.ai_proposals"
	case LabelledAnalysis:
		table = "twin.ai_analyses"
	default:
		return false, false, fmt.Errorf("investigations: %q is not a labelled kind", k)
	}
	err = tx.QueryRow(ctx, `SELECT r.investigation_id IS NOT NULL, coalesce(i.status = 'running' AND now() < i.deadline_at, false)
		FROM `+table+` r LEFT JOIN twin.investigations i ON i.organization_id = r.organization_id AND i.id = r.investigation_id
		WHERE r.organization_id=$1 AND r.id=$2`, org, id).Scan(&labelled, &running)
	if errors.Is(err, pgx.ErrNoRows) {
		return false, false, ErrNotFound
	}
	return labelled, running, err
}
