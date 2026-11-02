package meta_test

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/kcl1104/geminus/internal/ai/provider"
	"github.com/kcl1104/geminus/internal/ai/provider/meta"
	"github.com/kcl1104/geminus/internal/config/aiapproval"
	"github.com/kcl1104/geminus/internal/secret"
)

// The owner's base URL (GEMINUS_META_BASE_URL, the H-083 follow-up): the
// Meta Model API answered 404 to most Railway hosts, the same key 200
// through the owner's forwarding proxy, which forwards
// /<secret>/v1/{messages, messages/count_tokens, responses} to
// https://api.meta.ai and passes the Authorization header through. Every
// request goes under the base URL's path, the secret segment kept; the
// base URL is never in an error.

// proxySeg is the proxy's secret path segment in these tests.
const proxySeg = "PROXY-SECRET-SENTINEL-41c7"

// proxyPrefix is the method and path prefix of every request through the
// proxy.
const proxyPrefix = "POST /" + proxySeg + "/"

// liveConfig is H-029's configuration for model m with the base URL base.
func liveConfig(t *testing.T, m, base string) meta.Config {
	t.Helper()
	h029, ok := aiapproval.Recorded("H-029")
	if !ok {
		t.Fatal("H-029 is not recorded")
	}
	return meta.Config{Approval: h029, Model: m, Credential: meta.Credential{Label: "meta", Key: secret.Secret(key)}, BaseURL: secret.Secret(base)}
}

// proxied are the Muse Spark backend and the SAM segmenter New and
// NewSegmenter build for base, both on the local TLS server s.
func proxied(t *testing.T, s *server, base string) (*meta.Backend, *meta.Segmenter) {
	t.Helper()
	b, err := meta.New(liveConfig(t, model, base))
	if err != nil {
		t.Fatal(err)
	}
	sg, err := meta.NewSegmenter(liveConfig(t, samModel, base))
	if err != nil {
		t.Fatal(err)
	}
	for _, v := range []any{b, sg} {
		meta.SetTransport(v, s.srv.Client().Transport)
		meta.SetSleep(v, (&waits{}).sleep)
	}
	return b, sg
}

// Through the base URL, the text call, the image call's count and its
// call, and the segmentation all go under the secret prefix, with the key
// as the bearer token; nothing goes to the bare /v1 paths. The base URL
// without its trailing slash is the same base URL.
func TestTheBaseURLKeepsItsSecretPrefix(t *testing.T) {
	for _, slash := range []string{"", "/"} {
		s := newTLSServer(t, map[string][]reply{proxyPrefix + "v1/messages": {ok(answerJ)},
			proxyPrefix + "v1/messages/count_tokens": {counted("3000")}, proxyPrefix + "v1/responses": {response(docLine)}})
		b, sg := proxied(t, s, s.srv.URL+"/"+proxySeg+slash)
		if !b.CustomBaseURL() || !sg.CustomBaseURL() {
			t.Error("the backends do not say a custom base URL is set")
		}
		m := provider.NewMemoryMeter(100 * spendUSD)
		c, err := provider.NewMetered(b, provider.DefaultBudget(), m)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := ask(t, c); err != nil {
			t.Fatalf("text: %v", err)
		}
		if _, err := ask(t, c, pngImage(t, 64, 48)); err != nil {
			t.Fatalf("image: %v", err)
		}
		sc, err := provider.NewMeteredSegmentClient(sg, provider.DefaultBudget(), m)
		if err != nil {
			t.Fatal(err)
		}
		if res, err := sc.Segment(context.Background(), segRequest(t, 537, 561, "button")); err != nil || len(res.Segments) != 3 {
			t.Fatalf("segmentation: %+v %v", res, err)
		}
		want := []string{proxyPrefix + "v1/messages", proxyPrefix + "v1/messages/count_tokens", proxyPrefix + "v1/messages", proxyPrefix + "v1/responses"}
		if got := s.order(); !slices.Equal(got, want) {
			t.Errorf("slash %q: requests %v, want %v", slash, got, want)
		}
		for i, h := range s.headers {
			if h.Get("Authorization") != "Bearer "+key {
				t.Errorf("request %d: the key is not the bearer token", i)
			}
		}
	}
	// Without a base URL, the provider's own API, as before.
	b, err := meta.New(liveConfig(t, model, ""))
	if err != nil || b.CustomBaseURL() {
		t.Errorf("the default base URL: %v", err)
	}
	sg, err := meta.NewSegmenter(liveConfig(t, samModel, ""))
	if err != nil || sg.CustomBaseURL() {
		t.Errorf("the default base URL of the segmenter: %v", err)
	}
}

