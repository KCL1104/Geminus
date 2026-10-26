package config

import (
	"errors"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/kcl1104/geminus/internal/ai/spend"
	"github.com/kcl1104/geminus/internal/config/aiapproval"
	"github.com/kcl1104/geminus/internal/secret"
)

// AI is the LLM provider configuration (T-024, T-053, T-055; H-001, H-017,
// H-018, H-029, H-033, D-018).
//
// Live AI stays off unless all of these hold (T-053, T-055):
//
//   - AI_PROVIDER=anthropic or meta;
//   - AI_POLICY_APPROVAL names an approval of that provider this build
//     records (internal/config/aiapproval), and the record lacks nothing:
//     H-018 (anthropic) is complete since the owner confirmed the provider
//     organizations' data retention arrangement (H-026); H-029, amended by
//     H-033 (meta), records the owner's Standard-mode Muse Spark and SAM
//     3.1;
//   - AI_MODEL is exactly the record's model, and this build prices it (and
//     its segmentation model) (internal/ai/spend);
//   - the operator's switch GEMINUS_AI_LIVE=on;
//   - the provider's keys: both GEMINUS_ANTHROPIC_API_KEY_A and _B (two
//     different ones), or GEMINUS_MUSE_API_KEY (in the LLM_ shape).
//
// Otherwise:
//
//   - AI_PROVIDER=disabled (the default): every AI call fails with
//     AI_PROVIDER_NOT_CONFIGURED;
//   - AI_PROVIDER=anthropic or meta with the switch not on: every AI call
//     fails with AI_POLICY_NOT_APPROVED, no key is read, and LiveOff says
//     why (no approval named, an approval of another provider, what the
//     record lacks, the model, the switch);
//   - the switch on while any condition fails, or AI_POLICY_APPROVAL
//     naming an approval this build does not record: the process refuses
//     to start, with the condition's typed code.
//
// AI_PROVIDER=fake selects the deterministic fake provider, for
// development and tests only (APP_ENV development or test).
//
// The photo path (Vision, T-055 for T-056): AI_VISION_PROVIDER, unset,
// follows AI_PROVIDER; disabled turns it off; fake is for development and
// tests; the other live provider takes its own AI_VISION_POLICY_APPROVAL
// and AI_VISION_MODEL (the same rules), its own keys and the same switch.
// Its approval must name images (H-029 does; H-018 does not, so with
// Claude the photo path stays off: images_not_approved), and the
// proposals are unaffected either way.
//
// Keys are read only once live AI is on for their provider, only from
// GEMINUS_ANTHROPIC_API_KEY_A (primary, label org-a) and
// GEMINUS_ANTHROPIC_API_KEY_B (secondary, label org-b), or
// GEMINUS_MUSE_API_KEY (label meta), and handed to the adapter
// explicitly; nothing reads ANTHROPIC_API_KEY or MODEL_API_KEY (D-018). A
// key is a secret.Secret: it prints as [redacted], and everything else
// names it by label.
type AI struct {
	Provider AIProvider
	// Approval is the recorded approval AI_POLICY_APPROVAL names; the zero
	// value unless live AI is on. Only aiapproval.Recorded makes one, and
	// the live adapter takes nothing else (review L5).
	Approval aiapproval.Approval
	// Model is AI_MODEL: required for live AI, never defaulted, and the
	// approval's model exactly.
	Model string
	// Credentials in failover order, primary first.
	Credentials []AICredential
	Budget      AIBudget
	// Limits are the spend limits (D-110): set once live AI is on for
	// either path, under the smallest monthly budget of the live
	// approvals (one ledger).
	Limits spend.Limits
	// LiveOff is why live AI is off while AI_PROVIDER is a live provider:
	// one of the LiveOff* reasons, or what the record lacks (an
	// aiapproval.Reason). Empty when live AI is on or not selected.
	LiveOff string
	// Vision is the photo path (T-055).
	Vision AIVision
}

// Why live AI is off besides what a record lacks (aiapproval.Reason).
const (
	// LiveOffNoApproval: AI_POLICY_APPROVAL is empty.
	LiveOffNoApproval = "no_approval_named"
	// LiveOffProviderMismatch: the approval named is another provider's
	// (T-055).
	LiveOffProviderMismatch = "provider_mismatch"
	// LiveOffModelMismatch: AI_MODEL is not the approval's model.
	LiveOffModelMismatch = "model_mismatch"
	// LiveOffModelUnpriced: the approval's model has no price in this build
	// (internal/ai/spend), so no call can be reserved for.
	LiveOffModelUnpriced = "model_unpriced"
	// LiveOffSwitch: GEMINUS_AI_LIVE is not on.
	LiveOffSwitch = "switch_off"
	// LiveOffImagesNotApproved: the photo path's provider's approval names
	// no image in its data policy (T-055: H-018's names synthetic data and
	// user-typed text only).
	LiveOffImagesNotApproved = "images_not_approved"
)

