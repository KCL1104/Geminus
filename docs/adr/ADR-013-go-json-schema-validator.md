# ADR-013 — Go JSON Schema validator and embedded schemas

Status: ACCEPTED (2026-10-19, owner H-010; chosen by the T-004 agent). Date: 2026-10-15.

## Decision

The schema stage of the Go server uses `github.com/santhosh-tekuri/jsonschema/v6` v6.0.3 (Apache-2.0, 2026-06-28), which depends on `golang.org/x/text` v0.42.0 (BSD-3-Clause, 2026-09-08). `schemas/embed.go` compiles `schemas/*.schema.json` into the binary with `go:embed`; the validator registers each file as a resource under its `$id` and refuses to load any other URL, so validation reads no file system and makes no network connection, and always validates against the same source of truth. `AssertFormat()` is on, so `date-time` and other formats are enforced the same way as in the Python spec checks.

## Reasons for the choice

- Supports JSON Schema 2020-12 and resolves cross-file `$ref` by `$id`, without rewriting the schemas.
- The error tree carries the instance location, so a precise JSON Pointer can be produced; `oneOf` errors keep only the branch whose discriminator (`type`/`op`) matches, and when none matches, report only the discriminator's allowed values.
- Some of the library's error messages list field names in Go map order, which the fuzz tests caught as non-determinism; for `AdditionalProperties` and `Required`, this project builds the message from a sorted list instead.

## Consequences

- The `golang.org/x/text` v0.14.0 brought in by the first version had two module-level vulnerabilities found by `govulncheck v1.9.0` (GO-2026-6629, GO-2026-5970; not reached by the code); it was upgraded to v0.42.0 and a rescan found 0. CI's backend job runs govulncheck and fails when a new dependency has a known vulnerability.
- `./schemas/...` is added to `GO_PKGS` of `make backend-*`.
- The conclusions of the Go and Python validators on the shared fixtures are compared by `TestAgreesWithSharedFixtures`; a behaviour change in either implementation makes the test fail.
