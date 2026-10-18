// Package httpapi is the API's HTTP transport (ADR-019): the error
// envelope of common.schema.json#/$defs/Error, request IDs, strict and
// schema-validated JSON bodies, and the middleware that turns a verified
// token and the X-Organization-ID selector into a tenant.Scope. Handlers
// return errors; this package alone decides their status and body, so the
// API has one error format.
package httpapi

import (
	"context"
	"encoding/json/v2"
	"errors"
	"log/slog"
	"net/http"

	"github.com/kcl1104/geminus/internal/auth"
	"github.com/kcl1104/geminus/internal/storage/postgres"
	"github.com/kcl1104/geminus/internal/tenant"
)

// Code is the closed error-code catalogue (common.schema.json
// #/$defs/ErrorCode, D-025). Each code has exactly one status.
type Code string

const (
	InvalidRequest          Code = "INVALID_REQUEST"
	IdempotencyKeyRequired  Code = "IDEMPOTENCY_KEY_REQUIRED"
	Unauthenticated         Code = "UNAUTHENTICATED"
	Forbidden               Code = "FORBIDDEN"
	NotFound                Code = "NOT_FOUND"
	ModelVersionConflict    Code = "MODEL_VERSION_CONFLICT"
	IdempotencyKeyConflict  Code = "IDEMPOTENCY_KEY_CONFLICT"
	LifecycleConflict       Code = "LIFECYCLE_CONFLICT"
	ResultNotReady          Code = "RESULT_NOT_READY"
	ProposalAlreadyDecided  Code = "PROPOSAL_ALREADY_DECIDED"
	PayloadTooLarge         Code = "PAYLOAD_TOO_LARGE"
	UnsupportedMediaType    Code = "UNSUPPORTED_MEDIA_TYPE"
	ModelValidationFailed   Code = "MODEL_VALIDATION_FAILED"
	RequestValidationFailed Code = "REQUEST_VALIDATION_FAILED"
	PreconditionRequired    Code = "PRECONDITION_REQUIRED"
	RateLimited             Code = "RATE_LIMITED"
	InternalError           Code = "INTERNAL_ERROR"
	ServiceUnavailable      Code = "SERVICE_UNAVAILABLE"
	AIProviderNotConfigured Code = "AI_PROVIDER_NOT_CONFIGURED"
	AIPolicyNotApproved     Code = "AI_POLICY_NOT_APPROVED"
)

// Status is each code's HTTP status (the schema's x-http-status; a contract
// test keeps the two equal).
var Status = map[Code]int{
	InvalidRequest: 400, IdempotencyKeyRequired: 400, Unauthenticated: 401, Forbidden: 403, NotFound: 404,
	ModelVersionConflict: 409, IdempotencyKeyConflict: 409, LifecycleConflict: 409, ResultNotReady: 409,
	ProposalAlreadyDecided: 409, PayloadTooLarge: 413, UnsupportedMediaType: 415, ModelValidationFailed: 422,
	RequestValidationFailed: 422, PreconditionRequired: 428, RateLimited: 429, InternalError: 500,
	ServiceUnavailable: 503, AIProviderNotConfigured: 503, AIPolicyNotApproved: 503,
}

// Detail is one entry of error.details: a JSON Pointer into the request
// (or "" for the whole request) and why it was refused.
type Detail struct {
	Path     string `json:"path"`
	Reason   string `json:"reason"`
	Code     string `json:"code,omitzero"`
	Severity string `json:"severity,omitzero"`
}

// Error is a refusal the client may see: its code, message and details are
// sent as they are, so they never hold internal state.
type Error struct {
	Code    Code
	Message string
	Details []Detail
}

func (e *Error) Error() string { return string(e.Code) + ": " + e.Message }

// Errorf is an *Error without details.
func Errorf(code Code, message string) *Error { return &Error{Code: code, Message: message} }

type envelope struct {
	Error body `json:"error"`
}

type body struct {
	Code      Code     `json:"code"`
	Message   string   `json:"message"`
	Details   []Detail `json:"details,omitzero"`
	RequestID string   `json:"request_id"`
}

// classify maps err to what the client sees. Errors of other packages
// are mapped by sentinel; anything else is INTERNAL_ERROR, never its text.
func classify(err error) (*Error, bool) {
	var e *Error
	switch {
	case errors.As(err, &e):
		return e, true
	case errors.Is(err, auth.ErrKeysUnavailable):
		return Errorf(ServiceUnavailable, "the identity provider's keys are unavailable; retry later"), true
	case errors.Is(err, auth.ErrInvalidToken):
		return Errorf(Unauthenticated, "a valid bearer token is required"), true
	case errors.Is(err, tenant.ErrUnknownUser):
		return Errorf(Forbidden, "the authenticated identity has no user in this deployment"), true
	case errors.Is(err, tenant.ErrNoMembership):
		return Errorf(NotFound, "not found"), true
	case errors.Is(err, tenant.ErrForbidden):
		return Errorf(Forbidden, "your role in this organization does not allow this operation"), true
	case errors.Is(err, postgres.ErrRetryable), errors.Is(err, postgres.ErrOutcomeUnknown):
		return Errorf(ServiceUnavailable, "the request could not be completed; retry it with the same Idempotency-Key"), true
	case errors.Is(err, context.Canceled), errors.Is(err, context.DeadlineExceeded):
		return Errorf(ServiceUnavailable, "the request did not complete in time"), true
	}
	return Errorf(InternalError, "internal error"), false
}

// WriteError writes err as the error envelope. Unexpected errors are logged
// with the request ID; the client gets INTERNAL_ERROR without detail.
func WriteError(w http.ResponseWriter, r *http.Request, log *slog.Logger, err error) {
	e, known := classify(err)
	id := RequestID(r.Context())
	if !known {
		log.ErrorContext(r.Context(), "request failed", "request_id", id, "error", err.Error())
	}
	status, ok := Status[e.Code]
	if !ok {
		e, status = Errorf(InternalError, "internal error"), 500
		log.ErrorContext(r.Context(), "unknown error code", "request_id", id)
	}
	if e.Code == Unauthenticated {
		if c := auth.Challenge(err); c != "" {
			w.Header().Set("WWW-Authenticate", c)
		}
	}
	WriteJSON(w, status, envelope{Error: body{Code: e.Code, Message: e.Message, Details: e.Details, RequestID: id}})
}

// WriteJSON writes v with status as application/json.
func WriteJSON(w http.ResponseWriter, status int, v any) {
	data, err := json.Marshal(v)
	if err != nil {
		status = 500
		data = []byte(`{"error":{"code":"INTERNAL_ERROR","message":"internal error","request_id":"` + w.Header().Get("X-Request-ID") + `"}}`)
	}
	WriteBody(w, status, data)
}

// WriteBody writes data, a JSON document, with status.
func WriteBody(w http.ResponseWriter, status int, data []byte) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(status)
	_, _ = w.Write(data)
}
