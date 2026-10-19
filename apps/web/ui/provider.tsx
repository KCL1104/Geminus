"use client";
// Gives React Aria the reader's locale, so the few strings React Aria
// supplies itself (H-008, ADR-021) come in the app's language.
import type { ReactNode } from "react";
import { I18nProvider } from "react-aria-components";
import { ariaLocale } from "../lib/i18n/aria-locale";
import { useI18n } from "../lib/i18n/provider";

export function UiProvider({ children }: { children: ReactNode }) {
  const { locale } = useI18n();
  return <I18nProvider locale={ariaLocale(locale)}>{children}</I18nProvider>;
}
