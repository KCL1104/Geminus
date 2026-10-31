package generator

import (
	"context"
	"encoding/json/v2"
	"errors"
	"strings"
	"testing"

	"github.com/kcl1104/geminus/internal/ai/provider"
	"github.com/kcl1104/geminus/internal/ai/provider/fake"
	"github.com/kcl1104/geminus/internal/proposals"
	"github.com/kcl1104/geminus/internal/storage/jobs"
	store "github.com/kcl1104/geminus/internal/storage/proposals"
)

// H-049: a job that carries the person's choice of model asks exactly that
// model, in words and from a photo; a provider that may not ask it fails the
// proposal for good before any call (AI_MODEL_NOT_APPROVED, shown as
// AI_POLICY_NOT_APPROVED), and for a photo before the segmenter spends
// anything. A job without a choice asks the deployment's model.
func TestTheChosenModelIsAsked(t *testing.T) {
	ctx := context.Background()
	h := func(b provider.Backend) *Handler {
		h, err := New(Config{Provider: client(t, b), Packs: packs(t), Actor: "worker-1", Log: discard()})
		if err != nil {
			t.Fatal(err)
		}
		return h
	}
	f := fake.New()
	st := stored(t, "scenario", "Add a second oven.")
	for _, research := range []bool{false, true} {
		if _, err := h(f).generateAsked(ctx, st.proposal.OrganizationID, st, testAttribution, research, "muse-wonder-1.0"); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := h(f).generateAsked(ctx, st.proposal.OrganizationID, st, testAttribution, false, ""); err != nil {
		t.Fatal(err)
	}
	reqs := f.Requests()
	if len(reqs) != 3 || reqs[0].Model != "muse-wonder-1.0" || reqs[1].Model != "muse-wonder-1.0" || !reqs[1].Prompt.Research || reqs[2].Model != "" {
		t.Fatalf("requests %+v", reqs)
	}

	// A backend that asks its own model only (as the Claude adapter does).
	only := fake.New()
	_, err := h(noResearch{only}).generateAsked(ctx, st.proposal.OrganizationID, st, testAttribution, false, "muse-wonder-1.0")
	var jf *jobs.Failure
	if !errors.As(err, &jf) || jf.Code != "AI_MODEL_NOT_APPROVED" || jf.Retryable || jf.Message != providerMessages[provider.CodeModelNotApproved] ||
		len(only.Requests()) != 0 {
		t.Fatalf("a backend that cannot ask the model: %v (%d requests)", err, len(only.Requests()))
	}
	failure, _ := json.Marshal(jf)
	pub, err := proposals.Public(store.Proposal{ID: st.proposal.ID, Status: store.Failed, Mode: "scenario", Failure: failure,
		RequestID: "req_01M4YDQK00ADN261CME3WV5HZ6"}, "")
	if err != nil || pub.Error.Error.Code != "AI_POLICY_NOT_APPROVED" || pub.Error.Error.Details[0].Code != "AI_MODEL_NOT_APPROVED" {
		t.Errorf("shown as %+v %v", pub.Error, err)
	}
}

// Review round 1, M1: a Muse Wonder attempt's failure is final whatever its
// code (its job has a single attempt too, internal/proposals): a call that
// timed out at 900 s (H-065) is not run, and billed, again. The same failure on Muse
// Spark 1.3, or on the deployment's model, stays retryable.
func TestASlowModelsFailureIsFinal(t *testing.T) {
	ctx := context.Background()
	for _, code := range []provider.Code{provider.CodeTimeout, provider.CodeUnavailable, provider.CodeRateLimited} {
		f := fake.New()
		f.Fail(provider.RoleModeler, "Busy.", provider.NewError(code, 0, "meta"))
		h, err := New(Config{Provider: client(t, f), Packs: packs(t), Actor: "worker-1", Log: discard()})
		if err != nil {
			t.Fatal(err)
		}
		st := stored(t, "edit", "Busy.")
		var jf *jobs.Failure
		_, err = h.generateAsked(ctx, st.proposal.OrganizationID, st, testAttribution, false, "muse-wonder-1.0")
		if !errors.As(err, &jf) || jf.Retryable || jf.Code != "AI_"+strings.ToUpper(string(code)) {
			t.Errorf("%s on Muse Wonder: %+v", code, jf)
		}
		for _, m := range []string{"muse-spark-1.3", ""} {
			_, err = h.generateAsked(ctx, st.proposal.OrganizationID, st, testAttribution, false, m)
			if !errors.As(err, &jf) || !jf.Retryable {
				t.Errorf("%s on %q: %+v", code, m, jf)
			}
		}
	}
	// A photo on Muse Wonder whose vision call fails: final too.
	r := newPhotoRig(t, "electronics-line-demo")
	r.script(-1)
	r.vision.Fail(provider.RoleModeler, r.demo.Segments.StoredDigest, provider.NewError(provider.CodeTimeout, 0, "meta"))
	var jf *jobs.Failure
	if _, err := r.h.generatePhoto(ctx, photoOrg, r.st, testAttribution, "muse-wonder-1.0"); !errors.As(err, &jf) || jf.Retryable || jf.Code != "AI_TIMEOUT" {
		t.Errorf("a photo on Muse Wonder: %+v", jf)
	}
	if _, err := r.h.generatePhoto(ctx, photoOrg, r.st, testAttribution, ""); !errors.As(err, &jf) || !jf.Retryable {
		t.Errorf("a photo on the deployment's model: %+v", jf)
	}
}

// A photo of the chosen vision model: the vision call carries it; a vision
// client that cannot ask it fails the proposal before the segmenter is
// asked.
func TestThePhotoAsksTheChosenModel(t *testing.T) {
	ctx := context.Background()
	r := newPhotoRig(t, "electronics-line-demo")
	r.script(-1)
	if _, err := r.h.generatePhoto(ctx, photoOrg, r.st, testAttribution, "muse-wonder-1.0"); err != nil {
		t.Fatal(err)
	}
	if reqs := r.vision.Requests(); len(reqs) != 1 || reqs[0].Model != "muse-wonder-1.0" || len(reqs[0].Prompt.Images) != 1 {
		t.Fatalf("vision requests %+v", reqs)
	}
	only := newPhotoRig(t, "electronics-line-demo", func(c *Config) { c.Vision = client(t, imageOnly{fake.New()}) })
	only.script(-1)
	_, err := only.h.generatePhoto(ctx, photoOrg, only.st, testAttribution, "muse-wonder-1.0")
	var jf *jobs.Failure
	if !errors.As(err, &jf) || jf.Code != "AI_MODEL_NOT_APPROVED" || jf.Retryable || len(only.seg.Calls()) != 0 {
		t.Errorf("a vision client that cannot ask the model: %v, %d segmenter calls", err, len(only.seg.Calls()))
	}
}

// A job whose model is not of a model ID's shape is not one this server
// queued.
func TestAJobWithAMalformedModel(t *testing.T) {
	for _, m := range []string{"Muse Wonder", "muse-wonder-1.0\n", "../x", ""} {
		var p payload
		raw := `{"proposal_id":"aip_01M4YDQK00ADN261CME3WV5HZ6","request_id":"req_01M4YDQK00ADN261CME3WV5HZ6","model":` + quoted(m) + `}`
		if err := json.Unmarshal([]byte(raw), &p); err != nil {
			t.Fatal(err)
		}
		if ok := p.Model == "" || modelIDs.MatchString(p.Model); ok != (m == "") {
			t.Errorf("%q: taken %v", m, ok)
		}
	}
	if !modelIDs.MatchString("muse-wonder-1.0") || !modelIDs.MatchString("muse-spark-1.3") {
		t.Error("the chosen models are refused")
	}
}

func quoted(s string) string {
	b, _ := json.Marshal(s)
	return string(b)
}

// imageOnly takes images but asks its own model only.
type imageOnly struct{ b *fake.Backend }

func (i imageOnly) Name() string        { return "test" }
func (i imageOnly) AcceptsImages() bool { return true }
func (i imageOnly) Complete(ctx context.Context, req provider.Request) (provider.Response, error) {
	return i.b.Complete(ctx, req)
}
