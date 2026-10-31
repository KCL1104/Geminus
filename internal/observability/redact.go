package observability

import (
	"fmt"
	"log/slog"
	"reflect"
	"regexp"
	"strings"
	"unicode"
	"unicode/utf8"
)

// Redacted replaces a value the privacy rules do not let through, as
// internal/secret prints a credential.
const Redacted = "[redacted]"

// InvalidKey replaces a key that is not a plain lowercase name: a key
// built from data is data.
const InvalidKey = "invalid_key"

// Bounds of the free-text kinds, in runes (D-094 (c)): longer text is cut
// and ends with "…".
const (
	MaxText    = 1000
	MaxStack   = 8000
	MaxPointer = 256
	// preCut bounds the text the patterns scan: no kind keeps more than
	// MaxStack runes, so a credential that starts past it is cut away.
	preCut = 64 << 10
)

// Kind is the shape a field's value must have to be logged (D-094 (a)).
type Kind uint8

const (
	// KindID is a D-001 server ID (prefix, "_", ULID).
	KindID Kind = iota + 1
	// KindCode is an UPPER_SNAKE code (error, failure, issue code).
	KindCode
	// KindToken is a lowercase word: a status, an outcome, a job type.
	KindToken
	// KindName is an identifier-like name that is not a credential: an
	// operation ID, a worker ID, a provider, model or credential label.
	KindName
	// KindGoType is a Go type name (%T of an error).
	KindGoType
	// KindMethod is an HTTP method.
	KindMethod
	// KindRoute is a ServeMux pattern ("POST /v1/models/{id}") or
	// "unmatched": never the path the client sent.
	KindRoute
	// KindHash is "sha256:" and 64 hex digits.
	KindHash
	// KindNumber is a number, a boolean, a duration or a time.
	KindNumber
	// KindText is the server's own words: an error, a panic, a listen
	// address. Sanitized and cut at MaxText.
	KindText
	// KindStack is a stack trace: sanitized, lines kept, cut at MaxStack.
	KindStack
	// KindPointer is a JSON Pointer the server's validator produced:
	// sanitized, cut at MaxPointer.
	KindPointer
)

// fields is the registry of log keys (D-093): a key not listed here is
// logged as Redacted, so a new field is a reviewed change to this list.
// slog's own time, level and msg are not attributes.
var fields = map[string]Kind{
	// correlation (D-093 (b))
	"component":        KindToken,
	"request_id":       KindID,
	"organization_id":  KindID,
	"user_id":          KindID,
	"job_id":           KindID,
	"job_type":         KindToken,
	"attempt":          KindNumber,
	"run_id":           KindID,
	"proposal_id":      KindID,
	"import_id":        KindID,
	"analysis_id":      KindID,
	"model_id":         KindID,
	"model_version_id": KindID,
	"scenario_id":      KindID,
	"worker_id":        KindName,
	// the access log (D-093 (c))
	"method":         KindMethod,
	"route":          KindRoute,
	"http_status":    KindNumber,
	"duration_ms":    KindNumber,
	"response_bytes": KindNumber,
	"error_code":     KindCode,
	// outcomes and counts
	"status":        KindToken,
	"outcome":       KindToken,
	"code":          KindCode,
	"retried":       KindNumber,
	"retryable":     KindNumber,
	"requeued":      KindNumber,
	"failed":        KindNumber,
	"organizations": KindNumber,
	"events":        KindNumber,
	"replications":  KindNumber,
	"rows":          KindNumber,
	"invalid_rows":  KindNumber,
	"columns":       KindNumber,
	"size_bytes":    KindNumber,
	"result_digest": KindHash,
	"operation_id":  KindName,
	"ai_provider":   KindToken,
	// the AI provider call (D-095): labels and counts, never the prompt or
	// the answer; H-057: which call of the attempt it was (1, or 2 for the
	// one repair)
	"call_attempt":        KindNumber,
	"provider":            KindName,
	"model":               KindName,
	"credential_label":    KindName,
	"role":                KindToken,
	"latency_ms":          KindNumber,
	"input_tokens":        KindNumber,
	"output_tokens":       KindNumber,
	"provider_request_id": KindName,
	// the spend of a live AI call (T-053): amounts, the reservation, the
	// limit that refused it
	"cost_nano_usd":        KindNumber,
	"spend_reservation_id": KindID,
	"spend_unsettled":      KindNumber,
	"limit":                KindToken,
	// T-053 review round 1: the geo a live answer was priced at (global,
	// us, unknown: this build's word, never the provider's text) and a
	// settlement above its reservation (M2, M3, L1)
	"inference_geo":          KindToken,
	"spend_over_reservation": KindNumber,
	// T-053 schema follow-up: the HTTP status and the error type (the
	// adapter's list of the API's types, or other) of a call the provider
	// answered with an error status; never its message
	"provider_status":     KindNumber,
	"provider_error_type": KindToken,
	// T-056: how many boxes a segmenter found in a photo (never the boxes,
	// their labels or the image)
	"boxes": KindNumber,
	// the server's own text
	"error": KindText,
	"panic": KindText,
	"addr":  KindText,
	"stack": KindStack,
	"path":  KindPointer,
	"type":  KindGoType,
}

