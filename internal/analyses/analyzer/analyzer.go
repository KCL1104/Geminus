// Package analyzer is the analysis job handler (T-030; AI_SPEC §9,
// ADR-024): the adapter between the worker loop and internal/ai. For one
// claimed job it reads the generating analysis, its runs, their results and
// the analyzed run's model version under the lease; computes the
// comparison with internal/results/compare (the one delta computation the
// API also serves, D-090); builds the sanitized analyst context
// (internal/ai/aicontext); asks the provider through the checked client;
// has the server check the answer against what it supplied
// (internal/ai/analyst); and writes the analysis with the job's completion,
// under the lease (handler.Run.Finish).
//
// An answer that is not grounded (a metric the results do not hold, a
// delta that is not the comparison's, a number nobody supplied) fails the
// job for good (ANALYSIS_UNGROUNDED), and the analysis follows it to failed
// (0018): nothing the answer says is stored. A provider failure is the
// job's failure, retried while the provider may recover. Provider calls and
// long work run outside every transaction.
//
// The AI only suggests (AGENTS.md §2): the analysis is text for a person,
// its experiments hypotheses a person may choose; nothing it says runs or
// changes anything. Only cmd/worker links the package (architecture rule
// job-handlers-only-in-the-worker).
package analyzer

import (
	"context"
	"encoding/json/v2"
	"errors"
	"fmt"
	"log/slog"
	"regexp"
	"strings"

	"github.com/kcl1104/geminus/internal/ai/aicontext"
	"github.com/kcl1104/geminus/internal/ai/analyst"
	"github.com/kcl1104/geminus/internal/ai/provider"
	"github.com/kcl1104/geminus/internal/audit"
	"github.com/kcl1104/geminus/internal/comparisons"
	"github.com/kcl1104/geminus/internal/domainpack"
	"github.com/kcl1104/geminus/internal/model"
	"github.com/kcl1104/geminus/internal/results/compare"
	"github.com/kcl1104/geminus/internal/simulation"
	store "github.com/kcl1104/geminus/internal/storage/analyses"
	"github.com/kcl1104/geminus/internal/storage/auditlog"
	"github.com/kcl1104/geminus/internal/storage/jobs"
	models "github.com/kcl1104/geminus/internal/storage/models"
	"github.com/kcl1104/geminus/internal/storage/runs"
	"github.com/kcl1104/geminus/internal/tenant"
	"github.com/kcl1104/geminus/internal/worker/handler"
)

// PackLookup finds a model's domain pack; *domainpack.Registry is one.
type PackLookup interface {
	Lookup(domain string) (domainpack.DomainPack, error)
}

// Config is the handler's setup.
type Config struct {
	// Provider is the checked provider client (provider.Client) of the
	// deployment's mode.
	Provider provider.LLMProvider
	// Budget bounds the context the handler builds; it is the client's.
	Budget provider.Budget
	Packs  PackLookup
	// Actor names the worker in audit events (WORKER_ID).
	Actor string
	Log   *slog.Logger
}

// Handler runs analysis jobs (handler.Handler).
type Handler struct{ cfg Config }

var _ handler.Handler = (*Handler)(nil)

var (
	actorRef    = regexp.MustCompile(`^[A-Za-z][A-Za-z0-9_.:-]{0,127}$`)
	analysisIDs = regexp.MustCompile(`^ana_[0-7][0-9A-HJKMNP-TV-Z]{25}$`)
	reqIDs      = regexp.MustCompile(`^req_[0-7][0-9A-HJKMNP-TV-Z]{25}$`)
)

// New checks cfg.
func New(cfg Config) (*Handler, error) {
	switch {
	case cfg.Provider == nil || cfg.Packs == nil || cfg.Log == nil:
		return nil, errors.New("analyzer: a provider, packs and a log are required")
	case !actorRef.MatchString(cfg.Actor):
		return nil, errors.New("analyzer: the worker's name must start with a letter and use only letters, digits, '_', '.', ':' and '-' (at most 128)")
	}
	if cfg.Budget == (provider.Budget{}) {
		cfg.Budget = provider.DefaultBudget()
	}
	if err := cfg.Budget.Validate(); err != nil {
		return nil, fmt.Errorf("analyzer: %w", err)
	}
	return &Handler{cfg: cfg}, nil
}

