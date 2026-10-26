import type { NextConfig } from "next";

const nextConfig: NextConfig = {
  // next dev would otherwise write its own AGENTS.md into apps/web, next to
  // the repository's (ADR-021).
  agentRules: false,
  // The container image (apps/web/Dockerfile, T-039) runs Next's standalone
  // server: .next/standalone holds only the files the server needs, so the
  // image has no npm and no dev dependencies. Only the image's build sets
  // the variable; make frontend-build and the browser checks keep next start.
  ...(process.env.GEMINUS_NEXT_STANDALONE === "1" ? { output: "standalone" as const } : {}),
};

export default nextConfig;
