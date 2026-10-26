package audit_test

import (
	"errors"
	"go/ast"
	"go/parser"
	"go/token"
	"io/fs"
	"maps"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"testing"

	"github.com/kcl1104/geminus/internal/audit"
	"github.com/kcl1104/geminus/internal/ids"
)

// mode is the type a call site may use for an enumeration (a named string).
type mode string

func hash(c string) string { return "sha256:" + strings.Repeat(c, 64) }

var (
	user   = ids.New("usr")
	req    = ids.New("req")
	org    = ids.New("org")
	mdl    = ids.New("mdl")
	mv     = ids.New("mv")
	mv2    = ids.New("mv")
	scn    = ids.New("scn")
	sim    = ids.New("sim")
	imp    = ids.New("imp")
	aip    = ids.New("aip")
	job    = ids.New("job")
	prj    = ids.New("prj")
	worker = "worker-0.pod_a:1"
)

// provider is proposal.generate's provider metadata, as the generator's
// auditMeta builds it (internal/proposals/generator).
func provider() map[string]any {
	return map[string]any{"provider": "anthropic", "role": "modeler", "prompt_template_version": "modeler-1",
		"schema": "proposal-output.schema.json", "output_locale": "zh-Hant", "input_hash": hash("a"), "input_tokens": int64(1200),
		"output_tokens": int64(300), "latency_ms": int64(4100), "model": "claude-opus-5-5", "credential_label": "org-a",
		"output_hash": hash("b"), "failed_over_from": []string{"org-b"}, "provider_request_id": "req_011CUcvD2y5nA6z5tYt7aCb1"}
}

func with(m map[string]any, kv ...any) map[string]any {
	out := maps.Clone(m)
	for i := 0; i < len(kv); i += 2 {
		out[kv[i].(string)] = kv[i+1]
	}
	return out
}

