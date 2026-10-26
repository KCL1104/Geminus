package objects

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/kcl1104/geminus/internal/ids"
	"github.com/kcl1104/geminus/internal/secret"
)

// fakeS3 is an in-process S3-compatible server for the adapter's tests (no
// network): one bucket, path-style or virtual-hosted addressing, SigV4
// verified on every request (with the signer the published suite checks,
// sigv4_test.go), the payload's x-amz-content-sha256 checked against the
// bytes received, and conditional PUT (If-None-Match: *) as S3, Railway's
// buckets (Tigris) and R2 document it: 412 when the key exists.
type fakeS3 struct {
	t         *testing.T
	bucket    string
	pathStyle bool
	verify    sigv4

	mu       sync.Mutex
	objects  map[string][]byte
	requests []string
	// status, when set, answers every request with it (and code), after
	// the signature is checked.
	status int
	code   string
	// redirect, when set, answers every request with a 307 to it.
	redirect string
}

func newFakeS3(t *testing.T, pathStyle bool) (*fakeS3, *httptest.Server) {
	t.Helper()
	f := &fakeS3{t: t, bucket: "geminus-test-1a2b3c", pathStyle: pathStyle, objects: map[string][]byte{},
		verify: sigv4{accessKey: "AKIDTEST", secret: secret.Secret("SECRET-SENTINEL-0123456789"), region: "auto", service: "s3"}}
	srv := httptest.NewServer(f)
	t.Cleanup(srv.Close)
	return f, srv
}

func (f *fakeS3) fail(w http.ResponseWriter, status int, code string) {
	w.Header().Set("Content-Type", "application/xml")
	w.WriteHeader(status)
	if status >= 400 {
		fmt.Fprintf(w, `<?xml version="1.0" encoding="UTF-8"?><Error><Code>%s</Code><Message>fake</Message></Error>`, code)
	}
}

func (f *fakeS3) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	f.mu.Lock()
	f.requests = append(f.requests, r.Method+" "+r.Host+r.URL.Path)
	status, code, redirect := f.status, f.code, f.redirect
	f.mu.Unlock()
	if redirect != "" {
		http.Redirect(w, r, redirect, http.StatusTemporaryRedirect)
		return
	}
	// Addressing: the bucket in the path or in the host.
	var key string
	host, _, _ := net.SplitHostPort(r.Host)
	if f.pathStyle {
		rest, ok := strings.CutPrefix(r.URL.Path, "/"+f.bucket+"/")
		if !ok || host != "127.0.0.1" {
			f.fail(w, 400, "InvalidBucketName")
			return
		}
		key = rest
	} else {
		if host != f.bucket+".localhost" {
			f.fail(w, 400, "InvalidBucketName")
			return
		}
		key = strings.TrimPrefix(r.URL.Path, "/")
	}
	// SigV4, as the service checks it.
	auth := r.Header.Get("Authorization")
	amzDate := r.Header.Get("X-Amz-Date")
	payload := r.Header.Get("X-Amz-Content-Sha256")
	cred, rest, ok := strings.Cut(strings.TrimPrefix(auth, sigv4Algorithm+" Credential="), ", SignedHeaders=")
	signedList, sig, ok2 := strings.Cut(rest, ", Signature=")
	when, err := time.Parse("20060102T150405Z", amzDate)
	if !strings.HasPrefix(auth, sigv4Algorithm+" ") || !ok || !ok2 || err != nil || time.Since(when).Abs() > 15*time.Minute {
		f.fail(w, 403, "AccessDenied")
		return
	}
	scope := amzDate[:8] + "/auto/s3/aws4_request"
	if cred != f.verify.accessKey+"/"+scope {
		f.fail(w, 403, "InvalidAccessKeyId")
		return
	}
	headers := map[string][]string{}
	for n := range strings.SplitSeq(signedList, ";") {
		if n == "host" {
			headers[n] = []string{r.Host}
		} else {
			headers[n] = r.Header.Values(n)
		}
	}
	for _, must := range []string{"host", "x-amz-date", "x-amz-content-sha256"} {
		if _, ok := headers[must]; !ok {
			f.fail(w, 403, "AccessDenied")
			return
		}
	}
	creq, _ := canonicalRequest(r.Method, r.URL.EscapedPath(), r.URL.RawQuery, headers, payload)
	if f.verify.signature(amzDate[:8], stringToSign(amzDate, scope, creq)) != sig {
		f.fail(w, 403, "SignatureDoesNotMatch")
		return
	}
	body, err := io.ReadAll(r.Body)
	if err != nil {
		return
	}
	sum := sha256.Sum256(body)
	if hex.EncodeToString(sum[:]) != payload {
		f.fail(w, 400, "XAmzContentSHA256Mismatch")
		return
	}
	if status != 0 {
		f.fail(w, status, code)
		return
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	obj, exists := f.objects[key]
	switch r.Method {
	case http.MethodPut:
		if r.Header.Get("If-None-Match") != "*" {
			// The adapter must never write without the condition.
			f.t.Errorf("PUT %s without If-None-Match: *", key)
			f.fail(w, 400, "InvalidRequest")
			return
		}
		if exists {
			f.fail(w, 412, "PreconditionFailed")
			return
		}
		f.objects[key] = body
		w.Header().Set("ETag", `"`+hex.EncodeToString(sum[:16])+`"`)
		w.WriteHeader(200)
	case http.MethodGet, http.MethodHead:
		if !exists {
			f.fail(w, 404, "NoSuchKey")
			return
		}
		w.Header().Set("Content-Length", fmt.Sprint(len(obj)))
		w.WriteHeader(200)
		if r.Method == http.MethodGet {
			_, _ = w.Write(obj)
		}
	case http.MethodDelete:
		delete(f.objects, key)
		w.WriteHeader(204)
	default:
		f.fail(w, 405, "MethodNotAllowed")
	}
}

