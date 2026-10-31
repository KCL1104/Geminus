package config

import (
	"errors"
	"slices"
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
	// AgentLanes is how many loops split runs for AI investigations only
	// (WORKER_AGENT_LANES, H-061 §5.1): 0 (the default) to MaxAgentLanes;
	// run only while Investigations is on.
	AgentLanes int
	// Investigations is GEMINUS_INVESTIGATIONS=1 (H-061; off by default and
	// off on live until every slice is reviewed and the closed-loop live
	// checks pass): the investigation job's handler on the agent lanes.
	// It needs split lanes and at least one agent lane.
	Investigations bool
	// Transcript is INVESTIGATOR_TRANSCRIPT: "digest" (the default) or
	// "replay" (only once H-061 A1's probe P1 passes).
	Transcript string
	// PlannerModels are the planner models this worker runs
	// (INVESTIGATION_PLANNER_MODELS, comma-separated; muse-spark-1.3 by
	// default): Muse Wonder 1.0 only once the coordinator lists it after
	// its recorded evaluation (H-061 §14).
	PlannerModels []string
}

// MaxAgentLanes is the most agent lanes WORKER_AGENT_LANES may ask for.
const MaxAgentLanes = 4

// AgentLaneSuffix is the agent lanes' suffix of WORKER_ID, numbered:
// :agent1, :agent2, ... (AgentLaneSuffixOf).
const AgentLaneSuffix = ":agent"

// AgentLaneSuffixOf is the suffix of agent lane i (1 to MaxAgentLanes).
func AgentLaneSuffixOf(i int) string { return AgentLaneSuffix + strconv.Itoa(i) }

// PlannerModels are the models an investigation may name as its planner
// (common.schema.json PlannerModel).
var PlannerModels = []string{"muse-spark-1.3", "muse-wonder-1.0"}

// Lanes is WORKER_LANES (H-056 design §9, W1): one job loop for every job
// type, or, in the same process on the same organizations, a loop for
// simulations and WORKER_REST_LANES loops for the other types, so a
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
	invErrs := loadInvestigations(getenv, &w)
	switch {
	case w.ID == "":
		errs = append(errs, &Error{"WORKER_ID", CodeRequired, "a name for this worker's leases is required"})
	case !audit.ValidActor(w.ID):
		errs = append(errs, &Error{"WORKER_ID", CodeInvalid,
			"must start with a letter and use only letters, digits, '_', '.', ':' and '-' (at most 128): it names the worker in audit events (D-092)"})
	case w.Lanes == LanesSplit && len(w.ID)+len(RestLaneSuffixOf(w.RestLanes)) > maxActor:
		errs = append(errs, &Error{"WORKER_ID", CodeInvalid,
			"at most 123 characters with WORKER_LANES=split, 122 with WORKER_REST_LANES of 2 or more: the lanes are named <WORKER_ID>:sim, <WORKER_ID>:rest, <WORKER_ID>:rest2, ... in leases and audit events (D-092)"})
	case w.Lanes == LanesSplit && w.AgentLanes > 0 && len(w.ID)+len(AgentLaneSuffixOf(w.AgentLanes)) > maxActor:
		errs = append(errs, &Error{"WORKER_ID", CodeInvalid,
			"at most 121 characters with WORKER_AGENT_LANES: the agent lanes are named <WORKER_ID>:agent1, <WORKER_ID>:agent2, ... in leases and audit events (D-092)"})
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
	errs = append(errs, invErrs...)
	if len(errs) > 0 {
		return Worker{}, errors.Join(errs...)
	}
	return w, nil
}

// loadInvestigations reads H-061's worker settings into w (whose lanes are
// read): WORKER_AGENT_LANES, GEMINUS_INVESTIGATIONS, INVESTIGATOR_TRANSCRIPT
// and INVESTIGATION_PLANNER_MODELS, each exactly (never a guess at what was
// meant), and returns their problems.
func loadInvestigations(getenv func(string) string, w *Worker) []error {
	var errs []error
	if raw := getenv("WORKER_AGENT_LANES"); raw != "" {
		n, err := strconv.Atoi(raw)
		if err != nil || strconv.Itoa(n) != raw || n < 0 || n > MaxAgentLanes {
			errs = append(errs, &Error{"WORKER_AGENT_LANES", CodeInvalid, "0 (the default) to 4: the loops that run AI investigations only (H-061)"})
		} else {
			w.AgentLanes = n
		}
	}
	switch raw := getenv("GEMINUS_INVESTIGATIONS"); raw {
	case "", "0":
	case "1":
		w.Investigations = true
		if w.Lanes != LanesSplit || w.AgentLanes < 1 {
			errs = append(errs, &Error{"GEMINUS_INVESTIGATIONS", CodeInvalid,
				"needs WORKER_LANES=split and WORKER_AGENT_LANES of 1 to 4: in one loop an investigation would wait for jobs only that loop could run (H-061 §5.1)"})
		}
	default:
		errs = append(errs, &Error{"GEMINUS_INVESTIGATIONS", CodeInvalid, `"1" turns AI investigations on; unset or "0" leaves them off`})
	}
	switch raw := getenv("INVESTIGATOR_TRANSCRIPT"); raw {
	case "", "digest":
		w.Transcript = "digest"
	case "replay":
		w.Transcript = "replay"
	default:
		errs = append(errs, &Error{"INVESTIGATOR_TRANSCRIPT", CodeInvalid, `"digest" (the default) or "replay"`})
	}
	w.PlannerModels = []string{"muse-spark-1.3"}
	if raw := getenv("INVESTIGATION_PLANNER_MODELS"); raw != "" {
		var list []string
		ok := true
		for m := range strings.SplitSeq(raw, ",") {
			m = strings.TrimSpace(m)
			if !slices.Contains(PlannerModels, m) || slices.Contains(list, m) {
				ok = false
				break
			}
			list = append(list, m)
		}
		if !ok || len(list) == 0 {
			errs = append(errs, &Error{"INVESTIGATION_PLANNER_MODELS", CodeInvalid,
				"a comma-separated list of muse-spark-1.3 and muse-wonder-1.0, each once (H-061 §14)"})
		} else {
			w.PlannerModels = list
		}
	}
	return errs
}
