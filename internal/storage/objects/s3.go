package objects

import (
	"context"
	"crypto/sha256"
	"crypto/tls"
	"encoding/hex"
	"encoding/xml"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"github.com/kcl1104/geminus/internal/secret"
)

// S3Config is an S3-compatible bucket (T-039, ADR-029): Railway's buckets,
// or any service that speaks the S3 API with SigV4 and conditional PUT.
type S3Config struct {
	// Endpoint is the service's base URL, an origin only:
	// https://host[:port]. Plain http only on loopback (tests).
	Endpoint string
	// Bucket is the bucket's S3 name: 3–63 lowercase letters, digits and
	// '-', starting and ending with a letter or digit (a DNS label, so it
	// can be the first label of a virtual-hosted URL).
	Bucket string
	// Region signs the requests ("auto" for Railway's buckets).
	Region string
	// AccessKeyID and SecretAccessKey are the bucket's credentials.
	AccessKeyID     string
	SecretAccessKey secret.Secret
	// PathStyle addresses the bucket as <endpoint>/<bucket>/<key> instead
	// of <bucket>.<endpoint host>/<key> (virtual-hosted, the default).
	PathStyle bool
	// SpoolDir is where Put stages an object before it is sent (the
	// payload is signed by its SHA-256, so the bytes are read before the
	// request): an absolute directory, os.TempDir() when empty.
	SpoolDir string
}

// S3 keeps objects in an S3-compatible bucket (T-039; D-044's interface).
//
//   - Keys are the server's (ValidKey), checked before any request.
//   - Put stages the bytes in SpoolDir (at most max of them; a larger
//     object is never sent), then sends them with their SHA-256 signed
//     (x-amz-content-sha256: the service refuses other bytes) and
//     If-None-Match: *, so an existing object is never replaced: the
//     service answers 412 and Put returns ErrExists. A HEAD before the PUT
//     answers the common case without sending the bytes; the condition is
//     what holds when two writers race.
//   - Get streams the object; whoever serves it has authorized the caller
//     and checks its size and SHA-256 against the row (as with FS).
//   - Nothing is public: every request is signed, no URL is presigned and
//     no redirect is followed.
//
// Errors carry the operation, the HTTP status and the service's error code,
// never a credential or a response body.
type S3 struct {
	endpoint  *url.URL
	bucket    string
	pathStyle bool
	spool     string
	signer    sigv4
	client    *http.Client
	now       func() time.Time
	// skipHead leaves out the existence check before a PUT (tests of the
	// condition itself).
	skipHead bool
}

var (
	_ Store      = (*S3)(nil)
	_ Maintainer = (*S3)(nil)
)

var (
	bucketName = regexp.MustCompile(`^[a-z0-9][a-z0-9-]{1,61}[a-z0-9]$`)
	regionName = regexp.MustCompile(`^[a-z0-9][a-z0-9-]{0,62}$`)
	errorCode  = regexp.MustCompile(`^[A-Za-z][A-Za-z0-9.]{0,63}$`)
)

// emptySHA256 is the SHA-256 of no bytes, the payload hash of GET, HEAD
// and DELETE.
const emptySHA256 = "e3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b855"

// ValidS3Bucket reports whether name is a bucket name NewS3 accepts.
func ValidS3Bucket(name string) bool { return bucketName.MatchString(name) }

// ValidS3Region reports whether region is a region NewS3 accepts.
func ValidS3Region(region string) bool { return regionName.MatchString(region) }

// CheckS3Endpoint reports why raw is not an endpoint NewS3 accepts (nil if
// it is); the error never repeats the value.
func CheckS3Endpoint(raw string) error {
	_, err := s3Endpoint(raw)
	return err
}

// NewS3 checks cfg and returns the adapter; it sends nothing.
func NewS3(cfg S3Config) (*S3, error) {
	u, err := s3Endpoint(cfg.Endpoint)
	if err != nil {
		return nil, err
	}
	switch {
	case !ValidS3Bucket(cfg.Bucket):
		return nil, errors.New("objects: the bucket name must be 3-63 lowercase letters, digits and '-', starting and ending with a letter or digit")
	case !ValidS3Region(cfg.Region):
		return nil, errors.New("objects: the region must be lowercase letters, digits and '-'")
	case cfg.AccessKeyID == "" || cfg.SecretAccessKey.Reveal() == "":
		return nil, errors.New("objects: the bucket's access key ID and secret access key are required")
	case cfg.SpoolDir != "" && (!filepath.IsAbs(cfg.SpoolDir) || filepath.Clean(cfg.SpoolDir) != cfg.SpoolDir):
		return nil, errors.New("objects: the spool directory must be a clean absolute path")
	}
	spool := cfg.SpoolDir
	if spool == "" {
		spool = os.TempDir()
	}
	return &S3{
		endpoint: u, bucket: cfg.Bucket, pathStyle: cfg.PathStyle, spool: spool,
		signer: sigv4{accessKey: cfg.AccessKeyID, secret: cfg.SecretAccessKey, region: cfg.Region, service: "s3"},
		client: &http.Client{Transport: s3Transport(), CheckRedirect: noRedirects},
		now:    time.Now,
	}, nil
}

