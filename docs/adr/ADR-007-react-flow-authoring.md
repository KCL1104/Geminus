# ADR-007 — React Flow Authoring

Status: SOURCE_LOCKED (per the original report; this does not mean that new engineering proposals have been approved by a human).

Source: the original report pp. 2, 12–14.

## Decision

React Flow is the 2D authoring adapter; the domain model is kept separate from UI state.

## Consequences / Review boundary

The generated nodes/edges are a projection; no second IR is stored.

Changing this architectural boundary of the original report requires a new ADR confirmed by a human; it cannot be replaced by a coding agent's preference.
