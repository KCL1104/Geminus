package aiapproval_test

import (
	"errors"
	"os"
	"reflect"
	"slices"
	"strings"
	"testing"

	"github.com/kcl1104/geminus/internal/ai/provider/anthropic"
	"github.com/kcl1104/geminus/internal/ai/spend"
	"github.com/kcl1104/geminus/internal/config"
	"github.com/kcl1104/geminus/internal/config/aiapproval"
	"github.com/kcl1104/geminus/internal/secret"
)

// Obviously fake keys: no test here (or anywhere) reads a real one.
const keyA, keyB = "sk-ant-test-primary-0000000000", "sk-ant-test-secondary-1111111111"

// The owner's H-018 approval is recorded exactly as decided (T-053): Claude
// Opus 5.5 by its exact model ID, only synthetic data and text the user
// typed, no raw prompt or response in logs, USD 100 a month for both
// provider organizations combined, by the owner on 2026-10-20; the Console
// spend limits of both organizations confirmed by the owner (H-025,
// 2026-10-26); both organizations keep the default standard retention, no
// zero data retention, as the owner confirmed (H-026, 2026-10-26). The
// record lacks nothing, so it yields an approval.
func TestTheH018Record(t *testing.T) {
	r, ok := aiapproval.Lookup("H-018")
	if !ok {
		t.Fatal("H-018 is not recorded")
	}
	want := aiapproval.Record{
		ID:       "H-018",
		Provider: "anthropic",
		Model:    "claude-opus-5-5",
		DataPolicy: aiapproval.DataPolicy{
			Inputs:          aiapproval.InputsSyntheticAndUserText,
			Roles:           []string{"modeler", "scenario"},
			LogsKeepRawText: false,
		},
		MonthlyBudgetUSD: 100,
		Credentials:      []string{"org-a", "org-b"},
		ApprovedBy:       "KCL1104 (repository owner)",
		ApprovedAt:       "2026-10-20",
		Source:           "H-018 (planning/decisions.json human_decisions; decision page https://claude.ai/artifact/T5R9YeWWw4q2q8kKkGvU2P)",
		SpendLimits: aiapproval.Confirmation{By: "KCL1104 (repository owner)", At: "2026-10-26",
			Source: "H-025 (Console spend limits set for both provider organizations; relayed by the coordinator)"},
		Retention: h026,
	}
	if !reflect.DeepEqual(r, want) {
		t.Fatalf("the H-018 record is\n%+v\nwant\n%+v", r, want)
	}
	if got := r.Gap(); got != "" {
		t.Errorf("gap %q", got)
	}
	if a, ok := aiapproval.Recorded("H-018"); !ok || !a.Valid() || a.ID() != "H-018" || a.Model() != "claude-opus-5-5" {
		t.Errorf("H-018 yields no approval: %+v %v", a, ok)
	}
	// The record is a copy: changing it changes nothing recorded.
	r.Model, r.DataPolicy.Roles[0] = "other", "importer"
	if again, _ := aiapproval.Lookup("H-018"); again.Model != "claude-opus-5-5" || again.DataPolicy.Roles[0] != "modeler" {
		t.Error("Lookup hands out the recorded value itself")
	}
}

// Nothing but H-018 (and, since T-055, H-029: TestTheH029Record) is
// recorded, and the zero Approval is none.
func TestNothingElseIsRecorded(t *testing.T) {
	for _, id := range []string{"", "H-001", "H-017", "H-019", "H-025", "D-018", "h-018", "H-018 ", "approved", " "} {
		if _, ok := aiapproval.Lookup(id); ok {
			t.Errorf("%q is recorded", id)
		}
		if a, ok := aiapproval.Recorded(id); ok || a.Valid() || a.ID() != "" {
			t.Errorf("%q: %+v %v", id, a, ok)
		}
	}
	if (aiapproval.Approval{}).Valid() || (aiapproval.Approval{}).Model() != "" || (aiapproval.Approval{}).AllowsRole("modeler") {
		t.Error("the zero Approval is valid")
	}
}