// Failure codes of an analysis job (jobs.Failure.Code; the analysis keeps
// the failure, 0018, and the API shows it in the error envelope with this
// code in details[0].code). A provider failure is "AI_" and the provider
// client's code in upper case.
const (
	// InputInvalid: the job does not name its analysis and request, or the
	// analysis is not generating, or what the server stored cannot be built
	// into a context (never retried).
	InputInvalid = "ANALYSIS_INPUT_INVALID"
	// Ungrounded: the answer cites what the results do not hold, or states
	// a value or number the server did not supply (never retried: a retry
	// would ask the provider again and pay again).
	Ungrounded = "ANALYSIS_UNGROUNDED"
	// NotComparable: the server does not compare the two runs (D-088): no
	// provider call is made (T-030 review r1 M4).
	NotComparable = "ANALYSIS_NOT_COMPARABLE"
	// OutputUnstorable: the database refused the checked analysis.
	OutputUnstorable = "ANALYSIS_OUTPUT_UNSTORABLE"
)

func failure(code, message string, retryable bool) error {
	return &jobs.Failure{Code: code, Message: message, Retryable: retryable}
}

// payload is the job's reference (internal/analyses Create).
type payload struct {
	AnalysisID string `json:"analysis_id"`
	RequestID  string `json:"request_id"`
}

// started is what an attempt read under the lease.
type started struct {
	analysis           store.Analysis
	scenario, baseline *runs.Run
	results            map[string][]byte // run ID → stored Result JSON
	version            models.Version
}

// Handle runs one attempt of an analysis job.
func (h *Handler) Handle(ctx context.Context, hr handler.Run) error {
	job := hr.Job()
	var p payload
	if err := json.Unmarshal(job.Payload, &p); err != nil || p.AnalysisID != job.Resource.AIAnalysisID ||
		!analysisIDs.MatchString(p.AnalysisID) || !reqIDs.MatchString(p.RequestID) {
		return failure(InputInvalid, "the job does not name its analysis and request", false)
	}
	org := job.OrganizationID
	appendAudit := func(ctx context.Context, tx tenant.Tx, action string, meta map[string]any) error {
		meta["attempt"] = job.Attempt
		return auditlog.AppendWorker(ctx, tx, org, h.cfg.Actor, auditlog.Event{Action: action, TargetType: "ai_analysis",
			TargetID: p.AnalysisID, RequestID: p.RequestID, Metadata: meta})
	}
	var st started
	if err := hr.Checkpoint(ctx, func(ctx context.Context, tx tenant.Tx) error {
		var err error
		st, err = h.start(ctx, tx, org, p.AnalysisID)
		return err
	}); err != nil {
		return err
	}
	attr := provider.Attribution{TargetType: "ai_analysis", TargetID: p.AnalysisID, RequestID: p.RequestID}
	out, meta, issues, err := h.analyze(ctx, org, st, attr)
	if err != nil {
		// What a call that produced nothing cost is audited all the same
		// (best effort; the worker records the failure either way).
		var f *jobs.Failure
		if errors.As(err, &f) && ctx.Err() == nil && meta != nil {
			if aerr := hr.Checkpoint(ctx, func(ctx context.Context, tx tenant.Tx) error {
				m := auditMeta(*meta)
				m["code"], m["retryable"], m["issues"] = f.Code, f.Retryable, issues
				return appendAudit(ctx, tx, "analysis.attempt_failed", m)
			}); aerr != nil {
				h.cfg.Log.WarnContext(ctx, "recording a failed analysis attempt failed", "organization_id", org, "analysis_id", p.AnalysisID, "error", aerr.Error())
			}
		}
		return err
	}
	analysis, err := json.Marshal(out)
	if err != nil {
		return err
	}
	metadata, err := json.Marshal(meta)
	if err != nil {
		return err
	}
	return hr.Finish(ctx, jobs.Completed, func(ctx context.Context, tx tenant.Tx) error {
		now, err := store.Lock(ctx, tx, org, p.AnalysisID)
		if err != nil {
			return err
		}
		if now.Status != store.Generating {
			return failure(InputInvalid, "the analysis is "+string(now.Status)+", not generating", false)
		}
		if err := store.Complete(ctx, tx, org, p.AnalysisID, analysis, metadata); err != nil {
			if errors.Is(err, store.ErrUnstorable) {
				return failure(OutputUnstorable, "the database refused the analysis's content", false)
			}
			return err
		}
		m := auditMeta(*meta)
		m["deltas"], m["conclusions"], m["experiments"] = len(out.Delta), len(out.Bottleneck), len(out.SuggestedNextExperiments)
		return appendAudit(ctx, tx, "analysis.generate", m)
	})
}

