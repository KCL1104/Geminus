# Product Requirements — Product MVP

Source: the original report pp. 1–8, 47–53. The principles and the P0/P1 classification are SOURCE; this document's requirement IDs, detailed acceptance criteria and closed-loop interaction are derived; undefined behavior is in D-001–D-017. On 2026-10-19 the owner changed the first P0 domain to an electronics assembly/test line (H-016, a departure from the original report); affected passages are marked H-016.

## 1. Product positioning and success hypotheses

The product is not just a line dashboard (the original report's example is a DevOps dashboard), and it is not a 3D animation tool. It is positioned as **AI-native Digital Twin Modeling + Simulation + Decision Support**, with the architecture General-purpose Digital Twin Kernel + Domain Pack Architecture + AI Modeling Layer. The first complete domain is the electronics assembly/test line (H-016; in the original report it was Software Delivery / DevOps, whose pack is kept as an example and conformance pack); it is not the only domain hard-coded into the kernel.

Core closed loop: Describe / Import → Model → AI Proposal → Human Approval → Simulate → Compare → AI Explain → Iterate.

The three SOURCE validation hypotheses: AI lowers the barrier to modeling; simulation gives answers users are willing to use for decisions; the same Generic IR / DES / AI architecture can support the next domain. These are hypotheses for a pilot to test, not results already achieved. The second production domain (warehouse labor and peaks, H-016) is not required for the P0 launch.

The owner's near-term goal (H-016): build an initial product and technical validation, then apply to a launchpad. The owner currently has no potential users to contact, so all demo data is synthetic; we do not claim customer validation or that the model has been calibrated against a real line.

## 2. Roles and work context

The main users are industrial engineering (IE), production engineering and line planning staff at electronics manufacturers, and manufacturing managers (H-016; in the original report: engineering managers, Platform/DevOps leads and delivery process owners). The permission roles are only Owner, Editor and Viewer. The first two can edit, create proposals, approve and start simulations; Viewer is read-only; only Owner manages members. The original report does not forbid reviewing a proposal you created yourself, and this draft does not require an extra four-eyes policy.

Representative question (H-016): "We now have 2 ICT testers, 3 FCT testers and a buffer of 10 boards before FCT; if we add 1 ICT tester or enlarge the buffer, how will UPH, the bottleneck station, WIP, utilization, blocked time and the retest ratio change?" The original report's example is "2 reviewers, 3 CI runners". When the model or data is insufficient, ask for more first; never treat the example's parameters as customer facts. Results are conditional simulation estimates, not guarantees about the future.

## 3. P0 features and acceptance requirements

| ID | Capability | Acceptance criteria |
|---|---|---|
| REQ-001 | Workspace / tenancy | An authenticated user creates a project in an organization they have access to; cross-tenant read/write/job/artifact is never visible; isolation regression 100% pass |
| REQ-002 | RBAC / membership | Owner/Editor/Viewer server-side enforcement; UI buttons do not grant authorization; Owner can manage the membership of existing users |
| REQ-003 | Generic IR / Domain Pack | The electronics line DomainPack (H-016) produces valid IR; SoftwareDeliveryDomainPack is kept as an example; the generic simulation package depends on no domain package; unsupported domain fails closed |
| REQ-004 | Template + 2D authoring | Electronics line full template (H-016; stations defined by T-046; the research proposal is SMT→AOI→ICT→FCT→burn-in→packing and shipping, including test failure→debug→retest); the original report's Source→Review→CI→Test→Deploy→Sink stays in the example pack; can add/delete/move nodes, connect edges, edit properties; client/server validation agree |
| REQ-005 | Draft / immutable versions | Create builds model+draft+v1; publish produces a new immutable version and keeps the parent; simulation does not accept a draft ID |
| REQ-006 | Optimistic concurrency | A stale If-Match or published base returns 409 MODEL_VERSION_CONFLICT; must not overwrite unsaved changes or silently rebase |
| REQ-007 | CSV / JSON import | upload→profile/preview→map→validate→commit; invalid rows can be downloaded; invalid JSON returns a JSON Pointer; nothing modifies the Twin before commit |
| REQ-008 | AI modeling | Natural language produces a structured proposal; a schema/semantic-invalid proposal must not become ready; insufficient information gives needs_input |
| REQ-009 | Human approval | diff, assumptions, server risk and validation are visible; approve revalidates; an edit creates exactly one new version in one transaction; reject does not change the model hash |
| REQ-010 | Scenario | The baseline is an empty patch; an alternative is a patch on an immutable base; a scenario does not change the canonical version; an AI scenario needs human approval and then a manual run |
| REQ-011 | DES | The aggregate digest for a fixed IR, dataset, engine, config and seed is reproducible; bounded event processing; no arbitrary script execution |
| REQ-012 | Async runs | 202 immediately after a durable enqueue; queued/running/completed/failed/cancelled and progress can be queried; cancellation stops at a checkpoint |
| REQ-013 | Replications / metrics | Initial range 1–1000 (hard cap TBD); throughput (including hourly output, UPH), lead/cycle, queue, WIP, utilization, failure/rework, plus the blocked/starved time and failure downtime that REQ-024 brings (H-016); the statistical population and any shortfall are visible |
| REQ-014 | Baseline comparison | Show baseline, scenario, absolute delta and relative delta together; a 0 denominator, missing values and non-comparable configs never produce a fabricated percentage |
| REQ-015 | Data threshold for domain metrics | Domain metrics are produced only when the semantic data is sufficient: for the electronics line, first pass yield (FPY), per-station availability and so on are defined by T-046 (H-016); the example pack's DORA metrics need deployment/change/failure/recovery data; missing data is marked unavailable; general cycle time is never passed off as DORA, and utilization is never passed off as OEE |
| REQ-016 | AI analyst | Cites only the supplied results; major conclusions have metric evidence; distinguishes observation/inference; must not invent a metric or claim a certain outcome |
| REQ-017 | 3D viewer / playback | procedural scene, orbit/pan/zoom, hover/select, tokens, queue/utilization, timeline play/pause/scrub, baseline/scenario switch; same entity IDs; no 3D authoring |
| REQ-018 | Audit | create/edit/publish/proposal/approve/reject/import/run have a tenant-scoped audit; traceable by request_id / run_id; no private chain-of-thought is stored |
| REQ-019 | Security | JWT/OIDC, tenant isolation, RBAC, RLS, authorized artifact access, AI authority containment, resource budgets |
| REQ-020 | Observability / deployment | structured logs + traces/metrics; containerized web/api/worker, staging and controlled production approval; no customer content in product telemetry |
| REQ-021 | Quality gates | Six test layers, an initial target of 50 curated modeling intents, invalid mutation=0, AI leakage=0, mandatory adversarial invented metrics=0 |
| REQ-022 | Bilingual UI (added by the owner, H-005) | English (`en`) is the default and primary language, with Traditional Chinese (`zh-Hant`) also supported; every user-visible string comes from the message catalog, and the two languages have exactly the same keys; the user can switch the language and it is remembered; dates/numbers/durations are formatted per language; API errors are localized by the UI from the error code; AI text output follows the user's language; IDs, JSON Pointers, enums and metric paths are not translated |
| REQ-023 | UI design & accessibility (added by the owner, H-007) | Every P0 screen meets WCAG 2.2 AA (the main flow can be completed with the keyboard only, visible focus, color contrast, names and states readable by screen readers); shared design tokens and components, with the component library and styling approach decided by an ADR after comparing several UI libraries; every UI task first delivers a clickable prototype for owner review before implementation; no broken layout in en or zh-Hant, at phone or desktop widths |
| REQ-024 | Line flow semantics (added after the owner changed the domain, H-016) | Finite buffers and blocking, resource failures (MTBF/MTTR) and downtime, shift calendars (when a resource is available), time-varying arrivals (release schedule), batch processes; semantics follow the decisions proposed by T-046 and approved by the owner; each has fixed golden vectors and an independent SimPy reference comparison; still bounded event processing, with no arbitrary scripts |
| REQ-025 | Comparison of simulation and observation (H-016) | When a baseline run has matching observed data, show simulated and observed output side by side (for example weekly output, per-station cycle time) with the error; without observed data, mark unavailable and do not claim the model is calibrated; demo data is labeled synthetic |

REQ-022 is not an original report requirement: the owner added it on 2026-10-18 (H-005); Chinese is set to Traditional `zh-Hant`, and the owner confirmed only Traditional for now (H-006).

The REQ-022 exception (H-008): accessibility strings built into the UI component library that cannot be overridden by a prop may use the library's own translation, but they must be listed, reviewed and enumerated by a test.

REQ-024 and REQ-025 are not original report requirements: they were added when the owner, on 2026-10-19, adopted the agent's recommendation and changed the P0 domain to an electronics assembly/test line (H-016); the semantic details are D-047–D-060, approved by the owner as recommended on 2026-10-20 (H-018).

REQ-023 is not an original report requirement: the owner agreed on 2026-10-18 to add it at the agent's recommendation (H-007), and asked that several UI libraries' approaches be compared rather than adopting Tailwind + shadcn/ui directly; the owner has no visual preference yet, so the default is a neutral, information-dense tool interface.

Engineering supplements (approved): REQ-002's org bootstrap / membership HTTP scope is in D-008 / D-012; REQ-007's commit and proposal boundary is in D-009; REQ-010's scenario approval result is in D-004; other missing values, clock and consistency details follow the dedicated Specs.

## 4. UI and main journeys

### Creation and manual edits

Choose a project → choose the electronics line template (H-016) → the 2D editor shows the draft revision/base/latest → edit properties → show validation issues and locate them → Save Draft → explicit Publish. Unsaved editor state is kept separate from the server draft; switching versions must not silently overwrite local edits. On a 409, show the server revision and require a reload / explicit handling; no automatic merge.

### Import and AI modeling

Upload CSV/JSON → preview and row errors → mapping suggestions (below 0.80 needs confirmation) → a human confirms the mapping → deterministic validation → commit dataset → generate an inference/model proposal → review diff/assumptions/data quality → approve or reject. APPROVED D-009: commit is not publish; after a reject the frozen dataset can be kept, but the canonical Twin does not change at all.

### Scenario and decision

Choose an immutable version → create a baseline run → propose a scenario (manual or AI) → review/approve the AI scenario → manual Run → monitor/cancel → compare → AI analysis → a human chooses the next experiment. The user must be able to tell "save the change as an actual model version" from "use only for a what-if scenario"; the two buttons and their returned objects must not be mixed.

### 3D and results

3D reads the same IR/version/run, and 2D/3D share the selection state by entity ID. A sampled trace is not a full animation of every entity; the UI states clearly that it is sampled. Without a trace, aggregates are still viewable; a playback trajectory must never be fabricated. A 3D load failure must not block 2D / simulation / results.

## 5. P1 and explicit exclusions

P1: MES/CFX and GitHub/GitLab/Jira/CI connectors, parameter sweeps, advanced routing/priority/preemption (shift calendars are already in P0 because of H-016), line features after P0 (H-018: zero-capacity buffers, panelization and depanelization, holding resources across steps, failure clocks based on busy time, not starting work that cannot finish before a shift change, AGV/material handling), batch AI experiment approval, rich 3D assets, collaboration, SSO/SAML, billing, telemetry streaming, a second domain (warehouse labor and peaks, H-016), iTwin adapter, and a dedicated broker / GraphQL if needed.

P0 does not include BIM/CAD, IoT synchronization, physics/CFD/FEA, CRDT, AI autonomous mutation/production actions, an arbitrary Python simulation runtime, or 3D authoring. The full viewer itself is still P0; it must not be removed because of a non-goal.

## 6. Engineering targets, not achieved metrics

| Aspect | Original report's initial target | Qualification |
|---|---|---|
| Graph | 500 nodes / 1,000 edges | Alpha calibration |
| 2D / 3D initial render | <2s / <3s | reference device TBD |
| 3D interaction | median ≥30 FPS | reference device TBD |
| CSV | 100k rows / 50 MB | Confirm after load test |
| DES | 1M events <15s; peak RSS <1 GB | 2 vCPU / 4 GB reference server TBD |
| API / validation | normal API p95 <500ms; max model validation <1s | Excludes AI / simulation run time; workload TBD |
| Cancel | ≤5s after requested | benchmark to be confirmed |
| Security | tenant regression 100%; unsafe/invalid mutations committed=0 | Must be gating, not an average |

## 7. Launch gates / acceptance evidence

Alpha: manual model→simulation→results. AI Alpha: intent→proposal→approve→new version. Product Alpha: CSV→inferred model→approval→scenario→simulate→compare→analysis. Private Beta: an external team's real data and repeated what-if decisions, with an approved retention/residency/provider policy.

H-016's near-term goal (initial product + technical validation, apply to a launchpad) corresponds to Product Alpha, completed with synthetic data; Private Beta needs real external data, and the owner currently has no one to contact.

Engineering completion needs tests and a reproducible workload; product success needs evidence of decision usefulness from a pilot. time-to-first-model, time-to-first-simulation, proposal acceptance/rejection, runs/project and comparisons/project can be collected, but raw model/CSV/prompt does not go into telemetry. The original report gives no conversion rate or business KPI threshold, so no fictional target is added.
