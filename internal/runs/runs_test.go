package runs

import (
	"encoding/base64"
	"encoding/json/v2"
	"errors"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"testing"
	"time"

	"github.com/kcl1104/geminus/internal/httpapi"
	"github.com/kcl1104/geminus/internal/ids"
	"github.com/kcl1104/geminus/internal/model"
	"github.com/kcl1104/geminus/internal/model/validate"
	"github.com/kcl1104/geminus/internal/simulation"
	"github.com/kcl1104/geminus/internal/simulation/des"
	"github.com/kcl1104/geminus/internal/storage/idempotency"
	"github.com/kcl1104/geminus/internal/storage/jobs"
	store "github.com/kcl1104/geminus/internal/storage/runs"
	"github.com/kcl1104/geminus/internal/tenant"
)

// The simulation routes are the contract's: method, path and roles
// (planning/api-operations.json, from openapi.yaml); compareSimulations is
// T-029's.
func TestRoutesMatchContract(t *testing.T) {
	data, err := os.ReadFile(filepath.Join("..", "..", "planning", "api-operations.json"))
	if err != nil {
		t.Fatal(err)
	}
	type operation struct {
		Method      string   `json:"method"`
		Path        string   `json:"path"`
		OperationID string   `json:"operation_id"`
		Roles       []string `json:"roles"`
	}
	var doc struct {
		Operations []operation `json:"operations"`
	}
	if err := json.Unmarshal(data, &doc); err != nil {
		t.Fatal(err)
	}
	want := []string{"createSimulation", "listSimulations", "getSimulation", "cancelSimulation", "getSimulationResults", "streamSimulationEvents", "downloadSimulationTrace"}
	var got []string
	for _, r := range Routes {
		got = append(got, r.OperationID)
		i := slices.IndexFunc(doc.Operations, func(o operation) bool { return o.OperationID == r.OperationID })
		if i < 0 {
			t.Errorf("%s is not a contract operation", r.OperationID)
			continue
		}
		o := doc.Operations[i]
		var roles []string
		for _, x := range r.Roles {
			roles = append(roles, string(x))
		}
		if o.Method != r.Method || o.Path != r.Path || !slices.Equal(slices.Sorted(slices.Values(o.Roles)), slices.Sorted(slices.Values(roles))) {
			t.Errorf("%s: route %s %s %v, contract %s %s %v", r.OperationID, r.Method, r.Path, roles, o.Method, o.Path, o.Roles)
		}
	}
	if !slices.Equal(got, want) {
		t.Errorf("routes %v, want %v", got, want)
	}
}

// Each service error has its contract code; the 422s carry their issues as
// details. Others (tenant, transport, a corrupt trace) are httpapi's.
func TestRefused(t *testing.T) {
	issue := model.NewIssue("UNKNOWN_REFERENCE", "/flows/0/to", "no such node", model.SeverityError)
	for err, want := range map[error]httpapi.Code{
		ErrNotFound:               httpapi.NotFound,
		ErrNotReady:               httpapi.ResultNotReady,
		ErrFinished:               httpapi.LifecycleConflict,
		ErrNoTrace:                httpapi.NotFound,
		ErrStorageUnavailable:     httpapi.ServiceUnavailable,
		ErrInvalidCursor:          httpapi.InvalidRequest,
		ErrInvalidLimit:           httpapi.InvalidRequest,
		idempotency.ErrConflict:   httpapi.IdempotencyKeyConflict,
		idempotency.ErrInProgress: httpapi.IdempotencyKeyConflict,
		fmt.Errorf("wrapped: %w", &InvalidError{Issues: []model.ValidationIssue{issue}}): httpapi.ModelValidationFailed,
		&RequestError{Issues: []model.ValidationIssue{issue}}:                            httpapi.RequestValidationFailed,
	} {
		var e *httpapi.Error
		if !errors.As(Refused(err), &e) || e.Code != want {
			t.Errorf("%v: %v, want %s", err, Refused(err), want)
			continue
		}
		if (want == httpapi.ModelValidationFailed || want == httpapi.RequestValidationFailed) &&
			!slices.Equal(e.Details, []httpapi.Detail{{Path: "/flows/0/to", Reason: "no such node", Code: "UNKNOWN_REFERENCE", Severity: "error"}}) {
			t.Errorf("%s details %+v", want, e.Details)
		}
	}
	for _, err := range []error{tenant.ErrForbidden, tenant.ErrNoMembership, ErrTraceCorrupt, errors.New("database down")} {
		if got := Refused(err); got != err {
			t.Errorf("%v became %v", err, got)
		}
	}
}

