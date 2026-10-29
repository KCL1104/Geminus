# ADR-005 — AI Cannot Mutate Model

Status: SOURCE_LOCKED (per the original report; this does not mean that new engineering proposals have been approved by a human).

Source: the original report pp. 3, 23–27, 41–43.

## Decision

AI only makes proposals; only the server's deterministic validator + human approval have a canonical effect.

## Consequences / Review boundary

The provider has no DB/write/run tools; the Critic cannot override the server's authority.

Changing this architectural boundary of the original report requires a new ADR confirmed by a human; it cannot be replaced by a coding agent's preference.
