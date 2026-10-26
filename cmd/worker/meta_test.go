package main

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"image"
	"image/png"
	"strings"
	"testing"
	"time"

	"github.com/kcl1104/geminus/internal/ai/provider"
	"github.com/kcl1104/geminus/internal/config"
	"github.com/kcl1104/geminus/internal/ids"
	"github.com/kcl1104/geminus/internal/storage/aispend"
	"github.com/kcl1104/geminus/internal/tenant"
)

// T-055: these tests build the live Meta clients but never call one; every
// call made here goes to Disabled or to a fake. The key is a placeholder in
// the documented shape.
const museKey = "LLM_000000000000_test-key-SENTINEL-not-a-real-key"

// metaEnv is H-029 complete but for the switch, plus kv.
func metaEnv(kv ...string) func(string) string {
	return with(append([]string{"AI_PROVIDER", "meta", "AI_POLICY_APPROVAL", "H-029", "AI_MODEL", "muse-spark-1.3",
		"GEMINUS_MUSE_API_KEY", museKey}, kv...)...)
}

// h018 is H-018 complete but for the switch.
var h018 = []string{"AI_PROVIDER", "anthropic", "AI_POLICY_APPROVAL", "H-018", "AI_MODEL", "claude-opus-5-5",
	"GEMINUS_ANTHROPIC_API_KEY_A", "sk-ant-test-fake-aaaaaaaaaaaa", "GEMINUS_ANTHROPIC_API_KEY_B", "sk-ant-test-fake-bbbbbbbbbbbb"}

// unusedLedger is a spend runner no test may reach: a reservation through
// it would mean a live call was attempted.
func unusedLedger(t *testing.T) aispend.Runner {
	return func(context.Context, string, func(context.Context, tenant.Tx) error) error {
		t.Error("the spend ledger was used: a live call was attempted")
		return errors.New("no ledger here")
	}
}

func loadAI(t *testing.T, getenv func(string) string) config.AI {
	t.Helper()
	ai, err := config.LoadAI(getenv)
	if err != nil {
		t.Fatal(err)
	}
	return ai
}

// T-055: AI_PROVIDER=meta reaches the Muse Spark adapter as anthropic
// reaches Claude's: only with H-029, its model, the key and the switch, and
// only over the spend ledger. Until then every proposal answers
// policy_not_approved.
func TestTheMetaProviderIsTheConfigured(t *testing.T) {
	ctx := context.Background()
	unsealed := provider.ProposalContext{OrganizationID: ids.New("org"), Mode: provider.ModeEdit}
	for name, getenv := range map[string]func(string) string{
		"meta without an approval": with("AI_PROVIDER", "meta"),
		"H-029 without the switch": metaEnv(),
		"H-029 with Claude's model": with("AI_PROVIDER", "meta", "AI_POLICY_APPROVAL", "H-029", "AI_MODEL", "claude-opus-5-5",
			"GEMINUS_MUSE_API_KEY", museKey),
	} {
		ai := loadAI(t, getenv)
		if ai.LiveEnabled() {
			t.Fatalf("%s: live", name)
		}
		client, _, err := aiClient(ai, nil, "test-worker")
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		if _, err := client.GenerateModelProposal(ctx, unsealed); provider.CodeOf(err) != provider.CodePolicyNotApproved {
			t.Errorf("%s: %v", name, err)
		}
	}
	if _, err := config.LoadAI(with("AI_PROVIDER", "meta", "AI_POLICY_APPROVAL", "H-029", "AI_MODEL", "muse-spark-1.3", "GEMINUS_AI_LIVE", "on")); err == nil {
		t.Error("the switch without the key was accepted")
	}
	live := loadAI(t, metaEnv("GEMINUS_AI_LIVE", "on"))
	if !live.LiveEnabled() || live.Provider != config.AIProviderMeta {
		t.Fatalf("H-029 with the switch: %+v", live)
	}
	b, err := liveBackend(live.Provider, live.Approval, live.Model, live.Credentials)
	if err != nil || fmt.Sprintf("%T", b) != "*meta.Backend" {
		t.Fatalf("the live backend is a %T: %v", b, err)
	}
	if c, _, err := aiClient(live, nil, "test-worker"); err == nil || c != nil {
		t.Error("the worker built a live client without the spend ledger")
	}
	if c, _, err := aiClient(live, unusedLedger(t), "test-worker"); err != nil || c == nil {
		t.Errorf("the metered live client: %v", err)
	}
	// An approval goes to its own provider's adapter only.
	if b, err := liveBackend(config.AIProviderAnthropic, live.Approval, "muse-spark-1.3", live.Credentials); err == nil {
		t.Errorf("Claude's adapter took H-029: %T", b)
	}
	claude := loadAI(t, with(append(h018, "GEMINUS_AI_LIVE", "on")...))
	if b, err := liveBackend(config.AIProviderMeta, claude.Approval, claude.Model, claude.Credentials); err == nil {
		t.Errorf("the Meta adapter took H-018: %T", b)
	}
	if b, err := liveBackend(config.AIProviderFake, live.Approval, live.Model, live.Credentials); err == nil {
		t.Errorf("a live backend for the fake: %T", b)
	}
}

