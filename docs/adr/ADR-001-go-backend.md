# ADR-001 — Go Backend

Status: SOURCE_LOCKED (per the original report; this does not mean that new engineering proposals have been approved by a human).

Source: the original report pp. 9–10, 50.

## Decision

Go is the P0 SaaS backend, with net/http / ordinary packages; the framework stays light.

## Consequences / Review boundary

The main advantage is the overall delivery risk; this is not a claim that Go is always faster than Rust. The SDK versions and sqlc still need to be chosen later.

Changing this architectural boundary of the original report requires a new ADR confirmed by a human; it cannot be replaced by a coding agent's preference.
