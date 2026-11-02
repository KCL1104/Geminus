package main

import (
	"bytes"
	"context"
	"strings"
	"testing"
	"time"

	"github.com/kcl1104/geminus/internal/ai/provider/meta"
	"github.com/kcl1104/geminus/internal/config"
)

// proxySeg is the secret path segment of the owner's base URL in these
// tests (GEMINUS_META_BASE_URL, the H-083 follow-up).
const proxySeg = "PROXY-SECRET-SENTINEL-93d1"

const proxyBase = "https://proxy-test.example.workers.dev/" + proxySeg

// customBaseURL reports whether b is a Meta adapter that sends to a custom
// base URL; false for anything else.
func customBaseURL(b any) bool {
	c, ok := b.(interface{ CustomBaseURL() bool })
	return ok && c.CustomBaseURL()
}

// With GEMINUS_META_BASE_URL, every Meta adapter the worker builds sends
// to it: the proposals' backend and the photo path's judge and segmenter,
// whether the photo path follows AI_PROVIDER or is Meta beside Claude's
// proposals. Without it, none does. Built here, never called.
func TestTheWorkerSendsToTheMetaBaseURL(t *testing.T) {
	live := loadAI(t, metaEnv("GEMINUS_AI_LIVE", "on", "GEMINUS_META_BASE_URL", proxyBase))
	b, err := liveBackend(live.Provider, live.Approval, live.Model, live.Credentials)
	if _, ok := b.(*meta.Backend); err != nil || !ok || !customBaseURL(b) {
		t.Fatalf("the proposals' backend %T: %v", b, err)
	}
	judge, seg, err := photoBackends(live.Vision)
	if err != nil || !customBaseURL(judge) || !customBaseURL(seg) {
		t.Errorf("the photo path's %T %T: %v", judge, seg, err)
	}
	mixed := with(append(append([]string{}, h018...), "GEMINUS_AI_LIVE", "on", "AI_VISION_PROVIDER", "meta",
		"AI_VISION_POLICY_APPROVAL", "H-029", "AI_VISION_MODEL", "muse-spark-1.3", "GEMINUS_MUSE_API_KEY", museKey,
		"GEMINUS_META_BASE_URL", proxyBase)...)
	ai := loadAI(t, mixed)
	if judge, seg, err := photoBackends(ai.Vision); err != nil || !customBaseURL(judge) || !customBaseURL(seg) {
		t.Errorf("Meta for photos beside Claude: %T %T: %v", judge, seg, err)
	}
	direct := loadAI(t, metaEnv("GEMINUS_AI_LIVE", "on"))
	b, err = liveBackend(direct.Provider, direct.Approval, direct.Model, direct.Credentials)
	if err != nil || customBaseURL(b) {
		t.Errorf("unset: %T %v", b, err)
	}
	if judge, seg, err := photoBackends(direct.Vision); err != nil || customBaseURL(judge) || customBaseURL(seg) {
		t.Errorf("unset, the photo path: %v", err)
	}
}

// The start log says a custom base URL is set (base_url custom, on the
// proposals' line and the photo path's) and never what it is; without one
// the lines are as before. A base URL that does not check refuses to
// start, and the log names the variable, never its value.
func TestTheWorkerLogNamesNoBaseURL(t *testing.T) {
	for name, c := range map[string]struct {
		env    func(string) string
		code   int
		want   []string
		absent []string
	}{
		"set": {metaEnv("GEMINUS_AI_LIVE", "on", "GEMINUS_META_BASE_URL", proxyBase), exitDatabase,
			[]string{`"msg":"live AI is on"`, `"msg":"the photo path is on"`}, nil},
		"unset": {metaEnv("GEMINUS_AI_LIVE", "on"), exitDatabase, []string{`"msg":"live AI is on"`}, []string{`"base_url"`}},
		"http": {metaEnv("GEMINUS_AI_LIVE", "on", "GEMINUS_META_BASE_URL", "http://proxy-test.example.workers.dev/"+proxySeg), exitConfig,
			[]string{`"msg":"invalid configuration"`, "GEMINUS_META_BASE_URL (invalid)"}, []string{`"msg":"live AI is on"`}},
		"a query": {metaEnv("GEMINUS_AI_LIVE", "on", "GEMINUS_META_BASE_URL", proxyBase+"/?k="+proxySeg), exitConfig,
			[]string{"GEMINUS_META_BASE_URL (invalid)"}, nil},
	} {
		var stderr bytes.Buffer
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		code := run(ctx, c.env, &stderr, someHandler)
		cancel()
		out := stderr.String()
		if code != c.code {
			t.Errorf("%s: exit %d, want %d: %s", name, code, c.code, out)
		}
		for _, w := range c.want {
			if !strings.Contains(out, w) {
				t.Errorf("%s: no %s in %s", name, w, out)
			}
		}
		for _, w := range c.absent {
			if strings.Contains(out, w) {
				t.Errorf("%s: %s in %s", name, w, out)
			}
		}
		if name == "set" {
			for _, line := range strings.Split(out, "\n") {
				if (strings.Contains(line, `"msg":"live AI is on"`) || strings.Contains(line, `"msg":"the photo path is on"`)) &&
					!strings.Contains(line, `"base_url":"`+config.BaseURLCustom+`"`) {
					t.Errorf("set: the line does not say a custom base URL is set: %s", line)
				}
			}
		}
		for _, leak := range []string{proxySeg, "proxy-test", "workers.dev", museKey} {
			if strings.Contains(out, leak) {
				t.Errorf("%s: the log holds %q: %s", name, leak, out)
			}
		}
	}
}
