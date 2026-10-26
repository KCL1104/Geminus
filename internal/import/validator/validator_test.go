package validator

// The handler's checks that need no database (T-017). Its work on a
// claimed job, under the lease, is covered through the whole stack by
// internal/api's TestImportMappingClosedLoop, TestStaleValidationJobs,
// TestValidationJobFailures and TestJSONEventsMapping (a disposable
// database: make db-go-test).

import (
	"context"
	"encoding/json/v2"
	"errors"
	"io"
	"log/slog"
	"strings"
	"testing"

	"github.com/kcl1104/geminus/internal/domainpack"
	"github.com/kcl1104/geminus/internal/import/mapping"
	"github.com/kcl1104/geminus/internal/import/validation"
	"github.com/kcl1104/geminus/internal/model"
	"github.com/kcl1104/geminus/internal/model/validate"
	"github.com/kcl1104/geminus/internal/packs"
	store "github.com/kcl1104/geminus/internal/storage/imports"
	"github.com/kcl1104/geminus/internal/storage/jobs"
	"github.com/kcl1104/geminus/internal/tenant"
)

var quiet = slog.New(slog.NewTextHandler(io.Discard, nil))

func TestNew(t *testing.T) {
	for name, cfg := range map[string]Config{
		"no log":               {Actor: "worker-1"},
		"no actor":             {Log: quiet},
		"an actor with spaces": {Actor: "worker 1", Log: quiet},
		"a digit first":        {Actor: "1worker", Log: quiet},
	} {
		if _, err := New(cfg); err == nil {
			t.Errorf("%s: accepted", name)
		}
	}
	h, err := New(Config{Actor: "worker-1", Log: quiet})
	if err != nil || h.cfg.Limits != validation.DefaultLimits {
		t.Fatalf("%v %+v", err, h)
	}
	lim := validation.DefaultLimits
	lim.MaxIssues = 3
	if h, err := New(Config{Actor: "worker-1", Log: quiet, Limits: lim}); err != nil || h.cfg.Limits.MaxIssues != 3 {
		t.Errorf("the limits given are not kept: %v", err)
	}
}

// refusing is a run whose transactions must never run.
type refusing struct {
	t   *testing.T
	job jobs.Job
}

func (r refusing) Job() jobs.Job { return r.job }
func (r refusing) Checkpoint(context.Context, func(context.Context, tenant.Tx) error) error {
	r.t.Error("a checkpoint for a job that names no import")
	return nil
}
func (r refusing) Finish(context.Context, jobs.Status, func(context.Context, tenant.Tx) error) error {
	r.t.Error("a finish for a job that names no import")
	return nil
}

// A job that does not name its import, a mapping revision and its request
// fails at once, not retried, before any transaction.
func TestAJobThatNamesNoImport(t *testing.T) {
	h, err := New(Config{Actor: "worker-1", Log: quiet})
	if err != nil {
		t.Fatal(err)
	}
	const id, req = "imp_01M4YDQK00TA00000000000001", "req_01M4YDQK00TA00000000000001"
	for name, c := range map[string]struct {
		payload, resource string
	}{
		"not JSON":              {`{`, id},
		"another import":        {`{"import_id":"` + id + `","mapping_revision":1,"request_id":"` + req + `"}`, "imp_01M4YDQK00TA00000000000002"},
		"a malformed import":    {`{"import_id":"imp_x","mapping_revision":1,"request_id":"` + req + `"}`, "imp_x"},
		"no revision":           {`{"import_id":"` + id + `","request_id":"` + req + `"}`, id},
		"revision 0":            {`{"import_id":"` + id + `","mapping_revision":0,"request_id":"` + req + `"}`, id},
		"a revision as text":    {`{"import_id":"` + id + `","mapping_revision":"1","request_id":"` + req + `"}`, id},
		"no request":            {`{"import_id":"` + id + `","mapping_revision":1}`, id},
		"a malformed request":   {`{"import_id":"` + id + `","mapping_revision":1,"request_id":"x"}`, id},
		"a fractional revision": {`{"import_id":"` + id + `","mapping_revision":1.5,"request_id":"` + req + `"}`, id},
	} {
		job := jobs.Job{ID: "job_01M4YDQK00TA00000000000001", OrganizationID: "org_01M4YDQK00TA00000000000001", Type: jobs.ImportValidate,
			Resource: jobs.Resource{ImportID: c.resource}, Payload: []byte(c.payload), Attempt: 1}
		err := h.Handle(context.Background(), refusing{t, job})
		var f *jobs.Failure
		if !errors.As(err, &f) || f.Code != store.InputInvalid || f.Retryable {
			t.Errorf("%s: %v", name, err)
		}
	}
}

