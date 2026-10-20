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
	"path/filepath"
)

// AIProvider selects the LLM provider adapter.
type AIProvider string

const (
	AIProviderDisabled AIProvider = "disabled"
	// AIProviderFake is the deterministic fake provider (T-024), for
	// development and tests only.
	AIProviderFake AIProvider = "fake"
	// AIProviderAnthropic is the provider chosen by the repository owner on
	// 2026-10-15 (docs/DECISIONS_AND_TBD.md, H-001). The adapter is T-024;
	// it is called only once the owner's approval of model, token quota and
	// data policy is recorded (LoadAI): until then every AI call fails with
	// AI_POLICY_NOT_APPROVED.
	AIProviderAnthropic AIProvider = "anthropic"
)

// Config is the validated configuration shared by the API and the worker.
type Config struct {
	DatabaseURL string
	AIProvider  AIProvider
	// AILive reports whether the live provider is selected and approved
	// (AI.LiveEnabled): only then may an AI request reach it. The API
	// refuses new AI requests otherwise (T-025), without reading any key.
	AILive bool
	// Env is APP_ENV: "development", "test", "production", or "" (unset).
	Env           string
	ObjectStorage ObjectStorage
}

// ObjectStorageProvider selects the object storage adapter (D-044).
type ObjectStorageProvider string

const (
	ObjectStorageDisabled ObjectStorageProvider = "disabled"
	// ObjectStorageFilesystem keeps objects as files under Root. It is for
	// development and tests only (D-044): it needs APP_ENV development or
	// test and is refused for production (and when APP_ENV is unset, so a
	// deployment that forgot it fails closed).
	ObjectStorageFilesystem ObjectStorageProvider = "filesystem"
)

// ObjectStorage is where private artifacts and uploads are kept. A
// production adapter waits for the owner's cloud choice (D-044).
type ObjectStorage struct {
	Provider ObjectStorageProvider
	Root     string // filesystem: an absolute directory
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

	// The AI configuration (ai.go); its problems are reported with the rest.
	if ai, err := LoadAI(getenv); err != nil {
		var joined interface{ Unwrap() []error }
		if errors.As(err, &joined) {
			errs = append(errs, joined.Unwrap()...)
		} else {
			errs = append(errs, err)
		}
	} else {
		cfg.AIProvider, cfg.AILive = ai.Provider, ai.LiveEnabled()
	}

	switch cfg.Env = getenv("APP_ENV"); cfg.Env {
	case "", "development", "test", "production":
	default:
		errs = append(errs, &Error{"APP_ENV", CodeInvalid, `allowed: "development", "test", "production"`})
	}

	cfg.ObjectStorage.Provider = ObjectStorageDisabled
	switch p := ObjectStorageProvider(getenv("OBJECT_STORAGE_PROVIDER")); p {
	case "", ObjectStorageDisabled:
		if getenv("OBJECT_STORAGE_ROOT") != "" {
			errs = append(errs, &Error{"OBJECT_STORAGE_ROOT", CodeInvalid, "must be empty while object storage is disabled"})
		}
	case ObjectStorageFilesystem:
		cfg.ObjectStorage = ObjectStorage{Provider: p, Root: getenv("OBJECT_STORAGE_ROOT")}
		if cfg.Env != "development" && cfg.Env != "test" {
			errs = append(errs, &Error{"OBJECT_STORAGE_PROVIDER", CodeInvalid,
				"the filesystem adapter is for development and tests only: APP_ENV must be development or test (D-044)"})
		}
		if r := cfg.ObjectStorage.Root; r == "" {
			errs = append(errs, &Error{"OBJECT_STORAGE_ROOT", CodeRequired, "the filesystem adapter needs its root directory"})
		} else if !filepath.IsAbs(r) || filepath.Clean(r) != r {
			errs = append(errs, &Error{"OBJECT_STORAGE_ROOT", CodeInvalid, "must be a clean absolute path"})
		}
	default:
		errs = append(errs, &Error{"OBJECT_STORAGE_PROVIDER", CodeNotImplemented,
			`the production object storage provider waits for the owner's cloud choice (D-044); allowed: "disabled", "filesystem"`})
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
