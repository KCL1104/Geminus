package main

import (
	"bytes"
	"context"
	"encoding/json/v2"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"reflect"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/kcl1104/geminus/internal/config"
	"github.com/kcl1104/geminus/internal/observability"
	"github.com/kcl1104/geminus/internal/proposals"
	"github.com/kcl1104/geminus/internal/storage/dbtest"
	"github.com/kcl1104/geminus/internal/storage/objects"
	"github.com/kcl1104/geminus/internal/tenant"
)

var validEnv = map[string]string{
	"DATABASE_URL":       "postgresql://twin_api:db-pass-123@/twin?host=/nonexistent-geminus-socket",
	"AUTH_OIDC_ISSUER":   "https://id.example/realms/geminus",
	"AUTH_OIDC_AUDIENCE": "https://api.geminus.example",
	"AUTH_OIDC_JWKS_URL": "https://id.example/realms/geminus/certs",
	"API_LISTEN_ADDR":    "127.0.0.1:0",
}

func with(kv ...string) func(string) string {
	m := map[string]string{}
	for k, v := range validEnv {
		m[k] = v
	}
	for i := 0; i < len(kv); i += 2 {
		m[kv[i]] = kv[i+1]
	}
	return func(k string) string { return m[k] }
}

// T-025: the API accepts new AI proposals only with the fake (development
// and tests) or a live provider whose recorded approval, model and switch
// the worker's gate accepts (T-053 I5: without any key); otherwise it
// refuses them with the code AI_SPEC §2 names.
func TestAPIAvailability(t *testing.T) {
	for _, c := range []struct {
		name string
		env  []string
		want proposals.Availability
	}{
		{"default", nil, proposals.NotConfigured},
		{"disabled", []string{"AI_PROVIDER", "disabled"}, proposals.NotConfigured},
		{"disabled, the switch off", []string{"AI_PROVIDER", "disabled", "GEMINUS_AI_LIVE", "off"}, proposals.NotConfigured},
		{"live without approval", []string{"AI_PROVIDER", "anthropic"}, proposals.PolicyNotApproved},
		{"meta without approval", []string{"AI_PROVIDER", "meta"}, proposals.PolicyNotApproved},
		{"meta, the switch off", []string{"AI_PROVIDER", "meta", "AI_POLICY_APPROVAL", "H-029", "AI_MODEL", "muse-spark-1.3"}, proposals.PolicyNotApproved},
		{"meta, the switch off explicitly", []string{"AI_PROVIDER", "meta", "AI_POLICY_APPROVAL", "H-029", "AI_MODEL", "muse-spark-1.3",
			"GEMINUS_AI_LIVE", "off"}, proposals.PolicyNotApproved},
		{"anthropic, the switch off", []string{"AI_PROVIDER", "anthropic", "AI_POLICY_APPROVAL", "H-018", "AI_MODEL", "claude-opus-5-5"}, proposals.PolicyNotApproved},
		{"meta, another model, the switch off", []string{"AI_PROVIDER", "meta", "AI_POLICY_APPROVAL", "H-029", "AI_MODEL", "muse-spark-1.2"}, proposals.PolicyNotApproved},
		{"meta, Claude's approval, the switch off", []string{"AI_PROVIDER", "meta", "AI_POLICY_APPROVAL", "H-018", "AI_MODEL", "muse-spark-1.3"}, proposals.PolicyNotApproved},
		{"meta live", apiMeta, proposals.Available},
		{"anthropic live", apiClaude, proposals.Available},
		{"fake", []string{"AI_PROVIDER", "fake", "APP_ENV", "test"}, proposals.Available},
	} {
		ai, err := config.LoadAPIAI(with(c.env...))
		if err != nil {
			t.Fatalf("%s: %v", c.name, err)
		}
		if got := aiAvailability(ai); got != c.want {
			t.Errorf("%s: %v, want %v", c.name, got, c.want)
		}
	}
	// Only a valid approval of the provider itself makes it available,
	// however the value was made.
	ai, err := config.LoadAPIAI(with(apiMeta...))
	if err != nil {
		t.Fatal(err)
	}
	for name, v := range map[string]config.APIAI{
		"the zero value":        {},
		"meta, no approval":     {Provider: config.AIProviderMeta, Model: "muse-spark-1.3"},
		"anthropic, H-029":      {Provider: config.AIProviderAnthropic, Approval: ai.Approval, Model: ai.Model},
		"disabled, an approval": {Provider: config.AIProviderDisabled, Approval: ai.Approval},
	} {
		if got := aiAvailability(v); got == proposals.Available {
			t.Errorf("%s: available", name)
		}
	}
}

