// Package generator is the model_proposal job handler (T-025, ADR-024):
// the adapter between the worker loop and internal/ai. For one claimed job
// it reads the generating proposal and its base version under the lease,
// builds the sanitized provider context (internal/ai/aicontext), asks the
// provider through the checked client (internal/ai/provider), has the
// server review the answer (internal/ai/review, then the dataset binding
// stage) and writes the outcome with the job's completion, under the lease
// (handler.Run.Finish): ready, needs_input or invalid. A provider failure
// is the job's failure: retried while the provider may recover and
// attempts remain, and when the job fails for good the proposal follows it
// to failed with the failure (migration 0012). Provider calls and long
// work run outside every transaction.
//
// The AI only suggests (AGENTS.md §2): the provider gets a sealed context
// and returns untrusted JSON; nothing it says is written but through the
// review, which binds no identity, validation or approval field from it.
// The package reads and writes the database, so it is an adapter, outside
// internal/ai (whose rules forbid storage and tenant); only cmd/worker
// links it (architecture rule job-handlers-only-in-the-worker).
package generator

import (
	"context"
	"encoding/json/v2"
	"errors"
	"fmt"
	"log/slog"
	"regexp"
	"strings"

	"github.com/kcl1104/geminus/internal/ai/aicontext"
	"github.com/kcl1104/geminus/internal/ai/idpool"
	"github.com/kcl1104/geminus/internal/ai/provider"
	"github.com/kcl1104/geminus/internal/ai/review"
	"github.com/kcl1104/geminus/internal/domainpack"
	"github.com/kcl1104/geminus/internal/model"
	"github.com/kcl1104/geminus/internal/model/validate"
	"github.com/kcl1104/geminus/internal/storage/auditlog"
	"github.com/kcl1104/geminus/internal/storage/datasets"
	"github.com/kcl1104/geminus/internal/storage/jobs"
	models "github.com/kcl1104/geminus/internal/storage/models"
	"github.com/kcl1104/geminus/internal/storage/objects"
	store "github.com/kcl1104/geminus/internal/storage/proposals"
	"github.com/kcl1104/geminus/internal/storage/snapshots"
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
	// deployment's mode: disabled, fake or an approved live adapter.
	Provider provider.LLMProvider
	// Budget bounds the context the handler builds; it is the client's.
	Budget provider.Budget
	Packs  PackLookup
	// Objects holds dataset snapshots (D-044); nil when object storage is
	// disabled.
	Objects objects.Store
	// Actor names the worker in audit events (WORKER_ID).
	Actor string
	Log   *slog.Logger
}

// Handler runs model_proposal jobs (handler.Handler).
type Handler struct{ cfg Config }

var _ handler.Handler = (*Handler)(nil)

var (
	actorRef    = regexp.MustCompile(`^[A-Za-z][A-Za-z0-9_.:-]{0,127}$`)
	proposalIDs = regexp.MustCompile(`^aip_[0-7][0-9A-HJKMNP-TV-Z]{25}$`)
	reqIDs      = regexp.MustCompile(`^req_[0-7][0-9A-HJKMNP-TV-Z]{25}$`)
)

// New checks cfg.
func New(cfg Config) (*Handler, error) {
	switch {
	case cfg.Provider == nil || cfg.Packs == nil || cfg.Log == nil:
		return nil, errors.New("generator: a provider, packs and a log are required")
	case !actorRef.MatchString(cfg.Actor):
		return nil, errors.New("generator: the worker's name must start with a letter and use only letters, digits, '_', '.', ':' and '-' (at most 128) to name it in audit events")
	}
	if cfg.Budget == (provider.Budget{}) {
		cfg.Budget = provider.DefaultBudget()
	}
	if err := cfg.Budget.Validate(); err != nil {
		return nil, fmt.Errorf("generator: %w", err)
	}
	return &Handler{cfg: cfg}, nil
}

// Failure codes of a model_proposal job (jobs.Failure.Code; the proposal
// keeps the failure, 0012, and the API shows it in the error envelope,
// internal/proposals). A provider failure is "AI_" and the provider
// client's code in upper case (AI_RATE_LIMITED, AI_TIMEOUT...).
const (
	// InputInvalid: the job does not name its proposal and request, or
	// the proposal is not generating, or what the server stored cannot be
	// built into a context (never retried).
	InputInvalid = "PROPOSAL_INPUT_INVALID"
	// DatasetStorageFailed: the dataset snapshots the candidate uses could
	// not be read.
	DatasetStorageFailed = "DATASET_STORAGE_FAILED"
	// OutputUnstorable: the database refused the generated row's content
	// (store.ErrUnstorable). The review refuses what it knows the database
	// cannot store, so this is a gap in the review; never retried, since a
	// retry would ask the provider again for the same answer (H-003 review
	// M2).
	OutputUnstorable = "PROPOSAL_OUTPUT_UNSTORABLE"
)