// valid is one event of every action in the catalogue, with the metadata
// its call site writes (the keys are the call sites'; the database tests
// write them for real).
func valid() []audit.Event {
	u := func(action, tt, tid string, meta map[string]any) audit.Event {
		return audit.Event{ActorType: audit.User, ActorID: user, Action: action, TargetType: tt, TargetID: tid, RequestID: req, Metadata: meta}
	}
	w := func(action, tt, tid string, meta map[string]any) audit.Event {
		return audit.Event{ActorType: audit.Worker, ActorID: worker, Action: action, TargetType: tt, TargetID: tid, RequestID: req, Metadata: meta}
	}
	return []audit.Event{
		u("project.create", "project", prj, nil),
		u("membership.create", "membership", ids.New("usr"), map[string]any{"role": "editor"}),
		u("membership.update", "membership", ids.New("usr"), map[string]any{"role": "viewer", "previous_role": "owner"}),
		u("membership.delete", "membership", ids.New("usr"), map[string]any{"role": "viewer"}),
		u("model.create", "model", mdl, map[string]any{"template": "default_delivery_pipeline", "locale": "zh-Hant", "version_id": mv}),
		u("model.draft.update", "model", mdl, map[string]any{"revision": int64(4)}),
		u("model.draft.reset", "model", mdl, map[string]any{"revision": int64(5), "version_id": mv}),
		u("model.publish", "model_version", mv, map[string]any{"version": 2, "model_id": mdl}),
		u("import.upload", "import", imp, map[string]any{"kind": "csv_events", "model_id": mdl, "base_version_id": mv,
			"size_bytes": int64(1 << 20), "upload_content_hash": hash("c")}),
		u("import.map", "import", imp, map[string]any{"mapping_revision": int64(2), "columns_imported": 4, "columns_left_out": 1,
			"metadata_columns": 1, "confirmed_low_confidence": 0, "validations_cancelled": 1}),
		w("import.profile", "import", imp, map[string]any{"attempt": 1, "status": "AWAITING_MAPPING", "rows": int64(100),
			"invalid_rows": int64(2), "columns": 5, "error_report": true}),
		w("import.profile", "import", imp, map[string]any{"attempt": 1, "status": "FAILED", "code": "UPLOAD_CORRUPT"}),
		w("import.validate", "import", imp, map[string]any{"attempt": 2, "mapping_revision": int64(2), "outcome": "VALIDATED",
			"rows": int64(100), "dataset_size_bytes": int64(5000)}),
		w("import.validate", "import", imp, map[string]any{"attempt": 1, "mapping_revision": int64(1), "outcome": "stale"}),
		w("import.validate", "import", imp, map[string]any{"attempt": 1, "mapping_revision": int64(1), "outcome": "AWAITING_MAPPING",
			"rows": int64(9), "invalid_rows": int64(3), "issues": 3}),
		w("import.validate", "import", imp, map[string]any{"attempt": 1, "mapping_revision": int64(1), "outcome": "FAILED", "code": "UPLOAD_MISSING"}),
		u("proposal.create", "ai_proposal", aip, map[string]any{"model_id": mdl, "base_version_id": mv, "mode": mode("edit"),
			"output_locale": "en", "selected_entities": 2}),
		w("proposal.generate", "ai_proposal", aip, with(provider(), "attempt", 1, "status", "ready", "issues", 0, "patch_hash", hash("d"),
			"server_risk_level", "medium", "provider_risk_claim", "low", "risk_level", "medium")),
		w("proposal.generate", "ai_proposal", aip, with(provider(), "attempt", 1, "status", "invalid", "issues", 3)),
		// A provider error before the call: the client's metadata is empty.
		w("proposal.attempt_failed", "ai_proposal", aip, map[string]any{"provider": "", "role": "", "prompt_template_version": "",
			"schema": "", "output_locale": "", "input_hash": "", "input_tokens": int64(0), "output_tokens": int64(0), "latency_ms": int64(0),
			"attempt": 2, "code": "AI_RATE_LIMITED", "retryable": true}),
		u("proposal.conflict", "ai_proposal", aip, map[string]any{"mode": "scenario", "model_id": mdl, "base_version_id": mv, "latest_version_id": mv2}),
		u("proposal.approve", "ai_proposal", aip, map[string]any{"mode": "edit", "model_id": mdl, "base_version_id": mv, "patch_hash": hash("e"),
			"server_risk_level": "high", "risk_level": "high", "new_version_id": mv2, "version": 3, "draft_out_of_date": false}),
		u("proposal.approve", "ai_proposal", aip, map[string]any{"mode": "scenario", "model_id": mdl, "base_version_id": mv, "patch_hash": hash("e"),
			"server_risk_level": "low", "risk_level": "low", "scenario_id": scn}),
		u("proposal.reject", "ai_proposal", aip, map[string]any{"mode": "edit", "model_id": mdl, "base_version_id": mv, "from": "needs_input"}),
		u("scenario.create", "scenario", scn, map[string]any{"model_id": mdl, "base_version_id": mv, "patch_hash": hash("f"), "operations": 2}),
		u("simulation.create", "simulation", sim, map[string]any{"model_version_id": mv, "replications": 30, "engine_version": "des-go-0.4.0",
			"model_hash": hash("1"), "effective_model_hash": hash("2"), "config_hash": hash("3"), "dataset_manifest_hash": hash("4"), "scenario_id": scn}),
		u("simulation.cancel", "simulation", sim, map[string]any{"effect": "cancellation_requested"}),
		w("simulation.complete", "simulation", sim, map[string]any{"attempt": 1, "result_digest": hash("5"), "events": int64(123456)}),
		w("simulation.cancelled", "simulation", sim, map[string]any{"attempt": 2}),
		{ActorType: audit.System, ActorID: "provision-tenant", Action: "organization.provision", TargetType: "organization", TargetID: org,
			RequestID: req, Metadata: map[string]any{"owner_user_id": user}},
		w("job.attempt_failed", "simulation", sim, map[string]any{"job_id": job, "job_type": "simulation", "attempt": 1, "max_attempts": 3,
			"code": "WALL_TIMEOUT_EXCEEDED", "retryable": false, "outcome": "failed"}),
		w("job.attempt_failed", "import", imp, map[string]any{"job_id": job, "job_type": "import_profile", "attempt": 1, "max_attempts": 3,
			"code": "LEASE_EXPIRED", "retryable": true, "outcome": "requeued", "uncorrelated": true}),
		w("job.attempt_failed", "ai_proposal", aip, map[string]any{"job_id": job, "job_type": "model_proposal", "attempt": 3, "max_attempts": 3,
			"code": "AI_TIMEOUT", "retryable": true, "outcome": "failed"}),
		w("job.attempt_failed", "ai_analysis", ids.New("ana"), map[string]any{"job_id": job, "job_type": "analysis", "attempt": 1,
			"max_attempts": 3, "code": "INTERNAL", "retryable": true, "outcome": "requeued"}),
	}
}

func TestEveryActionFits(t *testing.T) {
	seen := map[string]bool{}
	for _, e := range valid() {
		if err := audit.Check(e); err != nil {
			t.Errorf("%s: %v", e.Action, err)
		}
		seen[e.Action] = true
	}
	for _, a := range audit.Actions() {
		if !seen[a] {
			t.Errorf("%s has no example here", a)
		}
	}
	if !slices.IsSorted(audit.Actions()) {
		t.Error("Actions is not sorted")
	}
}

// TEST-027 sentinels: what an audit event must never hold.
const (
	csvRow  = "CASE-SENTINEL-7731,FCT_FAIL,2026-10-01T00:00:00Z"
	apiKey  = "sk-ant-api03-SENTINELsentinel0123456789abcdef"
	thought = "SENTINEL: let me think step by step"
)

