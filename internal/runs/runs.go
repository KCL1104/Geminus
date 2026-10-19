// Package runs is the simulation run service (T-023, API_DATABASE §6–§7,
// SIMULATION_SPEC §1, §8): it freezes a run's input and queues it,
// lists and reads runs, requests their cancellation, and serves their
// results, sampled traces and progress. Each operation is one
// tenant.DB.Run transaction that checks the caller's membership and role
// first (D-008); SQL lives in internal/storage. The job that executes a run
// is internal/runs/runner's.
//
// A run binds an immutable model version, never a draft (REQ-005), the
// scenario patch (inline, or a saved scenario's, copied), the run
// configuration, the version's whole dataset manifest, the engine version
// and the execution limits (D-007, server-set). Everything the engine will
// check is checked before the run is queued, by the same code: the patch
// policy and validator, the domain pack, the dataset binding stage and the
// engine's own Compile. The run, its job and its audit event commit
// together, and only then is 202 answered (D-011).
package runs

import (
	"context"
	"encoding/base64"
	"encoding/json/jsontext"
	"encoding/json/v2"
	"errors"
	"fmt"
	"io"
	"regexp"
	"slices"
	"strings"
	"time"

	"github.com/kcl1104/geminus/internal/domainpack"
	"github.com/kcl1104/geminus/internal/ids"
	"github.com/kcl1104/geminus/internal/model"
	"github.com/kcl1104/geminus/internal/model/hashing"
	"github.com/kcl1104/geminus/internal/model/patch"
	"github.com/kcl1104/geminus/internal/model/validate"
	"github.com/kcl1104/geminus/internal/simulation"
	"github.com/kcl1104/geminus/internal/simulation/des"
	"github.com/kcl1104/geminus/internal/simulation/engine"
	"github.com/kcl1104/geminus/internal/storage/auditlog"
	"github.com/kcl1104/geminus/internal/storage/datasets"
	"github.com/kcl1104/geminus/internal/storage/idempotency"
	"github.com/kcl1104/geminus/internal/storage/jobs"
	models "github.com/kcl1104/geminus/internal/storage/models"
	"github.com/kcl1104/geminus/internal/storage/objects"
	store "github.com/kcl1104/geminus/internal/storage/runs"
	"github.com/kcl1104/geminus/internal/storage/snapshots"
	"github.com/kcl1104/geminus/internal/tenant"
)

var (
	// ErrNotFound: no such run, model, version or scenario in the
	// organization (or no access); the same for all (TEST-009).
	ErrNotFound = errors.New("runs: not found")
	// ErrNotReady: the run has no result: it is not completed (409
	// RESULT_NOT_READY; API_DATABASE §6).
	ErrNotReady = errors.New("runs: the run has no result")
	// ErrFinished: the run completed or failed, so it cannot be cancelled
	// (409 LIFECYCLE_CONFLICT).
	ErrFinished = errors.New("runs: the run has finished")
	// ErrNoTrace: the completed run has no sampled trace (trace_level
	// none, or no sampled entity had an event).
	ErrNoTrace = errors.New("runs: the run has no sampled trace")
	// ErrStorageUnavailable: the run needs object storage (its version
	// binds datasets, or it records a sampled trace) and none is
	// configured (503; D-044).
	ErrStorageUnavailable = errors.New("runs: object storage is not configured")
	// ErrInvalidCursor: the cursor is not one this service issued for
	// this list.
	ErrInvalidCursor = errors.New("runs: invalid cursor")
	// ErrInvalidLimit: a page size outside 1–100.
	ErrInvalidLimit = errors.New("runs: invalid page size")
)

// RequestError is a request that is not acceptable as it stands (422
// REQUEST_VALIDATION_FAILED); Issues point into the request body.
type RequestError struct{ Issues []model.ValidationIssue }

func (e *RequestError) Error() string {
	return fmt.Sprintf("runs: invalid request (%d issue(s))", len(e.Issues))
}

// InvalidError is a run whose effective model is not acceptable (422
// MODEL_VALIDATION_FAILED): patch issues point into the scenario patch
// (/scenario_patch/...), model issues into the effective IR, dataset
// issues into the version's manifest (/dataset_bindings/...).
type InvalidError struct{ Issues []model.ValidationIssue }

func (e *InvalidError) Error() string {
	return fmt.Sprintf("runs: invalid run (%d issue(s))", len(e.Issues))
}

// PackLookup finds a model's domain pack; *domainpack.Registry is one.
type PackLookup interface {
	Lookup(domain string) (domainpack.DomainPack, error)
}

