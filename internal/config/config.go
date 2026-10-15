// Package config loads process configuration for cmd/api and cmd/worker from
// the environment. It parses and validates only; it never opens connections.
//
// Integrations whose technology or policy is still open in
// docs/DECISIONS_AND_TBD.md are rejected rather than defaulted, so a
// deployment cannot silently enable a live provider. Error messages never
// include configured values, because several of them carry credentials.
package config

import (
	"errors"
	"net/url"
)

// AIProvider selects the LLM provider adapter.
type AIProvider string

const (
	AIProviderDisabled AIProvider = "disabled"
	// AIProviderAnthropic is the provider chosen by the repository owner on
	// 2026-10-15 (docs/DECISIONS_AND_TBD.md, H-001). It is recognised but not
	// yet accepted: the adapter is T-024 and live use is gated by D-018, the
	// data policy and token quotas.
	AIProviderAnthropic AIProvider = "anthropic"
)

// Config is the validated configuration shared by the API and the worker.
type Config struct {
	DatabaseURL string
	AIProvider  AIProvider
}

// Error codes returned in Error.Code.
const (
	CodeRequired       = "required"
	CodeInvalid        = "invalid"
	CodeNotImplemented = "not_implemented"
)

// Error describes one rejected environment variable.
type Error struct {
	Var  string
	Code string
	Msg  string
}

func (e *Error) Error() string { return e.Var + " (" + e.Code + "): " + e.Msg }

// Load reads and validates the configuration. All problems are reported
// together, joined with errors.Join, each as an *Error.
func Load(getenv func(string) string) (Config, error) {
	var errs []error
	cfg := Config{DatabaseURL: getenv("DATABASE_URL")}

	if err := checkDatabaseURL(cfg.DatabaseURL); err != nil {
		errs = append(errs, err)
	}

	switch p := AIProvider(getenv("AI_PROVIDER")); p {
	case "", AIProviderDisabled:
		cfg.AIProvider = AIProviderDisabled
	case AIProviderAnthropic:
		errs = append(errs, &Error{"AI_PROVIDER", CodeNotImplemented,
			"anthropic adapter is delivered by T-024; live AI also needs D-018, an approved data policy and token quotas"})
	default:
		errs = append(errs, &Error{"AI_PROVIDER", CodeInvalid, `unknown provider; allowed: "disabled"`})
	}

	if cfg.AIProvider == AIProviderDisabled && getenv("AI_API_KEY") != "" {
		errs = append(errs, &Error{"AI_API_KEY", CodeInvalid, "must be empty while AI_PROVIDER is disabled"})
	}

	switch getenv("OBJECT_STORAGE_PROVIDER") {
	case "", "disabled":
	default:
		errs = append(errs, &Error{"OBJECT_STORAGE_PROVIDER", CodeNotImplemented,
			"object storage provider is TBD and no adapter exists yet (T-016)"})
	}

	if len(errs) > 0 {
		return Config{}, errors.Join(errs...)
	}
	return cfg, nil
}

func checkDatabaseURL(raw string) error {
	if raw == "" {
		return &Error{"DATABASE_URL", CodeRequired, "a PostgreSQL connection URL is required"}
	}
	u, err := url.Parse(raw)
	if err != nil {
		return &Error{"DATABASE_URL", CodeInvalid, "not a parseable URL"}
	}
	if u.Scheme != "postgres" && u.Scheme != "postgresql" {
		return &Error{"DATABASE_URL", CodeInvalid, "scheme must be postgres or postgresql"}
	}
	if u.Host == "" && u.Query().Get("host") == "" {
		return &Error{"DATABASE_URL", CodeInvalid, "a host or host= socket directory is required"}
	}
	if u.Path == "" || u.Path == "/" {
		return &Error{"DATABASE_URL", CodeInvalid, "a database name is required"}
	}
	return nil
}