// What the handler records as the import's validation is
// common.schema.json#/$defs/Validation, issues and all.
func TestValidationIsTheSchemas(t *testing.T) {
	for _, v := range []Validation{
		{Valid: true, Issues: []model.ValidationIssue{}},
		{Valid: false, Issues: []model.ValidationIssue{
			model.NewIssue(mapping.CodeValueInvalid, "/1/開始", "timestamp: the value is not an RFC 3339 date-time with an offset", model.SeverityError),
			model.NewIssue(validation.CodeDatasetTooLarge, "", "the canonical dataset would be larger than 268435456 bytes; map fewer columns to metadata", model.SeverityError),
		}},
	} {
		data, err := json.Marshal(v)
		if err != nil {
			t.Fatal(err)
		}
		if issues, err := validate.Document(data, "common.schema.json#/$defs/Validation", "VALIDATION"); err != nil || len(issues) > 0 {
			t.Errorf("%s: %v %v", data, issues, err)
		}
	}
}

// eventCheck finds the data-quality rules (D-059, T-052) of the pack of the
// model an import is bound to, from that version's IR: none without packs
// (generic validation only) or for a pack without rules; the electronics
// line pack's for its template; a domain the worker does not serve, or a
// version that does not decode, fails the job for good (fail closed).
func TestEventCheck(t *testing.T) {
	served, err := packs.Registry()
	if err != nil {
		t.Fatal(err)
	}
	irOf := func(domain, tmpl string) []byte {
		t.Helper()
		p, err := served.Lookup(domain)
		if err != nil {
			t.Fatal(err)
		}
		ir, err := p.(domainpack.TemplateProvider).Template(tmpl, "mdl_01M4YDQK00TA00000000000001", "Line", domainpack.English)
		if err != nil {
			t.Fatal(err)
		}
		data, err := model.Encode(ir)
		if err != nil {
			t.Fatal(err)
		}
		return data
	}
	line := irOf("electronics_line", "electronics_assembly_test_line")
	if c, err := eventCheck(nil, line); c != nil || err != nil {
		t.Errorf("no packs: %v", err)
	}
	c, err := eventCheck(served, line)
	if err != nil || c == nil {
		t.Fatalf("the line: %v", err)
	}
	if got := c(domainpack.CanonicalEvent{CaseID: "SN-1", EventType: "operation", Timestamp: "2026-10-05T06:00:00+08:00",
		EndTimestamp: "2026-10-05T06:01:00+08:00", Stage: "xray", Product: "board_a"}); len(got) != 1 || got[0].Code != "EVENT_STAGE_UNKNOWN" {
		t.Errorf("the line's rules: %+v", got)
	}
	if c, err := eventCheck(served, irOf("software_delivery", "default_delivery_pipeline")); c != nil || err != nil {
		t.Errorf("a pack without rules: %v", err)
	}
	other := strings.Replace(string(line), `"domain":"electronics_line"`, `"domain":"hospital"`, 1)
	for name, text := range map[string][]byte{"an unserved domain": []byte(other), "not an IR": []byte(`{"nodes":1}`)} {
		var f *jobs.Failure
		if _, err := eventCheck(served, text); !errors.As(err, &f) || f.Code != store.InputInvalid || f.Retryable {
			t.Errorf("%s: %v", name, err)
		}
	}
}
