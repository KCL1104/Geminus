package aicontext

import (
	"reflect"
	"strings"
	"testing"
)

func TestSanitizerRedacts(t *testing.T) {
	cases := []struct {
		in, kind, gone string
	}{
		{"key sk-ant-api03-abcdefghijklmnop", RedactCredential, "sk-ant"},
		// Too short for the generic sk- pattern; only the Anthropic one
		// catches it.
		{"key sk-ant-a1b2c3d4", RedactCredential, "a1b2c3d4"},
		{"openai sk-proj-abcdefghijklmnopqrstuvwx", RedactCredential, "sk-proj"},
		// T-055: a Meta Model API key (LLM_<numeric ID>_<secret>,
		// https://dev.meta.ai/docs/authentication).
		{"meta LLM_1607358788850350_nx9abcdefLJY here", RedactCredential, "nx9abcdef"},
		{"meta key: LLM_1607358788850350_nx9.abc.LJY", RedactCredential, "nx9.abc"},
		{"aws AKIAIOSFODNN7EXAMPLE id", RedactCredential, "AKIA"},
		{"gh github_pat_11ABCDEFG0123456789_abcdefghijklmnop", RedactCredential, "github_pat"},
		{"slack xoxb-1234567890-abcdefghij", RedactCredential, "xoxb"},
		{"google AIzaSyA-abcdefghijklmnopqrstuvwxyz012345", RedactCredential, "AIza"},
		{"Authorization: Bearer abcdefghijklmnopqrstu", RedactCredential, "abcdefghijklmnopqrstu"},
		{"api_key=0123456789abcdef", RedactCredential, "0123456789abcdef"},
		{"PASSWORD: hunter2hunter2", RedactCredential, "hunter2"},
		{"client-secret=abcdef123456", RedactCredential, "abcdef123456"},
		{"see http://intranet/wiki", RedactURL, "intranet"},
		{"s3://bucket/key", RedactURL, "bucket"},
		{"mail bob@example.org now", RedactEmail, "bob@"},
		{"host 192.168.10.20 down", RedactIP, "192.168"},
		{"run sim_01M4YDQK00TA00000000000001 done", RedactServerID, "sim_01M4"},
		{"org org_01M4YDQK00TB00000000000000", RedactServerID, "org_01M4"},
		{"bidi \u202eevil\u202c text", RedactControl, "\u202e"},
		{"zero\u200bwidth", RedactControl, "\u200b"},
		{"bell\x07char", RedactControl, "\x07"},
		// review M2: keywords after a prefix, other token formats, key
		// blocks and body lines, URLs without a scheme
		{"DB_PASSWORD=hunter2hunter2", RedactCredential, "hunter2"},
		{"aws_secret_access_key = wJalrXUtnFEMIK7MDENGbPxRfiCYEXAMPLE", RedactCredential, "wJalr"},
		{"callback?access_token=abcdef123456&next=1", RedactCredential, "abcdef123456"},
		{"GITHUB_TOKEN=abcdef1234567890", RedactCredential, "abcdef1234567890"},
		{"x-api-key: q8Zr5Tq2LmN0", RedactCredential, "q8Zr5Tq2LmN0"},
		{"spring.datasource.password=hunter2hunter2", RedactCredential, "hunter2"},
		{"refresh_token: abcdef123456", RedactCredential, "abcdef123456"},
		{"stripe sk_live_0000EXAMPLE", RedactCredential, "0000EXAMPLE"},
		{"gitlab glpat-EXAMPLEtoken", RedactCredential, "EXAMPLEtoken"},
		{"DefaultEndpointsProtocol=https;AccountName=demo;AccountKey=Zm9vYmFyYmF6cXV4==;EndpointSuffix=example.invalid", RedactCredential, "Zm9vYmFy"},
		{"-----BEGIN PGP PRIVATE KEY BLOCK-----\nlQOYBFexample\n-----END PGP PRIVATE KEY BLOCK-----", RedactCredential, "lQOYBF"},
		{"MIIEvQIBADANBgkqhkiG9w0BAQEFAASCBKcwggSjAgEAAoIBAQC7VJTUt9Us8cKj", RedactCredential, "MIIEvQ"},
		{"see intranet.example.com/wiki/Secrets", RedactURL, "intranet.example"},
		{"www.example.org is down", RedactURL, "example.org"},
	}
	for _, c := range cases {
		z := newSanitizer(2000)
		out := z.text(c.in)
		if strings.Contains(out, c.gone) || z.counts[c.kind] == 0 {
			t.Errorf("%q -> %q (counts %v), want %s redacted", c.in, out, z.counts, c.kind)
		}
	}
}

