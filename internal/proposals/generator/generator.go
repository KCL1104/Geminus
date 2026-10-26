// Package generator is the model_proposal job handler (T-025, ADR-024):
// the adapter between the worker loop and internal/ai. For one claimed job
// it reads the generating proposal and its base version under the lease,
// builds the sanitized provider context (internal/ai/aicontext), reads and
// verifies the base's dataset snapshots, asks the provider through the
// checked client (internal/ai/provider), has the server review the answer
// (internal/ai/review, then the dataset binding stage over the snapshots
// already read) and writes the outcome with the job's completion, under
// the lease (handler.Run.Finish): ready, needs_input or invalid. A provider
// failure is the job's failure: retried while the provider may recover and
// attempts remain, and when the job fails for good the proposal follows it
// to failed with the failure (migration 0012). After the provider call only
// the provider's own recoverable failures, the worker's interruptions and
// a transient database error writing the outcome (INTERNAL) are retried
// (D-061 (c)): dataset storage is read before the call, and what the
// database refuses to store fails for good. Provider calls and long work
// run outside every transaction.
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
	"time"

	"github.com/kcl1104/geminus/internal/ai/aicontext"
	"github.com/kcl1104/geminus/internal/ai/idpool"
	"github.com/kcl1104/geminus/internal/ai/provider"
	"github.com/kcl1104/geminus/internal/ai/review"
	"github.com/kcl1104/geminus/internal/ai/spend"
	"github.com/kcl1104/geminus/internal/audit"
	"github.com/kcl1104/geminus/internal/domainpack"
	"github.com/kcl1104/geminus/internal/model"
	"github.com/kcl1104/geminus/internal/model/validate"
	"github.com/kcl1104/geminus/internal/observability/telemetry"
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
	// deployment's mode: disabled, fake or an approved live adapter. A
	// photo proposal (T-056) is sent to it with the image as the prompt's
	// image part, only when it takes images (provider.Client.AcceptsImages).
	Provider provider.LLMProvider
	// Segmenter finds the pack's concepts in a photo proposal's image
	// (T-056; T-055's provider.SegmentClient of the deployment's mode); nil
	// fails every photo proposal with AI_PROVIDER_NOT_CONFIGURED.
	Segmenter provider.Segmenter
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
	// DatasetStorageFailed: the base version's dataset snapshots could not
	// be read (retried: no provider call was made yet), or do not verify,
	// or this worker has no object storage (not retried). Raised before
	// the provider is asked, but for a fault of the server's own in the
	// binding stage after the call (not retried).
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
	// T-053: refused before any provider call.
	provider.CodeQuotaExceeded:    "a live AI spend limit was reached; no provider call was made",
	provider.CodeSpendUnavailable: "the live AI spend ledger could not reserve for the call; no provider call was made",
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
	// failed ends an attempt that asked the provider (or, for a photo,
	// the segmenter) and generated nothing: what the calls cost is audited
	// all the same (best effort; the worker records the failure either
	// way).
	failed := func(err error, gen generation) error {
		var f *jobs.Failure
		asked := gen.meta != nil || (gen.photo != nil && gen.photo.segmenter != nil)
		if errors.As(err, &f) && ctx.Err() == nil && asked {
			var call provider.Metadata
			if gen.meta != nil {
				call = *gen.meta
			}
			if aerr := hr.Checkpoint(ctx, func(ctx context.Context, tx tenant.Tx) error {
				m := photoAuditMeta(call, gen.photo)
				m["code"], m["retryable"] = f.Code, f.Retryable
				return audit(ctx, tx, "proposal.attempt_failed", m)
			}); aerr != nil {
				h.cfg.Log.WarnContext(ctx, "recording a failed proposal attempt failed", "organization_id", org, "proposal_id", p.ProposalID, "error", aerr.Error())
			}
		}
		return err
	}
	attr := provider.Attribution{TargetType: "ai_proposal", TargetID: p.ProposalID, RequestID: p.RequestID}
	var gen generation
	var err error
	if st.proposal.Image != nil {
		gen, err = h.generatePhoto(ctx, org, st, attr) // T-056: asked for with a photo
	} else {
		gen, err = h.generate(ctx, org, st, attr)
	}
	if err != nil {
		return failed(err, gen)
	}
	outcome, meta := gen.outcome, gen.meta
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
		g, err := generated(outcome, call, gen.manifest)
		if err != nil {
			return err
		}
		// T-056: a photo proposal's metadata names its image and the
		// segmenter's call too.
		if g.ProviderMetadata, err = photoMetadata(g.ProviderMetadata, gen); err != nil {
			return err
		}
		if err := store.Generate(ctx, tx, org, p.ProposalID, g); err != nil {
			return finishFailure(err)
		}
		m := photoAuditMeta(call, gen.photo)
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
		return failed(err, gen)
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

