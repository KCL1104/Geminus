// Message lookup (ADR-020). Catalogs are TypeScript objects: the zh-Hant
// one is typed against the en keys, so a missing or extra key does not
// compile, and tests/i18n.test.ts checks placeholders and plural forms.
import { en } from "../../messages/en";
import { zhHant } from "../../messages/zh-Hant";
import type { Locale } from "./locale";

/** A message, or its plural forms (Intl.PluralRules categories; `other` always). */
export type Message = string | { readonly one?: string; readonly other: string };
export type MessageKey = keyof typeof en;
export type Catalog = { readonly [K in MessageKey]: (typeof en)[K] extends string ? string : { readonly one?: string; readonly other: string } };
export type Params = Readonly<Record<string, string | number>>;

export const catalogs: Readonly<Record<Locale, Catalog>> = { en, "zh-Hant": zhHant };

const placeholder = /\{(\w+)\}/g;

/** The placeholder names a message (or any of its plural forms) uses. */
export function placeholders(m: Message): string[] {
  const forms = typeof m === "string" ? [m] : Object.values(m).filter((f): f is string => typeof f === "string");
  return [...new Set(forms.flatMap((f) => [...f.matchAll(placeholder)].map((x) => x[1] ?? "")))].sort();
}

export type Translator = (key: MessageKey, params?: Params) => string;

/**
 * A translator for locale. Numbers in params are formatted for the locale;
 * a plural message picks its form by params.count. A placeholder without a
 * value is a programming error, so it throws instead of showing "{name}".
 */
export function createTranslator(locale: Locale): Translator {
  const catalog: Catalog = catalogs[locale];
  const numbers = new Intl.NumberFormat(locale);
  const plural = new Intl.PluralRules(locale);
  return (key, params = {}) => {
    const m: Message = catalog[key];
    let text: string;
    if (typeof m === "string") {
      text = m;
    } else {
      const count = params.count;
      if (typeof count !== "number") throw new Error(`message ${key} needs a numeric count`);
      text = (plural.select(count) === "one" ? m.one : undefined) ?? m.other;
    }
    return text.replace(placeholder, (_, name: string) => {
      const v = Object.hasOwn(params, name) ? params[name] : undefined;
      if (v === undefined) throw new Error(`message ${key} has no value for {${name}}`);
      return typeof v === "number" ? numbers.format(v) : v;
    });
  };
}

// RFC 3339 date-time, as the contracts use (T and Z in either case);
// Date.parse alone accepts much more.
const dateTime = /^(\d{4})-(\d{2})-(\d{2})T(\d{2}):(\d{2}):(\d{2})(?:\.\d+)?(?:Z|[+-](\d{2}):(\d{2}))$/i;

// Whether the fields name a time that exists: Date rolls 2026-02-30 over to
// March 2 instead of refusing it (T-044 review L4). A leap second (:60)
// cannot be shown either.
function exists(m: RegExpExecArray): boolean {
  const [year, month, day, hour, minute, second, offsetHour = 0, offsetMinute = 0] = m.slice(1).map((f) => (f === undefined ? undefined : Number(f)));
  const days = new Date(Date.UTC(year!, month!, 0)).getUTCDate();
  return month! >= 1 && month! <= 12 && day! >= 1 && day! <= days && hour! <= 23 && minute! <= 59 && second! <= 59 && offsetHour <= 23 && offsetMinute <= 59;
}

/** Dates and times in the reader's time zone, written the locale's way. */
export function formatDateTime(locale: Locale, iso: string): string {
  const m = dateTime.exec(iso);
  const d = new Date(iso.toUpperCase());
  if (m === null || !exists(m) || Number.isNaN(d.getTime())) throw new Error("not a date-time");
  return new Intl.DateTimeFormat(locale, { dateStyle: "medium", timeStyle: "short" }).format(d);
}

export function formatNumber(locale: Locale, n: number, options?: Intl.NumberFormatOptions): string {
  return new Intl.NumberFormat(locale, options).format(n);
}

/** A duration in one unit, e.g. "3 hours" / "3 小時". */
export function formatDuration(locale: Locale, value: number, unit: "second" | "minute" | "hour" | "day"): string {
  return new Intl.NumberFormat(locale, { style: "unit", unit, unitDisplay: "long" }).format(value);
}
