// Sealed cookies of the browser session (ADR-018, T-043). Server-side only:
// the keys never reach the browser, and neither does anything a sealed value
// holds. A value is AES-256-GCM encrypted under one of at most two keys (the
// current one seals, the previous one still opens, for rotation); the cookie
// name and the format version are its additional data, so a value sealed for
// one cookie does not open as another. Anything that does not open, parse or
// fit is treated as absent: the caller sees no session, never an error that
// quotes the value.

const FORMAT = "v1";
const KEY_ID = /^[a-z0-9-]{1,16}$/;
const B64URL = /^[A-Za-z0-9_-]+$/;
const IV_BYTES = 12;

/** A Set-Cookie name=value longer than this is an error, not split (ADR-018). */
export const MAX_COOKIE_BYTES = 3584;

export class SessionConfigError extends Error {}
export class SealedTooLarge extends Error {}

export type SealKey = { id: string; key: CryptoKey };
export type SealKeys = { current: SealKey; previous: SealKey | null };

function fromB64url(s: string): Uint8Array<ArrayBuffer> | null {
  if (!B64URL.test(s)) return null;
  const bin = Buffer.from(s, "base64url");
  // Buffer accepts trailing garbage bits; insist on the canonical encoding.
  if (bin.toString("base64url") !== s) return null;
  const out = new Uint8Array(new ArrayBuffer(bin.length));
  out.set(bin);
  return out;
}

const toB64url = (b: Uint8Array) => Buffer.from(b).toString("base64url");

/**
 * Reads SESSION_KEYS: "id:key" pairs separated by a comma, the current key
 * first and at most one previous key after it; each key is 32 random bytes
 * in base64url. Errors name the problem, never the key material.
 */
export async function parseSealKeys(raw: string | undefined): Promise<SealKeys> {
  const entries = (raw ?? "").split(",").map((e) => e.trim());
  if (entries.length === 0 || entries.length > 2 || entries.some((e) => e === "")) {
    throw new SessionConfigError("SESSION_KEYS must hold the current key and at most one previous key");
  }
  const keys: SealKey[] = [];
  for (const [i, entry] of entries.entries()) {
    const at = entry.indexOf(":");
    const id = at < 0 ? "" : entry.slice(0, at);
    const bytes = at < 0 ? null : fromB64url(entry.slice(at + 1));
    if (!KEY_ID.test(id)) throw new SessionConfigError(`SESSION_KEYS entry ${i + 1}: the key ID must be 1-16 of a-z, 0-9 and -`);
    if (bytes === null || bytes.length !== 32) throw new SessionConfigError(`SESSION_KEYS entry ${i + 1}: the key must be 32 bytes in base64url`);
    if (keys.some((k) => k.id === id)) throw new SessionConfigError("SESSION_KEYS: two keys have the same ID");
    keys.push({ id, key: await crypto.subtle.importKey("raw", bytes, "AES-GCM", false, ["encrypt", "decrypt"]) });
  }
  const [current, previous] = keys;
  if (current === undefined) throw new SessionConfigError("SESSION_KEYS holds no key");
  return { current, previous: previous ?? null };
}

const aad = (cookie: string) => new TextEncoder().encode(`${cookie}\n${FORMAT}`);

/** Seals value for the cookie named cookie under the current key. */
export async function seal(keys: SealKeys, cookie: string, value: unknown): Promise<string> {
  const iv = crypto.getRandomValues(new Uint8Array(IV_BYTES));
  const plain = new TextEncoder().encode(JSON.stringify(value));
  const sealed = new Uint8Array(await crypto.subtle.encrypt({ name: "AES-GCM", iv, additionalData: aad(cookie) }, keys.current.key, plain));
  const out = `${FORMAT}.${keys.current.id}.${toB64url(iv)}.${toB64url(sealed)}`;
  if (cookie.length + 1 + out.length > MAX_COOKIE_BYTES) throw new SealedTooLarge(`the ${cookie} cookie would exceed ${MAX_COOKIE_BYTES} bytes`);
  return out;
}

/**
 * Opens a value sealed for cookie, or returns null: wrong format, unknown
 * key, a value sealed for another cookie, tampering or invalid JSON. The
 * caller still checks the shape of what it gets.
 */
export async function unseal(keys: SealKeys, cookie: string, sealed: string | undefined): Promise<unknown> {
  if (sealed === undefined || sealed.length > MAX_COOKIE_BYTES) return null;
  const parts = sealed.split(".");
  const [format, id, ivText, dataText] = parts;
  if (parts.length !== 4 || format !== FORMAT || ivText === undefined || dataText === undefined) return null;
  const key = [keys.current, keys.previous].find((k) => k !== null && k.id === id);
  const iv = fromB64url(ivText);
  const data = fromB64url(dataText);
  if (key === undefined || key === null || iv === null || iv.length !== IV_BYTES || data === null) return null;
  try {
    const plain = await crypto.subtle.decrypt({ name: "AES-GCM", iv, additionalData: aad(cookie) }, key.key, data);
    return JSON.parse(new TextDecoder("utf-8", { fatal: true }).decode(plain));
  } catch {
    return null;
  }
}