var (
	idShape     = regexp.MustCompile(`^[a-z]{2,4}_[0-7][0-9A-HJKMNP-TV-Z]{25}$`)
	codeShape   = regexp.MustCompile(`^[A-Z][A-Z0-9_]{0,63}$`)
	tokenShape  = regexp.MustCompile(`^[a-z][a-z0-9_]{0,63}$`)
	nameShape   = regexp.MustCompile(`^[A-Za-z][A-Za-z0-9_.:/+-]{0,127}$`)
	goTypeShape = regexp.MustCompile(`^[*\[\]A-Za-z0-9_./-]{1,200}$`)
	routeShape  = regexp.MustCompile(`^(?:(?:GET|HEAD|POST|PUT|PATCH|DELETE|OPTIONS|CONNECT|TRACE) /[A-Za-z0-9_./{}$-]{0,200}|unmatched)$`)
	hashShape   = regexp.MustCompile(`^sha256:[0-9a-f]{64}$`)
	keyShape    = regexp.MustCompile(`^[a-z][a-z0-9_]{0,63}$`)
	methods     = map[string]bool{"GET": true, "HEAD": true, "POST": true, "PUT": true, "PATCH": true, "DELETE": true,
		"OPTIONS": true, "CONNECT": true, "TRACE": true, "OTHER": true}
)

// filter is attr as the privacy rules let it be logged (D-094).
func filter(a slog.Attr) slog.Attr {
	if a.Equal(slog.Attr{}) {
		return a
	}
	key := a.Key
	kind, ok := fields[key]
	if !keyShape.MatchString(key) {
		key, ok = InvalidKey, false
	}
	if !ok {
		return slog.String(key, Redacted)
	}
	// Resolve calls a LogValuer's LogValue; when it panics, slog recovers
	// and resolves to an error value (sanitized text under a text key,
	// Redacted under any other).
	return slog.Attr{Key: key, Value: value(kind, a.Value.Resolve())}
}

func value(kind Kind, v slog.Value) slog.Value {
	switch kind {
	case KindNumber:
		switch v.Kind() {
		case slog.KindInt64, slog.KindUint64, slog.KindFloat64, slog.KindBool, slog.KindDuration, slog.KindTime:
			return v
		}
		return slog.StringValue(Redacted)
	case KindText, KindStack, KindPointer:
		switch v.Kind() {
		case slog.KindString:
			return slog.StringValue(sanitize(v.String(), kind))
		case slog.KindInt64, slog.KindUint64, slog.KindFloat64, slog.KindBool, slog.KindDuration, slog.KindTime:
			return v
		case slog.KindAny:
			if err, ok := v.Any().(error); ok {
				return slog.StringValue(sanitize(errorString(err), kind))
			}
		}
		return slog.StringValue(Redacted)
	}
	// An empty string says nothing, so it fits any shape.
	if v.Kind() != slog.KindString || (v.String() != "" && !fits(kind, v.String())) {
		return slog.StringValue(Redacted)
	}
	return v
}

