// Package investigations is the API's side of an AI investigation (H-056,
// H-060, H-061 §3, §6, §10, §13; slice A7; behind GEMINUS_INVESTIGATIONS,
// off by default): a person's own requests to start one, read it, list a
// model's and stop one. Each operation is one tenant.DB.Run transaction
// that checks the caller's membership and role first (D-008); SQL lives in
// internal/storage.
//
// createInvestigation stores the investigation (the stored acting-for
// scope of D-008 as amended: the organization, the requester, the frozen
// request, run settings and caps, a deadline 20 minutes on), its one
// investigation job and its audit event, and the idempotency response, in
// one transaction; nothing is proposed: the worker's agent asks the planner
// for the first tool call, and the API's executor
// (internal/investigations/executor) carries out each call it records.
// This package never acts for the AI itself: it never sets
// tenant.Scope.ActingFor, never links internal/tenant/actingfor, and never
// publishes, approves or rejects (TestNoActingPublishOrDecision).
//
// Reads show the ledger's steps, what the investigation made, the API's own
// comparison of each what-if run against the baseline (the figures the AI
// was shown, internal/ai/investigator.ComparisonOf), the server's ranking
// and closing line, and the AI's claimed outcome only as its claim.
package investigations

import (
	"context"
	"encoding/json/jsontext"
	"encoding/json/v2"
	"errors"
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/kcl1104/geminus/internal/ai/locale"
	"github.com/kcl1104/geminus/internal/ai/provider"
	"github.com/kcl1104/geminus/internal/ai/spend"
	"github.com/kcl1104/geminus/internal/ids"
	"github.com/kcl1104/geminus/internal/model"
	"github.com/kcl1104/geminus/internal/model/hashing"
	"github.com/kcl1104/geminus/internal/model/validate"
	"github.com/kcl1104/geminus/internal/proposals"
	"github.com/kcl1104/geminus/internal/simulation"
	analysisstore "github.com/kcl1104/geminus/internal/storage/analyses"
	"github.com/kcl1104/geminus/internal/storage/auditlog"
	"github.com/kcl1104/geminus/internal/storage/idempotency"
	store "github.com/kcl1104/geminus/internal/storage/investigations"
	"github.com/kcl1104/geminus/internal/storage/jobs"
	"github.com/kcl1104/geminus/internal/storage/models"
	runstore "github.com/kcl1104/geminus/internal/storage/runs"
	"github.com/kcl1104/geminus/internal/tenant"
)

var (
	// ErrOff: investigations are off in this deployment, or not for this
	// organization (503 SERVICE_UNAVAILABLE, INVESTIGATIONS_OFF); nothing
	// was read or written.
	ErrOff = errors.New("investigations: off in this deployment or for this organization")
	// ErrNotFound: no such investigation, model or version in the
	// organization (or no access).
	ErrNotFound = errors.New("investigations: not found")
	// ErrStaleBase: the base is not the model's latest version (409
	// MODEL_VERSION_CONFLICT; a *StaleBaseError says where).
	ErrStaleBase = errors.New("investigations: the base is not the model's latest version")
	// ErrRunning: the caller already has a running investigation in the
	// organization (409 LIFECYCLE_CONFLICT, INVESTIGATION_RUNNING).
	ErrRunning = errors.New("investigations: the caller already has a running investigation")
	// ErrEnded: the investigation already ended (409 LIFECYCLE_CONFLICT).
	ErrEnded = errors.New("investigations: the investigation has ended")
	// ErrNotTheRequester: an editor who did not start it asks to stop it
	// (403 FORBIDDEN).
	ErrNotTheRequester = errors.New("investigations: only the requester or an owner stops an investigation")
	// ErrInvalidCursor and ErrInvalidLimit: the list's paging (400).
	ErrInvalidCursor = errors.New("investigations: invalid cursor")
	ErrInvalidLimit  = errors.New("investigations: invalid page size")
)

// StaleBaseError is ErrStaleBase at Path: /base_version_id for entry A,
// /from_analysis_id for entry B (the analysis's baseline is on an older
// version).
type StaleBaseError struct{ Path string }

