package aievals

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/kcl1104/geminus/internal/ai/provider"
	"github.com/kcl1104/geminus/internal/ai/spend"
	"github.com/kcl1104/geminus/internal/config"
	"github.com/kcl1104/geminus/internal/ids"
	"github.com/kcl1104/geminus/internal/packs"
)

// Exit codes of the live eval (make ai-eval-live, make ai-smoke-live).
const (
	// ExitOK: the run is complete, no gate failed, the report is written,
	// the spend record closed.
	ExitOK = 0
	// ExitGates: a gate failed or the run stopped at its cap or on a
	// cancellation; the report is written and says which, the spend record
	// closed.
	ExitGates = 1
	// ExitRefused: refused before any provider call (live AI is off, the
	// cap or a flag is invalid, the cap does not fit the day's or the
	// month's limit or what the spend record leaves of them, the record
	// cannot be used): nothing was spent, nothing written.
	ExitRefused = 2
	// ExitReport: calls were made, but the run stopped on an error, or the
	// report or the spend record's close line could not be written; the
	// summary and the cost on stdout are what remains, and the record is
	// closed when it can be (an open line without its close counts at the
	// run's whole cap).
	ExitReport = 3
)

// The run caps (PROPOSED D-114): what one eval or smoke run may spend at
// most, by default; AI_EVAL_LIVE_MAX_USD sets another, within the
// deployment's daily and monthly limits and what the spend record leaves
// of them (review round 1, H1). One call's reservation under the default
// budget (3 attempts, a prompt of some 30 KB, 8192 output tokens, at the
// dearest inference geo) is about USD 1, so the smoke's cap holds one
// call; the eval's is the default daily limit for 50 calls of a few cents
// each (after a smoke run on the same UTC day it is more than the day has
// left: AI_EVAL_LIVE_MAX_USD then sets a lower cap).
const (
	DefaultEvalCap  = 10 * spend.NanoPerUSD
	DefaultSmokeCap = 2 * spend.NanoPerUSD
)

// SmokeCase is the smoke run's one case: a synthetic default pipeline and
// a capacity change the user typed (MOD-002).
const SmokeCase = "MOD-002"

// Factory builds the live backend of a configuration that turns live AI
// on (cmd/ai-eval-live: the adapter of AI_PROVIDER, anthropic.New or, T-055,
// meta.New).
type Factory func(config.AI) (provider.Billable, error)

