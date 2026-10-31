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