// h026 is the owner's retention confirmation as H-026 records it: both
// provider organizations keep Anthropic's default standard retention, no
// zero data retention, the same for both (the owner's answer to D-113's
// question, 2026-10-26).
var h026 = aiapproval.Retention{Arrangement: aiapproval.RetentionStandard,
	Confirmation: aiapproval.Confirmation{By: "KCL1104 (repository owner)", At: "2026-10-26",
		Source: "H-026 (both provider organizations keep the default standard retention, no zero data retention; the owner's answer in the session, recorded by the coordinator)"}}

// complete is H-018 with the owner's retention confirmation of H-026.
func complete(t *testing.T) aiapproval.Record {
	t.Helper()
	r, ok := aiapproval.Lookup("H-018")
	if !ok {
		t.Fatal("H-018 is not recorded")
	}
	r.Retention = h026
	return r
}

// H-026: with the owner's retention confirmation, the build's own H-018
// record is complete. With every other condition met, in the process
// environment as the worker reads it (fake keys set with t.Setenv, AI_MODEL
// its model, the switch on), live AI turns on; without the switch it stays
// off and no key is read. ANTHROPIC_API_KEY is never asked for (the getenv
// below would not read it even if it were).
func TestH026TurnsLiveOn(t *testing.T) {
	r, ok := aiapproval.Lookup("H-018")
	if !ok || !reflect.DeepEqual(r.Retention, h026) || r.Gap() != "" {
		t.Fatalf("the build's H-018 record: retention %+v, gap %q", r.Retention, r.Gap())
	}
	if a, ok := aiapproval.Recorded("H-018"); !ok || !a.Valid() || a.ID() != "H-018" {
		t.Fatalf("no approval from the build's record: %+v %v", a, ok)
	}
	for k, v := range liveEnv() {
		if k != "ANTHROPIC_API_KEY" {
			t.Setenv(k, v)
		}
	}
	// The optional settings are unset, whatever the test process holds.
	for _, k := range []string{"AI_MONTHLY_LIMIT_USD", "AI_DAILY_LIMIT_USD", "AI_ORGANIZATION_DAILY_LIMIT_USD", "AI_MAX_PROMPT_BYTES",
		"AI_MAX_OUTPUT_TOKENS", "AI_TIMEOUT_SECONDS", "AI_ATTEMPT_TIMEOUT_SECONDS", "AI_MAX_RETRIES", "AI_MAX_CONCURRENT_PER_ORGANIZATION", "APP_ENV"} {
		t.Setenv(k, "")
	}
	var read []string
	getenv := func(k string) string {
		read = append(read, k)
		if strings.HasPrefix(k, "ANTHROPIC_") {
			return ""
		}
		return os.Getenv(k)
	}
	ai, err := config.LoadAI(getenv)
	if err != nil {
		t.Fatal(err)
	}
	if !ai.LiveEnabled() || ai.LiveOff != "" || ai.Approval.ID() != "H-018" || ai.Model != "claude-opus-5-5" || len(ai.Credentials) != 2 ||
		ai.Credentials[0].Label != "org-a" || ai.Credentials[0].Key.Reveal() != keyA ||
		ai.Credentials[1].Label != "org-b" || ai.Credentials[1].Key.Reveal() != keyB {
		t.Fatalf("live AI is not on: %+v", ai)
	}
	if want := spend.DefaultLimits(100 * spend.NanoPerUSD); ai.Limits != want {
		t.Errorf("limits %+v, want %+v", ai.Limits, want)
	}
	// The adapter takes the configuration (no request is sent).
	creds := []anthropic.Credential{{Label: "org-a", Key: ai.Credentials[0].Key}, {Label: "org-b", Key: ai.Credentials[1].Key}}
	if _, err := anthropic.New(anthropic.Config{Approval: ai.Approval, Model: ai.Model, Credentials: creds}); err != nil {
		t.Errorf("the adapter refuses the configuration: %v", err)
	}
	// Without the switch: off, for that reason, and no key is read.
	t.Setenv("GEMINUS_AI_LIVE", "")
	read = nil
	if ai, err := config.LoadAI(getenv); err != nil || ai.LiveEnabled() || ai.LiveOff != config.LiveOffSwitch || len(ai.Credentials) != 0 {
		t.Errorf("without the switch: %+v %v", ai, err)
	}
	if slices.ContainsFunc(read, func(k string) bool { return strings.HasPrefix(k, "GEMINUS_ANTHROPIC_API_KEY") }) {
		t.Errorf("a key was read without the switch: %v", read)
	}
	for _, k := range read {
		if strings.HasPrefix(k, "ANTHROPIC_") {
			t.Errorf("%s was asked for", k)
		}
	}
}

