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

	"github.com/kcl1104/geminus/internal/secret"
	"github.com/kcl1104/geminus/internal/storage/objects"
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
	// AIProviderMeta is the Meta Model API (Muse Spark, SAM 3.1), chosen by
	// the repository owner in H-029 and H-033. The adapter is T-055; like
	// Anthropic it is called only once LoadAI finds its recorded approval,
	// the model, the switch and the key.
	AIProviderMeta AIProvider = "meta"
)

// live reports whether p is a provider that calls a network model, as
// opposed to disabled and the fake.
func (p AIProvider) live() bool { return p == AIProviderAnthropic || p == AIProviderMeta }

// Config is the validated configuration shared by the API and the worker.
type Config struct {
	DatabaseURL string
	AIProvider  AIProvider
	// AILive reports whether the live provider is selected and approved:
	// Load, the worker's, AI.LiveEnabled (its keys read); LoadAPI, the
	// API's, APIAI.LiveEnabled (the same gate, no key; T-053 I5). Only then
	// may an AI request reach the provider; the API refuses new AI requests
	// otherwise (T-025).
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
	// ObjectStorageS3 is a private S3-compatible bucket (T-039, ADR-029):
	// the deployment's object storage, in any APP_ENV.
	ObjectStorageS3 ObjectStorageProvider = "s3"
)

// ObjectStorage is where private artifacts and uploads are kept (D-044).
type ObjectStorage struct {
	Provider ObjectStorageProvider
	Root     string // filesystem: an absolute directory
	S3       S3Storage
}

// S3Storage is the bucket of OBJECT_STORAGE_PROVIDER=s3: the
// OBJECT_STORAGE_S3_* variables (internal/storage/objects.S3Config checks
// the same rules). The secret prints as [redacted].
type S3Storage struct {
	Endpoint        string // OBJECT_STORAGE_S3_ENDPOINT: an https origin
	Bucket          string // OBJECT_STORAGE_S3_BUCKET
	Region          string // OBJECT_STORAGE_S3_REGION ("auto" for Railway's buckets)
	AccessKeyID     string // OBJECT_STORAGE_S3_ACCESS_KEY_ID
	SecretAccessKey secret.Secret
	PathStyle       bool // OBJECT_STORAGE_S3_URL_STYLE=path (default virtual-hosted)
}

// s3Vars are the bucket's variables, in the order problems are reported.
var s3Vars = []string{
	"OBJECT_STORAGE_S3_ENDPOINT", "OBJECT_STORAGE_S3_BUCKET", "OBJECT_STORAGE_S3_REGION",
	"OBJECT_STORAGE_S3_ACCESS_KEY_ID", "OBJECT_STORAGE_S3_SECRET_ACCESS_KEY", "OBJECT_STORAGE_S3_URL_STYLE",
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

// Load reads and validates the configuration of the worker: its AI part is
// LoadAI's (the keys read once live AI is on). All problems are reported
// together, joined with errors.Join, each as an *Error.
func Load(getenv func(string) string) (Config, error) {
	return load(getenv, func() (AIProvider, bool, error) {
		ai, err := LoadAI(getenv)
		return ai.Provider, ai.LiveEnabled(), err
	})
}

// LoadAPI is Load for the API (T-053 I5): its AI part is LoadAPIAI's, which
// never reads a key and refuses one.
func LoadAPI(getenv func(string) string) (Config, error) {
	return load(getenv, func() (AIProvider, bool, error) {
		ai, err := LoadAPIAI(getenv)
		return ai.Provider, ai.LiveEnabled(), err
	})
}

// load is Load with the AI part loadAI reads.
func load(getenv func(string) string, loadAI func() (AIProvider, bool, error)) (Config, error) {
	var errs []error
	cfg := Config{DatabaseURL: getenv("DATABASE_URL")}

	if err := checkDatabaseURL(cfg.DatabaseURL); err != nil {
		errs = append(errs, err)
	}

	// The AI configuration (ai.go, apiai.go); its problems are reported with
	// the rest.
	if p, live, err := loadAI(); err != nil {
		var joined interface{ Unwrap() []error }
		if errors.As(err, &joined) {
			errs = append(errs, joined.Unwrap()...)
		} else {
			errs = append(errs, err)
		}
	} else {
		cfg.AIProvider, cfg.AILive = p, live
	}

	switch cfg.Env = getenv("APP_ENV"); cfg.Env {
	case "", "development", "test", "production":
	default:
		errs = append(errs, &Error{"APP_ENV", CodeInvalid, `allowed: "development", "test", "production"`})
	}

	cfg.ObjectStorage.Provider = ObjectStorageDisabled
	p := ObjectStorageProvider(getenv("OBJECT_STORAGE_PROVIDER"))
	if p != ObjectStorageS3 {
		// A bucket's variables without its provider are a half-made
		// configuration (T-039): refused, never ignored.
		for _, name := range s3Vars {
			if getenv(name) != "" {
				errs = append(errs, &Error{name, CodeInvalid, "must be empty unless OBJECT_STORAGE_PROVIDER is s3"})
			}
		}
	}
	switch p {
	case ObjectStorageS3:
		s3, s3errs := loadS3(getenv)
		cfg.ObjectStorage = ObjectStorage{Provider: p, S3: s3}
		errs = append(errs, s3errs...)
		if getenv("OBJECT_STORAGE_ROOT") != "" {
			errs = append(errs, &Error{"OBJECT_STORAGE_ROOT", CodeInvalid, "must be empty with the s3 provider"})
		}
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
			`no such object storage adapter (D-044); allowed: "disabled", "filesystem", "s3"`})
	}

	if len(errs) > 0 {
		return Config{}, errors.Join(errs...)
	}
	return cfg, nil
}

