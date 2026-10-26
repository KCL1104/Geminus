package aievals

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"path/filepath"
	"strings"
	"time"

	"github.com/kcl1104/geminus/internal/ai/provider"
	"github.com/kcl1104/geminus/internal/ai/spend"
	"github.com/kcl1104/geminus/internal/config"
	"github.com/kcl1104/geminus/internal/packs"
)

// Exit codes of the live eval (make ai-eval-live, make ai-smoke-live).
const (
	// ExitOK: the run is complete, no gate failed, the report is written.
	ExitOK = 0
	// ExitGates: a gate failed or the run stopped at its cap or on a
	// cancellation; the report is written and says which.
	ExitGates = 1
	// ExitRefused: refused before any provider call (live AI is off, the
	// cap or a flag is invalid): nothing was spent.
	ExitRefused = 2
	// ExitReport: the calls were made but the report could not be written;
	// the summary on stdout is what remains.
	ExitReport = 3
)

// The run caps (PROPOSED D-114): what one eval or smoke run may spend at
// most, by default; AI_EVAL_LIVE_MAX_USD sets another, within the monthly
// limit. One call's reservation under the default budget (3 attempts, a
// prompt of some 30 KB, 8192 output tokens) is about USD 1, so the smoke's
// cap holds one call; the eval's is a tenth of H-018's monthly budget for
// 50 calls of a few cents each.
const (
	DefaultEvalCap  = 10 * spend.NanoPerUSD
	DefaultSmokeCap = 2 * spend.NanoPerUSD
)

// SmokeCase is the smoke run's one case: a synthetic default pipeline and
// a capacity change the user typed (MOD-002).
const SmokeCase = "MOD-002"

// Factory builds the live backend of a configuration that turns live AI
// on (cmd/ai-eval-live: the Anthropic adapter, anthropic.New).
type Factory func(config.AI) (provider.Billable, error)

// Main is the live eval's command line: it refuses unless config turns
// live AI on (the complete recorded approval, AI_MODEL, both keys and
// GEMINUS_AI_LIVE=on: the same gate as the worker's) and the run's cap is
// within the monthly limit, and only then builds the backend; it runs the
// corpus (or, with -smoke, one case) through a client metered by the
// run's own cap, writes the report to a live-*.json file of its own, never
// a fake result's, and prints a summary without any answer text.
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
			why = "AI_PROVIDER is not anthropic"
		}
		return refuse("live AI is off (%s); it needs the owner's complete recorded approval (internal/config/aiapproval), AI_MODEL, both keys and GEMINUS_AI_LIVE=on", why)
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
	if capNano > ai.Limits.Month {
		return refuse("the run's cap is above the monthly limit")
	}
	cases, err := ReadCases(*root)
	if err != nil {
		return refuse("the corpus cannot be read")
	}
	var ids []string
	switch {
	case *only != "":
		ids = strings.Split(*only, ",")
	case *smoke:
		ids = []string{SmokeCase}
	}
	for _, id := range ids {
		if !containsCase(cases, id) {
			return refuse("no case %q in the corpus", id)
		}
	}
	reg, err := packs.Registry()
	if err != nil {
		return refuse("the packs cannot be built")
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
	report, err := Run(ctx, Config{Client: client, Budget: budget, Packs: reg, Root: *root, Cases: cases, Only: ids, Locale: *locale,
		Approval: ai.Approval.ID(), Model: ai.Model, Kind: kind, CapNanoUSD: capNano, Spent: meter.Spent})
	if err != nil {
		fmt.Fprintf(stderr, "ai-eval-live: the run stopped: %v\n", err)
		return ExitReport
	}
	out := *dir
	if out == "" {
		out = filepath.Join(*root, "checks", "ai-evals")
	}
	path := ReportPath(out, kind, report.Date, ai.Model)
	summary := fmt.Sprintf("ai-eval-live: %s run of %s: %d case(s) run, %d passed, golden %d/%d, gates %v, complete %v, cost USD %s (cap %s), %d input and %d output tokens",
		kind, ai.Model, report.Run, report.Passed, report.Golden.Passed, report.Golden.Total, report.Gates, report.Complete,
		report.Cost.USD, report.Cost.CapUSD, report.Usage.InputTokens, report.Usage.OutputTokens)
	if err := WriteReport(path, report); err != nil {
		fmt.Fprintln(stdout, summary)
		fmt.Fprintf(stderr, "ai-eval-live: the report could not be written: %v\n", err)
		return ExitReport
	}
	fmt.Fprintln(stdout, summary+"; report "+path)
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
