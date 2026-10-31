package investigations

import (
	"context"
	"encoding/base64"
	"encoding/json/jsontext"
	"encoding/json/v2"
	"fmt"
	"regexp"
	"sync"
	"time"

	"github.com/kcl1104/geminus/internal/ai/investigator"
	"github.com/kcl1104/geminus/internal/analyses"
	"github.com/kcl1104/geminus/internal/model/hashing"
	"github.com/kcl1104/geminus/internal/simulation"
	analysisstore "github.com/kcl1104/geminus/internal/storage/analyses"
	store "github.com/kcl1104/geminus/internal/storage/investigations"
	proposalstore "github.com/kcl1104/geminus/internal/storage/proposals"
	runstore "github.com/kcl1104/geminus/internal/storage/runs"
	"github.com/kcl1104/geminus/internal/tenant"
)

// Investigation is investigation.schema.json#/$defs/Investigation.
type Investigation struct {
	ID               string                `json:"id"`
	OrganizationID   string                `json:"organization_id"`
	ModelID          string                `json:"model_id"`
	BaseVersionID    string                `json:"base_version_id"`
	Mode             string                `json:"mode"`
	Source           string                `json:"source"`
	SourceAnalysisID *string               `json:"source_analysis_id"`
	Objective        *Objective            `json:"objective"`
	OutputLocale     string                `json:"output_locale"`
	Research         bool                  `json:"research"`
	ProposalModel    string                `json:"proposal_model"`
	Planner          Planner               `json:"planner"`
	Status           string                `json:"status"`
	Reason           *string               `json:"reason"`
	Activity         Activity              `json:"activity"`
	Caps             store.Caps            `json:"caps"`
	Used             Used                  `json:"used"`
	RunConfig        *simulation.RunConfig `json:"run_config"`
	BaselineRunID    *string               `json:"baseline_run_id"`
	Steps            []Step                `json:"steps"`
	Candidates       []Candidate           `json:"candidates"`
	Best             *Best                 `json:"best"`
	AIOutcome        *string               `json:"ai_outcome"`
	ClosingLine      *ClosingLine          `json:"closing_line"`
	Stop             *StopRecord           `json:"stop"`
	DeadlineAt       string                `json:"deadline_at"`
	CreatedBy        string                `json:"created_by"`
	CreatedAt        string                `json:"created_at"`
	FinishedAt       *string               `json:"finished_at"`
}

// Planner is InvestigationPlanner: "Done by Muse Spark 1.3 (Meta) for you".
type Planner struct {
	Provider              string `json:"provider"`
	Model                 string `json:"model"`
	PromptTemplateVersion string `json:"prompt_template_version"`
}

// Activity is InvestigationActivity.
type Activity struct {
	State string `json:"state"`
}

// The activity states (InvestigationActivity).
const (
	Queued      = "queued"       // no worker has the investigation's job yet
	Planning    = "planning"     // the AI is choosing its next call
	CarryingOut = "carrying_out" // a recorded call waits for the API
	Waiting     = "waiting"      // for a proposal, runs or an explanation
	Ended       = "ended"
)

// Used is InvestigationUsed, counted from the investigation's rows.
type Used struct {
	WhatIfs      int `json:"what_ifs"`
	Runs         int `json:"runs"`
	Explanations int `json:"explanations"`
	Steps        int `json:"steps"`
}

// Effect is InvestigationStepEffect: what a carried-out call made.
type Effect struct {
	ProposalID    *string `json:"proposal_id,omitzero"`
	ScenarioID    *string `json:"scenario_id,omitzero"`
	BaselineRunID *string `json:"baseline_run_id,omitzero"`
	RunID         *string `json:"run_id,omitzero"`
	AnalysisID    *string `json:"analysis_id,omitzero"`
	DraftRevision *int64  `json:"draft_revision,omitzero"`
}

