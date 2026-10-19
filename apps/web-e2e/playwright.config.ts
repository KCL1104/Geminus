import { defineConfig, devices } from "@playwright/test";

// Browser checks of apps/web (TEST-033). The app is built and started here
// with the component gallery on; no API is needed: pages without a session
// show their signed-out state.
const port = 3100;

export default defineConfig({
  testDir: "./tests",
  timeout: 120_000,
  expect: {
    timeout: 15_000,
    // Screenshot baselines are made in one pinned environment (CI's
    // Playwright container) and changed only by a person (README.md).
    toHaveScreenshot: { animations: "disabled", caret: "hide", maxDiffPixelRatio: 0.002 },
  },
  // Snapshots are named by test, not by platform: there is one environment.
  snapshotPathTemplate: "{testDir}/__screenshots__/{testFilePath}/{arg}{ext}",
  // Never write a baseline unless asked (npm test -- --update-snapshots).
  updateSnapshots: "none",
  fullyParallel: false,
  workers: 1,
  retries: 0,
  reporter: [["list"]],
  use: {
    baseURL: `http://127.0.0.1:${port}`,
    ...devices["Desktop Chrome"],
    locale: "en-US",
    timezoneId: "UTC",
    reducedMotion: "reduce",
  },
  projects: [{ name: "chromium" }],
  webServer: {
    command: `npm --prefix ../web run build && npm --prefix ../web run start -- --port ${port} --hostname 127.0.0.1`,
    url: `http://127.0.0.1:${port}/`,
    timeout: 300_000,
    reuseExistingServer: false,
    env: { GEMINUS_UI_GALLERY: "1", NEXT_TELEMETRY_DISABLED: "1" },
    stdout: "ignore",
    stderr: "pipe",
  },
});