// apiMeta and apiClaude are the API's settings with live AI on (T-053 I5):
// the provider, its recorded approval, its model and the switch; no key.
// apiClaudeMetaPhotos is Claude for the proposals and Meta for the photo
// path (the T-056 follow-up).
var (
	apiMeta             = []string{"AI_PROVIDER", "meta", "AI_POLICY_APPROVAL", "H-029", "AI_MODEL", "muse-spark-1.3", "GEMINUS_AI_LIVE", "on"}
	apiClaude           = []string{"AI_PROVIDER", "anthropic", "AI_POLICY_APPROVAL", "H-018", "AI_MODEL", "claude-opus-5-5", "GEMINUS_AI_LIVE", "on"}
	apiClaudeMetaPhotos = append(slices.Clone(apiClaude), "AI_VISION_PROVIDER", "meta", "AI_VISION_POLICY_APPROVAL", "H-029", "AI_VISION_MODEL", "muse-spark-1.3")
)

// The T-056 follow-up (D-124): the API takes a photo exactly when the
// worker's photo path is on, decided from the photo path's settings without
// any key (config.LoadAPIAI): the fake (development and tests), or a live
// provider whose approval names images, its model and the switch; a live
// provider that is not on is PolicyNotApproved (as for the proposals),
// anything else NotConfigured. The proposals in words are decided on their
// own: Claude's approval, which names no image, takes proposals and no
// photo; the fake photo path in tests takes photos beside a disabled
// provider.
func TestAPIPhotoAvailability(t *testing.T) {
	for _, c := range []struct {
		name        string
		env         []string
		text, photo proposals.Availability
	}{
		{"default", nil, proposals.NotConfigured, proposals.NotConfigured},
		{"disabled", []string{"AI_PROVIDER", "disabled"}, proposals.NotConfigured, proposals.NotConfigured},
		{"meta live", apiMeta, proposals.Available, proposals.Available},
		{"meta live, the photo path disabled", append(slices.Clone(apiMeta), "AI_VISION_PROVIDER", "disabled"), proposals.Available, proposals.NotConfigured},
		{"meta, the switch off", apiMeta[:6], proposals.PolicyNotApproved, proposals.PolicyNotApproved},
		{"meta without approval", []string{"AI_PROVIDER", "meta"}, proposals.PolicyNotApproved, proposals.PolicyNotApproved},
		{"anthropic live: no image approved", apiClaude, proposals.Available, proposals.PolicyNotApproved},
		{"anthropic, the switch off", apiClaude[:6], proposals.PolicyNotApproved, proposals.PolicyNotApproved},
		{"anthropic live, meta for photos", apiClaudeMetaPhotos, proposals.Available, proposals.Available},
		{"anthropic, meta for photos, the switch off", append(slices.Clone(apiClaudeMetaPhotos), "GEMINUS_AI_LIVE", "off"), proposals.PolicyNotApproved,
			proposals.PolicyNotApproved},
		{"anthropic live, meta for photos, no photo approval: off", append(slices.Clone(apiClaude[:6]), "AI_VISION_PROVIDER", "meta"),
			proposals.PolicyNotApproved, proposals.PolicyNotApproved},
		{"meta live, claude for photos", append(slices.Clone(apiMeta), "AI_VISION_PROVIDER", "anthropic", "AI_VISION_POLICY_APPROVAL", "H-018",
			"AI_VISION_MODEL", "claude-opus-5-5"), proposals.Available, proposals.PolicyNotApproved},
		{"the fake", []string{"AI_PROVIDER", "fake", "APP_ENV", "test"}, proposals.Available, proposals.Available},
		{"the fake, photos disabled", []string{"AI_PROVIDER", "fake", "APP_ENV", "test", "AI_VISION_PROVIDER", "disabled"}, proposals.Available,
			proposals.NotConfigured},
		{"disabled, the fake photo path", []string{"APP_ENV", "development", "AI_VISION_PROVIDER", "fake"}, proposals.NotConfigured, proposals.Available},
	} {
		ai, err := config.LoadAPIAI(with(c.env...))
		if err != nil {
			t.Fatalf("%s: %v", c.name, err)
		}
		if text, photo := aiAvailability(ai), photoAvailability(ai); text != c.text || photo != c.photo {
			t.Errorf("%s: proposals %v, photos %v; want %v, %v", c.name, text, photo, c.text, c.photo)
		}
	}
	// Only a valid approval of the photo path's provider itself that names
	// images makes photos available, however the value was made.
	claude, err := config.LoadAPIAI(with(apiClaude...))
	if err != nil {
		t.Fatal(err)
	}
	meta, err := config.LoadAPIAI(with(apiMeta...))
	if err != nil {
		t.Fatal(err)
	}
	for name, v := range map[string]config.APIAI{
		"the zero value":                {},
		"meta, no photo approval":       {Provider: config.AIProviderMeta, Approval: meta.Approval, Vision: config.APIVision{Provider: config.AIProviderMeta}},
		"anthropic photos, H-018":       {Vision: config.APIVision{Provider: config.AIProviderAnthropic, Approval: claude.Approval, Model: claude.Model}},
		"anthropic photos, H-029":       {Vision: config.APIVision{Provider: config.AIProviderAnthropic, Approval: meta.Vision.Approval}},
		"disabled photos, an approval":  {Vision: config.APIVision{Provider: config.AIProviderDisabled, Approval: meta.Vision.Approval}},
		"the proposals live, no photos": {Provider: config.AIProviderMeta, Approval: meta.Approval, Model: meta.Model},
	} {
		if got := photoAvailability(v); got == proposals.Available {
			t.Errorf("%s: photos available", name)
		}
	}
	if photoAvailability(meta) != proposals.Available {
		t.Error("H-029 with the switch takes no photo")
	}
}

