package aicontext

import (
	"encoding/json/jsontext"
	"encoding/json/v2"
	"regexp"
	"sort"
	"strings"
	"unicode"
	"unicode/utf8"
)

// Redaction kinds, counted in the disclosure and shown in the text as
// [REDACTED:<kind>].
const (
	RedactCredential = "credential"
	RedactURL        = "url"
	RedactEmail      = "email"
	RedactIP         = "ip_address"
	RedactServerID   = "server_id"
	// RedactControl counts removed invisible characters (bidirectional
	// overrides, zero-width characters, other format and control
	// characters), which can hide text from the reviewer.
	RedactControl = "control"
	// RedactTruncated counts strings cut to the context's bound.
	RedactTruncated = "truncated"
)

type pattern struct {
	kind string
	re   *regexp.Regexp
	// keep is the number of leading submatches kept in front of the marker
	// (the "password=" of password=...).
	keep bool
}

// patterns are applied in order; credentials first, so a key inside a URL
// is still counted as what it is. Default deny (AI_SPEC §3): raw
// credentials, any URL, e-mail and IP addresses and server resource IDs
// (D-001, uppercase ULIDs: another tenant's, or this one's, which the
// provider never needs) never reach a provider.
var patterns = []pattern{
	{RedactCredential, regexp.MustCompile(`-----BEGIN [A-Z ]*PRIVATE KEY-----(?s:.*?)(?:-----END [A-Z ]*PRIVATE KEY-----|$)`), false},
	{RedactCredential, regexp.MustCompile(`\bsk-ant-[A-Za-z0-9_-]{8,}`), false},
	{RedactCredential, regexp.MustCompile(`\b(?:sk|pk|rk)-[A-Za-z0-9_-]{16,}`), false},
	{RedactCredential, regexp.MustCompile(`\b(?:AKIA|ASIA)[0-9A-Z]{16}\b`), false},
	{RedactCredential, regexp.MustCompile(`\b(?:gh[pousr]_[A-Za-z0-9]{20,}|github_pat_[A-Za-z0-9_]{20,})\b`), false},
	{RedactCredential, regexp.MustCompile(`\bxox[abprs]-[A-Za-z0-9-]{10,}`), false},
	{RedactCredential, regexp.MustCompile(`\bAIza[0-9A-Za-z_-]{30,}`), false},
	{RedactCredential, regexp.MustCompile(`\beyJ[A-Za-z0-9_-]{6,}\.[A-Za-z0-9_-]{6,}\.[A-Za-z0-9_-]{6,}`), false},
	{RedactCredential, regexp.MustCompile(`(?i)\b(?:bearer|basic)\s+[A-Za-z0-9._~+/=-]{12,}`), false},
	{RedactCredential, regexp.MustCompile(`(?i)(\b(?:password|passwd|pwd|secret|token|api[_-]?key|apikey|access[_-]?key|client[_-]?secret|private[_-]?key|authorization)\s*[:=]\s*)[^\s,;"']{6,}`), true},
	{RedactURL, regexp.MustCompile(`(?i)\b[a-z][a-z0-9+.-]{1,15}://[^\s"'<>]+`), false},
	{RedactEmail, regexp.MustCompile(`\b[A-Za-z0-9._%+-]+@[A-Za-z0-9-]+(?:\.[A-Za-z0-9-]+)*\.[A-Za-z]{2,}\b`), false},
	{RedactIP, regexp.MustCompile(`\b(?:25[0-5]|2[0-4][0-9]|1[0-9]{2}|[1-9]?[0-9])(?:\.(?:25[0-5]|2[0-4][0-9]|1[0-9]{2}|[1-9]?[0-9])){3}\b`), false},
	{RedactServerID, regexp.MustCompile(`\b(?:aip|ana|art|aud|ds|dsv|imp|job|mdl|mv|org|prj|req|scn|sim|usr)_[0-7][0-9A-HJKMNP-TV-Z]{25}\b`), false},
}

// invisible reports characters that are dropped from untrusted text:
// control characters other than tab and newline, and format characters
// (bidirectional overrides and isolates, zero-width characters, BOM).
func invisible(r rune) bool {
	if r == '\t' || r == '\n' {
		return false
	}
	return unicode.IsControl(r) || unicode.Is(unicode.Cf, r)
}

// sanitizer redacts and bounds untrusted strings and counts what it did.
type sanitizer struct {
	maxRunes int
	counts   map[string]int
}

func newSanitizer(maxRunes int) *sanitizer {
	return &sanitizer{maxRunes: maxRunes, counts: map[string]int{}}
}

// text redacts s and cuts it to the bound.
func (z *sanitizer) text(s string) string {
	return z.textBound(s, z.maxRunes)
}

func (z *sanitizer) textBound(s string, maxRunes int) string {
	s = strings.ToValidUTF8(s, "�")
	if strings.IndexFunc(s, invisible) >= 0 {
		var b strings.Builder
		for _, r := range s {
			if invisible(r) {
				z.counts[RedactControl]++
				continue
			}
			b.WriteRune(r)
		}
		s = b.String()
	}
	for _, p := range patterns {
		s = p.re.ReplaceAllStringFunc(s, func(m string) string {
			marker := "[REDACTED:" + p.kind + "]"
			if p.keep {
				if sub := p.re.FindStringSubmatch(m); len(sub) > 1 {
					if strings.HasPrefix(m[len(sub[1]):], "[REDACTED:") {
						return m // the value was redacted by an earlier pattern
					}
					z.counts[p.kind]++
					return sub[1] + marker
				}
			}
			z.counts[p.kind]++
			return marker
		})
	}
	if maxRunes > 0 && utf8.RuneCountInString(s) > maxRunes {
		z.counts[RedactTruncated]++
		r := []rune(s)
		s = string(r[:maxRunes]) + "…[truncated]"
	}
	return s
}

// value sanitizes every string and member name of a decoded JSON value.
func (z *sanitizer) value(v any) any {
	switch x := v.(type) {
	case string:
		return z.text(x)
	case []any:
		out := make([]any, len(x))
		for i, e := range x {
			out[i] = z.value(e)
		}
		return out
	case map[string]any:
		out := make(map[string]any, len(x))
		keys := make([]string, 0, len(x))
		for k := range x {
			keys = append(keys, k)
		}
		sort.Strings(keys) // deterministic counts and collisions
		for _, k := range keys {
			nk := z.textBound(k, 256)
			if _, dup := out[nk]; dup {
				continue // two names redacted to the same marker: keep the first
			}
			out[nk] = z.value(x[k])
		}
		return out
	}
	return v
}

// json sanitizes a JSON document (untrusted) into a value to marshal.
func (z *sanitizer) json(v jsontext.Value) (any, error) {
	var x any
	if err := json.Unmarshal(v, &x, jsontext.AllowDuplicateNames(false), jsontext.AllowInvalidUTF8(false)); err != nil {
		return nil, err
	}
	return z.value(x), nil
}

// redacted returns the counts without the truncation counter.
func (z *sanitizer) redacted() map[string]int {
	out := map[string]int{}
	for k, n := range z.counts {
		if k != RedactTruncated && n > 0 {
			out[k] = n
		}
	}
	return out
}
