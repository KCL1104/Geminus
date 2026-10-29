# First work instruction for a Coding Agent

The text below can be used as the first-round task. Do not ask the agent to finish the whole product at once.

```text
Read this repository's AGENTS.md, README.md, docs/DECISIONS_AND_TBD.md,
docs/IMPLEMENTATION_PLAN.md and planning/tasks.json, and locate the original
requirements through sources/SOURCE_MAP.md. No proposed decision has been
approved yet; do not approve one yourself or change one to accepted.

First take stock of the repository, then run make spec-check and keep the full result.
This is a spec package, not an implemented system; a missing Go / Next.js / PostgreSQL runtime is not a success.

The first round handles only T-001 and T-002:
1. List the blockers of the P0 contracts and the decision IDs that need human confirmation.
2. Build a repo / CI / toolchain skeleton without any production secret.
3. Keep schema, OpenAPI and SQL as the source of truth; do not invent a separate React Flow-specific domain schema.
4. Technologies and providers that are not confirmed get an interface + an explicit disabled configuration; never turn on real AI by default.
5. Do not do 3D or a pretty dashboard first; do not add P1 connectors / brokers / autonomous tools.

The next round first finishes the contract + validator tests of T-003 / T-004; scope that needs a decision is proposed as a decision first.
Each time, pick one bounded task whose dependencies are done, test it, then deliver:
changed files, requirement IDs, decision IDs, the commands actually run, the passed/failed/not-run items,
the remaining risks and the next ready task. Never use a mock, TODO, skip or estimated number as evidence of completion.
```
