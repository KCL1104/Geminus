// The browser session's two cookies (ADR-018): the session itself, and the
// short-lived login transaction between the redirect to the provider and its
// callback. Both are sealed (seal.ts); this module fixes their names, shapes
// and lifetimes and builds their Set-Cookie headers. Server-side only.
import type { Profile } from "../auth/profile";
import { safeReturnTo } from "../return-to";
import { type SealKeys, seal, unseal } from "./seal";

// The return-path rule is shared with the page's sign-in link (lib/return-to.ts).
export { safeReturnTo };
// Who signed in (H-042, lib/auth/profile.ts): a session sealed before H-042 has no profile at all.
export type { Profile };

export const SESSION_COOKIE = "__Host-geminus_session";
export const LOGIN_COOKIE = "__Host-geminus_login";

/** Idle and absolute session limits (ADR-018's starting values). */
export const IDLE_MS = 12 * 60 * 60 * 1000;
export const ABSOLUTE_MS = 7 * 24 * 60 * 60 * 1000;
/** A login must come back from the provider within this time. */
export const LOGIN_MS = 10 * 60 * 1000;
/**
 * How far in the future a sealed time may be: clocks that differ a little
 * (an NTP step, another instance) must not end sessions or logins.
 */
export const SKEW_MS = 60_000;
/** The longest access or refresh token a session holds (ASCII, no spaces). */
export const MAX_TOKEN = 2048;


export type Session = {
  accessToken: string;
  refreshToken: string | null;
  /** When the access token expires, epoch milliseconds. */
  accessExpiresAt: number;
  createdAt: number;
  lastSeenAt: number;
  profile?: Profile | null;
};

/** The longest name and email a session keeps (characters). */
export const MAX_NAME = 128;
export const MAX_EMAIL = 254;
// Characters that show nothing or reorder text are dropped from a name, so it cannot disguise itself.
const hidden = /[\p{Cc}\p{Cf}\p{Zl}\p{Zp}]/gu;

/** A display name from a claim: a string with what shows nothing removed and spaces collapsed, at most MAX_NAME characters, or null. */
export function cleanName(v: unknown): string | null {
  if (typeof v !== "string") return null;
  const s = v.replace(hidden, "").replace(/\s+/g, " ").trim();
  // Trimmed again after the cut, which can end on a space: isProfile wants the result clean as it is.
  return s === "" ? null : [...s].slice(0, MAX_NAME).join("").trimEnd();
}

/** An email from a claim: one printable ASCII address with one @, at most MAX_EMAIL characters, or null. */
export function cleanEmail(v: unknown): string | null {
  return typeof v === "string" && v.length <= MAX_EMAIL && /^[\x21-\x3f\x41-\x7e]+@[\x21-\x3f\x41-\x7e]+$/.test(v) ? v : null;
}

/** The profile from an ID token's claims: its name (or nickname) and email, each cleaned, or null when neither is usable. */
export function profileFromClaims(claims: Record<string, unknown>): Profile | null {
  const name = cleanName(claims.name) ?? cleanName(claims.nickname);
  const email = cleanEmail(claims.email);
  return name === null && email === null ? null : { name, email };
}

const isProfile = (v: unknown): v is Profile | null => {
  if (v === null) return true;
  if (typeof v !== "object" || Array.isArray(v)) return false;
  const p = v as Record<string, unknown>;
  return Object.keys(p).length === 2 && (p.name === null || cleanName(p.name) === p.name) && (p.email === null || cleanEmail(p.email) === p.email);
};

export type Login = {
  state: string;
  nonce: string;
  codeVerifier: string;
  /** Where to go after login: a same-origin path, checked when it was set. */
  returnTo: string;
  createdAt: number;
};

const token = (v: unknown, max: number): v is string => typeof v === "string" && v.length > 0 && v.length <= max && /^[\x21-\x7e]+$/.test(v);
/** Whether v can be a session's access or refresh token. */
export const sessionToken = (v: unknown): v is string => token(v, MAX_TOKEN);
const time = (v: unknown): v is number => typeof v === "number" && Number.isSafeInteger(v) && v > 0;

function isSession(v: unknown): v is Session {
  if (typeof v !== "object" || v === null || Array.isArray(v)) return false;
  const s = v as Record<string, unknown>;
  const keys = Object.keys(s).length;
  return (
    // Five fields before H-042, six with the profile.
    (keys === 5 || (keys === 6 && "profile" in s && isProfile(s.profile))) &&
    sessionToken(s.accessToken) &&
    (s.refreshToken === null || sessionToken(s.refreshToken)) &&
    time(s.accessExpiresAt) &&
    time(s.createdAt) &&
    time(s.lastSeenAt) &&
    s.lastSeenAt >= s.createdAt
  );
}

function isLogin(v: unknown): v is Login {
  if (typeof v !== "object" || v === null || Array.isArray(v)) return false;
  const l = v as Record<string, unknown>;
  return (
    Object.keys(l).length === 5 &&
    token(l.state, 128) &&
    token(l.nonce, 128) &&
    token(l.codeVerifier, 128) &&
    typeof l.returnTo === "string" &&
    safeReturnTo(l.returnTo) === l.returnTo &&
    time(l.createdAt)
  );
}

/** The session in the cookie value, or null if none, invalid or expired at now. */
export async function readSession(keys: SealKeys, value: string | undefined, now: number): Promise<Session | null> {
  const s = await unseal(keys, SESSION_COOKIE, value);
  if (!isSession(s) || now - s.createdAt >= ABSOLUTE_MS || now - s.lastSeenAt >= IDLE_MS || s.lastSeenAt > now + SKEW_MS) return null;
  return s;
}

/** The login transaction in the cookie value, or null if none, invalid or too old at now. */
export async function readLogin(keys: SealKeys, value: string | undefined, now: number): Promise<Login | null> {
  const l = await unseal(keys, LOGIN_COOKIE, value);
  if (!isLogin(l) || now - l.createdAt >= LOGIN_MS || l.createdAt > now + SKEW_MS) return null;
  return l;
}

// __Host- cookies must be Secure, have Path=/ and no Domain (RFC 6265bis).
const attributes = "Path=/; HttpOnly; Secure; SameSite=Lax";

/** Set-Cookie for s; it lives until the earlier of its idle and absolute limits. */
export async function sessionCookie(keys: SealKeys, s: Session, now: number): Promise<string> {
  const ms = Math.min(s.lastSeenAt + IDLE_MS, s.createdAt + ABSOLUTE_MS) - now;
  return `${SESSION_COOKIE}=${await seal(keys, SESSION_COOKIE, s)}; ${attributes}; Max-Age=${Math.max(0, Math.floor(ms / 1000))}`;
}

export async function loginCookie(keys: SealKeys, l: Login): Promise<string> {
  return `${LOGIN_COOKIE}=${await seal(keys, LOGIN_COOKIE, l)}; ${attributes}; Max-Age=${LOGIN_MS / 1000}`;
}

/** Set-Cookie that removes the cookie named name. */
export function clearCookie(name: typeof SESSION_COOKIE | typeof LOGIN_COOKIE): string {
  return `${name}=; ${attributes}; Max-Age=0`;
}
