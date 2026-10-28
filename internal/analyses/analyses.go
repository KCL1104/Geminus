// Package analyses is the simulation analysis service (T-030; AI_SPEC §9,
// D-010, D-016, D-026): it queues an analysis of a completed run, beside
// an optional completed baseline run of the same model, and serves
// analyses as their job left them. Each operation is one tenant.DB.Run
// transaction that checks the caller's membership and role first (D-008);
// SQL lives in internal/storage. The job that asks the provider and checks
// its answer against the results is internal/analyses/analyzer's.
//
// The server binds every identity: the client names the runs, the
// question and the output language; the server adds the organization, the
// creator and the status, and the analysis record names the runs by ID
// where the provider's answer names them only by role (D-026). The
// analysis, its one job, its audit event and the idempotency response
// commit together, and only then is 202 answered (D-011).
package analyses

import (
	"context"
	"encoding/json/jsontext"
	"encoding/json/v2"
	"errors"
	"strings"
	"time"

	"github.com/kcl1104/geminus/internal/ai/locale"
	"github.com/kcl1104/geminus/internal/ids"
	"github.com/kcl1104/geminus/internal/model"
	"github.com/kcl1104/geminus/internal/model/hashing"
	"github.com/kcl1104/geminus/internal/model/validate"
	"github.com/kcl1104/geminus/internal/proposals"
	store "github.com/kcl1104/geminus/internal/storage/analyses"
	"github.com/kcl1104/geminus/internal/storage/auditlog"
	"github.com/kcl1104/geminus/internal/storage/idempotency"
	"github.com/kcl1104/geminus/internal/storage/jobs"
	"github.com/kcl1104/geminus/internal/storage/runs"
	"github.com/kcl1104/geminus/internal/tenant"
)

var (
	// ErrNotFound: no such analysis in the organization (or no access).
	ErrNotFound = errors.New("analyses: not found")
	// ErrRunNotFound: no such run (RunError.Param says which).
	ErrRunNotFound = errors.New("analyses: no such run")
	// ErrNotReady: the run has no result yet (409 RESULT_NOT_READY): only a
	// completed run is analyzed (AI_SPEC §9).
	ErrNotReady = errors.New("analyses: the run has no result yet")
)

// The parameters a RunError names: the analyzed run (the path's {id}) or
// the baseline.
const (
	RunParam      = "id"
	BaselineParam = "/baseline_run_id"
)

// RunError is an error about one of the request's runs.
type RunError struct {
	Param string
	Err   error
}

func (e *RunError) Error() string { return "analyses: " + e.Param + ": " + e.Err.Error() }
func (e *RunError) Unwrap() error { return e.Err }

// RequestError is a request that is not acceptable as it stands (422
// REQUEST_VALIDATION_FAILED); Issues point into the request body.
type RequestError struct{ Issues []model.ValidationIssue }

func (e *RequestError) Error() string { return "analyses: the request is not acceptable" }

// Service runs analysis operations for a verified scope.
type Service struct {
	DB *tenant.DB
	// AI is this deployment's provider availability (cmd/api derives it as
	// for proposals); the zero value refuses every new analysis.
	AI proposals.Availability
	// Policy is the queue's (jobs.DefaultPolicy when zero).
	Policy jobs.Policy
}

// The roles each operation requires (x-required-roles, planning/api-operations.json).
var (
	writeRoles = tenant.Writers
	readRoles  = tenant.Members
)

const createOperation = "createAnalysis"

// CreateRequest is http.schema.json#/$defs/AnalysisRequest.
type CreateRequest struct {
	BaselineRunID string `json:"baseline_run_id,omitzero"`
	Question      string `json:"question,omitzero"`
	OutputLocale  string `json:"output_locale,omitzero"`
}

// Accepted is http.schema.json#/$defs/AnalysisAccepted.
type Accepted struct {
	ID     string `json:"id"`
	Status string `json:"status"`
}