// T-053 I5: the deployed API holds no AI key, so with the switch on, the
// record's model and no key it starts and accepts proposals (here it gets
// as far as the database, which is not there); its log says so, with the
// provider, the model and a typed code, never a key. With the switch off it
// starts and refuses them, the log naming the typed reason. The T-056
// follow-up: it says the same of photos, as the worker says whether its
// photo path is on.
func TestAPIAvailabilityNeedsNoKey(t *testing.T) {
	photosOn := `"msg":"photo proposals are accepted; the worker calls the provider"`
	photosOff := `"msg":"photo proposals are refused"`
	for name, c := range map[string]struct {
		env  []string
		want []string
	}{
		"meta live": {apiMeta, []string{`"msg":"AI proposals are accepted; the worker calls the provider"`, `"provider":"meta"`,
			`"model":"muse-spark-1.3"`, `"code":"LIVE_AI_ON"`, photosOn, `"code":"PHOTO_PATH_ON"`}},
		"anthropic live": {apiClaude, []string{`"msg":"AI proposals are accepted; the worker calls the provider"`, `"provider":"anthropic"`,
			`"model":"claude-opus-5-5"`, `"code":"LIVE_AI_ON"`, photosOff, `"code":"IMAGES_NOT_APPROVED"`}},
		"anthropic live, meta for photos": {apiClaudeMetaPhotos, []string{`"msg":"AI proposals are accepted; the worker calls the provider"`,
			`"code":"LIVE_AI_ON"`, photosOn, `"provider":"meta"`, `"model":"muse-spark-1.3"`, `"code":"PHOTO_PATH_ON"`}},
		"meta, the switch off": {append(slices.Clone(apiMeta), "GEMINUS_AI_LIVE", "off"), []string{`"msg":"AI proposals are refused"`,
			`"provider":"meta"`, `"code":"SWITCH_OFF"`, photosOff}},
		"meta, another model, the switch off": {append(slices.Clone(apiMeta), "GEMINUS_AI_LIVE", "", "AI_MODEL", "muse-spark-1.2"),
			[]string{`"msg":"AI proposals are refused"`, `"code":"MODEL_MISMATCH"`, photosOff}},
		"anthropic, no approval": {[]string{"AI_PROVIDER", "anthropic"}, []string{`"msg":"AI proposals are refused"`, `"code":"NO_APPROVAL_NAMED"`, photosOff}},
	} {
		var stderr syncBuffer
		if code := refused(t, with(c.env...), &stderr); code != exitDatabase {
			t.Errorf("%s: exit %d: %s", name, code, stderr.String())
			continue
		}
		for _, w := range c.want {
			if !strings.Contains(stderr.String(), w) {
				t.Errorf("%s: no %s in %s", name, w, stderr.String())
			}
		}
	}
	// Disabled and the fake say nothing of a live provider.
	for _, env := range [][]string{nil, {"AI_PROVIDER", "fake", "APP_ENV", "test"}, {"APP_ENV", "test", "AI_VISION_PROVIDER", "fake"}} {
		var stderr syncBuffer
		if code := refused(t, with(env...), &stderr); code != exitDatabase || strings.Contains(stderr.String(), "AI proposals") ||
			strings.Contains(stderr.String(), "photo proposals") {
			t.Errorf("%v: exit %d: %s", env, code, stderr.String())
		}
	}
	// The fake with its photo path disabled: neither is a live provider.
	var stderr syncBuffer
	if code := refused(t, with("AI_PROVIDER", "fake", "APP_ENV", "test", "AI_VISION_PROVIDER", "disabled"), &stderr); code != exitDatabase ||
		strings.Contains(stderr.String(), "proposals are") {
		t.Errorf("the fake, photos disabled: exit %d: %s", code, stderr.String())
	}
}

