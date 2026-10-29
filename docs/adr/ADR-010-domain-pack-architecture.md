# ADR-010 — Domain Pack Architecture

Status: SOURCE_LOCKED (per the original report; this does not mean that new engineering proposals have been approved by a human).

Source: the original report pp. 1–3, 19–20, 49–53.

## Decision

One in-depth SoftwareDeliveryDomainPack + a generic kernel; the DomainPack provides catalog/rules/inference/metrics/prompts/views.

## Consequences / Review boundary

The second commercial domain is P1; a test-only generic pack can verify the boundary; the generic engine should not be changed to add a domain.

Changing this architectural boundary of the original report requires a new ADR confirmed by a human; it cannot be replaced by a coding agent's preference.

Amendment (2026-10-19, owner H-016, [ADR-025](ADR-025-p0-domain-electronics-line.md)): the pack P0 builds in depth is changed to the electronics assembly/test line; SoftwareDeliveryDomainPack is kept as an example and conformance pack; the second domain is warehouse labour and peaks (P1). The architectural boundaries of this ADR do not change.