// Fits reports whether s may be recorded as a value of kind: it has the
// kind's shape (an empty string says nothing and always fits). The free-
// text and number kinds are not shapes: Fits is false for them. Trace and
// metric attributes use the same shapes (D-094 (f)).
func Fits(kind Kind, s string) bool { return s == "" || fits(kind, s) }

// fits reports whether s has kind's shape.
func fits(kind Kind, s string) bool {
	switch kind {
	case KindID:
		return idShape.MatchString(s)
	case KindCode:
		return codeShape.MatchString(s)
	case KindToken:
		return tokenShape.MatchString(s)
	case KindName:
		return nameShape.MatchString(s) && !Sensitive(s)
	case KindGoType:
		return goTypeShape.MatchString(s) && !Sensitive(s)
	case KindMethod:
		return methods[s]
	case KindRoute:
		return routeShape.MatchString(s)
	case KindHash:
		return hashShape.MatchString(s)
	}
	return false
}

// errorString is err's text, or Redacted when its method panics (a
// handler's broken error type must not crash the logger).
func errorString(err error) (s string) {
	defer func() {
		if recover() != nil {
			s = Redacted
		}
	}()
	return err.Error()
}

// ErrorText is err's text as a log line keeps it ("" for nil).
func ErrorText(err error) string {
	if err == nil {
		return ""
	}
	s := errorString(err)
	if s == Redacted {
		return s
	}
	return sanitize(s, KindText)
}

// Text is s as a log line keeps free text (KindText).
func Text(s string) string { return sanitize(s, KindText) }

// credentials are the credential shapes removed from free text, after
// internal/ai/aicontext's (T-024) list without what a log needs to keep:
// server IDs, host names, file names and IP addresses stay.
var credentials = []*regexp.Regexp{
	regexp.MustCompile(`-----BEGIN [A-Z0-9 ]*PRIVATE KEY(?: BLOCK)?-----(?s:.*?)(?:-----END [A-Z0-9 ]*PRIVATE KEY(?: BLOCK)?-----|$)`),
	regexp.MustCompile(`\bsk-ant-[A-Za-z0-9_-]{8,}`),
	regexp.MustCompile(`\bLLM_[0-9]+_[^\s"'<>,;]{6,}`), // a Meta Model API key (T-055)
	regexp.MustCompile(`\b(?:sk|pk|rk)-[A-Za-z0-9_-]{16,}`),
	regexp.MustCompile(`\b(?:sk|pk|rk)_(?:live|test)_[A-Za-z0-9]{8,}`),
	regexp.MustCompile(`\b(?:AKIA|ASIA)[0-9A-Z]{16}\b`),
	regexp.MustCompile(`\b(?:gh[pousr]_[A-Za-z0-9]{20,}|github_pat_[A-Za-z0-9_]{20,})\b`),
	regexp.MustCompile(`\bglpat-[A-Za-z0-9_-]{8,}`),
	regexp.MustCompile(`\bxox[abprs]-[A-Za-z0-9-]{10,}`),
	regexp.MustCompile(`\bAIza[0-9A-Za-z_-]{30,}`),
	regexp.MustCompile(`\beyJ[A-Za-z0-9_-]{6,}\.[A-Za-z0-9_-]{6,}\.[A-Za-z0-9_-]{6,}`),
	regexp.MustCompile(`(?i)\b(?:bearer|basic)\s+[A-Za-z0-9._~+/=-]{12,}`),
}

