// Package prompts embeds the versioned system prompt templates
// (prompts/<role>/system.md), so the context builder sends exactly the
// reviewed files, never a copy. A template's first line names its
// prompt_template_version, which every provider call records (AI_SPEC §10).
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