// New and NewSegmenter take only an absolute https base URL with a host
// and a plain path (internal/config checked it at startup; the adapter
// checks again), after the approval; the error never names it.
func TestNewRefusesABadBaseURL(t *testing.T) {
	for name, base := range map[string]string{
		"http":             "http://proxy-test.example/" + proxySeg + "/",
		"no scheme":        "proxy-test.example/" + proxySeg + "/",
		"no host":          "https:///" + proxySeg + "/",
		"user information": "https://user:" + proxySeg + "@proxy-test.example/",
		"a query":          "https://proxy-test.example/x/?k=" + proxySeg,
		"a fragment":       "https://proxy-test.example/x/#" + proxySeg,
		"a dot segment":    "https://proxy-test.example/" + proxySeg + "/../",
		"an empty segment": "https://proxy-test.example//" + proxySeg + "/",
		"white space":      "https://proxy-test.example/" + proxySeg + "/\n",
		"a reference":      "https://proxy-test.example/${{PROXY_SECRET}}/" + proxySeg + "/",
	} {
		for _, m := range []string{model, samModel} {
			cfg := liveConfig(t, m, base)
			var err error
			if m == model {
				_, err = meta.New(cfg)
			} else {
				_, err = meta.NewSegmenter(cfg)
			}
			if err == nil || errors.Is(err, meta.ErrNotApproved) {
				t.Errorf("%s, %s: %v", name, m, err)
				continue
			}
			leaks(t, name, err.Error()+fmt.Sprintf("%+v", err), proxySeg, "proxy-test")
			// The approval is checked first.
			cfg.Approval = aiapproval.Approval{}
			if _, err := meta.New(cfg); !errors.Is(err, meta.ErrNotApproved) {
				t.Errorf("%s, %s, no approval: %v", name, m, err)
			}
		}
	}
	// The configuration prints the base URL as [redacted].
	cfg := liveConfig(t, model, "https://proxy-test.example/"+proxySeg+"/")
	for _, f := range []string{"%v", "%+v", "%#v", "%s"} {
		leaks(t, "the configuration "+f, fmt.Sprintf(f, cfg), proxySeg, "proxy-test")
	}
}

// What the provider answers through the proxy, and what fails on the way
// to it, comes back as a typed error and metadata that name the status,
// the error type and the key's label, never the base URL: H-083's 404
// not_found_error, a 5xx retried, a connection that fails (whose transport
// error names the URL), a timeout; the segmenter's too.
func TestNoProviderErrorCarriesTheBaseURL(t *testing.T) {
	prefix := proxyPrefix + "v1/"
	s := newTLSServer(t, map[string][]reply{prefix + "messages": {apiError(404, "not_found_error")},
		prefix + "responses": {apiError(404, "not_found_error")}})
	base := s.srv.URL + "/" + proxySeg + "/"
	b, sg := proxied(t, s, base)
	c, err := provider.NewMetered(b, provider.DefaultBudget(), provider.NewMemoryMeter(100*spendUSD))
	if err != nil {
		t.Fatal(err)
	}
	host := strings.TrimPrefix(s.srv.URL, "https://")
	check := func(what string, res any, err error, code provider.Code, status int) {
		t.Helper()
		var pe *provider.Error
		if !errors.As(err, &pe) || pe.Code != code || pe.Status != status {
			t.Errorf("%s: %v, want %s %d", what, err, code, status)
			return
		}
		data, merr := json.Marshal(res)
		if merr != nil {
			t.Fatal(merr)
		}
		text := fmt.Sprintf(" %+v %#v ", err, err) + string(data)
		for e := err; e != nil; e = errors.Unwrap(e) { // what wraps it too
			text += " " + e.Error()
		}
		leaks(t, what, text, proxySeg, host, "workers.dev")
	}
	res, err := ask(t, c)
	check("404", res, err, provider.CodeRejected, 404)
	sc, err := provider.NewMeteredSegmentClient(sg, provider.DefaultBudget(), provider.NewMemoryMeter(100*spendUSD))
	if err != nil {
		t.Fatal(err)
	}
	seg, err := sc.Segment(context.Background(), segRequest(t, 537, 561, "button"))
	check("the segmenter's 404", seg, err, provider.CodeRejected, 404)

	// A 5xx, retried and then given up on.
	s5 := newTLSServer(t, map[string][]reply{prefix + "messages": {apiError(503, "overloaded_error")}})
	b5, _ := proxied(t, s5, s5.srv.URL+"/"+proxySeg)
	c5, _ := provider.NewMetered(b5, provider.DefaultBudget(), provider.NewMemoryMeter(100*spendUSD))
	res, err = ask(t, c5)
	check("503", res, err, provider.CodeUnavailable, 503)

	// The proxy gone: the transport's own error names the URL; the call's
	// does not.
	gone := newTLSServer(t, nil)
	bg, _ := proxied(t, gone, gone.srv.URL+"/"+proxySeg+"/")
	gone.srv.Close()
	cg, _ := provider.NewMetered(bg, provider.DefaultBudget(), provider.NewMemoryMeter(100*spendUSD))
	res, err = ask(t, cg)
	check("a lost connection", res, err, provider.CodeUnavailable, 0)

	// A timeout: the cause is the context's, not the transport's.
	slow := newTLSServer(t, map[string][]reply{prefix + "messages": {{status: 200, delay: time.Second, body: "{}"}}})
	bs, _ := proxied(t, slow, slow.srv.URL+"/"+proxySeg+"/")
	cs, _ := provider.NewMetered(bs, provider.DefaultBudget(), provider.NewMemoryMeter(100*spendUSD))
	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()
	res, err = askCtx(t, ctx, cs)
	if provider.CodeOf(err) != provider.CodeTimeout && provider.CodeOf(err) != provider.CodeCancelled {
		t.Errorf("timeout: %v", err)
	}
	if err != nil {
		data, _ := json.Marshal(res)
		text := fmt.Sprintf(" %+v ", err) + string(data)
		for e := err; e != nil; e = errors.Unwrap(e) {
			text += " " + e.Error()
		}
		leaks(t, "a timeout", text, proxySeg, strings.TrimPrefix(slow.srv.URL, "https://"))
	}
}
