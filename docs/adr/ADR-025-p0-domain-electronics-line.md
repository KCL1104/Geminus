# ADR-025 — P0 domain: electronics assembly/test line

Status: ACCEPTED (2026-10-19, owner H-016: 「用你推薦的吧」 ("let's go with the one you recommend")). Date: 2026-10-19. Amends the domain chosen in ADR-010; ADR-010's architecture boundary is unchanged.

## Context

ADR-010 (SOURCE_LOCKED) set "one deep SoftwareDeliveryDomainPack + generic kernel", and requires that a change to the original report's architecture boundary get its own ADR, confirmed by a human. The owner considered Software Delivery too weak as the P0 demo and asked the agent to research in depth before discussing it. The agent compared candidate domains with five research tracks (manufacturing, warehousing and logistics, business processes, healthcare + software delivery, cross-domain and Taiwan). The main points:

- Software Delivery: the easiest data to obtain, the best fit for the engine, the lightest regulation; but LinearB, Swarmia and Jellyfish already sell Monte Carlo what-if as a feature, no evidence of demand for deeper simulation was found, and it cannot preview the vision of physical settings.
- Manufacturing (electronics assembly/test line): the largest application of discrete-event simulation, the closest match to Taiwan's industry, the best at bringing in 3D; but the most crowded competition (the AI assistants of Siemens and AnyLogic; Simio has built in MCP since 2026-09-23), and factory engineers treat blocking, failures, shift calendars, changeovers and batches as basic features.
- Warehouse labor and peaks: a process problem, data easy to obtain, light regulation, few buyers in Taiwan; most engine components are shared with manufacturing.

The research relied mainly on public web sources; the search quota ran out partway through; market-size figures come from research-firm press releases and differ widely from one another; there were no customer interviews. The owner currently has no reachable potential users; the near-term goal is to apply to a launchpad after initial product and technical validation.

## Decision

- The domain pack built in depth for P0 changes to the **electronics assembly/test line** (SMT, AOI, ICT, FCT, burn-in, including test failure → debug → retest; AI server rack integration test as a variant). Stations, the resource catalog, metrics and canonical event fields are defined by T-046.
- SoftwareDeliveryDomainPack (T-011) stays, as an example and conformance pack; it is not deleted and is no longer the product's lead.
- The second domain is warehouse labor and peaks, in P1; 3D authoring is decided before it starts (H-014).
- ADR-010's boundary is unchanged: the generic kernel imports no domain pack; the domain-specific catalog, rules, inference, metrics, prompts and views are all in the pack.
- The new core primitives the electronics line needs (finite buffers and blocking, resource failures, shift calendars, time-varying release, batches; changeover TBD) get their own ADR per TWIN_IR_SPEC §8, proposed by T-046; their semantic details are PROPOSED decisions, not implemented before the owner approves them.

## Consequences

- PRD: REQ-003, REQ-004, REQ-013 and REQ-015 are rewritten; REQ-024 (line flow semantics) and REQ-025 (comparing simulation with observation) are added; of the P1 calendar, shift calendars move into P0, priority/preemption stay P1.
- Plan: T-046–T-052 are added; T-015, T-017, T-027, T-037, T-038 and T-040 gain new dependencies. The original report's 16-week estimate predates this decision; the added engine work has not yet been re-estimated in weeks.
- Demo data is always labeled synthetic; there is no claim of customer validation, and no claim that the model has been calibrated to a real line (REQ-025 returns unavailable when there is no observed data).
- Risks: the factory DES market has the most crowded competition, and AI-assisted modeling is not a unique selling point; the differentiation has to lie in "operation records → model → comparison with observation → human approval". Engineers' bar for realism is high; results missing blocking or failures will be rejected outright. Line data involves trade secrets and customers prefer on-premises deployment, which may conflict with the cloud deployment approach that is not yet decided.
