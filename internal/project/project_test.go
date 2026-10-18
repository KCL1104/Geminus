package project

import (
	"encoding/json/v2"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/kcl1104/geminus/internal/storage/projects"
	"github.com/kcl1104/geminus/internal/tenant"
	"github.com/kcl1104/geminus/schemas"
)

// The routes are the contract's: path, method and x-required-roles of each
// operation equal planning/api-operations.json (generated from openapi.yaml
// and checked by make spec-check).
func TestRoutesMatchContract(t *testing.T) {
	data, err := os.ReadFile(filepath.Join("..", "..", "planning", "api-operations.json"))
	if err != nil {
		t.Fatal(err)
	}
	var doc struct {
		Operations []struct {
			Method      string   `json:"method"`
			Path        string   `json:"path"`
			OperationID string   `json:"operation_id"`
			Roles       []string `json:"roles"`
		} `json:"operations"`
	}
	if err := json.Unmarshal(data, &doc); err != nil {
		t.Fatal(err)
	}
	for _, r := range Routes {
		i := slices.IndexFunc(doc.Operations, func(o struct {
			Method      string   `json:"method"`
			Path        string   `json:"path"`
			OperationID string   `json:"operation_id"`
			Roles       []string `json:"roles"`
		}) bool {
			return o.OperationID == r.OperationID
		})
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
	if !slices.Equal(tenant.Members, []tenant.Role{tenant.Owner, tenant.Editor, tenant.Viewer}) || !slices.Equal(tenant.Writers, []tenant.Role{tenant.Owner, tenant.Editor}) {
		t.Errorf("role sets changed: %v %v", tenant.Members, tenant.Writers)
	}
}

// ValidID is the schema's ProjectID (D-001), not a second copy.
func TestProjectIDMatchesSchema(t *testing.T) {
	data, err := schemas.FS.ReadFile("common.schema.json")
	if err != nil {
		t.Fatal(err)
	}
	var doc struct {
		Defs map[string]struct {
			Pattern string `json:"pattern"`
		} `json:"$defs"`
	}
	if err := json.Unmarshal(data, &doc); err != nil {
		t.Fatal(err)
	}
	if got, want := projectID.String(), doc.Defs["ProjectID"].Pattern; got != want || want == "" {
		t.Errorf("project ID pattern %q, schema ProjectID %q", got, want)
	}
}

// A cursor round-trips, and only for the organization and list it names.
func TestCursor(t *testing.T) {
	const org = "org_01M4YDQK00TA00000000000000"
	pos := projects.Position{CreatedAt: time.Date(2026, 10, 18, 9, 30, 0, 123456000, time.FixedZone("x", 3600)), ID: "prj_01M4YDQK00TA00000000000002"}
	c := encodeCursor(org, pos)
	got, err := decodeCursor(c, org)
	if err != nil || !got.CreatedAt.Equal(pos.CreatedAt) || got.ID != pos.ID {
		t.Fatalf("round trip: %+v %v", got, err)
	}
	if _, err := decodeCursor(c, "org_01M4YDQK00TA00000000000009"); err != ErrInvalidCursor {
		t.Errorf("another organization: %v", err)
	}
	if strings.ContainsAny(c, "+/=") {
		t.Errorf("cursor %q is not URL-safe", c)
	}
	for _, bad := range []string{"", "x", c[:len(c)-2], strings.ToUpper(c)} {
		if _, err := decodeCursor(bad, org); err != ErrInvalidCursor {
			t.Errorf("%q: %v", bad, err)
		}
	}
	if !ValidID(pos.ID) || ValidID("prj_"+strings.Repeat("Z", 26)) || ValidID("mdl_01M4YDQK00TA00000000000002") {
		t.Error("ValidID")
	}
}
