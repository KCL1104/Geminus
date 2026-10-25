package comparisons

import (
	"errors"
	"log/slog"
	"net/http"
	"regexp"

	"github.com/kcl1104/geminus/internal/auth"
	"github.com/kcl1104/geminus/internal/httpapi"
	"github.com/kcl1104/geminus/internal/results/compare"
	"github.com/kcl1104/geminus/internal/tenant"
)

// Route is one implemented operation of openapi.yaml.
type Route struct {
	Method, Path, OperationID string
	Roles                     []tenant.Role // x-required-roles
}

// Routes are the operations this package serves. A contract test compares
// them with planning/api-operations.json; their roles are the ones the
// service requires.
var Routes = []Route{
	{"GET", "/v1/simulation-comparisons", "compareSimulations", readRoles},
}

// Register adds the comparison route to mux.
func Register(mux *http.ServeMux, log *slog.Logger, v auth.Verifier, users httpapi.Resolver, s *Service) {
	h := handlers{s: s, log: log}
	for _, r := range Routes {
		mux.Handle(r.Method+" "+r.Path, httpapi.Handler(log, httpapi.Scoped(v, users, h.compare)))
	}
}

type handlers struct {
	s   *Service
	log *slog.Logger
}

var runIDs = regexp.MustCompile(`^sim_[0-7][0-9A-HJKMNP-TV-Z]{25}$`)

func badParam(param, message string) error {
	return &httpapi.Error{Code: httpapi.InvalidRequest, Message: message, Details: []httpapi.Detail{{Path: "", Reason: param}}}
}

// runQuery reads the two run IDs: each parameter exactly once, a
// simulation run ID, and two different runs (400 naming the parameter
// otherwise; D-089 (a)). Other parameters are ignored, as the list routes
// ignore them.
func runQuery(r *http.Request) (baseline, scenario string, err error) {
	q, err := httpapi.Query(r)
	if err != nil {
		return "", "", err
	}
	var got [2]string
	for i, p := range []string{BaselineParam, ScenarioParam} {
		vs := q[p]
		if len(vs) != 1 || !runIDs.MatchString(vs[0]) {
			return "", "", badParam(p, p+" must be one simulation run ID")
		}
		got[i] = vs[0]
	}
	if got[0] == got[1] {
		return "", "", badParam(ScenarioParam, "baseline_run_id and scenario_run_id must name two different runs")
	}
	return got[0], got[1], nil
}

// Refused maps the service's errors to what the client sees (openapi error
// codes; details[0].reason names the parameter of the run); tenant and
// transport errors pass to httpapi, and anything else (a stored result
// that does not match its run, compare.ErrInconsistent, among them) is
// INTERNAL_ERROR there, logged.
func Refused(err error) error {
	var re *RunError
	switch {
	case errors.As(err, &re) && errors.Is(re.Err, ErrNotFound):
		return &httpapi.Error{Code: httpapi.NotFound, Message: "not found", Details: []httpapi.Detail{{Path: "", Reason: re.Param}}}
	case errors.As(err, &re) && errors.Is(re.Err, ErrNotReady):
		return &httpapi.Error{Code: httpapi.ResultNotReady, Message: "the run has no result yet; compare completed runs only",
			Details: []httpapi.Detail{{Path: "", Reason: re.Param}}}
	case errors.Is(err, compare.ErrSameRun):
		return badParam(ScenarioParam, "baseline_run_id and scenario_run_id must name two different runs")
	}
	return err
}

// compare serves compareSimulations: the query's own checks, then the
// service, which checks the caller's role before reading anything.
func (h handlers) compare(w http.ResponseWriter, r *http.Request, sc tenant.Scope) error {
	baseline, scenario, err := runQuery(r)
	if err != nil {
		return err
	}
	out, err := h.s.Compare(r.Context(), sc, baseline, scenario)
	if err != nil {
		return Refused(err)
	}
	httpapi.WriteBody(w, 200, out)
	return nil
}
