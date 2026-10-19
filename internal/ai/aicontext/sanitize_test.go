package aicontext

import (
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
