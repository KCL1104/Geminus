import { defineConfig, devices } from "@playwright/test";

// Browser checks of apps/web (TEST-033, TEST-032). The app is built and
// started here with the component gallery on, and its browser session (BFF,
// ADR-018) pointed at test-only servers (fake/servers.mjs): a deterministic
// OIDC provider on :3101 and a stand-in for the Go API on :3102. Pages
// without a session show their signed-out state.
const port = 3100;
const provider = "http://127.0.0.1:3101";

// Test values only: a fixed sealing key and the fake provider's client.
const session = {
  BFF_APP_ORIGIN: `http://127.0.0.1:${port}`,
  BFF_API_URL: "http://127.0.0.1:3102",
  BFF_OIDC_ISSUER: `${provider}/`,
  BFF_OIDC_AUTHORIZATION_URL: `${provider}/authorize`,
  BFF_OIDC_TOKEN_URL: `${provider}/oauth/token`,
  BFF_OIDC_REVOCATION_URL: `${provider}/oauth/revoke`,
  BFF_OIDC_END_SESSION_URL: `${provider}/v2/logout`,
  BFF_OIDC_JWKS_URL: `${provider}/.well-known/jwks.json`,
  BFF_OIDC_CLIENT_ID: "e2e-client",
  BFF_OIDC_CLIENT_SECRET: "e2e-client-secret",
  BFF_OIDC_AUDIENCE: "https://api.e2e.invalid",
  SESSION_KEYS: `e2e:${Buffer.alloc(32, 0x11).toString("base64url")}`,
};

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
  webServer: [
    {
      command: "node fake/servers.mjs",
      url: `${provider}/.well-known/jwks.json`,
      timeout: 30_000,
      reuseExistingServer: false,
      stdout: "ignore",
      stderr: "pipe",
    },
    {
      command: `npm --prefix ../web run build && npm --prefix ../web run start -- --port ${port} --hostname 127.0.0.1`,
      url: `http://127.0.0.1:${port}/`,
      timeout: 300_000,
      reuseExistingServer: false,
      env: { GEMINUS_UI_GALLERY: "1", NEXT_TELEMETRY_DISABLED: "1", ...session },
      stdout: "ignore",
      stderr: "pipe",
    },
  ],
});