// Step is InvestigationStep: one row of the ledger.
type Step struct {
	Seq         int              `json:"seq"`
	Tool        string           `json:"tool"`
	Arguments   jsontext.Value   `json:"arguments"`
	Status      string           `json:"status"`
	Effect      *Effect          `json:"effect"`
	Error       *store.ToolError `json:"error"`
	Note        *string          `json:"note"`
	RequestedAt string           `json:"requested_at"`
	ResolvedAt  *string          `json:"resolved_at"`
	ObservedAt  *string          `json:"observed_at"`
}

// Candidate is InvestigationCandidate: one what-if (or the one edit), with
// the API's own comparison of its run against the baseline.
type Candidate struct {
	Index                int                         `json:"index"`
	Source               string                      `json:"source"`
	Idea                 string                      `json:"idea"`
	ProposalID           string                      `json:"proposal_id"`
	ProposalStatus       string                      `json:"proposal_status"`
	ScenarioID           *string                     `json:"scenario_id"`
	RunID                *string                     `json:"run_id"`
	AnalysisIDs          []string                    `json:"analysis_ids"`
	AppliedDraftRevision *int64                      `json:"applied_draft_revision"`
	Comparison           *investigator.RunComparison `json:"comparison"`

	// operations is the count of the proposal's reviewed changes (the
	// ranking's second key); not shown.
	operations int
}

// StopRecord is InvestigationStop.
type StopRecord struct {
	By string `json:"by"`
	At string `json:"at"`
}

func timestamp(t time.Time) string { return t.UTC().Format(time.RFC3339Nano) }

func optional(t *time.Time) *string {
	if t == nil {
		return nil
	}
	s := timestamp(*t)
	return &s
}

// The planner's provider: both planner models are Meta's Muse models
// (H-060); twin.investigations stores the model, not the backend.
const plannerProvider = "meta"

// view is inv as getInvestigation shows it, read in the caller's
// transaction: its ledger, what it made, the comparisons and the ranking.
func (s *Service) view(ctx context.Context, tx tenant.Tx, inv store.Investigation) (Investigation, error) {
	org := inv.OrganizationID
	calls, err := store.Calls(ctx, tx, org, inv.ID)
	if err != nil {
		return Investigation{}, err
	}
	made, err := store.MadeBy(ctx, tx, org, inv.ID)
	if err != nil {
		return Investigation{}, err
	}
	v := Investigation{ID: inv.ID, OrganizationID: org, ModelID: inv.ModelID, BaseVersionID: inv.BaseVersionID, Mode: string(inv.Mode),
		Source: string(inv.Source), SourceAnalysisID: inv.SourceAnalysisID, OutputLocale: inv.OutputLocale, Research: inv.Research,
		ProposalModel: inv.ProposalModel, Planner: Planner{Provider: plannerProvider, Model: inv.PlannerModel, PromptTemplateVersion: inv.PromptTemplateVersion},
		Status: string(inv.Status), Caps: inv.Caps, BaselineRunID: inv.BaselineRunID, Steps: []Step{}, Candidates: []Candidate{},
		AIOutcome: inv.AIOutcome, DeadlineAt: timestamp(inv.DeadlineAt), CreatedBy: inv.CreatedBy, CreatedAt: timestamp(inv.CreatedAt),
		FinishedAt: optional(inv.FinishedAt),
		Used:       Used{WhatIfs: len(made.Proposals), Runs: len(made.Runs), Explanations: len(made.Analyses), Steps: len(calls)}}
	if inv.Reason != nil {
		r := string(*inv.Reason)
		v.Reason = &r
		if *inv.Reason == store.StoppedByPerson && inv.StopRequestedBy != nil && inv.FinishedAt != nil {
			v.Stop = &StopRecord{By: *inv.StopRequestedBy, At: timestamp(*inv.FinishedAt)}
		}
	}
	if inv.Objective != nil {
		var o Objective
		if err := json.Unmarshal(inv.Objective, &o, json.RejectUnknownMembers(true)); err != nil {
			return Investigation{}, fmt.Errorf("investigation %s: stored objective: %w", inv.ID, err)
		}
		v.Objective = &o
	}
	if inv.RunConfig != nil {
		var cfg simulation.RunConfig
		if err := json.Unmarshal(inv.RunConfig, &cfg, json.RejectUnknownMembers(true)); err != nil {
			return Investigation{}, fmt.Errorf("investigation %s: stored run settings: %w", inv.ID, err)
		}
		v.RunConfig = &cfg
	}
	for _, c := range calls {
		st, err := step(c)
		if err != nil {
			return Investigation{}, err
		}
		v.Steps = append(v.Steps, st)
	}
	v.Activity = Activity{State: activity(inv, calls)}
	if v.Candidates, err = s.candidates(ctx, tx, inv, calls, made); err != nil {
		return Investigation{}, err
	}
	metric := ""
	if v.Objective != nil {
		metric = v.Objective.MetricPath
	}
	v.Best = best(v.Candidates, v.Objective)
	if inv.Status != store.Running {
		line := closingLine(inv.Mode, metric, v.Candidates, v.Best)
		v.ClosingLine = &line
	}
	return v, nil
}

