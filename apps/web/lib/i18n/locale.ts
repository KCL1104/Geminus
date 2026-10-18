// UI languages (REQ-022, ADR-020): en is the default, zh-Hant the other.
// Kept equal to schemas/common.schema.json#/$defs/Locale by tests/i18n.test.ts.
export const locales = ["en", "zh-Hant"] as const;
export type Locale = (typeof locales)[number];
export const defaultLocale: Locale = "en";

/**
 * The cookie that remembers the choice. It holds the language only: no
 * identity, no session, nothing the server uses to authorize (REQ-022).
 */
export const localeCookie = "geminus_locale";

/** The locale a stored value names, exactly; anything else is the default. */
export function parseLocale(value: string | null | undefined): Locale {
  return locales.find((l) => l === value) ?? defaultLocale;
}

/** The Set-Cookie value for remembering locale for a year. */
export function localeCookieValue(locale: Locale, secure: boolean): string {
  return `${localeCookie}=${locale}; Path=/; Max-Age=31536000; SameSite=Lax${secure ? "; Secure" : ""}`;
}