func (e *StaleBaseError) Error() string { return ErrStaleBase.Error() + " (" + e.Path + ")" }
func (e *StaleBaseError) Unwrap() error { return ErrStaleBase }

// DailyLimitError: the organization started 10 investigations this UTC day
// (429 RATE_LIMITED, INVESTIGATION_DAILY_LIMIT); RetryAfter is the seconds
// until the next UTC day, by the database's clock.
type DailyLimitError struct{ RetryAfter int }

func (e *DailyLimitError) Error() string {
	return "investigations: the organization's 10 investigations of the UTC day are used up"
}

// RequestError is a request that is not acceptable as it stands (422
// REQUEST_VALIDATION_FAILED); Issues point into the request body.
type RequestError struct{ Issues []model.ValidationIssue }

func (e *RequestError) Error() string {
	return fmt.Sprintf("investigations: invalid request (%d issue(s))", len(e.Issues))
}

// The detail codes of createInvestigation's own refusals (openapi.yaml).
const (
	CodeOff                   = "INVESTIGATIONS_OFF"
	CodePlannerNotOffered     = "INVESTIGATION_PLANNER_NOT_OFFERED"
	CodeResearchOverCeiling   = "INVESTIGATION_RESEARCH_OVER_CEILING"
	CodeRunning               = "INVESTIGATION_RUNNING"
	CodeDailyLimit            = "INVESTIGATION_DAILY_LIMIT"
	CodeRoleNotApproved       = "AI_ROLE_NOT_APPROVED"
	CodePlannerModelNotApprov = "AI_MODEL_NOT_APPROVED"
)

// NotApprovedError: the deployment's recorded approval does not list the
// investigator role (Planner false) or the planner model (Planner true)
// (503 AI_POLICY_NOT_APPROVED); nothing was stored.
type NotApprovedError struct{ Planner bool }

func (e *NotApprovedError) Error() string {
	if e.Planner {
		return "investigations: the planner model is not approved in this deployment (AI_POLICY_NOT_APPROVED, AI_MODEL_NOT_APPROVED)"
	}
	return "investigations: the investigator role is not approved in this deployment (AI_POLICY_NOT_APPROVED, AI_ROLE_NOT_APPROVED)"
}

// PromptTemplateVersion is the planner's prompt (prompts/investigator,
// H-061 §9; its tool descriptions included), stored with each
// investigation: the template's version (investigations_test.go holds them
// equal). investigator-0.2.0 (H-061 A9): optional members left out, a
// refusal's hint; investigator-0.4.0: a failed step's tool is not called
// again, and the prompt says what ends an investigation.
const PromptTemplateVersion = "investigator-0.4.0"

// Ceiling is an investigation's spend (H-060): USD 2.50, settled and open,
// its planner turns', proposals' and explanations' together (the database's
// check_investigation_spend).
const Ceiling spend.NanoUSD = 2_500_000_000

// The research quote of the live adapter (internal/ai/provider/meta's
// Quote for a research call: its context window as the input bound, 64
// server tool calls): a research proposal reserves this much before it is
// sent, so createInvestigation refuses research that cannot fit the
// Ceiling (422 at /research) rather than starting an investigation whose
// first proposal the spend ledger would refuse.
const (
	researchInputTokens int64 = 1_048_576
	researchToolCalls   int64 = 64
)

// ResearchReservation is what one research proposal on model reserves:
// one attempt at its input bound and its output bound (the slow model's for
// Muse Wonder 1.0, H-065), plus its search steps; ok is false for a model
// this build cannot price.
func ResearchReservation(m string) (spend.NanoUSD, bool) {
	price, ok := spend.PriceOf(m)
	search, sok := spend.SearchPriceOf(m)
	if !ok || !sok {
		return 0, false
	}
	out := provider.ResearchMaxOutputTokens
	if provider.SlowModel(m) {
		out = provider.SlowModelMaxOutputTokens
	}
	return price.AttemptBound(researchInputTokens, out) + researchToolCalls*search, true
}

