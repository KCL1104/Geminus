# ADR-004 — JSON Patch Change Protocol

Status: SOURCE_LOCKED (per the original report; this does not mean that new engineering proposals have been approved by a human).

Source: the original report pp. 19, 24–25, 31–33.

## Decision

Changes are expressed as RFC 6902 JSON Patch; base/revision pinning prevents silent overwrite.

## Consequences / Review boundary

The P0 allowlist and pointer safety are restricted by D-014; not every RFC operation is allowed.

Changing this architectural boundary of the original report requires a new ADR confirmed by a human; it cannot be replaced by a coding agent's preference.
