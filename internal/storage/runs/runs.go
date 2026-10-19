// Package runs is the SQL of twin.simulation_runs, twin.run_datasets and
// twin.simulation_results (T-023, API_DATABASE §6–§8). Every statement runs
// in a scoped transaction (tenant.DB.Run for the API, the worker's lease
// fenced Checkpoint and Finish) and also filters by organization.
//
// A run's inputs are frozen at insert; the database refuses to change them
// (0001) and moves the run only along its lifecycle (0009): the API inserts
// it queued and may request its cancellation, the worker starts it, writes
// its progress and event budget and completes it with its result, and every
// other move follows its job.
package runs

import (
	"context"
	"encoding/json/v2"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/kcl1104/geminus/internal/model"
	"github.com/kcl1104/geminus/internal/simulation"
)

// Status is a run's state.
type Status string

const (
	Queued    Status = "queued"
	Running   Status = "running"
	Completed Status = "completed"
	Failed    Status = "failed"
	Cancelled Status = "cancelled"
)

// Final reports whether s never changes again.
func (s Status) Final() bool { return s == Completed || s == Failed || s == Cancelled }

// Run is one row of twin.simulation_runs.
type Run struct {
	ID, OrganizationID, ModelID, ModelVersionID string
	ScenarioID                                  *string
	Name                                        string
	ScenarioPatch                               []byte // a JSON Patch array
	Config                                      simulation.RunConfig
	EngineVersion                               string
	ModelHash, EffectiveModelHash               string
	ConfigHash, DatasetManifestHash             string
	DatasetBindings                             []model.DatasetBinding // byte order of logical_ref (D-034)
	Limits                                      simulation.ExecutionLimits
	EventBudgetReserved                         int64
	ExecutionDeadlineAt                         *time.Time
	Status                                      Status
	CompletedReplications                       int
	EventsProcessed                             int64
	ProgressRevision                            int64
	CancellationRequested                       bool
	// Failure is the failure its job recorded when the run failed (the
	// jobs.Failure JSON the database copied, 0009); nil otherwise.
	Failure               []byte
	CreatedBy             string
	CreatedAt             time.Time
	StartedAt, FinishedAt *time.Time
	RequestID             string // the request that created it (its job's payload)
}

// ErrNotFound: no such run in the organization.
var ErrNotFound = errors.New("runs: not found")

// Insert stores r queued, with its dataset bindings. The database checks at
// commit that the bindings are exactly r.DatasetBindings.
func Insert(ctx context.Context, tx pgx.Tx, r Run) (Run, error) {
	bindings := r.DatasetBindings
	if bindings == nil {
		bindings = []model.DatasetBinding{}
	}
	manifest, err := json.Marshal(bindings)
	if err != nil {
		return Run{}, fmt.Errorf("encoding the manifest: %w", err)
	}
	config, err := json.Marshal(r.Config)
	if err != nil {
		return Run{}, fmt.Errorf("encoding the config: %w", err)
	}
	limits, err := json.Marshal(r.Limits)
	if err != nil {
		return Run{}, fmt.Errorf("encoding the limits: %w", err)
	}
	err = tx.QueryRow(ctx, `INSERT INTO twin.simulation_runs(id,organization_id,model_id,model_version_id,scenario_id,name,scenario_patch,
		  config,seed,replications,engine_version,model_hash,effective_model_hash,config_hash,dataset_manifest_hash,dataset_bindings,
		  execution_limits,status,created_by)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15,$16,$17,'queued',$18) RETURNING created_at`,
		r.ID, r.OrganizationID, r.ModelID, r.ModelVersionID, r.ScenarioID, r.Name, string(r.ScenarioPatch), config, r.Config.Seed,
		r.Config.Replications, r.EngineVersion, r.ModelHash, r.EffectiveModelHash, r.ConfigHash, r.DatasetManifestHash, manifest, limits,
		r.CreatedBy).Scan(&r.CreatedAt)
	if err != nil {
		return Run{}, err
	}
	for _, b := range bindings {
		if _, err := tx.Exec(ctx, `INSERT INTO twin.run_datasets(organization_id,run_id,logical_ref,dataset_version_id,content_hash,unit,sample_count)
			VALUES ($1,$2,$3,$4,$5,$6,$7)`, r.OrganizationID, r.ID, b.LogicalRef, b.DatasetVersionID, b.ContentHash, string(b.Unit), b.SampleCount); err != nil {
			return Run{}, err
		}
	}
	r.DatasetBindings, r.Status = bindings, Queued
	return r, nil
}

