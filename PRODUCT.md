# Product

<!-- impeccable:product-schema 1 -->

This file records Geminus's product facts for UI design and review. It holds no visual decisions (the visual system is recorded separately in DESIGN.md once the owner has chosen a direction). Sources: `docs/PRD.md`, `docs/DECISIONS_AND_TBD.md`, and the owner's answers of 2026-10-19 (H-012, H-016). Where this file conflicts with SOURCE or the PRD, they prevail.

## Platform

web

## Users

- **Long-term audience**: any process or operations lead who needs to simulate before deciding. The owner wants to cover manufacturing, industry, warehousing, aerospace, cities, construction engineering and similar settings (H-012).
- **P0 audience**: users of an electronics assembly/test line, including industrial engineering (IE), production engineering, line planners and manufacturing managers (H-016, PRD §2). The original report's P0 was Software Delivery; it is now kept only as an example pack.
- **Permission roles**: only three: Owner, Editor, Viewer. Owner and Editor can edit, create proposals, approve and start simulations; Viewer can only read; only Owner can manage members.

The three usage situations the owner confirmed (H-012):

1. **Desktop decisions**: several times a week, 30–60 minutes each time. Users come with a what-if question, for example "with one more ICT test machine, or a larger buffer before FCT, how do UPH, the bottleneck station, WIP and utilization change?". They want a credible comparison, not an animation.
2. **Long daily modelling**: a process owner maintains the model in the 2D editor for long stretches every day. This needs long use without fatigue, enough information density and smooth keyboard operation.
3. **Meeting projection**: the baseline vs. scenario comparison is often projected in meetings or screen-shared with the team. The result screens must stay legible on a projector and over a low-quality screen share.

## Product Purpose

Geminus is an AI-native Digital Twin Modeling + Simulation + Decision Support product. Its core closed loop is: Describe/Import → Model → AI Proposal → Human Approval → Simulate → Compare → AI Explain → Iterate.

Success is defined as validating SOURCE's three hypotheses: AI lowers the barrier to modelling; simulation gives answers users are willing to decide on; the same Generic IR, DES and AI architecture can support the next domain. These three are hypotheses for a pilot to validate; none of them is proven yet.

## Positioning

Geminus is not a line dashboard, and not a 3D animation tool. Architecturally it consists of four separate boundaries: a generic Twin kernel (Generic IR + DES), a DomainPack per domain, an AI layer that can only make suggestions, and the human-approved ModelChangeProposal.

It differs in three points:

- **AI suggests, people approve**: AI cannot approve, publish or start a simulation on its own.
- **Results are reproducible**: every result is bound to an immutable model version, dataset, engine, config and seed, so it can be rerun to the same result.
- **Results are conditional estimates**: a result is an estimate under the conditions above, not a guarantee about the future. When data is insufficient, the product says needs_input or unavailable and does not fabricate numbers.

## Operating Context

Main journeys (PRD §4):

- **Create and edit by hand**: choose a project → choose a template → edit the draft in 2D → resolve validation issues → Save Draft → explicit Publish.
- **Import and AI modelling**: CSV/JSON upload → preview and row errors → mapping → validation → commit dataset → AI proposal → review the diff and assumptions → approve or reject.
- **Scenario and decision**: choose an immutable version → run the baseline → propose a scenario → review and approve → Run by hand → monitor or cancel → compare → AI analysis → decide the next experiment.
- **3D and results**: 3D reads the same IR, version and run; 2D and 3D share selection by entity ID; playback uses a sampled trace and says clearly that it is sampled.

The product must let users see the difference between two operations: "save the change as an actual model version" and "only run a what-if scenario". Their buttons and returned objects must not be mixed up.

## Capabilities and Constraints

- **Domain scope**: P0 does the electronics assembly/test line first (SMT, AOI, ICT, FCT, burn-in); the second formal domain is warehouse labour and peaks, which is P1 (H-016). The owner first wanted to start from software development; after research the owner chose the electronics line instead: it is the largest application of discrete-event simulation, fits Taiwan's industry best, and best carries the vision of 3D and physical settings. The long-term vision covers all settings (H-012). So the kernel, the interface vocabulary, the icons and the design system must not assume a single setting; domain-specific things come from the DomainPack.
- **Engine scope**: so that line engineers trust the results, the P0 engine adds finite buffers and blocking, machine breakdowns, shift calendars, time-varying release and batches (REQ-024); the semantics await the owner's approval (T-046).
- **2D and 3D**: in P0, 2D is authoring and 3D is a read-only projection and playback; both share entity IDs and the IR topology (SOURCE). The owner thinks 3D may matter more for later spatial domains (factories, warehouses, cities, construction) (H-012). P0 only does read-only 3D; 3D authoring is decided when the second domain is chosen (H-014). 3D is a first-class screen, not an add-on.
- **Devices**: desktop first. A phone can view results, review proposals and approve or reject them, but does not edit topology (H-012). No width may break the layout (REQ-023).
- **Language**: English `en` is the default; Traditional Chinese `zh-Hant` is also supported. Every user-visible string comes from a message catalog (REQ-022).
- **Concurrency and immutability**: a version conflict (stale) returns 409 and is shown clearly; no silent overwrite and no automatic rebase. ModelVersion, Scenario and completed results are never modified in place.
- **Not in P0**: BIM/CAD, physics simulation, 3D authoring, real-time streaming, AI production actions (AGENTS.md §7).
- **Not decided yet**: the live AI provider's usage policy and quota, the cloud vendor, the Auth0 tenant settings, the launchpad programme name and deadline (H-016). The visual direction was decided by H-013.

## Brand Commitments

The product name is **Geminus**. There is no logo, brand color or tone-of-voice guide yet (H-012); these are built by the design system and approved by the owner.

The visual constraint the owner set (H-012): the dark theme uses grey-black, not blue.

## Evidence on Hand

- There are no real customers, customer data, benchmarks, testimonials or achieved metrics yet; the owner also has no potential users to reach yet (H-016).
- The examples in the PRD (for example 2 ICT and 3 FCT machines) are only illustrations, not a customer's actual situation. Demo data in prototypes and the UI is always labelled synthetic.
- There is a Software Delivery example template, with the pipeline Source → Review → CI → Test → Deploy → Sink (T-011); the electronics line template is not built yet (T-051).
- The engineering targets in PRD §6 are not achieved results and must not be written as product claims.

## Product Principles

1. **People make the decisions**: AI output is a proposal; the server computes risk and validation, and a person reviews, approves or rejects it.
2. **Every number is traceable**: results state the version, dataset, seed, sampling and statistics; where something is missing, they say unavailable and do not gloss over it.
3. **One topology, several projections**: 2D and 3D are different projections of the same IR and share selection and IDs.
4. **Neutral core, domains as plug-ins**: the generic part does not hard-code any one domain; domain traits come from the DomainPack.
5. **Explicit beats silent**: conflicts, staleness, cancellation and failure are stated plainly, with the next step.

## Accessibility & Inclusion

- Meets WCAG 2.2 AA; the main flows can be completed with the keyboard alone, and focus is always visible (REQ-023).
- Because of the meeting-projection situation, the contrast and type size of result screens must stay legible when projected, and baseline vs. scenario must not be told apart by color alone.
- Users who model for long stretches every day need a low-fatigue reading environment, so both light and dark themes are supported (H-011).
- Neither en nor zh-Hant may break the layout at phone or desktop width.
