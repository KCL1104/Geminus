package httpapi

import (
	"context"
	"net/http"
	"regexp"

	"github.com/kcl1104/geminus/internal/auth"
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
		ctx := auth.WithIdentity(r.Context(), id)
		return next(w, r.WithContext(ctx), tenant.Scope{OrganizationID: values[0], UserID: user})
	}
}