// generation is what an attempt produced between its two transactions.
type generation struct {
	outcome review.Outcome
	// meta is the provider call's; nil when there was none.
	meta *provider.Metadata
	// manifest is the base version's whole dataset manifest as the binding
	// stage verified it, in D-034 order: what a ready proposal binds, used
	// or not, as a version published from a draft does (TWIN_IR_SPEC §5.2,
	// D-062 (g)).
	manifest []model.DatasetBinding
	// photo is a photo proposal's image and segmenter call (T-056); nil
	// for a proposal asked for in words.
	photo *photoCall
}

// generate builds the context, reads and verifies the base's dataset
// snapshots, asks the provider, reviews the answer and runs the dataset
// binding stage on the candidate, outside any transaction. The snapshots
// are read before the provider is asked, so a storage failure never comes
// after a paid call (D-061 (c)); the binding stage after the call reads
// the copies, never storage.
func (h *Handler) generate(ctx context.Context, org string, st started, attr provider.Attribution) (generation, error) {
	pr, v := st.proposal, st.version
	var ir model.ModelIR
	if err := model.Decode(v.IR, &ir); err != nil {
		return generation{}, failure(InputInvalid, "the base version does not decode", false)
	}
	pack, err := h.cfg.Packs.Lookup(ir.Domain)
	if err != nil {
		return generation{}, failure(InputInvalid, "the model's domain is not served by this worker", false)
	}
	var pool idpool.Pool
	if err := json.Unmarshal(pr.IDPool, &pool); err != nil {
		return generation{}, failure(InputInvalid, "the stored ID pool does not decode", false)
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
			return generation{}, failure("AI_CONTEXT_TOO_LARGE", providerMessages[provider.CodeContextTooLarge], false)
		}
		h.cfg.Log.WarnContext(ctx, "building a proposal context failed", "organization_id", org, "proposal_id", pr.ID, "code", string(aicontext.CodeOf(err)))
		return generation{}, failure(InputInvalid, "the proposal's stored request cannot be built into a context", false)
	}
	snaps, err := h.preload(ctx, org, st)
	if err != nil {
		return generation{}, err
	}
	manifest, err := h.verify(ctx, org, pr.ID, snaps, &ir, v.DatasetBindings)
	if err != nil {
		return generation{}, err
	}

	pc.Attribution = attr // what the spend ledger charges the call to (T-053)
	res, err := h.cfg.Provider.GenerateModelProposal(ctx, pc)
	meta := res.Metadata
	h.logCall(ctx, meta, err)
	if err != nil {
		if ctx.Err() != nil {
			return generation{}, err // the attempt is stopping: the worker records it, or nothing
		}
		var pe *provider.Error
		if errors.As(err, &pe) && pe.Code == provider.CodeInvalidOutput {
			// Not a proposal: invalid, with where the answer is wrong
			// and nothing of what it says (AI_SPEC §4).
			return generation{outcome: review.InvalidOutput(pe.Issues), meta: &meta, manifest: manifest}, nil
		}
		return generation{meta: &meta}, providerFailure(err)
	}
	outcome, err := review.Review(review.Input{Mode: mode, Base: v.IR, Pool: pool, Pack: pack, Output: res.Output})
	if err != nil {
		h.cfg.Log.ErrorContext(ctx, "reviewing a proposal failed", "organization_id", org, "proposal_id", pr.ID, "error", err.Error())
		return generation{meta: &meta}, failure(InputInvalid, "the server could not review the answer against the stored base", false)
	}
	if outcome.Status != review.Ready {
		return generation{outcome: outcome, meta: &meta, manifest: manifest}, nil
	}
	// The dataset binding stage: every empirical distribution of the
	// candidate is bound in the base version's manifest, and the snapshots
	// verify (D-006, D-033), read from the copies made before the call.
	resolution, err := datasets.Resolve(ctx, snaps, org, outcome.Candidate, v.DatasetBindings)
	if err != nil {
		if ctx.Err() != nil {
			return generation{}, err
		}
		// No storage is read here, so this is the server's own fault; a
		// retry would ask the provider again, so the attempt fails for good.
		h.cfg.Log.ErrorContext(ctx, "the dataset binding stage failed after the provider answered", "organization_id", org, "proposal_id", pr.ID, "error", err.Error())
		return generation{meta: &meta}, failure(DatasetStorageFailed, "the dataset binding stage failed after the provider answered", false)
	}
	return generation{outcome: outcome.Refuse(withoutIR(resolution.Issues)), meta: &meta, manifest: manifest}, nil
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

