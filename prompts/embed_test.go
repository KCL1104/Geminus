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
