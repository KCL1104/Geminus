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
	// RedactDelimiter counts look-alike angle brackets (full-width, CJK,
	// mathematical, single guillemets...) folded to ASCII, which
	// provider.Seal then escapes like any '<' or '>' (review L3).
	RedactDelimiter = "delimiter"
)

// collided counts member names that redacted to a name the object already
// has: the first value is kept and the context lists the others in
// CONTEXT_OMISSIONS (review L4). It is not a redaction kind.
const collided = "collided"

type pattern struct {
	kind string
	re   *regexp.Regexp
	// keep is the number of leading submatches kept in front of the marker
	// (the "password=" of password=...).
	keep bool
	// accept, when set, decides whether a match is redacted.
	accept func(string) bool
}

// patterns are applied in order; credentials first, so a key inside a URL
// is still counted as what it is. Default deny (AI_SPEC §3): raw
// credentials, any URL, e-mail and IP addresses and server resource IDs
// (D-001, uppercase ULIDs: another tenant's, or this one's, which the
// provider never needs) never reach a provider. A value under a
// credential-like member or column name is redacted by its name
// (credentialName), whatever it looks like.
var patterns = []pattern{
	// PEM and PGP private key blocks, to the end of the text when the end
	// line is missing.
	{RedactCredential, regexp.MustCompile(`-----BEGIN [A-Z0-9 ]*PRIVATE KEY(?: BLOCK)?-----(?s:.*?)(?:-----END [A-Z0-9 ]*PRIVATE KEY(?: BLOCK)?-----|$)`), false, nil},
	{RedactCredential, regexp.MustCompile(`\bsk-ant-[A-Za-z0-9_-]{8,}`), false, nil},
	{RedactCredential, regexp.MustCompile(`\b(?:sk|pk|rk)-[A-Za-z0-9_-]{16,}`), false, nil},
	{RedactCredential, regexp.MustCompile(`\b(?:sk|pk|rk)_(?:live|test)_[A-Za-z0-9]{8,}`), false, nil}, // Stripe
	{RedactCredential, regexp.MustCompile(`\b(?:AKIA|ASIA)[0-9A-Z]{16}\b`), false, nil},
	{RedactCredential, regexp.MustCompile(`\b(?:gh[pousr]_[A-Za-z0-9]{20,}|github_pat_[A-Za-z0-9_]{20,})\b`), false, nil},
	{RedactCredential, regexp.MustCompile(`\bglpat-[A-Za-z0-9_-]{8,}`), false, nil}, // GitLab
	{RedactCredential, regexp.MustCompile(`\bxox[abprs]-[A-Za-z0-9-]{10,}`), false, nil},
	{RedactCredential, regexp.MustCompile(`\bAIza[0-9A-Za-z_-]{30,}`), false, nil},
	{RedactCredential, regexp.MustCompile(`\beyJ[A-Za-z0-9_-]{6,}\.[A-Za-z0-9_-]{6,}\.[A-Za-z0-9_-]{6,}`), false, nil},
	{RedactCredential, regexp.MustCompile(`(?i)\b(?:bearer|basic)\s+[A-Za-z0-9._~+/=-]{12,}`), false, nil},
	// key=value and key: value, the keyword anywhere in the key
	// (DB_PASSWORD, aws_secret_access_key, ?access_token=, AccountKey=;
	// review M2). The value stops at a separator of query strings and
	// connection strings.
	{RedactCredential, regexp.MustCompile(`(?i)(\b[A-Za-z0-9_-]*?(?:password|passwd|passphrase|pwd|secret|token|api[_-]?key|access[_-]?key|account[_-]?key|` +
		`client[_-]?secret|private[_-]?key|authorization|credentials?)[A-Za-z0-9_-]*\s*[:=]\s*)[^\s,;"'&]{6,}`), true, nil},
	// A long base64 run with upper- and lowercase letters and digits: a key
	// body line in a cell of its own, a token or a signature. Hex digests
	// (one case) and words are left alone.
	{RedactCredential, regexp.MustCompile(`[A-Za-z0-9+/]{40,}={0,2}`), false, mixedBase64},
	{RedactURL, regexp.MustCompile(`(?i)\b[a-z][a-z0-9+.-]{1,15}://[^\s"'<>]+`), false, nil},
	// Without a scheme: www.host, or a host with a top-level domain and a
	// path (intranet.example.com/wiki). A bare file name (steps.csv) stays.
	{RedactURL, regexp.MustCompile(`(?i)\b(?:www\.[a-z0-9-]+(?:\.[a-z0-9-]+)+(?:/[^\s"'<>]*)?|[a-z0-9-]+(?:\.[a-z0-9-]+)*\.[a-z]{2,}(?::[0-9]{1,5})?/[^\s"'<>]*)`), false, nil},
	{RedactEmail, regexp.MustCompile(`\b[A-Za-z0-9._%+-]+@[A-Za-z0-9-]+(?:\.[A-Za-z0-9-]+)*\.[A-Za-z]{2,}\b`), false, nil},
	{RedactIP, regexp.MustCompile(`\b(?:25[0-5]|2[0-4][0-9]|1[0-9]{2}|[1-9]?[0-9])(?:\.(?:25[0-5]|2[0-4][0-9]|1[0-9]{2}|[1-9]?[0-9])){3}\b`), false, nil},
	{RedactServerID, regexp.MustCompile(`\b(?:aip|ana|art|aud|ds|dsv|imp|job|mdl|mv|org|prj|req|scn|sim|usr)_[0-7][0-9A-HJKMNP-TV-Z]{25}\b`), false, nil},
}

