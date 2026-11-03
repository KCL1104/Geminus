package analyses

// Unit tests of the analysis routes and of listAnalyses' paging (H-073
// slice 1): no database. The closed loop, the roles and the tenant scope
// are internal/api's (analyses_test.go).

import (
	"encoding/base64"
	"encoding/json/v2"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/kcl1104/geminus/internal/httpapi"
	"github.com/kcl1104/geminus/internal/model/validate"
	store "github.com/kcl1104/geminus/internal/storage/analyses"
	"github.com/kcl1104/geminus/internal/tenant"
)

// The analysis routes are the contract's: method, path and roles
// (planning/api-operations.json, from openapi.yaml); any member reads and
// lists, an owner or editor asks.
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
			t.Errorf("route %s %s %v, contract %s %s %v", r.Method, r.Path, roles, o.Method, o.Path, o.Roles)
		}
	}
	if want := []string{"createAnalysis", "listAnalyses", "getAnalysis"}; !slices.Equal(got, want) {
		t.Errorf("routes %v, want %v", got, want)
	}
	if !slices.Equal(readRoles, tenant.Members) || !slices.Equal(writeRoles, tenant.Writers) {
		t.Errorf("read %v, write %v", readRoles, writeRoles)
	}
}

// The list's cursor is bound to the organization, the pair and the list:
// a cursor of another organization, another run, another baseline or the
// pair the other way round is not a position in this list.
func TestCursor(t *testing.T) {
	const (
		org  = "org_01M4YDQK00Y5H2929M9WWZWTVH"
		run  = "sim_01M4YDQK00FGZZWH0PP15XPNFB"
		base = "sim_01M4YDQK00FGZZWH0PP15XPNFA"
	)
	pos := store.Position{CreatedAt: time.Date(2026, 11, 1, 6, 0, 0, 123000, time.UTC), ID: "ana_01M4YDQK00B8S5WQXJ3TQ7ZK61"}
	c := encodeCursor(org, run, base, pos)
	got, err := decodeCursor(c, org, run, base)
	if err != nil || !got.CreatedAt.Equal(pos.CreatedAt) || got.ID != pos.ID {
		t.Errorf("%+v %v", got, err)
	}
	for _, bad := range []struct{ c, org, run, base string }{
		{c, "org_01M4YDQK00Y5H2929M9WWZWTVJ", run, base},
		{c, org, "sim_01M4YDQK00FGZZWH0PP15XPNFC", base},
		{c, org, run, "sim_01M4YDQK00FGZZWH0PP15XPNFC"},
		{c, org, base, run},
		{"not-base64!", org, run, base},
		{"e30", org, run, base}, // {}
	} {
		if _, err := decodeCursor(bad.c, bad.org, bad.run, bad.base); !errors.Is(err, ErrInvalidCursor) {
			t.Errorf("%+v: %v", bad, err)
		}
	}
	// Another list's cursor (an investigation's position) is not this list's either.
	other := `{"v":1,"o":"` + org + `","l":"investigations","r":"` + run + `","b":"` + base + `","t":"2026-11-01T06:00:00.000123Z","i":"inv_01M4YDQK00B8S5WQXJ3TQ7ZK2A"}`
	if _, err := decodeCursor(base64URL(other), org, run, base); !errors.Is(err, ErrInvalidCursor) {
		t.Errorf("another list's cursor: %v", err)
	}
}

// What the client sees of the list's own refusals: a cursor the list did
// not issue is 400 naming the cursor; a run of none of the organization's
// 404 naming the parameter (the path's id, or the query's baseline_run_id).
func TestRefused(t *testing.T) {
	for _, c := range []struct {
		err    error
		code   httpapi.Code
		reason string
	}{
		{ErrInvalidCursor, httpapi.InvalidRequest, "cursor"},
		{&RunError{Param: RunParam, Err: ErrRunNotFound}, httpapi.NotFound, "id"},
		{&RunError{Param: BaselineQuery, Err: ErrRunNotFound}, httpapi.NotFound, "baseline_run_id"},
	} {
		var he *httpapi.Error
		if !errors.As(Refused(c.err), &he) || he.Code != c.code || len(he.Details) != 1 || he.Details[0].Reason != c.reason {
			t.Errorf("%v: %+v", c.err, he)
		}
	}
	var he *httpapi.Error
	if !errors.As(Refused(ErrInvalidLimit), &he) || he.Code != httpapi.InvalidRequest {
		t.Errorf("a limit out of range: %+v", he)
	}
}

func base64URL(s string) string { return base64.RawURLEncoding.EncodeToString([]byte(s)) }