// loadS3 reads the bucket of OBJECT_STORAGE_PROVIDER=s3 with the adapter's
// rules (internal/storage/objects); messages name the variable only.
func loadS3(getenv func(string) string) (S3Storage, []error) {
	var errs []error
	s := S3Storage{Endpoint: getenv("OBJECT_STORAGE_S3_ENDPOINT"), Bucket: getenv("OBJECT_STORAGE_S3_BUCKET"),
		Region: getenv("OBJECT_STORAGE_S3_REGION"), AccessKeyID: getenv("OBJECT_STORAGE_S3_ACCESS_KEY_ID"),
		SecretAccessKey: secret.Secret(getenv("OBJECT_STORAGE_S3_SECRET_ACCESS_KEY"))}
	check := func(name, value string, ok bool, why string) {
		switch {
		case value == "":
			errs = append(errs, &Error{name, CodeRequired, "the s3 object storage needs it"})
		case !ok:
			errs = append(errs, &Error{name, CodeInvalid, why})
		}
	}
	check("OBJECT_STORAGE_S3_ENDPOINT", s.Endpoint, objects.CheckS3Endpoint(s.Endpoint) == nil,
		"must be an https origin (scheme and host, no path, credentials, query or fragment); plain http only on loopback")
	check("OBJECT_STORAGE_S3_BUCKET", s.Bucket, objects.ValidS3Bucket(s.Bucket),
		"must be the bucket's S3 name: 3-63 lowercase letters, digits and '-', starting and ending with a letter or digit")
	check("OBJECT_STORAGE_S3_REGION", s.Region, objects.ValidS3Region(s.Region), "must be lowercase letters, digits and '-'")
	check("OBJECT_STORAGE_S3_ACCESS_KEY_ID", s.AccessKeyID, cleanKey(s.AccessKeyID) && len(s.AccessKeyID) <= 256,
		"has white space around it or a control character in it, or is longer than 256 bytes; set it exactly")
	check("OBJECT_STORAGE_S3_SECRET_ACCESS_KEY", s.SecretAccessKey.Reveal(), cleanKey(s.SecretAccessKey.Reveal()) && len(s.SecretAccessKey.Reveal()) <= 256,
		"has white space around it or a control character in it, or is longer than 256 bytes; set it exactly")
	switch getenv("OBJECT_STORAGE_S3_URL_STYLE") {
	case "", "virtual-hosted":
	case "path":
		s.PathStyle = true
	default:
		errs = append(errs, &Error{"OBJECT_STORAGE_S3_URL_STYLE", CodeInvalid, `allowed: "virtual-hosted" (the default) or "path"`})
	}
	return s, errs
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
