package ids

import (
	"regexp"
	"slices"
	"strings"
	"testing"
	"time"
)

var pattern = regexp.MustCompile(`^prj_[0-7][0-9A-HJKMNP-TV-Z]{25}$`)

// Golden vectors: the ULID specification's timestamp example and the
// extremes of the 128-bit space.
func TestEncode(t *testing.T) {
	var zero, ones [10]byte
	for i := range ones {
		ones[i] = 0xff
	}
	for _, c := range []struct {
		ms      uint64
		entropy [10]byte
		want    string
	}{
		{0, zero, "00000000000000000000000000"},
		{1469922850259, zero, "01ARZ3NDEK0000000000000000"}, // github.com/ulid/spec
		{1<<48 - 1, ones, "7ZZZZZZZZZZZZZZZZZZZZZZZZZ"},
		{0, [10]byte{9: 1}, "00000000000000000000000001"},
		{1, zero, "00000000010000000000000000"},
		{0, [10]byte{0x80}, "0000000000G000000000000000"},
	} {
		if got := encode(c.ms, c.entropy); got != c.want {
			t.Errorf("encode(%d, %x) = %s, want %s", c.ms, c.entropy, got, c.want)
		}
	}
}

// New IDs match the D-001 pattern, are unique, and sort by millisecond.
func TestNew(t *testing.T) {
	seen := map[string]bool{}
	for range 10000 {
		id := New("prj")
		if !pattern.MatchString(id) {
			t.Fatalf("%q does not match the D-001 pattern", id)
		}
		if seen[id] {
			t.Fatalf("duplicate %s", id)
		}
		seen[id] = true
	}
	// The time part is now, to the millisecond.
	before := time.Now().UnixMilli()
	id := New("prj")
	after := time.Now().UnixMilli()
	var ms int64
	for _, c := range id[4:14] {
		ms = ms<<5 | int64(strings.IndexRune(alphabet, c))
	}
	if ms < before || ms > after || !strings.HasPrefix(id, "prj_") {
		t.Errorf("%s: time part %d, want %d–%d", id, ms, before, after)
	}
	base := time.UnixMilli(1760000000000)
	var ordered []string
	for i := range 50 {
		ordered = append(ordered, ULID(base.Add(time.Duration(i)*time.Millisecond)))
	}
	if !slices.IsSorted(ordered) {
		t.Error("ULIDs of later milliseconds do not sort later")
	}
	if a, b := ULID(base), ULID(base); a[:10] != b[:10] || a == b {
		t.Errorf("same millisecond: %s %s (same time part, different randomness expected)", a, b)
	}
}