// DefaultLimits are D-007's initial execution limits (approved; benchmark
// pending, T-037). Every run freezes the service's limits; a client never
// sets or raises them.
var DefaultLimits = simulation.ExecutionLimits{
	WallTimeoutSeconds: 300, MaxEvents: 10_000_000, MaxActiveEntities: 100_000, MaxEntityVisits: 10_000, MaxTraceEvents: 100_000,
}

// Service runs simulation operations for a verified scope.
type Service struct {
	DB    *tenant.DB
	Packs PackLookup
	// Objects holds dataset snapshots and trace artifacts (D-044); nil when
	// object storage is disabled, so a run that needs it fails closed (503).
	Objects objects.Store
	// Limits are the execution limits every new run freezes; zero means
	// DefaultLimits. They must be within D-007's (the schema's) maxima.
	Limits simulation.ExecutionLimits
	// EngineVersion is the engine a new run is frozen for; "" means
	// des.Version. The worker refuses a run frozen for another engine.
	EngineVersion string
	// Policy is the queue's (jobs.DefaultPolicy when zero).
	Policy jobs.Policy
}

// The roles each operation requires (x-required-roles, planning/api-operations.json).
var (
	writeRoles = tenant.Writers
	readRoles  = tenant.Members
)

const (
	createOperation = "createSimulation"
	cancelOperation = "cancelSimulation"
	maxLimit        = 100
	maxIssues       = 1000
)

func (s *Service) limits() simulation.ExecutionLimits {
	if s.Limits == (simulation.ExecutionLimits{}) {
		return DefaultLimits
	}
	return s.Limits
}

func (s *Service) engineVersion() string {
	if s.EngineVersion == "" {
		return des.Version
	}
	return s.EngineVersion
}

func (s *Service) policy() jobs.Policy {
	if s.Policy == (jobs.Policy{}) {
		return jobs.DefaultPolicy
	}
	return s.Policy
}

// CreateRequest is simulation.schema.json#/$defs/RunRequest.
type CreateRequest struct {
	ModelVersionID string               `json:"model_version_id"`
	Name           string               `json:"name"`
	ScenarioPatch  jsontext.Value       `json:"scenario_patch,omitzero"`
	ScenarioID     string               `json:"scenario_id,omitzero"`
	Config         simulation.RunConfig `json:"config"`
}

// Accepted is http.schema.json#/$defs/RunAccepted.
type Accepted struct {
	ID             string `json:"id"`
	Status         string `json:"status"`
	ModelVersionID string `json:"model_version_id"`
	CreatedAt      string `json:"created_at"`
}

// Run is simulation.schema.json#/$defs/Run.
type Run struct {
	ID                    string                     `json:"id"`
	OrganizationID        string                     `json:"organization_id"`
	ModelID               string                     `json:"model_id"`
	ModelVersionID        string                     `json:"model_version_id"`
	Name                  string                     `json:"name"`
	ScenarioPatch         jsontext.Value             `json:"scenario_patch"`
	ScenarioID            *string                    `json:"scenario_id"`
	Config                simulation.RunConfig       `json:"config"`
	ConfigHash            string                     `json:"config_hash"`
	ModelHash             string                     `json:"model_hash"`
	EffectiveModelHash    string                     `json:"effective_model_hash"`
	EngineVersion         string                     `json:"engine_version"`
	DatasetBindings       []model.DatasetBinding     `json:"dataset_bindings"`
	ExecutionLimits       simulation.ExecutionLimits `json:"execution_limits"`
	Status                string                     `json:"status"`
	Progress              Progress                   `json:"progress"`
	CancellationRequested bool                       `json:"cancellation_requested"`
	CreatedAt             string                     `json:"created_at"`
	StartedAt             *string                    `json:"started_at"`
	FinishedAt            *string                    `json:"finished_at"`
	Error                 *ErrorEnvelope             `json:"error"`
}

// Progress is Run.progress.
type Progress struct {
	CompletedReplications int   `json:"completed_replications"`
	TotalReplications     int   `json:"total_replications"`
	EventsProcessed       int64 `json:"events_processed"`
	Revision              int64 `json:"revision"`
}

// Page is http.schema.json#/$defs/Runs.
type Page struct {
	Items      []Run   `json:"items"`
	NextCursor *string `json:"next_cursor"`
}

// Snapshot is http.schema.json#/$defs/ProgressSnapshot, an SSE event's data.
type Snapshot struct {
	RunID                 string `json:"run_id"`
	Status                string `json:"status"`
	Revision              int64  `json:"revision"`
	CompletedReplications int    `json:"completed_replications"`
	TotalReplications     int    `json:"total_replications"`
	EventsProcessed       int64  `json:"events_processed"`
	CancellationRequested bool   `json:"cancellation_requested"`
}