// A failed run's error is the contract's envelope (CB-38): the closed code
// by kind, the failure's code in details[0], a limit at its place in the
// Run resource, under the creating request. A stored failure that is not
// one is an internal error, never shown.
func TestRunError(t *testing.T) {
	req := ids.New("req")
	for failure, want := range map[string][2]string{
		store.EventBudgetExceeded:    {"MODEL_VALIDATION_FAILED", "/execution_limits/max_events"},
		store.WallTimeoutExceeded:    {"MODEL_VALIDATION_FAILED", "/execution_limits/wall_timeout_seconds"},
		store.ActiveEntitiesExceeded: {"MODEL_VALIDATION_FAILED", "/execution_limits/max_active_entities"},
		store.EntityVisitsExceeded:   {"MODEL_VALIDATION_FAILED", "/execution_limits/max_entity_visits"},
		store.RunInputInvalid:        {"MODEL_VALIDATION_FAILED", ""},
		store.SimulationDeadlock:     {"MODEL_VALIDATION_FAILED", ""},
		store.SimulationStalled:      {"MODEL_VALIDATION_FAILED", ""},
		jobs.LeaseExpired:            {"SERVICE_UNAVAILABLE", ""},
		"WORKER_STOPPED":             {"SERVICE_UNAVAILABLE", ""},
		store.ArtifactStorageFailed:  {"SERVICE_UNAVAILABLE", ""},
		"INTERNAL":                   {"INTERNAL_ERROR", ""},
		"NO_RESULT":                  {"INTERNAL_ERROR", ""},
		store.EngineInternal:         {"INTERNAL_ERROR", ""},
		store.EngineVersionMismatch:  {"INTERNAL_ERROR", ""},
	} {
		raw, _ := json.Marshal(jobs.Failure{Code: failure, Message: "it failed", Retryable: false})
		e, err := runError(raw, req)
		if err != nil {
			t.Fatalf("%s: %v", failure, err)
		}
		if e.Error.Code != want[0] || e.Error.RequestID != req || e.Error.Message != "it failed" || len(e.Error.Details) != 1 ||
			e.Error.Details[0].Code != failure || e.Error.Details[0].Path != want[1] {
			t.Errorf("%s: %+v", failure, e)
		}
		data, _ := json.Marshal(e)
		if issues, err := validate.Document(data, "common.schema.json#/$defs/Error", "ERROR"); err != nil || len(issues) > 0 {
			t.Errorf("%s: %s is not a contract error: %v %v", failure, data, issues, err)
		}
	}
	if e, err := runError([]byte(`{"code":"INTERNAL","message":"","retryable":false}`), req); err != nil || e.Error.Message != "the run failed" {
		t.Errorf("an empty message: %+v %v", e, err)
	}
	for _, bad := range []string{`null`, `{}`, `{"code":"lower"}`, `[]`, `x`} {
		if _, err := runError([]byte(bad), req); err == nil {
			t.Errorf("%s was shown", bad)
		}
	}
}

