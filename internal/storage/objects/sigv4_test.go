package objects

import (
	"bufio"
	"crypto/sha256"
	"encoding/hex"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/kcl1104/geminus/internal/secret"
)

// The published AWS Signature Version 4 test suite (testdata/sigv4, its
// provenance in SOURCE.txt): for every request the canonical request, the
// string to sign and the Authorization header are exactly the suite's.
// These vectors are the independent check of the signer; the fake S3
// server of s3_test.go then checks the requests the adapter sends.
func TestSigV4Suite(t *testing.T) {
	root := filepath.Join("..", "..", "..", "testdata", "sigv4")
	entries, err := os.ReadDir(root)
	if err != nil {
		t.Fatal(err)
	}
	s := sigv4{accessKey: "AKIDEXAMPLE", secret: secret.Secret("wJalrXUtnFEMI/K7MDENG+bPxRfiCYEXAMPLEKEY"), region: "us-east-1", service: "service"}
	n := 0
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		name := e.Name()
		t.Run(name, func(t *testing.T) {
			read := func(ext string) string {
				b, err := os.ReadFile(filepath.Join(root, name, name+"."+ext))
				if err != nil {
					t.Fatal(err)
				}
				return string(b)
			}
			method, target, headers, body := parseSuiteRequest(t, read("req"))
			path, query, _ := strings.Cut(target, "?")
			sum := sha256.Sum256([]byte(body))
			creq, signed := canonicalRequest(method, path, query, headers, hex.EncodeToString(sum[:]))
			if want := read("creq"); creq != want {
				t.Fatalf("canonical request\n%s\nwant\n%s", creq, want)
			}
			date := headers["x-amz-date"][0]
			scope := date[:8] + "/us-east-1/service/aws4_request"
			sts := stringToSign(date, scope, creq)
			if want := read("sts"); sts != want {
				t.Fatalf("string to sign\n%s\nwant\n%s", sts, want)
			}
			if got, want := s.authorization(scope, signed, s.signature(date[:8], sts)), read("authz"); got != want {
				t.Fatalf("authorization\n%s\nwant\n%s", got, want)
			}
		})
		n++
	}
	if n < 15 {
		t.Fatalf("%d vectors, want the 15 in testdata/sigv4", n)
	}
}

// parseSuiteRequest reads the suite's .req format: a request line,
// "Name:value" headers (a line starting with white space continues the
// previous value), an empty line and the body.
func parseSuiteRequest(t *testing.T, raw string) (method, target string, headers map[string][]string, body string) {
	t.Helper()
	head, body, _ := strings.Cut(raw, "\n\n")
	sc := bufio.NewScanner(strings.NewReader(head))
	if !sc.Scan() {
		t.Fatal("no request line")
	}
	line := sc.Text()
	method, rest, _ := strings.Cut(line, " ")
	target = strings.TrimSuffix(rest, " HTTP/1.1")
	headers = map[string][]string{}
	var last string
	for sc.Scan() {
		l := sc.Text()
		if l != "" && (l[0] == ' ' || l[0] == '\t') {
			vs := headers[last]
			vs[len(vs)-1] += " " + strings.TrimSpace(l)
			continue
		}
		k, v, ok := strings.Cut(l, ":")
		if !ok {
			t.Fatalf("header line %q", l)
		}
		last = strings.ToLower(k)
		headers[last] = append(headers[last], v)
	}
	return method, target, headers, body
}

// The signer covers what an S3 request must sign: the host the transport
// sends, x-amz-date, x-amz-content-sha256 (the payload's hash, so the
// server can check the bytes), If-None-Match and Content-Type; it leaves
// out what a transport may add or change (User-Agent, Content-Length,
// Accept-Encoding).
func TestSignedHeadersOfARequest(t *testing.T) {
	s := sigv4{accessKey: "AKID", secret: secret.Secret("SECRET-SENTINEL"), region: "auto", service: "s3"}
	req, err := http.NewRequest(http.MethodPut, "https://bucket.storage.example/tenant/org/x", nil)
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("If-None-Match", "*")
	req.Header.Set("Content-Type", "application/octet-stream")
	req.Header.Set("User-Agent", "geminus")
	req.ContentLength = 3
	now := time.Date(2026, 10, 26, 1, 2, 3, 0, time.UTC)
	s.sign(req, strings.Repeat("a", 64), now)
	auth := req.Header.Get("Authorization")
	want := "AWS4-HMAC-SHA256 Credential=AKID/20261026/auto/s3/aws4_request, SignedHeaders=content-type;host;if-none-match;x-amz-content-sha256;x-amz-date, Signature="
	if !strings.HasPrefix(auth, want) || len(auth) != len(want)+64 {
		t.Errorf("Authorization %q", auth)
	}
	if req.Header.Get("X-Amz-Date") != "20261026T010203Z" || req.Header.Get("X-Amz-Content-Sha256") != strings.Repeat("a", 64) {
		t.Errorf("headers %v", req.Header)
	}
	if strings.Contains(auth, "SECRET-SENTINEL") {
		t.Error("the secret is in the header")
	}
	// The same request signed again at the same time is signed the same:
	// nothing random, nothing from the environment.
	again := req.Clone(req.Context())
	s.sign(again, strings.Repeat("a", 64), now)
	if again.Header.Get("Authorization") != auth {
		t.Error("signing is not deterministic")
	}
}

// Keys are the server's (NewKey: ASCII letters, digits, '_', '-', '.',
// '/'), all unreserved, so the canonical URI is the path itself; anything
// else is percent-encoded once per byte, as S3 expects (no double encoding).
func TestCanonicalURI(t *testing.T) {
	for in, want := range map[string]string{
		"":                       "/",
		"/":                      "/",
		"/bucket/tenant/org_1/x": "/bucket/tenant/org_1/x",
		"/a b":                   "/a%20b",
		"/ሴ":                     "/%E1%88%B4",
		"/a+b=c":                 "/a%2Bb%3Dc",
	} {
		if got := canonicalURI(in); got != want {
			t.Errorf("%q: %q, want %q", in, got, want)
		}
	}
}
