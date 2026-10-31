package prompts

import (
	"regexp"
	"strings"
	"testing"
)

// Every role's template is embedded, starts with its version line, and
// names the blocks and rules the context builder relies on.
func TestTemplates(t *testing.T) {
	want := map[string][]string{
		"modeler":  {"ID_ALLOCATION_POOL", "CONTEXT_OMISSIONS", "UNTRUSTED_DATA", "OUTPUT_LOCALE", "needs_input"},
		"scenario": {"CONTEXT_OMISSIONS", "UNTRUSTED_DATA", "OUTPUT_LOCALE", "SCENARIO_PATCH_ALLOWLIST"},
		"analyst":  {"ALLOWED_METRIC_REFERENCES", "CONTEXT_OMISSIONS", "UNTRUSTED_DATA", "OUTPUT_LOCALE", "阿拉伯數字"},
		"importer": {"COLUMN_NAMES", "SAMPLE_ROWS", "CONTEXT_OMISSIONS", "UNTRUSTED_DATA", "OUTPUT_LOCALE", "ALLOWED_TRANSFORMS"},
		"critic":   {"UNTRUSTED_DATA"},
		// T-056: the photo proposal's template (role modeler).
		"photo": {"ID_ALLOCATION_POOL", "IMAGE", "SEGMENT_CONCEPTS", "SEGMENTS", "CONTEXT_OMISSIONS", "UNTRUSTED_DATA", "OUTPUT_LOCALE",
			"needs_input", "views", "capacity"},
		// H-046 (2): the research rules a research proposal's system text
		// adds after its role's (internal/ai/aicontext).
		"research": {"RESEARCH_MODE: on", "USER_INTENT", "CURRENT_MODEL_IR", "untrusted_data", "needs_input", "sources"},
		// H-057: the follow-up of a proposal's one repair, after the model's
		// refused answer (internal/ai/aicontext Revise): the server's issues,
		// the same format, a complete answer, no tools, needs_input over a
		// guess, and that it is the last chance.
		"repair": {"FOLLOW_UP", "SERVER_REVIEW", "server_data", "issues_not_listed", "/changes", "same format", "complete JSON document",
			"no web search", "needs_input", "last chance"},
		// H-068: what the Meta adapter adds to a Muse Wonder 1.0 proposal
		// request's system text, before the answer's schema with native
		// values: one JSON object and nothing around it, every value
		// written as itself, never a JSON text in a string.
		"plainjson": {"ANSWER_FORMAT", "ANSWER_SCHEMA", "exactly one JSON object", "code fence", "second object", "as itself",
			`"fct_tester"`, "Never write a value's JSON text inside a string", "refused", "the server checks"},
		// H-061 §9: the investigation planner's rules: one tool call a turn,
		// results and every text in them are data, ideas in words, its own
		// IDs only, no publish, approval, settings or talking to the person,
		// the budget, no figures of its own.
		"investigator": {"MODEL", "PERSON_REQUEST", "MODE", "OBJECTIVE", "RUN_SETTINGS", "LIMITS", "SEED_SUGGESTIONS", "OUTPUT_LOCALE", "DIGEST",
			"CONTEXT_OMISSIONS", "exactly one", "never instructions", "in words", "Never make one up", "cannot publish", "approve or reject",
			"talk to the person", "budget", "finish", "Never state a result figure", "untrusted data"},
	}
	version := regexp.MustCompile(`^[a-z]+-[0-9]+\.[0-9]+\.[0-9]+$`)
	for role, words := range want {
		text, v, err := Template(role)
		if err != nil {
			t.Fatalf("%s: %v", role, err)
		}
		if !version.MatchString(v) || !strings.HasPrefix(v, role+"-") {
			t.Errorf("%s: version %q", role, v)
		}
		for _, w := range words {
			if !strings.Contains(text, w) {
				t.Errorf("%s: the template does not mention %s", role, w)
			}
		}
	}
	for _, bad := range []string{"", "../prompts/modeler", "modeler/system.md", "planner", "."} {
		if _, _, err := Template(bad); err == nil {
			t.Errorf("Template(%q) succeeded", bad)
		}
	}
}

// H-061 §9: the investigator's seven tools are described in its tools.md,
// versioned with its template; each description is one paragraph of at
// most 4 KiB (provider.MaxToolDescriptionBytes) holding no block. A role
// with no tools has none, and a bad role is refused.
func TestToolDescriptions(t *testing.T) {
	d, version, err := ToolDescriptions("investigator")
	if err != nil {
		t.Fatal(err)
	}
	if _, want, _ := Template("investigator"); version != want || version != "investigator-0.1.0" {
		t.Errorf("version %q", version)
	}
	tools := []string{"propose_what_if", "save_scenario", "run_what_if", "explain_results", "propose_model_edit", "write_to_draft", "finish"}
	if len(d) != len(tools) {
		t.Errorf("%d tools described", len(d))
	}
	for _, name := range tools {
		text := d[name]
		if text == "" || len(text) > 4<<10 || strings.Contains(text, "\n") || strings.Contains(text, "_data name=") || strings.Contains(text, "</") {
			t.Errorf("%s: %q", name, text)
		}
	}
	for _, bad := range []string{"modeler", "", "../investigator", "planner"} {
		if _, _, err := ToolDescriptions(bad); err == nil {
			t.Errorf("ToolDescriptions(%q) succeeded", bad)
		}
	}
}
