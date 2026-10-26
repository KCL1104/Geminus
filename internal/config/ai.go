package config

import (
	"errors"
	"strconv"

	"github.com/kcl1104/geminus/internal/ai/spend"
	"github.com/kcl1104/geminus/internal/config/aiapproval"
	"github.com/kcl1104/geminus/internal/secret"
)

// AI is the LLM provider configuration (T-024, T-053; H-001, H-017, H-018,
// D-018).
//
// Live AI stays off unless all of these hold (T-053):
//
//   - AI_PROVIDER=anthropic;
//   - AI_POLICY_APPROVAL names an approval this build records
//     (internal/config/aiapproval), and the record lacks nothing: H-018 is
//     recorded, complete since the owner confirmed the provider
//     organizations' data retention arrangement (H-026);
//   - AI_MODEL is exactly the record's model, and this build prices it
//     (internal/ai/spend);
//   - the operator's switch GEMINUS_AI_LIVE=on;
//   - both keys, GEMINUS_ANTHROPIC_API_KEY_A and _B (two different ones).
//
// Otherwise:
//
//   - AI_PROVIDER=disabled (the default): every AI call fails with
//     AI_PROVIDER_NOT_CONFIGURED;
//   - AI_PROVIDER=anthropic with the switch not on: every AI call fails
//     with AI_POLICY_NOT_APPROVED, no key is read, and LiveOff says why
//     (no approval named, what the record lacks, the model, the switch);
//   - the switch on while any condition fails, or AI_POLICY_APPROVAL
//     naming an approval this build does not record: the process refuses
//     to start, with the condition's typed code.
//
// AI_PROVIDER=fake selects the deterministic fake provider, for
// development and tests only (APP_ENV development or test).
//
// Keys are read only once live AI is on, only from
// GEMINUS_ANTHROPIC_API_KEY_A (primary, label org-a) and
// GEMINUS_ANTHROPIC_API_KEY_B (secondary, label org-b), and handed to the
// adapter explicitly; nothing reads ANTHROPIC_API_KEY (D-018). A key is a
// secret.Secret: it prints as [redacted], and everything else names it by
// label.
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
	// Limits are the spend limits (D-110): set once live AI is on.
	Limits spend.Limits
	// LiveOff is why live AI is off while AI_PROVIDER is anthropic: one of
	// the LiveOff* reasons, or what the record lacks (an
	// aiapproval.Reason). Empty when live AI is on or not selected.
	LiveOff string
}

// Why live AI is off besides what a record lacks (aiapproval.Reason).
const (
	// LiveOffNoApproval: AI_POLICY_APPROVAL is empty.
	LiveOffNoApproval = "no_approval_named"
	// LiveOffModelMismatch: AI_MODEL is not the approval's model.
	LiveOffModelMismatch = "model_mismatch"
	// LiveOffModelUnpriced: the approval's model has no price in this build
	// (internal/ai/spend), so no call can be reserved for.
	LiveOffModelUnpriced = "model_unpriced"
	// LiveOffSwitch: GEMINUS_AI_LIVE is not on.
	LiveOffSwitch = "switch_off"
)

// LiveEnabled reports whether the live provider may be called.
func (a AI) LiveEnabled() bool { return a.Provider == AIProviderAnthropic && a.Approval.Valid() }

// AICredential is one API key and the label that proposals, analyses and
// audit name it by ("org-a", "org-b").
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

// liveSwitch is the operator's switch (T-053).
const liveSwitch = "GEMINUS_AI_LIVE"