// keyValue is a key=value or key: value whose key ends with a credential
// word (DB_PASSWORD=, ?access_token=, AccountKey=, session=): the key
// stays, the value goes.
var keyValue = regexp.MustCompile(`(?i)(\b(?:(?:[A-Za-z0-9.]+[_.-])*?(?:password|passwd|passphrase|pwd|secrets?|credentials?|` +
	`(?:api|access|account|private|secret)[_-]?key|client[_-]?secret|connection[_-]?string|session(?:[_-]?id)?|sid|jsessionid|phpsessid|signature|sig|` +
	`(?:access|refresh|id|auth|api|bearer|session|oauth|personal|secret|private|github|gh|gitlab|slack|npm|pypi|bot|csrf|xsrf|jwt)[_.-]?token)|` +
	`token)\s*[:=]\s*)[^\s,;"'&]{6,}`)

var (
	// headerLine is a credential-carrying HTTP header written out.
	headerLine = regexp.MustCompile(`(?i)\b((?:set-)?cookie|(?:proxy-)?authorization|x-api-key)(\s*:\s*)[^\r\n]*`)
	// userinfo is the user:password@ of a URL or connection string.
	userinfo = regexp.MustCompile(`(?i)\b([a-z][a-z0-9+.-]{1,15}://)[^\s/@"'<>]+@`)
	email    = regexp.MustCompile(`\b[A-Za-z0-9._%+-]+@[A-Za-z0-9-]+(?:\.[A-Za-z0-9-]+)*\.[A-Za-z]{2,}\b`)
	// longBase64 is a token, key or signature body: a long run of base64
	// with upper- and lowercase letters and digits (checked in base64Like).
	longBase64 = regexp.MustCompile(`[A-Za-z0-9+/]{40,}={0,2}`)
	// quoted is a double-quoted span, Go's %q or PostgreSQL's: where a
	// server message names a value (D-094 (c)). A quote that is never
	// closed (a value that holds a quote, text cut short) runs to the end:
	// what follows it may be the value.
	quoted = regexp.MustCompile(`"(?:[^"\\]|\\.)*(?:"|\\?$)`)
	// backticked, curly and curlySingle are the other quotes a message can
	// name a value in (review L2), closed or running to the end; single
	// quotes are singleQuoted's.
	backticked  = regexp.MustCompile("`[^`]*(?:`|$)")
	curly       = regexp.MustCompile("\u201c[^\u201d]*(?:\u201d|$)")
	curlySingle = regexp.MustCompile("\u2018[^\u2019]*(?:\u2019|$)")
)

// singleQuoted is s with every span 'like this' replaced (review L2). A
// single quote is an apostrophe inside a word, so only a quote that starts
// a word (at the start or after a character that is neither a letter nor a
// digit, and before one that is not a space) opens a span, and only a quote
// that ends a word (at the end or before a character that is neither a
// letter nor a digit) closes it: contractions and possessives (don't, the
// job's, the workers') stay. A span that is never closed runs to the end.
func singleQuoted(s string) string {
	if !strings.Contains(s, "'") {
		return s
	}
	word := func(r rune) bool { return unicode.IsLetter(r) || unicode.IsDigit(r) }
	rs := []rune(s)
	var b strings.Builder
	for i := 0; i < len(rs); i++ {
		if rs[i] != '\'' || i > 0 && (word(rs[i-1]) || rs[i-1] == '\'') || i+1 == len(rs) || unicode.IsSpace(rs[i+1]) {
			b.WriteRune(rs[i])
			continue
		}
		j := i + 1
		for j < len(rs) && !(rs[j] == '\'' && (j+1 == len(rs) || !word(rs[j+1]))) {
			j++
		}
		b.WriteString("'" + Redacted + "'")
		i = j
	}
	return b.String()
}

func base64Like(m string) bool {
	return strings.ContainsAny(m, "ABCDEFGHIJKLMNOPQRSTUVWXYZ") && strings.ContainsAny(m, "abcdefghijklmnopqrstuvwxyz") &&
		strings.ContainsAny(m, "0123456789") && (strings.ContainsAny(m, "+/=") || len(m) >= 64)
}

