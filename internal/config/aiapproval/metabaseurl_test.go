package aiapproval_test

import (
	"bytes"
	"context"
	"encoding/json/v2"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/kcl1104/geminus/internal/ai/provider"
	"github.com/kcl1104/geminus/internal/ai/provider/fake"
	"github.com/kcl1104/geminus/internal/aievals"
	"github.com/kcl1104/geminus/internal/config"
	"github.com/kcl1104/geminus/internal/contracttest"
)

// The H-083 follow-up: a live run through the owner's base URL
// (GEMINUS_META_BASE_URL) builds its backends with it, as the worker does,
// and its report says so ("base_url": "custom"), so a smoke through the
// proxy is never read as one sent to the provider's own API; neither the
// report, the spend record nor the output holds the base URL. Without it
// the report has no such member, as before.

const proxySeg = "PROXY-SECRET-SENTINEL-c5e0"

const proxyBase = "https://proxy-test.example.workers.dev/" + proxySeg + "/"

func TestTheLiveEvalSaysItWentThroughTheBaseURL(t *testing.T) {
	root := contracttest.Root(t)
	for _, set := range []bool{true, false} {
		env := map[string]string{"AI_PROVIDER": "meta", "AI_POLICY_APPROVAL": "H-029", "AI_MODEL": "muse-spark-1.3",
			"GEMINUS_MUSE_API_KEY": museKey, "GEMINUS_AI_LIVE": "on"}
		if set {
			env["GEMINUS_META_BASE_URL"] = proxyBase
		}
		dir := emptyRecord(t)
		var out, errb bytes.Buffer
		b := &inProcessMeta{Backend: fake.New()}
		factory := func(ai config.AI) (provider.Billable, error) {
			if got := ai.Credentials[0].BaseURL.Reveal(); (got == proxyBase) != set || (got == "") == set {
				t.Errorf("set %v: the backend was asked for base URL %q", set, got)
			}
			return b, nil
		}
		code := aievals.Main(context.Background(), []string{"-root", root, "-dir", dir, "-smoke"}, func(k string) string { return env[k] }, &out, &errb, factory)
		if code != aievals.ExitOK {
			t.Fatalf("set %v: exit %d: %s %s", set, code, out.String(), errb.String())
		}
		reports, _ := filepath.Glob(filepath.Join(dir, "live-smoke-*-muse-spark-1.3.json"))
		if len(reports) != 1 {
			t.Fatalf("set %v: reports %v", set, reports)
		}
		data, err := os.ReadFile(reports[0])
		if err != nil {
			t.Fatal(err)
		}
		var r aievals.Report
		if err := json.Unmarshal(data, &r); err != nil {
			t.Fatal(err)
		}
		if want := map[bool]string{true: config.BaseURLCustom, false: ""}[set]; r.BaseURL != want {
			t.Errorf("set %v: base_url %q, want %q", set, r.BaseURL, want)
		}
		if !set && bytes.Contains(data, []byte(`"base_url"`)) {
			t.Errorf("unset: the report names a base URL: %s", data)
		}
		record, _ := os.ReadFile(filepath.Join(dir, recordName))
		for _, leak := range []string{proxySeg, "proxy-test", "workers.dev", museKey} {
			if bytes.Contains(data, []byte(leak)) || bytes.Contains(record, []byte(leak)) || strings.Contains(out.String()+errb.String(), leak) {
				t.Errorf("set %v: %q is in the report, the record or the output", set, leak)
			}
		}
	}
}

func TestThePhotoSmokeSaysItWentThroughTheBaseURL(t *testing.T) {
	for _, set := range []bool{true, false} {
		env := h029Env()
		if set {
			env["GEMINUS_META_BASE_URL"] = proxyBase
		}
		dir := emptyRecord(t)
		r := runPhotoMain(t, env, dir, &smokeJudge{t: t})
		if r.code != aievals.ExitOK {
			t.Fatalf("set %v: exit %d: %s %s", set, r.code, r.stdout, r.stderr)
		}
		if got := r.asked.Credentials[0].BaseURL.Reveal(); (got == proxyBase) != set {
			t.Errorf("set %v: the backends were asked for base URL %q", set, got)
		}
		reports, _ := filepath.Glob(filepath.Join(dir, "live-smoke-photo-*-muse-spark-1.3.json"))
		if len(reports) != 1 {
			t.Fatalf("set %v: reports %v", set, reports)
		}
		data, err := os.ReadFile(reports[0])
		if err != nil {
			t.Fatal(err)
		}
		var rep aievals.PhotoReport
		if err := json.Unmarshal(data, &rep); err != nil {
			t.Fatal(err)
		}
		if want := map[bool]string{true: config.BaseURLCustom, false: ""}[set]; rep.BaseURL != want {
			t.Errorf("set %v: base_url %q, want %q", set, rep.BaseURL, want)
		}
		record, _ := os.ReadFile(filepath.Join(dir, recordName))
		for _, leak := range []string{proxySeg, "proxy-test", "workers.dev", museKey} {
			if bytes.Contains(data, []byte(leak)) || bytes.Contains(record, []byte(leak)) || strings.Contains(r.stdout+r.stderr, leak) {
				t.Errorf("set %v: %q is in the report, the record or the output", set, leak)
			}
		}
	}
}
