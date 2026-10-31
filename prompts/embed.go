// Package prompts embeds the versioned system prompt templates
// (prompts/<role>/system.md), so the context builder sends exactly the
// reviewed files, never a copy. A template's first line names its
// prompt_template_version, which every provider call records (AI_SPEC §10).
// Two are not a role's own: research (H-046 (2)), appended to a proposal
// role's system text, and repair (H-057), the text of the follow-up turn of
// a proposal's one repair; the prompt's version names them after the
// role's ("modeler-0.2.0+repair-0.1.0"). A role that calls tools (the
// investigator, H-061 §9) also has prompts/<role>/tools.md: the tools'
// descriptions, prompt text versioned with its template (ToolDescriptions).
package prompts

import (
	"embed"
	"fmt"
	"regexp"
	"strings"
)

//go:embed */system.md */tools.md
var fs embed.FS

var versionLine = regexp.MustCompile(`^# .+ — prompt_template_version: ([a-z]+-[0-9]+\.[0-9]+\.[0-9]+)$`)

// Template returns the system template of role and its version.
func Template(role string) (text, version string, err error) {
	if strings.ContainsAny(role, "/\\.") || role == "" {
		return "", "", fmt.Errorf("prompts: bad role %q", role)
	}
	data, err := fs.ReadFile(role + "/system.md")
	if err != nil {
		return "", "", fmt.Errorf("prompts: no template for %q", role)
	}
	text = string(data)
	first, _, _ := strings.Cut(text, "\n")
	m := versionLine.FindStringSubmatch(first)
	if m == nil || !strings.HasPrefix(m[1], role+"-") {
		return "", "", fmt.Errorf("prompts: %s/system.md does not start with its prompt_template_version", role)
	}
	return text, m[1], nil
}

var toolHeading = regexp.MustCompile(`^## ([a-z][a-z0-9_]{0,63})$`)

// ToolDescriptions returns the descriptions of role's tools by tool name
// (prompts/<role>/tools.md: after its version line, one "## <tool>" heading
// per tool and its description as one paragraph) and their version, which
// must be the role's template's: the descriptions are prompt text, reviewed
// and versioned with it (H-061 §9).
func ToolDescriptions(role string) (map[string]string, string, error) {
	_, version, err := Template(role)
	if err != nil {
		return nil, "", err
	}
	data, err := fs.ReadFile(role + "/tools.md")
	if err != nil {
		return nil, "", fmt.Errorf("prompts: no tools for %q", role)
	}
	lines := strings.Split(strings.TrimRight(string(data), "\n"), "\n")
	m := versionLine.FindStringSubmatch(lines[0])
	if m == nil || m[1] != version {
		return nil, "", fmt.Errorf("prompts: %s/tools.md does not start with its template's prompt_template_version", role)
	}
	out := map[string]string{}
	name := ""
	var body []string
	flush := func() error {
		if name == "" {
			return nil
		}
		text := strings.TrimSpace(strings.Join(body, " "))
		if text == "" {
			return fmt.Errorf("prompts: %s/tools.md: the tool %s has no description", role, name)
		}
		out[name] = text
		return nil
	}
	for _, line := range lines[1:] {
		if h := toolHeading.FindStringSubmatch(line); h != nil {
			if err := flush(); err != nil {
				return nil, "", err
			}
			if _, dup := out[h[1]]; dup {
				return nil, "", fmt.Errorf("prompts: %s/tools.md names %s twice", role, h[1])
			}
			name, body = h[1], nil
			continue
		}
		if strings.HasPrefix(line, "#") || (name == "" && strings.TrimSpace(line) != "") {
			return nil, "", fmt.Errorf("prompts: %s/tools.md: text outside a tool's section", role)
		}
		if strings.TrimSpace(line) != "" {
			body = append(body, strings.TrimSpace(line))
		}
	}
	if err := flush(); err != nil {
		return nil, "", err
	}
	if len(out) == 0 {
		return nil, "", fmt.Errorf("prompts: %s/tools.md describes no tool", role)
	}
	return out, version, nil
}
