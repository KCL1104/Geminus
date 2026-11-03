// Package prompts embeds the versioned system prompt templates
// (prompts/<role>/system.md), so the context builder sends exactly the
// reviewed files, never a copy. A template's first line names its
// prompt_template_version, which every provider call records (AI_SPEC §10).
// Three are not a role's own: research (H-046 (2)), appended to a proposal
// role's system text, repair (H-057), the text of the follow-up turn of a
// proposal's one repair, and reground (H-086 (R)), the text of the
// follow-up turn of the one re-ask of an explanation the server's grounding
// check refused; the prompt's version names them after the role's
// ("modeler-0.2.0+repair-0.1.0", "analyst-0.3.3+reground-0.1.0"). A role that calls tools (the
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

var (
	toolHeading   = regexp.MustCompile(`^## ([a-z][a-z0-9_]{0,63})$`)
	memberHeading = regexp.MustCompile(`^### ([a-z][a-z0-9_]{0,63})$`)
)

// ToolDescriptions returns the descriptions of role's tools by tool name
// (prompts/<role>/tools.md: after its version line, one "## <tool>" heading
// per tool and its description as one paragraph, then the descriptions of
// some of its members: MemberDescriptions) and their version, which must be
// the role's template's: the descriptions are prompt text, reviewed and
// versioned with it (H-061 §9).
func ToolDescriptions(role string) (map[string]string, string, error) {
	doc, err := readTools(role)
	if err != nil {
		return nil, "", err
	}
	return doc.tools, doc.version, nil
}

// MemberDescriptions returns the descriptions tools.md gives some of role's
// tools' arguments, by tool and member name: under a tool's description,
// one "### <member>" heading per member and its description as one
// paragraph, and their version, the template's (H-061 A9: a member the
// tool's shown input schema leaves optional, the one that may be null,
// says to omit it when unused). A tool none of whose members is described
// is absent.
func MemberDescriptions(role string) (map[string]map[string]string, string, error) {
	doc, err := readTools(role)
	if err != nil {
		return nil, "", err
	}
	return doc.members, doc.version, nil
}

// toolsDoc is a role's tools.md, read.
type toolsDoc struct {
	tools   map[string]string
	members map[string]map[string]string
	version string
}

func readTools(role string) (toolsDoc, error) {
	_, version, err := Template(role)
	if err != nil {
		return toolsDoc{}, err
	}
	data, err := fs.ReadFile(role + "/tools.md")
	if err != nil {
		return toolsDoc{}, fmt.Errorf("prompts: no tools for %q", role)
	}
	return parseTools(role, version, string(data))
}

// parseTools reads role's tools.md text, whose version line must name
// version: each tool's section is its heading, its description and its
// members' sections, each a heading and a description; nothing else.
func parseTools(role, version, text string) (toolsDoc, error) {
	lines := strings.Split(strings.TrimRight(text, "\n"), "\n")
	m := versionLine.FindStringSubmatch(lines[0])
	if m == nil || m[1] != version {
		return toolsDoc{}, fmt.Errorf("prompts: %s/tools.md does not start with its template's prompt_template_version", role)
	}
	doc := toolsDoc{tools: map[string]string{}, members: map[string]map[string]string{}, version: version}
	tool, member := "", ""
	var body []string
	flush := func() error {
		if tool == "" {
			return nil
		}
		text := strings.TrimSpace(strings.Join(body, " "))
		body = nil
		switch {
		case text == "" && member != "":
			return fmt.Errorf("prompts: %s/tools.md: the member %s of %s has no description", role, member, tool)
		case text == "":
			return fmt.Errorf("prompts: %s/tools.md: the tool %s has no description", role, tool)
		case member != "":
			if doc.members[tool] == nil {
				doc.members[tool] = map[string]string{}
			}
			doc.members[tool][member] = text
		default:
			doc.tools[tool] = text
		}
		return nil
	}
	for _, line := range lines[1:] {
		if h := toolHeading.FindStringSubmatch(line); h != nil {
			if err := flush(); err != nil {
				return toolsDoc{}, err
			}
			if _, dup := doc.tools[h[1]]; dup {
				return toolsDoc{}, fmt.Errorf("prompts: %s/tools.md names %s twice", role, h[1])
			}
			tool, member = h[1], ""
			continue
		}
		if h := memberHeading.FindStringSubmatch(line); h != nil && tool != "" {
			if err := flush(); err != nil {
				return toolsDoc{}, err
			}
			if _, dup := doc.members[tool][h[1]]; dup {
				return toolsDoc{}, fmt.Errorf("prompts: %s/tools.md describes %s of %s twice", role, h[1], tool)
			}
			member = h[1]
			continue
		}
		if strings.HasPrefix(line, "#") || (tool == "" && strings.TrimSpace(line) != "") {
			return toolsDoc{}, fmt.Errorf("prompts: %s/tools.md: text outside a tool's section", role)
		}
		if strings.TrimSpace(line) != "" {
			body = append(body, strings.TrimSpace(line))
		}
	}
	if err := flush(); err != nil {
		return toolsDoc{}, err
	}
	if len(doc.tools) == 0 {
		return toolsDoc{}, fmt.Errorf("prompts: %s/tools.md describes no tool", role)
	}
	return doc, nil
}
