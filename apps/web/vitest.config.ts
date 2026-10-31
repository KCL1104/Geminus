import { defineConfig } from "vitest/config";

// H-065: every unit test runs twice, as the production build
// (NEXT_PUBLIC_GEMINUS_OFFER_WONDER empty: Muse Spark 1.3 alone, H-051) and as
// a build with Muse Wonder 1.0 on offer ("1"), whatever the shell has set. A
// test about one state still sets it itself (vi.stubEnv).
const offer = (value: "" | "1") => ({ NEXT_PUBLIC_GEMINUS_OFFER_WONDER: value });

export default defineConfig({
  test: {
    include: ["tests/**/*.test.{ts,tsx}"],
    environment: "node",
    projects: [
      { extends: true, test: { name: "wonder-off", env: offer("") } },
      { extends: true, test: { name: "wonder-on", env: offer("1") } },
    ],
  },
});
