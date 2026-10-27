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

	"github.com/kcl1104/geminus/internal/ai/photopipeline"
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

// PhotoFactory builds the photo path's live backends of a configuration
// whose photo path is on, as the worker builds them (cmd/worker
// photoBackends): the judge (meta.New: Muse Spark, which reads images) and
// the segmenter (meta.NewSegmenter: the approval's segmentation model,
// SAM 3.1). T-056 review round 1.
type PhotoFactory func(config.AIVision) (provider.Billable, provider.BillableSegmenter, error)

// Backends are the live backends Main may build once its gate passed.
type Backends struct {
	Text Factory
	// Photo is nil in a build without the photo smoke: -smoke-photo is
	// refused.
	Photo PhotoFactory
}

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
	return MainWith(ctx, args, getenv, stdout, stderr, Backends{Text: newBackend})
}

// MainWith is Main with the photo smoke (T-056 review round 1, I1): with
// -smoke-photo it runs the photo pipeline once on the demo image
// (photoSmoke), behind the worker's photo-path gate, metered and recorded
// as every run.
func MainWith(ctx context.Context, args []string, getenv func(string) string, stdout, stderr io.Writer, b Backends) int {
	fs := flag.NewFlagSet("ai-eval-live", flag.ContinueOnError)
	fs.SetOutput(stderr)
	smoke := fs.Bool("smoke", false, "run the smoke case only ("+SmokeCase+")")
	smokePhoto := fs.Bool("smoke-photo", false, "run the photo pipeline once on "+PhotoImage+" with "+PhotoBase+" (the first live SAM 3.1 call)")
	root := fs.String("root", ".", "the repository root (fixtures)")
	dir := fs.String("dir", "", "where the report goes (default <root>/checks/ai-evals)")
	only := fs.String("cases", "", "comma-separated case IDs to run (default: every modeler case)")
	locale := fs.String("locale", "en", "the output locale of every case (en or zh-Hant)")
	concepts := fs.String("concepts", "", "with -smoke-photo: comma-separated concepts SAM is asked for instead of the pack's, to explore their wording (also "+
		conceptsVar+"); at most 8, each a short lowercase noun phrase")
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
	if *smokePhoto && (*smoke || *only != "") {
		return refuse("-smoke-photo runs alone, without -smoke or -cases")
	}
	if *concepts != "" && !*smokePhoto {
		return refuse("-concepts is the photo smoke's (-smoke-photo)")
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
	if *smokePhoto {
		return photoSmoke(ctx, ai, *root, *dir, *locale, *concepts, getenv, stdout, stderr, b.Photo, refuse)
	}
	newBackend := b.Text
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
	if capNano, err = capOf(getenv, capNano); err != nil {
		return refuse("AI_EVAL_LIVE_MAX_USD is not a positive amount of US dollars")
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
	st, why := begin(out, kind, capNano, ai, ai.Model)
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

// started is a run whose spend record is read and locked, whose cap fits
// what the record leaves of the day and the month, with its ID, start and
// report path.
type started struct {
	rec        *Record
	recordPath string
	start      time.Time
	runID      string
	path       string
}

// begin reads and locks the spend record in out, checks capNano against
// what it leaves of ai's daily and monthly limits, and names the run (kind,
// model) and its report, which must not exist yet. On a refusal it returns
// why, and the record is not held; otherwise the caller releases it.
func begin(out, kind string, capNano int64, ai config.AI, model string) (*started, string) {
	recordPath := filepath.Join(out, RecordName)
	rec, err := OpenRecord(recordPath)
	if err != nil {
		return nil, fmt.Sprintf("the spend record %s cannot be used (%v); it is committed with the reports: restore it, never start it again empty", recordPath, err)
	}
	start := time.Now().UTC().Truncate(time.Second)
	day, month := rec.Spent(start)
	why := ""
	switch {
	case capNano > ai.Limits.Day-day:
		why = fmt.Sprintf("the run's cap (USD %s) is more than the UTC day has left (the spend record holds USD %s of the daily limit USD %s)",
			spend.FormatUSD(capNano), spend.FormatUSD(day), spend.FormatUSD(ai.Limits.Day))
	case capNano > ai.Limits.Month-month:
		why = fmt.Sprintf("the run's cap (USD %s) is more than the UTC month has left (the spend record holds USD %s of the monthly limit USD %s)",
			spend.FormatUSD(capNano), spend.FormatUSD(month), spend.FormatUSD(ai.Limits.Month))
	}
	runID := ids.ULID(start)
	path := ReportPath(out, kind, start, runID, model)
	if why == "" {
		if _, err := os.Lstat(path); err == nil || !errors.Is(err, os.ErrNotExist) {
			why = fmt.Sprintf("the report %s exists; a report never replaces another", path)
		}
	}
	if why != "" {
		rec.Release()
		return nil, why
	}
	return &started{rec: rec, recordPath: recordPath, start: start, runID: runID, path: path}, ""
}

// capOf is the run's cap: def, or AI_EVAL_LIVE_MAX_USD.
func capOf(getenv func(string) string, def int64) (int64, error) {
	raw := getenv("AI_EVAL_LIVE_MAX_USD")
	if raw == "" {
		return def, nil
	}
	return spend.ParseUSD(raw)
}

// conceptsVar is the photo smoke's override of the pack's concepts, as -concepts
// (T-056 after the first live run). Nothing else reads it: not the worker,
// the API or config.
const conceptsVar = "AI_SMOKE_PHOTO_CONCEPTS"

// parseConcepts reads a comma-separated list of concepts (each item
// trimmed of spaces at its ends) and checks it as the segmenter client
// checks a request's (provider.CheckConcepts); an empty item is refused.
func parseConcepts(raw string) ([]string, error) {
	var out []string
	for item := range strings.SplitSeq(raw, ",") {
		item = strings.TrimSpace(item)
		if item == "" {
			return nil, errors.New("an empty concept (a comma at an end or two in a row)")
		}
		out = append(out, item)
	}
	if err := provider.CheckConcepts(out); err != nil {
		return nil, err
	}
	return out, nil
}

// photoSmoke is MainWith -smoke-photo (T-056 review round 1, I1: the first
// live SAM 3.1 call goes through the worker's photo pipeline, never a
// hand-made request). It refuses, before building any backend, unless the
// photo path is on exactly as the worker turns it on (config's decision:
// the photo path's live provider, its complete recorded approval naming
// images, its model and key, GEMINUS_AI_LIVE=on), the provider is Meta
// (the one adapter of this build that reads images) and the approval names
// a segmentation model; unless its cap (DefaultPhotoSmokeCap, or
// AI_EVAL_LIVE_MAX_USD) fits the daily limit and what the spend record
// leaves of the day and the month; and unless the demo image and base read
// and check as the API and the worker read them. Then it builds the judge
// and the segmenter (factory) metered by the cap, checks that the judge
// takes images, opens the run's line in the spend record, runs the
// pipeline once, writes the report and closes the line with what it cost.
//
// flagConcepts (-concepts) or AI_SMOKE_PHOTO_CONCEPTS, one of them, is a
// comma-separated list of concepts SAM is asked for instead of the pack's
// (T-056 after the first live run, to explore their wording live through
// the worker's pipeline, never by hand), checked as the segmenter client
// checks the pack's before anything is built; the same gate, cap and
// spend record, the override and the list in the report. Only this smoke
// reads the variable: the worker's concepts are always its pack's.
func photoSmoke(ctx context.Context, ai config.AI, root, dir, loc, flagConcepts string, getenv func(string) string, stdout, stderr io.Writer,
	factory PhotoFactory, refuse func(string, ...any) int) int {
	v := ai.Vision
	if !v.LiveEnabled() {
		why := v.LiveOff
		if why == "" {
			why = "the photo path's provider is not a live provider"
		}
		return refuse("the photo path is off (%s); it needs AI_PROVIDER meta (or AI_VISION_PROVIDER meta), the owner's complete recorded approval of it naming images (internal/config/aiapproval: H-029), its model, its key and GEMINUS_AI_LIVE=on, as the worker's", why)
	}
	switch {
	case v.Provider != config.AIProviderMeta:
		return refuse("the photo path's provider has no adapter that reads images in this build")
	case v.SegmentationModel == "":
		return refuse("the photo path's approval names no segmentation model")
	case factory == nil:
		return refuse("this build has no photo backends")
	}
	var override []string
	switch envConcepts := getenv(conceptsVar); {
	case flagConcepts != "" && envConcepts != "":
		return refuse("set either -concepts or %s, not both", conceptsVar)
	case flagConcepts != "" || envConcepts != "":
		source, raw := "-concepts", flagConcepts
		if raw == "" {
			source, raw = conceptsVar, envConcepts
		}
		var err error
		if override, err = parseConcepts(raw); err != nil {
			return refuse("%s: %v", source, err)
		}
	}
	capNano, err := capOf(getenv, DefaultPhotoSmokeCap)
	if err != nil {
		return refuse("AI_EVAL_LIVE_MAX_USD is not a positive amount of US dollars")
	}
	if capNano > ai.Limits.Day {
		return refuse("the run's cap (USD %s) is above the deployment's daily limit (USD %s, within the monthly USD %s)",
			spend.FormatUSD(capNano), spend.FormatUSD(ai.Limits.Day), spend.FormatUSD(ai.Limits.Month))
	}
	reg, err := packs.Registry()
	if err != nil {
		return refuse("the packs cannot be built")
	}
	in, err := ReadPhotoInputs(ctx, root, reg)
	if err != nil {
		return refuse("the photo smoke's inputs cannot be used: %v", err)
	}
	out := dir
	if out == "" {
		out = filepath.Join(root, "checks", "ai-evals")
	}
	st, why := begin(out, PhotoSmokeKind, capNano, ai, v.Model)
	if st == nil {
		return refuse("%s", why)
	}
	rec, recordPath, start, runID, path := st.rec, st.recordPath, st.start, st.runID, st.path
	defer rec.Release()
	judge, seg, err := factory(v)
	if err != nil || judge == nil || seg == nil {
		return refuse("the live backends cannot be built")
	}
	budget := budgetOf(ai.Budget)
	run, err := NewPhotoRun(judge, seg, budget, capNano)
	if err != nil {
		return refuse("the clients cannot be built: %v", err)
	}
	if !photopipeline.TakesImages(run.Judge) {
		return refuse("the photo path's judge does not take images")
	}
	approval := v.Approval.ID()
	if err := rec.Open(runID, PhotoSmokeKind, start, v.Model, approval, capNano); err != nil {
		return refuse("the spend record cannot be written: %v", err)
	}
	rec.Release()

	// From here on calls may be made: every end closes the run's line.
	closeRun := func(result string) error {
		end := time.Now().UTC()
		if end.Before(start) {
			end = start
		}
		return CloseRun(recordPath, runID, end, run.Spent(), run.Committed(), result)
	}
	unclosed := func(err error) {
		fmt.Fprintf(stderr, "ai-eval-live: the spend record could not be closed (%v): its open line counts at the run's whole cap until it is\n", err)
	}
	report, runErr := run.Run(ctx, PhotoConfig{Packs: reg, Root: root, Locale: loc, Provider: string(v.Provider), Approval: approval, Model: v.Model,
		SegmentationModel: v.SegmentationModel, CapNanoUSD: capNano, RunID: runID, Concepts: override, Now: func() time.Time { return start }}, in)
	if runErr != nil {
		fmt.Fprintf(stdout, "ai-eval-live: %s run %s of %s stopped: cost USD %s (committed %s)\n", PhotoSmokeKind, runID, v.Model,
			spend.FormatUSD(run.Spent()), spend.FormatUSD(run.Committed()))
		fmt.Fprintf(stderr, "ai-eval-live: the run stopped: %v\n", runErr)
		if err := closeRun(ResultError); err != nil {
			unclosed(err)
		}
		return ExitReport
	}
	summary := func() string {
		r := report
		status := r.Status
		if r.Code != "" {
			status += " " + r.Code
		}
		concepts := "the pack's concepts"
		if r.ConceptsOverridden {
			concepts = "concepts overridden " + strings.Join(r.Segmenter.ConceptList, ",")
		}
		var by []string
		for _, c := range r.Segmenter.ByConcept {
			by = append(by, fmt.Sprintf("%s %d", c.Concept, c.Boxes))
		}
		return fmt.Sprintf("ai-eval-live: %s run %s of %s and %s: status %s, server risk %s, %s, %d of %d concepts answered, %d boxes (%s), SAM images processed %v, "+
			"judge counted %d and billed %d input tokens (reserved for %d), cost USD %s (committed %s, cap %s), gates %v, observations %v, complete %v",
			PhotoSmokeKind, runID, r.SegmentationModel, r.Model, status, or(r.ServerRisk, "none"), concepts,
			r.Segmenter.Answered, r.Segmenter.Concepts, r.Segmenter.Boxes, strings.Join(by, ", "), r.Segmenter.ImagesProcessed, r.Judge.Counted, r.Judge.BilledInput,
			r.Judge.ReservedInput, spend.FormatUSD(run.Spent()), spend.FormatUSD(run.Committed()), r.Cost.CapUSD, r.Gates, r.Warnings, r.Complete)
	}
	providerError := func() {
		if report.ProviderStatus != 0 {
			fmt.Fprintf(stderr, "ai-eval-live: WARNING: the photo smoke failed with %s: the provider answered %d %s\n", report.Code, report.ProviderStatus,
				report.ProviderErrorType)
		}
	}
	if err := WritePhotoReport(path, report); err != nil {
		fmt.Fprintln(stdout, summary())
		providerError()
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
		providerError()
		unclosed(err)
		return ExitReport
	}
	fmt.Fprintln(stdout, summary()+"; report "+path)
	providerError()
	for _, w := range []string{WarningOverReservation, WarningCountExceeded, WarningImagesProcessed, WarningFrame, WarningNoBoxes} {
		if report.Warnings[w] > 0 {
			fmt.Fprintf(stderr, "ai-eval-live: WARNING: %s (%d): see the report; H-037 and the review's I1 ask for it to be reported\n", w, report.Warnings[w])
		}
	}
	if !report.Ok() {
		return ExitGates
	}
	return ExitOK
}

func or(s, def string) string {
	if s == "" {
		return def
	}
	return s
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
