package config

import (
	"errors"
	"strings"

	"github.com/kcl1104/geminus/internal/auth"
)

// LoadAuth reads the API's OIDC provider (T-008). Endpoints come only from
// these variables, the deployment allowlist of docs/SECURITY.md §3; nothing
// is discovered from the issuer or taken from a token.
//
//	AUTH_OIDC_ISSUER      exact iss value (https URL)
//	AUTH_OIDC_AUDIENCE    the API's audience
//	AUTH_OIDC_JWKS_URL    https URL of the provider's JWKS
//	AUTH_OIDC_ALGORITHMS  optional comma-separated allowlist (default RS256,ES256)
//	AUTH_OIDC_REQUIRE_AT_JWT  optional "true": only tokens typed at+jwt (RFC 9068)
//
// It does not contact the provider. Messages never include the values.
func LoadAuth(getenv func(string) string) (auth.Config, error) {
	var errs []error
	cfg := auth.Config{Issuer: getenv("AUTH_OIDC_ISSUER"), Audience: getenv("AUTH_OIDC_AUDIENCE"), JWKSURL: getenv("AUTH_OIDC_JWKS_URL")}
	for _, v := range []struct{ name, value string }{
		{"AUTH_OIDC_ISSUER", cfg.Issuer}, {"AUTH_OIDC_AUDIENCE", cfg.Audience}, {"AUTH_OIDC_JWKS_URL", cfg.JWKSURL},
	} {
		if v.value == "" {
			errs = append(errs, &Error{v.name, CodeRequired, "required: the API verifies OIDC access tokens (T-008)"})
		}
	}
	if algs := getenv("AUTH_OIDC_ALGORITHMS"); algs != "" {
		for _, a := range strings.Split(algs, ",") {
			cfg.Algorithms = append(cfg.Algorithms, strings.TrimSpace(a))
		}
	}
	switch getenv("AUTH_OIDC_REQUIRE_AT_JWT") {
	case "", "false":
	case "true":
		cfg.RequireAccessTokenType = true
	default:
		errs = append(errs, &Error{"AUTH_OIDC_REQUIRE_AT_JWT", CodeInvalid, `"true", "false" or unset`})
	}
	if len(errs) > 0 {
		return auth.Config{}, errors.Join(errs...)
	}
	if err := cfg.Validate(); err != nil {
		return auth.Config{}, errors.Join(&Error{"AUTH_OIDC_*", CodeInvalid, err.Error()})
	}
	return cfg, nil
}
