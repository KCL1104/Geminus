// Package aievals runs the AI eval corpus against a live provider (make
// ai-eval-live, cmd/ai-eval-live; T-053, T-038): every modeler case of
// testdata/ai-evals through the context builder, a metered live client
// and the server's review, as the model_proposal job runs them, without
// the database. It scores the model's own answers (TEST_PLAN §3: live
// results are kept apart from the fake ones) and the zero-tolerance gates
// a live answer can break; it keeps no answer text, prompt or key: a
// case's report is its status, codes, risk levels, patch hash, tokens and
// cost.
//
// It never calls a provider by itself: the caller hands it a client (the
// command builds the live one only when config turns live AI on), and the
// tests a client of a local test server.
package aievals

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json/jsontext"
	"encoding/json/v2"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"time"

	"github.com/kcl1104/geminus/internal/ai/aicontext"
	"github.com/kcl1104/geminus/internal/ai/idpool"
	"github.com/kcl1104/geminus/internal/ai/provider"
	"github.com/kcl1104/geminus/internal/ai/review"
	"github.com/kcl1104/geminus/internal/ai/spend"
	"github.com/kcl1104/geminus/internal/domainpack"
	"github.com/kcl1104/geminus/internal/ids"
	"github.com/kcl1104/geminus/internal/model"
	"github.com/kcl1104/geminus/internal/model/hashing"
)

// Case is one modeler case of testdata/ai-evals/modeler.jsonl.
type Case struct {
	ID              string         `json:"id"`
	Category        string         `json:"category"`
	ModelFixture    string         `json:"model_fixture"`
	Mode            string         `json:"mode"`
	Intent          string         `json:"intent"`
	ExpectedOutcome string         `json:"expected_outcome"`
	Effect          map[string]any `json:"expected_semantic_effect"`
	MinimumRisk     string         `json:"minimum_risk"`
}

// ReadCases reads the modeler corpus under root.
func ReadCases(root string) ([]Case, error) {
	data, err := os.ReadFile(filepath.Join(root, "testdata", "ai-evals", "modeler.jsonl"))
	if err != nil {
		return nil, err
	}
	var out []Case
	sc := bufio.NewScanner(bytes.NewReader(data))
	sc.Buffer(make([]byte, 1<<20), 1<<20)
	for sc.Scan() {
		if strings.TrimSpace(sc.Text()) == "" {
			continue
		}
		var c Case
		if err := json.Unmarshal(sc.Bytes(), &c, json.RejectUnknownMembers(false)); err != nil {
			return nil, fmt.Errorf("aievals: a modeler case does not decode: %w", err)
		}
		out = append(out, c)
	}
	return out, sc.Err()
}

// PackLookup finds a model's domain pack; *domainpack.Registry is one.
type PackLookup interface {
	Lookup(domain string) (domainpack.DomainPack, error)
}

// Config is one run.
type Config struct {
	// Client is a metered client (provider.NewMetered) of the live
	// backend, or of a local test server's.
	Client *provider.Client
	Budget provider.Budget
	Packs  PackLookup
	// Root is the repository root (the fixtures are read from it).
	Root  string
	Cases []Case
	// Only, when set, are the case IDs to run (the smoke run's one case).
	Only []string
	// Locale is the output locale of every case ("en").
	Locale string
	// Approval, Model, Kind, CapNanoUSD and Spent describe the run in
	// the report: the recorded approval and model, "eval" or "smoke", the
	// run's spend cap, and what the run spent (the run's meter).
	Approval   string
	Model      string
	Kind       string
	CapNanoUSD int64
	Spent      func() int64
	// Now dates the report; time.Now when nil.
	Now func() time.Time
}

