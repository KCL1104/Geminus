package proposals

import (
	"errors"
	"fmt"
	"testing"

	"github.com/jackc/pgx/v5/pgconn"
)

// PostgreSQL's refusals of what a row may hold (text with U+0000: 22021;
// jsonb with \u0000: 22P05; a CHECK: 23514) are ErrUnstorable, which the
// generator ends the attempt with for good; anything else stays as it was
// (H-003 review M2).
func TestUnstorable(t *testing.T) {
	for _, code := range []string{"22021", "22P05", "23514"} {
		err := fmt.Errorf("exec: %w", &pgconn.PgError{Code: code, Message: "a value the row quoted"})
		got := unstorable(err)
		if !errors.Is(got, ErrUnstorable) {
			t.Errorf("%s: %v", code, got)
		}
		var pg *pgconn.PgError
		if errors.As(got, &pg) {
			t.Errorf("%s: the server's message stays reachable: %v", code, got)
		}
	}
	for _, err := range []error{&pgconn.PgError{Code: "40001"}, &pgconn.PgError{Code: "55000"}, errors.New("reset"), nil} {
		if got := unstorable(err); got != err {
			t.Errorf("%v became %v", err, got)
		}
	}
}
