package config

import (
	"errors"
	"regexp"
	"slices"
	"strings"
)

// Investigations is the API's side of the AI investigations (H-061 §6, §14;
// slice A7; behind the switch, off by default): whether its routes and its
// background executor run, the organizations the executor carries out
// recorded tool calls in, and the planners a person may choose.
type Investigations struct {
	// Enabled is GEMINUS_INVESTIGATIONS=1. Off (unset or "0"), every
	// investigation route answers 503 SERVICE_UNAVAILABLE
	// (INVESTIGATIONS_OFF) and changes nothing, and no executor runs.
	Enabled bool
	// Organizations are INVESTIGATION_ORGANIZATION_IDS (comma-separated, in
	// order): the organizations the executor polls, one per transaction
	// (internal/tenant/actingfor), and the only ones createInvestigation
	// accepts (503 for any other). On Railway it is a reference to the
	// worker's WORKER_ORGANIZATION_IDS. Required, and read, only once
	// Enabled: off, it is not used, so a deployment that declares it before
	// the switch is turned on starts as before whatever it holds.
	Organizations []string
	// PlannerModels are INVESTIGATION_PLANNER_MODELS (comma-separated):
	// the planners createInvestigation takes (422 at /planner_model for any
	// other). Muse Spark 1.3 when unset; Muse Wonder 1.0 only once its
	// evaluation passed (§14 gate 2, set by the coordinator).
	PlannerModels []string
}

// PlannerModels are the planners the contract names (common.schema.json
// PlannerModel); DefaultPlannerModel is the one used when a request names
// none.
var PlannerModels = []string{"muse-spark-1.3", "muse-wonder-1.0"}

const DefaultPlannerModel = "muse-spark-1.3"

var investigationOrgID = regexp.MustCompile(`^org_[0-7][0-9A-HJKMNP-TV-Z]{25}$`)

// LoadInvestigations reads GEMINUS_INVESTIGATIONS,
// INVESTIGATION_ORGANIZATION_IDS and INVESTIGATION_PLANNER_MODELS. All
// problems are reported together, as in Load; nothing is guessed: a value
// that is not exactly one of the allowed forms is refused.
func LoadInvestigations(getenv func(string) string) (Investigations, error) {
	var errs []error
	var inv Investigations
	switch getenv("GEMINUS_INVESTIGATIONS") {
	case "", "0":
	case "1":
		inv.Enabled = true
	default:
		errs = append(errs, &Error{"GEMINUS_INVESTIGATIONS", CodeInvalid, `allowed: "1" (on), "0" or unset (off, the default)`})
	}
	switch raw := getenv("INVESTIGATION_ORGANIZATION_IDS"); {
	case !inv.Enabled:
	case raw == "":
		errs = append(errs, &Error{"INVESTIGATION_ORGANIZATION_IDS", CodeRequired,
			"the organizations the investigation executor serves are required once GEMINUS_INVESTIGATIONS is 1 (D-008 as amended)"})
	default:
		orgs, ok := list(raw, func(s string) bool { return investigationOrgID.MatchString(s) })
		if ok {
			inv.Organizations = orgs
		} else {
			errs = append(errs, &Error{"INVESTIGATION_ORGANIZATION_IDS", CodeInvalid,
				"must be organization IDs, comma-separated, each listed once (the worker's WORKER_ORGANIZATION_IDS)"})
		}
	}
	inv.PlannerModels = []string{DefaultPlannerModel}
	if raw := getenv("INVESTIGATION_PLANNER_MODELS"); raw != "" {
		models, ok := list(raw, func(s string) bool { return slices.Contains(PlannerModels, s) })
		if ok {
			inv.PlannerModels = models
		} else {
			errs = append(errs, &Error{"INVESTIGATION_PLANNER_MODELS", CodeInvalid,
				"must name planner models of the contract, comma-separated, each once: Muse Spark 1.3 (the default) and, only after its evaluation passed, Muse Wonder 1.0"})
		}
	}
	if len(errs) > 0 {
		return Investigations{}, errors.Join(errs...)
	}
	return inv, nil
}

// list splits raw at commas, trimming the space around each item; ok is
// false when an item is empty, fails valid or is listed twice.
func list(raw string, valid func(string) bool) ([]string, bool) {
	var out []string
	for item := range strings.SplitSeq(raw, ",") {
		item = strings.TrimSpace(item)
		if item == "" || !valid(item) || slices.Contains(out, item) {
			return nil, false
		}
		out = append(out, item)
	}
	return out, true
}