// Final reports whether the run behind the snapshot never changes again.
func (s Snapshot) Final() bool { return store.Status(s.Status).Final() }

// Create freezes req and queues the run, or replays the response of the
// earlier request with the same Idempotency-Key (same body: the same
// answer; another body: idempotency.ErrConflict). It returns the HTTP
// status (202) and body.
func (s *Service) Create(ctx context.Context, sc tenant.Scope, requestID, key string, body []byte, req CreateRequest) (int, []byte, error) {
	canonical, err := hashing.Canonical(body)
	if err != nil {
		return 0, nil, err
	}
	k := idempotency.Key{Org: sc.OrganizationID, Actor: sc.UserID, Operation: createOperation, Key: key}
	var status int
	var out []byte
	err = s.DB.Run(ctx, sc, tenant.ReadWrite, func(ctx context.Context, tx tenant.Tx, m tenant.Membership) error {
		if err := m.Require(writeRoles...); err != nil {
			return err
		}
		replay, err := idempotency.Claim(ctx, tx, k, hashing.Sum(canonical))
		if err != nil {
			return err
		}
		if replay != nil {
			status = replay.Status
			out, err = hashing.Canonical(replay.Body)
			return err
		}
		r, err := s.freeze(ctx, tx, m.OrganizationID, req)
		if err != nil {
			return err
		}
		r.ID, r.CreatedBy = ids.New("sim"), m.UserID
		run, err := store.Insert(ctx, tx, r)
		if err != nil {
			return err
		}
		if _, err := jobs.Enqueue(ctx, tx, jobs.New{ID: ids.New("job"), OrganizationID: m.OrganizationID, Type: jobs.Simulation,
			Resource: jobs.Resource{SimulationRunID: run.ID}, Payload: map[string]any{"run_id": run.ID, "request_id": requestID}}, s.policy()); err != nil {
			return err
		}
		// The frozen input by its hashes only, never its content (SECURITY §9).
		meta := map[string]any{"model_version_id": run.ModelVersionID, "replications": run.Config.Replications, "engine_version": run.EngineVersion,
			"model_hash": run.ModelHash, "effective_model_hash": run.EffectiveModelHash, "config_hash": run.ConfigHash,
			"dataset_manifest_hash": run.DatasetManifestHash}
		if run.ScenarioID != nil {
			meta["scenario_id"] = *run.ScenarioID
		}
		if err := auditlog.AppendUser(ctx, tx, m.OrganizationID, m.UserID, auditlog.Event{
			Action: "simulation.create", TargetType: "simulation", TargetID: run.ID, RequestID: requestID, Metadata: meta}); err != nil {
			return err
		}
		data, err := json.Marshal(Accepted{ID: run.ID, Status: string(store.Queued), ModelVersionID: run.ModelVersionID, CreatedAt: timestamp(run.CreatedAt)})
		if err != nil {
			return err
		}
		if out, err = hashing.Canonical(data); err != nil {
			return err
		}
		status = 202
		return idempotency.Complete(ctx, tx, k, idempotency.Response{Status: status, Body: out})
	})
	return status, out, err
}

