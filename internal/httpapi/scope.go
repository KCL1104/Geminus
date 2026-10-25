package httpapi

import (
	"context"
	"log/slog"
	"net/http"
	"regexp"

	"github.com/kcl1104/geminus/internal/auth"
	"github.com/kcl1104/geminus/internal/observability"
	"github.com/kcl1104/geminus/internal/tenant"
)

// Resolver finds the user of a verified identity (tenant.DB.ResolveUser).
type Resolver interface {
	ResolveUser(ctx context.Context, issuer, subject string) (string, error)
}

var orgID = regexp.MustCompile(`^org_[0-7][0-9A-HJKMNP-TV-Z]{25}$`)

// Scoped authenticates the request (auth.BearerToken and v), resolves its
// user and reads the X-Organization-ID selector, then calls next with the
// scope. The selector is not authorization: every handler runs its work in
// tenant.DB.Run, which checks the membership in the same transaction, so a
// non-member gets 404 and a missing role 403 (D-008, SECURITY §2).
func Scoped(v auth.Verifier, users Resolver, next func(w http.ResponseWriter, r *http.Request, s tenant.Scope) error) HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) error {
		token, err := auth.BearerToken(r)
		if err != nil {
			return err
		}
		id, err := v.Verify(r.Context(), token)
		if err != nil {
			return err
		}
		values := r.Header.Values("X-Organization-ID")
		if len(values) != 1 || !orgID.MatchString(values[0]) {
			return &Error{Code: InvalidRequest, Message: "exactly one X-Organization-ID header with an organization ID is required",
				Details: []Detail{{Path: "", Reason: "X-Organization-ID"}}}
		}
		user, err := users.ResolveUser(r.Context(), id.Issuer, id.Subject)
		if err != nil {
			return err
		}
		// The selected organization and the user, opaque IDs only (never the
		// issuer or subject), on the request's later log lines (D-093 (b)).
		// The selection is not proof of membership: the handler's
		// transaction checks that.
		observability.Annotate(r.Context(), slog.String("organization_id", values[0]), slog.String("user_id", user))
		ctx := auth.WithIdentity(r.Context(), id)
		return next(w, r.WithContext(ctx), tenant.Scope{OrganizationID: values[0], UserID: user})
	}
}

// Self authenticates a request that reads the caller's own identity and
// selects no organization (D-045, GET /v1/me), in Scoped's order: it
// verifies the token (auth.BearerToken and v), refuses a request that
// carries X-Organization-ID, even empty (400: the header is never ignored,
// so a client cannot believe it selected an organization), and resolves
// the user, then calls next with the user's ID. An identity without a user
// is tenant.ErrUnknownUser (403); none is created (D-008).
func Self(v auth.Verifier, users Resolver, next func(w http.ResponseWriter, r *http.Request, user string) error) HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) error {
		token, err := auth.BearerToken(r)
		if err != nil {
			return err
		}
		id, err := v.Verify(r.Context(), token)
		if err != nil {
			return err
		}
		if len(r.Header.Values("X-Organization-ID")) > 0 {
			return &Error{Code: InvalidRequest, Message: "this operation selects no organization: send no X-Organization-ID header",
				Details: []Detail{{Path: "", Reason: "X-Organization-ID"}}}
		}
		user, err := users.ResolveUser(r.Context(), id.Issuer, id.Subject)
		if err != nil {
			return err
		}
		observability.Annotate(r.Context(), slog.String("user_id", user))
		return next(w, r.WithContext(auth.WithIdentity(r.Context(), id)), user)
	}
}