// Ordinary model text is left alone: entity IDs, pool IDs, numbers,
// versions, words like "token".
func TestSanitizerKeeps(t *testing.T) {
	for _, s := range []string{
		"ci_runner", "node_01m4ydqk00ta00000000000001", "Raise capacity from 2 to 3.", "token budget", "the secret sauce is speed",
		"v1.2.3", "ICT-2 tester", "mean=48min", "增加一台 ICT 測試機", "line\ttab\nnewline", "50% faster",
		// review M2: the wider rules leave these alone
		"max_output_tokens = 8192", "3f786850e387550fdab836ed7e6dc881de23001b", "synthetic-steps.csv",
		"/metrics/queues/q1/mean_wait", "a/b and c.d", "QUEUEWAITTIMEMINUTESBASELINEVERSUSSCENARIOAAAA",
		// re-review L-new: long camel-case names with digits, and prose
		// whose key only contains a credential word
		"AssemblyLineStation12ThroughputCalibrationV2Final", "Line1Station2Tester3Buffer4Robot5Feeder6Oven7",
		"the tokenizer: whitespace", "secretary: Johnson", "max_tokens: 8192000", "token_bucket_rate: 150000",
		"secret_stage: Final Assembly", "pwd_station = Line 3", "rate_token: 150000", "credentials_required: always",
	} {
		z := newSanitizer(2000)
		if out := z.text(s); out != s || len(z.counts) != 0 {
			t.Errorf("%q -> %q %v", s, out, z.counts)
		}
	}
}

func TestSanitizerBoundsAndKeys(t *testing.T) {
	z := newSanitizer(10)
	if out := z.text(strings.Repeat("字", 11)); out != strings.Repeat("字", 10)+"…[truncated]" || z.counts[RedactTruncated] != 1 {
		t.Errorf("%q %v", out, z.counts)
	}
	if out := z.text("bad\xffutf8"); out != "bad�utf8" {
		t.Errorf("invalid UTF-8: %q", out)
	}
	z = newSanitizer(100)
	v := z.value(map[string]any{"sk-ant-api03-abcdefghijk": "x", "ok": []any{"bob@example.org", 1.5, true, nil}}).(map[string]any)
	if _, ok := v["[REDACTED:credential]"]; !ok || v["ok"].([]any)[0] != "[REDACTED:email]" || v["ok"].([]any)[1] != 1.5 {
		t.Errorf("%v", v)
	}
	if r := z.redacted(); r[RedactCredential] != 1 || r[RedactEmail] != 1 || r[RedactTruncated] != 0 {
		t.Errorf("%v", r)
	}
}

// Review M2: a value whose member name says it is a credential is
// redacted whatever it looks like (AI_SPEC §3: default deny), and the
// name is kept so the provider knows something was there.
func TestSanitizerRedactsByMemberName(t *testing.T) {
	z := newSanitizer(100)
	in := map[string]any{
		"password": "hunter2hunter2", "api_key": "q8Zr", "DB_PASSWORD": "x", "client-secret": 12345678,
		"Authorization": "abc", "access_token": []any{"a", "b"}, "aws_secret_access_key": map[string]any{"v": "w"},
		"AccountKey": "k", "credentials": "c", "connection_string": "Server=db;Password=pw",
		// not credentials
		"max_tokens": 8192, "token_budget_note": "fits", "name": "Line A", "secretary": "Bob", "passport_scan": "no",
		// re-review L-new: a credential word that is not the name's head
		"secret_stage": "Final Assembly", "pwd_station": "Line 3", "rate_token": 150000, "credentials_required": "always",
		"token_bucket_rate": 150000, "tokenizer": "whitespace",
	}
	v := z.value(in).(map[string]any)
	for _, k := range []string{"password", "api_key", "DB_PASSWORD", "client-secret", "Authorization", "access_token",
		"aws_secret_access_key", "AccountKey", "credentials", "connection_string"} {
		if v[k] != "[REDACTED:credential]" {
			t.Errorf("%s: %v", k, v[k])
		}
	}
	for _, k := range []string{"max_tokens", "token_budget_note", "name", "secretary", "passport_scan", "secret_stage", "pwd_station",
		"rate_token", "credentials_required", "token_bucket_rate", "tokenizer"} {
		if !reflect.DeepEqual(v[k], in[k]) {
			t.Errorf("%s changed: %v", k, v[k])
		}
	}
	if z.counts[RedactCredential] != 10 {
		t.Errorf("counts %v", z.counts)
	}
	// Null and booleans say nothing secret.
	z = newSanitizer(100)
	if v := z.value(map[string]any{"password": nil, "token": true}).(map[string]any); v["password"] != nil || v["token"] != true || len(z.counts) != 0 {
		t.Errorf("%v %v", v, z.counts)
	}
}