// Sensitive reports whether s holds a credential, a URL's user
// information, a credential header or an e-mail address.
func Sensitive(s string) bool {
	for _, re := range credentials {
		if re.MatchString(s) {
			return true
		}
	}
	if keyValue.MatchString(s) || headerLine.MatchString(s) || userinfo.MatchString(s) || email.MatchString(s) {
		return true
	}
	for _, m := range longBase64.FindAllString(s, -1) {
		if base64Like(m) {
			return true
		}
	}
	return false
}

// sanitize is free text as a log keeps it (D-094 (c)): valid UTF-8;
// format characters (bidirectional overrides, zero-width characters)
// removed; credentials, a URL's user information, credential headers,
// e-mail addresses and quoted values (double, single, backtick and
// typographic quotes) replaced by Redacted; control characters and line
// separators replaced by a space, except a stack's line breaks and tabs;
// cut to the kind's bound.
func sanitize(s string, kind Kind) string {
	if s == "" {
		return s
	}
	if len(s) > preCut {
		s = s[:preCut]
	}
	s = strings.ToValidUTF8(s, "�")
	// Format characters (zero-width, bidirectional overrides) are invisible:
	// they go first, so none can split a credential the patterns would see.
	s = strings.Map(func(r rune) rune {
		if unicode.Is(unicode.Cf, r) {
			return -1
		}
		return r
	}, s)
	for _, re := range credentials {
		s = re.ReplaceAllString(s, Redacted)
	}
	s = keyValue.ReplaceAllString(s, "${1}"+Redacted)
	s = headerLine.ReplaceAllString(s, "${1}${2}"+Redacted)
	s = userinfo.ReplaceAllString(s, "${1}"+Redacted+"@")
	s = email.ReplaceAllString(s, Redacted)
	s = longBase64.ReplaceAllStringFunc(s, func(m string) string {
		if base64Like(m) {
			return Redacted
		}
		return m
	})
	s = quoted.ReplaceAllString(s, `"`+Redacted+`"`)
	s = backticked.ReplaceAllString(s, "`"+Redacted+"`")
	s = curly.ReplaceAllString(s, "\u201c"+Redacted+"\u201d")
	s = curlySingle.ReplaceAllString(s, "\u2018"+Redacted+"\u2019")
	s = singleQuoted(s)
	lines := kind == KindStack
	s = strings.Map(func(r rune) rune {
		switch {
		case lines && (r == '\n' || r == '\t'):
			return r
		case unicode.IsControl(r) || r == '\u2028' || r == '\u2029':
			return ' '
		}
		return r
	}, s)
	max := MaxText
	switch kind {
	case KindStack:
		max = MaxStack
	case KindPointer:
		max = MaxPointer
	}
	if utf8.RuneCountInString(s) > max {
		s = string([]rune(s)[:max]) + "…"
	}
	return s
}

// PanicText is what a log keeps of a recovered panic's value: the message
// of an error the Go runtime raised (RuntimeError), otherwise only the
// value's type, since the value may hold the data the code was working on
// (D-094 (d)).
func PanicText(v any) string {
	if err, ok := RuntimeError(v); ok {
		return "panic: " + Text(errorString(err))
	}
	return fmt.Sprintf("panic with a value of type %T", v)
}

// RuntimeError is v as an error when the Go runtime raised it: an error of
// a type declared in package runtime itself (an index out of range, a nil
// map assignment, a failed type assertion), whose message the runtime
// wrote. A value of any other type is not, even one with a RuntimeError
// method or of a package that is also named runtime: its message is its
// author's and may hold the data the code was working on (review L3).
func RuntimeError(v any) (error, bool) {
	err, ok := v.(error)
	if !ok {
		return nil, false
	}
	t := reflect.TypeOf(v)
	for t.Kind() == reflect.Pointer {
		t = t.Elem()
	}
	return err, t.PkgPath() == "runtime"
}