// freeze builds the run row of req: the version and its whole manifest,
// the scenario patch, the effective model's hash, the configuration and its
// hash, the engine version and the limits, after checking all of it as the
// engine will.
func (s *Service) freeze(ctx context.Context, tx tenant.Tx, org string, req CreateRequest) (store.Run, error) {
	if issues := configIssues(req.Config); len(issues) > 0 {
		return store.Run{}, &RequestError{Issues: issues}
	}
	v, err := models.GetVersion(ctx, tx, org, req.ModelVersionID)
	if errors.Is(err, models.ErrNotFound) {
		return store.Run{}, ErrNotFound
	}
	if err != nil {
		return store.Run{}, err
	}
	patchDoc, scenarioID := []byte(req.ScenarioPatch), (*string)(nil)
	if req.ScenarioID != "" {
		scn, err := store.GetScenario(ctx, tx, org, req.ScenarioID)
		if errors.Is(err, store.ErrNotFound) {
			return store.Run{}, ErrNotFound
		}
		if err != nil {
			return store.Run{}, err
		}
		if scn.BaseVersionID != v.ID {
			return store.Run{}, &RequestError{Issues: []model.ValidationIssue{model.NewIssue(validate.ConstraintFailed, "/scenario_id",
				"the scenario is a patch of version "+scn.BaseVersionID+", not of model_version_id", model.SeverityError)}}
		}
		patchDoc, scenarioID = scn.Patch, &scn.ID
	}
	res, err := patch.Apply(v.IR, patchDoc, patch.Scenario)
	if err != nil {
		return store.Run{}, err
	}
	if !res.Applied() {
		return store.Run{}, &InvalidError{Issues: capped(prefixed("/scenario_patch", res.PatchIssues))}
	}
	if !res.Valid() {
		return store.Run{}, &InvalidError{Issues: capped(validate.Sort(res.Validation.Issues))}
	}
	ir := res.Validation.IR
	pack, err := s.Packs.Lookup(ir.Domain)
	if err != nil {
		return store.Run{}, err
	}
	if issues := pack.Validate(ir); slices.ContainsFunc(issues, isError) {
		return store.Run{}, &InvalidError{Issues: capped(validate.Sort(issues))}
	}
	limits := s.limits()
	if len(v.DatasetBindings) > 0 && s.Objects == nil ||
		req.Config.TraceLevel == simulation.TraceSampled && limits.MaxTraceEvents > 0 && s.Objects == nil {
		return store.Run{}, ErrStorageUnavailable
	}
	keys, err := snapshots.Keys(ctx, tx, org, v.DatasetBindings)
	if err != nil {
		return store.Run{}, err
	}
	resolution, err := datasets.Resolve(ctx, snapshots.Store{Keys: keys, Objects: s.Objects}, org, ir, v.DatasetBindings)
	if err != nil {
		return store.Run{}, err
	}
	if !resolution.Valid() {
		return store.Run{}, &InvalidError{Issues: capped(withoutIR(resolution.Issues))}
	}
	effective, err := hashing.Model(res.Candidate)
	if err != nil {
		return store.Run{}, err
	}
	// The engine recomputes the effective hash from the decoded model
	// (T-022); a run is queued only if the two definitions agree.
	encoded, err := model.Encode(ir)
	if err != nil {
		return store.Run{}, err
	}
	if again, err := hashing.Model(encoded); err != nil || again != effective {
		return store.Run{}, fmt.Errorf("runs: the engine would hash the effective model differently (%s, %s): %v", effective, again, err)
	}
	if _, err := engine.Compile(simulation.RunInput{Model: *ir, ModelHash: v.Hash, EffectiveModelHash: effective, Config: req.Config,
		Limits: limits, Datasets: resolution.Datasets, Manifest: resolution.Manifest}); err != nil {
		var ie *engine.InputError
		if errors.As(err, &ie) {
			return store.Run{}, &InvalidError{Issues: capped(ie.Issues)}
		}
		return store.Run{}, err
	}
	configHash, err := hashing.Config(req.Config)
	if err != nil {
		return store.Run{}, err
	}
	frozenPatch, err := hashing.Canonical(patchDoc)
	if err != nil {
		return store.Run{}, err
	}
	return store.Run{OrganizationID: org, ModelID: v.ModelID, ModelVersionID: v.ID, ScenarioID: scenarioID, Name: req.Name,
		ScenarioPatch: frozenPatch, Config: req.Config, EngineVersion: s.engineVersion(), ModelHash: v.Hash, EffectiveModelHash: effective,
		ConfigHash: configHash, DatasetManifestHash: resolution.ManifestHash, DatasetBindings: resolution.Manifest, Limits: limits}, nil
}

// configIssues checks what the schema cannot: both durations within 365
// days in ticks and the warmup shorter than the horizon (SIMULATION_SPEC
// §5), as the engine's Compile does.
func configIssues(c simulation.RunConfig) []model.ValidationIssue {
	var issues []model.ValidationIssue
	horizon, err := model.Ticks(c.Horizon.Value, c.Horizon.Unit)
	if err != nil {
		issues = append(issues, model.NewIssue(validate.LimitExceeded, "/config/horizon", err.Error(), model.SeverityError))
	}
	warmup, werr := model.Ticks(c.Warmup.Value, c.Warmup.Unit)
	if werr != nil {
		issues = append(issues, model.NewIssue(validate.LimitExceeded, "/config/warmup", werr.Error(), model.SeverityError))
	}
	if err == nil && werr == nil && warmup >= horizon {
		issues = append(issues, model.NewIssue(validate.LimitExceeded, "/config/warmup", "the warmup must be shorter than the horizon", model.SeverityError))
	}
	return issues
}

func isError(i model.ValidationIssue) bool { return i.Severity == model.SeverityError }

func prefixed(root string, issues []model.ValidationIssue) []model.ValidationIssue {
	out := make([]model.ValidationIssue, len(issues))
	for i, is := range issues {
		out[i] = model.NewIssue(is.Code, root+is.Path, is.Message, is.Severity)
	}
	return validate.Sort(out)
}

