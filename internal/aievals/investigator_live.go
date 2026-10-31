package aievals

import (
	"context"
	"fmt"
	"io"
	"path/filepath"
	"strings"
	"time"

	"github.com/kcl1104/geminus/internal/ai/investigator"
	"github.com/kcl1104/geminus/internal/ai/provider"
	"github.com/kcl1104/geminus/internal/ai/spend"
	"github.com/kcl1104/geminus/internal/config"
	"github.com/kcl1104/geminus/internal/packs"
)

// plannerPromptBytes is the prompt budget of a planner turn in the live
// suite: the brief (at most 64 KiB) and a 14-step digest (at most 112 KiB)
// with the tools, within BudgetCaps (H-061 §9). A deployment's smaller
// AI_MAX_PROMPT_BYTES is raised to it for the planner only.
const plannerPromptBytes = 256 << 10

// investigatorLive is MainWith -suite investigator (H-061 §14): the six
// cases on the live planner, behind the same gate as every live run (live
// AI on, the cap within the day's and the month's limits and what the spend
// record leaves of them), and the approval's investigator role, its tool
// calls and the planner model; then the run's line in the spend record
// before any call, the report checks/ai-evals/live-investigator-<UTC
// start>-<run ID>-<planner>.json, and the line closed with what it cost.
// Only planner turns are paid: the tools' results are the cases' recorded
// ones.
func investigatorLive(ctx context.Context, ai config.AI, root, dir, loc, planner, transcript string, only []string, getenv func(string) string,
	stdout, stderr io.Writer, newBackend Factory, refuse func(string, ...any) int) int {
	mode, err := investigator.ParseTranscriptMode(transcript)
	if err != nil {
		return refuse("-transcript is digest or replay")
	}
	if !ai.LiveEnabled() {
		why := ai.LiveOff
		if why == "" {
			why = "AI_PROVIDER is not a live provider"
		}
		return refuse("live AI is off (%s)", why)
	}
	const role = string(provider.RoleInvestigator)
	if !ai.Approval.AllowsRole(role) || !ai.Approval.AllowsTools(role) || !ai.Approval.AllowsModel(role, planner) {
		return refuse("the approval does not serve the investigator with tool calls on planner %q (internal/config/aiapproval: H-029 as amended by H-063)", planner)
	}
	capNano := int64(DefaultSparkPlannerCap)
	if provider.SlowModel(planner) {
		capNano = DefaultWonderPlannerCap
	}
	if capNano, err = capOf(getenv, capNano); err != nil {
		return refuse("AI_EVAL_LIVE_MAX_USD is not a positive amount of US dollars")
	}
	if capNano > ai.Limits.Day {
		return refuse("the run's cap (USD %s) is above the deployment's daily limit (USD %s, within the monthly USD %s)",
			spend.FormatUSD(capNano), spend.FormatUSD(ai.Limits.Day), spend.FormatUSD(ai.Limits.Month))
	}
	cases, err := ReadInvestigatorCases(root)
	if err != nil {
		return refuse("the investigator cases cannot be read")
	}
	for _, id := range only {
		if !containsInvestigatorCase(cases, id) {
			return refuse("no investigator case %q", id)
		}
	}
	reg, err := packs.Registry()
	if err != nil {
		return refuse("the packs cannot be built")
	}
	out := dir
	if out == "" {
		out = filepath.Join(root, "checks", "ai-evals")
	}
	st, why := begin(out, InvestigatorKind, capNano, ai, planner)
	if st == nil {
		return refuse("%s", why)
	}
	rec, recordPath, start, runID, path := st.rec, st.recordPath, st.start, st.runID, st.path
	defer rec.Release()
	backend, err := newBackend(ai)
	if err != nil {
		return refuse("the live backend cannot be built")
	}
	budget := budgetOf(ai.Budget)
	budget.MaxPromptBytes = max(budget.MaxPromptBytes, plannerPromptBytes)
	meter := provider.NewMemoryMeter(capNano)
	client, err := provider.NewMetered(backend, budget, meter)
	if err != nil {
		return refuse("the client cannot be built: %v", err)
	}
	if !client.AcceptsTools(provider.RoleInvestigator) || !client.AcceptsModel(provider.RoleInvestigator, planner) {
		return refuse("the live backend takes no planner turn on %q", planner)
	}
	if err := rec.Open(runID, InvestigatorKind, start, planner, ai.Approval.ID(), capNano); err != nil {
		return refuse("the spend record cannot be written: %v", err)
	}
	rec.Release()

	// From here on calls may be made: every end closes the run's line.
	closeRun := func(result string) error {
		end := time.Now().UTC()
		if end.Before(start) {
			end = start
		}
		return CloseRun(recordPath, runID, end, meter.Spent(), meter.Committed(), result)
	}
	unclosed := func(err error) {
		fmt.Fprintf(stderr, "ai-eval-live: the spend record could not be closed (%v): its open line counts at the run's whole cap until it is\n", err)
	}
	report, runErr := RunInvestigator(ctx, InvestigatorConfig{ClientFor: func(InvestigatorCase) (*provider.Client, error) { return client, nil },
		Budget: budget, Packs: reg, Cases: cases, Only: only, Planner: planner, Transcript: mode, Locale: loc, Live: true, Spent: meter.Spent})
	if report != nil {
		report.Provider, report.Approval, report.RunID, report.StartedAt = string(ai.Provider), ai.Approval.ID(), runID, start.Format(time.RFC3339)
		report.CapUSD, report.CostUSD = spend.FormatUSD(capNano), spend.FormatUSD(meter.Spent())
	}
	summary := func() string {
		var ends []string
		for _, o := range report.Outcomes {
			ends = append(ends, o.ID+" "+o.End)
		}
		bar := "none"
		if report.PassBar != nil {
			bar = fmt.Sprintf("passed %v (valid %.3f, median %d ms, p95 %d ms, max %d ms)", report.PassBar.Passed, report.PassBar.ValidCallRatio,
				report.PassBar.MedianTurnMillis, report.PassBar.P95TurnMillis, report.PassBar.MaxTurnMillis)
		}
		return fmt.Sprintf("ai-eval-live: %s run %s of planner %s: %d case(s) run, %d passed, ends [%s], gates %v, pass bar %s, cost USD %s (committed %s, cap %s)",
			InvestigatorKind, runID, planner, report.Run, report.Passed, strings.Join(ends, ", "), report.Gates, bar,
			spend.FormatUSD(meter.Spent()), spend.FormatUSD(meter.Committed()), spend.FormatUSD(capNano))
	}
	if runErr != nil {
		if report != nil {
			fmt.Fprintln(stdout, summary())
		}
		fmt.Fprintf(stderr, "ai-eval-live: the run stopped: %v\n", runErr)
		if err := closeRun(ResultError); err != nil {
			unclosed(err)
		}
		return ExitReport
	}
	if err := writeLive(path, report); err != nil {
		fmt.Fprintln(stdout, summary())
		fmt.Fprintf(stderr, "ai-eval-live: the report could not be written: %v\n", err)
		if err := closeRun(ResultReportUnwritten); err != nil {
			unclosed(err)
		}
		return ExitReport
	}
	result := ResultComplete
	switch {
	case !report.Complete:
		result = ResultStopped
	case !report.Ok():
		result = ResultGateFailed
	}
	if err := closeRun(result); err != nil {
		fmt.Fprintln(stdout, summary()+"; report "+path)
		unclosed(err)
		return ExitReport
	}
	fmt.Fprintln(stdout, summary()+"; report "+path)
	if !report.Ok() {
		return ExitGates
	}
	return ExitOK
}

func containsInvestigatorCase(cases []InvestigatorCase, id string) bool {
	for _, c := range cases {
		if c.ID == id {
			return true
		}
	}
	return false
}