// logCall is the AI provider call's operational metric (T-035, D-095):
// the provider, model and credential labels, the role, latency, token
// counts, the provider's request ID and the outcome code (OK, or AI_ and
// the client's code); never the prompt, the answer or a key. The job's
// correlation comes with ctx.
func (h *Handler) logCall(ctx context.Context, m provider.Metadata, err error) {
	code := "OK"
	switch c := provider.CodeOf(err); {
	case err == nil:
	case c == "":
		code = "ERROR" // not the client's error
	default:
		code = "AI_" + strings.ToUpper(string(c))
	}
	h.cfg.Log.InfoContext(ctx, "ai provider call ended", "provider", m.Provider, "model", m.Model, "credential_label", m.Credential,
		"role", string(m.Role), "latency_ms", m.LatencyMillis, "input_tokens", m.Usage.InputTokens, "output_tokens", m.Usage.OutputTokens,
		"provider_request_id", m.ProviderRequestID, "code", code, "cost_nano_usd", m.CostNanoUSD, "spend_reservation_id", m.SpendReservation)
	var pe *provider.Error
	if errors.As(err, &pe) && pe.Code == provider.CodeQuotaExceeded {
		h.cfg.Log.WarnContext(ctx, "a live AI spend limit refused a call", "role", string(m.Role), "limit", pe.Limit)
	}
	// T-053 schema follow-up: a call the provider answered with an error
	// status says which, by its HTTP status and error type (the adapter's
	// list of the API's types, or other), never the provider's message.
	if pe != nil && pe.Status != 0 {
		h.cfg.Log.WarnContext(ctx, "the ai provider answered the call with an error status", "role", string(m.Role),
			"credential_label", m.Credential, "provider_request_id", m.ProviderRequestID, "code", code,
			"provider_status", pe.Status, "provider_error_type", pe.ErrorType)
	}
	if m.SpendUnsettled {
		// The reservation stays counted at its bound (the safe side).
		h.cfg.Log.WarnContext(ctx, "settling a live AI spend reservation failed", "spend_reservation_id", m.SpendReservation,
			"cost_nano_usd", m.CostNanoUSD, "spend_unsettled", true)
	}
	// T-053 review round 1 (M2, M3, L1): what was billed is recorded even
	// above the reservation, and an answer that names no geo this build
	// knows is priced at the dearest; both mean an assumption of the
	// reservation's bound broke, so both are warned of.
	if m.SpendOverReservation {
		h.cfg.Log.WarnContext(ctx, "a live AI call cost more than its spend reservation; recorded at what it cost",
			"spend_reservation_id", m.SpendReservation, "cost_nano_usd", m.CostNanoUSD, "spend_over_reservation", true)
	}
	if m.InferenceGeo == spend.GeoUnknown {
		h.cfg.Log.WarnContext(ctx, "a live AI answer named no inference geo this build prices; priced at the dearest",
			"spend_reservation_id", m.SpendReservation, "inference_geo", m.InferenceGeo)
	}
	telemetry.Current().ProviderCall(ctx, time.Now().Add(-time.Duration(m.LatencyMillis)*time.Millisecond), m.Provider, m.Model, string(m.Role),
		code, m.Usage.InputTokens, m.Usage.OutputTokens)
}

// auditMeta is what an audit event says of a provider call: the provider,
// model, credential label, template, schema, language, hashes, token
// counts and latency; never the prompt, the answer or a key (SECURITY §9).
func auditMeta(m provider.Metadata) map[string]any {
	out := map[string]any{"provider": m.Provider, "role": string(m.Role), "prompt_template_version": m.TemplateVersion,
		"schema": m.OutputSchema, "output_locale": m.Locale, "input_hash": m.InputHash,
		"input_tokens": m.Usage.InputTokens, "output_tokens": m.Usage.OutputTokens, "latency_ms": m.LatencyMillis}
	// The provider chooses its model name and its request ID (its own
	// correlation, T-035, D-091 (d)): one that is no label the audit
	// catalogue accepts is left out, so it can never fail the write.
	for k, v := range map[string]string{"model": audit.Label(m.Model), "credential_label": m.Credential, "output_hash": m.OutputHash,
		"provider_request_id": audit.Label(m.ProviderRequestID)} {
		if v != "" {
			out[k] = v
		}
	}
	if len(m.FailedOver) > 0 {
		out["failed_over_from"] = m.FailedOver
	}
	// T-053: what a metered call cost and the reservation it was charged to.
	if m.SpendReservation != "" {
		out["cost_nano_usd"], out["spend_reservation_id"], out["spend_unsettled"] = m.CostNanoUSD, m.SpendReservation, m.SpendUnsettled
	}
	return out
}