// D-050 B1 (T-047): a deadlocked run's error is the contract example
// testdata/contracts/valid-run-failed-deadlock.json, whose message is the
// runner's for the hand-derived G-D2 deadlock (blocking-goldens.json; the
// engine and the runner tests check that they produce exactly it):
// MODEL_VALIDATION_FAILED like the execution limits (D-063 (b), CB-38(a)),
// the failure's own code and severity in details[0].
func TestDeadlockError(t *testing.T) {
	root := filepath.Join("..", "..")
	var golden struct {
		Cases []struct {
			ID       string `json:"id"`
			Expected struct {
				Deadlock *struct {
					Reason string `json:"reason"`
				} `json:"deadlock"`
			} `json:"expected"`
		} `json:"cases"`
	}
	data, err := os.ReadFile(filepath.Join(root, "testdata/simulation/blocking-goldens.json"))
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(data, &golden); err != nil {
		t.Fatal(err)
	}
	reason := ""
	for _, c := range golden.Cases {
		if c.Expected.Deadlock != nil && c.ID == "G-D2" {
			reason = c.Expected.Deadlock.Reason
		}
	}
	var fixture struct {
		EngineVersion string        `json:"engine_version"`
		Error         ErrorEnvelope `json:"error"`
	}
	data, err = os.ReadFile(filepath.Join(root, "testdata/contracts/valid-run-failed-deadlock.json"))
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(data, &fixture); err != nil {
		t.Fatal(err)
	}
	raw, _ := json.Marshal(jobs.Failure{Code: store.SimulationDeadlock, Message: reason, Retryable: false})
	e, err := runError(raw, fixture.Error.Error.RequestID)
	if err != nil {
		t.Fatal(err)
	}
	if reason == "" || !reflect.DeepEqual(*e, fixture.Error) {
		t.Errorf("runError %+v\n fixture %+v", *e, fixture.Error)
	}
	if fixture.EngineVersion != des.Version {
		t.Errorf("the fixture's engine_version %s is not this engine's %s", fixture.EngineVersion, des.Version)
	}
}

// D-050 B2 (T-048): a stalled run's error is the contract example
// testdata/contracts/valid-run-failed-stalled.json, whose message is the
// runner's for the hand-derived G-S1 stall (breakdown-goldens.json):
// MODEL_VALIDATION_FAILED like a deadlock, the failure's own code and
// severity in details[0].
func TestStalledError(t *testing.T) {
	root := filepath.Join("..", "..")
	var golden struct {
		Cases []struct {
			ID       string `json:"id"`
			Expected struct {
				Stalled *struct {
					Reason string `json:"reason"`
				} `json:"stalled"`
			} `json:"expected"`
		} `json:"cases"`
	}
	data, err := os.ReadFile(filepath.Join(root, "testdata/simulation/breakdown-goldens.json"))
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(data, &golden); err != nil {
		t.Fatal(err)
	}
	reason := ""
	for _, c := range golden.Cases {
		if c.Expected.Stalled != nil && c.ID == "G-S1" {
			reason = c.Expected.Stalled.Reason
		}
	}
	var fixture struct {
		EngineVersion string        `json:"engine_version"`
		Error         ErrorEnvelope `json:"error"`
	}
	data, err = os.ReadFile(filepath.Join(root, "testdata/contracts/valid-run-failed-stalled.json"))
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(data, &fixture); err != nil {
		t.Fatal(err)
	}
	raw, _ := json.Marshal(jobs.Failure{Code: store.SimulationStalled, Message: reason, Retryable: false})
	e, err := runError(raw, fixture.Error.Error.RequestID)
	if err != nil {
		t.Fatal(err)
	}
	if reason == "" || !reflect.DeepEqual(*e, fixture.Error) {
		t.Errorf("runError %+v\n fixture %+v", *e, fixture.Error)
	}
	if fixture.EngineVersion != des.Version {
		t.Errorf("the fixture's engine_version %s is not this engine's %s", fixture.EngineVersion, des.Version)
	}
}

// Last-Event-ID names this run's stream: "<run ID>:<revision>".
func TestLastEventID(t *testing.T) {
	id, other := ids.New("sim"), ids.New("sim")
	for _, c := range []struct {
		values []string
		want   int64
		ok     bool
	}{
		{nil, -1, true}, {[]string{id + ":0"}, 0, true}, {[]string{id + ":17"}, 17, true},
		{[]string{other + ":1"}, 0, false}, {[]string{id}, 0, false}, {[]string{id + ":"}, 0, false}, {[]string{id + ":-1"}, 0, false},
		{[]string{id + ":01"}, 0, false}, {[]string{id + ":1.5"}, 0, false}, {[]string{id + ":1", id + ":2"}, 0, false},
		{[]string{id + ":99999999999999999999"}, 0, false}, {[]string{":" + id + ":1"}, 0, false},
	} {
		r, _ := http.NewRequest("GET", "/", nil)
		for _, v := range c.values {
			r.Header.Add("Last-Event-ID", v)
		}
		got, err := lastEventID(r, id)
		var e *httpapi.Error
		if c.ok && (err != nil || got != c.want) || !c.ok && (!errors.As(err, &e) || e.Code != httpapi.InvalidRequest) {
			t.Errorf("%q: %d %v", c.values, got, err)
		}
	}
}

