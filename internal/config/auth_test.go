package config

import (
	"slices"
	"strings"
	"testing"
)

var validAuth = map[string]string{
	"AUTH_OIDC_ISSUER":   "https://id.example/realms/geminus",
	"AUTH_OIDC_AUDIENCE": "https://api.geminus.example",
	"AUTH_OIDC_JWKS_URL": "https://id.example/realms/geminus/protocol/openid-connect/certs",
}

func withEnv(base map[string]string, kv ...string) map[string]string {
	out := map[string]string{}
	for k, v := range base {
		out[k] = v
	}
	for i := 0; i < len(kv); i += 2 {
		out[kv[i]] = kv[i+1]
	}
	return out
}

func TestLoadAuth(t *testing.T) {
	cfg, err := LoadAuth(env(validAuth))
	if err != nil || cfg.Issuer != validAuth["AUTH_OIDC_ISSUER"] || cfg.Audience != validAuth["AUTH_OIDC_AUDIENCE"] || cfg.JWKSURL != validAuth["AUTH_OIDC_JWKS_URL"] || cfg.Algorithms != nil {
		t.Fatalf("valid: %+v %v", cfg, err)
	}
	cfg, err = LoadAuth(env(withEnv(validAuth, "AUTH_OIDC_ALGORITHMS", "ES256, PS256")))
	if err != nil || !slices.Equal(cfg.Algorithms, []string{"ES256", "PS256"}) {
		t.Fatalf("algorithms: %v %v", cfg.Algorithms, err)
	}
	for value, want := range map[string]bool{"": false, "false": false, "true": true} {
		if cfg, err := LoadAuth(env(withEnv(validAuth, "AUTH_OIDC_REQUIRE_AT_JWT", value))); err != nil || cfg.RequireAccessTokenType != want {
			t.Errorf("AUTH_OIDC_REQUIRE_AT_JWT=%q: %v %v", value, cfg.RequireAccessTokenType, err)
		}
	}
	if _, err := LoadAuth(env(withEnv(validAuth, "AUTH_OIDC_REQUIRE_AT_JWT", "yes"))); err == nil || !slices.Equal(codes(t, err), []string{"AUTH_OIDC_REQUIRE_AT_JWT:invalid"}) {
		t.Errorf("AUTH_OIDC_REQUIRE_AT_JWT=yes: %v", err)
	}
	if _, err := LoadAuth(env(nil)); err == nil || !slices.Equal(codes(t, err), []string{"AUTH_OIDC_ISSUER:required", "AUTH_OIDC_AUDIENCE:required", "AUTH_OIDC_JWKS_URL:required"}) {
		t.Errorf("empty: %v", err)
	}
	for name, kv := range map[string]map[string]string{
		"http issuer":   withEnv(validAuth, "AUTH_OIDC_ISSUER", "http://id.example"),
		"http JWKS":     withEnv(validAuth, "AUTH_OIDC_JWKS_URL", "http://id.example/certs?key=TOPSECRET"),
		"HS256 allowed": withEnv(validAuth, "AUTH_OIDC_ALGORITHMS", "RS256,HS256"),
		"none allowed":  withEnv(validAuth, "AUTH_OIDC_ALGORITHMS", "none"),
		"credentials":   withEnv(validAuth, "AUTH_OIDC_JWKS_URL", "https://u:TOPSECRET@id.example/certs"),
	} {
		_, err := LoadAuth(env(kv))
		if err == nil || !slices.Equal(codes(t, err), []string{"AUTH_OIDC_*:invalid"}) {
			t.Errorf("%s: %v", name, err)
		} else if strings.Contains(err.Error(), "TOPSECRET") {
			t.Errorf("%s: the error echoes a value: %v", name, err)
		}
	}
}