// Service runs investigation operations for a verified scope.
type Service struct {
	DB *tenant.DB
	// On is GEMINUS_INVESTIGATIONS; Organizations INVESTIGATION_ORGANIZATION_IDS.
	// Off, or for any other organization, every operation is ErrOff.
	On            bool
	Organizations []string
	// PlannerModels are the planners this deployment offers
	// (INVESTIGATION_PLANNER_MODELS; 422 at /planner_model for another).
	PlannerModels []string
	// AI is this deployment's proposal availability (cmd/api's, as for
	// proposals); the zero value refuses every investigation.
	AI proposals.Availability
	// InvestigatorApproved is whether the recorded approval lists the
	// investigator role (the fake: true); ApprovedPlanners the planner
	// models it allows for that role. 503 AI_POLICY_NOT_APPROVED otherwise.
	InvestigatorApproved bool
	ApprovedPlanners     []string
	// ProposalModels are the models a person may choose for the
	// investigation's proposals (proposals.Service.Models);
	// DefaultProposalModel the one stored when the request names none (the
	// deployment's model).
	ProposalModels       []string
	DefaultProposalModel string
	// Research is whether the deployment's provider may research for a
	// proposal (proposals.Service.Research).
	Research proposals.Availability
	// Policy is the investigation job's queue policy (jobs.DefaultPolicy
	// when zero); its attempts are store.MaxAttempts.
	Policy jobs.Policy

	cache comparisons
}

// The roles each operation requires (x-required-roles, planning/api-operations.json).
var (
	writeRoles = tenant.Writers
	readRoles  = tenant.Members
)

const (
	createOperation = "createInvestigation"
	stopOperation   = "stopInvestigation"
	maxLimit        = 100
)

func (s *Service) policy() jobs.Policy {
	if s.Policy == (jobs.Policy{}) {
		return jobs.DefaultPolicy
	}
	return s.Policy
}

// serves reports whether investigations run for org here.
func (s *Service) serves(org string) bool { return s.On && slices.Contains(s.Organizations, org) }

// ObjectiveChoice is investigation.schema.json#/$defs/InvestigationObjectiveChoice.
type ObjectiveChoice struct {
	MetricPath string `json:"metric_path"`
}

// CreateRequest is investigation.schema.json#/$defs/InvestigationRequest:
// entry A (from a request) or entry B (FromAnalysisID set).
type CreateRequest struct {
	BaseVersionID  string                    `json:"base_version_id,omitzero"`
	Mode           string                    `json:"mode,omitzero"`
	Intent         string                    `json:"intent,omitzero"`
	Context        *proposals.RequestContext `json:"context,omitzero"`
	OutputLocale   string                    `json:"output_locale,omitzero"`
	Research       bool                      `json:"research,omitzero"`
	Model          string                    `json:"model,omitzero"`
	PlannerModel   string                    `json:"planner_model,omitzero"`
	RunConfig      *simulation.RunConfig     `json:"run_config,omitzero"`
	BaselineName   string                    `json:"baseline_name,omitzero"`
	Objective      *ObjectiveChoice          `json:"objective,omitzero"`
	FromAnalysisID string                    `json:"from_analysis_id,omitzero"`
}

// Accepted is investigation.schema.json#/$defs/InvestigationAccepted.
type Accepted struct {
	InvestigationID string `json:"investigation_id"`
	Status          string `json:"status"`
}

// Objective is investigation.schema.json#/$defs/InvestigationObjective: the
// metric with the direction the server fixes for it.
type Objective struct {
	MetricPath string `json:"metric_path"`
	Better     string `json:"better"`
}

// DefaultObjective is the median lead time (H-056 §4).
const DefaultObjective = "/metrics/lead_time_minutes/p50"

// objectiveOf is the metric's objective: higher is better for throughput,
// lower for lead time.
func objectiveOf(metric string) Objective {
	if metric == "/metrics/throughput/mean" {
		return Objective{MetricPath: metric, Better: "higher"}
	}
	return Objective{MetricPath: metric, Better: "lower"}
}

