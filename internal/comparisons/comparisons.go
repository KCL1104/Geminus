// Package comparisons is the baseline/scenario comparison service and its
// HTTP adapter (T-029; compareSimulations, SIMULATION_SPEC §7, D-010; the
// binding is D-089, PROPOSED): any member of an organization compares two
// of its completed runs. The comparison itself is internal/results/compare
// (pure, in the generic kernel), so the API and a later analysis job give
// the same answer for the same runs; this package only reads the runs and
// their results in the caller's scope and maps the outcome to HTTP.
//
// One tenant.DB.Run transaction (REPEATABLE READ, read-only) checks the
// caller's membership and role first (D-008), then reads both runs and
// both results through internal/storage/runs, which filters every statement
// by the organization on top of RLS: another organization's run is not
// found, exactly like a run that does not exist (TEST-009). A run that is
// not completed has no result to compare (409, as getSimulationResults).
// Reading changes nothing and writes no audit event.
package comparisons

import (
	"context"
	"errors"
	"fmt"

	"github.com/kcl1104/geminus/internal/model"
	"github.com/kcl1104/geminus/internal/results/compare"
	"github.com/kcl1104/geminus/internal/simulation"
	store "github.com/kcl1104/geminus/internal/storage/runs"
	"github.com/kcl1104/geminus/internal/tenant"
)

var (
	// ErrNotFound: no such run in the organization (or none the caller
	// may see); the same for both (TEST-009).
	ErrNotFound = errors.New("comparisons: no such run")
	// ErrNotReady: the run is not completed, so it has no result (409
	// RESULT_NOT_READY; API_DATABASE §6).
	ErrNotReady = errors.New("comparisons: the run has no result")
)

// The query parameters (openapi compareSimulations), which also name the
// run an error is about.
const (
	BaselineParam = "baseline_run_id"
	ScenarioParam = "scenario_run_id"
)

// RunError is ErrNotFound or ErrNotReady for the run of one parameter.
type RunError struct {
	Param string // BaselineParam or ScenarioParam
	Err   error
}

func (e *RunError) Error() string { return "comparisons: " + e.Param + ": " + e.Err.Error() }
func (e *RunError) Unwrap() error { return e.Err }

// readRoles: any member compares (x-required-roles, planning/api-operations.json).
var readRoles = tenant.Members

// inTransaction, when this package's DB test sets it, runs inside the
// comparison's transaction right after the role check: the only way to
// observe that transaction's mode from outside the service (review round 1
// L6; tenant's own TestReadSnapshot shows what the mode does). It is nil
// in production and unexported, so nothing outside the package can set it.
var inTransaction func(ctx context.Context, tx tenant.Tx) error

// Service compares runs for a verified scope.
type Service struct {
	DB *tenant.DB
}

// Compare returns the comparison of the scenario run with the baseline run
// as RFC 8785 JSON (http.schema.json#/$defs/Comparison). The baseline is
// checked before the scenario, and both runs' existence before either's
// result (404 before 409).
func (s *Service) Compare(ctx context.Context, sc tenant.Scope, baselineID, scenarioID string) ([]byte, error) {
	if baselineID == scenarioID {
		return nil, compare.ErrSameRun
	}
	params := [2]string{BaselineParam, ScenarioParam}
	ids := [2]string{baselineID, scenarioID}
	var inputs [2]compare.Run
	err := s.DB.Run(ctx, sc, tenant.ReadSnapshot, func(ctx context.Context, tx tenant.Tx, m tenant.Membership) error {
		if err := m.Require(readRoles...); err != nil {
			return err
		}
		if inTransaction != nil {
			if err := inTransaction(ctx, tx); err != nil {
				return err
			}
		}
		var rows [2]store.Run
		for i, id := range ids {
			r, err := store.Get(ctx, tx, m.OrganizationID, id)
			if errors.Is(err, store.ErrNotFound) {
				return &RunError{Param: params[i], Err: ErrNotFound}
			}
			if err != nil {
				return err
			}
			rows[i] = r
		}
		for i, r := range rows {
			if r.Status != store.Completed {
				return &RunError{Param: params[i], Err: ErrNotReady}
			}
		}
		for i, r := range rows {
			stored, err := store.GetResult(ctx, tx, m.OrganizationID, r.ID)
			if errors.Is(err, store.ErrNotFound) {
				// 0009 commits a result with its run's completion.
				return fmt.Errorf("%w: completed run %s has no result", compare.ErrInconsistent, r.ID)
			}
			if err != nil {
				return err
			}
			res, err := decodeResult(stored.Result)
			if err != nil {
				return fmt.Errorf("run %s: %w", r.ID, err)
			}
			inputs[i] = input(r, res)
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	// After the transaction: the comparison is computation only.
	c, err := compare.Compare(inputs[0], inputs[1])
	if err != nil {
		return nil, err
	}
	return c.JSON()
}

// input is what the comparison reads of a run: its frozen input and result.
func input(r store.Run, res *simulation.Result) compare.Run {
	return compare.Run{ID: r.ID, ModelVersionID: r.ModelVersionID, EngineVersion: r.EngineVersion, DatasetManifestHash: r.DatasetManifestHash,
		ModelHash: r.ModelHash, EffectiveModelHash: r.EffectiveModelHash, ConfigHash: r.ConfigHash,
		ScenarioPatch: r.ScenarioPatch, Config: r.Config, Result: res}
}

// decodeResult decodes a stored Result strictly (no unknown or duplicate
// member): one that does not decode is a server fault, never compared.
func decodeResult(data []byte) (*simulation.Result, error) {
	var res simulation.Result
	if err := model.Decode(data, &res); err != nil {
		return nil, fmt.Errorf("%w: the stored result does not decode: %v", compare.ErrInconsistent, err)
	}
	return &res, nil
}
