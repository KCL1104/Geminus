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
  // H-061 A8 (PROPOSED): AI investigations and Muse Wonder 1.0 as their
  // planner, each off unless the build sets it, fixed the same way; the
  // image's build sets neither, so both stay off on the deployment until
  // the coordinator turns them on (H-061 §14, §18 A9).
  env: {
    NEXT_PUBLIC_GEMINUS_OFFER_WONDER: process.env.NEXT_PUBLIC_GEMINUS_OFFER_WONDER ?? "",
    NEXT_PUBLIC_GEMINUS_INVESTIGATIONS: process.env.NEXT_PUBLIC_GEMINUS_INVESTIGATIONS ?? "",
    NEXT_PUBLIC_GEMINUS_OFFER_WONDER_PLANNER: process.env.NEXT_PUBLIC_GEMINUS_OFFER_WONDER_PLANNER ?? "",
  },
  ...(process.env.GEMINUS_NEXT_STANDALONE === "1" ? { output: "standalone" as const } : {}),
};

export default nextConfig;
