package objects

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"net/http"
	"net/url"
	"sort"
	"strings"
	"time"

	"github.com/kcl1104/geminus/internal/secret"
)

// sigv4 signs requests with AWS Signature Version 4 in the Authorization
// header (the form every S3-compatible service accepts). The payload is
// signed by its SHA-256, computed before the request is sent, so the server
// can refuse bytes that are not the ones signed. It is checked against the
// published test suite (testdata/sigv4, sigv4_test.go).
type sigv4 struct {
	accessKey string
	secret    secret.Secret
	region    string
	service   string
}

const sigv4Algorithm = "AWS4-HMAC-SHA256"

// sign sets X-Amz-Date, X-Amz-Content-Sha256 and Authorization on req. It
// signs the host the transport will send and every Content-Type,
// If-None-Match and X-Amz-* header already set; nothing a transport may add
// or change (User-Agent, Content-Length, Accept-Encoding) is signed.
func (s sigv4) sign(req *http.Request, payloadHash string, now time.Time) {
	t := now.UTC()
	amzDate := t.Format("20060102T150405Z")
	day := amzDate[:8]
	req.Header.Set("X-Amz-Date", amzDate)
	req.Header.Set("X-Amz-Content-Sha256", payloadHash)
	host := req.Host
	if host == "" {
		host = req.URL.Host
	}
	headers := map[string][]string{"host": {host}}
	for name, values := range req.Header {
		n := strings.ToLower(name)
		if n == "content-type" || n == "if-none-match" || strings.HasPrefix(n, "x-amz-") {
			headers[n] = append(headers[n], values...)
		}
	}
	creq, signed := canonicalRequest(req.Method, req.URL.EscapedPath(), req.URL.RawQuery, headers, payloadHash)
	scope := day + "/" + s.region + "/" + s.service + "/aws4_request"
	req.Header.Set("Authorization", s.authorization(scope, signed, s.signature(day, stringToSign(amzDate, scope, creq))))
}

// authorization is the Authorization header's value.
func (s sigv4) authorization(scope, signedHeaders, signature string) string {
	return sigv4Algorithm + " Credential=" + s.accessKey + "/" + scope + ", SignedHeaders=" + signedHeaders + ", Signature=" + signature
}

// signature signs the string to sign with the day's signing key.
func (s sigv4) signature(day, sts string) string {
	k := mac([]byte("AWS4"+s.secret.Reveal()), day)
	k = mac(k, s.region)
	k = mac(k, s.service)
	k = mac(k, "aws4_request")
	return hex.EncodeToString(mac(k, sts))
}

func mac(key []byte, data string) []byte {
	h := hmac.New(sha256.New, key)
	h.Write([]byte(data))
	return h.Sum(nil)
}

// stringToSign is SigV4's string to sign for a canonical request.
func stringToSign(amzDate, scope, creq string) string {
	sum := sha256.Sum256([]byte(creq))
	return sigv4Algorithm + "\n" + amzDate + "\n" + scope + "\n" + hex.EncodeToString(sum[:])
}

// canonicalRequest is SigV4's canonical request and its signed-headers
// list. path may be percent-encoded already (it is decoded first, then
// encoded once, as S3 expects); rawQuery's names and values are decoded,
// encoded again and sorted; each header's values are trimmed, inner runs of
// white space collapsed and joined with commas, and the headers sorted by
// their lowercase names.
func canonicalRequest(method, path, rawQuery string, headers map[string][]string, payloadHash string) (string, string) {
	names := make([]string, 0, len(headers))
	for n := range headers {
		names = append(names, strings.ToLower(n))
	}
	sort.Strings(names)
	var ch strings.Builder
	for _, n := range names {
		values := make([]string, 0, len(headers[n]))
		for _, v := range headers[n] {
			values = append(values, strings.Join(strings.Fields(v), " "))
		}
		ch.WriteString(n + ":" + strings.Join(values, ",") + "\n")
	}
	signed := strings.Join(names, ";")
	return method + "\n" + canonicalURI(path) + "\n" + canonicalQuery(rawQuery) + "\n" + ch.String() + "\n" + signed + "\n" + payloadHash, signed
}

// canonicalURI encodes path once per byte outside RFC 3986's unreserved
// set, keeping "/"; an empty path is "/".
func canonicalURI(path string) string {
	if p, err := url.PathUnescape(path); err == nil {
		path = p
	}
	if path == "" {
		return "/"
	}
	return uriEncode(path, false)
}

// canonicalQuery sorts the query's encoded names, then values.
func canonicalQuery(raw string) string {
	if raw == "" {
		return ""
	}
	type pair struct{ k, v string }
	var pairs []pair
	for part := range strings.SplitSeq(raw, "&") {
		if part == "" {
			continue
		}
		k, v, _ := strings.Cut(part, "=")
		if d, err := url.QueryUnescape(k); err == nil {
			k = d
		}
		if d, err := url.QueryUnescape(v); err == nil {
			v = d
		}
		pairs = append(pairs, pair{uriEncode(k, true), uriEncode(v, true)})
	}
	sort.Slice(pairs, func(i, j int) bool {
		if pairs[i].k != pairs[j].k {
			return pairs[i].k < pairs[j].k
		}
		return pairs[i].v < pairs[j].v
	})
	out := make([]string, len(pairs))
	for i, p := range pairs {
		out[i] = p.k + "=" + p.v
	}
	return strings.Join(out, "&")
}

// uriEncode percent-encodes every byte outside A-Z, a-z, 0-9, '-', '.',
// '_', '~' (and '/' unless slash), in upper-case hex.
func uriEncode(s string, slash bool) string {
	const hexDigits = "0123456789ABCDEF"
	var b strings.Builder
	for i := 0; i < len(s); i++ {
		c := s[i]
		if 'A' <= c && c <= 'Z' || 'a' <= c && c <= 'z' || '0' <= c && c <= '9' || c == '-' || c == '.' || c == '_' || c == '~' || c == '/' && !slash {
			b.WriteByte(c)
			continue
		}
		b.WriteByte('%')
		b.WriteByte(hexDigits[c>>4])
		b.WriteByte(hexDigits[c&15])
	}
	return b.String()
}