// Create queues an analysis of run runID, or replays the response of the
// earlier request with the same Idempotency-Key (same run and body: the
// same answer; anything else: idempotency.ErrConflict). It returns the
// HTTP status (202) and body.
func (s *Service) Create(ctx context.Context, sc tenant.Scope, requestID, key, runID string, body []byte, req CreateRequest) (int, []byte, error) {
	canonical, err := hashing.Canonical(body)
	if err != nil {
		return 0, nil, err
	}
	fingerprint, err := json.Marshal(map[string]any{"run_id": runID, "body": jsontext.Value(canonical)}, json.Deterministic(true))
	if err != nil {
		return 0, nil, err
	}
	hash, err := hashing.Of(fingerprint)
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
		replay, err := idempotency.Claim(ctx, tx, k, hash)
		if err != nil {
			return err
		}
		if replay != nil {
			status = replay.Status
			out, err = hashing.Canonical(replay.Body)
			return err
		}
		if s.AI != proposals.Available {
			return &proposals.UnavailableError{Availability: s.AI}
		}
		a, err := prepare(ctx, tx, m, runID, req)
		if err != nil {
			return err
		}
		if a, err = store.Insert(ctx, tx, a); err != nil {
			return err
		}
		policy := s.Policy
		if policy == (jobs.Policy{}) {
			policy = jobs.DefaultPolicy
		}
		if _, err := jobs.Enqueue(ctx, tx, jobs.New{ID: ids.New("job"), OrganizationID: m.OrganizationID, Type: jobs.Analysis,
			Resource: jobs.Resource{AIAnalysisID: a.ID}, Payload: map[string]any{"analysis_id": a.ID, "request_id": requestID}}, policy); err != nil {
			return err
		}
		// IDs, the language and whether a question was asked: never the
		// question (customer prose, SECURITY §9).
		meta := map[string]any{"run_id": a.RunID, "output_locale": a.OutputLocale, "question": a.Question != nil}
		if a.BaselineRunID != nil {
			meta["baseline_run_id"] = *a.BaselineRunID
		}
		if err := auditlog.AppendUser(ctx, tx, m.OrganizationID, m.UserID, auditlog.Event{
			Action: "analysis.create", TargetType: "ai_analysis", TargetID: a.ID, RequestID: requestID, Metadata: meta}); err != nil {
			return err
		}
		data, err := json.Marshal(Accepted{ID: a.ID, Status: string(store.Generating)})
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

// prepare checks req and the runs in the caller's organization, and binds
// what the server decides: the analysis's ID, organization and creator.
func prepare(ctx context.Context, tx tenant.Tx, m tenant.Membership, runID string, req CreateRequest) (store.Analysis, error) {
	var issues []model.ValidationIssue
	loc, err := locale.Parse(req.OutputLocale)
	if err != nil {
		issues = append(issues, model.NewIssue(validate.ConstraintFailed, "/output_locale", "output_locale is en or zh-Hant", model.SeverityError))
	}
	if req.Question != "" && strings.TrimSpace(req.Question) == "" {
		issues = append(issues, model.NewIssue(validate.ConstraintFailed, "/question", "the question says nothing: it is only white space", model.SeverityError))
	}
	if req.BaselineRunID == runID {
		issues = append(issues, model.NewIssue(validate.ConstraintFailed, BaselineParam, "the baseline is another run than the one analyzed", model.SeverityError))
	}
	if len(issues) > 0 {
		return store.Analysis{}, &RequestError{Issues: issues}
	}
	run, err := completed(ctx, tx, m.OrganizationID, runID, RunParam)
	if err != nil {
		return store.Analysis{}, err
	}
	a := store.Analysis{ID: ids.New("ana"), OrganizationID: m.OrganizationID, RunID: run.ID, OutputLocale: string(loc), CreatedBy: m.UserID}
	if req.Question != "" {
		a.Question = &req.Question
	}
	if req.BaselineRunID != "" {
		base, err := completed(ctx, tx, m.OrganizationID, req.BaselineRunID, BaselineParam)
		if err != nil {
			return store.Analysis{}, err
		}
		if base.ModelID != run.ModelID {
			return store.Analysis{}, &RequestError{Issues: []model.ValidationIssue{
				model.NewIssue(validate.ConstraintFailed, BaselineParam, "the baseline is a run of another model", model.SeverityError)}}
		}
		a.BaselineRunID = &base.ID
	}
	return a, nil
}

// completed returns run id if the organization has it and it completed.
func completed(ctx context.Context, tx tenant.Tx, org, id, param string) (runs.Run, error) {
	r, err := runs.Get(ctx, tx, org, id)
	if errors.Is(err, runs.ErrNotFound) {
		return runs.Run{}, &RunError{Param: param, Err: ErrRunNotFound}
	}
	if err != nil {
		return runs.Run{}, err
	}
	if r.Status != runs.Completed {
		return runs.Run{}, &RunError{Param: param, Err: ErrNotReady}
	}
	return r, nil
}

// Record is http.schema.json#/$defs/AnalysisRecord.
type Record struct {
	ID             string                   `json:"id"`
	OrganizationID string                   `json:"organization_id"`
	RunID          string                   `json:"run_id"`
	BaselineRunID  *string                  `json:"baseline_run_id"`
	Status         string                   `json:"status"`
	Analysis       jsontext.Value           `json:"analysis"`
	CreatedAt      string                   `json:"created_at"`
	Error          *proposals.ErrorEnvelope `json:"error"`
}

// Get returns analysis id.
func (s *Service) Get(ctx context.Context, sc tenant.Scope, requestID, id string) (Record, error) {
	var out Record
	err := s.DB.Run(ctx, sc, tenant.ReadOnly, func(ctx context.Context, tx tenant.Tx, m tenant.Membership) error {
		if err := m.Require(readRoles...); err != nil {
			return err
		}
		a, err := store.Get(ctx, tx, m.OrganizationID, id)
		if errors.Is(err, store.ErrNotFound) {
			return ErrNotFound
		}
		if err != nil {
			return err
		}
		out, err = public(a, requestID)
		return err
	})
	return out, err
}

// public is the record of a as the contract shows it: its analysis only
// when completed, its job's failure only when failed, as the contract's
// error with the failure's own code in details[0].code.
func public(a store.Analysis, requestID string) (Record, error) {
	r := Record{ID: a.ID, OrganizationID: a.OrganizationID, RunID: a.RunID, BaselineRunID: a.BaselineRunID, Status: string(a.Status),
		Analysis: jsontext.Value("null"), CreatedAt: a.CreatedAt.UTC().Format(time.RFC3339Nano)}
	switch a.Status {
	case store.Completed:
		r.Analysis = jsontext.Value(a.Analysis)
	case store.Failed:
		e, err := proposals.FailureError(a.Failure, requestID)
		if err != nil {
			return Record{}, err
		}
		r.Error = e
	}
	return r, nil
}