// Format prints the bucket only, never the credentials.
func (s S3) Format(f fmt.State, _ rune) {
	fmt.Fprintf(f, "objects.S3{endpoint: %s, bucket: %s}", s.endpoint.Redacted(), s.bucket)
}

// s3Endpoint parses an endpoint: an https origin, or plain http on loopback.
func s3Endpoint(raw string) (*url.URL, error) {
	u, err := url.Parse(raw)
	if err != nil || u.Host == "" || u.Opaque != "" || u.User != nil || u.Path != "" || u.RawQuery != "" || u.ForceQuery ||
		u.Fragment != "" || strings.Contains(raw, "#") {
		return nil, errors.New("objects: the endpoint must be an origin (scheme and host) without a path, credentials, query or fragment")
	}
	if u.Scheme != "https" && !(u.Scheme == "http" && loopbackHost(u.Hostname())) {
		return nil, errors.New("objects: the endpoint must be https (plain http only on loopback)")
	}
	return u, nil
}

func loopbackHost(host string) bool {
	if strings.EqualFold(host, "localhost") {
		return true
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}

// s3Transport bounds connecting and waiting for an answer; a body is
// bounded by the request's context, not by a fixed deadline (a large
// object takes as long as it takes).
func s3Transport() *http.Transport {
	return &http.Transport{
		Proxy:                 http.ProxyFromEnvironment,
		DialContext:           (&net.Dialer{Timeout: 10 * time.Second, KeepAlive: 30 * time.Second}).DialContext,
		TLSClientConfig:       &tls.Config{MinVersion: tls.VersionTLS12},
		TLSHandshakeTimeout:   10 * time.Second,
		ResponseHeaderTimeout: time.Minute,
		IdleConnTimeout:       90 * time.Second,
		MaxIdleConnsPerHost:   8,
		ForceAttemptHTTP2:     true,
	}
}

// noRedirects keeps a signed request at the host it was signed for.
func noRedirects(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }

// objectURL is key's URL in the bucket.
func (s *S3) objectURL(key string) string {
	u := *s.endpoint
	if s.pathStyle {
		u.Path = "/" + s.bucket + "/" + key
	} else {
		u.Host = s.bucket + "." + u.Host
		u.Path = "/" + key
	}
	return u.String()
}

// checkKey accepts only keys the server makes (as FS.path does).
func checkKey(key string) error {
	parts := strings.SplitN(key, "/", 3)
	if len(parts) != 3 || parts[0] != "tenant" || !ValidKey(parts[1], key) {
		return ErrInvalidKey
	}
	return nil
}

// s3Error is a request the bucket did not answer as the contract expects.
type s3Error struct {
	op     string
	status int
	code   string
}

func (e *s3Error) Error() string {
	msg := fmt.Sprintf("objects: S3 %s: HTTP %d", e.op, e.status)
	if e.code != "" {
		msg += " " + e.code
	}
	return msg
}

// failure reads the service's error code from res (at most 64 KiB of the
// body, the code only if it is a plain identifier) and closes it.
func failure(op string, res *http.Response) error {
	defer res.Body.Close()
	var body struct {
		Code string `xml:"Code"`
	}
	code := ""
	if xml.NewDecoder(io.LimitReader(res.Body, 64<<10)).Decode(&body) == nil && errorCode.MatchString(body.Code) {
		code = body.Code
	}
	return &s3Error{op: op, status: res.StatusCode, code: code}
}

// do sends one signed request for key.
func (s *S3) do(ctx context.Context, method, key string, body *os.File, size int64, payloadHash string, conditional bool) (*http.Response, error) {
	req, err := http.NewRequestWithContext(ctx, method, s.objectURL(key), nil)
	if err != nil {
		return nil, fmt.Errorf("objects: S3 %s: %w", method, err)
	}
	if body != nil {
		req.ContentLength = size
		if size == 0 {
			req.Body = http.NoBody
		} else {
			req.Body = io.NopCloser(body)
		}
		req.Header.Set("Content-Type", "application/octet-stream")
	}
	if conditional {
		req.Header.Set("If-None-Match", "*")
	}
	s.signer.sign(req, payloadHash, s.now())
	res, err := s.client.Do(req)
	if err != nil {
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		return nil, unanswered(method, err)
	}
	return res, nil
}

// unanswered is a request the bucket did not answer while its context was
// alive (the transport's own timeouts, a refused or broken connection). It
// never wraps the cause: Go reports its own timeouts as
// context.DeadlineExceeded, and the API routes take a context error for a
// request that ended (datasets.StorageFailure), so the bucket's own timeout
// would be answered as one, without details or a log line (T-054 review
// round 2, I1). As this error it is the storage's failure: 503
// DATASET_STORAGE_FAILED, logged. The text names the URL (the bucket's host
// and the key, no credential: the signature is in a header).
func unanswered(op string, err error) error {
	why := "no answer"
	var ne net.Error
	if errors.As(err, &ne) && ne.Timeout() {
		why = "no answer in time"
	}
	return fmt.Errorf("objects: S3 %s: %s (%s)", op, why, err.Error())
}

// exists asks whether key has an object.
func (s *S3) exists(ctx context.Context, key string) (bool, error) {
	res, err := s.do(ctx, http.MethodHead, key, nil, 0, emptySHA256, false)
	if err != nil {
		return false, err
	}
	switch res.StatusCode {
	case http.StatusOK:
		res.Body.Close()
		return true, nil
	case http.StatusNotFound:
		res.Body.Close()
		return false, nil
	}
	return false, failure("HEAD", res)
}

// Put stores r's bytes under key (see Store and S3).
func (s *S3) Put(ctx context.Context, key string, r io.Reader, max int64) (Object, error) {
	if err := ctx.Err(); err != nil {
		return Object{}, err
	}
	if err := checkKey(key); err != nil {
		return Object{}, err
	}
	if max < 0 {
		return Object{}, fmt.Errorf("objects: a negative size limit")
	}
	if !s.skipHead {
		taken, err := s.exists(ctx, key)
		if err != nil {
			return Object{}, err
		}
		if taken {
			return Object{}, ErrExists
		}
	}
	tmp, err := os.CreateTemp(s.spool, ".put-*")
	if err != nil {
		return Object{}, fmt.Errorf("objects: staging the object: %w", err)
	}
	defer func() {
		tmp.Close()
		os.Remove(tmp.Name())
	}()
	h := sha256.New()
	n, err := io.Copy(io.MultiWriter(tmp, h), &limited{ctx: ctx, r: r, left: max + 1})
	if err == nil && n > max {
		err = ErrTooLarge
	}
	if err != nil {
		return Object{}, err
	}
	if _, err := tmp.Seek(0, io.SeekStart); err != nil {
		return Object{}, fmt.Errorf("objects: staging the object: %w", err)
	}
	sum := hex.EncodeToString(h.Sum(nil))
	res, err := s.do(ctx, http.MethodPut, key, tmp, n, sum, true)
	if err != nil {
		return Object{}, err
	}
	switch res.StatusCode {
	case http.StatusOK, http.StatusCreated, http.StatusNoContent:
		_, _ = io.Copy(io.Discard, io.LimitReader(res.Body, 64<<10))
		res.Body.Close()
		return Object{Key: key, SHA256: "sha256:" + sum, Size: n}, nil
	case http.StatusPreconditionFailed:
		res.Body.Close()
		return Object{}, ErrExists
	}
	return Object{}, failure("PUT", res)
}

// Get opens key (see Store).
func (s *S3) Get(ctx context.Context, key string) (io.ReadCloser, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if err := checkKey(key); err != nil {
		return nil, err
	}
	res, err := s.do(ctx, http.MethodGet, key, nil, 0, emptySHA256, false)
	if err != nil {
		return nil, err
	}
	switch res.StatusCode {
	case http.StatusOK:
		return &s3Reader{ctx: ctx, body: res.Body}, nil
	case http.StatusNotFound:
		res.Body.Close()
		return nil, ErrNotFound
	}
	return nil, failure("GET", res)
}

// Delete removes key (maintenance only, see Maintainer); ErrNotFound if
// there is no object.
func (s *S3) Delete(ctx context.Context, key string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := checkKey(key); err != nil {
		return err
	}
	found, err := s.exists(ctx, key)
	if err != nil {
		return err
	}
	if !found {
		return ErrNotFound
	}
	res, err := s.do(ctx, http.MethodDelete, key, nil, 0, emptySHA256, false)
	if err != nil {
		return err
	}
	if res.StatusCode == http.StatusNoContent || res.StatusCode == http.StatusOK {
		res.Body.Close()
		return nil
	}
	return failure("DELETE", res)
}

// s3Reader is an object's body that stops when its context ends.
type s3Reader struct {
	ctx  context.Context
	body io.ReadCloser
}

func (r *s3Reader) Read(p []byte) (int, error) {
	if err := r.ctx.Err(); err != nil {
		return 0, err
	}
	n, err := r.body.Read(p)
	switch {
	case err == nil || err == io.EOF:
	case r.ctx.Err() != nil:
		return n, r.ctx.Err()
	default:
		// The body broke off while the reader's context was alive: the
		// storage's failure, never a context error (see unanswered).
		return n, unanswered("GET body", err)
	}
	return n, err
}

func (r *s3Reader) Close() error { return r.body.Close() }
