import type { NextConfig } from "next";

const nextConfig: NextConfig = {
  // next dev would otherwise write its own AGENTS.md into apps/web, next to
  // the repository's (ADR-021).
  agentRules: false,
  // No X-Powered-By (T-057); the other security headers are proxy.ts's.
  poweredByHeader: false,
  // The container image (apps/web/Dockerfile, T-039) runs Next's standalone
  // server: .next/standalone holds only the files the server needs, so the
  // image has no npm and no dev dependencies. Only the image's build sets
  // the variable; make frontend-build and the browser checks keep next start.
  // H-051: fixed at build time even when unset, so the server and the
  // browser always agree on whether Muse Wonder is offered (a runtime
  // variable would change only the server's half). The image's build never
  // sets it (the deploy check allows no build arguments), so Muse Wonder is
  // hidden on the deployment; offering it again is a code change here.
  // H-065, H-069: on in production builds since Muse Wonder passed its live
  // tests (after H-068); "" builds the app without it. The unit tests
  // (vitest.config.ts) and the browser checks (GEMINUS_E2E_OFFER_WONDER,
  // apps/web-e2e) run with it on and off. H-061 A8: AI investigations, on
  // in production builds since the closed-loop live checks passed (A9,
  // 2026-11-01; "" builds the app without them), and Muse Wonder 1.0 as
  // their planner (H-061 §14 gate 3), on in production builds since its
  // live end-to-end check passed on investigator-0.4.0 (H-086, 2026-11-03,
  // checks/H-061_PLANNER_EVAL.md; "" builds the app without it); both fixed
  // the same way.
  env: {
    NEXT_PUBLIC_GEMINUS_OFFER_WONDER: process.env.NEXT_PUBLIC_GEMINUS_OFFER_WONDER ?? "1",
    NEXT_PUBLIC_GEMINUS_INVESTIGATIONS: process.env.NEXT_PUBLIC_GEMINUS_INVESTIGATIONS ?? "1",
    NEXT_PUBLIC_GEMINUS_OFFER_WONDER_PLANNER: process.env.NEXT_PUBLIC_GEMINUS_OFFER_WONDER_PLANNER ?? "1",
  },
  ...(process.env.GEMINUS_NEXT_STANDALONE === "1" ? { output: "standalone" as const } : {}),
};

export default nextConfig;