const columns = `r.id, r.model_id, r.model_version_id, r.scenario_id, r.name, r.scenario_patch, r.config, r.engine_version, r.model_hash,
	r.effective_model_hash, r.config_hash, r.dataset_manifest_hash, r.dataset_bindings, r.execution_limits, r.event_budget_reserved,
	r.execution_deadline_at, r.status, r.completed_replications, r.events_processed, r.progress_revision, r.cancellation_requested,
	r.error, r.created_by, r.created_at, r.started_at, r.finished_at, coalesce(j.payload->>'request_id','')`

// from joins each run with its job, whose payload holds the request that
// created it (both immutable).
const from = ` FROM twin.simulation_runs r LEFT JOIN twin.jobs j ON j.organization_id=r.organization_id AND j.simulation_run_id=r.id`

func scan(row pgx.Row, org string) (Run, error) {
	r := Run{OrganizationID: org}
	var patch, config, manifest, limits []byte
	var status string
	err := row.Scan(&r.ID, &r.ModelID, &r.ModelVersionID, &r.ScenarioID, &r.Name, &patch, &config, &r.EngineVersion, &r.ModelHash,
		&r.EffectiveModelHash, &r.ConfigHash, &r.DatasetManifestHash, &manifest, &limits, &r.EventBudgetReserved, &r.ExecutionDeadlineAt,
		&status, &r.CompletedReplications, &r.EventsProcessed, &r.ProgressRevision, &r.CancellationRequested, &r.Failure, &r.CreatedBy,
		&r.CreatedAt, &r.StartedAt, &r.FinishedAt, &r.RequestID)
	if errors.Is(err, pgx.ErrNoRows) {
		return Run{}, ErrNotFound
	}
	if err != nil {
		return Run{}, err
	}
	r.Status, r.ScenarioPatch = Status(status), patch
	if err := json.Unmarshal(config, &r.Config); err != nil {
		return Run{}, fmt.Errorf("run %s: stored config: %w", r.ID, err)
	}
	if err := json.Unmarshal(manifest, &r.DatasetBindings); err != nil {
		return Run{}, fmt.Errorf("run %s: stored manifest: %w", r.ID, err)
	}
	if err := json.Unmarshal(limits, &r.Limits); err != nil {
		return Run{}, fmt.Errorf("run %s: stored limits: %w", r.ID, err)
	}
	return r, nil
}

// Get returns the run id of organization org.
func Get(ctx context.Context, tx pgx.Tx, org, id string) (Run, error) {
	return scan(tx.QueryRow(ctx, `SELECT `+columns+from+` WHERE r.organization_id=$1 AND r.id=$2`, org, id), org)
}

// Lock returns the run and holds its row until the transaction ends. A
// write that decides on a run's state locks its job first, then the run
// (LockJob, then Lock: the worker's order, ADR-024).
func Lock(ctx context.Context, tx pgx.Tx, org, id string) (Run, error) {
	return scan(tx.QueryRow(ctx, `SELECT `+columns+from+` WHERE r.organization_id=$1 AND r.id=$2 FOR UPDATE OF r`, org, id), org)
}

// JobOf is a run's job as a cancellation sees it.
type JobOf struct {
	ID     string
	Status string
}

