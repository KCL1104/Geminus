# ADR-003 — Immutable Model Versions

Status: SOURCE_LOCKED (per the original report; this does not mean that new engineering proposals have been approved by a human).

Source: the original report pp. 6, 18–19, 31–33.

## Decision

Drafts are mutable; published versions are immutable and keep their parent; a run references only a version.

## Consequences / Review boundary

The interaction of a dirty draft with approval is filled in by D-003; for hash/dataset freezing see D-006.

Changing this architectural boundary of the original report requires a new ADR confirmed by a human; it cannot be replaced by a coding agent's preference.