// An altered retention confirmation keeps live AI off with
// retention_unconfirmed: one arrangement for both organizations, named as
// this build knows it (so a record that tells the organizations apart,
// zero data retention on one of them, is not one), who confirmed it, a
// calendar date (YYYY-MM-DD) and the decision that records it. With the
// switch on the process refuses to start; with it off no key is read.
func TestAnAlteredRetentionRefuses(t *testing.T) {
	for name, alter := range map[string]func(*aiapproval.Retention){
		"zero data retention on one organization": func(r *aiapproval.Retention) {
			r.Arrangement = "org-a: standard; org-b: zero_data_retention"
		},
		"the arrangements of both, apart": func(r *aiapproval.Retention) { r.Arrangement = "standard,zero_data_retention" },
		"zdr as a shorthand":              func(r *aiapproval.Retention) { r.Arrangement = "zdr" },
		"standard capitalized":            func(r *aiapproval.Retention) { r.Arrangement = "Standard" },
		"no arrangement":                  func(r *aiapproval.Retention) { r.Arrangement = "" },
		"no source":                       func(r *aiapproval.Retention) { r.Source = "" },
		"no confirmer":                    func(r *aiapproval.Retention) { r.By = "" },
		"no date":                         func(r *aiapproval.Retention) { r.At = "" },
		"day first":                       func(r *aiapproval.Retention) { r.At = "26/10/2026" },
		"slashes":                         func(r *aiapproval.Retention) { r.At = "2026/10/26" },
		"with a time":                     func(r *aiapproval.Retention) { r.At = "2026-10-26T00:00:00Z" },
		"in words":                        func(r *aiapproval.Retention) { r.At = "26 October 2026" },
		"no such day":                     func(r *aiapproval.Retention) { r.At = "2026-02-30" },
		"a space before":                  func(r *aiapproval.Retention) { r.At = " 2026-10-26" },
	} {
		t.Run(name, func(t *testing.T) {
			r := complete(t)
			alter(&r.Retention)
			if got := r.Gap(); got != aiapproval.ReasonRetentionUnconfirmed {
				t.Fatalf("gap %q, want %q", got, aiapproval.ReasonRetentionUnconfirmed)
			}
			aiapproval.RecordForTest(t, r)
			if a, ok := aiapproval.Recorded("H-018"); ok || a.Valid() {
				t.Error("the altered record yields an approval")
			}
			if _, err := config.LoadAI(func(k string) string { return liveEnv()[k] }); !slices.Equal(codesOf(err), []string{"GEMINUS_AI_LIVE:retention_unconfirmed"}) {
				t.Errorf("switch on: %v", err)
			}
			rec := &recorder{kv: liveEnv()}
			delete(rec.kv, "GEMINUS_AI_LIVE")
			ai, err := config.LoadAI(rec.get)
			if err != nil || ai.LiveEnabled() || ai.LiveOff != string(aiapproval.ReasonRetentionUnconfirmed) || len(ai.Credentials) != 0 {
				t.Errorf("switch off: %+v %v", ai, err)
			}
			for _, k := range rec.read {
				if strings.HasPrefix(k, "GEMINUS_ANTHROPIC_API_KEY") || strings.HasPrefix(k, "ANTHROPIC_") {
					t.Errorf("read %s", k)
				}
			}
		})
	}
}

