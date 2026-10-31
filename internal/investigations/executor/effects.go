package executor

import (
	"context"
	"encoding/json/jsontext"
	"encoding/json/v2"
	"errors"
	"fmt"

	"github.com/kcl1104/geminus/internal/ai/investigator"
	"github.com/kcl1104/geminus/internal/analyses"
	"github.com/kcl1104/geminus/internal/ids"
	"github.com/kcl1104/geminus/internal/proposals"
	"github.com/kcl1104/geminus/internal/proposals/effect"
	"github.com/kcl1104/geminus/internal/runs"
	"github.com/kcl1104/geminus/internal/simulation"
	analysisstore "github.com/kcl1104/geminus/internal/storage/analyses"
	store "github.com/kcl1104/geminus/internal/storage/investigations"
	runstore "github.com/kcl1104/geminus/internal/storage/runs"
	scenariostore "github.com/kcl1104/geminus/internal/storage/scenarios"
	"github.com/kcl1104/geminus/internal/tenant"
)

// refused is a call's typed refusal (H-061 §7): nothing it asked for took
// effect, and the AI is shown {code, path}.
func refused(code investigator.Code, path string) store.Resolution {
	return store.Resolution{Status: store.Refused, Error: &store.ToolError{Code: string(code), Path: path}}
}

// failedWith is a call recorded failed at once: what it needs is missing
// in this deployment, not something a retry fixes.
func failedWith(code investigator.Code) store.Resolution {
	return store.Resolution{Status: store.Failed, Error: &store.ToolError{Code: string(code), Path: ""}}
}

// atCap reports whether err is the database refusing a row past an
// investigation's cap (the backstop of the counts each effect checks
// first, for a store that keeps the database's error): LIMIT_REACHED.
func atCap(err error) bool { return store.AtCap(err) }

func isImmutable(err error) bool { return store.Immutable(err) }

// effect carries out call of inv in tx (H-061 §6.2 steps 4-6, §6.4): the
// arguments decoded strictly a second time (the worker's decoder, the
// schema, NFC) and their hash checked against the one recorded, the
// investigation's deadline, then the tool's effect in a SAVEPOINT. A typed
// refusal rolls back to the savepoint and is the call's resolution; any
// other error is the transaction's.
func (e *Executor) effect(ctx context.Context, tx tenant.Tx, m tenant.Membership, inv store.Investigation, call store.Call) (store.Resolution, error) {
	decoded, err := investigator.Decode(investigator.Mode(inv.Mode), call.Tool, call.Arguments)
	var r *investigator.Refusal
	if errors.As(err, &r) {
		return refused(r.Code, r.Path), nil
	}
	if err != nil {
		return store.Resolution{}, err
	}
	if call.ArgumentsHash == nil || decoded.Hash != *call.ArgumentsHash {
		return store.Resolution{}, fmt.Errorf("call %s/%d: the recorded arguments do not hash to the recorded hash", inv.ID, call.Seq)
	}
	now, err := store.Now(ctx, tx)
	if err != nil {
		return store.Resolution{}, err
	}
	if !now.Before(inv.DeadlineAt) {
		// The 20 minutes are over (the worker ends the investigation with
		// time_limit): the call is closed, nothing more is made.
		return refused(investigator.LimitReached, ""), nil
	}
	sp, err := tx.Begin(ctx)
	if err != nil {
		return store.Resolution{}, err
	}
	res, err := e.apply(ctx, sp, m, inv, decoded.Arguments)
	var refusal *effect.Refusal
	switch {
	case err == nil && res.Status == store.Done:
		return res, sp.Commit(ctx)
	case err == nil:
	case errors.As(err, &refusal):
		res = refused(refusal.Code, refusal.Path)
	case atCap(err):
		res = refused(investigator.LimitReached, "")
	case errors.Is(err, effect.ErrDatasetsUnavailable), errors.Is(err, runs.ErrStorageUnavailable):
		res = failedWith(investigator.DatasetStorageFailed)
	default:
		_ = sp.Rollback(ctx)
		return store.Resolution{}, err
	}
	if err := sp.Rollback(ctx); err != nil {
		return store.Resolution{}, err
	}
	return res, nil
}