// withoutIR rewrites the binding stage's /ir/... paths as paths into the
// effective IR, as the other model issues are; manifest paths stay.
func withoutIR(issues []model.ValidationIssue) []model.ValidationIssue {
	out := make([]model.ValidationIssue, len(issues))
	for i, is := range issues {
		if rest, ok := strings.CutPrefix(is.Path, "/ir"); ok && (rest == "" || rest[0] == '/') {
			is.Path = rest
		}
		out[i] = is
	}
	return validate.Sort(out)
}

// capped keeps an issue list within Error.details' 1000: the first ones,
// after a LIMIT_EXCEEDED issue that says how many are left out.
func capped(issues []model.ValidationIssue) []model.ValidationIssue {
	if len(issues) <= maxIssues {
		return issues
	}
	kept, rest := issues[:maxIssues-1], issues[maxIssues-1:]
	severity := model.SeverityWarning
	if slices.ContainsFunc(rest, isError) {
		severity = model.SeverityError
	}
	return append([]model.ValidationIssue{model.NewIssue(validate.LimitExceeded, "", fmt.Sprintf("%d more issues are not listed", len(rest)), severity)}, kept...)
}

// Get returns run id.
func (s *Service) Get(ctx context.Context, sc tenant.Scope, requestID, id string) (Run, error) {
	var out Run
	err := s.DB.Run(ctx, sc, tenant.ReadOnly, func(ctx context.Context, tx tenant.Tx, m tenant.Membership) error {
		if err := m.Require(readRoles...); err != nil {
			return err
		}
		r, err := store.Get(ctx, tx, m.OrganizationID, id)
		if errors.Is(err, store.ErrNotFound) {
			return ErrNotFound
		}
		if err != nil {
			return err
		}
		out, err = public(r, requestID)
		return err
	})
	return out, err
}

// List returns one page of the model's runs, oldest first.
func (s *Service) List(ctx context.Context, sc tenant.Scope, requestID, modelID, cursor string, limit int) (Page, error) {
	if limit < 1 || limit > maxLimit {
		return Page{}, ErrInvalidLimit
	}
	var after *store.Position
	if cursor != "" {
		pos, err := decodeCursor(cursor, sc.OrganizationID, modelID)
		if err != nil {
			return Page{}, err
		}
		after = &pos
	}
	page := Page{Items: []Run{}}
	err := s.DB.Run(ctx, sc, tenant.ReadOnly, func(ctx context.Context, tx tenant.Tx, m tenant.Membership) error {
		if err := m.Require(readRoles...); err != nil {
			return err
		}
		if _, err := models.GetModel(ctx, tx, m.OrganizationID, modelID); errors.Is(err, models.ErrNotFound) {
			return ErrNotFound
		} else if err != nil {
			return err
		}
		rows, err := store.List(ctx, tx, m.OrganizationID, modelID, after, limit+1)
		if err != nil {
			return err
		}
		if len(rows) > limit {
			last := rows[limit-1]
			c := encodeCursor(m.OrganizationID, modelID, store.Position{CreatedAt: last.CreatedAt, ID: last.ID})
			page.NextCursor = &c
			rows = rows[:limit]
		}
		for _, r := range rows {
			p, err := public(r, requestID)
			if err != nil {
				return err
			}
			page.Items = append(page.Items, p)
		}
		return nil
	})
	return page, err
}

