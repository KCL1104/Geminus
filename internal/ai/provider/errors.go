package provider

import (
	"errors"
	"fmt"
	"strconv"

	"github.com/kcl1104/geminus/internal/model"
)

// Code classifies a provider failure. The proposal and analysis services
// (T-025, T-030) map codes to their states and HTTP errors; a provider
// failure never becomes an empty or synthetic success.
type Code string

const (
	// CodeNotConfigured: live AI is disabled (AI_PROVIDER_NOT_CONFIGURED).
	CodeNotConfigured Code = "provider_not_configured"
	// CodePolicyNotApproved: a live provider is selected, but the owner has
	// not recorded the approval of model, quota and data policy
	// (AI_POLICY_NOT_APPROVED; H-001, H-017).
	CodePolicyNotApproved Code = "policy_not_approved"
	// CodeInvalidRequest: the server built a request the client refuses
	// (an unsealed or changed prompt, a role that does not fit).
	CodeInvalidRequest Code = "invalid_request"
	// CodeContextTooLarge: the prompt exceeds the budget's bytes.
	CodeContextTooLarge Code = "context_too_large"
	// CodeConcurrencyLimited: the organization already has the budget's
	// number of provider calls in flight in this process.
	CodeConcurrencyLimited Code = "concurrency_limited"
	// CodeTimeout: the call did not finish within the budget's timeout.
	CodeTimeout Code = "timeout"
	// CodeCancelled: the caller's context ended.
	CodeCancelled Code = "cancelled"
	// CodeRateLimited: 429 on the same credential after its retries.
	CodeRateLimited Code = "rate_limited"
	// CodeUnavailable: 5xx or 529, or no connection, after the retries.
	CodeUnavailable Code = "unavailable"
	// CodeCredentialsExhausted: every credential was refused with 401, 402
	// or 403 (D-018).
	CodeCredentialsExhausted Code = "credentials_exhausted"
	// CodeRejected: the provider refused the request itself (400, 404,
	// 413...): not retried and not failed over.
	CodeRejected Code = "request_rejected"
	// CodeRefused: the model declined to answer (stop reason refusal).
	CodeRefused Code = "refused"
	// CodeTruncated: the answer reached the output token budget.
	CodeTruncated Code = "output_truncated"
	// CodeInvalidOutput: the answer is not one JSON document valid under
	// the role's output schema, or is too large.
	CodeInvalidOutput Code = "invalid_output"
)

// retryable codes may succeed if the job runs again later.
var retryable = map[Code]bool{
	CodeConcurrencyLimited: true, CodeTimeout: true, CodeRateLimited: true, CodeUnavailable: true,
	CodeSpendUnavailable: true, // no call was made; the ledger may answer next time (T-053)
}

// Error is a provider failure. It names a credential by label only and
// never carries the provider's response body, the prompt or a key; for an
// invalid output only issue paths and codes are kept (AI_SPEC §4).
type Error struct {
	Code Code
	// Status is the provider's HTTP status, 0 when there was none.
	Status int
	// ErrorType is the provider's error type of an error status.
	ErrorType string
	// Credential is the label (e.g. "org-a") of the credential that
	// answered, if any.
	Credential string
	// Issues locate an invalid output: JSON Pointer paths with the code
	// AI_OUTPUT_INVALID and a fixed message, never the offending text.
	Issues []model.ValidationIssue
	// Limit names the spend limit a quota_exceeded call would have passed
	// (organization_day, day or month; T-053).
	Limit string
	cause error // context.Canceled or context.DeadlineExceeded only
}

// Retryable reports whether running the job again later may succeed.
func (e *Error) Retryable() bool { return retryable[e.Code] }

func (e *Error) Error() string {
	s := "ai provider: " + string(e.Code)
	if e.Status != 0 {
		s += " (status " + strconv.Itoa(e.Status) + ")"
	}
	if e.Credential != "" {
		s += " [credential " + e.Credential + "]"
	}
	if n := len(e.Issues); n > 0 {
		s += fmt.Sprintf(": %d issue(s), first at %q", n, e.Issues[0].Path)
	}
	return s
}

// Unwrap exposes the context error behind cancelled and timeout, so
// errors.Is(err, context.Canceled) holds through every layer.
func (e *Error) Unwrap() error { return e.cause }

// Is matches another *Error with the same Code (a sentinel such as
// ErrNotConfigured).
func (e *Error) Is(target error) bool {
	t, ok := target.(*Error)
	return ok && t.Code == e.Code && t.Status == 0 && t.Credential == "" && t.Issues == nil && t.cause == nil
}

// NewError returns an *Error with the code; backends use it.
func NewError(code Code, status int, credential string) *Error {
	return &Error{Code: code, Status: status, Credential: credential}
}

// WithCause attaches the context error that ended a call.
func (e *Error) WithCause(cause error) *Error { e.cause = cause; return e }

// Sentinels for errors.Is.
var (
	ErrNotConfigured     = &Error{Code: CodeNotConfigured}
	ErrPolicyNotApproved = &Error{Code: CodePolicyNotApproved}
	ErrInvalidOutput     = &Error{Code: CodeInvalidOutput}
)

// CodeOf returns the Code of a provider error, or "" for another error.
func CodeOf(err error) Code {
	var e *Error
	if errors.As(err, &e) {
		return e.Code
	}
	return ""
}

// OutputInvalid is the issue code of an invalid provider output.
const OutputInvalid = "AI_OUTPUT_INVALID"
