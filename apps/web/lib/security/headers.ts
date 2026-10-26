// The web app's security headers (T-057, docs/SECURITY.md §3, ADR-018
// addendum). proxy.ts puts them on every response, with a nonce made for
// that request; the reasons for each directive are in docs/SECURITY.md.
// Pure: no request, no environment, so the tests can check every directive.

/** The request header that hands the request's nonce to the root layout (proxy.ts sets it; a client's is replaced). */
export const NONCE_HEADER = "x-nonce";

/** 16 random bytes in base64: what newNonce makes, and the only nonce the policy takes. */
const NONCE = /^[A-Za-z0-9+/]{22}==$/;

export function isNonce(value: string | null | undefined): value is string {
  return typeof value === "string" && NONCE.test(value);
}

/** A nonce for one response: 128 bits from the platform's CSPRNG. */
export function newNonce(): string {
  return btoa(String.fromCharCode(...crypto.getRandomValues(new Uint8Array(16))));
}

export type PolicyOptions = {
  /** The request's nonce (newNonce). */
  nonce: string;
  /** The app is served over https (servedOverHttps): HSTS and upgrade-insecure-requests. */
  https: boolean;
  /** next dev: React's debugging aids need eval there and the dev overlay inline style elements; never in a build. */
  development: boolean;
};

/**
 * The Content-Security-Policy. Scripts run only with the request's nonce
 * (Next.js puts it on its own) or when such a script loads them
 * ('strict-dynamic'); style elements need the nonce too (React Aria reads
 * it from the layout's csp-nonce meta); style attributes are allowed,
 * since libraries render them on the server where no nonce can reach them.
 * Everything else comes from the app's own origin, or nowhere.
 */
export function contentSecurityPolicy({ nonce, https, development }: PolicyOptions): string {
  if (!isNonce(nonce)) throw new Error("the nonce must be 16 random bytes in base64 (newNonce)");
  const n = `'nonce-${nonce}'`;
  const directives: string[][] = [
    ["default-src", "'none'"],
    // Next.js takes the nonce from the first directive starting with script-src.
    ["script-src", "'self'", n, "'strict-dynamic'", ...(development ? ["'unsafe-eval'"] : [])],
    // Only browsers without style-src-elem and style-src-attr read this one;
    // a nonce here would turn 'unsafe-inline' off for the attributes as well.
    ["style-src", "'self'", "'unsafe-inline'"],
    // next dev's overlay adds style elements without the nonce (a nonce
    // would turn 'unsafe-inline' off); a build never has them.
    ["style-src-elem", "'self'", ...(development ? ["'unsafe-inline'"] : [n])],
    ["style-src-attr", "'unsafe-inline'"],
    ["img-src", "'self'"],
    ["font-src", "'self'"],
    ["connect-src", "'self'"],
    ["object-src", "'none'"],
    ["base-uri", "'none'"],
    // The sign-in is a link and a 303 to the provider, not a form; no form posts elsewhere.
    ["form-action", "'self'"],
    ["frame-ancestors", "'none'"],
    ...(https ? [["upgrade-insecure-requests"]] : []),
  ];
  return directives.map((d) => d.join(" ")).join("; ");
}

/** One year; no includeSubDomains or preload until the owner chooses the domain. */
export const HSTS = "max-age=31536000";

/**
 * Every powerful feature denied: the app uses none (the photo is a file
 * input, not the camera). Only names Chromium knows: an unknown one is a
 * console warning on every page (the browser checks fail on it).
 */
export const PERMISSIONS_POLICY = [
  "camera", "microphone", "geolocation", "display-capture", "screen-wake-lock", "fullscreen", "picture-in-picture",
  "autoplay", "encrypted-media", "midi", "usb", "serial", "hid", "payment", "publickey-credentials-get",
  "accelerometer", "gyroscope", "magnetometer", "idle-detection", "local-fonts", "browsing-topics", "xr-spatial-tracking",
].map((f) => `${f}=()`).join(", ");

/** The headers for one response, in the order they are set. */
export function securityHeaders(options: PolicyOptions): [string, string][] {
  return [
    ["Content-Security-Policy", contentSecurityPolicy(options)],
    ["X-Content-Type-Options", "nosniff"],
    // Not even the origin: nothing reads a Referer (the BFF's CSRF check is
    // Origin, which fetch sends whatever this says).
    ["Referrer-Policy", "no-referrer"],
    ["Permissions-Policy", PERMISSIONS_POLICY],
    // frame-ancestors 'none' for the browsers that do not read CSP.
    ["X-Frame-Options", "DENY"],
    ...(options.https ? [["Strict-Transport-Security", HSTS] as [string, string]] : []),
  ];
}

/**
 * Whether the app is served over https: the deployment's configured origin
 * (BFF_APP_ORIGIN, the one the BFF's redirects and CSRF check use), never the
 * request's scheme or X-Forwarded-Proto, which a client controls and which
 * is http behind the platform's TLS edge.
 */
export function servedOverHttps(appOrigin: string | undefined): boolean {
  if (appOrigin === undefined) return false;
  try {
    return new URL(appOrigin.trim()).protocol === "https:";
  } catch {
    return false;
  }
}