// Main is the live eval's command line: it refuses unless config turns
// live AI on (the complete recorded approval, AI_MODEL, the provider's keys
// and GEMINUS_AI_LIVE=on: the same gate as the worker's), the run's cap is
// within the deployment's daily and monthly limits, and the spend record
// (RecordName, next to the reports) can be read and leaves room for the
// cap in the UTC day and month; only then does it build the backend. It
// appends the run's open line to the record before any call, runs the
// corpus (or, with -smoke, one case) through a client metered by the
// run's own cap, writes the report to a live-*.json file of its own that
// names the run (never a fake result's, never an existing file), closes
// the run's line with what it cost whatever the run's end, and prints a
// summary without any answer text.
func Main(ctx context.Context, args []string, getenv func(string) string, stdout, stderr io.Writer, newBackend Factory) int {
	fs := flag.NewFlagSet("ai-eval-live", flag.ContinueOnError)
	fs.SetOutput(stderr)
	smoke := fs.Bool("smoke", false, "run the smoke case only ("+SmokeCase+")")
	root := fs.String("root", ".", "the repository root (fixtures)")
	dir := fs.String("dir", "", "where the report goes (default <root>/checks/ai-evals)")
	only := fs.String("cases", "", "comma-separated case IDs to run (default: every modeler case)")
	locale := fs.String("locale", "en", "the output locale of every case (en or zh-Hant)")
	if err := fs.Parse(args); err != nil {
		return ExitRefused
	}
	refuse := func(format string, a ...any) int {
		fmt.Fprintf(stderr, "ai-eval-live: REFUSED (no provider call was made): "+format+"\n", a...)
		return ExitRefused
	}
	if fs.NArg() > 0 || (*locale != "en" && *locale != "zh-Hant") {
		return refuse("unexpected arguments or locale")
	}
	ai, err := config.LoadAI(getenv)
	if err != nil {
		var codes []string
		var joined interface{ Unwrap() []error }
		errs := []error{err}
		if errors.As(err, &joined) {
			errs = joined.Unwrap()
		}
		for _, e := range errs {
			var ce *config.Error
			if errors.As(e, &ce) {
				codes = append(codes, ce.Var+":"+ce.Code)
			}
		}
		return refuse("the AI configuration is invalid: %s", strings.Join(codes, ", "))
	}
	if !ai.LiveEnabled() {
		why := ai.LiveOff
		if why == "" {
			why = "AI_PROVIDER is not a live provider"
		}
		return refuse("live AI is off (%s); it needs AI_PROVIDER anthropic or meta, the owner's complete recorded approval of it (internal/config/aiapproval), AI_MODEL, its keys and GEMINUS_AI_LIVE=on", why)
	}
	kind, capNano := "eval", DefaultEvalCap
	if *smoke {
		kind, capNano = "smoke", DefaultSmokeCap
	}
	if raw := getenv("AI_EVAL_LIVE_MAX_USD"); raw != "" {
		if capNano, err = spend.ParseUSD(raw); err != nil {
			return refuse("AI_EVAL_LIVE_MAX_USD is not a positive amount of US dollars")
		}
	}
	// The run spends through the deployment's provider organizations, so
	// its cap fits the deployment's daily limit (H-018; review round 1, H1),
	// and so the monthly limit too: config keeps the day within the month
	// (spend.Limits.Validate). What the record leaves of both is checked
	// below.
	if capNano > ai.Limits.Day {
		return refuse("the run's cap (USD %s) is above the deployment's daily limit (USD %s, within the monthly USD %s)",
			spend.FormatUSD(capNano), spend.FormatUSD(ai.Limits.Day), spend.FormatUSD(ai.Limits.Month))
	}
	cases, err := ReadCases(*root)
	if err != nil {
		return refuse("the corpus cannot be read")
	}
	var caseIDs []string
	switch {
	case *only != "":
		caseIDs = strings.Split(*only, ",")
	case *smoke:
		caseIDs = []string{SmokeCase}
	}
	for _, id := range caseIDs {
		if !containsCase(cases, id) {
			return refuse("no case %q in the corpus", id)
		}
	}
	reg, err := packs.Registry()
	if err != nil {
		return refuse("the packs cannot be built")
	}
	out := *dir
	if out == "" {
		out = filepath.Join(*root, "checks", "ai-evals")
	}
	// What earlier runs spent (the spend record), against what the day and
	// the month have left; the record is locked from here until this run's
	// open line is in it.
	recordPath := filepath.Join(out, RecordName)
	rec, err := OpenRecord(recordPath)
	if err != nil {
		return refuse("the spend record %s cannot be used (%v); it is committed with the reports: restore it, never start it again empty", recordPath, err)
	}
	defer rec.Release()
	start := time.Now().UTC().Truncate(time.Second)
	day, month := rec.Spent(start)
	switch {
	case capNano > ai.Limits.Day-day:
		return refuse("the run's cap (USD %s) is more than the UTC day has left (the spend record holds USD %s of the daily limit USD %s)",
			spend.FormatUSD(capNano), spend.FormatUSD(day), spend.FormatUSD(ai.Limits.Day))
	case capNano > ai.Limits.Month-month:
		return refuse("the run's cap (USD %s) is more than the UTC month has left (the spend record holds USD %s of the monthly limit USD %s)",
			spend.FormatUSD(capNano), spend.FormatUSD(month), spend.FormatUSD(ai.Limits.Month))
	}
	runID := ids.ULID(start)
	path := ReportPath(out, kind, start, runID, ai.Model)
	if _, err := os.Lstat(path); err == nil || !errors.Is(err, os.ErrNotExist) {
		return refuse("the report %s exists; a report never replaces another", path)
	}
	backend, err := newBackend(ai)
	if err != nil {
		return refuse("the live backend cannot be built")
	}
	budget := budgetOf(ai.Budget)
	meter := provider.NewMemoryMeter(capNano)
	client, err := provider.NewMetered(backend, budget, meter)
	if err != nil {
		return refuse("the client cannot be built: %v", err)
	}
	if err := rec.Open(runID, kind, start, ai.Model, ai.Approval.ID(), capNano); err != nil {
		return refuse("the spend record cannot be written: %v", err)
	}
	rec.Release()

	// From here on calls may be made: every end closes the run's line.
	closeRun := func(result string) error {
		end := time.Now().UTC()
		if end.Before(start) {
			end = start // a clock that went back: the close is never before the open
		}
		return CloseRun(recordPath, runID, end, meter.Spent(), meter.Committed(), result)
	}
	report, runErr := Run(ctx, Config{Client: client, Budget: budget, Packs: reg, Root: *root, Cases: cases, Only: caseIDs, Locale: *locale,
		Provider: string(ai.Provider), Approval: ai.Approval.ID(), Model: ai.Model, Kind: kind, CapNanoUSD: capNano, Spent: meter.Spent, RunID: runID,
		Now: func() time.Time { return start }})
	summary := func() string {
		return fmt.Sprintf("ai-eval-live: %s run %s of %s: %d case(s) run, %d passed, golden %d/%d, gates %v, complete %v, cost USD %s (committed %s, cap %s), %d input and %d output tokens, spend warnings %v",
			kind, runID, ai.Model, report.Run, report.Passed, report.Golden.Passed, report.Golden.Total, report.Gates, report.Complete,
			spend.FormatUSD(meter.Spent()), spend.FormatUSD(meter.Committed()), report.Cost.CapUSD, report.Usage.InputTokens,
			report.Usage.OutputTokens, report.SpendWarnings)
	}
	// Each case the provider answered with an error status, after the
	// summary: its code, status and error type, never the provider's
	// message (T-053 schema follow-up).
	providerErrors := func() {
		for _, o := range report.Outcomes {
			if o.ProviderStatus != 0 {
				fmt.Fprintf(stderr, "ai-eval-live: WARNING: %s failed with %s: the provider answered %d %s\n", o.ID, o.Code, o.ProviderStatus, o.ProviderErrorType)
			}
		}
	}
	unclosed := func(err error) {
		fmt.Fprintf(stderr, "ai-eval-live: the spend record could not be closed (%v): its open line counts at the run's whole cap until it is\n", err)
	}
	if runErr != nil {
		// The calls made are paid for: the summary and the cost still say
		// what they were (review round 1, L6).
		if report != nil {
			fmt.Fprintln(stdout, summary())
			providerErrors()
		} else {
			fmt.Fprintf(stdout, "ai-eval-live: %s run %s of %s stopped: cost USD %s (committed %s)\n", kind, runID, ai.Model,
				spend.FormatUSD(meter.Spent()), spend.FormatUSD(meter.Committed()))
		}
		fmt.Fprintf(stderr, "ai-eval-live: the run stopped: %v\n", runErr)
		if err := closeRun(ResultError); err != nil {
			unclosed(err)
		}
		return ExitReport
	}
	if err := WriteReport(path, report); err != nil {
		fmt.Fprintln(stdout, summary())
		providerErrors()
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
		providerErrors()
		unclosed(err)
		return ExitReport
	}
	fmt.Fprintln(stdout, summary()+"; report "+path)
	providerErrors()
	if n := report.SpendWarnings[WarningOverReservation] + report.SpendWarnings[WarningGeoUnknown]; n > 0 {
		fmt.Fprintf(stderr, "ai-eval-live: WARNING: %d call(s) broke an assumption of the reservation's bound (%v); what they cost is recorded\n",
			n, report.SpendWarnings)
	}
	if !report.Ok() {
		return ExitGates
	}
	return ExitOK
}

func containsCase(cases []Case, id string) bool {
	for _, c := range cases {
		if c.ID == id {
			return true
		}
	}
	return false
}

// budgetOf is the provider budget a deployment's AI configuration sets,
// as the worker builds it (cmd/worker aiClient).
func budgetOf(b config.AIBudget) provider.Budget {
	budget := provider.DefaultBudget()
	if b.MaxPromptBytes > 0 {
		budget.MaxPromptBytes = b.MaxPromptBytes
	}
	if b.MaxOutputTokens > 0 {
		budget.MaxOutputTokens = b.MaxOutputTokens
	}
	if b.TimeoutSeconds > 0 {
		budget.Timeout = time.Duration(b.TimeoutSeconds) * time.Second
	}
	if b.AttemptTimeoutSeconds > 0 {
		budget.AttemptTimeout = time.Duration(b.AttemptTimeoutSeconds) * time.Second
	}
	if b.MaxRetries != nil {
		budget.MaxRetries = *b.MaxRetries
	}
	if b.MaxConcurrentPerOrganization > 0 {
		budget.MaxConcurrentPerOrganization = b.MaxConcurrentPerOrganization
	}
	return budget
}
