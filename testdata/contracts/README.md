# Contract fixtures

Use `cases.json` with `make spec-contracts`. Schema-positive does not mean authorized or semantically safe. Semantic negatives are deliberately schema-valid. The bundled lightweight semantic linter checks a subset only; it is not the Go deterministic validator.

All IDs, timestamps, model parameters, metrics and hashes are synthetic. `sha256:` followed by 64 zeroes is a shape placeholder; no content/hash consistency is claimed. Do not use these placeholders in production data.
