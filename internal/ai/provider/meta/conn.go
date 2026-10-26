package meta

import (
	"bytes"
	"context"
	"encoding/json/v2"
	"errors"
	"io"
	"math/rand/v2"
	"net/http"
	"slices"
	"strconv"
	"sync"
	"time"

	"github.com/kcl1104/geminus/internal/ai/provider"
	"github.com/kcl1104/geminus/internal/secret"
)

// conn is one key's connection to the Meta Model API: the HTTP client,
// the key's state (refused or resting) and the attempts of a call.
type conn struct {
	label   string
	key     secret.Secret
	baseURL string
	client  *http.Client
	now     func() time.Time
	sleep   func(context.Context, time.Duration) error
	backoff func(attempt int) time.Duration

	mu sync.Mutex
	// disabled: refused with 401 or 403 (invalid or revoked); off until the
	// process restarts, with a rotated key.
	disabled bool
	// resting: refused with 402 (a billing issue); tried again from then on.
	resting time.Time
}

func newConn(cred Credential, baseURL string, client *http.Client) (*conn, error) {
	if cred.Label == "" || cred.Key.Reveal() == "" {
		return nil, errors.New("meta: the credential needs a key and a label")
	}
	if client == nil {
		client = &http.Client{Transport: http.DefaultTransport.(*http.Transport).Clone()}
	} else {
		copied := *client
		client = &copied
	}
	// Never follow a redirect: the key header would go with it.
	client.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	return &conn{label: cred.Label, key: cred.Key, baseURL: baseURL, client: client, now: time.Now, sleep: sleepCtx, backoff: defaultBackoff}, nil
}

func (c *conn) usable(now time.Time) bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	return !c.disabled && !now.Before(c.resting)
}

func (c *conn) refuse(status int, now time.Time) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if status == http.StatusPaymentRequired {
		c.resting = now.Add(billingCooldown)
		return
	}
	c.disabled = true
}

