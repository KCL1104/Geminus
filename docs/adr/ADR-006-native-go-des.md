# ADR-006 — Native Go DES

Status: SOURCE_LOCKED (per the original report; this does not mean that new engineering proposals have been approved by a human).

Source: the original report pp. 20–22, 45–46.

## Decision

P0 builds its own Go DES with a limited set of primitives; SimPy is only for reference/testing.

## Consequences / Review boundary

Exact time/RNG/statistics are the D-005/D-006/D-010 proposals; do not claim that correctness is already proven.

Changing this architectural boundary of the original report requires a new ADR confirmed by a human; it cannot be replaced by a coding agent's preference.