// set makes the server answer every request with status and code (0: normally).
func (f *fakeS3) set(status int, code string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.status, f.code = status, code
}

// object is the stored object of key.
func (f *fakeS3) object(key string) ([]byte, bool) {
	f.mu.Lock()
	defer f.mu.Unlock()
	b, ok := f.objects[key]
	return b, ok
}

// count is how many requests began with prefix ("PUT", "GET <host>...").
func (f *fakeS3) count(prefix string) int {
	f.mu.Lock()
	defer f.mu.Unlock()
	n := 0
	for _, r := range f.requests {
		if strings.HasPrefix(r, prefix) {
			n++
		}
	}
	return n
}

// testS3 is an adapter for the fake server. Virtual-hosted requests go to
// <bucket>.localhost, which the test client dials at the server's address
// (no DNS); the endpoint is plain http on loopback, which NewS3 accepts
// only there.
func testS3(t *testing.T, f *fakeS3, srv *httptest.Server) (*S3, string) {
	t.Helper()
	_, port, _ := net.SplitHostPort(srv.Listener.Addr().String())
	endpoint := "http://127.0.0.1:" + port
	if !f.pathStyle {
		endpoint = "http://localhost:" + port
	}
	spool := t.TempDir()
	s, err := NewS3(S3Config{Endpoint: endpoint, Bucket: f.bucket, Region: "auto", AccessKeyID: f.verify.accessKey,
		SecretAccessKey: f.verify.secret, PathStyle: f.pathStyle, SpoolDir: spool})
	if err != nil {
		t.Fatal(err)
	}
	// Only the dialer changes: the client (no redirects) is NewS3's.
	addr := srv.Listener.Addr().String()
	tr := s3Transport()
	tr.Proxy = nil
	tr.DialContext = func(ctx context.Context, network, _ string) (net.Conn, error) {
		return (&net.Dialer{}).DialContext(ctx, network, addr)
	}
	s.client.Transport = tr
	return s, spool
}

