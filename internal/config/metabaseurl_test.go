package config

import (
	"encoding/json"
	"fmt"
	"log/slog"
	"maps"
	"slices"
	"strings"
	"testing"
)

// GEMINUS_META_BASE_URL (H-083 follow-up): the Meta Model API answered 404
// to most Railway hosts while the same key answered 200 from the owner's
// machine and through the owner's forwarding proxy, so the owner may name
// the proxy's base URL. Its path carries a secret, so it is read as the key
// is (only once Meta live AI is on), checked, normalised to end with "/",
// kept as a secret.Secret and never echoed in an error. Unset is the
// provider's own API, exactly as before.

// proxySecret is the secret path segment of the test's proxy URL: it must
// never be in an error, a printed configuration or a log line.
const proxySecret = "PROXY-SECRET-SENTINEL-7f3a9c"

const proxyBase = "https://proxy-test.example.workers.dev/" + proxySecret + "/"

func TestTheMetaBaseURL(t *testing.T) {
	// Unset: the provider's own API, as before.
	ai, err := LoadAI(env(metaEnv()))
	if err != nil || !ai.LiveEnabled() || ai.Credentials[0].BaseURL != "" || ai.Vision.Credentials[0].BaseURL != "" ||
		BaseURLOf(ai.Credentials) != "" || BaseURLOf(ai.Vision.Credentials) != "" {
		t.Fatalf("unset: %+v %v", ai, err)
	}
	for name, c := range map[string]struct{ raw, want string }{
		"with its slash":             {proxyBase, proxyBase},
		"without the trailing slash": {strings.TrimSuffix(proxyBase, "/"), proxyBase},
		"a host only":                {"https://proxy-test.example.workers.dev", "https://proxy-test.example.workers.dev/"},
		"a port and two segments":    {"https://proxy-test.example:8443/a/" + proxySecret, "https://proxy-test.example:8443/a/" + proxySecret + "/"},
	} {
		kv := metaEnv()
		kv["GEMINUS_META_BASE_URL"] = c.raw
		ai, err := LoadAI(env(kv))
		if err != nil || !ai.LiveEnabled() || len(ai.Credentials) != 1 {
			t.Fatalf("%s: %+v %v", name, ai, err)
		}
		// The proposals' key and the photo path's (it follows AI_PROVIDER)
		// both go to the base URL.
		if got := ai.Credentials[0].BaseURL.Reveal(); got != c.want {
			t.Errorf("%s: %q, want %q", name, got, c.want)
		}
		if got := ai.Vision.Credentials[0].BaseURL.Reveal(); got != c.want {
			t.Errorf("%s: the photo path's %q, want %q", name, got, c.want)
		}
		if BaseURLOf(ai.Credentials) != BaseURLCustom || BaseURLOf(ai.Vision.Credentials) != BaseURLCustom {
			t.Errorf("%s: the label", name)
		}
		printed(t, name, ai)
	}
	// Meta for the photo path only (Claude's proposals): the photo path's
	// key goes to the base URL; Claude's keys never do.
	mixed := map[string]string{"AI_PROVIDER": "anthropic", "AI_POLICY_APPROVAL": "H-018", "AI_MODEL": "claude-opus-5-5",
		"GEMINUS_ANTHROPIC_API_KEY_A": keyA, "GEMINUS_ANTHROPIC_API_KEY_B": keyB, "GEMINUS_AI_LIVE": "on",
		"AI_VISION_PROVIDER": "meta", "AI_VISION_POLICY_APPROVAL": "H-029", "AI_VISION_MODEL": "muse-spark-1.3",
		"GEMINUS_MUSE_API_KEY": museKey, "GEMINUS_META_BASE_URL": proxyBase}
	ai, err = LoadAI(env(mixed))
	if err != nil || !ai.Vision.LiveEnabled() || ai.Vision.Credentials[0].BaseURL.Reveal() != proxyBase || BaseURLOf(ai.Credentials) != "" {
		t.Errorf("Claude, Meta for photos: %+v %v", ai, err)
	}
	for _, c := range ai.Credentials {
		if c.BaseURL != "" {
			t.Errorf("Claude's %s goes to the base URL", c.Label)
		}
	}
}

// Read as the key is: not while the switch is off, nor for another
// provider; so a bad value is no error then either.
func TestTheMetaBaseURLIsReadOnlyWhenMetaIsLive(t *testing.T) {
	for name, kv := range map[string]map[string]string{
		"the switch off": func() map[string]string { kv := metaEnv(); delete(kv, "GEMINUS_AI_LIVE"); return kv }(),
		"anthropic": {"AI_PROVIDER": "anthropic", "AI_POLICY_APPROVAL": "H-018", "AI_MODEL": "claude-opus-5-5",
			"GEMINUS_ANTHROPIC_API_KEY_A": keyA, "GEMINUS_ANTHROPIC_API_KEY_B": keyB, "GEMINUS_AI_LIVE": "on"},
		"disabled": {},
	} {
		kv = maps.Clone(kv)
		kv["GEMINUS_META_BASE_URL"] = "http://not-checked.example/" + proxySecret
		r := &recorder{kv: kv}
		if _, err := LoadAI(r.get); err != nil {
			t.Errorf("%s: %v", name, err)
		}
		if slices.Contains(r.read, "GEMINUS_META_BASE_URL") {
			t.Errorf("%s: read", name)
		}
	}
}