// start checks the analysis as the attempt begins and reads its runs,
// their results and the analyzed run's version.
func (h *Handler) start(ctx context.Context, tx tenant.Tx, org, id string) (started, error) {
	a, err := store.Lock(ctx, tx, org, id)
	if errors.Is(err, store.ErrNotFound) {
		return started{}, failure(InputInvalid, "the job's analysis is not visible to its organization", false)
	}
	if err != nil {
		return started{}, err
	}
	if a.Status != store.Generating {
		return started{}, failure(InputInvalid, "the analysis is "+string(a.Status)+", not generating", false)
	}
	st := started{analysis: a, results: map[string][]byte{}}
	read := func(id string) (*runs.Run, error) {
		r, err := runs.Get(ctx, tx, org, id)
		if errors.Is(err, runs.ErrNotFound) {
			return nil, failure(InputInvalid, "a run of the analysis is not visible to its organization", false)
		}
		if err != nil {
			return nil, err
		}
		if r.Status != runs.Completed {
			return nil, failure(InputInvalid, "a run of the analysis has no result", false)
		}
		res, err := runs.GetResult(ctx, tx, org, id)
		if errors.Is(err, runs.ErrNotFound) {
			return nil, failure(InputInvalid, "a completed run of the analysis has no stored result", false)
		}
		if err != nil {
			return nil, err
		}
		st.results[id] = res.Result
		return &r, nil
	}
	if st.scenario, err = read(a.RunID); err != nil {
		return started{}, err
	}
	if a.BaselineRunID != nil {
		if st.baseline, err = read(*a.BaselineRunID); err != nil {
			return started{}, err
		}
	}
	v, err := models.GetVersion(ctx, tx, org, st.scenario.ModelVersionID)
	if errors.Is(err, models.ErrNotFound) {
		return started{}, failure(InputInvalid, "the analyzed run's version is not visible to its organization", false)
	}
	if err != nil {
		return started{}, err
	}
	st.version = v
	return st, nil
}

// providerMessages are the fixed messages of provider failures: never the
// provider's text.
var providerMessages = map[provider.Code]string{
	provider.CodeNotConfigured:        "AI assistance is not configured in this deployment",
	provider.CodePolicyNotApproved:    "live AI is off until the owner approves its model, quota and data policy",
	provider.CodeInvalidRequest:       "the server built a request the AI provider client refused",
	provider.CodeContextTooLarge:      "the results are too large for the AI context budget",
	provider.CodeConcurrencyLimited:   "too many AI requests of this organization were in flight",
	provider.CodeTimeout:              "the AI provider did not answer in time",
	provider.CodeRateLimited:          "the AI provider kept refusing the request for its rate limit",
	provider.CodeUnavailable:          "the AI provider was unavailable",
	provider.CodeCredentialsExhausted: "every AI provider credential was refused",
	provider.CodeRejected:             "the AI provider rejected the request",
	provider.CodeRefused:              "the AI model declined to answer",
	provider.CodeTruncated:            "the AI answer did not fit the output budget",
	provider.CodeInvalidOutput:        "the AI answer is not an analysis the schema accepts",
	provider.CodeQuotaExceeded:        "a live AI spend limit was reached; no provider call was made",
	provider.CodeSpendUnavailable:     "the live AI spend ledger could not reserve for the call; no provider call was made",
}

