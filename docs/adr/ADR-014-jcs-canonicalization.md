# ADR-014 — RFC 8785 canonicalization library and hash verification

Status: ACCEPTED (chosen by the T-005 agent; owner KCL1104 agreed on 2026-10-15). Date: 2026-10-15. Preconditions: D-006, D-019, D-034 (all approved).

## Decision

`internal/model/hashing` uses `github.com/gowebpki/jcs` v1.0.2 (Apache-2.0, released 2026-09-21) for RFC 8785 canonicalization. It is the Go fork of the reference implementation by the RFC's author Anders Rundgren (cyberphone), maintained with the original author's agreement; the API is `jcs.Transform([]byte) ([]byte, error)`. `hashing.Canonical` sits in front of it:

- An independent pass with `encoding/json/jsontext`: it rejects duplicate members (including ones that are equal after unescaping), invalid UTF-8 and multiple top-level values. jcs itself also rejects these (1.6M differential fuzz runs found no input that jsontext rejects and jcs accepts); the second parser keeps the rules from depending on a single implementation, and provides the error kind and byte offset.
- Error messages carry only the byte offset; messages from jsontext/jcs that quote input tokens are not passed on.
- Numbers are binary64 per RFC 8785: documents that differ only beyond binary64 precision have the same hash; exact integer fields are limited to 2^53−1 by the schema. Rejecting integer literals above 2^53−1 was considered (Python `rfc8785` does this by type), but fuzzing showed that it makes Canonical reject its own output (`1e17` → `100000000000000000`) and would also leave patch candidates that Go re-marshals unable to compute an effective hash, so it is not adopted. For such input Go accepts and Python rejects; the two never give different hashes for the same input.

## Why it can be trusted: three independent oracles

| Oracle | Content | Location |
|---|---|---|
| The RFC 8785 text | The §3.2.2–§3.2.4 examples (byte by byte), the §3.2.3 UTF-16 ordering, all 26 numbers of Appendix B (including NaN/Infinity, which must raise an error) | `testdata/hashing/rfc8785-vectors.json`, extracted mechanically from the RFC text |
| V8 | The `JSON.stringify` results of 6,000 IEEE-754 values (boundaries, powers of 10, fixed/exponent switch points, random bit patterns); RFC §3.2.2.3 names V8 as the reference | `testdata/hashing/v8-number-vectors.txt`, reproducible with `scripts/gen_number_vectors.mjs` (Node 24.21.0) |
| Python `rfc8785` 0.1.4 (Trail of Bits, Apache-2.0) | All golden models, patches, effective models, configs (with a quantizer implemented independently with `Decimal`), manifests, result digests, content hashes; plus 400 random documents (including astral/private-use member names whose UTF-16 and code point orders differ, escapes, control characters) compared byte by byte | `scripts/check_hashes.py` → `testdata/hashing/expected-hashes.json`, `rfc8785-documents.jsonl`; `make spec-hashes` recomputes in CI |

The Go tests and the Python check read the same expected file, so CI fails as soon as either implementation changes behaviour. Extended runs (not in the repo): `HASHING_V8_VECTORS` points to the output of `gen_number_vectors.mjs 1000000`, and `HASHING_PY_DOCUMENTS` points to the output of `check_hashes.py --documents 100000 PATH`; T-005 ran them with 1,000,000 numbers and 100,000 documents, all in agreement (checks/T-005_VALIDATION.md).

## Consequences

- New direct dependency `github.com/gowebpki/jcs`; its `go.mod` brings only test dependencies (testify, yaml), which are not compiled into the binary. CI's govulncheck covers it.
- Only the digests on the tested platform (currently linux/amd64) are guaranteed (D-006); cross-architecture byte-identical output is not verified and must not be claimed.
- Switching the JCS implementation or profile changes existing hashes: all three oracles must pass first, and it is handled as a hash version change.