// activity is what the investigation is doing, from its status, its last
// recorded call and its job.
func activity(inv store.Investigation, calls []store.Call) string {
	switch {
	case inv.Status != store.Running:
		return Ended
	case len(calls) > 0 && calls[len(calls)-1].Status == store.Pending:
		return CarryingOut
	case len(calls) > 0 && calls[len(calls)-1].ObservedAt == nil:
		return Waiting
	case inv.JobStatus == "queued": // no worker holds the job (yet, or again after a lost lease)
		return Queued
	}
	return Planning
}

// step is ledger row c as InvestigationStep.
func step(c store.Call) (Step, error) {
	st := Step{Seq: c.Seq, Tool: c.Tool, Arguments: jsontext.Value("null"), Status: string(c.Status), Error: c.Error, Note: c.Note,
		RequestedAt: timestamp(c.RequestedAt), ResolvedAt: optional(c.ResolvedAt), ObservedAt: optional(c.ObservedAt)}
	if c.Arguments != nil {
		args, err := hashing.Canonical(c.Arguments)
		if err != nil {
			return Step{}, fmt.Errorf("call %s/%d: stored arguments: %w", c.InvestigationID, c.Seq, err)
		}
		st.Arguments = args
	}
	e := Effect{ProposalID: c.Effect.ProposalID, ScenarioID: c.Effect.ScenarioID, BaselineRunID: c.Effect.BaselineRunID, RunID: c.Effect.RunID,
		AnalysisID: c.Effect.AnalysisID, DraftRevision: c.Effect.DraftRevision}
	if e != (Effect{}) {
		st.Effect = &e
	}
	return st, nil
}

// proposeArgs are the arguments of a call that proposes: propose_what_if's
// (or propose_model_edit's, without from_suggestion).
type proposeArgs struct {
	Idea           string                      `json:"idea"`
	FromSuggestion *investigator.SuggestionRef `json:"from_suggestion"`
}

