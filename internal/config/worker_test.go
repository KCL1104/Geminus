package config

import (
	"slices"
	"strings"
	"testing"
)

// H-056 design §9 (W1): WORKER_LANES chooses one job loop (single, the
// default: unset is single) or two in one process (split: simulations in
// one, every other job type in the other).
func TestLoadWorkerLanes(t *testing.T) {
	for raw, want := range map[string]Lanes{"": LanesSingle, "single": LanesSingle, "split": LanesSplit} {
		w, err := LoadWorker(env(map[string]string{"WORKER_ID": "worker-0", "WORKER_ORGANIZATION_IDS": "org_a", "WORKER_LANES": raw}))
		if err != nil || w.Lanes != want {
			t.Errorf("%q: %+v %v", raw, w, err)
		}
	}
	// Nothing else is a mode: never a guess at what was meant.
	for _, raw := range []string{"Split", "SPLIT", " split", "split ", "both", "two", "1", "2", "sim,rest", "true", "on"} {
		_, err := LoadWorker(env(map[string]string{"WORKER_ID": "worker-0", "WORKER_ORGANIZATION_IDS": "org_a", "WORKER_LANES": raw}))
		if err == nil || !slices.Equal(codes(t, err), []string{"WORKER_LANES:invalid"}) {
			t.Errorf("%q: %v", raw, err)
		}
	}
	// The lanes hold leases as <WORKER_ID>:sim and <WORKER_ID>:rest, each an
	// actor reference of at most 128 characters (D-092): split leaves room
	// for the longer suffix, single is unchanged.
	long := map[string]string{"WORKER_ID": strings.Repeat("w", 123), "WORKER_ORGANIZATION_IDS": "org_a", "WORKER_LANES": "split"}
	if w, err := LoadWorker(env(long)); err != nil || w.ID != strings.Repeat("w", 123) {
		t.Errorf("123 characters, split: %+v %v", w, err)
	}
	long["WORKER_ID"] = strings.Repeat("w", 124)
	if _, err := LoadWorker(env(long)); err == nil || !slices.Equal(codes(t, err), []string{"WORKER_ID:invalid"}) || !strings.Contains(err.Error(), "WORKER_LANES") {
		t.Errorf("124 characters, split: %v", err)
	}
	long["WORKER_LANES"] = ""
	long["WORKER_ID"] = strings.Repeat("w", 128)
	if w, err := LoadWorker(env(long)); err != nil || w.Lanes != LanesSingle {
		t.Errorf("128 characters, single: %+v %v", w, err)
	}
	// Every problem is reported together, as before.
	if _, err := LoadWorker(env(map[string]string{"WORKER_LANES": "both"})); err == nil ||
		!slices.Equal(codes(t, err), []string{"WORKER_ID:required", "WORKER_ORGANIZATION_IDS:required", "WORKER_LANES:invalid"}) {
		t.Errorf("nothing valid: %v", err)
	}
}

