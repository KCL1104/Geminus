# Hashing vectors (T-005)

Inputs and expected values for `internal/model/hashing` (docs/TWIN_IR_SPEC.md §5.1, ADR-014). None of the expected values here are produced by the Go code under test.

| File | Source | Regenerate |
|---|---|---|
| `rfc8785-vectors.json` | Mechanically extracted from the RFC 8785 text (its SHA-256 is recorded in the file) | Re-extract only if the RFC text changes |
| `v8-number-vectors.txt` | `JSON.stringify` in Node/V8 for 6,000 IEEE-754 values | `node scripts/gen_number_vectors.mjs > testdata/hashing/v8-number-vectors.txt` (Node 24.21.0; deterministic). `node scripts/gen_number_vectors.mjs 1000000 > PATH` plus `HASHING_V8_VECTORS=PATH` runs an extended comparison |
| `configs.json`, `manifest.json` | Hand-written inputs; `same_as` / `expected_order` state the intended equalities | — |
| `expected-hashes.json` | Python `rfc8785` 0.1.4 via `scripts/check_hashes.py` | `python scripts/check_hashes.py --write`, then review the diff: a changed hash is a hash-version change |
| `rfc8785-documents.jsonl` | 400 random documents (seed 8785) and their Python `rfc8785` canonical bytes; member names mix astral and U+E000–U+FFFF characters, where UTF-16 order differs from code point order | Same `--write` run; `--documents N PATH` writes a larger corpus for `HASHING_PY_DOCUMENTS` |

`make spec-hashes` recomputes everything in Python and also checks that the hash members of `testdata/contracts/valid-model-version.json`, `valid-scenario.json` and `valid-result.json` match their content. Other `sha256:000…` values in contract fixtures are format-only placeholders.