// Every gap of a record has its reason, and only a complete record has
// none.
func TestGapReasons(t *testing.T) {
	if got := complete(t).Gap(); got != "" {
		t.Fatalf("a complete record has gap %q", got)
	}
	for name, c := range map[string]struct {
		mutate func(*aiapproval.Record)
		want   aiapproval.Reason
	}{
		"no model":                 {func(r *aiapproval.Record) { r.Model = "" }, aiapproval.ReasonIncomplete},
		"other provider":           {func(r *aiapproval.Record) { r.Provider = "openai" }, aiapproval.ReasonIncomplete},
		"no approver":              {func(r *aiapproval.Record) { r.ApprovedBy = "" }, aiapproval.ReasonIncomplete},
		"no date":                  {func(r *aiapproval.Record) { r.ApprovedAt = "" }, aiapproval.ReasonIncomplete},
		"no source":                {func(r *aiapproval.Record) { r.Source = "" }, aiapproval.ReasonIncomplete},
		"one credential":           {func(r *aiapproval.Record) { r.Credentials = []string{"org-a"} }, aiapproval.ReasonIncomplete},
		"customer data":            {func(r *aiapproval.Record) { r.DataPolicy.Inputs = "customer_data" }, aiapproval.ReasonPolicyUnsupported},
		"raw text in logs":         {func(r *aiapproval.Record) { r.DataPolicy.LogsKeepRawText = true }, aiapproval.ReasonPolicyUnsupported},
		"no role":                  {func(r *aiapproval.Record) { r.DataPolicy.Roles = nil }, aiapproval.ReasonPolicyUnsupported},
		"importer role":            {func(r *aiapproval.Record) { r.DataPolicy.Roles = append(r.DataPolicy.Roles, "importer") }, aiapproval.ReasonPolicyUnsupported},
		"an unknown role":          {func(r *aiapproval.Record) { r.DataPolicy.Roles = []string{"critic"} }, aiapproval.ReasonPolicyUnsupported},
		"no budget":                {func(r *aiapproval.Record) { r.MonthlyBudgetUSD = 0 }, aiapproval.ReasonBudgetMissing},
		"negative budget":          {func(r *aiapproval.Record) { r.MonthlyBudgetUSD = -100 }, aiapproval.ReasonBudgetMissing},
		"spend limits unconfirmed": {func(r *aiapproval.Record) { r.SpendLimits = aiapproval.Confirmation{} }, aiapproval.ReasonSpendLimitsUnconfirmed},
		"spend limits half said":   {func(r *aiapproval.Record) { r.SpendLimits.Source = "" }, aiapproval.ReasonSpendLimitsUnconfirmed},
		"spend limits other date":  {func(r *aiapproval.Record) { r.SpendLimits.At = "26.10.2026" }, aiapproval.ReasonSpendLimitsUnconfirmed},
		"retention unconfirmed":    {func(r *aiapproval.Record) { r.Retention = aiapproval.Retention{} }, aiapproval.ReasonRetentionUnconfirmed},
		"retention not said":       {func(r *aiapproval.Record) { r.Retention.Arrangement = "" }, aiapproval.ReasonRetentionUnconfirmed},
		"retention unknown":        {func(r *aiapproval.Record) { r.Retention.Arrangement = "mixed" }, aiapproval.ReasonRetentionUnconfirmed},
		"retention without who":    {func(r *aiapproval.Record) { r.Retention.By = "" }, aiapproval.ReasonRetentionUnconfirmed},
	} {
		r := complete(t)
		c.mutate(&r)
		if got := r.Gap(); got != c.want {
			t.Errorf("%s: gap %q, want %q", name, got, c.want)
		}
	}
	// Zero data retention is the other arrangement the owner may confirm.
	r := complete(t)
	r.Retention.Arrangement = aiapproval.RetentionZero
	if got := r.Gap(); got != "" {
		t.Errorf("zero data retention: gap %q", got)
	}
}