// pngPhoto is a small synthetic picture, as the photo path's tests send.
func pngPhoto(t *testing.T) provider.Image {
	t.Helper()
	var buf bytes.Buffer
	if err := png.Encode(&buf, image.NewGray(image.Rect(0, 0, 40, 30))); err != nil {
		t.Fatal(err)
	}
	img, err := provider.NewImage(provider.ImagePNG, buf.Bytes())
	if err != nil {
		t.Fatal(err)
	}
	return img
}

// T-055 (for T-056; H-033): the photo path's providers are a setting, not
// an API change. AI_VISION_PROVIDER unset follows AI_PROVIDER; the judge
// (a client whose backend reads images) and the segmenter (SAM 3.1) are
// the configured provider's, live only with an approval that names images
// (H-029) and the switch, over the ledger; otherwise they answer the code
// a proposal would (not_configured, policy_not_approved), and with Claude,
// whose approval names no image, the photo path stays off while the
// proposals run.
func TestThePhotoPath(t *testing.T) {
	ctx := context.Background()
	img := pngPhoto(t)
	org := ids.New("org")
	unsealed := provider.ProposalContext{OrganizationID: org, Mode: provider.ModeEdit}
	seg := provider.SegmentRequest{OrganizationID: org, Role: provider.RoleModeler, Image: img, Concepts: []string{"station"},
		Attribution: provider.Attribution{TargetType: "ai_proposal", TargetID: ids.New("aip"), RequestID: ids.New("req")}}
	mixed := append(append([]string{}, h018...), "GEMINUS_AI_LIVE", "on", "AI_VISION_PROVIDER", "meta",
		"AI_VISION_POLICY_APPROVAL", "H-029", "AI_VISION_MODEL", "muse-spark-1.3", "GEMINUS_MUSE_API_KEY", museKey)
	for _, c := range []struct {
		name       string
		env        func(string) string
		judge, seg string
		code       provider.Code // "" for a backend that is called here (the fakes) or never (live)
		live       bool
	}{
		{"default", with(), "provider.Disabled", "provider.Disabled", provider.CodeNotConfigured, false},
		{"turned off beside the fake", with("AI_PROVIDER", "fake", "APP_ENV", "test", "AI_VISION_PROVIDER", "disabled"),
			"provider.Disabled", "provider.Disabled", provider.CodeNotConfigured, false},
		{"the fake", with("AI_PROVIDER", "fake", "APP_ENV", "test"), "*fake.Backend", "*fake.Segmenter", "", false},
		{"meta without the switch", metaEnv(), "provider.Disabled", "provider.Disabled", provider.CodePolicyNotApproved, false},
		{"claude: no image approved", with(append(h018, "GEMINUS_AI_LIVE", "on")...), "provider.Disabled", "provider.Disabled",
			provider.CodePolicyNotApproved, false},
		{"meta with the switch", metaEnv("GEMINUS_AI_LIVE", "on"), "*meta.Backend", "*meta.Segmenter", "", true},
		{"claude for proposals, meta for photos", with(mixed...), "*meta.Backend", "*meta.Segmenter", "", true},
	} {
		t.Run(c.name, func(t *testing.T) {
			ai := loadAI(t, c.env)
			judge, segmenter, err := photoBackends(ai.Vision)
			if err != nil || fmt.Sprintf("%T", judge) != c.judge || fmt.Sprintf("%T", segmenter) != c.seg {
				t.Fatalf("backends %T %T: %v", judge, segmenter, err)
			}
			if in, ok := judge.(provider.ImageInput); c.judge != "provider.Disabled" && (!ok || !in.AcceptsImages()) {
				t.Errorf("the judge reads no image: %T", judge)
			}
			if c.live {
				if _, err := photoPath(ai, provider.DefaultBudget(), nil, "test-worker"); err == nil {
					t.Error("the live photo path was built without the spend ledger")
				}
				p, err := photoPath(ai, provider.DefaultBudget(), unusedLedger(t), "test-worker")
				if err != nil || p.Judge == nil || p.Segmenter == nil {
					t.Fatalf("the live photo path: %+v %v", p, err)
				}
				return // built, never called
			}
			p, err := photoPath(ai, provider.DefaultBudget(), nil, "test-worker")
			if err != nil {
				t.Fatal(err)
			}
			if c.code == "" { // the fakes: reached, so the client's checks ran
				if _, err := p.Judge.GenerateModelProposal(ctx, unsealed); provider.CodeOf(err) != provider.CodeInvalidRequest {
					t.Errorf("the fake judge: %v", err)
				}
				if out, err := p.Segmenter.Segment(ctx, seg); err != nil || len(out.Segments) != 0 {
					t.Errorf("the fake segmenter: %+v %v", out, err)
				}
				return
			}
			if _, err := p.Judge.GenerateModelProposal(ctx, unsealed); provider.CodeOf(err) != c.code {
				t.Errorf("judge: %v, want %s", err, c.code)
			}
			if _, err := p.Segmenter.Segment(ctx, seg); provider.CodeOf(err) != c.code {
				t.Errorf("segmenter: %v, want %s", err, c.code)
			}
		})
	}
	// With Claude for proposals and Meta for photos, the proposals stay
	// Claude's.
	ai := loadAI(t, with(mixed...))
	if b, err := liveBackend(ai.Provider, ai.Approval, ai.Model, ai.Credentials); err != nil || fmt.Sprintf("%T", b) != "*anthropic.Backend" {
		t.Errorf("the proposals' backend is a %T: %v", b, err)
	}
}

