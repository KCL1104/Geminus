package config

import (
	"errors"
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
}

// LoadWorker reads WORKER_ID and WORKER_ORGANIZATION_IDS; both are
// required. All problems are reported together, as in Load.
func LoadWorker(getenv func(string) string) (Worker, error) {
	var errs []error
	w := Worker{ID: getenv("WORKER_ID")}
	switch {
	case w.ID == "":
		errs = append(errs, &Error{"WORKER_ID", CodeRequired, "a name for this worker's leases is required"})
	case !audit.ValidActor(w.ID):
		errs = append(errs, &Error{"WORKER_ID", CodeInvalid,
			"must start with a letter and use only letters, digits, '_', '.', ':' and '-' (at most 128): it names the worker in audit events (D-092)"})
	}
	switch raw := getenv("WORKER_ORGANIZATION_IDS"); {
	case strings.TrimSpace(raw) == "":
		errs = append(errs, &Error{"WORKER_ORGANIZATION_IDS", CodeRequired, "the organizations this worker serves are required (D-008)"})
	default:
		for o := range strings.SplitSeq(raw, ",") {
			w.Organizations = append(w.Organizations, strings.TrimSpace(o))
		}
	}
	if len(errs) > 0 {
		return Worker{}, errors.Join(errs...)
	}
	return w, nil
}