// T-053 I5: with the switch on, a setting the worker's gate refuses stops
// the API before it connects anywhere, with the typed code, as it stops the
// worker.
func TestAPIAvailabilityRefusesAMismatch(t *testing.T) {
	for name, c := range map[string]struct {
		env  []string
		want string
	}{
		"another model":          {[]string{"AI_MODEL", "muse-spark-1.3-contributor"}, "AI_MODEL (model_mismatch)"},
		"no model":               {[]string{"AI_MODEL", ""}, "AI_MODEL (required)"},
		"Claude's approval":      {[]string{"AI_POLICY_APPROVAL", "H-018"}, "AI_POLICY_APPROVAL (provider_mismatch)"},
		"an unrecorded approval": {[]string{"AI_POLICY_APPROVAL", "H-033"}, "AI_POLICY_APPROVAL (not_approved)"},
		"no approval":            {[]string{"AI_POLICY_APPROVAL", ""}, "GEMINUS_AI_LIVE (invalid)"},
		"the switch, disabled":   {[]string{"AI_PROVIDER", "disabled", "AI_POLICY_APPROVAL", "", "AI_MODEL", ""}, "GEMINUS_AI_LIVE (invalid)"},
		"another provider":       {[]string{"AI_PROVIDER", "openai"}, "AI_PROVIDER (invalid)"},
		// The T-056 follow-up: the photo path's settings, as the worker reads them.
		"another photo provider":     {[]string{"AI_VISION_PROVIDER", "openai"}, "AI_VISION_PROVIDER (invalid)"},
		"fake photos in production":  {[]string{"AI_VISION_PROVIDER", "fake", "APP_ENV", "production"}, "AI_VISION_PROVIDER (invalid)"},
		"a photo approval to follow": {[]string{"AI_VISION_POLICY_APPROVAL", "H-029"}, "AI_VISION_POLICY_APPROVAL (invalid)"},
		"claude photos, no approval": {[]string{"AI_VISION_PROVIDER", "anthropic", "AI_VISION_MODEL", "claude-opus-5-5"}, "AI_VISION_POLICY_APPROVAL (required)"},
		"claude photos, H-029":       {[]string{"AI_VISION_PROVIDER", "anthropic", "AI_VISION_POLICY_APPROVAL", "H-029", "AI_VISION_MODEL", "muse-spark-1.3"}, "AI_VISION_POLICY_APPROVAL (provider_mismatch)"},
		"claude photos, no model":    {[]string{"AI_VISION_PROVIDER", "anthropic", "AI_VISION_POLICY_APPROVAL", "H-018"}, "AI_VISION_MODEL (required)"},
		"claude photos, unrecorded":  {[]string{"AI_VISION_PROVIDER", "anthropic", "AI_VISION_POLICY_APPROVAL", "H-033", "AI_VISION_MODEL", "x"}, "AI_VISION_POLICY_APPROVAL (not_approved)"},
	} {
		var stderr syncBuffer
		if code := refused(t, with(append(slices.Clone(apiMeta), c.env...)...), &stderr); code != exitConfig {
			t.Errorf("%s: exit %d: %s", name, code, stderr.String())
			continue
		}
		if out := stderr.String(); !strings.Contains(out, `"msg":"invalid configuration"`) || !strings.Contains(out, c.want) ||
			strings.Contains(out, "database") || strings.Contains(out, "proposals are") {
			t.Errorf("%s: %s", name, out)
		}
	}
}

