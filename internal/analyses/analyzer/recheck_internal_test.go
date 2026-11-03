package analyzer

import (
	"bytes"
	"context"
	"encoding/json/jsontext"
	"encoding/json/v2"
	"strings"
	"testing"

	"github.com/kcl1104/geminus/internal/ai/provider"
	"github.com/kcl1104/geminus/internal/ai/provider/fake"
	"github.com/kcl1104/geminus/internal/audit"
	"github.com/kcl1104/geminus/internal/ids"
	"github.com/kcl1104/geminus/internal/proposals"
	"github.com/kcl1104/geminus/internal/storage/jobs"
)

// H-086 (R): what the job writes of the one re-ask, from analyze's own
// outcome over the fake behind a metered client. The completed analysis's
// stored metadata is the first call's, byte for byte as before without a
// re-ask, and with "repair" (outcome grounded, the issues listed, the second
// call's metadata) after one, which the API shows as
// generated_by.revised_after_check; the audit events of a completed and of a
// refused analysis carry the re-ask's fields and fit the catalogue; neither
// holds either answer's prose.
func TestWhatTheJobWritesOfTheReask(t *testing.T) {
	run := func(t *testing.T, script func(*fake.Backend)) (analyzed, error) {
		f := fake.New()
		script(f)
		l := &ledger{m: provider.NewMemoryMeter(1_000_000_000_000)}
		client, err := provider.NewMetered(billed{f}, provider.DefaultBudget(), l)
		if err != nil {
			t.Fatal(err)
		}
		var buf bytes.Buffer
		a, err := setup(t, client, nil, provider.Budget{}, &buf).analyze(context.Background(), tOrg, fixture(t), tAttr)
		// The memory meter's reservations are named as the ledger's are
		// (asr_...), as the audit catalogue takes them.
		if a.meta != nil && a.meta.SpendReservation != "" {
			a.meta.SpendReservation = ids.New("asr")
		}
		if a.recheck != nil && a.recheck.Call != nil && a.recheck.Call.SpendReservation != "" {
			a.recheck.Call.SpendReservation = ids.New("asr")
		}
		return a, err
	}
	event := func(action string, meta map[string]any) audit.Event {
		meta["attempt"] = 1
		return audit.Event{ActorType: audit.Worker, ActorID: "test-worker", Action: action, TargetType: "ai_analysis", TargetID: tAnalysis,
			RequestID: tRequest, Metadata: meta}
	}
	noProse := func(t *testing.T, what string, data []byte) {
		t.Helper()
		for _, leak := range []string{"4242", "Throughput rose", "guaranteed", tQuestion} {
			if strings.Contains(string(data), leak) {
				t.Errorf("%s holds %q: %s", what, leak, data)
			}
		}
	}

	// Asked again, and grounded.
	a, err := run(t, func(f *fake.Backend) {
		f.Script(provider.RoleAnalyst, tQuestion, invented(t))
		f.ScriptRevision(provider.RoleAnalyst, tQuestion, fixedAnswer(t))
	})
	if err != nil || a.recheck == nil || a.meta == nil {
		t.Fatalf("%v %+v", err, a)
	}
	stored, err := storedMetadata(*a.meta, a.recheck)
	if err != nil {
		t.Fatal(err)
	}
	var doc struct {
		TemplateVersion string `json:"prompt_template_version"`
		Repair          struct {
			Outcome   string             `json:"outcome"`
			Issues    int                `json:"issues"`
			NotListed *int               `json:"issues_not_listed"`
			Call      *provider.Metadata `json:"call"`
		} `json:"repair"`
	}
	if err := json.Unmarshal(stored, &doc); err != nil {
		t.Fatal(err)
	}
	if doc.TemplateVersion != "analyst-0.3.3" || doc.Repair.Outcome != "grounded" || doc.Repair.Issues != 1 || doc.Repair.NotListed != nil ||
		doc.Repair.Call == nil || doc.Repair.Call.TemplateVersion != "analyst-0.3.3+reground-0.1.0" || doc.Repair.Call.SpendReservation == "" {
		t.Errorf("the stored metadata %s", stored)
	}
	if g := proposals.FromMetadata(stored); g == nil || !g.RevisedAfterCheck {
		t.Errorf("generated_by %+v", g)
	}
	noProse(t, "the stored metadata", stored)
	m := auditMeta(*a.meta)
	m["deltas"], m["conclusions"], m["experiments"] = len(a.out.Delta), len(a.out.Bottleneck), len(a.out.SuggestedNextExperiments)
	recheckAuditMeta(m, a.recheck)
	if err := audit.Check(event("analysis.generate", m)); err != nil {
		t.Errorf("analysis.generate: %v", err)
	}
	if m["repair"] != "grounded" || m["repair_issues"] != 1 || m["repair_prompt_template_version"] != "analyst-0.3.3+reground-0.1.0" ||
		m["repair_spend_reservation_id"] == "" || m["repair_input_hash"] == m["input_hash"] {
		t.Errorf("the audit's re-ask fields %v", m)
	}
	// Without a re-ask the stored metadata is the call's own, as before
	// (compared canonically: its disclosure's maps have no order).
	plain, err := storedMetadata(*a.meta, nil)
	if err != nil {
		t.Fatal(err)
	}
	want, err := json.Marshal(a.meta)
	if err != nil {
		t.Fatal(err)
	}
	got, exp := jsontext.Value(plain), jsontext.Value(want)
	if err := got.Canonicalize(); err != nil {
		t.Fatal(err)
	}
	if err := exp.Canonicalize(); err != nil {
		t.Fatal(err)
	}
	if string(got) != string(exp) || strings.Contains(string(plain), "repair") {
		t.Errorf("without a re-ask: %s", plain)
	}

	// Asked again, refused again; and a re-ask whose call failed.
	for name, c := range map[string]struct {
		script  func(*fake.Backend)
		outcome string
	}{
		"refused again": {func(f *fake.Backend) {
			f.Script(provider.RoleAnalyst, tQuestion, invented(t))
			f.ScriptRevision(provider.RoleAnalyst, tQuestion, inventedAgain(t))
		}, "ungrounded"},
		"its call failed": {func(f *fake.Backend) {
			f.Script(provider.RoleAnalyst, tQuestion, invented(t))
			f.FailRevision(provider.RoleAnalyst, tQuestion, &provider.Error{Code: provider.CodeQuotaExceeded, Limit: provider.LimitInvestigation})
		}, "failed"},
	} {
		a, err := run(t, c.script)
		f, ok := err.(*jobs.Failure)
		if !ok || f.Code != Ungrounded || f.Retryable || a.recheck == nil || a.recheck.Outcome != c.outcome || a.meta == nil {
			t.Fatalf("%s: %v %+v", name, err, a)
		}
		m := auditMeta(*a.meta)
		m["code"], m["retryable"], m["issues"] = f.Code, f.Retryable, a.issues
		recheckAuditMeta(m, a.recheck)
		if err := audit.Check(event("analysis.attempt_failed", m)); err != nil {
			t.Errorf("%s: analysis.attempt_failed: %v", name, err)
		}
		if m["repair"] != c.outcome || (c.outcome == "failed") != (m["repair_code"] == "AI_QUOTA_EXCEEDED") {
			t.Errorf("%s: %v", name, m)
		}
		data, _ := json.Marshal(m)
		noProse(t, name+": the audit metadata", data)
	}
}