func failure(code, message string, retryable bool) error {
	return &jobs.Failure{Code: code, Message: message, Retryable: retryable}
}

// finishFailure is the job failure of an error writing the outcome: content
// the database refuses ends the attempt for good; any other error stays the
// worker's to classify.
func finishFailure(err error) error {
	if errors.Is(err, store.ErrUnstorable) {
		return failure(OutputUnstorable, "the database refused the generated proposal's content", false)
	}
	return err
}

// providerMessages are the fixed messages of provider failures: never the
// provider's text.
var providerMessages = map[provider.Code]string{
	provider.CodeNotConfigured:        "AI assistance is not configured in this deployment",
	provider.CodePolicyNotApproved:    "live AI is off until the owner approves its model, quota and data policy",
	provider.CodeInvalidRequest:       "the server built a request the AI provider client refused",
	provider.CodeContextTooLarge:      "the model is too large for the AI context budget",
	provider.CodeConcurrencyLimited:   "too many AI requests of this organization were in flight",
	provider.CodeTimeout:              "the AI provider did not answer in time",
	provider.CodeRateLimited:          "the AI provider kept refusing the request for its rate limit",
	provider.CodeUnavailable:          "the AI provider was unavailable",
	provider.CodeCredentialsExhausted: "every AI provider credential was refused",
	provider.CodeRejected:             "the AI provider rejected the request",
	provider.CodeRefused:              "the AI model declined to answer",
	provider.CodeTruncated:            "the AI answer did not fit the output budget",
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

// payload is the job's reference (internal/proposals Create).
type payload struct {
	ProposalID string `json:"proposal_id"`
	RequestID  string `json:"request_id"`
}

// started is what an attempt read under the lease.
type started struct {
	proposal store.Proposal
	version  models.Version
	keys     map[string]string
}

// Handle runs one attempt of a model_proposal job.
func (h *Handler) Handle(ctx context.Context, hr handler.Run) error {
	job := hr.Job()
	var p payload
	if err := json.Unmarshal(job.Payload, &p); err != nil || p.ProposalID != job.Resource.AIProposalID ||
		!proposalIDs.MatchString(p.ProposalID) || !reqIDs.MatchString(p.RequestID) {
		return failure(InputInvalid, "the job does not name its proposal and request", false)
	}
	org := job.OrganizationID
	audit := func(ctx context.Context, tx tenant.Tx, action string, meta map[string]any) error {
		meta["attempt"] = job.Attempt
		return auditlog.AppendWorker(ctx, tx, org, h.cfg.Actor, auditlog.Event{Action: action, TargetType: "ai_proposal",
			TargetID: p.ProposalID, RequestID: p.RequestID, Metadata: meta})
	}
	var st started
	if err := hr.Checkpoint(ctx, func(ctx context.Context, tx tenant.Tx) error {
		var err error
		st, err = h.start(ctx, tx, org, p.ProposalID)
		return err
	}); err != nil {
		return err
	}
	// failed ends an attempt that asked the provider and generated nothing:
	// what the call cost is audited all the same (best effort; the worker
	// records the failure either way).
	failed := func(err error, meta *provider.Metadata) error {
		var f *jobs.Failure
		if errors.As(err, &f) && ctx.Err() == nil && meta != nil {
			if aerr := hr.Checkpoint(ctx, func(ctx context.Context, tx tenant.Tx) error {
				m := auditMeta(*meta)
				m["code"], m["retryable"] = f.Code, f.Retryable
				return audit(ctx, tx, "proposal.attempt_failed", m)
			}); aerr != nil {
				h.cfg.Log.WarnContext(ctx, "recording a failed proposal attempt failed", "organization_id", org, "proposal_id", p.ProposalID, "error", aerr.Error())
			}
		}
		return err
	}
	outcome, meta, err := h.generate(ctx, org, st)
	if err != nil {
		return failed(err, meta)
	}
	var call provider.Metadata
	if meta != nil {
		call = *meta
	}
	err = hr.Finish(ctx, jobs.Completed, func(ctx context.Context, tx tenant.Tx) error {
		now, err := store.Lock(ctx, tx, org, p.ProposalID)
		if err != nil {
			return err
		}
		if now.Status != store.Generating {
			return failure(InputInvalid, "the proposal is "+string(now.Status)+", not generating", false)
		}
		g, err := generated(outcome, call, st.version.DatasetBindings)
		if err != nil {
			return err
		}
		if err := store.Generate(ctx, tx, org, p.ProposalID, g); err != nil {
			return finishFailure(err)
		}
		m := auditMeta(call)
		m["status"] = string(outcome.Status)
		if outcome.Validation != nil {
			m["issues"] = len(outcome.Validation.Issues)
		}
		if outcome.Status == review.Ready {
			m["patch_hash"], m["server_risk_level"], m["provider_risk_claim"], m["risk_level"] =
				outcome.PatchHash, string(outcome.ServerRisk), string(outcome.ProviderRisk), string(outcome.Risk)
		}
		return audit(ctx, tx, "proposal.generate", m)
	})
	if err != nil {
		return failed(err, meta)
	}
	return nil
}

// start checks the proposal as the attempt begins and reads its base.
func (h *Handler) start(ctx context.Context, tx tenant.Tx, org, id string) (started, error) {
	pr, err := store.Lock(ctx, tx, org, id)
	if errors.Is(err, store.ErrNotFound) {
		return started{}, failure(InputInvalid, "the job's proposal is not visible to its organization", false)
	}
	if err != nil {
		return started{}, err
	}
	if pr.Status != store.Generating {
		return started{}, failure(InputInvalid, "the proposal is "+string(pr.Status)+", not generating", false)
	}
	v, err := models.GetVersion(ctx, tx, org, pr.BaseVersionID)
	if errors.Is(err, models.ErrNotFound) {
		return started{}, failure(InputInvalid, "the proposal's base version is not visible to its organization", false)
	}
	if err != nil {
		return started{}, err
	}
	keys, err := snapshots.Keys(ctx, tx, org, v.DatasetBindings)
	if err != nil {
		return started{}, err
	}
	return started{proposal: pr, version: v, keys: keys}, nil
}

// generate builds the context, asks the provider and reviews the answer,
// outside any transaction. meta is the provider call's, nil when there was
// none.
func (h *Handler) generate(ctx context.Context, org string, st started) (review.Outcome, *provider.Metadata, error) {
	pr, v := st.proposal, st.version
	var ir model.ModelIR
	if err := model.Decode(v.IR, &ir); err != nil {
		return review.Outcome{}, nil, failure(InputInvalid, "the base version does not decode", false)
	}
	pack, err := h.cfg.Packs.Lookup(ir.Domain)
	if err != nil {
		return review.Outcome{}, nil, failure(InputInvalid, "the model's domain is not served by this worker", false)
	}
	var pool idpool.Pool
	if err := json.Unmarshal(pr.IDPool, &pool); err != nil {
		return review.Outcome{}, nil, failure(InputInvalid, "the stored ID pool does not decode", false)
	}
	mode := provider.Mode(pr.Mode)
	pc, err := aicontext.Proposal(aicontext.ProposalInput{
		Scope:             aicontext.Scope{OrganizationID: org, ModelID: pr.ModelID},
		Mode:              mode,
		Base:              aicontext.Version{OrganizationID: org, ModelID: v.ModelID, VersionID: v.ID, IR: &ir},
		Domain:            pack.PromptContext(),
		Intent:            pr.Intent,
		SelectedEntityIDs: pr.SelectedEntityIDs,
		Pool:              aicontext.Pool{OrganizationID: org, ProposalID: pr.ID, IDs: pool},
		OutputLocale:      pr.OutputLocale,
		Budget:            h.cfg.Budget,
	})
	if err != nil {
		if aicontext.CodeOf(err) == aicontext.TooLarge {
			return review.Outcome{}, nil, failure("AI_CONTEXT_TOO_LARGE", providerMessages[provider.CodeContextTooLarge], false)
		}
		h.cfg.Log.WarnContext(ctx, "building a proposal context failed", "organization_id", org, "proposal_id", pr.ID, "code", string(aicontext.CodeOf(err)))
		return review.Outcome{}, nil, failure(InputInvalid, "the proposal's stored request cannot be built into a context", false)
	}
	res, err := h.cfg.Provider.GenerateModelProposal(ctx, pc)
	meta := res.Metadata
	if err != nil {
		if ctx.Err() != nil {
			return review.Outcome{}, nil, err // the attempt is stopping: the worker records it, or nothing
		}
		var pe *provider.Error
		if errors.As(err, &pe) && pe.Code == provider.CodeInvalidOutput {
			// Not a proposal: invalid, with where the answer is wrong
			// and nothing of what it says (AI_SPEC §4).
			return review.InvalidOutput(pe.Issues), &meta, nil
		}
		return review.Outcome{}, &meta, providerFailure(err)
	}
	outcome, err := review.Review(review.Input{Mode: mode, Base: v.IR, Pool: pool, Pack: pack, Output: res.Output})
	if err != nil {
		h.cfg.Log.ErrorContext(ctx, "reviewing a proposal failed", "organization_id", org, "proposal_id", pr.ID, "error", err.Error())
		return review.Outcome{}, &meta, failure(InputInvalid, "the server could not review the answer against the stored base", false)
	}
	if outcome.Status != review.Ready {
		return outcome, &meta, nil
	}
	// The dataset binding stage: every empirical distribution of the
	// candidate is bound in the base version's manifest, and the stored
	// snapshots verify (D-006, D-033). It reads object storage only when
	// the manifest binds datasets.
	if len(v.DatasetBindings) > 0 && h.cfg.Objects == nil {
		return review.Outcome{}, &meta, failure(DatasetStorageFailed, "the base version binds datasets and this worker has no object storage", false)
	}
	resolution, err := datasets.Resolve(ctx, snapshots.Store{Keys: st.keys, Objects: h.cfg.Objects}, org, outcome.Candidate, v.DatasetBindings)
	if err != nil {
		if ctx.Err() != nil {
			return review.Outcome{}, nil, err
		}
		h.cfg.Log.WarnContext(ctx, "reading a proposal's dataset snapshots failed", "organization_id", org, "proposal_id", pr.ID, "error", err.Error())
		return review.Outcome{}, &meta, failure(DatasetStorageFailed, "the dataset snapshots could not be read", true)
	}
	return outcome.Refuse(withoutIR(resolution.Issues)), &meta, nil
}

// withoutIR rewrites the binding stage's /ir/... paths as paths into the
// candidate, as the other model issues are; manifest paths stay.
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

func optional(s string) *string {
	if s == "" {
		return nil
	}
	return &s
}

// generated is the row content of an outcome: what its state holds, and
// the call's metadata (no prompt, no answer, no key; AI_SPEC §10).
func generated(o review.Outcome, meta provider.Metadata, manifest []model.DatasetBinding) (store.Generated, error) {
	metadata, err := json.Marshal(meta)
	if err != nil {
		return store.Generated{}, err
	}
	g := store.Generated{Status: store.Status(o.Status), ProviderMetadata: metadata}
	if o.Validation != nil {
		if g.Validation, err = json.Marshal(o.Validation); err != nil {
			return store.Generated{}, err
		}
	}
	switch o.Status {
	case review.NeedsInput:
		g.Summary, g.Assumptions, g.Questions = optional(o.Summary), o.Assumptions, o.Questions
	case review.Ready:
		g.Summary, g.Assumptions, g.ExpectedEffects = optional(o.Summary), o.Assumptions, o.ExpectedEffects
		g.Patch, g.PatchHash, g.Confidence = o.Changes, optional(o.PatchHash), o.Confidence
		g.ServerRisk, g.ProviderRisk, g.Risk = optional(string(o.ServerRisk)), optional(string(o.ProviderRisk)), optional(string(o.Risk))
		g.DatasetBindings = manifest
	case review.Invalid:
	default:
		return store.Generated{}, fmt.Errorf("generator: an outcome is ready, needs_input or invalid, not %q", o.Status)
	}
	return g, nil
}

// auditMeta is what an audit event says of a provider call: the provider,
// model, credential label, template, schema, language, hashes, token
// counts and latency; never the prompt, the answer or a key (SECURITY §9).
func auditMeta(m provider.Metadata) map[string]any {
	out := map[string]any{"provider": m.Provider, "role": string(m.Role), "prompt_template_version": m.TemplateVersion,
		"schema": m.OutputSchema, "output_locale": m.Locale, "input_hash": m.InputHash,
		"input_tokens": m.Usage.InputTokens, "output_tokens": m.Usage.OutputTokens, "latency_ms": m.LatencyMillis}
	for k, v := range map[string]string{"model": m.Model, "credential_label": m.Credential, "output_hash": m.OutputHash} {
		if v != "" {
			out[k] = v
		}
	}
	if len(m.FailedOver) > 0 {
		out["failed_over_from"] = m.FailedOver
	}
	return out
}