// T-053 I5, defense in depth: any AI key variable in the API's environment
// stops it before it connects anywhere, with the typed code, the log naming
// the variable and never its value; whatever the AI settings are.
func TestAPIRefusesToStartWithAKey(t *testing.T) {
	const value = "SENTINEL-key-value-0123456789abcdef"
	for _, k := range []string{"GEMINUS_ANTHROPIC_API_KEY_A", "GEMINUS_ANTHROPIC_API_KEY_B", "GEMINUS_MUSE_API_KEY", "GEMINUS_META_API_KEY",
		"ANTHROPIC_API_KEY", "ANTHROPIC_AUTH_TOKEN", "AI_API_KEY", "MODEL_API_KEY"} {
		for name, env := range map[string][]string{"default": nil, "meta live": apiMeta, "anthropic, the switch off": apiClaude[:6],
			"anthropic live, meta for photos": apiClaudeMetaPhotos, "the fake photo path": {"APP_ENV", "test", "AI_VISION_PROVIDER", "fake"}} {
			var stderr syncBuffer
			if code := refused(t, with(append(slices.Clone(env), k, value)...), &stderr); code != exitConfig {
				t.Errorf("%s, %s: exit %d: %s", k, name, code, stderr.String())
				continue
			}
			out := stderr.String()
			if !strings.Contains(out, k+" ("+config.CodeKeyForbidden+")") || strings.Contains(out, "SENTINEL") || strings.Contains(out, value[9:]) ||
				strings.Contains(out, "database") {
				t.Errorf("%s, %s: %s", k, name, out)
			}
		}
	}
}

func TestRunRejectsInvalidConfiguration(t *testing.T) {
	var stderr bytes.Buffer
	if code := refused(t, func(string) string { return "" }, &stderr); code != exitConfig {
		t.Fatalf("exit = %d, want %d", code, exitConfig)
	}
	for _, v := range []string{"DATABASE_URL", "AUTH_OIDC_ISSUER", "AUTH_OIDC_AUDIENCE", "AUTH_OIDC_JWKS_URL"} {
		if !strings.Contains(stderr.String(), v) {
			t.Fatalf("stderr does not name the missing %s: %s", v, stderr.String())
		}
	}
	for name, getenv := range map[string]func(string) string{
		"insecure algorithm": with("AUTH_OIDC_ALGORITHMS", "HS256"),
		"listen address":     with("API_LISTEN_ADDR", "localhost"),
		"port out of range":  with("API_LISTEN_ADDR", ":70000"),
	} {
		stderr.Reset()
		if code := refused(t, getenv, &stderr); code != exitConfig {
			t.Errorf("%s: exit %d: %s", name, code, stderr.String())
		}
	}
}

// refused runs the API with getenv, expecting it to stop before it
// listens: if it serves instead, it fails at once rather than hanging the
// test until its timeout.
func refused(t *testing.T, getenv func(string) string, stderr io.Writer) int {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	ready, done := make(chan string, 1), make(chan int, 1)
	go func() { done <- run(ctx, getenv, stderr, ready) }()
	select {
	case code := <-done:
		return code
	case addr := <-ready:
		t.Errorf("serving at %s", addr)
		cancel()
		<-done
		return -1
	}
}

// Without a usable database the API does not start, and says so without
// the password.
func TestRunNeedsTheDatabase(t *testing.T) {
	var stderr syncBuffer
	if code := refused(t, with(), &stderr); code != exitDatabase {
		t.Fatalf("exit = %d, want %d: %s", code, exitDatabase, stderr.String())
	}
	if strings.Contains(stderr.String(), "db-pass-123") {
		t.Fatalf("stderr leaks the database password: %s", stderr.String())
	}
	if !strings.Contains(stderr.String(), "database unavailable") || strings.Contains(stderr.String(), "refusing the database login") {
		t.Errorf("an unreachable database is not reported as unavailable (review L8): %s", stderr.String())
	}
}