// cancellingReader yields a chunk, then cancels its context.
type cancellingReader struct {
	cancel context.CancelFunc
	n      int
}

func (c *cancellingReader) Read(p []byte) (int, error) {
	c.n++
	if c.n > 1 {
		c.cancel()
	}
	return copy(p, "chunk"), nil
}

func digest(b []byte) string {
	h := sha256.Sum256(b)
	return "sha256:" + hex.EncodeToString(h[:])
}

func emptyDir(t *testing.T, dir string) {
	t.Helper()
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range entries {
		t.Errorf("a spooled file stayed: %s", e.Name())
	}
}

// D-044 / T-039: the S3-compatible adapter keeps the Store contract of the
// filesystem adapter (objects_test.go's TestPutGet), for path-style and
// virtual-hosted buckets: Put reports the bytes' SHA-256 and size, Get reads
// them back, an object is never replaced, a missing one is ErrNotFound, an
// object over the limit is never sent and leaves nothing, and keys the
// server does not make never reach the bucket.
func TestS3PutGet(t *testing.T) {
	for _, pathStyle := range []bool{false, true} {
		t.Run(fmt.Sprintf("path-style=%v", pathStyle), func(t *testing.T) {
			ctx := context.Background()
			f, srv := newFakeS3(t, pathStyle)
			s, spool := testS3(t, f, srv)
			org := ids.New("org")
			key, _ := NewKey(org, SimulationResults, ids.New("sim"), ids.New("art")+".json")
			data := []byte(`{"format":"geminus.trace.v1"}`)
			obj, err := s.Put(ctx, key, bytes.NewReader(data), int64(len(data)))
			if err != nil || obj != (Object{Key: key, SHA256: digest(data), Size: int64(len(data))}) {
				t.Fatalf("Put: %+v %v", obj, err)
			}
			rc, err := s.Get(ctx, key)
			if err != nil {
				t.Fatal(err)
			}
			got, err := io.ReadAll(rc)
			rc.Close()
			if err != nil || !bytes.Equal(got, data) {
				t.Errorf("Get: %q %v", got, err)
			}
			sent := f.count("PUT")
			if _, err := s.Put(ctx, key, strings.NewReader("other"), 100); !errors.Is(err, ErrExists) {
				t.Errorf("a second Put: %v", err)
			}
			if f.count("PUT") != sent {
				t.Error("a second Put sent its bytes although HEAD found the object")
			}
			if b, _ := f.object(key); !bytes.Equal(b, data) {
				t.Errorf("the object was replaced: %q", b)
			}
			missing, _ := NewKey(org, SimulationResults, "nothing.json")
			if _, err := s.Get(ctx, missing); !errors.Is(err, ErrNotFound) {
				t.Errorf("a missing object: %v", err)
			}
			big, _ := NewKey(org, SimulationResults, "big.json")
			puts := f.count("PUT")
			if _, err := s.Put(ctx, big, bytes.NewReader(make([]byte, 11)), 10); !errors.Is(err, ErrTooLarge) {
				t.Errorf("11 bytes for a limit of 10: %v", err)
			}
			if f.count("PUT") != puts {
				t.Error("an object over the limit was sent")
			}
			if _, err := s.Get(ctx, big); !errors.Is(err, ErrNotFound) {
				t.Errorf("an object over the limit was kept: %v", err)
			}
			if obj, err := s.Put(ctx, big, bytes.NewReader(make([]byte, 10)), 10); err != nil || obj.Size != 10 {
				t.Errorf("exactly the limit: %+v %v", obj, err)
			}
			empty, _ := NewKey(org, Assets, "empty")
			if obj, err := s.Put(ctx, empty, bytes.NewReader(nil), 0); err != nil || obj.SHA256 != digest(nil) || obj.Size != 0 {
				t.Errorf("an empty object: %+v %v", obj, err)
			}
			if b, ok := f.object(empty); !ok || len(b) != 0 {
				t.Errorf("the empty object is not stored: %v %q", ok, b)
			}
			before := f.count("")
			for _, k := range []string{"tenant/" + org + "/../x", "tenant/" + org + "/datasets/./x", "../x", "tenant/" + org, "", "tenant/" + org + "/datasets/a b"} {
				if _, err := s.Put(ctx, k, strings.NewReader("x"), 1); !errors.Is(err, ErrInvalidKey) {
					t.Errorf("Put %q: %v", k, err)
				}
				if _, err := s.Get(ctx, k); !errors.Is(err, ErrInvalidKey) {
					t.Errorf("Get %q: %v", k, err)
				}
				if err := s.Delete(ctx, k); !errors.Is(err, ErrInvalidKey) {
					t.Errorf("Delete %q: %v", k, err)
				}
			}
			if f.count("") != before {
				t.Error("an invalid key reached the bucket")
			}
			if _, err := s.Put(ctx, key+"x", strings.NewReader("x"), -1); err == nil {
				t.Error("a negative limit was accepted")
			}
			emptyDir(t, spool)
		})
	}
}

