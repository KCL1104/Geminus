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
- Proof: real product screens only (gallery review pages on the synthetic sample line, both themes; two live-demo crops for Muse Spark's own explanation and the scenario it proposed). No invented figures, customers or claims; every number is from the synthetic demo.
- Constraints: the app's design system unchanged (five sizes, tokens, hairlines, three shadows, no gradient or animation; tests/design-rules.test.ts); one primary action (H-027); no credentials anywhere (the judge account is in the submission only); CSP img-src 'self'; 390 px with no horizontal scroll.

## Direction and memorable moment

The decision record: the loop as a numbered list, each step naming who acts (you, a Meta model, the Geminus server, the engine) as pills beside the screen it happens on. Refuses the feature-card grid and the big-number hero. Memorable moment: the results page itself in the first view, then the step where Muse Spark proposes and the Ink-framed "Geminus server" pill beside it.

## Open

- The live-demo crops exist in the light theme only.
- The web-search step describes H-046's feature, built in parallel; if it does not ship, its row and pill must go.
- The four new industries (H-045) are named only; their templates are built in parallel.
