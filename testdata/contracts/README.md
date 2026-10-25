# Contract fixtures

Use `cases.json` with `make spec-contracts`. Schema-positive does not mean authorized or semantically safe. Semantic negatives are deliberately schema-valid. The bundled lightweight semantic linter checks a subset only; it is not the Go deterministic validator.

All IDs, timestamps, model parameters and metrics are synthetic. `sha256:` followed by 64 zeroes is a shape placeholder; no content/hash consistency is claimed for it. Do not use these placeholders in production data. Exceptions since T-005: `valid-model-version.json` `hash`, `valid-scenario.json` `patch_hash` (and, since T-028, `valid-manual-scenario.json` `patch_hash`; the other scenario fixtures and examples reuse these two hashes for the same patches) and `valid-result.json` `reproducibility.result_digest` are the real hashes of their own content, checked by `make spec-hashes` and `internal/model/hashing` tests (see `testdata/hashing/README.md`). They hash fixture content; they do not claim an engine produced the result.

## Comparison golden vector (T-029)

`valid-comparison.json` is the comparison of `valid-result.json` (baseline, frozen patch `[]`) with `valid-result-scenario.json` (the same synthetic run with two servers; its `result_digest` is the zero placeholder), both of one replication over [0, 1010 min] with seed 7. `internal/results/compare` `TestGolden` must produce it byte for byte (RFC 8785). The values were derived by hand from the two results, not by that code (IEEE-754 binary64; checked with Python floats, which are the same type):

| Metric path | Baseline | Scenario | absolute = s − b | relative = absolute / b | Reason |
|---|---|---|---|---|---|
| `/metrics/throughput/{mean,p50,p95}` | 100 | 110 | 10 | 10/100 = 0.1 | — |
| `/metrics/lead_time_minutes/{mean,p50,p95}` | 5 | 6.5 | 1.5 | 1.5/5 = 0.3 | — |
| `/metrics/failure_count/{mean,p50,p95}` | 0 | 2 | 2 | null | `BASELINE_ZERO` |
| `/metrics/rework_count/{mean,p50,p95}` | 0 | 0 | 0 | null | `BASELINE_ZERO` |
| `/metrics/dora/change_lead_time/{mean,p50,p95}` | null | null | null | null | `BOTH_UNAVAILABLE` (no DORA mapping, D-010) |
| `/metrics/resources/server/utilization_mean` | 0.49504950495049505 (50/101) | 0.24752475247524752 (25/101, two servers) | −0.24752475247524752 (exact: halving and Sterbenz) | −0.5 (exact) | — |
| `/metrics/queues/service/{wait_mean,wait_p50,wait_p95}_minutes`, `queue_length_mean` | 0 | 0 | 0 | null | `BASELINE_ZERO` |

Order: run-level metrics (throughput, lead time, failures, reworks), then `additional_metrics` (none here), `dora`, `domain` (none), `resources`, `queues`; statistics in the order mean, p50, p95 (queues: the three waits, then the length). Warnings: `SINGLE_REPLICATION` only (one replication; the scenario's patch is not `[]`; nothing censored). `valid-comparison-incomparable.json` and the `invalid-comparison-*.json` negatives are shape fixtures: they are not the output of any pair of results here.