// Every way an event can leave the catalogue is refused, and the error
// names the action and the field but never the value.
func TestRefused(t *testing.T) {
	base := audit.Event{ActorType: audit.User, ActorID: user, Action: "proposal.reject", TargetType: "ai_proposal", TargetID: aip,
		RequestID: req, Metadata: map[string]any{"mode": "edit", "model_id": mdl, "base_version_id": mv, "from": "ready"}}
	if err := audit.Check(base); err != nil {
		t.Fatal(err)
	}
	meta := func(kv ...any) func(*audit.Event) {
		return func(e *audit.Event) { e.Metadata = with(e.Metadata, kv...) }
	}
	for name, edit := range map[string]func(*audit.Event){
		"unknown action":               func(e *audit.Event) { e.Action = "proposal.delete" },
		"action of free text":          func(e *audit.Event) { e.Action = csvRow },
		"a worker writes a user event": func(e *audit.Event) { e.ActorType = audit.Worker; e.ActorID = worker },
		"user actor not a user ID":     func(e *audit.Event) { e.ActorID = "usr_" + csvRow },
		"unknown actor type":           func(e *audit.Event) { e.ActorType = "admin" },
		"target type of another action": func(e *audit.Event) {
			e.TargetType = "simulation"
			e.TargetID = sim
		},
		"target ID of another kind":    func(e *audit.Event) { e.TargetID = mdl },
		"target ID of free text":       func(e *audit.Event) { e.TargetID = csvRow },
		"request ID not a request":     func(e *audit.Event) { e.RequestID = job },
		"no request ID":                func(e *audit.Event) { e.RequestID = "" },
		"the person's comment":         meta("comment", "approved by the night shift SENTINEL"),
		"the scenario name":            meta("name", "SENTINEL scenario"),
		"the intent":                   meta("intent", "add a tester SENTINEL"),
		"the prompt":                   meta("prompt", "SYSTEM SENTINEL"),
		"chain-of-thought":             meta("thinking", thought),
		"the provider's summary":       meta("summary", "SENTINEL"),
		"the patch":                    meta("patch", []any{map[string]any{"op": "replace", "path": "/x", "value": csvRow}}),
		"a CSV row":                    meta("row", csvRow),
		"the file name":                meta("filename", "SENTINEL.csv"),
		"an enum of free text":         meta("from", thought),
		"an enum value of another key": meta("from", "approved"),
		"an ID of another kind":        meta("model_id", mv),
		"an ID of free text":           meta("model_id", csvRow),
		"an ID as a number":            meta("model_id", 7),
		"a nested map":                 meta("model_id", map[string]any{"x": csvRow}),
		"a key of free text":           meta(csvRow, 1),
	} {
		e := base
		e.Metadata = maps.Clone(base.Metadata)
		edit(&e)
		err := audit.Check(e)
		if !errors.Is(err, audit.ErrInvalid) {
			t.Errorf("%s: %v", name, err)
			continue
		}
		if strings.Contains(err.Error(), "SENTINEL") || strings.Contains(err.Error(), "FCT_FAIL") {
			t.Errorf("%s: the error repeats the value: %v", name, err)
		}
	}
	// A worker or system actor is an actor reference (D-092): what
	// twin.actor_ref allows, never free text.
	for _, e := range []audit.Event{
		{ActorType: audit.Worker, ActorID: "1 worker SENTINEL", Action: "simulation.cancelled", TargetType: "simulation", TargetID: sim,
			RequestID: req, Metadata: map[string]any{"attempt": 1}},
		{ActorType: audit.System, ActorID: "provision tenant", Action: "organization.provision", TargetType: "organization", TargetID: org,
			RequestID: req, Metadata: map[string]any{"owner_user_id": user}},
		{ActorType: audit.Worker, ActorID: "", Action: "simulation.cancelled", TargetType: "simulation", TargetID: sim, RequestID: req},
	} {
		var ie *audit.InvalidError
		if err := audit.Check(e); !errors.As(err, &ie) || ie.Field != "actor_id" || strings.Contains(err.Error(), "SENTINEL") {
			t.Errorf("%s by %q: %v", e.Action, e.ActorID, err)
		}
	}
}