// Create stores the investigation, its one job and its event, or replays
// the response of the earlier request with the same Idempotency-Key (same
// model and body: the same answer; anything else: idempotency.ErrConflict).
// It returns the HTTP status (202) and body.
func (s *Service) Create(ctx context.Context, sc tenant.Scope, requestID, key, modelID string, body []byte, req CreateRequest) (int, []byte, error) {
	if !s.serves(sc.OrganizationID) {
		return 0, nil, ErrOff
	}
	canonical, err := hashing.Canonical(body)
	if err != nil {
		return 0, nil, err
	}
	fingerprint, err := json.Marshal(map[string]any{"model_id": modelID, "body": jsontext.Value(canonical)}, json.Deterministic(true))
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
		a, err := s.start(ctx, tx, m, requestID, modelID, req)
		if err != nil {
			return err
		}
		data, err := json.Marshal(a)
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

// start is createInvestigation's body after the role and the key: the
// deployment's checks, the request and its base, the person's stale
// investigation ended, then the investigation, its job and its event.
func (s *Service) start(ctx context.Context, tx tenant.Tx, m tenant.Membership, requestID, modelID string, req CreateRequest) (Accepted, error) {
	if s.AI != proposals.Available {
		return Accepted{}, &proposals.UnavailableError{Availability: s.AI}
	}
	if !s.InvestigatorApproved {
		return Accepted{}, &NotApprovedError{}
	}
	planner := req.PlannerModel
	if planner == "" {
		planner = "muse-spark-1.3"
	}
	if !slices.Contains(s.PlannerModels, planner) {
		return Accepted{}, &RequestError{Issues: []model.ValidationIssue{model.NewIssue(CodePlannerNotOffered, "/planner_model",
			"this planner is not offered in this deployment", model.SeverityError)}}
	}
	if !slices.Contains(s.ApprovedPlanners, planner) {
		return Accepted{}, &NotApprovedError{Planner: true}
	}
	proposalModel := req.Model
	if proposalModel != "" && !slices.Contains(s.ProposalModels, proposalModel) {
		return Accepted{}, &proposals.UnavailableError{Availability: proposals.PolicyNotApproved, Model: true}
	}
	if proposalModel == "" {
		proposalModel = s.DefaultProposalModel
	}
	if !slices.Contains(proposals.ProposalModels, proposalModel) {
		return Accepted{}, &proposals.UnavailableError{Availability: proposals.PolicyNotApproved, Model: true}
	}
	if req.Research {
		if s.Research != proposals.Available {
			return Accepted{}, &proposals.UnavailableError{Availability: s.Research, Research: true}
		}
		if r, ok := ResearchReservation(proposalModel); !ok || r > Ceiling {
			return Accepted{}, &RequestError{Issues: []model.ValidationIssue{model.NewIssue(CodeResearchOverCeiling, "/research",
				"research with this model cannot fit the investigation's spending limit of USD 2.50", model.SeverityError)}}
		}
	}
	org := m.OrganizationID
	now, err := store.Now(ctx, tx)
	if err != nil {
		return Accepted{}, err
	}
	inv := store.Investigation{ID: ids.New("inv"), OrganizationID: org, ModelID: modelID, ProposalModel: proposalModel, PlannerModel: planner,
		PromptTemplateVersion: PromptTemplateVersion, Research: req.Research, CreatedBy: m.UserID}
	if req.FromAnalysisID != "" {
		err = s.fromAnalysis(ctx, tx, org, modelID, req, &inv)
	} else {
		err = s.fromRequest(ctx, tx, org, modelID, req, &inv)
	}
	if err != nil {
		return Accepted{}, err
	}
	// The person's running investigation whose clock ran out with no worker
	// to end it is ended here, in their own transaction (H-061 §13); one
	// still running is a 409.
	if running, ok, err := store.RunningOf(ctx, tx, org, m.UserID); err != nil {
		return Accepted{}, err
	} else if ok {
		ended, err := store.EndStale(ctx, tx, org, running.ID)
		if err != nil {
			return Accepted{}, err
		}
		if !ended {
			return Accepted{}, ErrRunning
		}
	}
	if inv, err = store.Insert(ctx, tx, inv); errors.Is(err, store.ErrRunning) {
		return Accepted{}, ErrRunning
	} else if errors.Is(err, store.ErrDailyLimit) {
		return Accepted{}, &DailyLimitError{RetryAfter: untilTomorrow(now)}
	} else if err != nil {
		return Accepted{}, err
	}
	if _, err := jobs.Enqueue(ctx, tx, jobs.New{ID: ids.New("job"), OrganizationID: org, Type: jobs.Investigation,
		Resource: jobs.Resource{InvestigationID: inv.ID}, Payload: map[string]any{"investigation_id": inv.ID, "request_id": requestID},
		MaxAttempts: store.MaxAttempts}, s.policy()); err != nil {
		return Accepted{}, err
	}
	// IDs, counts, flags and model names: never the intent (customer
	// prose, SECURITY §9).
	meta := map[string]any{"model_id": inv.ModelID, "base_version_id": inv.BaseVersionID, "mode": string(inv.Mode), "source": string(inv.Source),
		"output_locale": inv.OutputLocale, "requested_model": inv.ProposalModel, "planner_model": inv.PlannerModel}
	if inv.Research {
		meta["research"] = true
	}
	if inv.SourceAnalysisID != nil {
		meta["source_analysis_id"] = *inv.SourceAnalysisID
	}
	if inv.RunConfig != nil {
		var cfg simulation.RunConfig
		if err := json.Unmarshal(inv.RunConfig, &cfg); err != nil {
			return Accepted{}, err
		}
		meta["replications"] = cfg.Replications
	}
	if inv.BaselineRunID != nil {
		meta["baseline_reused"] = true
	}
	if err := auditlog.AppendUser(ctx, tx, org, m.UserID, auditlog.Event{Action: "investigation.create", TargetType: "investigation",
		TargetID: inv.ID, RequestID: requestID, Metadata: meta}); err != nil {
		return Accepted{}, err
	}
	return Accepted{InvestigationID: inv.ID, Status: string(store.Running)}, nil
}

// untilTomorrow is the seconds from now to the next UTC midnight (1 at least).
func untilTomorrow(now time.Time) int {
	now = now.UTC()
	next := time.Date(now.Year(), now.Month(), now.Day()+1, 0, 0, 0, 0, time.UTC)
	return max(1, int(next.Sub(now).Seconds()+0.999))
}

func issue(path, reason string) *RequestError {
	return &RequestError{Issues: []model.ValidationIssue{model.NewIssue(validate.ConstraintFailed, path, reason, model.SeverityError)}}
}

// base reads the model (locked, as every write about its versions takes it
// first, ADR-022) and its version id: the model or version of another
// organization, or none, is ErrNotFound; another model's version a 422 at
// path; a version that is not the latest ErrStaleBase.
func base(ctx context.Context, tx tenant.Tx, org, modelID, id, path string) (models.Version, error) {
	mdl, err := models.LockModel(ctx, tx, org, modelID)
	if errors.Is(err, models.ErrNotFound) {
		return models.Version{}, ErrNotFound
	}
	if err != nil {
		return models.Version{}, err
	}
	v, err := models.GetVersion(ctx, tx, org, id)
	if errors.Is(err, models.ErrNotFound) {
		return models.Version{}, ErrNotFound
	}
	if err != nil {
		return models.Version{}, err
	}
	if v.ModelID != mdl.ID {
		return models.Version{}, issue(path, "the version is not a version of this model")
	}
	if mdl.LatestVersionID != v.ID {
		return models.Version{}, &StaleBaseError{Path: path}
	}
	return v, nil
}

// outputLocale is the request's output locale (en when absent).
func outputLocale(raw string) (string, error) {
	loc, err := locale.Parse(raw)
	if err != nil {
		return "", issue("/output_locale", "output_locale is en or zh-Hant")
	}
	return string(loc), nil
}

// fromRequest fills inv from entry A: its base, mode, intent and, in
// scenario mode, its frozen run settings and objective.
func (s *Service) fromRequest(ctx context.Context, tx tenant.Tx, org, modelID string, req CreateRequest, inv *store.Investigation) error {
	var issues []model.ValidationIssue
	if strings.TrimSpace(req.Intent) == "" {
		issues = append(issues, model.NewIssue(validate.ConstraintFailed, "/intent", "the intent says nothing: it is only white space", model.SeverityError))
	}
	loc, err := outputLocale(req.OutputLocale)
	if err != nil {
		var re *RequestError
		errors.As(err, &re)
		issues = append(issues, re.Issues...)
	}
	if req.Mode == "scenario" && req.RunConfig != nil {
		issues = append(issues, runConfigIssues(*req.RunConfig)...)
	}
	if len(issues) > 0 {
		return &RequestError{Issues: issues}
	}
	v, err := base(ctx, tx, org, modelID, req.BaseVersionID, "/base_version_id")
	if err != nil {
		return err
	}
	ctxJSON := []byte("{}")
	if req.Context != nil {
		if ctxJSON, err = json.Marshal(req.Context, json.Deterministic(true)); err != nil {
			return err
		}
	}
	intent := req.Intent
	inv.BaseVersionID, inv.Mode, inv.Source, inv.Intent, inv.Context, inv.OutputLocale = v.ID, store.Mode(req.Mode), store.FromRequest, &intent, ctxJSON, loc
	inv.Caps = store.CapsFor(inv.Mode, false)
	if inv.Mode == store.Scenario {
		metric := DefaultObjective
		if req.Objective != nil {
			metric = req.Objective.MetricPath
		}
		name := req.BaselineName
		inv.BaselineName = &name
		return freeze(inv, *req.RunConfig, metric)
	}
	return nil
}

// freeze stores cfg and the objective's metric on a scenario-mode
// investigation, with the config's hash (the runs' config_hash).
func freeze(inv *store.Investigation, cfg simulation.RunConfig, metric string) error {
	data, err := json.Marshal(cfg, json.Deterministic(true))
	if err != nil {
		return err
	}
	h, err := hashing.Config(cfg)
	if err != nil {
		return err
	}
	obj, err := json.Marshal(objectiveOf(metric), json.Deterministic(true))
	if err != nil {
		return err
	}
	inv.RunConfig, inv.RunConfigHash, inv.Objective = data, &h, obj
	return nil
}

// runConfigIssues checks what the schema cannot: the warmup shorter than
// the horizon, both within the engine's range (as createSimulation checks
// them).
func runConfigIssues(c simulation.RunConfig) []model.ValidationIssue {
	horizon, herr := model.Ticks(c.Horizon.Value, c.Horizon.Unit)
	warmup, werr := model.Ticks(c.Warmup.Value, c.Warmup.Unit)
	switch {
	case herr != nil:
		return []model.ValidationIssue{model.NewIssue(validate.LimitExceeded, "/run_config/horizon", herr.Error(), model.SeverityError)}
	case werr != nil:
		return []model.ValidationIssue{model.NewIssue(validate.LimitExceeded, "/run_config/warmup", werr.Error(), model.SeverityError)}
	case warmup >= horizon:
		return []model.ValidationIssue{model.NewIssue(validate.LimitExceeded, "/run_config/warmup", "the warmup must be shorter than the horizon",
			model.SeverityError)}
	}
	return nil
}

// maxReusedDays bounds a reused baseline's horizon (H-060: runs of at most
// 28 days), in milliseconds.
const maxReusedMillis = 28 * 24 * 3600 * 1000

// fromAnalysis fills inv from entry B: a completed analysis of one of this
// model's runs against a baseline whose patch is [] and whose settings fit
// the caps; the baseline is reused (its settings frozen as the
// investigation's), and its version, the base, must be the latest.
func (s *Service) fromAnalysis(ctx context.Context, tx tenant.Tx, org, modelID string, req CreateRequest, inv *store.Investigation) error {
	loc, err := outputLocale(req.OutputLocale)
	if err != nil {
		return err
	}
	a, err := analysisstore.Get(ctx, tx, org, req.FromAnalysisID)
	if errors.Is(err, analysisstore.ErrNotFound) {
		return ErrNotFound
	}
	if err != nil {
		return err
	}
	const at = "/from_analysis_id"
	if a.Status != analysisstore.Completed || a.BaselineRunID == nil {
		return issue(at, "the analysis is not a completed analysis of a run against a baseline")
	}
	run, err := runstore.Get(ctx, tx, org, a.RunID)
	if err != nil {
		return err
	}
	if run.ModelID != modelID {
		return issue(at, "the analysis is not of a run of this model")
	}
	baseline, err := runstore.Get(ctx, tx, org, *a.BaselineRunID)
	if err != nil {
		return err
	}
	cfg := baseline.Config
	horizon, herr := model.Ticks(cfg.Horizon.Value, cfg.Horizon.Unit)
	if baseline.ScenarioID != nil || !emptyPatch(baseline.ScenarioPatch) || baseline.ModelID != modelID || herr != nil || horizon > maxReusedMillis ||
		cfg.Replications < 1 || cfg.Replications > 10 || cfg.TraceLevel != simulation.TraceNone && cfg.TraceLevel != simulation.TraceSampled {
		return issue(at, "the analysis's baseline cannot be reused: it has a patch, or settings over the caps (10 replications, 28 days, no full trace)")
	}
	if _, err := base(ctx, tx, org, modelID, baseline.ModelVersionID, at); err != nil {
		return err
	}
	metric := DefaultObjective
	if req.Objective != nil {
		metric = req.Objective.MetricPath
	}
	src, reused := a.ID, baseline.ID
	inv.BaseVersionID, inv.Mode, inv.Source, inv.SourceAnalysisID, inv.Context, inv.OutputLocale = baseline.ModelVersionID, store.Scenario,
		store.FromAnalysis, &src, []byte("{}"), loc
	inv.BaselineRunID, inv.Caps = &reused, store.CapsFor(store.Scenario, true)
	if err := freeze(inv, cfg, metric); err != nil {
		return err
	}
	if *inv.RunConfigHash != baseline.ConfigHash {
		return fmt.Errorf("investigations: the reused baseline %s's settings hash to %s, not its config hash", baseline.ID, *inv.RunConfigHash)
	}
	return nil
}

// emptyPatch reports whether a stored scenario patch is [].
func emptyPatch(p []byte) bool {
	var ops []jsontext.Value
	return json.Unmarshal(p, &ops) == nil && len(ops) == 0
}

// Get returns investigation id as getInvestigation shows it.
func (s *Service) Get(ctx context.Context, sc tenant.Scope, id string) (Investigation, error) {
	if !s.serves(sc.OrganizationID) {
		return Investigation{}, ErrOff
	}
	var out Investigation
	err := s.DB.Run(ctx, sc, tenant.ReadSnapshot, func(ctx context.Context, tx tenant.Tx, m tenant.Membership) error {
		if err := m.Require(readRoles...); err != nil {
			return err
		}
		inv, err := store.Get(ctx, tx, m.OrganizationID, id)
		if errors.Is(err, store.ErrNotFound) {
			return ErrNotFound
		}
		if err != nil {
			return err
		}
		out, err = s.view(ctx, tx, inv)
		return err
	})
	return out, err
}

// Page is investigation.schema.json#/$defs/Investigations.
type Page struct {
	Items      []Investigation `json:"items"`
	NextCursor *string         `json:"next_cursor"`
}

// List returns one page of the model's investigations, newest first.
func (s *Service) List(ctx context.Context, sc tenant.Scope, modelID, cursor string, limit int) (Page, error) {
	if !s.serves(sc.OrganizationID) {
		return Page{}, ErrOff
	}
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
	page := Page{Items: []Investigation{}}
	err := s.DB.Run(ctx, sc, tenant.ReadSnapshot, func(ctx context.Context, tx tenant.Tx, m tenant.Membership) error {
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
		for _, inv := range rows {
			v, err := s.view(ctx, tx, inv)
			if err != nil {
				return err
			}
			page.Items = append(page.Items, v)
		}
		return nil
	})
	return page, err
}

// Stop stops investigation id for the caller (its requester, or an owner),
// or replays the response of the earlier request with the same
// Idempotency-Key. In one transaction, in H-061 §6.3's lock order (the
// caller's membership, the agent job with SKIP LOCKED, the investigation
// FOR UPDATE, its runs, its child jobs): the queued agent job is cancelled,
// the investigation ends stopped_by_person in the caller's name, its queued
// and running runs are asked to cancel, its queued proposal jobs are
// cancelled (not its explanations': store.QueuedProposalJobs says why), and
// investigation.stop is written. It returns the HTTP status (200) and the
// investigation.
func (s *Service) Stop(ctx context.Context, sc tenant.Scope, requestID, key, id string, body []byte) (int, []byte, error) {
	if !s.serves(sc.OrganizationID) {
		return 0, nil, ErrOff
	}
	canonical, err := hashing.Canonical(body)
	if err != nil {
		return 0, nil, err
	}
	fingerprint, err := json.Marshal(map[string]any{"investigation_id": id, "body": jsontext.Value(canonical)}, json.Deterministic(true))
	if err != nil {
		return 0, nil, err
	}
	hash, err := hashing.Of(fingerprint)
	if err != nil {
		return 0, nil, err
	}
	k := idempotency.Key{Org: sc.OrganizationID, Actor: sc.UserID, Operation: stopOperation, Key: key}
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
		v, err := s.stop(ctx, tx, m, requestID, id)
		if err != nil {
			return err
		}
		data, err := json.Marshal(v)
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

func (s *Service) stop(ctx context.Context, tx tenant.Tx, m tenant.Membership, requestID, id string) (Investigation, error) {
	org := m.OrganizationID
	seen, err := store.Get(ctx, tx, org, id)
	if errors.Is(err, store.ErrNotFound) {
		return Investigation{}, ErrNotFound
	}
	if err != nil {
		return Investigation{}, err
	}
	byOwner := m.UserID != seen.CreatedBy
	if byOwner && m.Role != tenant.Owner {
		return Investigation{}, ErrNotTheRequester
	}
	if seen.Status != store.Running {
		return Investigation{}, ErrEnded
	}
	if _, err := store.CancelQueuedJob(ctx, tx, org, id); err != nil {
		return Investigation{}, err
	}
	inv, err := store.Lock(ctx, tx, org, id)
	if err != nil {
		return Investigation{}, err
	}
	if inv.Status != store.Running {
		return Investigation{}, ErrEnded
	}
	if err := store.Stop(ctx, tx, org, id, m.UserID); errors.Is(err, store.ErrEnded) {
		return Investigation{}, ErrEnded
	} else if err != nil {
		return Investigation{}, err
	}
	made, err := store.MadeBy(ctx, tx, org, id)
	if err != nil {
		return Investigation{}, err
	}
	for _, r := range made.Runs {
		if _, err := runstore.RequestCancellation(ctx, tx, org, r.ID); err != nil {
			return Investigation{}, err
		}
	}
	children, err := store.QueuedProposalJobs(ctx, tx, org, id)
	if err != nil {
		return Investigation{}, err
	}
	for _, j := range children {
		if _, err := jobs.CancelQueued(ctx, tx, org, j); err != nil {
			return Investigation{}, err
		}
	}
	calls, err := store.Calls(ctx, tx, org, id)
	if err != nil {
		return Investigation{}, err
	}
	meta := map[string]any{"reason": string(store.StoppedByPerson), "steps": len(calls)}
	if byOwner {
		meta["by_owner"] = true
	}
	if err := auditlog.AppendUser(ctx, tx, org, m.UserID, auditlog.Event{Action: "investigation.stop", TargetType: "investigation",
		TargetID: id, RequestID: requestID, Metadata: meta}); err != nil {
		return Investigation{}, err
	}
	if inv, err = store.Get(ctx, tx, org, id); err != nil {
		return Investigation{}, err
	}
	return s.view(ctx, tx, inv)
}