// The listener's limits and the pool guard are those of ADR-019 (review
// M1, L4): a missing timeout or guard is not visible in any response.
func TestServerSettings(t *testing.T) {
	s := newServer(http.NotFoundHandler(), slog.New(slog.DiscardHandler))
	if s.ReadHeaderTimeout != 10*time.Second || s.ReadTimeout != 30*time.Second || s.WriteTimeout != 60*time.Second ||
		s.IdleTimeout != 120*time.Second || s.MaxHeaderBytes != 32<<10 || s.ErrorLog == nil {
		t.Errorf("server %+v", s)
	}
	if shutdownGrace != 20*time.Second {
		t.Errorf("shutdown grace %v", shutdownGrace)
	}
	cfg, err := poolConfig("postgresql://twin_api@/twin?host=/nonexistent")
	if err != nil {
		t.Fatal(err)
	}
	if cfg.AfterRelease == nil || reflect.ValueOf(cfg.AfterRelease).Pointer() != reflect.ValueOf(tenant.Guard).Pointer() {
		t.Error("the pool does not run tenant.Guard on release")
	}
	if _, err := poolConfig("not a url ::"); err == nil {
		t.Error("an invalid DATABASE_URL was accepted")
	}
}

// T-039: the platform's probes beside the /v1 handler. GET /healthz is
// 200 while the process serves; GET /readyz is 200 only while the database
// answers, within readyTimeout, and 503 otherwise, without the reason.
// Neither needs a token, and both say nothing else; every other request
// (another method, another path) is the API's.
func TestProbes(t *testing.T) {
	api := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusTeapot)
		io.WriteString(w, "api "+r.Method+" "+r.URL.Path)
	})
	var readyErr error
	h := withProbes(api, func(context.Context) error { return readyErr })
	get := func(method, path string) (int, string, http.Header) {
		t.Helper()
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, httptest.NewRequest(method, path, nil))
		return rec.Code, rec.Body.String(), rec.Header()
	}
	for _, path := range []string{"/healthz", "/readyz"} {
		code, body, hdr := get(http.MethodGet, path)
		if code != 200 || hdr.Get("Cache-Control") != "no-store" || hdr.Get("Content-Type") != "application/json" ||
			(path == "/healthz" && body != `{"status":"ok"}`) || (path == "/readyz" && body != `{"status":"ready"}`) {
			t.Errorf("%s: %d %q %v", path, code, body, hdr)
		}
	}
	readyErr = errors.New("dial tcp db.internal: connection refused SENTINEL")
	if code, body, _ := get(http.MethodGet, "/readyz"); code != 503 || body != `{"status":"unavailable"}` {
		t.Errorf("not ready: %d %q", code, body)
	}
	if code, _, _ := get(http.MethodGet, "/healthz"); code != 200 {
		t.Errorf("live while the database is down: %d", code)
	}
	for _, c := range [][2]string{{http.MethodPost, "/healthz"}, {http.MethodDelete, "/readyz"}, {http.MethodGet, "/healthz/x"},
		{http.MethodGet, "/v1/me"}, {http.MethodGet, "/"}, {http.MethodGet, "/v1/healthz"}} {
		if code, body, _ := get(c[0], c[1]); code != http.StatusTeapot || body != "api "+c[0]+" "+c[1] {
			t.Errorf("%s %s did not reach the API: %d %q", c[0], c[1], code, body)
		}
	}
	// A database that does not answer is not ready once readyTimeout has
	// passed: the probe does not hang with it.
	hang := withProbes(api, func(ctx context.Context) error { <-ctx.Done(); return ctx.Err() })
	start := time.Now()
	rec := httptest.NewRecorder()
	hang.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/readyz", nil))
	if rec.Code != 503 || time.Since(start) > readyTimeout+3*time.Second {
		t.Errorf("a hanging database: %d after %v", rec.Code, time.Since(start))
	}
}