// LiveEnabled reports whether the live provider may be called: a live
// provider with a valid approval of its own.
func (a AI) LiveEnabled() bool {
	return a.Provider.live() && a.Approval.Valid() && a.Approval.Provider() == string(a.Provider)
}

// AIVision is the photo path's configuration (T-055, for T-056; H-033):
// which provider judges stations and flow from a photo, and with it the
// segmentation model (SAM 3.1, when the approval names one). With a live
// provider it carries that provider's approval, model and key, read only
// once the photo path is on.
type AIVision struct {
	Provider AIProvider
	// Approval, Model and Credentials: the live provider's, as for AI.
	Approval    aiapproval.Approval
	Model       string
	Credentials []AICredential
	// SegmentationModel is the approval's segmentation model ("" for none:
	// the provider judges the photo alone).
	SegmentationModel string
	// LiveOff is why the photo path is off while its provider is live: a
	// LiveOff* reason or what the record lacks. Empty when it is on or no
	// live provider is selected.
	LiveOff string
}

// LiveEnabled reports whether the photo path's live provider may be
// called: a live provider whose valid approval names images.
func (v AIVision) LiveEnabled() bool {
	return v.Provider.live() && v.Approval.Valid() && v.Approval.Provider() == string(v.Provider) && v.Approval.AllowsImages()
}

// AICredential is one API key and the label that proposals, analyses and
// audit name it by ("org-a", "org-b", "meta").
type AICredential struct {
	Label string
	Key   secret.Secret
}

// AIBudget overrides the provider's default budget; a zero field keeps the
// default (provider.DefaultBudget), and the provider checks the caps.
type AIBudget struct {
	MaxPromptBytes               int
	MaxOutputTokens              int64
	TimeoutSeconds               int
	AttemptTimeoutSeconds        int
	MaxRetries                   *int
	MaxConcurrentPerOrganization int
}

// CodeNotApproved: the variable names an approval this build does not
// record (internal/config/aiapproval).
const CodeNotApproved = "not_approved"

// Credential labels by variable (D-018: labels only in proposals,
// analyses and audit).
var aiKeys = []struct{ name, label string }{
	{"GEMINUS_ANTHROPIC_API_KEY_A", "org-a"},
	{"GEMINUS_ANTHROPIC_API_KEY_B", "org-b"},
}

// museKey is the Meta Model API key's variable (T-055: the name the owner
// gave it, H-033), labelled meta.
const (
	museKeyVar   = "GEMINUS_MUSE_API_KEY"
	museKeyLabel = "meta"
)

// liveSwitch is the operator's switch (T-053).
const liveSwitch = "GEMINUS_AI_LIVE"

// The photo path's variables (T-055).
const (
	visionProviderVar = "AI_VISION_PROVIDER"
	visionApprovalVar = "AI_VISION_POLICY_APPROVAL"
	visionModelVar    = "AI_VISION_MODEL"
)

