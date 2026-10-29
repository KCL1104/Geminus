# ADR-009 — PostgreSQL-backed P0 Jobs

Status: SOURCE_LOCKED (per the original report; this does not mean that new engineering proposals have been approved by a human).

Source: the original report pp. 37, 39–40.

## Decision

P0 uses a PostgreSQL pull queue + SKIP LOCKED; no broker is added ahead of time.

## Consequences / Review boundary

RLS-aware tenant assignment and lease/retry/fencing are completed by D-008/D-011.

Changing this architectural boundary of the original report requires a new ADR confirmed by a human; it cannot be replaced by a coding agent's preference.
