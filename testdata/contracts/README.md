# Contract fixtures

Use `cases.json` with `make spec-contracts`. Schema-positive does not mean authorized or semantically safe. Semantic negatives are deliberately schema-valid. The bundled lightweight semantic linter checks a subset only; it is not the Go deterministic validator.

All IDs, timestamps, model parameters and metrics are synthetic. `sha256:` followed by 64 zeroes is a shape placeholder; no content/hash consistency is claimed for it. Do not use these placeholders in production data. Exceptions since T-005: `valid-model-version.json` `hash`, `valid-scenario.json` `patch_hash` and `valid-result.json` `reproducibility.result_digest` are the real hashes of their own content, checked by `make spec-hashes` and `internal/model/hashing` tests (see `testdata/hashing/README.md`). They hash fixture content; they do not claim an engine produced the result.