// H-073 review: a listed analysis names the language it was asked in
// (output_locale, the request's), so the page can prefer one in the
// reader's language and mark another's; getAnalysis stays byte for byte as
// before, without it.
func TestOutputLocaleIsListed(t *testing.T) {
	base := "sim_01M4YDQK00FGZZWH0PP15XPNFA"
	a := store.Analysis{ID: "ana_01M4YDQK00B8S5WQXJ3TQ7ZK61", OrganizationID: "org_01M4YDQK00Y5H2929M9WWZWTVH", RunID: "sim_01M4YDQK00FGZZWH0PP15XPNFB",
		BaselineRunID: &base, Status: store.Completed, OutputLocale: "zh-Hant", Analysis: []byte(`{"scenario":{"summary":"x"}}`), ProviderMetadata: []byte(`{}`),
		CreatedAt: time.Date(2026, 11, 1, 6, 0, 46, 0, time.UTC)}
	got, err := public(a, "req_01M4YDQK00V6H8S0ST4F7510KR")
	if err != nil {
		t.Fatal(err)
	}
	shown, err := json.Marshal(got)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(shown), "output_locale") {
		t.Errorf("getAnalysis shows %s", shown)
	}
	item, err := listed(a, "req_01M4YDQK00V6H8S0ST4F7510KR")
	if err != nil {
		t.Fatal(err)
	}
	data, err := json.Marshal(item)
	if err != nil {
		t.Fatal(err)
	}
	if want := strings.TrimSuffix(string(shown), "}") + `,"output_locale":"zh-Hant"}`; string(data) != want {
		t.Errorf("listed %s, want %s", data, want)
	}
	// A row's locale is one the contract has (0018's check); anything else is not listed as if it were.
	a.OutputLocale = "fr"
	if _, err := listed(a, "req_01M4YDQK00V6H8S0ST4F7510KR"); err == nil {
		t.Error("an output locale outside the contract was listed")
	}
}

// H-086 (R): a completed analysis that holds the AI's second answer (the
// job's one re-ask after the server's grounding check refused the first)
// says so in generated_by.revised_after_check, as a revised proposal does;
// nothing of the re-ask's call is shown, and the record is the contract's
// AnalysisRecord. One without a re-ask shows no flag.
func TestARevisedAnalysisSaysSo(t *testing.T) {
	base := "sim_01M4YDQK00FGZZWH0PP15XPNFA"
	analysis := `{"baseline":null,"scenario":{"summary":"The second server shortened the queue."},"delta":[],"bottleneck":[],"assumptions":[],` +
		`"uncertainty":["Only one replication was run."],"suggested_next_experiments":[]}`
	first := `"provider":"meta","model":"muse-spark-1.3","role":"analyst","prompt_template_version":"analyst-0.3.3","input_hash":"sha256:aa"`
	repair := `,"repair":{"outcome":"grounded","issues":2,"call":{"provider":"meta","model":"muse-spark-1.3","role":"analyst",` +
		`"prompt_template_version":"analyst-0.3.3+reground-0.1.0","input_hash":"sha256:bb","cost_nano_usd":99,"spend_reservation_id":"asr_01M4YDQK00TA00000000000003"}}`
	for _, c := range []struct {
		metadata string
		revised  bool
	}{{`{` + first + `}`, false}, {`{` + first + repair + `}`, true}} {
		a := store.Analysis{ID: "ana_01M4YDQK00B8S5WQXJ3TQ7ZK61", OrganizationID: "org_01M4YDQK00Y5H2929M9WWZWTVH", RunID: "sim_01M4YDQK00FGZZWH0PP15XPNFB",
			BaselineRunID: &base, Status: store.Completed, OutputLocale: "en", Analysis: []byte(analysis), ProviderMetadata: []byte(c.metadata),
			CreatedAt: time.Date(2026, 11, 3, 6, 0, 0, 0, time.UTC)}
		got, err := public(a, "req_01M4YDQK00V6H8S0ST4F7510KR")
		if err != nil {
			t.Fatal(err)
		}
		data, err := json.Marshal(got)
		if err != nil {
			t.Fatal(err)
		}
		if issues, err := validate.Document(data, "http.schema.json#/$defs/AnalysisRecord", "RESPONSE"); err != nil || len(issues) > 0 {
			t.Errorf("%s: %v %v", data, issues, err)
		}
		if got.GeneratedBy == nil || got.GeneratedBy.RevisedAfterCheck != c.revised || strings.Contains(string(data), "false") {
			t.Errorf("revised %v: %s", c.revised, data)
		}
		for _, leak := range []string{"reground", "sha256", "asr_", "99", "issues\":2", "repair"} {
			if strings.Contains(string(data), leak) {
				t.Errorf("%s leaks %q", data, leak)
			}
		}
	}
}