// T-039: the image's health check (geminus-api -probe) is 0 only when this
// machine's API answers GET /readyz with 200.
func TestProbeCommand(t *testing.T) {
	status := http.StatusOK
	var paths []string
	var mu sync.Mutex
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		paths = append(paths, r.Method+" "+r.URL.Path)
		s := status
		mu.Unlock()
		w.WriteHeader(s)
	}))
	defer srv.Close()
	_, port, _ := strings.Cut(srv.Listener.Addr().String(), ":")
	for addr, want := range map[string]int{":" + port: 0, "0.0.0.0:" + port: 0, "127.0.0.1:" + port: 0} {
		if got := probe(with("API_LISTEN_ADDR", addr)); got != want {
			t.Errorf("%s: %d", addr, got)
		}
	}
	mu.Lock()
	status = http.StatusServiceUnavailable
	mu.Unlock()
	if got := probe(with("API_LISTEN_ADDR", ":"+port)); got != 1 {
		t.Errorf("not ready: %d", got)
	}
	srv.Close()
	if got := probe(with("API_LISTEN_ADDR", ":"+port)); got != 1 {
		t.Errorf("nothing listening: %d", got)
	}
	if got := probe(with("API_LISTEN_ADDR", "bad")); got != 1 {
		t.Errorf("a bad address: %d", got)
	}
	mu.Lock()
	defer mu.Unlock()
	for _, p := range paths {
		if p != "GET /readyz" {
			t.Errorf("asked %s", p)
		}
	}
}

// T-039: the API opens the configured object storage: none when disabled,
// the S3-compatible bucket of a deployment, the filesystem only outside
// production (config refuses it there).
func TestObjectStore(t *testing.T) {
	cfg, err := config.LoadAPI(with())
	if err != nil {
		t.Fatal(err)
	}
	if s, err := objectStore(cfg); s != nil || err != nil {
		t.Errorf("disabled: %v %v", s, err)
	}
	cfg, err = config.LoadAPI(with("APP_ENV", "production", "OBJECT_STORAGE_PROVIDER", "s3", "OBJECT_STORAGE_S3_ENDPOINT", "https://storage.example",
		"OBJECT_STORAGE_S3_BUCKET", "geminus-demo-1a2b", "OBJECT_STORAGE_S3_REGION", "auto", "OBJECT_STORAGE_S3_ACCESS_KEY_ID", "AKID",
		"OBJECT_STORAGE_S3_SECRET_ACCESS_KEY", "SECRET-SENTINEL"))
	if err != nil {
		t.Fatal(err)
	}
	s, err := objectStore(cfg)
	if _, ok := s.(*objects.S3); !ok || err != nil {
		t.Errorf("s3: %T %v", s, err)
	}
	cfg, err = config.LoadAPI(with("APP_ENV", "test", "OBJECT_STORAGE_PROVIDER", "filesystem", "OBJECT_STORAGE_ROOT", t.TempDir()))
	if err != nil {
		t.Fatal(err)
	}
	if s, err := objectStore(cfg); s == nil || err != nil {
		t.Errorf("filesystem: %v %v", s, err)
	}
}

type syncBuffer struct {
	mu sync.Mutex
	b  bytes.Buffer
}

func (s *syncBuffer) Write(p []byte) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.b.Write(p)
}
func (s *syncBuffer) String() string { s.mu.Lock(); defer s.mu.Unlock(); return s.b.String() }