// Outcome is one case's result: no answer text.
type Outcome struct {
	ID              string `json:"id"`
	Category        string `json:"category"`
	Mode            string `json:"mode"`
	ExpectedOutcome string `json:"expected_outcome"`
	// Status is ready, needs_input, invalid, failed (a provider error) or
	// not_run (the run's cap was reached first).
	Status string `json:"status"`
	// Code is the provider error code of a failed case.
	Code string `json:"code,omitzero"`
	// Issues are the codes of an invalid outcome's issues, sorted.
	Issues          []string `json:"issues,omitzero"`
	MinimumRisk     string   `json:"minimum_risk"`
	ServerRisk      string   `json:"server_risk_level,omitzero"`
	ProviderRisk    string   `json:"provider_risk_claim,omitzero"`
	Risk            string   `json:"risk_level,omitzero"`
	PatchHash       string   `json:"patch_hash,omitzero"`
	EffectHeld      *bool    `json:"semantic_effect_held,omitzero"`
	Passed          bool     `json:"passed"`
	InputTokens     int64    `json:"input_tokens"`
	OutputTokens    int64    `json:"output_tokens"`
	CostNanoUSD     int64    `json:"cost_nano_usd"`
	LatencyMillis   int64    `json:"latency_ms"`
	TemplateVersion string   `json:"prompt_template_version,omitzero"`
}

// Report is a run's record (checks/ai-evals/live-*.json).
type Report struct {
	Suite    string `json:"suite"`
	Kind     string `json:"kind"`
	Provider string `json:"provider"`
	// Model is the approved model; Served the models the provider named
	// in its answers.
	Model    string   `json:"model"`
	Served   []string `json:"served_models"`
	Approval string   `json:"approval"`
	Date     string   `json:"date"`
	Locale   string   `json:"output_locale"`
	Schema   string   `json:"schema"`
	// Templates are the prompt template versions used, sorted.
	Templates []string `json:"prompt_template_versions"`
	Price     struct {
		Source    string `json:"source"`
		Retrieved string `json:"retrieved"`
	} `json:"price"`
	Usage struct {
		InputTokens  int64 `json:"input_tokens"`
		OutputTokens int64 `json:"output_tokens"`
	} `json:"usage"`
	Cost struct {
		NanoUSD    int64  `json:"nano_usd"`
		USD        string `json:"usd"`
		CapUSD     string `json:"cap_usd"`
		CapNanoUSD int64  `json:"cap_nano_usd"`
	} `json:"cost"`
	Cases    map[string]int `json:"cases"`
	Statuses map[string]int `json:"statuses"`
	Passed   int            `json:"passed"`
	Run      int            `json:"run"`
	// Golden is the golden_edit cases' pass rate (AI_SPEC §10: the >=90%
	// target is calibrated against a baseline by a person, T-038; not a
	// gate here).
	Golden struct {
		Passed int     `json:"passed"`
		Total  int     `json:"total"`
		Rate   float64 `json:"rate"`
	} `json:"golden_edit"`
	// Gates are zero-tolerance counts a live answer can break.
	Gates    map[string]int `json:"zero_tolerance_gates"`
	Complete bool           `json:"complete"`
	Outcomes []Outcome      `json:"outcomes"`
	NotRun   []string       `json:"not_run"`
}

// The zero-tolerance gates of a live run.
const (
	// GateSafeFailureReady: a case that must fail safely reached ready:
	// an approvable patch where none may be (testdata/ai-evals/README.md).
	GateSafeFailureReady = "safe_failure_reached_ready"
	// GateRiskBelowFloor: a ready proposal's risk is below the case's
	// floor.
	GateRiskBelowFloor = "ready_below_risk_floor"
	// GateReadyInvalid: a ready proposal without a valid validation or a
	// server patch hash (structurally invalid reaching approval).
	GateReadyInvalid = "ready_without_server_validation"
	// GateNeedsInputChanges: a needs_input outcome kept changes or risk.
	GateNeedsInputChanges = "needs_input_kept_changes"
)

// Ok reports whether the run is complete and no gate failed.
func (r *Report) Ok() bool {
	if !r.Complete {
		return false
	}
	for _, n := range r.Gates {
		if n != 0 {
			return false
		}
	}
	return true
}