// candidates are the investigation's proposals in what-if order, each with
// the call that asked for it, its scenario, run, explanations and the
// comparison of its run against the baseline once both completed.
func (s *Service) candidates(ctx context.Context, tx tenant.Tx, inv store.Investigation, calls []store.Call, made store.Made) ([]Candidate, error) {
	org := inv.OrganizationID
	out := []Candidate{}
	if len(made.Proposals) == 0 {
		return out, nil
	}
	asked := map[string]store.Call{}
	for _, c := range calls {
		if c.Status == store.Done && c.Effect.ProposalID != nil {
			asked[*c.Effect.ProposalID] = c
		}
	}
	runs := map[int]string{}
	for _, r := range made.Runs {
		runs[r.N] = r.ID
	}
	baselineID := inv.BaselineRunID
	if id, ok := runs[0]; ok {
		baselineID = &id
	}
	var baseline *runstore.Run
	if baselineID != nil {
		b, err := runstore.Get(ctx, tx, org, *baselineID)
		if err != nil {
			return nil, err
		}
		baseline = &b
	}
	explained := map[string][]string{}
	for _, a := range made.Analyses {
		rec, err := analysisstore.Get(ctx, tx, org, a.ID)
		if err != nil {
			return nil, err
		}
		explained[rec.RunID] = append(explained[rec.RunID], rec.ID)
	}
	for _, n := range made.Proposals {
		p, err := proposalstore.Get(ctx, tx, org, n.ID)
		if err != nil {
			return nil, err
		}
		call, ok := asked[p.ID]
		if !ok {
			return nil, fmt.Errorf("investigation %s: proposal %s has no call that asked for it", inv.ID, p.ID)
		}
		var args proposeArgs
		if err := json.Unmarshal(call.Arguments, &args); err != nil {
			return nil, fmt.Errorf("call %s/%d: stored arguments: %w", inv.ID, call.Seq, err)
		}
		c := Candidate{Index: n.N, Source: "idea", Idea: args.Idea, ProposalID: p.ID, ProposalStatus: string(p.Status), ScenarioID: p.ScenarioID,
			AnalysisIDs: []string{}, AppliedDraftRevision: p.AppliedDraftRevision}
		if args.FromSuggestion != nil {
			c.Source = "suggestion"
		}
		var ops []jsontext.Value
		if p.Patch != nil {
			if err := json.Unmarshal(p.Patch, &ops); err != nil {
				return nil, fmt.Errorf("proposal %s: stored patch: %w", p.ID, err)
			}
		}
		c.operations = len(ops)
		if id, ok := runs[n.N]; ok && n.N >= 1 {
			run := id
			c.RunID = &run
			if ids := explained[id]; ids != nil {
				c.AnalysisIDs = ids
			}
			if baseline != nil && baseline.Status == runstore.Completed {
				r, err := runstore.Get(ctx, tx, org, id)
				if err != nil {
					return nil, err
				}
				if r.Status == runstore.Completed {
					cmp, err := s.compare(ctx, tx, org, *baseline, r, inv.Objective)
					if err != nil {
						return nil, err
					}
					c.Comparison = &cmp
				}
			}
		}
		out = append(out, c)
	}
	return out, nil
}

// compare is the comparison of two completed runs as a candidate shows it,
// from the cache when the pair was compared before: completed runs and
// their results never change (immutable_record), so a pair's comparison
// never does either.
func (s *Service) compare(ctx context.Context, tx tenant.Tx, org string, baseline, scenario runstore.Run, objective []byte) (investigator.RunComparison, error) {
	var o Objective
	if objective != nil {
		if err := json.Unmarshal(objective, &o); err != nil {
			return investigator.RunComparison{}, err
		}
	}
	k := pair{org: org, baseline: baseline.ID, scenario: scenario.ID, metric: o.MetricPath}
	if c, ok := s.cache.get(k); ok {
		return c, nil
	}
	c, err := analyses.Comparison(ctx, tx, org, baseline, scenario)
	if err != nil {
		return investigator.RunComparison{}, err
	}
	out := investigator.ComparisonOf(c, o.MetricPath)
	s.cache.put(k, out)
	return out, nil
}

// pair keys the comparison cache: the organization, the two runs and the
// objective's metric.
type pair struct{ org, baseline, scenario, metric string }

// maxCached bounds the comparison cache; it is emptied when full.
const maxCached = 512

// comparisons caches what candidates show of compared run pairs.
type comparisons struct {
	mu sync.Mutex
	m  map[pair]investigator.RunComparison
}

func (c *comparisons) get(k pair) (investigator.RunComparison, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	v, ok := c.m[k]
	return v, ok
}

func (c *comparisons) put(k pair, v investigator.RunComparison) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.m == nil || len(c.m) >= maxCached {
		c.m = map[pair]investigator.RunComparison{}
	}
	c.m[k] = v
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

const cursorList = "investigations"

var investigationIDs = regexp.MustCompile(`^inv_[0-7][0-9A-HJKMNP-TV-Z]{25}$`)

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
	if err != nil || c.Version != 1 || c.Org != org || c.List != cursorList || c.Model != modelID || !investigationIDs.MatchString(c.ID) {
		return store.Position{}, ErrInvalidCursor
	}
	return store.Position{CreatedAt: t, ID: c.ID}, nil
}