// mixedBase64 accepts a base64 run that has an uppercase letter, a
// lowercase letter and a digit.
func mixedBase64(s string) bool {
	return strings.ContainsAny(s, "ABCDEFGHIJKLMNOPQRSTUVWXYZ") && strings.ContainsAny(s, "abcdefghijklmnopqrstuvwxyz") &&
		strings.ContainsAny(s, "0123456789")
}

// lookAlike maps angle brackets that read like '<' or '>' to ASCII
// (review L3): full-width, small, CJK, mathematical and technical angle
// brackets, single guillemets and modifier arrowheads. The CJK title marks
// 《》 and quotes 「」 are prose and stay.
var lookAlike = map[rune]rune{
	'＜': '<', '﹤': '<', '〈': '<', '⟨': '<', '〈': '<', '‹': '<', '˂': '<',
	'＞': '>', '﹥': '>', '〉': '>', '⟩': '>', '〉': '>', '›': '>', '˃': '>',
}

// credentialName reports whether a member or column name says its value
// is a credential (review M2, AI_SPEC §3 default deny): a word for a
// password, secret, credential, API, access, account, private or secret
// key, connection string, authorization or cookie anywhere in the name, or
// a token at its end (access_token, GITHUB_TOKEN; not max_tokens). Words
// are split at punctuation and case changes (DBPassword, AccountKey).
func credentialName(name string) bool {
	words := nameWords(name)
	for i, w := range words {
		switch w {
		case "password", "passwd", "passphrase", "pwd", "secret", "secrets", "credential", "credentials", "apikey", "accesskey",
			"accountkey", "privatekey", "secretkey", "clientsecret", "connectionstring", "authorization", "cookie":
			return true
		}
		if i+1 < len(words) {
			switch w + words[i+1] {
			case "apikey", "accesskey", "accountkey", "privatekey", "secretkey", "connectionstring":
				return true
			}
		}
	}
	return len(words) > 0 && words[len(words)-1] == "token"
}

// nameWords splits a name into lowercase words at every character that is
// not a letter or digit and at case changes.
func nameWords(s string) []string {
	var words []string
	var cur []rune
	flush := func() {
		if len(cur) > 0 {
			words = append(words, strings.ToLower(string(cur)))
			cur = cur[:0]
		}
	}
	rs := []rune(s)
	for i, r := range rs {
		if !unicode.IsLetter(r) && !unicode.IsDigit(r) {
			flush()
			continue
		}
		if i > 0 && unicode.IsUpper(r) && len(cur) > 0 {
			prev := rs[i-1]
			nextLower := i+1 < len(rs) && unicode.IsLower(rs[i+1])
			if unicode.IsLower(prev) || unicode.IsDigit(prev) || (unicode.IsUpper(prev) && nextLower) {
				flush()
			}
		}
		cur = append(cur, r)
	}
	flush()
	return words
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
	if strings.ContainsFunc(s, func(r rune) bool { _, ok := lookAlike[r]; return ok }) {
		s = strings.Map(func(r rune) rune {
			if a, ok := lookAlike[r]; ok {
				z.counts[RedactDelimiter]++
				return a
			}
			return r
		}, s)
	}
	for _, p := range patterns {
		s = p.re.ReplaceAllStringFunc(s, func(m string) string {
			if p.accept != nil && !p.accept(m) {
				return m
			}
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
			z.member(out, k, x[k])
		}
		return out
	}
	return v
}

// member sanitizes one member into out: its name, and its value, which is
// redacted whole when the name says it is a credential. Two names that
// redact to the same text keep the first value; the others are counted
// (collided) for CONTEXT_OMISSIONS.
func (z *sanitizer) member(out map[string]any, name string, v any) {
	nk := z.textBound(name, 256)
	if _, dup := out[nk]; dup {
		z.counts[collided]++
		return
	}
	switch v.(type) {
	case nil, bool:
		out[nk] = v // says nothing secret
		return
	}
	if credentialName(name) {
		z.counts[RedactCredential]++
		out[nk] = "[REDACTED:" + RedactCredential + "]"
		return
	}
	out[nk] = z.value(v)
}

// json sanitizes a JSON document (untrusted) into a value to marshal.
func (z *sanitizer) json(v jsontext.Value) (any, error) {
	var x any
	if err := json.Unmarshal(v, &x, jsontext.AllowDuplicateNames(false), jsontext.AllowInvalidUTF8(false)); err != nil {
		return nil, err
	}
	return z.value(x), nil
}

// redacted returns the counts without the truncation and collision
// counters, which CONTEXT_OMISSIONS lists as omissions.
func (z *sanitizer) redacted() map[string]int {
	out := map[string]int{}
	for k, n := range z.counts {
		if k != RedactTruncated && k != collided && n > 0 {
			out[k] = n
		}
	}
	return out
}
