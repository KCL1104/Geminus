/**
 * One cookie's value from a Cookie header or document.cookie, read the way
 * the server reads it with Next.js cookies().get (@edge-runtime/cookies
 * parseCookie): pairs split at ";" and the spaces after it, each value
 * percent-decoded, a pair whose value does not decode skipped, a pair
 * without "=" read as "true", and of two pairs with the name the last kept.
 * The app's own cookies (language, theme) hold plain words; reading every
 * value alike keeps a page and its global-error fallback in agreement on
 * whatever a cookie holds (T-044 review 2 N5, T-045 review 1 I3).
 */
export function cookieValue(header: string, name: string): string | undefined {
  let value: string | undefined;
  for (const pair of header.split(/; */)) {
    const at = pair.indexOf("=");
    if (at < 0) {
      if (pair === name) value = "true";
      continue;
    }
    if (pair.slice(0, at) !== name) continue;
    try {
      value = decodeURIComponent(pair.slice(at + 1));
    } catch {
      // Not decodable: skipped, as on the server.
    }
  }
  return value;
}