// LoadAI reads the AI configuration. APP_ENV decides whether the fake is
// allowed. All problems are reported together, as in Load.
func LoadAI(getenv func(string) string) (AI, error) {
	var errs []error
	ai := AI{Provider: AIProviderDisabled}
	env := getenv("APP_ENV")
	approval := getenv("AI_POLICY_APPROVAL")
	fakeOK := env == "development" || env == "test"

	switch p := AIProvider(getenv("AI_PROVIDER")); p {
	case "", AIProviderDisabled:
	case AIProviderFake:
		ai.Provider = p
		if !fakeOK {
			errs = append(errs, &Error{"AI_PROVIDER", CodeInvalid,
				"the fake provider is for development and tests only: APP_ENV must be development or test"})
		}
	case AIProviderAnthropic, AIProviderMeta:
		ai.Provider = p
	default:
		errs = append(errs, &Error{"AI_PROVIDER", CodeInvalid, `unknown provider; allowed: "disabled", "fake", "anthropic", "meta"`})
	}
	if getenv("AI_API_KEY") != "" {
		errs = append(errs, &Error{"AI_API_KEY", CodeInvalid,
			"not read: the live adapters take GEMINUS_ANTHROPIC_API_KEY_A and _B, or GEMINUS_MUSE_API_KEY, and only once live AI is on (D-018)"})
	}

	on := false
	switch getenv(liveSwitch) {
	case "", "off":
	case "on":
		on = true
		if !ai.Provider.live() || approval == "" {
			errs = append(errs, &Error{liveSwitch, CodeInvalid, "live AI needs AI_PROVIDER=anthropic or meta and AI_POLICY_APPROVAL naming the owner's approval"})
			on = false
		}
	default:
		errs = append(errs, &Error{liveSwitch, CodeInvalid, `allowed: "on", "off" or unset`})
	}

	var budgets []int64
	switch {
	case approval == "" && ai.Provider.live():
		ai.LiveOff = LiveOffNoApproval
	case approval == "":
	case !ai.Provider.live():
		errs = append(errs, &Error{"AI_POLICY_APPROVAL", CodeInvalid, "must be empty unless AI_PROVIDER is anthropic or meta"})
	default:
		l, why, lerrs := loadLive(getenv, ai.Provider, approval, "AI_POLICY_APPROVAL", "AI_MODEL", liveSwitch, on, false)
		errs = append(errs, lerrs...)
		ai.LiveOff = why
		if why == "" && len(lerrs) == 0 && l.approval.Valid() {
			ai.Approval, ai.Model, ai.Credentials = l.approval, l.model, l.creds
			budgets = append(budgets, l.approval.MonthlyBudgetUSD())
		}
	}

	// The photo path (T-055): AI_VISION_PROVIDER, unset: AI_PROVIDER's.
	vp := AIProvider(getenv(visionProviderVar))
	vApproval, vModel := getenv(visionApprovalVar), getenv(visionModelVar)
	switch vp {
	case "":
		vp = ai.Provider
	case AIProviderDisabled, AIProviderAnthropic, AIProviderMeta:
	case AIProviderFake:
		if !fakeOK {
			errs = append(errs, &Error{visionProviderVar, CodeInvalid, "the fake provider is for development and tests only: APP_ENV must be development or test"})
		}
	default:
		errs = append(errs, &Error{visionProviderVar, CodeInvalid, `unknown provider; allowed: "disabled", "fake", "anthropic", "meta", or unset (AI_PROVIDER's)`})
		vp = AIProviderDisabled
	}
	ai.Vision.Provider = vp
	separate := vp.live() && vp != ai.Provider
	if !separate {
		for _, v := range []struct{ name, value string }{{visionApprovalVar, vApproval}, {visionModelVar, vModel}} {
			if v.value != "" {
				errs = append(errs, &Error{v.name, CodeInvalid, "must be empty unless AI_VISION_PROVIDER names a live provider other than AI_PROVIDER's"})
			}
		}
	}
	switch {
	case !vp.live():
	case !separate:
		// The proposals' provider: its approval, model and key, if its
		// approval names images.
		switch {
		case ai.LiveOff != "" || !ai.LiveEnabled():
			ai.Vision.LiveOff = ai.LiveOff
		case !ai.Approval.AllowsImages():
			ai.Vision.LiveOff = LiveOffImagesNotApproved
		default:
			ai.Vision.Approval, ai.Vision.Model, ai.Vision.Credentials = ai.Approval, ai.Model, ai.Credentials
			ai.Vision.SegmentationModel = ai.Approval.SegmentationModel()
		}
	case vApproval == "":
		ai.Vision.LiveOff = LiveOffNoApproval
		if on {
			errs = append(errs, &Error{visionApprovalVar, CodeRequired, "the photo path's provider needs the owner's approval of it"})
		}
	default:
		l, why, lerrs := loadLive(getenv, vp, vApproval, visionApprovalVar, visionModelVar, visionApprovalVar, on, true)
		errs = append(errs, lerrs...)
		ai.Vision.LiveOff = why
		if why == "" && len(lerrs) == 0 && l.approval.Valid() {
			ai.Vision.Approval, ai.Vision.Model, ai.Vision.Credentials = l.approval, l.model, l.creds
			ai.Vision.SegmentationModel = l.approval.SegmentationModel()
			budgets = append(budgets, l.approval.MonthlyBudgetUSD())
		}
	}

	// One ledger for every live provider (T-053's limits, H-029): the
	// month at most the smallest budget of the live approvals.
	if len(budgets) > 0 {
		limits, lerrs := loadLimits(getenv, slices.Min(budgets)*spend.NanoPerUSD)
		if errs = append(errs, lerrs...); len(lerrs) == 0 {
			ai.Limits = limits
		} else {
			ai.Approval, ai.Credentials, ai.Vision.Approval, ai.Vision.Credentials = aiapproval.Approval{}, nil, aiapproval.Approval{}, nil
		}
	}

	ints := []struct {
		name string
		set  func(int64)
	}{
		{"AI_MAX_PROMPT_BYTES", func(v int64) { ai.Budget.MaxPromptBytes = int(v) }},
		{"AI_MAX_OUTPUT_TOKENS", func(v int64) { ai.Budget.MaxOutputTokens = v }},
		{"AI_TIMEOUT_SECONDS", func(v int64) { ai.Budget.TimeoutSeconds = int(v) }},
		{"AI_ATTEMPT_TIMEOUT_SECONDS", func(v int64) { ai.Budget.AttemptTimeoutSeconds = int(v) }},
		{"AI_MAX_RETRIES", func(v int64) { n := int(v); ai.Budget.MaxRetries = &n }},
		{"AI_MAX_CONCURRENT_PER_ORGANIZATION", func(v int64) { ai.Budget.MaxConcurrentPerOrganization = int(v) }},
	}
	for _, c := range ints {
		raw := getenv(c.name)
		if raw == "" {
			continue
		}
		v, err := strconv.ParseInt(raw, 10, 32)
		if err != nil || v < 0 || (v == 0 && c.name != "AI_MAX_RETRIES") {
			errs = append(errs, &Error{c.name, CodeInvalid, "must be a positive integer (AI_MAX_RETRIES may be 0)"})
			continue
		}
		c.set(v)
	}

	if len(errs) > 0 {
		return AI{}, errors.Join(errs...)
	}
	return ai, nil
}

