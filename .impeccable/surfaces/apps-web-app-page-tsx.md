---
version: 1
slug: "apps-web-app-page-tsx"
primary_target: "apps/web/app/page.tsx"
related_targets: ["apps/web/features/landing/landing.tsx"]
---

## Scope and mode

The start page `/` (H-048). Persuade, inside the Operate product: the visitor decides to sign in and try it.

## Audience, job, proof, constraints

- Audience: the Meta Global AI Developer Hackathon's judges first (Impact 30%, Use of Meta AI tooling 25%, Demo 25%, Technical depth 20%), then anyone who opens the app. English first; zh-Hant is the translation.
- Job: understand in one view what Geminus does (try a change on a factory's digital twin before touching the real line), see it is real, and sign in.
- Proof: real product screens only, framed as screenshots (gallery review pages on the synthetic sample line in both themes: the results' key metrics, the canvas with a request to the AI, the Marey chart; two live-demo crops of Muse Spark's own scenario proposal and explanation). No invented figures, customers or claims; every number is from the synthetic demo.
- Constraints: the app's design system unchanged (five sizes, tokens, hairlines, three shadows, no gradient or animation; tests/design-rules.test.ts); one primary action (H-027); no credentials anywhere (the judge account is in the submission only); CSP img-src 'self'; 390 px with no horizontal scroll.

## Direction and memorable moment

The decision record: the loop as a numbered list, each step naming who acts (you, a Meta model, the Geminus server, the engine) as pills beside the screen it happens on. Refuses the feature-card grid and the big-number hero. Memorable moment: the results page itself in the first view, then the step where Muse Spark proposes and the Ink-framed "Geminus server" pill beside it.

## Open

- The live-demo crops exist in the light theme only (dimmed in dark).
- The web-search row describes H-046's feature (a tool of the Meta Model API that Muse Spark uses), built in parallel; if it does not ship, the row must go.
- The four new industries (H-045) are named only, under the parallel work's current names; their templates are built in parallel.
