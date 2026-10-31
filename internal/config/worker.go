package config

import (
	"errors"
	"strconv"
	"strings"

	"github.com/kcl1104/geminus/internal/audit"
)

// Worker is the job worker's identity and assignment (T-019).
type Worker struct {
	// ID names the worker in the leases it holds (WORKER_ID): 1–200
	// visible ASCII characters, unique per running worker so a lease shows
	// who holds it. Leases are fenced by their token, not by this name.
	ID string
	// Organizations is the deployment's allowlist (D-008,
	// WORKER_ORGANIZATION_IDS, comma-separated) in the order the worker
	// serves them; internal/tenant/workerscope checks that each is an
	// organization ID listed once.
	Organizations []string
	// Lanes is how many job loops the process runs (WORKER_LANES).
	Lanes Lanes
	// RestLanes is how many loops split runs for the types other than
	// simulations (WORKER_REST_LANES): 1 (the default) to MaxRestLanes;
	// 0 in single mode, which has no rest lane.
	RestLanes int
}

// Lanes is WORKER_LANES (H-056 design §9, W1): one job loop for every job
// type, or two in the same process on the same organizations, so a
// simulation never waits behind a long AI job.
type Lanes string

const (
	// LanesSingle (the default; unset is single): one loop, holding its
	// leases as WORKER_ID, for every type the build handles.
	LanesSingle Lanes = "single"
	// LanesSplit: a loop for simulations, holding its leases as
	// <WORKER_ID>:sim, and WORKER_REST_LANES loops for every other type (AI
	// proposals, analyses, imports, and any type added later), as
	// <WORKER_ID>:rest, <WORKER_ID>:rest2, ...
	LanesSplit Lanes = "split"
)

// The split lanes' suffixes of WORKER_ID in their leases and audit events:
// the sim lane's, and the first rest lane's (RestLaneSuffixOf names the
// others).
const (
	SimLaneSuffix  = ":sim"
	RestLaneSuffix = ":rest"
)

// MaxRestLanes is the most rest lanes WORKER_REST_LANES may ask for. The
// worker also refuses more rest lanes than the AI calls one organization
// may have in flight (cmd/worker; AI_MAX_CONCURRENT_PER_ORGANIZATION,
// 2 by default), which config does not check: the default is the
// provider's.
const MaxRestLanes = 4

// RestLaneSuffixOf is the suffix of rest lane i (1 to MaxRestLanes): the
// first is ":rest", W1's name of the one rest lane, unchanged; the others
// ":rest2", ":rest3", ...
func RestLaneSuffixOf(i int) string {
	if i == 1 {
		return RestLaneSuffix
	}
	return RestLaneSuffix + strconv.Itoa(i)
}

// maxActor is the longest actor reference (audit.ValidActor, D-092).
const maxActor = 128

// LoadWorker reads WORKER_ID and WORKER_ORGANIZATION_IDS, both required,
// WORKER_LANES and WORKER_REST_LANES. All problems are reported together,
// as in Load.
func LoadWorker(getenv func(string) string) (Worker, error) {
	var errs []error
	w := Worker{ID: getenv("WORKER_ID")}
	switch Lanes(getenv("WORKER_LANES")) {
	case "", LanesSingle:
		w.Lanes = LanesSingle
	case LanesSplit:
		w.Lanes = LanesSplit
	}
	// The count is read exactly: "1" to "4", no sign, zero or space.
	rest, restErr := 1, error(nil)
	if raw := getenv("WORKER_REST_LANES"); raw != "" {
		n, err := strconv.Atoi(raw)
		switch {
		case err != nil || strconv.Itoa(n) != raw || n < 1 || n > MaxRestLanes:
			restErr = &Error{"WORKER_REST_LANES", CodeInvalid, "allowed with WORKER_LANES=split: 1 (the default) to 4, the loops for the job types other than simulations"}
		case w.Lanes == LanesSingle:
			restErr = &Error{"WORKER_REST_LANES", CodeInvalid, "only with WORKER_LANES=split: single runs one loop for every job type, and no rest lane"}
		default:
			rest = n
		}
	}
	if w.Lanes == LanesSplit {
		w.RestLanes = rest
	}
	switch {
	case w.ID == "":
		errs = append(errs, &Error{"WORKER_ID", CodeRequired, "a name for this worker's leases is required"})
	case !audit.ValidActor(w.ID):
		errs = append(errs, &Error{"WORKER_ID", CodeInvalid,
			"must start with a letter and use only letters, digits, '_', '.', ':' and '-' (at most 128): it names the worker in audit events (D-092)"})
	case w.Lanes == LanesSplit && len(w.ID)+len(RestLaneSuffixOf(w.RestLanes)) > maxActor:
		errs = append(errs, &Error{"WORKER_ID", CodeInvalid,
			"at most 123 characters with WORKER_LANES=split, 122 with WORKER_REST_LANES of 2 or more: the lanes are named <WORKER_ID>:sim, <WORKER_ID>:rest, <WORKER_ID>:rest2, ... in leases and audit events (D-092)"})
	}
	switch raw := getenv("WORKER_ORGANIZATION_IDS"); {
	case strings.TrimSpace(raw) == "":
		errs = append(errs, &Error{"WORKER_ORGANIZATION_IDS", CodeRequired, "the organizations this worker serves are required (D-008)"})
	default:
		for o := range strings.SplitSeq(raw, ",") {
			w.Organizations = append(w.Organizations, strings.TrimSpace(o))
		}
	}
	if w.Lanes == "" {
		errs = append(errs, &Error{"WORKER_LANES", CodeInvalid, `allowed: "single" (the default) or "split"`})
	}
	if restErr != nil {
		errs = append(errs, restErr)
	}
	if len(errs) > 0 {
		return Worker{}, errors.Join(errs...)
	}
	return w, nil
}
