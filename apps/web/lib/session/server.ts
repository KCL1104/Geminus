// Builds the BFF once per server process from the environment (ADR-018,
// T-043). A deployment without a valid session configuration answers 503
// for every auth and API route: it never serves a half-configured login.
// Server-side only.
import { apiError, bff } from "./bff";
import { BffConfigError, loadBffConfig } from "./config";
import { newOidc } from "./oidc";
import { parseSealKeys, SessionConfigError } from "./seal";

type Handlers = ReturnType<typeof bff>;

function log(event: string, fields?: Record<string, string | number>) {
  process.stdout.write(`${JSON.stringify({ time: new Date().toISOString(), component: "bff", event, ...fields })}\n`);
}

let built: Promise<Handlers | null> | undefined;

async function build(): Promise<Handlers | null> {
  try {
    const cfg = loadBffConfig(process.env);
    const keys = await parseSealKeys(process.env.SESSION_KEYS);
    return bff({ cfg, keys, oidc: newOidc(cfg), now: Date.now, fetch, log });
  } catch (e) {
    if (e instanceof BffConfigError || e instanceof SessionConfigError) {
      // Names the variables, never their values.
      log("config.invalid", { reason: e.message });
      return null;
    }
    throw e;
  }
}

/** Runs fn with the process's BFF, or answers 503 when it is not configured. */
export async function withBff(fn: (h: Handlers) => Promise<Response>): Promise<Response> {
  built ??= build();
  const h = await built;
  if (h === null) return apiError("SERVICE_UNAVAILABLE", "sign-in is not configured", Date.now());
  return fn(h);
}
