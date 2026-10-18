package tenant_test

import (
	"errors"
	"testing"

	"github.com/kcl1104/geminus/internal/tenant"
)

func TestRequire(t *testing.T) {
	for _, c := range []struct {
		role  tenant.Role
		roles []tenant.Role
		ok    bool
	}{
		{tenant.Owner, tenant.Writers, true},
		{tenant.Editor, tenant.Writers, true},
		{tenant.Viewer, tenant.Writers, false},
		{tenant.Viewer, tenant.Members, true},
		{tenant.Owner, []tenant.Role{tenant.Owner}, true},
		{tenant.Editor, []tenant.Role{tenant.Owner}, false},
		{"admin", tenant.Members, false},
		{"", tenant.Members, false},
		{tenant.Owner, nil, false},
	} {
		err := tenant.Membership{Role: c.role}.Require(c.roles...)
		if (err == nil) != c.ok || (err != nil && !errors.Is(err, tenant.ErrForbidden)) {
			t.Errorf("%q requiring %v: %v", c.role, c.roles, err)
		}
	}
}