// Against the disposable database: an unsafe login (the provisioning
// superuser) is refused; the API login serves the routes and stops cleanly
// when its context ends.
func TestRunServesAndStops(t *testing.T) {
	dbtest.Admin(t) // skips unless the disposable database is configured; creates the API login
	var stderr syncBuffer
	if code := refused(t, with("DATABASE_URL", dbtest.AdminURL(t)), &stderr); code != exitDatabase || !strings.Contains(stderr.String(), "refusing the database login") {
		t.Errorf("the superuser login: exit %d: %s", code, stderr.String())
	}
	ctx, cancel := context.WithCancel(context.Background())
	ready := make(chan string, 1)
	done := make(chan int, 1)
	go func() { done <- run(ctx, with("DATABASE_URL", dbtest.LoginURL(t, dbtest.APILogin)), &stderr, ready) }()
	var addr string
	select {
	case addr = <-ready:
	case code := <-done:
		t.Fatalf("exit %d before serving: %s", code, stderr.String())
	case <-time.After(30 * time.Second):
		t.Fatal("not serving after 30 s")
	}
	// An organization's route and GET /v1/me (D-045), which selects none.
	for _, path := range []string{"/v1/projects", "/v1/me"} {
		res, err := http.Get("http://" + addr + path)
		if err != nil {
			t.Fatal(err)
		}
		body, _ := io.ReadAll(res.Body)
		res.Body.Close()
		if res.StatusCode != 401 || !strings.Contains(string(body), `"UNAUTHENTICATED"`) || res.Header.Get("X-Request-ID") == "" {
			t.Errorf("unauthenticated request to %s: %d %s", path, res.StatusCode, body)
		}
	}
	// T-039: the probes, without a token: live, and ready while the
	// database answers.
	for path, want := range map[string]string{"/healthz": `{"status":"ok"}`, "/readyz": `{"status":"ready"}`} {
		res, err := http.Get("http://" + addr + path)
		if err != nil {
			t.Fatal(err)
		}
		body, _ := io.ReadAll(res.Body)
		res.Body.Close()
		if res.StatusCode != 200 || string(body) != want {
			t.Errorf("%s: %d %s", path, res.StatusCode, body)
		}
	}
	cancel()
	select {
	case code := <-done:
		if code != 0 {
			t.Errorf("exit %d after shutdown: %s", code, stderr.String())
		}
	case <-time.After(30 * time.Second):
		t.Fatal("did not stop after its context ended")
	}
	// T-035 (D-093 (c)): the served handler is behind the access log: one
	// line per request, with its route's pattern and the response's
	// request ID.
	routes, requests := map[string]bool{}, 0
	for _, l := range strings.Split(strings.TrimSpace(stderr.String()), "\n") {
		var line map[string]any
		if json.Unmarshal([]byte(l), &line) == nil && line["msg"] == "request" {
			requests++
		}
		if json.Unmarshal([]byte(l), &line) == nil && line["msg"] == "request" && line["http_status"] == float64(401) &&
			line["error_code"] == "UNAUTHENTICATED" && strings.HasPrefix(fmt.Sprint(line["request_id"]), "req_") {
			routes[fmt.Sprint(line["route"])] = true
		}
	}
	if !routes["GET /v1/projects"] || !routes["GET /v1/me"] {
		t.Errorf("access lines %v: %s", routes, stderr.String())
	}
	// The probes are outside the access log (a platform polls them): the
	// two /v1 requests are its only lines.
	if requests != 2 || strings.Contains(stderr.String(), "healthz") || strings.Contains(stderr.String(), "readyz") {
		t.Errorf("the probes are logged (%d access lines): %s", requests, stderr.String())
	}
}

// T-035 (D-095 (b)): a telemetry endpoint that is not an absolute http(s)
// URL without credentials stops the API before it connects anywhere, and
// the log names the variable, never its value.
func TestRunRefusesABadTelemetryEndpoint(t *testing.T) {
	for _, c := range [][2]string{
		{"OTEL_EXPORTER_OTLP_ENDPOINT", "https://u:otel-pw-SENTINEL@collector"},
		// Review L6: plain http to another host.
		{"OTEL_EXPORTER_OTLP_ENDPOINT", "http://collector-SENTINEL:4318"},
		// Review M1: headers the exporter cannot parse (it would print them).
		{"OTEL_EXPORTER_OTLP_HEADERS", "Authorization: Bearer hdr-SENTINEL"},
		{"OTEL_EXPORTER_OTLP_METRICS_HEADERS", "x-api-key=hdr-SENTINEL%ZZ"},
	} {
		var stderr syncBuffer
		if code := refused(t, with(c[0], c[1]), &stderr); code != exitConfig {
			t.Fatalf("%s: exit = %d: %s", c[0], code, stderr.String())
		}
		if !strings.Contains(stderr.String(), c[0]) || strings.Contains(stderr.String(), "SENTINEL") ||
			strings.Contains(stderr.String(), "database") {
			t.Errorf("%s", stderr.String())
		}
	}
}

// T-035: the API's log is observability's (D-093, D-094): JSON lines whose
// attributes pass the privacy filter.
func TestTheLogIsFiltered(t *testing.T) {
	var b bytes.Buffer
	newLogger(&b).Error("database unavailable", "error", `connect postgres://twin_api:pw-SENTINEL@db/twin: row "CASE-SENTINEL"`,
		"prompt", "SENTINEL")
	var line map[string]any
	if err := json.Unmarshal(b.Bytes(), &line); err != nil || line["component"] != "api" || line["prompt"] != observability.Redacted {
		t.Errorf("%v %s", err, b.String())
	}
	if strings.Contains(b.String(), "SENTINEL") {
		t.Errorf("the log holds what it must not: %s", b.String())
	}
}