// providerFailure is the job failure of a provider error.
func providerFailure(err error) error {
	var pe *provider.Error
	if !errors.As(err, &pe) {
		return err // not the client's: the worker records INTERNAL and retries
	}
	message, ok := providerMessages[pe.Code]
	if !ok {
		message = "the AI provider failed"
	}
	return failure("AI_"+strings.ToUpper(string(pe.Code)), message, pe.Retryable())
}

// result decodes a stored Result strictly.
func result(data []byte) (*simulation.Result, error) {
	var r simulation.Result
	if err := model.Decode(data, &r); err != nil {
		return nil, failure(InputInvalid, "a stored result does not decode", false)
	}
	return &r, nil
}

// analyze builds the context, asks the provider and checks the answer,
// outside any transaction. It returns the analysis to store and the call's
// metadata, or the failure and, after a call, its metadata and the number
// of issues an ungrounded answer had.
func (h *Handler) analyze(ctx context.Context, org string, st started, attr provider.Attribution) (provider.AnalysisOutput, *provider.Metadata, int, error) {
	var ir model.ModelIR
	if err := model.Decode(st.version.IR, &ir); err != nil {
		return provider.AnalysisOutput{}, nil, 0, failure(InputInvalid, "the analyzed run's version does not decode", false)
	}
	pack, err := h.cfg.Packs.Lookup(ir.Domain)
	if err != nil {
		return provider.AnalysisOutput{}, nil, 0, failure(InputInvalid, "the model's domain is not served by this worker", false)
	}
	scen, err := result(st.results[st.scenario.ID])
	if err != nil {
		return provider.AnalysisOutput{}, nil, 0, err
	}
	var patch model.Patch
	if err := json.Unmarshal(st.scenario.ScenarioPatch, &patch); err != nil {
		return provider.AnalysisOutput{}, nil, 0, failure(InputInvalid, "the analyzed run's patch does not decode", false)
	}
	in := aicontext.AnalysisInput{
		Scope:         aicontext.Scope{OrganizationID: org, ModelID: st.scenario.ModelID},
		Version:       aicontext.Version{OrganizationID: org, ModelID: st.version.ModelID, VersionID: st.version.ID, IR: &ir},
		Domain:        pack.PromptContext(),
		Scenario:      aicontext.RunResult{OrganizationID: org, ModelID: st.scenario.ModelID, RunID: st.scenario.ID, Result: scen},
		ScenarioPatch: patch,
		Config:        st.scenario.Config,
		OutputLocale:  st.analysis.OutputLocale,
		Budget:        h.cfg.Budget,
	}
	if st.analysis.Question != nil {
		in.Question = *st.analysis.Question
	}
	var base *simulation.Result
	var comparison *compare.Comparison
	if st.baseline != nil {
		if base, err = result(st.results[st.baseline.ID]); err != nil {
			return provider.AnalysisOutput{}, nil, 0, err
		}
		c, err := compare.Compare(comparisons.Input(*st.baseline, base), comparisons.Input(*st.scenario, scen))
		if err != nil {
			h.cfg.Log.ErrorContext(ctx, "comparing an analysis's runs failed", "organization_id", org, "analysis_id", st.analysis.ID, "error", err.Error())
			return provider.AnalysisOutput{}, nil, 0, failure(InputInvalid, "the analysis's runs cannot be compared", false)
		}
		if !c.Comparable {
			// The API refuses such a pair; a job that has one stops before the paid call.
			return provider.AnalysisOutput{}, nil, 0, failure(NotComparable, "the server does not compare the analysis's runs ("+strings.Join(c.IncomparableReasons, ", ")+")", false)
		}
		comparison = &c
		if in.Comparison, err = c.JSON(); err != nil {
			return provider.AnalysisOutput{}, nil, 0, err
		}
		in.Baseline = &aicontext.RunResult{OrganizationID: org, ModelID: st.baseline.ModelID, RunID: st.baseline.ID, Result: base}
	}
	ap, err := aicontext.Analysis(in)
	if err != nil {
		if aicontext.CodeOf(err) == aicontext.TooLarge {
			return provider.AnalysisOutput{}, nil, 0, failure("AI_CONTEXT_TOO_LARGE", providerMessages[provider.CodeContextTooLarge], false)
		}
		h.cfg.Log.WarnContext(ctx, "building an analysis context failed", "organization_id", org, "analysis_id", st.analysis.ID, "code", string(aicontext.CodeOf(err)))
		return provider.AnalysisOutput{}, nil, 0, failure(InputInvalid, "the analysis's stored request cannot be built into a context", false)
	}
	ap.Context.Attribution = attr // what the spend ledger charges the call to (T-053)
	res, err := h.cfg.Provider.AnalyzeSimulation(ctx, ap.Context)
	meta := res.Metadata
	h.logCall(ctx, meta, err)
	if err != nil {
		if ctx.Err() != nil {
			return provider.AnalysisOutput{}, nil, 0, err // the attempt is stopping: the worker records it, or nothing
		}
		return provider.AnalysisOutput{}, &meta, 0, providerFailure(err)
	}
	out, issues := analyst.Check(analyst.Input{Output: res.Output, Allowed: ap.Allowed, Supplied: ap.Supplied, Names: ap.Names,
		Scenario: scen, Baseline: base, Comparison: comparison})
	if len(issues) > 0 {
		// The first issue's code and path are the server's own words.
		return provider.AnalysisOutput{}, &meta, len(issues), failure(Ungrounded,
			fmt.Sprintf("the AI answer is not grounded in the results (%s at %s)", issues[0].Code, issues[0].Path), false)
	}
	return out, &meta, 0, nil
}