// Cancel requests cancellation of run id (API_DATABASE §6): a queued run is
// cancelled at once with its job; a running one is marked, and its worker
// stops at the next checkpoint; a cancelled one is returned as it is; a
// completed or failed one is ErrFinished. Like Create it is idempotent per
// key (the key belongs to this run) and returns the HTTP status and body.
func (s *Service) Cancel(ctx context.Context, sc tenant.Scope, requestID, key, id string, body []byte) (int, []byte, error) {
	canonical, err := hashing.Canonical(body)
	if err != nil {
		return 0, nil, err
	}
	fingerprint, err := json.Marshal(map[string]any{"run_id": id, "body": jsontext.Value(canonical)}, json.Deterministic(true))
	if err != nil {
		return 0, nil, err
	}
	hash, err := hashing.Of(fingerprint)
	if err != nil {
		return 0, nil, err
	}
	k := idempotency.Key{Org: sc.OrganizationID, Actor: sc.UserID, Operation: cancelOperation, Key: key}
	var status int
	var out []byte
	err = s.DB.Run(ctx, sc, tenant.ReadWrite, func(ctx context.Context, tx tenant.Tx, m tenant.Membership) error {
		if err := m.Require(writeRoles...); err != nil {
			return err
		}
		replay, err := idempotency.Claim(ctx, tx, k, hash)
		if err != nil {
			return err
		}
		if replay != nil {
			status = replay.Status
			out, err = hashing.Canonical(replay.Body)
			return err
		}
		// The job first, then the run: the order the worker's finalizing
		// transaction locks them in (ADR-024), so a cancellation and a
		// completion take turns and the second sees what the first did.
		job, err := store.LockJob(ctx, tx, m.OrganizationID, id)
		if errors.Is(err, store.ErrNotFound) {
			return ErrNotFound
		}
		if err != nil {
			return err
		}
		r, err := store.Lock(ctx, tx, m.OrganizationID, id)
		if errors.Is(err, store.ErrNotFound) {
			return ErrNotFound
		}
		if err != nil {
			return err
		}
		effect := ""
		switch {
		case r.Status == store.Completed || r.Status == store.Failed:
			return ErrFinished
		case r.Status == store.Cancelled:
		case job.Status == string(jobs.Queued):
			// The run follows its job (0009): both end now.
			if ok, err := jobs.CancelQueued(ctx, tx, m.OrganizationID, job.ID); err != nil {
				return err
			} else if !ok {
				return fmt.Errorf("runs: the queued job %s of %s was not cancelled", job.ID, id)
			}
			effect = "cancelled"
		case job.Status == string(jobs.Running):
			requested, err := store.RequestCancellation(ctx, tx, m.OrganizationID, id)
			if err != nil {
				return err
			}
			if requested {
				effect = "cancellation_requested"
			}
		default:
			return fmt.Errorf("runs: run %s is %s but its job is %s", id, r.Status, job.Status)
		}
		if effect != "" {
			if err := auditlog.AppendUser(ctx, tx, m.OrganizationID, m.UserID, auditlog.Event{
				Action: "simulation.cancel", TargetType: "simulation", TargetID: id, RequestID: requestID,
				Metadata: map[string]any{"effect": effect}}); err != nil {
				return err
			}
			if r, err = store.Get(ctx, tx, m.OrganizationID, id); err != nil {
				return err
			}
		}
		p, err := public(r, requestID)
		if err != nil {
			return err
		}
		data, err := json.Marshal(p)
		if err != nil {
			return err
		}
		if out, err = hashing.Canonical(data); err != nil {
			return err
		}
		status = 200
		return idempotency.Complete(ctx, tx, k, idempotency.Response{Status: status, Body: out})
	})
	return status, out, err
}

// Results returns the stored Result of completed run id (canonical JSON);
// a run without one is ErrNotReady (partial output is never shown).
func (s *Service) Results(ctx context.Context, sc tenant.Scope, id string) ([]byte, error) {
	var out []byte
	err := s.DB.Run(ctx, sc, tenant.ReadSnapshot, func(ctx context.Context, tx tenant.Tx, m tenant.Membership) error {
		if err := m.Require(readRoles...); err != nil {
			return err
		}
		r, err := store.Get(ctx, tx, m.OrganizationID, id)
		if errors.Is(err, store.ErrNotFound) {
			return ErrNotFound
		}
		if err != nil {
			return err
		}
		if r.Status != store.Completed {
			return ErrNotReady
		}
		res, err := store.GetResult(ctx, tx, m.OrganizationID, id)
		if err != nil {
			return err
		}
		out, err = hashing.Canonical(res.Result)
		return err
	})
	return out, err
}

// ErrTraceCorrupt: the stored trace object is missing or is not the one
// its result row names (size or SHA-256): nothing is served (fail closed).
var ErrTraceCorrupt = errors.New("runs: the stored trace does not match its record")

