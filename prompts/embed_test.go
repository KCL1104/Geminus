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
		// H-086 (R): the follow-up of the one re-ask of an explanation the
		// server's grounding check refused (internal/ai/aicontext
		// ReviseAnalysis): the server's issues with the rule each code stands
		// for, the same format, a complete answer, no number of its own, no
		// invented value, and that it is the last chance.
		"reground": {"FOLLOW_UP", "SERVER_CHECK", "server_data", "issues_not_listed", "same format", "complete JSON document",
			"ANALYSIS_NUMBER_UNVERIFIED", "never compute", "Never invent", "uncertainty", "last chance", "MODEL_SUMMARY"},
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
			"talk to the person", "budget", "finish", "Never state a result figure", "untrusted data",
			// H-061 A9: a refusal's hint is the server's own sentence; an
			// unused member is left out, never the string null.
			"hint", "Never repeat a refused or failed call unchanged", "leave out a member you do not use", "string null"},
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
	if _, want, _ := Template("investigator"); version != want || version != "investigator-0.4.0" {
		t.Errorf("version %q", version)
	}
	tools := []string{"propose_what_if", "save_scenario", "run_what_if", "explain_results", "propose_model_edit", "write_to_draft", "finish"}
	if len(d) != len(tools) {
		t.Errorf("%d tools described", len(d))
	}
	for _, name := range tools {
		text := d[name]
		if text == "" || len(text) > 4<<10 || strings.Contains(text, "\n") || strings.Contains(text, "_data name=") || strings.Contains(text, "</") ||
			strings.Contains(text, "###") {
			t.Errorf("%s: %q", name, text)
		}
	}
	// H-061 A9 (live, 2026-11-01): a tool with a member that may be null
	// says plainly, in its own description, to leave the member out when
	// unused and never to write it as the string null.
	for tool, member := range map[string]string{"propose_what_if": "from_suggestion", "explain_results": "question", "finish": "best_scenario_id"} {
		if text := d[tool]; !strings.Contains(text, "leave "+member+" out") || !strings.Contains(text, "never write it as the string null") {
			t.Errorf("%s does not say to leave %s out: %q", tool, member, text)
		}
	}
	for _, bad := range []string{"modeler", "", "../investigator", "planner"} {
		if _, _, err := ToolDescriptions(bad); err == nil {
			t.Errorf("ToolDescriptions(%q) succeeded", bad)
		}
	}
}

// H-061 A9: the members a tool's shown input schema leaves optional (those
// that may be null) are described in tools.md under their tool ("###
// <member>"), versioned with the template: each one paragraph within the
// same bounds as a tool's, saying to omit the member when unused and never
// to write it as the string null. No other member is described.
func TestMemberDescriptions(t *testing.T) {
	m, version, err := MemberDescriptions("investigator")
	if err != nil {
		t.Fatal(err)
	}
	if _, want, _ := Template("investigator"); version != want {
		t.Errorf("version %q, the template's %q", version, want)
	}
	want := map[string][]string{"propose_what_if": {"from_suggestion"}, "explain_results": {"question"}, "finish": {"best_scenario_id"}}
	if len(m) != len(want) {
		t.Errorf("members of %d tools described: %v", len(m), m)
	}
	for tool, members := range want {
		if len(m[tool]) != len(members) {
			t.Errorf("%s: %d members described", tool, len(m[tool]))
		}
		for _, member := range members {
			text := m[tool][member]
			if !strings.HasPrefix(text, "Omit it when") || !strings.Contains(text, "Never write it as the string null.") || len(text) > 4<<10 ||
				strings.Contains(text, "\n") || strings.Contains(text, "_data name=") || strings.Contains(text, "</") || strings.Contains(text, "#") {
				t.Errorf("%s.%s: %q", tool, member, text)
			}
		}
	}
	// A tool's own description stops at its first member heading.
	d, _, _ := ToolDescriptions("investigator")
	if strings.Contains(d["propose_what_if"], m["propose_what_if"]["from_suggestion"]) {
		t.Error("the member's text is in its tool's description")
	}
	for _, bad := range []string{"modeler", "", "../investigator", "planner"} {
		if _, _, err := MemberDescriptions(bad); err == nil {
			t.Errorf("MemberDescriptions(%q) succeeded", bad)
		}
	}
}

// tools.md's shape is checked before any of it is used: a member heading
// outside a tool, a member described twice, an empty member, a heading of
// another level and a member heading that is not a member name are refused.
func TestToolsDocRefuses(t *testing.T) {
	const head = "# Planner tools — prompt_template_version: investigator-9.9.9\n\n"
	good := head + "## finish\nEnd it.\n\n### best_scenario_id\nOmit it when none did.\n"
	doc, err := parseTools("investigator", "investigator-9.9.9", good)
	if err != nil || doc.tools["finish"] != "End it." || doc.members["finish"]["best_scenario_id"] != "Omit it when none did." {
		t.Fatalf("%+v %v", doc, err)
	}
	for name, text := range map[string]string{
		"another version":            strings.Replace(good, "9.9.9", "9.9.8", 1),
		"a member outside a tool":    head + "### best_scenario_id\nOmit it.\n\n## finish\nEnd it.\n",
		"a member twice":             good + "\n### best_scenario_id\nAgain.\n",
		"an empty member":            head + "## finish\nEnd it.\n\n### best_scenario_id\n\n## save_scenario\nSave.\n",
		"a tool with only members":   head + "## finish\n### best_scenario_id\nOmit it.\n",
		"a heading of another kind":  head + "## finish\nEnd it.\n\n#### best_scenario_id\nOmit it.\n",
		"a member name of free text": head + "## finish\nEnd it.\n\n### Best scenario\nOmit it.\n",
		"a tool twice":               good + "\n## finish\nAgain.\n",
		"no tool":                    head,
	} {
		if doc, err := parseTools("investigator", "investigator-9.9.9", text); err == nil {
			t.Errorf("%s: taken (%+v)", name, doc)
		}
	}
}