// live is a provider's live configuration: the approval token, the model,
// the keys.
type live struct {
	approval aiapproval.Approval
	model    string
	creds    []AICredential
}

// loadLive checks the live configuration of provider p: the approval
// approvalVar names (approval), the model modelVar names, and, once every
// condition holds and the switch is on, p's keys. why is the typed reason
// live AI is off for p (empty when on); a condition that fails while the
// switch is on is an error, named after the variable (the record's gaps
// and the price after switchVar). With images, an approval whose data
// policy names no image is off (LiveOffImagesNotApproved), never an error,
// and no key is read.
func loadLive(getenv func(string) string, p AIProvider, approval, approvalVar, modelVar, switchVar string, on, images bool) (live, string, []error) {
	var errs []error
	rec, ok := aiapproval.Lookup(approval)
	if !ok {
		return live{}, "", []error{&Error{approvalVar, CodeNotApproved,
			"this build records no such approval of live AI; the owner records one (model, budget, data policy) before any live call (H-001, H-017, H-018)"}}
	}
	model := getenv(modelVar)
	_, priced := spend.PriceOf(rec.Model)
	if rec.SegmentationModel != "" {
		_, imagePriced := spend.ImagePriceOf(rec.SegmentationModel)
		priced = priced && imagePriced
	}
	var why string
	switch {
	case rec.Provider != string(p):
		why = LiveOffProviderMismatch
		if on {
			errs = append(errs, &Error{approvalVar, why, "names an approval of another provider"})
		}
	case rec.Gap() != "":
		why = string(rec.Gap())
		if on {
			errs = append(errs, &Error{switchVar, why, "the recorded approval lacks something before live AI can be on (internal/config/aiapproval)"})
		}
	case model != rec.Model:
		why = LiveOffModelMismatch
		if on && model == "" {
			errs = append(errs, &Error{modelVar, CodeRequired, "live AI needs the model the owner approved; there is no default"})
		} else if on {
			errs = append(errs, &Error{modelVar, LiveOffModelMismatch, "must be exactly the model of the recorded approval"})
		}
	case !priced:
		why = LiveOffModelUnpriced
		if on {
			errs = append(errs, &Error{switchVar, why, "this build has no price for the approved model, so no call can be reserved for (internal/ai/spend)"})
		}
	case images && rec.DataPolicy.Inputs != aiapproval.InputsSyntheticDemoImagesAndUserText:
		why = LiveOffImagesNotApproved
	case !on:
		why = LiveOffSwitch
	}
	if why != "" {
		return live{}, why, errs
	}
	a, ok := aiapproval.Recorded(approval)
	if !ok { // Gap was "": Recorded agrees, unless the record changed meanwhile
		return live{}, "", []error{&Error{approvalVar, CodeNotApproved, "the recorded approval cannot be used"}}
	}
	var creds []AICredential
	var cerrs []error
	switch p {
	case AIProviderAnthropic:
		creds, cerrs = loadKeys(getenv)
	case AIProviderMeta:
		creds, cerrs = loadMuseKey(getenv)
	}
	if len(cerrs) > 0 {
		return live{}, "", cerrs
	}
	return live{approval: a, model: model, creds: creds}, "", nil
}