// An Approval is made only from a complete record, and is valid while
// exactly that record stays recorded.
func TestRecorded(t *testing.T) {
	var kept aiapproval.Approval
	t.Run("recorded", func(t *testing.T) {
		// A complete record of this test only (the build's own is complete
		// too since H-026; a token must not outlive the one it came from).
		own := complete(t)
		own.Retention.Source = "H-TEST (this test's record only)"
		aiapproval.RecordForTest(t, own)
		a, ok := aiapproval.Recorded("H-018")
		if !ok || !a.Valid() || a.ID() != "H-018" || a.Model() != "claude-opus-5-5" || a.MonthlyBudgetUSD() != 100 {
			t.Fatalf("%+v %v", a, ok)
		}
		if !a.AllowsRole("modeler") || !a.AllowsRole("scenario") || a.AllowsRole("importer") || a.AllowsRole("analyst") || a.AllowsRole("") {
			t.Error("roles")
		}
		if !slices.Equal(a.Credentials(), []string{"org-a", "org-b"}) {
			t.Errorf("credentials %v", a.Credentials())
		}
		kept = a
		// A token of the complete record is not valid for a changed one.
		changed := complete(t)
		changed.Model = "claude-opus-5"
		aiapproval.RecordForTest(t, changed)
		if a.Valid() || a.Model() != "" || a.AllowsRole("modeler") {
			t.Error("a token outlives a change of its record")
		}
		// The changed record yields a token of its own, for its own model.
		if b, ok := aiapproval.Recorded("H-018"); !ok || b.Model() != "claude-opus-5" {
			t.Errorf("the changed record: %+v %v", b, ok)
		}
		// An incomplete one yields none.
		changed.Retention = aiapproval.Retention{}
		aiapproval.RecordForTest(t, changed)
		if b, ok := aiapproval.Recorded("H-018"); ok || b.Valid() {
			t.Error("an incomplete record yields an approval")
		}
	})
	if kept.Valid() {
		t.Error("an Approval outlives its record")
	}
	// After the test, the build's own record is back (complete since
	// H-026).
	if r, ok := aiapproval.Lookup("H-018"); !ok || r.Gap() != "" || !reflect.DeepEqual(r.Retention, h026) || r.Model != "claude-opus-5-5" {
		t.Errorf("the H-018 record after the test: %+v %v", r, ok)
	}
}

// The live adapter takes the approval's own model and credentials only,
// and serves the roles its data policy names. No request is sent: New
// opens no connection.
func TestTheAdapterTakesOnlyTheApproval(t *testing.T) {
	aiapproval.RecordForTest(t, complete(t))
	a, ok := aiapproval.Recorded("H-018")
	if !ok {
		t.Fatal("no approval")
	}
	creds := []anthropic.Credential{{Label: "org-a", Key: secret.Secret(keyA)}, {Label: "org-b", Key: secret.Secret(keyB)}}
	if _, err := anthropic.New(anthropic.Config{Approval: a, Model: "claude-opus-5-5", Credentials: creds}); err != nil {
		t.Fatalf("the approved configuration: %v", err)
	}
	for name, cfg := range map[string]anthropic.Config{
		"other model":       {Approval: a, Model: "claude-opus-5", Credentials: creds},
		"no model":          {Approval: a, Credentials: creds},
		"one credential":    {Approval: a, Model: "claude-opus-5-5", Credentials: creds[:1]},
		"swapped order":     {Approval: a, Model: "claude-opus-5-5", Credentials: []anthropic.Credential{creds[1], creds[0]}},
		"other label":       {Approval: a, Model: "claude-opus-5-5", Credentials: []anthropic.Credential{creds[0], {Label: "org-c", Key: secret.Secret(keyB)}}},
		"no approval":       {Model: "claude-opus-5-5", Credentials: creds},
		"incomplete record": {Approval: aiapproval.Approval{}, Model: "claude-opus-5-5", Credentials: creds},
	} {
		_, err := anthropic.New(cfg)
		switch {
		case err == nil:
			t.Errorf("%s: accepted", name)
		case strings.Contains(err.Error(), keyA) || strings.Contains(err.Error(), keyB):
			t.Errorf("%s: the error leaks a key", name)
		case errors.Is(err, anthropic.ErrNotApproved) != (cfg.Approval == aiapproval.Approval{}):
			// The approval is checked first, for itself: a missing one is
			// ErrNotApproved even with the right model and credentials
			// (mutation run 1: U24 survived on the model check alone).
			t.Errorf("%s: %v", name, err)
		}
	}
	// The record changes after the token was made: the adapter refuses it.
	changed := complete(t)
	changed.Retention = aiapproval.Retention{}
	aiapproval.RecordForTest(t, changed)
	if _, err := anthropic.New(anthropic.Config{Approval: a, Model: "claude-opus-5-5", Credentials: creds}); !errors.Is(err, anthropic.ErrNotApproved) {
		t.Errorf("a token of a record that is no longer complete: %v", err)
	}
}

