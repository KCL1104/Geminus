package models_test

import (
	"encoding/json/v2"
	"errors"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"slices"
	"testing"

	"github.com/kcl1104/geminus/internal/httpapi"
	"github.com/kcl1104/geminus/internal/model"
	"github.com/kcl1104/geminus/internal/models"
	"github.com/kcl1104/geminus/internal/storage/idempotency"
	"github.com/kcl1104/geminus/internal/tenant"
)

// The model routes are the contract's: method, path and roles
// (planning/api-operations.json, from openapi.yaml).
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
	want := []string{"createModel", "listModels", "getModel", "getDraft", "patchDraft", "resetDraft", "publishVersion", "listVersions", "getModelVersion"}
	var got []string
	for _, r := range models.Routes {
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

// If-Match (openapi IfMatch): one strong ETag "draft-revision-N". Missing
// is 428; anything else malformed is 400; a well-formed revision no draft
// can have is a conflict.
func TestIfMatch(t *testing.T) {
	for _, c := range []struct {
		values []string
		rev    int64
		code   httpapi.Code
		err    error
	}{
		{[]string{`"draft-revision-1"`}, 1, "", nil},
		{[]string{`"draft-revision-9223372036854775807"`}, 9223372036854775807, "", nil},
		{nil, 0, httpapi.PreconditionRequired, nil},
		{[]string{`draft-revision-1`}, 0, httpapi.InvalidRequest, nil},
		{[]string{`W/"draft-revision-1"`}, 0, httpapi.InvalidRequest, nil},
		{[]string{`"draft-revision-0"`}, 0, httpapi.InvalidRequest, nil},
		{[]string{`"draft-revision-01"`}, 0, httpapi.InvalidRequest, nil},
		{[]string{`*`}, 0, httpapi.InvalidRequest, nil},
		{[]string{`"draft-revision-1", "draft-revision-2"`}, 0, httpapi.InvalidRequest, nil},
		{[]string{`"draft-revision-1"`, `"draft-revision-1"`}, 0, httpapi.InvalidRequest, nil},
		{[]string{`""`}, 0, httpapi.InvalidRequest, nil},
		{[]string{fmt.Sprintf(`"draft-revision-1%0100d"`, 0)}, 0, httpapi.InvalidRequest, nil},
		{[]string{`"draft-revision-9223372036854775808"`}, 0, "", models.ErrConflict},
	} {
		r, _ := http.NewRequest("PATCH", "/", nil)
		for _, v := range c.values {
			r.Header.Add("If-Match", v)
		}
		rev, err := models.IfMatch(r)
		var e *httpapi.Error
		switch {
		case c.err != nil:
			if !errors.Is(err, c.err) {
				t.Errorf("%q: %d %v", c.values, rev, err)
			}
		case c.code != "":
			if !errors.As(err, &e) || e.Code != c.code {
				t.Errorf("%q: %d %v", c.values, rev, err)
			}
		case err != nil || rev != c.rev:
			t.Errorf("%q: %d %v", c.values, rev, err)
		}
	}
}

// Each service error has its contract code; the 422s carry their issues
// as details. Others (tenant, transport) are httpapi's to classify.
func TestRefused(t *testing.T) {
	issue := model.NewIssue("UNKNOWN_REFERENCE", "/flows/0/to", "no such node", model.SeverityError)
	for err, want := range map[error]httpapi.Code{
		models.ErrNotFound:            httpapi.NotFound,
		models.ErrConflict:            httpapi.ModelVersionConflict,
		models.ErrNoChanges:           httpapi.LifecycleConflict,
		models.ErrInvalidCursor:       httpapi.InvalidRequest,
		models.ErrDatasetsUnavailable: httpapi.ServiceUnavailable,
		models.ErrInvalidComment:      httpapi.RequestValidationFailed,
		idempotency.ErrConflict:       httpapi.IdempotencyKeyConflict,
		idempotency.ErrInProgress:     httpapi.IdempotencyKeyConflict,
		fmt.Errorf("wrapped: %w", &models.InvalidError{Issues: []model.ValidationIssue{issue}}): httpapi.ModelValidationFailed,
	} {
		var e *httpapi.Error
		if !errors.As(models.Refused(err), &e) || e.Code != want {
			t.Errorf("%v: %v, want %s", err, models.Refused(err), want)
		}
		if want == httpapi.ModelValidationFailed && !slices.Equal(e.Details, []httpapi.Detail{{Path: "/flows/0/to", Reason: "no such node", Code: "UNKNOWN_REFERENCE", Severity: "error"}}) {
			t.Errorf("details %+v", e.Details)
		}
		if want == httpapi.RequestValidationFailed && (len(e.Details) != 1 || e.Details[0].Path != "/comment") {
			t.Errorf("comment details %+v", e.Details)
		}
		if _, ok := httpapi.Status[e.Code]; !ok {
			t.Errorf("%s has no status", e.Code)
		}
	}
	for _, err := range []error{tenant.ErrForbidden, tenant.ErrNoMembership, errors.New("database down")} {
		if got := models.Refused(err); got != err {
			t.Errorf("%v became %v", err, got)
		}
	}
}