// Put never replaces an object even when the existence check before it
// cannot see the other writer: the PUT itself is conditional
// (If-None-Match: *), so of concurrent Puts of one key exactly one wins and
// the object is the winner's.
func TestS3ConditionalPut(t *testing.T) {
	ctx := context.Background()
	f, srv := newFakeS3(t, false)
	s, spool := testS3(t, f, srv)
	s.skipHead = true // only the condition protects the object
	key, _ := NewKey(ids.New("org"), Datasets, "x.json")
	if _, err := s.Put(ctx, key, strings.NewReader("first"), 100); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Put(ctx, key, strings.NewReader("second"), 100); !errors.Is(err, ErrExists) {
		t.Errorf("a second Put without the existence check: %v", err)
	}
	other, _ := NewKey(ids.New("org"), Datasets, "y.json")
	var wg sync.WaitGroup
	var mu sync.Mutex
	won := map[string]bool{}
	for i := range 8 {
		wg.Go(func() {
			body := strings.Repeat(string(rune('a'+i)), 1000)
			if _, err := s.Put(ctx, other, strings.NewReader(body), 1000); err == nil {
				mu.Lock()
				won[body] = true
				mu.Unlock()
			} else if !errors.Is(err, ErrExists) {
				t.Error(err)
			}
		})
	}
	wg.Wait()
	o, _ := f.object(other)
	k, _ := f.object(key)
	if len(won) != 1 || !won[string(o)] || string(k) != "first" {
		t.Errorf("%d winners; objects %.10q %q", len(won), o, k)
	}
	emptyDir(t, spool)
}

// What the bucket answers that is not the contract's is an error, with
// the HTTP status and the service's error code, never a credential; a
// redirect is not followed (the signed request would go to another host).
func TestS3Errors(t *testing.T) {
	ctx := context.Background()
	f, srv := newFakeS3(t, true)
	s, spool := testS3(t, f, srv)
	key, _ := NewKey(ids.New("org"), Imports, "a.csv")
	f.set(403, "AccessDenied")
	for name, op := range map[string]func() error{
		"put":    func() error { _, err := s.Put(ctx, key, strings.NewReader("x"), 1); return err },
		"get":    func() error { _, err := s.Get(ctx, key); return err },
		"delete": func() error { return s.Delete(ctx, key) },
	} {
		err := op()
		if err == nil || errors.Is(err, ErrNotFound) || errors.Is(err, ErrExists) {
			t.Errorf("%s: %v", name, err)
			continue
		}
		// Put and Delete ask HEAD first, whose answer has no body (so no
		// error code), as S3's has none.
		msg := err.Error()
		if !strings.Contains(msg, "403") || name == "get" && !strings.Contains(msg, "AccessDenied") {
			t.Errorf("%s: %q", name, msg)
		}
		for _, leak := range []string{"SECRET-SENTINEL", "AKIDTEST", "Signature="} {
			if strings.Contains(msg, leak) {
				t.Errorf("%s: the error holds %q: %s", name, leak, msg)
			}
		}
	}
	// An error code that is not a plain identifier is not repeated.
	f.set(403, `Denied for "x" = 1`)
	if _, err := s.Get(ctx, key); err == nil || strings.Contains(err.Error(), "Denied") {
		t.Errorf("an odd error code: %v", err)
	}
	f.set(500, "InternalError")
	if _, err := s.Put(ctx, key, strings.NewReader("x"), 1); err == nil || !strings.Contains(err.Error(), "500") {
		t.Errorf("500: %v", err)
	}
	f.set(0, "")
	target, srv2 := newFakeS3(t, true)
	f.mu.Lock()
	f.redirect = srv2.URL + "/" + target.bucket + "/" + key
	f.mu.Unlock()
	asked := f.count("GET")
	if _, err := s.Get(ctx, key); err == nil {
		t.Error("a redirect was followed")
	}
	if n := target.count("") + f.count("GET") - asked; n != 1 {
		t.Errorf("%d requests for one Get: a redirect was followed", n)
	}
	emptyDir(t, spool)
}