type recorder struct {
	kv   map[string]string
	read []string
}

func (r *recorder) get(k string) string {
	r.read = append(r.read, k)
	return r.kv[k]
}

// liveEnv is everything live AI needs: the provider, the approval named,
// its model, both keys and the operator's switch.
func liveEnv() map[string]string {
	return map[string]string{"AI_PROVIDER": "anthropic", "AI_POLICY_APPROVAL": "H-018", "AI_MODEL": "claude-opus-5-5",
		"GEMINUS_ANTHROPIC_API_KEY_A": keyA, "GEMINUS_ANTHROPIC_API_KEY_B": keyB, "GEMINUS_AI_LIVE": "on",
		"ANTHROPIC_API_KEY": "must-not-be-read"}
}

func codesOf(err error) []string {
	var out []string
	var joined interface{ Unwrap() []error }
	errs := []error{err}
	if errors.As(err, &joined) {
		errs = joined.Unwrap()
	}
	for _, e := range errs {
		var ce *config.Error
		if errors.As(e, &ce) {
			out = append(out, ce.Var+":"+ce.Code)
		}
	}
	slices.Sort(out)
	return out
}

// T-053: live AI turns on with exactly the H-018 record (complete), both
// keys and the operator's switch; config then reads both keys (D-018: A
// primary, B secondary, labelled org-a and org-b), the model and the
// spend limits, and never ANTHROPIC_API_KEY.
func TestLiveTurnsOn(t *testing.T) {
	aiapproval.RecordForTest(t, complete(t))
	r := &recorder{kv: liveEnv()}
	r.kv["AI_MAX_OUTPUT_TOKENS"], r.kv["AI_MAX_RETRIES"] = "4000", "0"
	ai, err := config.LoadAI(r.get)
	if err != nil {
		t.Fatal(err)
	}
	if !ai.LiveEnabled() || ai.LiveOff != "" || ai.Approval.ID() != "H-018" || ai.Model != "claude-opus-5-5" || len(ai.Credentials) != 2 ||
		ai.Credentials[0].Label != "org-a" || ai.Credentials[0].Key.Reveal() != keyA ||
		ai.Credentials[1].Label != "org-b" || ai.Credentials[1].Key.Reveal() != keyB {
		t.Fatalf("%+v", ai)
	}
	if want := spend.DefaultLimits(100 * spend.NanoPerUSD); ai.Limits != want {
		t.Errorf("limits %+v, want %+v", ai.Limits, want)
	}
	if ai.Budget.MaxOutputTokens != 4000 || ai.Budget.MaxRetries == nil || *ai.Budget.MaxRetries != 0 {
		t.Errorf("budget %+v", ai.Budget)
	}
	if slices.Contains(r.read, "ANTHROPIC_API_KEY") {
		t.Error("ANTHROPIC_API_KEY was read")
	}
	// Lower limits a deployment sets are kept.
	r.kv["AI_MONTHLY_LIMIT_USD"], r.kv["AI_DAILY_LIMIT_USD"], r.kv["AI_ORGANIZATION_DAILY_LIMIT_USD"] = "50", "4.5", "0.25"
	if ai, err := config.LoadAI(r.get); err != nil || ai.Limits != (spend.Limits{Month: 50 * spend.NanoPerUSD, Day: 4_500_000_000, OrganizationDay: 250_000_000}) {
		t.Errorf("set limits: %+v %v", ai.Limits, err)
	}
}