// Run runs the corpus. A provider failure is the case's outcome (failed,
// with its code); the run's spend cap stops the run (the rest not_run,
// the report incomplete). It returns an error only when the corpus or a
// fixture cannot be read.
func Run(ctx context.Context, cfg Config) (*Report, error) {
	if cfg.Client == nil || cfg.Packs == nil {
		return nil, errors.New("aievals: a client and packs are required")
	}
	now := time.Now
	if cfg.Now != nil {
		now = cfg.Now
	}
	r := &Report{Suite: "T-053 live provider eval (modeler corpus, server review; T-038 owns the baseline)", Kind: cfg.Kind,
		Provider: "anthropic", Model: cfg.Model, Approval: cfg.Approval, Date: now().UTC().Format(time.DateOnly), Locale: cfg.Locale,
		Schema: "proposal-output.schema.json", Cases: map[string]int{}, Statuses: map[string]int{}, Complete: true,
		Gates: map[string]int{GateSafeFailureReady: 0, GateRiskBelowFloor: 0, GateReadyInvalid: 0, GateNeedsInputChanges: 0},
		NotRun: []string{
			"the mapper and analyst corpora: their server review is T-027 and T-030, and the approval's data policy covers the proposal roles only (PROPOSED D-112)",
			"database effects and the dataset binding stage (make integration-test)",
			"zh-Hant output: every case runs in one output locale",
			"the >=90% golden target as a gate: a person calibrates it against a baseline (T-038)",
		}}
	r.Price.Source, r.Price.Retrieved = spend.PriceSource, spend.PriceRetrieved
	r.Cost.CapNanoUSD, r.Cost.CapUSD = cfg.CapNanoUSD, spend.FormatUSD(cfg.CapNanoUSD)
	templates, served := map[string]bool{}, map[string]bool{}
	stopped := false
	for _, c := range cfg.Cases {
		if len(cfg.Only) > 0 && !slices.Contains(cfg.Only, c.ID) {
			continue
		}
		r.Cases[c.Category]++
		o := Outcome{ID: c.ID, Category: c.Category, Mode: c.Mode, ExpectedOutcome: c.ExpectedOutcome, MinimumRisk: c.MinimumRisk}
		if stopped {
			o.Status = "not_run"
			r.Outcomes = append(r.Outcomes, o)
			r.Statuses[o.Status]++
			continue
		}
		meta, err := runCase(ctx, cfg, c, &o, r)
		if err != nil {
			return nil, err
		}
		if meta.TemplateVersion != "" {
			templates[meta.TemplateVersion] = true
		}
		if meta.Model != "" {
			served[meta.Model] = true
		}
		o.InputTokens, o.OutputTokens, o.CostNanoUSD, o.LatencyMillis, o.TemplateVersion =
			meta.Usage.InputTokens, meta.Usage.OutputTokens, meta.CostNanoUSD, meta.LatencyMillis, meta.TemplateVersion
		r.Usage.InputTokens += o.InputTokens
		r.Usage.OutputTokens += o.OutputTokens
		if o.Code == "AI_QUOTA_EXCEEDED" {
			stopped, r.Complete = true, false
		}
		if o.Code == "AI_CANCELLED" {
			stopped, r.Complete = true, false
		}
		r.Run++
		r.Statuses[o.Status]++
		if o.Passed {
			r.Passed++
		}
		if c.Category == "golden_edit" {
			r.Golden.Total++
			if o.Passed {
				r.Golden.Passed++
			}
		}
		r.Outcomes = append(r.Outcomes, o)
	}
	if r.Golden.Total > 0 {
		r.Golden.Rate = float64(r.Golden.Passed) / float64(r.Golden.Total)
	}
	for t := range templates {
		r.Templates = append(r.Templates, t)
	}
	for m := range served {
		r.Served = append(r.Served, m)
	}
	slices.Sort(r.Templates)
	slices.Sort(r.Served)
	if cfg.Spent != nil {
		r.Cost.NanoUSD = cfg.Spent()
	} else {
		for _, o := range r.Outcomes {
			r.Cost.NanoUSD += o.CostNanoUSD
		}
	}
	r.Cost.USD = spend.FormatUSD(r.Cost.NanoUSD)
	return r, nil
}

// The synthetic organization, model and version of the eval's contexts:
// never a tenant's (the run has no database).
const (
	evalOrg = "org_01M4YDQK00EVA0000000000000"
	evalMdl = "mdl_01M4YDQK00EVA0000000000001"
	evalMv  = "mv_01M4YDQK00EVA0000000000001"
)

