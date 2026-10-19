package runs

import (
	"context"
	"errors"

	"github.com/jackc/pgx/v5"
)

// Failure codes a simulation job records (jobs.Failure.Code); its run keeps
// the failure when it fails (0009), and the API shows it as the run's error
// (internal/runs). The job loop's own codes (INTERNAL, NO_RESULT,
// WORKER_STOPPED) and the reaper's (LEASE_EXPIRED) appear as well.
const (
	// The run exceeded one of its frozen execution limits (D-007): never
	// retried, and the logical budgets hold across retries.
	EventBudgetExceeded    = "EVENT_BUDGET_EXCEEDED"
	WallTimeoutExceeded    = "WALL_TIMEOUT_EXCEEDED"
	ActiveEntitiesExceeded = "ACTIVE_ENTITIES_EXCEEDED"
	EntityVisitsExceeded   = "ENTITY_VISITS_EXCEEDED"
	// RunInputInvalid: the frozen input no longer compiles or verifies (a
	// dataset object missing or changed, say): never retried.
	RunInputInvalid = "RUN_INPUT_INVALID"
	// EngineVersionMismatch: the worker's engine is not the one the run
	// was frozen for; another numerical profile would give another result.
	EngineVersionMismatch = "ENGINE_VERSION_MISMATCH"
	// EngineInternal: the engine broke an invariant (a bug).
	EngineInternal = "ENGINE_INTERNAL"
	// ArtifactStorageFailed: the trace could not be stored (retried).
	ArtifactStorageFailed = "ARTIFACT_STORAGE_FAILED"
)

// Scenario is the part of a twin.scenarios row a run freezes.
type Scenario struct {
	ID, ModelID, BaseVersionID string
	Patch                      []byte
}

// GetScenario returns scenario id of organization org (T-028 creates them).
func GetScenario(ctx context.Context, tx pgx.Tx, org, id string) (Scenario, error) {
	s := Scenario{ID: id}
	err := tx.QueryRow(ctx, `SELECT model_id, base_version_id, patch FROM twin.scenarios WHERE organization_id=$1 AND id=$2`, org, id).
		Scan(&s.ModelID, &s.BaseVersionID, &s.Patch)
	if errors.Is(err, pgx.ErrNoRows) {
		return Scenario{}, ErrNotFound
	}
	return s, err
}
