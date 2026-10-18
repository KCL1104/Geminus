"use client";
// The reader's locale for client components. Without a provider (tests,
// isolated components) it is the default locale.
import { createContext, type ReactNode, useContext, useMemo, useSyncExternalStore } from "react";
import { cookieLocale, defaultLocale, type Locale } from "./locale";
import { createTranslator, formatDateTime, formatDuration, formatNumber, type Translator } from "./translate";

export type I18n = {
  locale: Locale;
  t: Translator;
  /**
   * A date-time from the API. A string that is not one is shown as it came,
   * and a value that is not a string as nothing, rather than failing the
   * page (T-044 review M2).
   */
  dateTime: (iso: string) => string;
  /** What goes between two sentences, or a label and its value: a space in English, nothing in Chinese. */
  gap: string;
  number: (n: number, options?: Intl.NumberFormatOptions) => string;
  duration: (value: number, unit: "second" | "minute" | "hour" | "day") => string;
};

export function i18nFor(locale: Locale): I18n {
  return {
    locale,
    t: createTranslator(locale),
    dateTime: (iso: unknown) => {
      if (typeof iso !== "string") return "";
      try {
        return formatDateTime(locale, iso);
      } catch {
        return iso;
      }
    },
    number: (n, options) => formatNumber(locale, n, options),
    duration: (value, unit) => formatDuration(locale, value, unit),
    gap: locale === "zh-Hant" ? "" : " ",
  };
}

const I18nContext = createContext<I18n>(i18nFor(defaultLocale));

export function I18nProvider({ locale, children }: { locale: Locale; children: ReactNode }) {
  const value = useMemo(() => i18nFor(locale), [locale]);
  return <I18nContext.Provider value={value}>{children}</I18nContext.Provider>;
}

export function useI18n(): I18n {
  return useContext(I18nContext);
}

const noSubscription = () => () => {};

/**
 * The remembered choice as the browser sees it, for a page rendered without
 * the server's locale (global-error): the default on the server, the
 * cookie's in the browser.
 */
export function useRememberedLocale(): Locale {
  return useSyncExternalStore(noSubscription, () => cookieLocale(document.cookie), () => defaultLocale);
}
