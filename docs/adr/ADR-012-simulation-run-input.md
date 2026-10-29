# ADR-012 — SimulationEngine dataset injection: explicit RunInput

Status: APPROVED (2026-10-15, KCL1104 chose 「明確的 RunInput」 ("explicit RunInput") in T-003). Source: docs/ARCHITECTURE.md §3, T-003 scope.

## Decision

The Run signature of `internal/simulation.Engine` is `Run(ctx, RunInput, ProgressSink)`, replacing the original report's literal `Run(ctx, ModelIR, RunConfig, ProgressSink)`. `RunInput` brings in, at once, the effective IR, the effective model hash, RunConfig, ExecutionLimits, the validated datasets by logical_ref, and the dataset manifest sorted by logical_ref.

## Consequences

- "All inputs are frozen before Run" is guaranteed by the type; the engine has no resolver, DB, object storage or network entry point, so retries and replays read the same input.
- RunConfig stays a pure API payload; no non-serialized internal fields are hidden in it, so that a RunConfig decoded from JSON does not "quietly have no datasets".
- The difference from the original report's signature is only in how the parameters are packed; the Validate and ProgressSink concepts do not change. `ProgressSink.Report` carries only the replications/events counts the engine owns; run identity, status and revision are added by the worker.

## Implementation addendum (T-022, 2026-10-15)

- `RunInput.ModelHash`: the model_hash of the pinned ModelVersion (before the patch). It cannot be recomputed from the effective IR, so the worker freezes it and records it as is. `EffectiveModelHash` is recomputed by the engine with `hashing.Model(model.Encode(Model))`, and a mismatch is rejected.
- `des.Engine.RunWithTrace` also returns the trace artifact (`simulation.TraceArtifact`); `Run` returns only the Result, matching the `simulation.Engine` interface. Result.run_id and trace.artifact_id are filled in by the worker.
- The DomainPack's metrics are injected as a function (`des.Engine.DomainMetrics`), so the engine does not import any domain.