// T-055: with Meta live, the worker says so (the model, the photo path's
// providers, typed codes) and never prints the key; it gets as far as the
// database check, which fails here, so no job and no call runs.
func TestRunNamesTheLiveMetaProvider(t *testing.T) {
	for name, c := range map[string]struct {
		env  func(string) string
		want []string
	}{
		"meta live": {metaEnv("GEMINUS_AI_LIVE", "on"), []string{`"msg":"live AI is on"`, `"code":"LIVE_AI_ON"`,
			`"model":"muse-spark-1.3"`, `"msg":"the photo path is on"`, `"provider":"meta"`, `"code":"PHOTO_PATH_ON"`}},
		"meta off": {metaEnv(), []string{`"msg":"live AI is off"`, `"code":"SWITCH_OFF"`, `"msg":"the photo path is off"`}},
		"claude live, photos off": {with(append(h018, "GEMINUS_AI_LIVE", "on")...), []string{`"code":"LIVE_AI_ON"`,
			`"msg":"the photo path is off"`, `"code":"IMAGES_NOT_APPROVED"`}},
	} {
		var stderr bytes.Buffer
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		code := run(ctx, c.env, &stderr, someHandler)
		cancel()
		out := stderr.String()
		if code != exitDatabase {
			t.Errorf("%s: exit %d: %s", name, code, out)
		}
		for _, w := range c.want {
			if !strings.Contains(out, w) {
				t.Errorf("%s: no %s in %s", name, w, out)
			}
		}
		for _, leak := range []string{museKey, "SENTINEL", "test-key", "sk-ant-test"} {
			if strings.Contains(out, leak) {
				t.Errorf("%s: the log holds a key: %s", name, out)
			}
		}
	}
}