// Without the operator's switch, a complete record keeps live AI off:
// no key is read, and the reason is typed.
func TestTheSwitchIsRequired(t *testing.T) {
	aiapproval.RecordForTest(t, complete(t))
	for _, v := range []string{"", "off"} {
		r := &recorder{kv: liveEnv()}
		r.kv["GEMINUS_AI_LIVE"] = v
		ai, err := config.LoadAI(r.get)
		if err != nil {
			t.Fatalf("%q: %v", v, err)
		}
		if ai.LiveEnabled() || ai.LiveOff != config.LiveOffSwitch || len(ai.Credentials) != 0 || ai.Approval.Valid() {
			t.Errorf("%q: %+v", v, ai)
		}
		for _, k := range r.read {
			if strings.HasPrefix(k, "GEMINUS_ANTHROPIC_API_KEY") || strings.HasPrefix(k, "ANTHROPIC_") {
				t.Errorf("%q: read %s", v, k)
			}
		}
	}
	for _, v := range []string{"ON", "true", "1", "yes", "on "} {
		kv := liveEnv()
		kv["GEMINUS_AI_LIVE"] = v
		if _, err := config.LoadAI(func(k string) string { return kv[k] }); !slices.Equal(codesOf(err), []string{"GEMINUS_AI_LIVE:invalid"}) {
			t.Errorf("%q: %v", v, err)
		}
	}
}

// Any mismatch keeps live AI off with a typed reason. With the switch on
// the process refuses to start (fail closed and loud); with it off the
// same reason is reported and the provider answers policy_not_approved.
// No key is read in either case.
func TestAnyMismatchKeepsLiveOff(t *testing.T) {
	type mismatch struct {
		record func(*aiapproval.Record)
		env    map[string]string
		want   string // the typed reason
		on     []string
	}
	cases := map[string]mismatch{
		"the record before H-026 (retention unconfirmed)": {record: func(r *aiapproval.Record) { r.Retention = aiapproval.Retention{} },
			want: "retention_unconfirmed", on: []string{"GEMINUS_AI_LIVE:retention_unconfirmed"}},
		"spend limits unconfirmed": {record: func(r *aiapproval.Record) { r.SpendLimits = aiapproval.Confirmation{} },
			want: "spend_limits_unconfirmed", on: []string{"GEMINUS_AI_LIVE:spend_limits_unconfirmed"}},
		"changed policy": {record: func(r *aiapproval.Record) { r.DataPolicy.Inputs = "customer_data" },
			want: "policy_unsupported", on: []string{"GEMINUS_AI_LIVE:policy_unsupported"}},
		"raw text in logs": {record: func(r *aiapproval.Record) { r.DataPolicy.LogsKeepRawText = true },
			want: "policy_unsupported", on: []string{"GEMINUS_AI_LIVE:policy_unsupported"}},
		"recorded model without a price": {record: func(r *aiapproval.Record) { r.Model = "claude-opus-5" },
			env: map[string]string{"AI_MODEL": "claude-opus-5"}, want: "model_unpriced", on: []string{"GEMINUS_AI_LIVE:model_unpriced"}},
		"other model configured": {env: map[string]string{"AI_MODEL": "claude-sonnet-5-5"},
			want: "model_mismatch", on: []string{"AI_MODEL:model_mismatch"}},
		"model with a date suffix": {env: map[string]string{"AI_MODEL": "claude-opus-5-5-20260401"},
			want: "model_mismatch", on: []string{"AI_MODEL:model_mismatch"}},
		"no model configured": {env: map[string]string{"AI_MODEL": ""},
			want: "model_mismatch", on: []string{"AI_MODEL:required"}},
	}
	for name, c := range cases {
		t.Run(name, func(t *testing.T) {
			r := complete(t)
			if c.record != nil {
				c.record(&r)
			}
			aiapproval.RecordForTest(t, r)
			for _, sw := range []string{"on", ""} {
				rec := &recorder{kv: liveEnv()}
				for k, v := range c.env {
					rec.kv[k] = v
				}
				rec.kv["GEMINUS_AI_LIVE"] = sw
				ai, err := config.LoadAI(rec.get)
				if sw == "on" {
					if got := codesOf(err); !slices.Equal(got, c.on) {
						t.Errorf("switch on: codes %v, want %v (%v)", got, c.on, err)
					}
					if err != nil && (strings.Contains(err.Error(), keyA) || strings.Contains(err.Error(), keyB)) {
						t.Error("the error leaks a key")
					}
				} else {
					if err != nil {
						t.Fatalf("switch off: %v", err)
					}
					if ai.LiveEnabled() || ai.LiveOff != c.want || len(ai.Credentials) != 0 {
						t.Errorf("switch off: %+v, want reason %q", ai, c.want)
					}
				}
				for _, k := range rec.read {
					if strings.HasPrefix(k, "GEMINUS_ANTHROPIC_API_KEY") || k == "ANTHROPIC_API_KEY" {
						t.Errorf("switch %q: read %s", sw, k)
					}
				}
			}
		})
	}
}