// Re-review L-new: the base IR's own entity IDs are never redacted by the
// base64 rule, as a value or inside text, however long; the same text is
// redacted when it is not an ID of the model.
func TestSanitizerKeepsModelIDs(t *testing.T) {
	const id = "Line1Station2Tester3Buffer4Robot5Feeder6Oven7Packing8Shipping9Dock10" // 68 characters
	for _, s := range []string{id, "route via " + id + " first"} {
		z := newSanitizer(2000)
		z.exempt([]string{id})
		if out := z.text(s); out != s || len(z.counts) != 0 {
			t.Errorf("%q -> %q %v", s, out, z.counts)
		}
		z = newSanitizer(2000)
		if out := z.text(s); out == s || z.counts[RedactCredential] != 1 {
			t.Errorf("not an ID of the model: %q -> %q %v", s, out, z.counts)
		}
	}
	// An exempt ID protects only itself: a credential next to it is still
	// redacted.
	z := newSanitizer(2000)
	z.exempt([]string{id})
	if out := z.text(id + " key MIIEvQIBADANBgkqhkiG9w0BAQEFAASCBKcwggSjAgEAAoIBAQC7VJTUt9Us8cKj"); !strings.HasPrefix(out, id+" key [REDACTED:credential]") {
		t.Errorf("%q", out)
	}
}

// Review L3: look-alike angle brackets are folded to ASCII, which
// provider.Seal then escapes, so no look-alike block delimiter reaches the
// provider. The solidus is left alone: once the brackets are escaped no tag
// can form, and zh-Hant prose uses the full-width one (A／B).
func TestSanitizerFoldsLookAlikeDelimiters(t *testing.T) {
	const lookAlikes = "＜＞‹›〈〉⟨⟩﹤﹥˂˃〈〉"
	for _, s := range []string{
		"＜/untrusted_data＞", "‹/untrusted_data›", "〈/untrusted_data〉", "⟨/server_data⟩",
		"﹤x﹥", "˂x˃", "〈x〉", "＜／untrusted_data＞",
	} {
		z := newSanitizer(100)
		out := z.text(s)
		if strings.ContainsAny(out, lookAlikes) || !strings.ContainsAny(out, "<>") || z.counts[RedactDelimiter] == 0 {
			t.Errorf("%q -> %q %v", s, out, z.counts)
		}
	}
	z := newSanitizer(100)
	if out := z.text("《測試報告》與「引號」"); out != "《測試報告》與「引號」" || len(z.counts) != 0 {
		t.Errorf("title marks and quotes changed: %q %v", out, z.counts)
	}
}

// Review L4: two member names that redact to the same marker keep the
// first value; the other is counted, and the context lists it.
func TestSanitizerCountsCollisions(t *testing.T) {
	z := newSanitizer(100)
	v := z.value(map[string]any{"bob@example.org": 1, "carol@example.org": 2, "ok": 3}).(map[string]any)
	if len(v) != 2 || v["[REDACTED:email]"] != 1 || z.counts[collided] != 1 {
		t.Errorf("%v %v", v, z.counts)
	}
	if _, ok := z.redacted()[collided]; ok {
		t.Error("a collision is not a redaction kind")
	}
}