// logCall is the provider call's operational log line: labels, latency,
// tokens, cost and the outcome code; never the prompt, the answer or a key.
func (h *Handler) logCall(ctx context.Context, m provider.Metadata, err error) {
	code := "OK"
	switch c := provider.CodeOf(err); {
	case err == nil:
	case c == "":
		code = "ERROR"
	default:
		code = "AI_" + strings.ToUpper(string(c))
	}
	h.cfg.Log.InfoContext(ctx, "ai provider call ended", "provider", m.Provider, "model", m.Model, "credential_label", m.Credential,
		"role", string(m.Role), "latency_ms", m.LatencyMillis, "input_tokens", m.Usage.InputTokens, "output_tokens", m.Usage.OutputTokens,
		"provider_request_id", m.ProviderRequestID, "code", code, "cost_nano_usd", m.CostNanoUSD, "spend_reservation_id", m.SpendReservation)
	if m.SpendUnsettled {
		h.cfg.Log.WarnContext(ctx, "settling a live AI spend reservation failed", "spend_reservation_id", m.SpendReservation, "spend_unsettled", true)
	}
}

// auditMeta is what an audit event says of a provider call (as for
// proposals): labels, hashes, token counts, latency and cost.
func auditMeta(m provider.Metadata) map[string]any {
	out := map[string]any{"provider": m.Provider, "role": string(m.Role), "prompt_template_version": m.TemplateVersion,
		"schema": m.OutputSchema, "output_locale": m.Locale, "input_hash": m.InputHash,
		"input_tokens": m.Usage.InputTokens, "output_tokens": m.Usage.OutputTokens, "latency_ms": m.LatencyMillis}
	for k, v := range map[string]string{"model": audit.Label(m.Model), "credential_label": m.Credential, "output_hash": m.OutputHash,
		"provider_request_id": audit.Label(m.ProviderRequestID)} {
		if v != "" {
			out[k] = v
		}
	}
	if len(m.FailedOver) > 0 {
		out["failed_over_from"] = m.FailedOver
	}
	if m.SpendReservation != "" {
		out["cost_nano_usd"], out["spend_reservation_id"], out["spend_unsettled"] = m.CostNanoUSD, m.SpendReservation, m.SpendUnsettled
	}
	return out
}