// Both credentials are required (T-053; D-018's two organizations); a
// missing one keeps live off and names the variable, never a value.
func TestBothCredentialsAreRequired(t *testing.T) {
	aiapproval.RecordForTest(t, complete(t))
	for name, c := range map[string]struct {
		unset []string
		want  []string
	}{
		"no primary":   {[]string{"GEMINUS_ANTHROPIC_API_KEY_A"}, []string{"GEMINUS_ANTHROPIC_API_KEY_A:required"}},
		"no secondary": {[]string{"GEMINUS_ANTHROPIC_API_KEY_B"}, []string{"GEMINUS_ANTHROPIC_API_KEY_B:required"}},
		"neither":      {[]string{"GEMINUS_ANTHROPIC_API_KEY_A", "GEMINUS_ANTHROPIC_API_KEY_B"}, []string{"GEMINUS_ANTHROPIC_API_KEY_A:required", "GEMINUS_ANTHROPIC_API_KEY_B:required"}},
	} {
		kv := liveEnv()
		for _, k := range c.unset {
			delete(kv, k)
		}
		_, err := config.LoadAI(func(k string) string { return kv[k] })
		if got := codesOf(err); !slices.Equal(got, c.want) {
			t.Errorf("%s: %v, want %v", name, got, c.want)
		}
		if err != nil && (strings.Contains(err.Error(), keyA) || strings.Contains(err.Error(), keyB)) {
			t.Errorf("%s: the error leaks a key", name)
		}
	}
	// The same key twice is one organization, not two.
	kv := liveEnv()
	kv["GEMINUS_ANTHROPIC_API_KEY_B"] = keyA
	if _, err := config.LoadAI(func(k string) string { return kv[k] }); !slices.Equal(codesOf(err), []string{"GEMINUS_ANTHROPIC_API_KEY_B:invalid"}) {
		t.Errorf("one key twice: %v", err)
	}
}

// The spend limits are the record's monthly budget at most, the
// deployment's day within the month, an organization's day within the
// deployment's day.
func TestSpendLimitsAreChecked(t *testing.T) {
	aiapproval.RecordForTest(t, complete(t))
	for name, c := range map[string]struct {
		kv   map[string]string
		want []string
	}{
		"month above the record":   {map[string]string{"AI_MONTHLY_LIMIT_USD": "100.01"}, []string{"AI_MONTHLY_LIMIT_USD:invalid"}},
		"day above the month":      {map[string]string{"AI_MONTHLY_LIMIT_USD": "5", "AI_DAILY_LIMIT_USD": "6"}, []string{"AI_DAILY_LIMIT_USD:invalid"}},
		"org day above the day":    {map[string]string{"AI_DAILY_LIMIT_USD": "2", "AI_ORGANIZATION_DAILY_LIMIT_USD": "3"}, []string{"AI_ORGANIZATION_DAILY_LIMIT_USD:invalid"}},
		"not an amount":            {map[string]string{"AI_DAILY_LIMIT_USD": "ten"}, []string{"AI_DAILY_LIMIT_USD:invalid"}},
		"zero":                     {map[string]string{"AI_ORGANIZATION_DAILY_LIMIT_USD": "0"}, []string{"AI_ORGANIZATION_DAILY_LIMIT_USD:invalid"}},
		"switch without anthropic": {map[string]string{"AI_PROVIDER": "fake", "APP_ENV": "test", "AI_POLICY_APPROVAL": ""}, []string{"GEMINUS_AI_LIVE:invalid"}},
		"switch without approval":  {map[string]string{"AI_POLICY_APPROVAL": ""}, []string{"GEMINUS_AI_LIVE:invalid"}},
	} {
		kv := liveEnv()
		for k, v := range c.kv {
			kv[k] = v
		}
		_, err := config.LoadAI(func(k string) string { return kv[k] })
		if got := codesOf(err); !slices.Equal(got, c.want) {
			t.Errorf("%s: %v, want %v (%v)", name, got, c.want, err)
		}
	}
}