// LoadAI reads the AI configuration. APP_ENV decides whether the fake is
// allowed. All problems are reported together, as in Load.
func LoadAI(getenv func(string) string) (AI, error) {
	var errs []error
	ai := AI{Provider: AIProviderDisabled}
	env := getenv("APP_ENV")
	approval := getenv("AI_POLICY_APPROVAL")

	switch p := AIProvider(getenv("AI_PROVIDER")); p {
	case "", AIProviderDisabled:
	case AIProviderFake:
		ai.Provider = p
		if env != "development" && env != "test" {
			errs = append(errs, &Error{"AI_PROVIDER", CodeInvalid,
				"the fake provider is for development and tests only: APP_ENV must be development or test"})
		}
	case AIProviderAnthropic:
		ai.Provider = p
	default:
		errs = append(errs, &Error{"AI_PROVIDER", CodeInvalid, `unknown provider; allowed: "disabled", "fake", "anthropic"`})
	}
	if getenv("AI_API_KEY") != "" {
		errs = append(errs, &Error{"AI_API_KEY", CodeInvalid,
			"not read: the live adapter takes GEMINUS_ANTHROPIC_API_KEY_A and _B, and only once live AI is on (D-018)"})
	}

	on := false
	switch getenv(liveSwitch) {
	case "", "off":
	case "on":
		on = true
		if ai.Provider != AIProviderAnthropic || approval == "" {
			errs = append(errs, &Error{liveSwitch, CodeInvalid, "live AI needs AI_PROVIDER=anthropic and AI_POLICY_APPROVAL naming the owner's approval"})
			on = false
		}
	default:
		errs = append(errs, &Error{liveSwitch, CodeInvalid, `allowed: "on", "off" or unset`})
	}

	switch {
	case approval == "" && ai.Provider == AIProviderAnthropic:
		ai.LiveOff = LiveOffNoApproval
	case approval == "":
	case ai.Provider != AIProviderAnthropic:
		errs = append(errs, &Error{"AI_POLICY_APPROVAL", CodeInvalid, "must be empty unless AI_PROVIDER is anthropic"})
	default:
		rec, ok := aiapproval.Lookup(approval)
		if !ok {
			errs = append(errs, &Error{"AI_POLICY_APPROVAL", CodeNotApproved,
				"this build records no such approval of live AI; the owner records one (model, budget, data policy) before any live call (H-001, H-017, H-018)"})
			break
		}
		model := getenv("AI_MODEL")
		_, priced := spend.PriceOf(rec.Model)
		var why string
		switch {
		case rec.Gap() != "":
			why = string(rec.Gap())
			if on {
				errs = append(errs, &Error{liveSwitch, why, "the recorded approval lacks something before live AI can be on (internal/config/aiapproval)"})
			}
		case model != rec.Model:
			why = LiveOffModelMismatch
			if on && model == "" {
				errs = append(errs, &Error{"AI_MODEL", CodeRequired, "live AI needs the model the owner approved; there is no default"})
			} else if on {
				errs = append(errs, &Error{"AI_MODEL", LiveOffModelMismatch, "must be exactly the model of the recorded approval"})
			}
		case !priced:
			why = LiveOffModelUnpriced
			if on {
				errs = append(errs, &Error{liveSwitch, why, "this build has no price for the approved model, so no call can be reserved for (internal/ai/spend)"})
			}
		case !on:
			why = LiveOffSwitch
		}
		if why != "" {
			ai.LiveOff = why
			break
		}
		a, ok := aiapproval.Recorded(approval)
		if !ok { // Gap was "": Recorded agrees, unless the record changed meanwhile
			errs = append(errs, &Error{"AI_POLICY_APPROVAL", CodeNotApproved, "the recorded approval cannot be used"})
			break
		}
		ai.Model = model
		creds, cerrs := loadKeys(getenv)
		limits, lerrs := loadLimits(getenv, a.MonthlyBudgetUSD()*spend.NanoPerUSD)
		if errs = append(append(errs, cerrs...), lerrs...); len(cerrs)+len(lerrs) == 0 {
			ai.Approval, ai.Credentials, ai.Limits = a, creds, limits
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

// loadKeys reads both credentials (T-053: both provider organizations of
// D-018). Errors name the variable, never a value.
func loadKeys(getenv func(string) string) ([]AICredential, []error) {
	var creds []AICredential
	var errs []error
	for _, k := range aiKeys {
		v := getenv(k.name)
		switch {
		case v == "":
			errs = append(errs, &Error{k.name, CodeRequired, "live AI needs both provider organizations' keys (D-018)"})
		case len(creds) > 0 && creds[0].Key.Reveal() == v:
			errs = append(errs, &Error{k.name, CodeInvalid, "the secondary key is the primary key: one organization, not two"})
		default:
			creds = append(creds, AICredential{Label: k.label, Key: secret.Secret(v)})
		}
	}
	return creds, errs
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
