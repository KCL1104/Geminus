"use client";
// The reader's locale for client components. Without a provider (tests,
// isolated components) it is the default locale.
import { createContext, type ReactNode, useContext, useMemo } from "react";
import { defaultLocale, type Locale } from "./locale";
import { createTranslator, formatDateTime, formatDuration, formatNumber, type Translator } from "./translate";

export type I18n = {
  locale: Locale;
  t: Translator;
  /** A date-time from the API; a value that is not one is shown as it came rather than failing the page. */
  dateTime: (iso: string) => string;
  number: (n: number, options?: Intl.NumberFormatOptions) => string;
  duration: (value: number, unit: "second" | "minute" | "hour" | "day") => string;
};

export function i18nFor(locale: Locale): I18n {
  return {
    locale,
    t: createTranslator(locale),
    dateTime: (iso) => {
      try {
        return formatDateTime(locale, iso);
      } catch {
        return iso;
      }
    },
    number: (n, options) => formatNumber(locale, n, options),
    duration: (value, unit) => formatDuration(locale, value, unit),
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