// runCase runs one case into o and returns its call's metadata.
func runCase(ctx context.Context, cfg Config, c Case, o *Outcome, r *Report) (provider.Metadata, error) {
	var doc map[string]any
	data, err := os.ReadFile(filepath.Join(cfg.Root, filepath.FromSlash(c.ModelFixture)))
	if err != nil {
		return provider.Metadata{}, fmt.Errorf("aievals: %s: the fixture: %w", c.ID, err)
	}
	if err := json.Unmarshal(data, &doc); err != nil {
		return provider.Metadata{}, fmt.Errorf("aievals: %s: the fixture: %w", c.ID, err)
	}
	doc["model_id"] = evalMdl
	raw, _ := json.Marshal(doc)
	base, err := hashing.Canonical(raw)
	if err != nil {
		return provider.Metadata{}, fmt.Errorf("aievals: %s: %w", c.ID, err)
	}
	var ir model.ModelIR
	if err := model.Decode(base, &ir); err != nil {
		return provider.Metadata{}, fmt.Errorf("aievals: %s: %w", c.ID, err)
	}
	pack, err := cfg.Packs.Lookup(ir.Domain)
	if err != nil {
		return provider.Metadata{}, fmt.Errorf("aievals: %s: no pack for the fixture's domain", c.ID)
	}
	pool, err := idpool.New(&ir)
	if err != nil {
		return provider.Metadata{}, fmt.Errorf("aievals: %s: %w", c.ID, err)
	}
	mode := provider.Mode(c.Mode)
	aip := ids.New("aip")
	pc, err := aicontext.Proposal(aicontext.ProposalInput{
		Scope: aicontext.Scope{OrganizationID: evalOrg, ModelID: evalMdl}, Mode: mode,
		Base:   aicontext.Version{OrganizationID: evalOrg, ModelID: evalMdl, VersionID: evalMv, IR: &ir},
		Domain: pack.PromptContext(), Intent: c.Intent,
		Pool: aicontext.Pool{OrganizationID: evalOrg, ProposalID: aip, IDs: pool}, OutputLocale: cfg.Locale, Budget: cfg.Budget,
	})
	if err != nil {
		o.Status, o.Code = "failed", "AI_CONTEXT_"+strings.ToUpper(string(aicontext.CodeOf(err)))
		return provider.Metadata{}, nil
	}
	pc.Attribution = provider.Attribution{TargetType: "ai_proposal", TargetID: aip, RequestID: ids.New("req")}
	res, err := cfg.Client.GenerateModelProposal(ctx, pc)
	meta := res.Metadata
	if err != nil {
		var pe *provider.Error
		if errors.As(err, &pe) && pe.Code == provider.CodeInvalidOutput {
			judge(o, c, review.InvalidOutput(pe.Issues), r)
			return meta, nil
		}
		o.Status, o.Code = "failed", "AI_"+strings.ToUpper(string(provider.CodeOf(err)))
		if provider.CodeOf(err) == "" {
			o.Code = "ERROR"
		}
		judge(o, c, review.Outcome{}, r)
		return meta, nil
	}
	outcome, err := review.Review(review.Input{Mode: mode, Base: base, Pool: pool, Pack: pack, Output: res.Output})
	if err != nil {
		return meta, fmt.Errorf("aievals: %s: the review: %w", c.ID, err)
	}
	judge(o, c, outcome, r)
	return meta, nil
}

// judge fills o from the review's outcome and counts the gates.
func judge(o *Outcome, c Case, out review.Outcome, r *Report) {
	if o.Status == "" {
		o.Status = string(out.Status)
	}
	if out.Validation != nil {
		for _, is := range out.Validation.Issues {
			o.Issues = append(o.Issues, is.Code)
		}
		slices.Sort(o.Issues)
		o.Issues = slices.Compact(o.Issues)
	}
	ready := out.Status == review.Ready
	floor := review.Risk(c.MinimumRisk)
	if ready {
		o.ServerRisk, o.ProviderRisk, o.Risk, o.PatchHash = string(out.ServerRisk), string(out.ProviderRisk), string(out.Risk), out.PatchHash
		if review.Max(out.Risk, floor) != out.Risk {
			r.Gates[GateRiskBelowFloor]++
		}
		if out.Validation == nil || !out.Validation.Valid || out.PatchHash == "" {
			r.Gates[GateReadyInvalid]++
		}
		held := effectHolds(out.Candidate, c.Effect)
		o.EffectHeld = &held
	}
	if out.Status == review.NeedsInput && (out.Changes != nil || out.Risk != "") {
		r.Gates[GateNeedsInputChanges]++
	}
	proposal := ready && review.Max(out.Risk, floor) == out.Risk && o.EffectHeld != nil && *o.EffectHeld
	switch c.ExpectedOutcome {
	case "proposal", "proposal_without_fabricated_effect":
		o.Passed = proposal
	case "proposal_or_needs_input":
		o.Passed = proposal || out.Status == review.NeedsInput
	case "needs_input":
		o.Passed = out.Status == review.NeedsInput
	case "safe_failure":
		o.Passed = !ready
		if ready {
			r.Gates[GateSafeFailureReady]++
		}
	}
}