// Trace returns the sampled trace artifact of completed run id, the bytes
// its result row names, after checking the caller may read the run (the
// trace is the run's organization's only, SECURITY §6). The object is read
// after the transaction and served only if its size and SHA-256 match.
func (s *Service) Trace(ctx context.Context, sc tenant.Scope, id string) ([]byte, error) {
	var trace *store.Trace
	err := s.DB.Run(ctx, sc, tenant.ReadSnapshot, func(ctx context.Context, tx tenant.Tx, m tenant.Membership) error {
		if err := m.Require(readRoles...); err != nil {
			return err
		}
		r, err := store.Get(ctx, tx, m.OrganizationID, id)
		if errors.Is(err, store.ErrNotFound) {
			return ErrNotFound
		}
		if err != nil {
			return err
		}
		if r.Status != store.Completed {
			return ErrNotReady
		}
		res, err := store.GetResult(ctx, tx, m.OrganizationID, id)
		if err != nil {
			return err
		}
		if res.Trace == nil {
			return ErrNoTrace
		}
		if !objects.InArea(m.OrganizationID, objects.SimulationResults, res.Trace.ObjectKey) {
			return ErrTraceCorrupt
		}
		trace = res.Trace
		return nil
	})
	if err != nil {
		return nil, err
	}
	if s.Objects == nil {
		return nil, ErrStorageUnavailable
	}
	rc, err := s.Objects.Get(ctx, trace.ObjectKey)
	if errors.Is(err, objects.ErrNotFound) {
		return nil, ErrTraceCorrupt
	}
	if err != nil {
		return nil, err
	}
	defer rc.Close()
	data, err := io.ReadAll(io.LimitReader(rc, trace.Size+1))
	if err != nil {
		return nil, err
	}
	if int64(len(data)) != trace.Size || hashing.ContentHash(data) != trace.SHA256 {
		return nil, ErrTraceCorrupt
	}
	return data, nil
}

// Snapshot returns run id's progress, after checking the caller's
// membership and role in the same transaction: the SSE stream calls it
// before each event, so a member removed (or with a role that no longer
// reads runs) gets no further event (TEST-026). Both reads see one
// snapshot, so an event never shows a change committed after the
// membership was gone.
func (s *Service) Snapshot(ctx context.Context, sc tenant.Scope, id string) (Snapshot, error) {
	var out Snapshot
	err := s.DB.Run(ctx, sc, tenant.ReadSnapshot, func(ctx context.Context, tx tenant.Tx, m tenant.Membership) error {
		if err := m.Require(readRoles...); err != nil {
			return err
		}
		r, err := store.Get(ctx, tx, m.OrganizationID, id)
		if errors.Is(err, store.ErrNotFound) {
			return ErrNotFound
		}
		if err != nil {
			return err
		}
		out = snapshot(r)
		return nil
	})
	return out, err
}

func snapshot(r store.Run) Snapshot {
	return Snapshot{RunID: r.ID, Status: string(r.Status), Revision: r.ProgressRevision, CompletedReplications: r.CompletedReplications,
		TotalReplications: r.Config.Replications, EventsProcessed: r.EventsProcessed, CancellationRequested: r.CancellationRequested}
}

func timestamp(t time.Time) string { return t.UTC().Format(time.RFC3339Nano) }

func optionalTime(t *time.Time) *string {
	if t == nil {
		return nil
	}
	s := timestamp(*t)
	return &s
}

// public is the Run resource of r. A failed run's error is its job's
// failure in the contract's error envelope, under the request that
// created the run (requestID only if that is unknown).
func public(r store.Run, requestID string) (Run, error) {
	p := Run{ID: r.ID, OrganizationID: r.OrganizationID, ModelID: r.ModelID, ModelVersionID: r.ModelVersionID, Name: r.Name,
		ScenarioPatch: jsontext.Value(r.ScenarioPatch), ScenarioID: r.ScenarioID, Config: r.Config, ConfigHash: r.ConfigHash,
		ModelHash: r.ModelHash, EffectiveModelHash: r.EffectiveModelHash, EngineVersion: r.EngineVersion, DatasetBindings: r.DatasetBindings,
		ExecutionLimits: r.Limits, Status: string(r.Status), CancellationRequested: r.CancellationRequested, CreatedAt: timestamp(r.CreatedAt),
		StartedAt: optionalTime(r.StartedAt), FinishedAt: optionalTime(r.FinishedAt),
		Progress: Progress{CompletedReplications: r.CompletedReplications, TotalReplications: r.Config.Replications,
			EventsProcessed: r.EventsProcessed, Revision: r.ProgressRevision}}
	if p.DatasetBindings == nil {
		p.DatasetBindings = []model.DatasetBinding{}
	}
	if r.Status == store.Failed {
		id := r.RequestID
		if id == "" {
			id = requestID
		}
		e, err := runError(r.Failure, id)
		if err != nil {
			return Run{}, fmt.Errorf("run %s: %w", r.ID, err)
		}
		p.Error = e
	}
	return p, nil
}

// ErrorEnvelope is common.schema.json#/$defs/Error.
type ErrorEnvelope struct {
	Error ErrorBody `json:"error"`
}

// ErrorBody is ErrorEnvelope.error.
type ErrorBody struct {
	Code      string        `json:"code"`
	Message   string        `json:"message"`
	Details   []ErrorDetail `json:"details"`
	RequestID string        `json:"request_id"`
}