// The configuration the engine cannot run is a request issue at its field.
func TestConfigIssues(t *testing.T) {
	d := func(v float64, u model.TimeUnit) simulation.Duration { return simulation.Duration{Value: v, Unit: u} }
	for _, c := range []struct {
		horizon, warmup simulation.Duration
		paths           []string
	}{
		{d(10, model.Minute), d(0, model.Minute), nil},
		{d(365, model.Day), d(364, model.Day), nil},
		{d(10, model.Minute), d(10, model.Minute), []string{"/config/warmup"}},
		{d(10, model.Minute), d(11, model.Minute), []string{"/config/warmup"}},
		{d(366, model.Day), d(0, model.Minute), []string{"/config/horizon"}},
		{d(1, model.Day), d(400, model.Day), []string{"/config/warmup"}},
		{d(400, model.Day), d(400, model.Day), []string{"/config/horizon", "/config/warmup"}},
		{d(0, model.Minute), d(0, model.Minute), []string{"/config/warmup"}},
	} {
		var paths []string
		for _, is := range configIssues(simulation.RunConfig{Horizon: c.horizon, Warmup: c.warmup, Replications: 1, TraceLevel: simulation.TraceNone}) {
			if is.Severity != model.SeverityError || is.Code != validate.LimitExceeded {
				t.Errorf("%+v", is)
			}
			paths = append(paths, is.Path)
		}
		if !slices.Equal(paths, c.paths) {
			t.Errorf("horizon %v, warmup %v: %v, want %v", c.horizon, c.warmup, paths, c.paths)
		}
	}
}

// A cursor is the list's own: its organization, model and list.
func TestCursor(t *testing.T) {
	org, mdl := ids.New("org"), ids.New("mdl")
	pos := store.Position{CreatedAt: time.Date(2026, 10, 19, 1, 2, 3, 456789000, time.UTC), ID: ids.New("sim")}
	c := encodeCursor(org, mdl, pos)
	if got, err := decodeCursor(c, org, mdl); err != nil || !got.CreatedAt.Equal(pos.CreatedAt) || got.ID != pos.ID {
		t.Errorf("round trip: %+v %v", got, err)
	}
	for name, s := range map[string]struct{ cursor, org, model string }{
		"another organization": {c, ids.New("org"), mdl},
		"another model":        {c, org, ids.New("mdl")},
		"not base64":           {"!!", org, mdl},
		"another list":         {encodeOther(org, mdl), org, mdl},
	} {
		if _, err := decodeCursor(s.cursor, s.org, s.model); !errors.Is(err, ErrInvalidCursor) {
			t.Errorf("%s: %v", name, err)
		}
	}
}

func encodeOther(org, mdl string) string {
	data, _ := json.Marshal(cursor{Version: 1, Org: org, List: "projects", Model: mdl, After: "2026-10-19T00:00:00Z", ID: ids.New("sim")})
	return base64.RawURLEncoding.EncodeToString(data)
}

// T-023 review I1: after a write the deadline outlasts the longest silence
// before the next one (a keep-alive at the first poll after KeepAlive)
// plus that write's own WriteTimeout, for the defaults and for any setting.
func TestStreamIdleDeadline(t *testing.T) {
	d := Stream{}.withDefaults()
	if d.Poll != time.Second || d.KeepAlive != 15*time.Second || d.WriteTimeout != 10*time.Second || d.MaxAge != 10*time.Minute {
		t.Errorf("defaults %+v", d)
	}
	for _, c := range []Stream{d, {Poll: 20 * time.Millisecond, KeepAlive: 600 * time.Millisecond, WriteTimeout: 250 * time.Millisecond}} {
		if c.idle() < c.KeepAlive+c.Poll+c.WriteTimeout {
			t.Errorf("%+v: a write's deadline lasts %v after it", c, c.idle())
		}
	}
}
