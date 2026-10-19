import type { Locale } from "./locale";

/**
 * The locale React Aria needs for the app's locale (ADR-021). React Aria
 * picks its own strings by region: "zh-Hant" falls back to its Simplified
 * strings, "zh-TW" gives Traditional ones (tests/ui-provider.test.tsx).
 */
export function ariaLocale(locale: Locale): string {
  return locale === "zh-Hant" ? "zh-TW" : "en-US";
}
