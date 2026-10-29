# ADR-002 — Canonical Model IR

Status: SOURCE_LOCKED (per the original report; this does not mean that new engineering proposals have been approved by a human).

Source: the original report pp. 14–20, 49, 53.

## Decision

The JSON IR is the only model source; JSON Schema 2020-12; all renderers/import/AI/engine go through adapters.

## Consequences / Review boundary

The full schema profile is the D-014 proposal; React Flow nodes must not be treated as canonical data.

Changing this architectural boundary of the original report requires a new ADR confirmed by a human; it cannot be replaced by a coding agent's preference.
