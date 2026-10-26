// The colour theme (H-013, DESIGN.md): it follows the system unless the
// reader chose light or dark, and the choice is remembered. The choice is a
// cookie, like the language (ADR-020), so the root layout renders it as
// <html data-theme> and the first paint already has the right colours: no
// script runs before paint and there is no flash of the other theme.
import { cookieValue } from "../cookie";

export const themes = ["system", "light", "dark"] as const;
export type Theme = (typeof themes)[number];
export const defaultTheme: Theme = "system";

/**
 * The cookie that remembers the choice: "light" or "dark", nothing else; no
 * cookie means the system's. No identity, nothing the server authorizes by.
 */
export const themeCookie = "geminus_theme";

/** The theme a stored value names, exactly; anything else is the system's. */
export function parseTheme(value: string | null | undefined): Theme {
  return value === "light" || value === "dark" ? value : defaultTheme;
}

/** The theme a Cookie header (or document.cookie) names, read as the server reads it (lib/cookie.ts), as for the locale. */
export function cookieTheme(header: string): Theme {
  return parseTheme(cookieValue(header, themeCookie));
}

/** The Set-Cookie value that remembers a choice for a year, or forgets it (back to the system's). */
export function themeCookieValue(theme: Theme, secure: boolean): string {
  const tail = `; Path=/; SameSite=Lax${secure ? "; Secure" : ""}`;
  return theme === "system" ? `${themeCookie}=${tail}; Max-Age=0` : `${themeCookie}=${theme}; Max-Age=31536000${tail}`;
}

/** The <html data-theme> value for a theme: none for the system's. */
export function themeAttribute(theme: Theme): "light" | "dark" | undefined {
  return theme === "system" ? undefined : theme;
}
