package config

import (
	"errors"
	"strconv"

	"github.com/kcl1104/geminus/internal/config/aiapproval"
	"github.com/kcl1104/geminus/internal/secret"
)

// AI is the LLM provider configuration (T-024; H-001, D-018, H-017).
//
// Live AI stays off unless a deployment selects it and names the owner's
// recorded approval of model, token quota and data policy, and this build
// records that approval (internal/config/aiapproval, empty until the owner
// records one in a reviewed change). Without it:
//
//   - AI_PROVIDER=disabled (the default): every AI call fails with
//     AI_PROVIDER_NOT_CONFIGURED;
//   - AI_PROVIDER=anthropic and no AI_POLICY_APPROVAL: every AI call fails
//     with AI_POLICY_NOT_APPROVED, and no key is read;
//   - AI_POLICY_APPROVAL naming an approval this build does not list: the
//     process refuses to start.
//
// AI_PROVIDER=fake selects the deterministic fake provider, for
// development and tests only (APP_ENV development or test).
//
// Keys are read only for an approved live provider, only from
// GEMINUS_ANTHROPIC_API_KEY_A (primary, required) and
// GEMINUS_ANTHROPIC_API_KEY_B (secondary, optional), and handed to the
// adapter explicitly; nothing reads ANTHROPIC_API_KEY (D-018). A key is a
// secret.Secret: it prints as [redacted], and everything else names it by
// label.
type AI struct {
	Provider AIProvider
	// Approval is the recorded approval AI_POLICY_APPROVAL names; the zero
	// value unless live AI is approved. Only aiapproval.Recorded makes one,
	// and the live adapter takes nothing else (review L5).
	Approval aiapproval.Approval
	// Model is AI_MODEL: required for live AI and never defaulted (the TBD
	// "LLM model" is the owner's choice after an eval).
	Model string
	// Credentials in failover order, primary first.
	Credentials []AICredential
	Budget      AIBudget
}

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
			"not read: the live adapter takes GEMINUS_ANTHROPIC_API_KEY_A and _B, and only once live AI is approved (D-018)"})
	}

	switch {
	case approval == "":
	case ai.Provider != AIProviderAnthropic:
		errs = append(errs, &Error{"AI_POLICY_APPROVAL", CodeInvalid, "must be empty unless AI_PROVIDER is anthropic"})
	default:
		a, ok := aiapproval.Recorded(approval)
		if !ok {
			errs = append(errs, &Error{"AI_POLICY_APPROVAL", CodeNotApproved,
				"this build records no such approval of live AI; the owner records one (model, token quota, data policy) before any live call (H-001, H-017)"})
			break
		}
		ai.Approval = a
	}

	if ai.LiveEnabled() {
		if ai.Model = getenv("AI_MODEL"); ai.Model == "" {
			errs = append(errs, &Error{"AI_MODEL", CodeRequired, "live AI needs the model the owner approved; there is no default"})
		}
		for i, k := range aiKeys {
			v := getenv(k.name)
			switch {
			case v == "" && i == 0:
				errs = append(errs, &Error{k.name, CodeRequired, "the primary key is required for live AI"})
			case v == "":
			default:
				ai.Credentials = append(ai.Credentials, AICredential{Label: k.label, Key: secret.Secret(v)})
			}
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