func sleepCtx(ctx context.Context, d time.Duration) error {
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-t.C:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

// The retry waits (https://dev.meta.ai/docs/pricing-rate-limits, "Retry on
// 429 responses": "Start with a short delay such as 500 ms, double it after
// each consecutive rate-limit response, and add random jitter ... If the
// response includes a Retry-After header, wait at least that long";
// /docs/error-handling: service_overloaded and backend_unavailable send
// Retry-After 60).
const (
	backoffBase   = 500 * time.Millisecond
	backoffJitter = 500 * time.Millisecond
	backoffMax    = 8 * time.Second
	maxRetryAfter = 60 * time.Second
)

// defaultBackoff is the wait before retry attempt+1: backoffBase doubled
// per attempt, with up to backoffJitter of jitter, at most backoffMax.
func defaultBackoff(attempt int) time.Duration {
	d := backoffBase << min(max(attempt, 0), 5)
	if d >= backoffMax {
		return backoffMax
	}
	return min(d+rand.N(backoffJitter), backoffMax)
}

// retryAfter is the wait a Retry-After header asks for: seconds or an
// HTTP date; 0 when it says nothing this build reads.
func retryAfter(h http.Header, now time.Time) time.Duration {
	v := h.Get("Retry-After")
	if v == "" {
		return 0
	}
	if n, err := strconv.Atoi(v); err == nil && n >= 0 {
		return time.Duration(min(n, 3600)) * time.Second
	}
	if at, err := http.ParseTime(v); err == nil && at.After(now) {
		return at.Sub(now)
	}
	return 0
}

// wait waits before the retry after attempt, at least what the answer's
// Retry-After asks for and at most maxRetryAfter. A wait the call's
// deadline cannot hold is not waited: false, and the call ends with its
// status's code.
func (c *conn) wait(ctx context.Context, attempt int, h http.Header) bool {
	d := min(max(c.backoff(attempt), retryAfter(h, c.now())), maxRetryAfter)
	if deadline, ok := ctx.Deadline(); ok && time.Until(deadline) <= d {
		return false
	}
	return c.sleep(ctx, d) == nil
}

// tally counts a call's attempts that ended without an answer this build
// read after the request may have reached the provider: a lost connection,
// an attempt's timeout, a body cut short, a 5xx (the docs do not say an
// error status is never billed: PROPOSED D-121), an answer that could not
// be read (undecoded). The provider may have billed each of them (T-053).
type tally struct {
	unknown   int
	undecoded bool
}

// exchange is an attempt's answer, read to its end.
type exchange struct {
	status int
	header http.Header
	body   []byte
	// over: the body is larger than maxResponseBytes and was not read.
	over bool
}

// errorTypes are the error types the Meta Model API documents: the
// Messages endpoint's Anthropic envelope (https://dev.meta.ai/docs/
// protocols/messages, "Error handling": invalid_request_error,
// authentication_error, permission_error, not_found_error,
// rate_limit_error, overloaded_error, api_error) and the OpenAI-style
// envelope of the other endpoints (https://dev.meta.ai/docs/error-handling:
// invalid_request_error, authentication_error, rate_limit_error,
// server_error), retrieved 2026-10-26. An error records one of them; any
// other type, or none, is ErrorTypeOther: nothing the provider wrote is
// recorded as it is, and never its message.
var errorTypes = []string{"invalid_request_error", "authentication_error", "permission_error", "not_found_error",
	"rate_limit_error", "overloaded_error", "api_error", "server_error"}

// ErrorTypeOther is the error type of an error status whose type is not
// one of errorTypes.
const ErrorTypeOther = "other"

// errorType reads error.type from either envelope ({"type":"error",
// "error":{"type":...}} or {"error":{"type":...}}).
func errorType(body []byte) string {
	var env struct {
		Error struct {
			Type string `json:"type"`
		} `json:"error"`
	}
	if json.Unmarshal(body, &env) != nil || !slices.Contains(errorTypes, env.Error.Type) {
		return ErrorTypeOther
	}
	return env.Error.Type
}

func (c *conn) statusErr(code provider.Code, ex exchange) *provider.Error {
	e := provider.NewError(code, ex.status, c.label)
	e.ErrorType = ErrorTypeOther
	if !ex.over {
		e.ErrorType = errorType(ex.body)
	}
	return e
}

// post sends body to path (under the base URL) as one call: its attempts
// on the key, retried as the rules below say. It returns the answer of a
// 200 status, read to its end, for the caller to decode, or a
// *provider.Error; failedOver names the key when it was refused (now or
// before). t counts the attempts that may have been billed.
//
//   - A transport failure (a lost connection, an attempt's timeout) and a
//     body cut short are retried, and may have been billed.
//   - 200 is the answer. Another 2xx, or a 200 larger than
//     maxResponseBytes, is an answer that cannot be read (undecoded, may
//     have been billed, not retried).
//   - 401 and 403 disable the key until the process restarts, 402 rests it
//     for billingCooldown: credentials_exhausted (one key, nothing to fail
//     over to; not billed).
//   - 429 is retried with backoff: rate_limited (not billed).
//   - 500, 502, 503 and the other 5xx but 504 are retried ("always
//     retryable"), and may have been billed: unavailable.
//   - 504 is not retried ("Do not simply retry — the same request will
//     usually time out again") and may have been billed: unavailable.
//   - Any other status (400, 404, 413, 422, a redirect...) is rejected:
//     not retried, not billed.
func (c *conn) post(ctx context.Context, t *tally, path string, body []byte, attemptTimeout time.Duration, maxRetries int) (exchange, []string, error) {
	if !c.usable(c.now()) {
		// Refused earlier and still off: named, so the metadata and the
		// audit show the call that did not use the key.
		return exchange{}, []string{c.label}, provider.NewError(provider.CodeCredentialsExhausted, 0, c.label)
	}
	for attempt := 0; ; attempt++ {
		more := attempt < max(0, maxRetries)
		ex, err := c.attempt(ctx, path, body, attemptTimeout)
		if err != nil {
			t.unknown++
			if ctxErr := ctx.Err(); ctxErr != nil {
				code := provider.CodeCancelled
				if errors.Is(ctxErr, context.DeadlineExceeded) {
					code = provider.CodeTimeout
				}
				return exchange{}, nil, provider.NewError(code, 0, c.label).WithCause(ctxErr)
			}
			if more && c.wait(ctx, attempt, nil) {
				continue
			}
			return exchange{}, nil, provider.NewError(provider.CodeUnavailable, 0, c.label)
		}
		switch s := ex.status; {
		case s == http.StatusOK && !ex.over:
			return ex, nil, nil
		case s >= 200 && s < 300:
			t.unknown++
			t.undecoded = true
			return exchange{}, nil, provider.NewError(provider.CodeUnavailable, 0, c.label)
		case s == http.StatusUnauthorized, s == http.StatusPaymentRequired, s == http.StatusForbidden:
			c.refuse(s, c.now())
			return exchange{}, []string{c.label}, c.statusErr(provider.CodeCredentialsExhausted, ex)
		case s == http.StatusTooManyRequests:
			if more && c.wait(ctx, attempt, ex.header) {
				continue
			}
			return exchange{}, nil, c.statusErr(provider.CodeRateLimited, ex)
		case s == http.StatusGatewayTimeout:
			t.unknown++
			return exchange{}, nil, c.statusErr(provider.CodeUnavailable, ex)
		case s >= 500:
			t.unknown++
			if more && c.wait(ctx, attempt, ex.header) {
				continue
			}
			return exchange{}, nil, c.statusErr(provider.CodeUnavailable, ex)
		default:
			return exchange{}, nil, c.statusErr(provider.CodeRejected, ex)
		}
	}
}

// attempt is one POST within attemptTimeout: the key as a bearer token
// (https://dev.meta.ai/docs/authentication: "Pass the key as a Bearer
// token in the Authorization header"), nothing else of it anywhere. The
// error of a failed attempt never leaves this package: it may name the
// URL.
func (c *conn) attempt(ctx context.Context, path string, body []byte, attemptTimeout time.Duration) (exchange, error) {
	actx, cancel := context.WithTimeout(ctx, attemptTimeout)
	defer cancel()
	req, err := http.NewRequestWithContext(actx, http.MethodPost, c.baseURL+path, bytes.NewReader(body))
	if err != nil {
		return exchange{}, err
	}
	req.Header.Set("Authorization", "Bearer "+c.key.Reveal())
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")
	resp, err := c.client.Do(req)
	if err != nil {
		return exchange{}, err
	}
	defer resp.Body.Close()
	data, err := io.ReadAll(io.LimitReader(resp.Body, maxResponseBytes+1))
	if err != nil {
		return exchange{}, err
	}
	if len(data) > maxResponseBytes {
		return exchange{status: resp.StatusCode, header: resp.Header, over: true}, nil
	}
	return exchange{status: resp.StatusCode, header: resp.Header, body: data}, nil
}
