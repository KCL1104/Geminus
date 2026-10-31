// Package prompts embeds the versioned system prompt templates
// (prompts/<role>/system.md), so the context builder sends exactly the
// reviewed files, never a copy. A template's first line names its
// prompt_template_version, which every provider call records (AI_SPEC §10).
// Two are not a role's own: research (H-046 (2)), appended to a proposal
// role's system text, and repair (H-057), the text of the follow-up turn of
// a proposal's one repair; the prompt's version names them after the
// role's ("modeler-0.2.0+repair-0.1.0").
package prompts

import (
	"embed"
	"fmt"
	"regexp"
	"strings"
)

//go:embed */system.md
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