// A cancelled context stops Put (sending and keeping nothing), Get, the
// reader Get returns, and Delete; Delete removes an object and reports a
// missing one.
func TestS3ContextAndDelete(t *testing.T) {
	f, srv := newFakeS3(t, false)
	s, spool := testS3(t, f, srv)
	key, _ := NewKey(ids.New("org"), Imports, "a.csv")
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := s.Put(ctx, key, strings.NewReader("x"), 1); !errors.Is(err, context.Canceled) {
		t.Errorf("Put: %v", err)
	}
	ctx2, cancel2 := context.WithCancel(context.Background())
	if _, err := s.Put(ctx2, key, &cancellingReader{cancel: cancel2}, 1<<20); !errors.Is(err, context.Canceled) {
		t.Errorf("Put cancelled midway: %v", err)
	}
	if _, kept := f.object(key); f.count("PUT") != 0 || kept {
		t.Error("a cancelled Put sent or kept the object")
	}
	if _, err := s.Put(context.Background(), key, strings.NewReader(strings.Repeat("y", 100)), 100); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Get(ctx, key); !errors.Is(err, context.Canceled) {
		t.Errorf("Get: %v", err)
	}
	ctx3, cancel3 := context.WithCancel(context.Background())
	rc, err := s.Get(ctx3, key)
	if err != nil {
		t.Fatal(err)
	}
	cancel3()
	if _, err := io.ReadAll(rc); !errors.Is(err, context.Canceled) {
		t.Errorf("reading after the context ended: %v", err)
	}
	rc.Close()
	if err := s.Delete(ctx, key); !errors.Is(err, context.Canceled) {
		t.Errorf("Delete: %v", err)
	}
	if err := s.Delete(context.Background(), key); err != nil {
		t.Fatal(err)
	}
	if err := s.Delete(context.Background(), key); !errors.Is(err, ErrNotFound) {
		t.Errorf("Delete twice: %v", err)
	}
	emptyDir(t, spool)
}