// apply is the tool's effect for args in sp (H-061 §6.4).
func (e *Executor) apply(ctx context.Context, sp tenant.Tx, m tenant.Membership, inv store.Investigation, args investigator.Arguments) (store.Resolution, error) {
	requestID := ids.New("req")
	switch a := args.(type) {
	case investigator.ProposeWhatIfArguments:
		return e.propose(ctx, sp, m, inv, requestID, a.Idea, a.FromSuggestion)
	case investigator.ProposeModelEditArguments:
		return e.propose(ctx, sp, m, inv, requestID, a.Idea, nil)
	case investigator.SaveScenarioArguments:
		saved, err := e.cfg.Effects.SaveScenario(ctx, sp, m, requestID, a.ProposalID)
		if err != nil {
			return store.Resolution{}, err
		}
		return store.Resolution{Status: store.Done, Effect: store.Effect{ScenarioID: &saved.ScenarioID}}, nil
	case investigator.WriteToDraftArguments:
		written, err := e.cfg.Effects.WriteDraft(ctx, sp, m, requestID, a.ProposalID)
		if err != nil {
			return store.Resolution{}, err
		}
		return store.Resolution{Status: store.Done, Effect: store.Effect{DraftRevision: &written.DraftRevision}}, nil
	case investigator.RunWhatIfArguments:
		return e.run(ctx, sp, m, inv, requestID, a.ScenarioID)
	case investigator.ExplainResultsArguments:
		return e.explain(ctx, sp, m, inv, requestID, a)
	}
	// finish is recorded done by the worker and never pending.
	return refused(investigator.ModeMismatch, ""), nil
}

// propose queues the investigation's next proposal (propose_what_if,
// propose_model_edit) through proposals.Queue: on its base, in its mode,
// with the person's proposal model; research only for the first, and only
// when the person asked for it; the intent the AI's idea, or the server's
// "<name>: <hypothesis>" of one of the analysis's suggestions.
func (e *Executor) propose(ctx context.Context, sp tenant.Tx, m tenant.Membership, inv store.Investigation, requestID, idea string,
	from *investigator.SuggestionRef) (store.Resolution, error) {
	made, err := store.MadeBy(ctx, sp, inv.OrganizationID, inv.ID)
	if err != nil {
		return store.Resolution{}, err
	}
	if len(made.Proposals) >= inv.Caps.WhatIfs {
		return refused(investigator.LimitReached, ""), nil
	}
	intent, source := idea, proposals.FromIntent
	if from != nil {
		s, refusal, err := suggestion(ctx, sp, inv, *from)
		if err != nil || refusal != nil {
			if refusal != nil {
				return *refusal, nil
			}
			return store.Resolution{}, err
		}
		intent, source = s, proposals.FromSuggestion
	}
	req := proposals.CreateRequest{BaseVersionID: inv.BaseVersionID, Mode: string(inv.Mode), Intent: intent, OutputLocale: inv.OutputLocale,
		Research: inv.Research && len(made.Proposals) == 0, Model: inv.ProposalModel}
	var rc proposals.RequestContext
	if err := json.Unmarshal(inv.Context, &rc, json.RejectUnknownMembers(true)); err != nil {
		return store.Resolution{}, fmt.Errorf("investigation %s: stored context: %w", inv.ID, err)
	}
	if len(rc.SelectedEntityIDs) > 0 {
		req.Context = &rc
	}
	a, err := e.cfg.Proposals.Queue(ctx, sp, m, requestID, inv.ModelID, req, source)
	var re *proposals.RequestError
	var ue *proposals.UnavailableError
	switch {
	case errors.Is(err, proposals.ErrStaleBase):
		return refused(investigator.BaseMoved, ""), nil
	case errors.As(err, &re):
		return refused(investigator.InvalidArguments, "/idea"), nil
	case errors.As(err, &ue):
		return failedWith(investigator.ServiceUnavailable), nil
	case err != nil:
		return store.Resolution{}, err
	}
	return store.Resolution{Status: store.Done, Effect: store.Effect{ProposalID: &a.ProposalID}}, nil
}