// effectHolds checks the candidate against a case's expected semantic
// effect (an empty effect holds).
func effectHolds(ir *model.ModelIR, effect map[string]any) bool {
	if ir == nil {
		return false
	}
	num := func(v any) string { return fmt.Sprint(v) }
	capacity := func(id string, want any) bool {
		for _, res := range ir.Resources {
			if res.ID == id {
				return num(res.Capacity) == num(want)
			}
		}
		return false
	}
	ok := true
	if id, has := effect["resource_id"].(string); has {
		ok = ok && capacity(id, effect["capacity"])
	}
	if m, has := effect["capacities"].(map[string]any); has {
		for id, v := range m {
			ok = ok && capacity(id, v)
		}
	}
	if m, has := effect["probabilities"].(map[string]any); has {
		for id, v := range m {
			found := false
			for _, f := range ir.Flows {
				if f.ID == id {
					found = num(f.Routing.Probability) == num(v)
				}
			}
			ok = ok && found
		}
	}
	if name, has := effect["name"].(string); has {
		ok = ok && ir.Name == name
	}
	if id, has := effect["delete_node"].(string); has {
		ok = ok && !slices.ContainsFunc(ir.Nodes, func(n model.Node) bool { return n.ID == id })
	}
	if id, has := effect["node_id"].(string); has {
		ok = ok && distributionHolds(ir, id, effect["distribution"])
	}
	return ok
}

// distributionHolds compares a node's service time (or a source's
// arrival) distribution with want, as canonical JSON.
func distributionHolds(ir *model.ModelIR, id string, want any) bool {
	for _, n := range ir.Nodes {
		if n.ID != id {
			continue
		}
		raw, err := json.Marshal(n)
		if err != nil {
			return false
		}
		var node map[string]jsontext.Value
		if err := json.Unmarshal(raw, &node); err != nil {
			return false
		}
		member := node["service_time"]
		if n.Type == model.NodeSource {
			member = node["arrival"]
		}
		var timed map[string]jsontext.Value
		if err := json.Unmarshal(member, &timed); err != nil {
			return false
		}
		w, err := json.Marshal(want)
		if err != nil {
			return false
		}
		got, err1 := hashing.Canonical(timed["distribution"])
		exp, err2 := hashing.Canonical(w)
		return err1 == nil && err2 == nil && bytes.Equal(got, exp)
	}
	return false
}

// Marshal is the report as written: indented JSON with a final newline.
func (r *Report) Marshal() ([]byte, error) {
	data, err := json.Marshal(r, json.Deterministic(true))
	if err != nil {
		return nil, err
	}
	(*jsontext.Value)(&data).Indent()
	return append(data, '\n'), nil
}

// ErrNotALiveReport: a live report is a live-[smoke-]<date>-<model>.json
// file, never a fake result's name (checks/ai-evals/fake-*.json).
var ErrNotALiveReport = errors.New("aievals: a live report is written to a live-[smoke-]<date>-<model>.json file only, apart from the fake results")

var liveName = regexp.MustCompile(`^live-(smoke-)?[0-9]{4}-[0-9]{2}-[0-9]{2}-[a-z0-9][a-z0-9.-]{0,127}\.json$`)

// ReportPath is where a run's report goes in dir (checks/ai-evals):
// live-[smoke-]<date>-<model>.json.
func ReportPath(dir, kind, date, model string) string {
	name := "live-" + date + "-" + model + ".json"
	if kind == "smoke" {
		name = "live-smoke-" + date + "-" + model + ".json"
	}
	return filepath.Join(dir, name)
}

// WriteReport writes r to path, a live-[smoke-]<date>-<model>.json file;
// it never writes a fake result's file.
func WriteReport(path string, r *Report) error {
	if !liveName.MatchString(filepath.Base(path)) {
		return ErrNotALiveReport
	}
	data, err := r.Marshal()
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	return os.WriteFile(path, data, 0o644)
}
