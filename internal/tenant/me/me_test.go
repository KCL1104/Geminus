package me

import (
	"fmt"
	"testing"

	store "github.com/kcl1104/geminus/internal/storage/memberships"
	"github.com/kcl1104/geminus/internal/tenant"
)

func rows(n int) []store.Own {
	out := make([]store.Own, n)
	for i := range out {
		out[i] = store.Own{OrganizationID: fmt.Sprintf("org_01M4YDQK00TA%014d", i), OrganizationName: fmt.Sprintf("Org %03d", i),
			Role: string([]tenant.Role{tenant.Owner, tenant.Editor, tenant.Viewer}[i%3])}
	}
	return out
}

// page keeps the storage order and at most MaxMemberships rows, and says
// when it cut (D-045: never silently): OfUser is asked for one more than
// the list holds, so 101 rows mean "more than 100".
func TestPage(t *testing.T) {
	if MaxMemberships != 100 {
		t.Fatalf("MaxMemberships %d, D-045 says 100", MaxMemberships)
	}
	for _, c := range []struct {
		rows, want int
		truncated  bool
	}{{0, 0, false}, {1, 1, false}, {99, 99, false}, {100, 100, false}, {101, 100, true}, {150, 100, true}} {
		in := rows(c.rows)
		got, truncated := page(in)
		if got == nil || len(got) != c.want || truncated != c.truncated {
			t.Errorf("%d rows: %d memberships (nil %v), truncated %v; want %d, %v", c.rows, len(got), got == nil, truncated, c.want, c.truncated)
			continue
		}
		for i, m := range got {
			if r := in[i]; m.OrganizationID != r.OrganizationID || m.OrganizationName != r.OrganizationName || string(m.Role) != r.Role {
				t.Errorf("%d rows, membership %d: %+v from %+v", c.rows, i, m, r)
			}
		}
	}
}