// suggestion is the intent of an analysis's suggested experiment ref:
// "<name>: <hypothesis>", as the person's own "Propose as a scenario"
// writes it (H-056 §10). The analysis must be this investigation's, or the
// one it started from, and completed; ref.Index one of its suggestions.
func suggestion(ctx context.Context, sp tenant.Tx, inv store.Investigation, ref investigator.SuggestionRef) (string, *store.Resolution, error) {
	a, err := analysisstore.Get(ctx, sp, inv.OrganizationID, ref.AnalysisID)
	ours := err == nil && (a.InvestigationID != nil && *a.InvestigationID == inv.ID ||
		inv.SourceAnalysisID != nil && *inv.SourceAnalysisID == a.ID)
	if errors.Is(err, analysisstore.ErrNotFound) || err == nil && (!ours || a.Status != analysisstore.Completed) {
		r := refused(investigator.NotInInvestigation, "/from_suggestion/analysis_id")
		return "", &r, nil
	}
	if err != nil {
		return "", nil, err
	}
	var out struct {
		Experiments []struct {
			Name       string `json:"name"`
			Hypothesis string `json:"hypothesis"`
		} `json:"suggested_next_experiments"`
	}
	if err := json.Unmarshal(a.Analysis, &out); err != nil {
		return "", nil, fmt.Errorf("analysis %s: stored analysis: %w", a.ID, err)
	}
	if ref.Index < 0 || ref.Index >= len(out.Experiments) {
		r := refused(investigator.NotInInvestigation, "/from_suggestion/index")
		return "", &r, nil
	}
	x := out.Experiments[ref.Index]
	return x.Name + ": " + x.Hypothesis, nil, nil
}

// run is run_what_if's effect: the investigation's saved scenario of
// what-if k runs once (slot k) with the frozen run settings, against the
// baseline: the person's reused one, the investigation's slot 0, or a new
// slot 0 run of the base with patch [] (started once, here).
func (e *Executor) run(ctx context.Context, sp tenant.Tx, m tenant.Membership, inv store.Investigation, requestID, scenarioID string) (store.Resolution, error) {
	const at = "/scenario_id"
	org := inv.OrganizationID
	sc, err := scenariostore.Get(ctx, sp, org, scenarioID)
	if errors.Is(err, scenariostore.ErrNotFound) || err == nil && (sc.AI == nil || sc.AI.InvestigationID != inv.ID || sc.SourceProposalID == nil) {
		return refused(investigator.NotInInvestigation, at), nil
	}
	if err != nil {
		return store.Resolution{}, err
	}
	made, err := store.MadeBy(ctx, sp, org, inv.ID)
	if err != nil {
		return store.Resolution{}, err
	}
	slot := -1
	for _, p := range made.Proposals {
		if p.ID == *sc.SourceProposalID {
			slot = p.N
		}
	}
	if slot < 1 {
		return refused(investigator.NotInInvestigation, at), nil
	}
	var baseline string
	for _, r := range made.Runs {
		if r.N == slot {
			return refused(investigator.AlreadyRun, at), nil
		}
		if r.N == 0 {
			baseline = r.ID
		}
	}
	if inv.RunConfig == nil {
		return refused(investigator.ModeMismatch, ""), nil
	}
	starts := 1
	if baseline == "" && inv.BaselineRunID == nil {
		starts = 2
	}
	if len(made.Runs)+starts > inv.Caps.Runs {
		return refused(investigator.LimitReached, ""), nil
	}
	var cfg simulation.RunConfig
	if err := json.Unmarshal(inv.RunConfig, &cfg, json.RejectUnknownMembers(true)); err != nil {
		return store.Resolution{}, fmt.Errorf("investigation %s: stored run settings: %w", inv.ID, err)
	}
	queue := func(req runs.CreateRequest) (string, *store.Resolution, error) {
		a, err := e.cfg.Runs.Queue(ctx, sp, m, requestID, req)
		var re *runs.RequestError
		var ie *runs.InvalidError
		switch {
		case errors.Is(err, runs.ErrNotFound):
			r := refused(investigator.NotInInvestigation, at)
			return "", &r, nil
		case errors.As(err, &re), errors.As(err, &ie):
			// The server's checks refuse to run it as it stands now.
			r := refused(investigator.RecheckFailed, at)
			return "", &r, nil
		case err != nil:
			return "", nil, err
		}
		return a.ID, nil, nil
	}
	if inv.BaselineRunID != nil {
		baseline = *inv.BaselineRunID
	}
	if baseline == "" {
		name := "Baseline"
		if inv.BaselineName != nil {
			name = *inv.BaselineName
		}
		id, refusal, err := queue(runs.CreateRequest{ModelVersionID: inv.BaseVersionID, Name: name, ScenarioPatch: jsontext.Value("[]"), Config: cfg})
		if err != nil || refusal != nil {
			return derefOr(refusal), err
		}
		baseline = id
	}
	id, refusal, err := queue(runs.CreateRequest{ModelVersionID: inv.BaseVersionID, Name: sc.Name, ScenarioID: sc.ID, Config: cfg})
	if err != nil || refusal != nil {
		return derefOr(refusal), err
	}
	return store.Resolution{Status: store.Done, Effect: store.Effect{BaselineRunID: &baseline, RunID: &id}}, nil
}