// LockJob returns the job of run id and holds its row: a cancellation takes
// it before the run's, as the worker's finalizing transaction does.
func LockJob(ctx context.Context, tx pgx.Tx, org, id string) (JobOf, error) {
	var j JobOf
	err := tx.QueryRow(ctx, `SELECT id, status FROM twin.jobs WHERE organization_id=$1 AND simulation_run_id=$2 FOR UPDATE`, org, id).Scan(&j.ID, &j.Status)
	if errors.Is(err, pgx.ErrNoRows) {
		return JobOf{}, ErrNotFound
	}
	return j, err
}

// Position is a place in the (created_at, id) order of a model's runs.
type Position struct {
	CreatedAt time.Time
	ID        string
}

// List returns up to limit runs of the model in (created_at, id) order,
// after the position if one is given.
func List(ctx context.Context, tx pgx.Tx, org, modelID string, after *Position, limit int) ([]Run, error) {
	var rows pgx.Rows
	var err error
	if after == nil {
		rows, err = tx.Query(ctx, `SELECT `+columns+from+` WHERE r.organization_id=$1 AND r.model_id=$2
			ORDER BY r.created_at, r.id LIMIT $3`, org, modelID, limit)
	} else {
		rows, err = tx.Query(ctx, `SELECT `+columns+from+` WHERE r.organization_id=$1 AND r.model_id=$2
			AND (r.created_at, r.id) > ($3, $4) ORDER BY r.created_at, r.id LIMIT $5`, org, modelID, after.CreatedAt, after.ID, limit)
	}
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Run
	for rows.Next() {
		r, err := scan(rows, org)
		if err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

// ErrNotActive: the run is not queued or running any more (or, for the
// worker's writes, not running); nothing was written.
var ErrNotActive = errors.New("runs: the run is not active")

// RequestCancellation records that cancellation of a queued or running run
// was requested (the API, API_DATABASE §6): the worker stops at its next
// checkpoint. It reports whether the request is new.
func RequestCancellation(ctx context.Context, tx pgx.Tx, org, id string) (bool, error) {
	tag, err := tx.Exec(ctx, `UPDATE twin.simulation_runs SET cancellation_requested=true, progress_revision=progress_revision+1
		WHERE organization_id=$1 AND id=$2 AND status IN ('queued','running') AND NOT cancellation_requested`, org, id)
	if err != nil {
		return false, err
	}
	return tag.RowsAffected() == 1, nil
}

// Attempt is what a starting attempt reserved (Start).
type Attempt struct {
	// Base is the event budget reserved before this attempt: spent, or
	// granted to an attempt that ended without saying, so it counts as
	// spent (D-007).
	Base int64
	// Granted is this attempt's first grant, reserved with the start.
	Granted int64
	// Remaining is the wall time left of the logical run, measured by the
	// database when the attempt started.
	Remaining time.Duration
}

// Start marks the queued run running for a new attempt: the first start
// sets started_at and the execution deadline (started_at plus the wall
// timeout), which no retry resets, and reserves the attempt's first grant
// of events on top of what earlier attempts reserved.
func Start(ctx context.Context, tx pgx.Tx, org, id string, grant int64) (Attempt, error) {
	var a Attempt
	var remaining int64
	err := tx.QueryRow(ctx, `UPDATE twin.simulation_runs SET status='running',
		  started_at=coalesce(started_at, now()),
		  execution_deadline_at=coalesce(execution_deadline_at, now()+((execution_limits->>'wall_timeout_seconds')::bigint*interval '1 second')),
		  event_budget_reserved=event_budget_reserved+$3,
		  completed_replications=0, events_processed=0, progress_revision=progress_revision+1
		WHERE organization_id=$1 AND id=$2 AND status='queued'
		RETURNING event_budget_reserved-$3, $3::bigint, (extract(epoch FROM execution_deadline_at-now())*1000)::bigint`,
		org, id, grant).Scan(&a.Base, &a.Granted, &remaining)
	if errors.Is(err, pgx.ErrNoRows) {
		return Attempt{}, ErrNotActive
	}
	a.Remaining = time.Duration(remaining) * time.Millisecond
	return a, err
}

// Reserve raises the run's reserved event budget from was to total for the
// running attempt that reserved was (Credit). Any other value means another
// writer, and nothing is written.
func Reserve(ctx context.Context, tx pgx.Tx, org, id string, was, total int64) error {
	tag, err := tx.Exec(ctx, `UPDATE twin.simulation_runs SET event_budget_reserved=$4
		WHERE organization_id=$1 AND id=$2 AND status='running' AND event_budget_reserved=$3`, org, id, was, total)
	if err != nil {
		return err
	}
	if tag.RowsAffected() != 1 {
		return ErrNotActive
	}
	return nil
}

// Progress writes a running run's progress, advancing its revision when it
// changed, and reports whether cancellation was requested.
func Progress(ctx context.Context, tx pgx.Tx, org, id string, completed int, events int64) (cancel bool, err error) {
	err = tx.QueryRow(ctx, `UPDATE twin.simulation_runs SET completed_replications=$3, events_processed=$4,
		  progress_revision=progress_revision+CASE WHEN (completed_replications, events_processed)=($3::int, $4::bigint) THEN 0 ELSE 1 END
		WHERE organization_id=$1 AND id=$2 AND status='running' RETURNING cancellation_requested`, org, id, completed, events).Scan(&cancel)
	if errors.Is(err, pgx.ErrNoRows) {
		return false, ErrNotActive
	}
	return cancel, err
}

// Result is one row of twin.simulation_results.
type Result struct {
	RunID, OrganizationID string
	Result                []byte // the Result JSON
	Digest                string
	Trace                 *Trace
	CreatedAt             time.Time
}

// Trace names a run's sampled trace artifact in object storage (D-044).
type Trace struct {
	ArtifactID, ObjectKey, SHA256 string
	Size                          int64
}

// Complete stores res and marks its running run completed with it, all of
// its replications done. The database refuses a result that does not
// reproduce the run's frozen input, and one for a run whose cancellation was
// requested (0009).
func Complete(ctx context.Context, tx pgx.Tx, res Result, replications int, events int64) error {
	var art, key, hash *string
	var size *int64
	if t := res.Trace; t != nil {
		art, key, hash, size = &t.ArtifactID, &t.ObjectKey, &t.SHA256, &t.Size
	}
	if _, err := tx.Exec(ctx, `INSERT INTO twin.simulation_results(run_id,organization_id,result,result_digest,trace_artifact_id,trace_object_key,
		  trace_content_hash,trace_size_bytes) VALUES ($1,$2,$3,$4,$5,$6,$7,$8)`,
		res.RunID, res.OrganizationID, string(res.Result), res.Digest, art, key, hash, size); err != nil {
		return err
	}
	tag, err := tx.Exec(ctx, `UPDATE twin.simulation_runs SET status='completed', completed_replications=$3, events_processed=$4,
		  finished_at=now(), progress_revision=progress_revision+1
		WHERE organization_id=$1 AND id=$2 AND status='running'`, res.OrganizationID, res.RunID, replications, events)
	if err != nil {
		return err
	}
	if tag.RowsAffected() != 1 {
		return ErrNotActive
	}
	return nil
}

// GetResult returns the result of run of organization org.
func GetResult(ctx context.Context, tx pgx.Tx, org, run string) (Result, error) {
	res := Result{RunID: run, OrganizationID: org}
	var art, key, hash *string
	var size *int64
	err := tx.QueryRow(ctx, `SELECT result, result_digest, trace_artifact_id, trace_object_key, trace_content_hash, trace_size_bytes, created_at
		FROM twin.simulation_results WHERE organization_id=$1 AND run_id=$2`, org, run).Scan(&res.Result, &res.Digest, &art, &key, &hash, &size, &res.CreatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return Result{}, ErrNotFound
	}
	if err != nil {
		return Result{}, err
	}
	if art != nil && key != nil && hash != nil && size != nil {
		res.Trace = &Trace{ArtifactID: *art, ObjectKey: *key, SHA256: *hash, Size: *size}
	}
	return res, nil
}