// Anything but an absolute https URL with a host and a plain path refuses
// startup, with the variable's name and never its value.
func TestABadMetaBaseURLRefusesStartup(t *testing.T) {
	for name, raw := range map[string]string{
		"http":                  "http://proxy-test.example/" + proxySecret + "/",
		"http on this machine":  "http://127.0.0.1:8787/" + proxySecret + "/",
		"another scheme":        "ftp://proxy-test.example/" + proxySecret + "/",
		"upper-case scheme":     "HTTPS://proxy-test.example/" + proxySecret + "/",
		"no scheme":             "proxy-test.example/" + proxySecret + "/",
		"a relative path":       "/" + proxySecret + "/",
		"no host":               "https:///" + proxySecret + "/",
		"an empty host":         "https://:443/" + proxySecret + "/",
		"user information":      "https://user:" + proxySecret + "@proxy-test.example/",
		"a user only":           "https://" + proxySecret + "@proxy-test.example/x/",
		"a query":               "https://proxy-test.example/x/?k=" + proxySecret,
		"an empty query":        "https://proxy-test.example/" + proxySecret + "/?",
		"a fragment":            "https://proxy-test.example/x/#" + proxySecret,
		"an empty fragment":     "https://proxy-test.example/" + proxySecret + "/#",
		"a dot segment":         "https://proxy-test.example/" + proxySecret + "/../v1/",
		"an encoded dot":        "https://proxy-test.example/" + proxySecret + "/%2e%2e/",
		"a single dot":          "https://proxy-test.example/./" + proxySecret + "/",
		"an empty segment":      "https://proxy-test.example//" + proxySecret + "/",
		"a space inside":        "https://proxy-test.example/" + proxySecret + " x/",
		"a trailing newline":    "https://proxy-test.example/" + proxySecret + "/\n",
		"a leading space":       " https://proxy-test.example/" + proxySecret + "/",
		"a control character":   "https://proxy-test.example/" + proxySecret + "\x00/",
		"a backslash":           "https://proxy-test.example\\" + proxySecret + "/",
		"too long":              "https://proxy-test.example/" + proxySecret + "/" + strings.Repeat("a", 2048),
		"not valid UTF-8":       "https://proxy-test.example/" + proxySecret + "\xff/",
		"an opaque URL":         "https:" + proxySecret,
		"a bad percent-escape":  "https://proxy-test.example/" + proxySecret + "%zz/",
		"an IPv6 host unclosed": "https://[::1/" + proxySecret + "/",
		// Railway did not expand a reference: every call would go to a
		// path the proxy does not know.
		"an unexpanded reference": "https://proxy-test.example/${{PROXY_SECRET}}/",
		"a brace":                 "https://proxy-test.example/" + proxySecret + "{/",
		"non-ASCII":               "https://proxy-test.example/" + proxySecret + "é/",
	} {
		kv := metaEnv()
		kv["GEMINUS_META_BASE_URL"] = raw
		ai, err := LoadAI(env(kv))
		if got := codes(t, err); !slices.Equal(got, []string{"GEMINUS_META_BASE_URL:invalid"}) || ai.LiveEnabled() || len(ai.Credentials) != 0 {
			t.Errorf("%s: %v, live %v", name, got, ai.LiveEnabled())
		}
		if err != nil && (strings.Contains(err.Error(), proxySecret) || strings.Contains(err.Error(), "proxy-test") || strings.Contains(fmt.Sprintf("%+v", err), proxySecret)) {
			t.Errorf("%s: the error carries the value: %v", name, err)
		}
	}
	// A bad key and a bad base URL: both reported, neither echoed.
	kv := metaEnv()
	kv["GEMINUS_MUSE_API_KEY"], kv["GEMINUS_META_BASE_URL"] = "", "http://proxy-test.example/"+proxySecret
	_, err := LoadAI(env(kv))
	if got := codes(t, err); !slices.Equal(got, []string{"GEMINUS_MUSE_API_KEY:required", "GEMINUS_META_BASE_URL:invalid"}) ||
		strings.Contains(err.Error(), proxySecret) {
		t.Errorf("both: %v", err)
	}
}

// printed fails t when the configuration, printed, marshalled or logged,
// carries the secret path segment.
func printed(t *testing.T, name string, ai AI) {
	t.Helper()
	var b strings.Builder
	for _, verb := range []string{"%v", "%+v", "%#v", "%s", "%q", "%x"} {
		fmt.Fprintf(&b, verb+"\n", ai)
		fmt.Fprintf(&b, verb+"\n", ai.Credentials)
	}
	j, err := json.Marshal(ai)
	if err != nil {
		t.Fatal(err)
	}
	b.Write(j)
	slog.New(slog.NewJSONHandler(&b, nil)).Info("config", "ai", ai, "credential", ai.Credentials[0])
	if out := b.String(); strings.Contains(out, proxySecret) || strings.Contains(out, "proxy-test") || strings.Contains(out, fmt.Sprintf("%x", proxySecret)) {
		t.Errorf("%s: the base URL is printed:\n%s", name, out)
	}
}
