package main

import (
	"slices"
	"testing"
)

// T-035 review I8: github.com/google/uuid may bring database/sql/driver
// (its UUID type is a driver.Valuer and sql.Scanner) and nothing else the
// rule forbids: database/sql or a database driver reached through it is
// still a violation, and so is a package that imports database/sql/driver
// itself.
func TestTheUUIDGatewayBringsOnlyTheDriver(t *testing.T) {
	var r rule
	for _, x := range rules {
		if x.name == "db-only-in-storage" {
			r = x
		}
	}
	root := module + "/internal/observability/telemetry/export"
	for _, c := range []struct {
		name    string
		root    []string
		uuid    []string
		wantBad string
	}{
		{"the driver through uuid", []string{"github.com/google/uuid"}, []string{"database/sql/driver"}, ""},
		{"database/sql through uuid", []string{"github.com/google/uuid"}, []string{"database/sql/driver", "database/sql"}, "database/sql"},
		{"a driver through uuid", []string{"github.com/google/uuid"}, []string{"github.com/jackc/pgx/v5"}, "github.com/jackc/pgx/v5"},
		{"the driver itself", []string{"database/sql/driver"}, nil, "database/sql/driver"},
	} {
		g := &graph{pkgs: map[string]*pkg{
			root:                      {ImportPath: root, Imports: c.root},
			"github.com/google/uuid":  {ImportPath: "github.com/google/uuid", Imports: c.uuid},
			"database/sql/driver":     {ImportPath: "database/sql/driver", Standard: true, Imports: []string{"database/sql/internal"}},
			"database/sql/internal":   {ImportPath: "database/sql/internal", Standard: true},
			"database/sql":            {ImportPath: "database/sql", Standard: true},
			"github.com/jackc/pgx/v5": {ImportPath: "github.com/jackc/pgx/v5"},
		}, roots: []string{root}}
		violations, errs := check(g, []rule{r})
		if len(errs) > 0 {
			t.Fatalf("%s: %v", c.name, errs)
		}
		var got []string
		for _, v := range violations {
			got = append(got, v.chain[len(v.chain)-1])
		}
		if c.wantBad == "" && len(got) != 0 || c.wantBad != "" && !slices.Equal(got, []string{c.wantBad}) {
			t.Errorf("%s: violations on %v, want %q", c.name, got, c.wantBad)
		}
	}
}