// T-054 review round 2, I1: the bucket's own timeout while the caller's
// context is alive is the storage's failure, never a context error (the API
// routes take a context error for a request that ended and answer it
// without details or a log line); when the caller's context ends, it is the
// context's error.
func TestS3OwnTimeoutIsNoContextError(t *testing.T) {
	release := make(chan struct{})
	slow := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		select {
		case <-release:
		case <-r.Context().Done():
		}
	}))
	defer slow.Close()
	defer close(release)
	f, _ := newFakeS3(t, true)
	s, _ := testS3(t, f, slow)
	tr := s.client.Transport.(*http.Transport)
	tr.ResponseHeaderTimeout = 100 * time.Millisecond
	key, _ := NewKey(ids.New("org"), Datasets, "x.json")
	_, err := s.Get(context.Background(), key)
	if err == nil || errors.Is(err, context.DeadlineExceeded) || errors.Is(err, context.Canceled) || !strings.Contains(err.Error(), "no answer in time") {
		t.Errorf("the bucket's own timeout: %v (deadline: %v)", err, errors.Is(err, context.DeadlineExceeded))
	}
	if _, err := s.Put(context.Background(), key, strings.NewReader("x"), 1); err == nil || errors.Is(err, context.DeadlineExceeded) {
		t.Errorf("Put: %v", err)
	}
	tr.ResponseHeaderTimeout = time.Minute
	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()
	if _, err := s.Get(ctx, key); !errors.Is(err, context.DeadlineExceeded) {
		t.Errorf("the caller's deadline: %v", err)
	}
}

// The configuration is checked before anything is sent: an https endpoint
// that is only an origin (plain http only on loopback, for tests), a bucket
// name that is a DNS label (virtual-hosted addressing puts it in the host),
// a region, and both credentials; the secret is never in an error.
func TestNewS3(t *testing.T) {
	good := S3Config{Endpoint: "https://storage.example", Bucket: "geminus-demo-1a2b", Region: "auto",
		AccessKeyID: "AKID", SecretAccessKey: secret.Secret("SECRET-SENTINEL")}
	if _, err := NewS3(good); err != nil {
		t.Fatalf("good: %v", err)
	}
	for _, ok := range []string{"https://storage.example:8443", "http://127.0.0.1:9000", "http://localhost:9000", "http://[::1]:9000"} {
		c := good
		c.Endpoint = ok
		if _, err := NewS3(c); err != nil {
			t.Errorf("%s: %v", ok, err)
		}
	}
	bad := map[string]func(*S3Config){
		"http elsewhere":    func(c *S3Config) { c.Endpoint = "http://storage.example" },
		"a path":            func(c *S3Config) { c.Endpoint = "https://storage.example/bucket" },
		"a trailing slash":  func(c *S3Config) { c.Endpoint = "https://storage.example/" },
		"a query":           func(c *S3Config) { c.Endpoint = "https://storage.example?x=1" },
		"user information":  func(c *S3Config) { c.Endpoint = "https://u:p@storage.example" },
		"a fragment":        func(c *S3Config) { c.Endpoint = "https://storage.example#x" },
		"no endpoint":       func(c *S3Config) { c.Endpoint = "" },
		"another scheme":    func(c *S3Config) { c.Endpoint = "ftp://storage.example" },
		"upper-case bucket": func(c *S3Config) { c.Bucket = "Geminus" },
		"a dotted bucket":   func(c *S3Config) { c.Bucket = "geminus.demo" },
		"a short bucket":    func(c *S3Config) { c.Bucket = "ab" },
		"a long bucket":     func(c *S3Config) { c.Bucket = strings.Repeat("a", 64) },
		"a hyphen first":    func(c *S3Config) { c.Bucket = "-geminus" },
		"no region":         func(c *S3Config) { c.Region = "" },
		"a region with /":   func(c *S3Config) { c.Region = "us/east" },
		"no access key":     func(c *S3Config) { c.AccessKeyID = "" },
		"no secret":         func(c *S3Config) { c.SecretAccessKey = "" },
		"a relative spool":  func(c *S3Config) { c.SpoolDir = "tmp" },
	}
	for name, change := range bad {
		c := good
		change(&c)
		_, err := NewS3(c)
		if err == nil {
			t.Errorf("%s accepted", name)
		} else if strings.Contains(err.Error(), "SECRET-SENTINEL") {
			t.Errorf("%s: %v", name, err)
		}
	}
	// The adapter prints no credential either.
	s, _ := NewS3(good)
	if out := fmt.Sprintf("%v %+v %#v", s, s, s); strings.Contains(out, "SECRET-SENTINEL") {
		t.Errorf("the adapter prints its secret: %s", out)
	}
}