// WORKER_REST_LANES (after H-065's finding that a Muse Wonder proposal holds
// the one rest lane for up to 30 minutes): with WORKER_LANES=split, 1 (the
// default: unset is 1) to 4 rest loops; single has none, and refuses the
// variable instead of ignoring it. Nothing else is a count: never a guess.
func TestLoadWorkerRestLanes(t *testing.T) {
	base := func(lanes, rest string) map[string]string {
		return map[string]string{"WORKER_ID": "worker-0", "WORKER_ORGANIZATION_IDS": "org_a", "WORKER_LANES": lanes, "WORKER_REST_LANES": rest}
	}
	for rest, want := range map[string]int{"": 1, "1": 1, "2": 2, "3": 3, "4": 4} {
		w, err := LoadWorker(env(base("split", rest)))
		if err != nil || w.Lanes != LanesSplit || w.RestLanes != want {
			t.Errorf("split, %q: %+v %v", rest, w, err)
		}
	}
	for _, lanes := range []string{"", "single"} {
		if w, err := LoadWorker(env(base(lanes, ""))); err != nil || w.Lanes != LanesSingle || w.RestLanes != 0 {
			t.Errorf("%q, unset: %+v %v", lanes, w, err)
		}
		// Single runs no rest lane: a count there is a mistake, said so.
		for _, rest := range []string{"1", "2", "4", "0", "two"} {
			_, err := LoadWorker(env(base(lanes, rest)))
			if err == nil || !slices.Equal(codes(t, err), []string{"WORKER_REST_LANES:invalid"}) || !strings.Contains(err.Error(), "WORKER_LANES=split") {
				t.Errorf("%q, %q: %v", lanes, rest, err)
			}
		}
	}
	for _, rest := range []string{"0", "5", "16", "-1", "+2", "02", " 2", "2 ", "two", "2.0", "0x2", "9223372036854775809"} {
		_, err := LoadWorker(env(base("split", rest)))
		if err == nil || !slices.Equal(codes(t, err), []string{"WORKER_REST_LANES:invalid"}) || strings.Contains(err.Error(), rest) {
			t.Errorf("split, %q: %v", rest, err)
		}
	}
	// An unknown mode is reported alone; a count out of range besides it.
	if _, err := LoadWorker(env(base("both", "2"))); err == nil || !slices.Equal(codes(t, err), []string{"WORKER_LANES:invalid"}) {
		t.Errorf("both, 2: %v", err)
	}
	if _, err := LoadWorker(env(base("both", "9"))); err == nil || !slices.Equal(codes(t, err), []string{"WORKER_LANES:invalid", "WORKER_REST_LANES:invalid"}) {
		t.Errorf("both, 9: %v", err)
	}
	// The rest lanes after the first are <WORKER_ID>:rest2 to :rest4, one
	// character longer than :rest (D-092: an actor reference is at most 128).
	long := base("split", "2")
	long["WORKER_ID"] = strings.Repeat("w", 122)
	for _, rest := range []string{"2", "3", "4"} {
		long["WORKER_REST_LANES"] = rest
		if w, err := LoadWorker(env(long)); err != nil || w.RestLanes != int(rest[0]-'0') {
			t.Errorf("122 characters, %s rest lanes: %+v %v", rest, w, err)
		}
	}
	long["WORKER_ID"] = strings.Repeat("w", 123)
	if _, err := LoadWorker(env(long)); err == nil || !slices.Equal(codes(t, err), []string{"WORKER_ID:invalid"}) || !strings.Contains(err.Error(), "WORKER_REST_LANES") {
		t.Errorf("123 characters, 4 rest lanes: %v", err)
	}
	long["WORKER_REST_LANES"] = "1"
	if w, err := LoadWorker(env(long)); err != nil || w.RestLanes != 1 {
		t.Errorf("123 characters, 1 rest lane: %+v %v", w, err)
	}
	// Every problem is reported together, as before.
	if _, err := LoadWorker(env(map[string]string{"WORKER_REST_LANES": "2"})); err == nil ||
		!slices.Equal(codes(t, err), []string{"WORKER_ID:required", "WORKER_ORGANIZATION_IDS:required", "WORKER_REST_LANES:invalid"}) {
		t.Errorf("nothing valid: %v", err)
	}
}

// The lanes' suffixes: :sim, then :rest (W1's name of the one rest lane,
// unchanged), :rest2, :rest3 and :rest4.
func TestRestLaneSuffixes(t *testing.T) {
	var got []string
	for i := 1; i <= MaxRestLanes; i++ {
		got = append(got, RestLaneSuffixOf(i))
	}
	if want := []string{":rest", ":rest2", ":rest3", ":rest4"}; !slices.Equal(got, want) || MaxRestLanes != 4 || SimLaneSuffix != ":sim" {
		t.Errorf("%v, want %v (max %d)", got, want, MaxRestLanes)
	}
}