// ErrorDetail is one ErrorBody.details entry.
type ErrorDetail struct {
	Path     string `json:"path"`
	Reason   string `json:"reason"`
	Code     string `json:"code,omitzero"`
	Severity string `json:"severity,omitzero"`
}

// limitPaths are the execution limits whose failure codes the runner
// records, at their place in the Run resource.
var limitPaths = map[string]string{
	store.EventBudgetExceeded:    "/execution_limits/max_events",
	store.WallTimeoutExceeded:    "/execution_limits/wall_timeout_seconds",
	store.ActiveEntitiesExceeded: "/execution_limits/max_active_entities",
	store.EntityVisitsExceeded:   "/execution_limits/max_entity_visits",
}

var failureCode = regexp.MustCompile(`^[A-Z][A-Z0-9_]{0,63}$`)

// runError shows a stored job failure as the contract's error. The
// contract does not define run failure codes (CB-38, a question for the
// owner); until it does, the closed ErrorCode list is used as follows, with
// the failure's own code in details[0].code and, for a limit, the limit's
// place in the Run resource as details[0].path:
//   - an execution limit (EVENT_BUDGET_EXCEEDED, WALL_TIMEOUT_EXCEEDED,
//     ACTIVE_ENTITIES_EXCEEDED, ENTITY_VISITS_EXCEEDED) or an input the
//     engine refused (RUN_INPUT_INVALID): MODEL_VALIDATION_FAILED, as
//     D-023 counts runtime limits among the validator's issues;
//   - the infrastructure failing until the attempts were spent
//     (LEASE_EXPIRED, WORKER_STOPPED, ARTIFACT_STORAGE_FAILED):
//     SERVICE_UNAVAILABLE;
//   - anything else (INTERNAL, NO_RESULT, ENGINE_INTERNAL,
//     ENGINE_VERSION_MISMATCH): INTERNAL_ERROR.
func runError(failure []byte, requestID string) (*ErrorEnvelope, error) {
	var f jobs.Failure
	if err := json.Unmarshal(failure, &f); err != nil || !failureCode.MatchString(f.Code) {
		return nil, fmt.Errorf("the stored failure is not a job failure")
	}
	message := f.Message
	if message == "" {
		message = "the run failed"
	}
	body := ErrorBody{Code: "INTERNAL_ERROR", Message: message, RequestID: requestID, Details: []ErrorDetail{{Path: "", Reason: message, Code: f.Code}}}
	switch path, limit := limitPaths[f.Code]; {
	case limit:
		body.Code = "MODEL_VALIDATION_FAILED"
		body.Details[0].Path, body.Details[0].Severity = path, string(model.SeverityError)
	case f.Code == store.RunInputInvalid:
		body.Code = "MODEL_VALIDATION_FAILED"
	case f.Code == jobs.LeaseExpired || f.Code == "WORKER_STOPPED" || f.Code == store.ArtifactStorageFailed:
		body.Code = "SERVICE_UNAVAILABLE"
	}
	return &ErrorEnvelope{Error: body}, nil
}

// cursor is the position after which a page starts, bound to the
// organization, the model and the list (API_DATABASE §3).
type cursor struct {
	Version int    `json:"v"`
	Org     string `json:"o"`
	List    string `json:"l"`
	Model   string `json:"m"`
	After   string `json:"t"` // RFC 3339 with nanoseconds
	ID      string `json:"i"`
}

const cursorList = "simulations"

var runIDs = regexp.MustCompile(`^sim_[0-7][0-9A-HJKMNP-TV-Z]{25}$`)

func encodeCursor(org, modelID string, pos store.Position) string {
	data, _ := json.Marshal(cursor{Version: 1, Org: org, List: cursorList, Model: modelID, After: timestamp(pos.CreatedAt), ID: pos.ID})
	return base64.RawURLEncoding.EncodeToString(data)
}

func decodeCursor(s, org, modelID string) (store.Position, error) {
	data, err := base64.RawURLEncoding.Strict().DecodeString(s)
	if err != nil {
		return store.Position{}, ErrInvalidCursor
	}
	var c cursor
	if err := json.Unmarshal(data, &c, json.RejectUnknownMembers(true)); err != nil {
		return store.Position{}, ErrInvalidCursor
	}
	t, err := time.Parse(time.RFC3339Nano, c.After)
	if err != nil || c.Version != 1 || c.Org != org || c.List != cursorList || c.Model != modelID || !runIDs.MatchString(c.ID) {
		return store.Position{}, ErrInvalidCursor
	}
	return store.Position{CreatedAt: t, ID: c.ID}, nil
}