// loadMuseKey reads the Meta Model API key (T-055): present, clean (as
// loadKeys checks), and in the documented shape: "API keys start with
// LLM_, followed by a numeric ID and the secret"
// (https://dev.meta.ai/docs/authentication), so an Anthropic key set in
// its place is refused. The error names the variable, never a value.
func loadMuseKey(getenv func(string) string) ([]AICredential, []error) {
	v := getenv(museKeyVar)
	switch {
	case v == "":
		return nil, []error{&Error{museKeyVar, CodeRequired, "live AI on the Meta Model API needs the owner's key"}}
	case !cleanKey(v):
		return nil, []error{&Error{museKeyVar, CodeInvalid, "has white space around it or a control character in it; set the key exactly"}}
	case !museKeyShape.MatchString(v):
		return nil, []error{&Error{museKeyVar, CodeInvalid, "is not a Meta Model API key (LLM_<id>_<secret>)"}}
	}
	return []AICredential{{Label: museKeyLabel, Key: secret.Secret(v)}}, nil
}

// museKeyShape is the documented key shape, LLM_ and the numeric ID and
// the secret, separated by underscores.
var museKeyShape = regexp.MustCompile(`^LLM_[0-9]+_\S+$`)

// loadKeys reads both credentials (T-053: both provider organizations of
// D-018). A key with white space around it or a control character in it
// is refused (fail closed: review round 1, L2) before the two are
// compared, so the same key with a trailing newline from a secret file, or
// a pasted space, is never taken for a second organization's. Errors name
// the variable, never a value.
func loadKeys(getenv func(string) string) ([]AICredential, []error) {
	var creds []AICredential
	var errs []error
	for _, k := range aiKeys {
		v := getenv(k.name)
		switch {
		case v == "":
			errs = append(errs, &Error{k.name, CodeRequired, "live AI needs both provider organizations' keys (D-018)"})
		case !cleanKey(v):
			errs = append(errs, &Error{k.name, CodeInvalid, "has white space around it or a control character in it; set the key exactly"})
		case len(creds) > 0 && creds[0].Key.Reveal() == v:
			errs = append(errs, &Error{k.name, CodeInvalid, "the secondary key is the primary key: one organization, not two"})
		default:
			creds = append(creds, AICredential{Label: k.label, Key: secret.Secret(v)})
		}
	}
	return creds, errs
}

// cleanKey reports whether v has no white space at either end and no
// control character anywhere (C0, DEL, C1) and is valid UTF-8.
func cleanKey(v string) bool {
	if !utf8.ValidString(v) || strings.TrimFunc(v, unicode.IsSpace) != v {
		return false
	}
	return !strings.ContainsFunc(v, unicode.IsControl)
}

// loadLimits reads the spend limits (D-110): the monthly limit at most the
// approval's budget (its default), the daily limits by default
// spend.DefaultLimits, each within the one above it.
func loadLimits(getenv func(string) string, budget spend.NanoUSD) (spend.Limits, []error) {
	var errs []error
	read := func(name string, def spend.NanoUSD) spend.NanoUSD {
		raw := getenv(name)
		if raw == "" {
			return def
		}
		v, err := spend.ParseUSD(raw)
		if err != nil {
			errs = append(errs, &Error{name, CodeInvalid, "must be a positive amount of US dollars, such as 10 or 2.5"})
			return def
		}
		return v
	}
	l := spend.Limits{Month: read("AI_MONTHLY_LIMIT_USD", budget)}
	if l.Month > budget {
		errs = append(errs, &Error{"AI_MONTHLY_LIMIT_USD", CodeInvalid, "may not exceed the approval's monthly budget"})
		l.Month = budget
	}
	def := spend.DefaultLimits(l.Month)
	l.Day = read("AI_DAILY_LIMIT_USD", def.Day)
	if l.Day > l.Month {
		errs = append(errs, &Error{"AI_DAILY_LIMIT_USD", CodeInvalid, "may not exceed the monthly limit"})
		l.Day = l.Month
	}
	l.OrganizationDay = read("AI_ORGANIZATION_DAILY_LIMIT_USD", min(def.OrganizationDay, l.Day))
	if l.OrganizationDay > l.Day {
		errs = append(errs, &Error{"AI_ORGANIZATION_DAILY_LIMIT_USD", CodeInvalid, "may not exceed the deployment's daily limit"})
	}
	if len(errs) == 0 {
		if err := l.Validate(); err != nil {
			errs = append(errs, &Error{"AI_MONTHLY_LIMIT_USD", CodeInvalid, err.Error()})
		}
	}
	return l, errs
}