// Value shapes beyond IDs and enumerations.
func TestShapes(t *testing.T) {
	gen := audit.Event{ActorType: audit.Worker, ActorID: worker, Action: "proposal.generate", TargetType: "ai_proposal", TargetID: aip,
		RequestID: req, Metadata: with(provider(), "attempt", 1, "status", "needs_input")}
	if err := audit.Check(gen); err != nil {
		t.Fatal(err)
	}
	for name, kv := range map[string][]any{
		"a negative count":          {"input_tokens", int64(-1)},
		"a fractional count":        {"latency_ms", 1.5},
		"a count as text":           {"attempt", "1"},
		"a boolean as a count":      {"attempt", true},
		"a hash of free text":       {"input_hash", csvRow},
		"a hash without its prefix": {"input_hash", strings.Repeat("a", 64)},
		"a key as a label":          {"model", apiKey},
		"a token as a label":        {"credential_label", "Bearer eyJhbGciOiJSUzI1NiJ9.eyJzdWIiOiJTRU5USU5FTCJ9.U0VOVElORUw"},
		"free text as a label":      {"provider", "the model said SENTINEL"},
		"an e-mail as a label":      {"provider_request_id", "sentinel@example.com"},
		"a long label":              {"model", "m" + strings.Repeat("x", 128)},
		"a label list of text":      {"failed_over_from", []string{"org-a", thought}},
		"a long label list":         {"failed_over_from", []string{"a", "b", "c", "d", "e", "f", "g", "h", "i"}},
		"a label list of numbers":   {"failed_over_from", []int{1}},
		"an empty label in a list":  {"failed_over_from", []string{"org-a", ""}}, // review L4
		"a code of free text":       {"status", "SENTINEL done"},
		"NUL in a label":            {"model", "claude\x00"},
	} {
		e := gen
		e.Metadata = with(gen.Metadata, kv...)
		if err := audit.Check(e); !errors.Is(err, audit.ErrInvalid) {
			t.Errorf("%s: %v", name, err)
		} else if strings.Contains(err.Error(), "SENTINEL") || strings.Contains(err.Error(), "sk-ant") {
			t.Errorf("%s: %v", name, err)
		}
	}
	// At most 32 fields.
	e := gen
	e.Metadata = map[string]any{}
	for i := range 33 {
		e.Metadata["k"+strconv.Itoa(i)] = 1
	}
	// The bound is checked before any field, so the refusal names it.
	var ie *audit.InvalidError
	if err := audit.Check(e); !errors.As(err, &ie) || ie.Field != "metadata" {
		t.Errorf("33 fields: %v", err)
	}
}

func TestActors(t *testing.T) {
	for _, s := range []string{"worker-1", "w", "worker-0.pod_a:1", "provision-tenant", "a" + strings.Repeat("b", 127)} {
		if !audit.ValidActor(s) {
			t.Errorf("%q refused", s)
		}
	}
	for _, s := range []string{"", "1worker", "-w", "has space", "worker/1", "naïve", "a" + strings.Repeat("b", 128), "w\n"} {
		if audit.ValidActor(s) {
			t.Errorf("%q accepted", s)
		}
	}
}

var actionLiteral = regexp.MustCompile(`^(project|membership|model|import|proposal|scenario|simulation|organization|job|analysis)\.[a-z_]+(\.[a-z_]+)?$`)

// The catalogue is the code's: every action name a production file writes
// is listed, and every listed action is written somewhere. (What each one
// holds is checked at run time by Check, which the database tests run.)
func TestTheCatalogueMatchesTheCode(t *testing.T) {
	root := filepath.Join("..", "..")
	found := map[string][]string{}
	for _, dir := range []string{"internal", "cmd"} {
		err := filepath.WalkDir(filepath.Join(root, dir), func(path string, d fs.DirEntry, err error) error {
			if err != nil {
				return err
			}
			if d.IsDir() {
				if d.Name() == "testdata" || path == filepath.Join(root, "internal", "audit") {
					return filepath.SkipDir
				}
				return nil
			}
			if !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
				return nil
			}
			src, err := os.ReadFile(path)
			if err != nil {
				return err
			}
			f, err := parser.ParseFile(token.NewFileSet(), path, src, 0)
			if err != nil {
				return err
			}
			ast.Inspect(f, func(n ast.Node) bool {
				lit, ok := n.(*ast.BasicLit)
				if !ok || lit.Kind != token.STRING {
					return true
				}
				s, err := strconv.Unquote(lit.Value)
				if err == nil && actionLiteral.MatchString(s) && !strings.HasSuffix(s, ".json") { // a schema file, not an action
					found[s] = append(found[s], path)
				}
				// One writer (D-091 (c)): only auditlog's checked append
				// writes the table.
				if err == nil && strings.Contains(strings.ToLower(s), "into twin.audit_events") &&
					path != filepath.Join(root, "internal", "storage", "auditlog", "auditlog.go") {
					t.Errorf("%s writes twin.audit_events itself, around auditlog's catalogue check", path)
				}
				return true
			})
			return nil
		})
		if err != nil {
			t.Fatal(err)
		}
	}
	for a, where := range found {
		if !slices.Contains(audit.Actions(), a) {
			t.Errorf("%s (%v) is not in the catalogue", a, where)
		}
	}
	for _, a := range audit.Actions() {
		if len(found[a]) == 0 {
			t.Errorf("%s is in the catalogue but no production file writes it", a)
		}
	}
}