func derefOr(r *store.Resolution) store.Resolution {
	if r == nil {
		return store.Resolution{}
	}
	return *r
}

// explain is explain_results' effect: an explanation of one of the
// investigation's what-if runs against its baseline, both completed,
// through analyses.Queue (the analyst on the deployment's model).
func (e *Executor) explain(ctx context.Context, sp tenant.Tx, m tenant.Membership, inv store.Investigation, requestID string,
	a investigator.ExplainResultsArguments) (store.Resolution, error) {
	const at = "/run_id"
	org := inv.OrganizationID
	r, err := runstore.Get(ctx, sp, org, a.RunID)
	if errors.Is(err, runstore.ErrNotFound) || err == nil && (r.AI == nil || r.AI.InvestigationID != inv.ID || r.AI.Slot < 1) {
		return refused(investigator.NotInInvestigation, at), nil
	}
	if err != nil {
		return store.Resolution{}, err
	}
	baseline := ""
	if inv.BaselineRunID != nil {
		baseline = *inv.BaselineRunID
	} else {
		made, err := store.MadeBy(ctx, sp, org, inv.ID)
		if err != nil {
			return store.Resolution{}, err
		}
		for _, x := range made.Runs {
			if x.N == 0 {
				baseline = x.ID
			}
		}
	}
	if r.Status != runstore.Completed || baseline == "" {
		return refused(investigator.RunNotCompleted, at), nil
	}
	if made, err := store.MadeBy(ctx, sp, org, inv.ID); err != nil {
		return store.Resolution{}, err
	} else if len(made.Analyses) >= inv.Caps.Explanations {
		return refused(investigator.LimitReached, ""), nil
	}
	q := ""
	if a.Question != nil {
		q = *a.Question
	}
	acc, err := e.cfg.Analyses.Queue(ctx, sp, m, requestID, a.RunID, analyses.CreateRequest{BaselineRunID: baseline, Question: q,
		OutputLocale: inv.OutputLocale})
	var run *analyses.RunError
	var re *analyses.RequestError
	var ue *proposals.UnavailableError
	switch {
	case errors.As(err, &run) && errors.Is(run.Err, analyses.ErrNotReady):
		return refused(investigator.RunNotCompleted, at), nil
	case errors.As(err, &run):
		return refused(investigator.NotInInvestigation, at), nil
	case errors.As(err, &re):
		// The pair cannot be explained as it stands (not comparable, a
		// question the analyst refuses).
		return refused(investigator.InvalidArguments, at), nil
	case errors.As(err, &ue):
		return failedWith(investigator.ServiceUnavailable), nil
	case err != nil:
		return store.Resolution{}, err
	}
	return store.Resolution{Status: store.Done, Effect: store.Effect{AnalysisID: &acc.ID}}, nil
}
