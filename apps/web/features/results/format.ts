// Result numbers in the reader's language (REQ-022, TEST-031). Formatting
// only writes a value; it never recomputes one: the server's relative
// delta r is shown as r × 100 % (SIMULATION_SPEC §7), and every figure on
// the page also carries the exact server value in <data value> (T-032).
import type { I18n } from "../../lib/i18n/provider";

/** How a metric's numbers are written: a count, minutes, a ratio (shown as %), or a plain number. */
export type MetricUnit = "count" | "minute" | "ratio" | "number";

// Up to three significant digits or two decimals, whichever keeps more,
// so 29.1387 is 29.14 and 0.000309 is not rounded away to 0.
const precise = { maximumSignificantDigits: 3, maximumFractionDigits: 2, roundingPriority: "morePrecision" } as const satisfies Intl.NumberFormatOptions;
const minutes = { style: "unit", unit: "minute", unitDisplay: "short" } as const satisfies Intl.NumberFormatOptions;
const percent = { style: "percent", maximumFractionDigits: 1 } as const satisfies Intl.NumberFormatOptions;
const signed = { signDisplay: "exceptZero" } as const satisfies Intl.NumberFormatOptions;

/** From two hours on, a time in minutes also says its hours (H-073: 1,010.96 min is about 16.8 h). */
const HOURS_FROM_MINUTES = 120;

/**
 * A negative number's sign as a minus (U+2212) rather than a hyphen, as a
 * screen reader also reads it; and no sign on a figure whose digits are
 * all zero (H-073: a −0 from the server, or a small negative share shown
 * as 0%, is never written −0 or −0%).
 */
const minus = (s: string) => (/\d/.test(s) && !/[1-9]/.test(s) ? s.replace(/[-−+]/, "") : s.replace("-", "−"));

/** Where the minutes go in the hours message, so its own words around them can be set apart. */
const MINUTES = "\u0000minutes\u0000";

/**
 * formatValue in its parts: `shown`, the value with its unit, and the hours
 * message's words before and after it ("" when there are no hours). A cell
 * sets them apart (figure.tsx), so a narrow one can put the hours under the
 * minutes without breaking either (H-073 review).
 */
export function formatValueParts(i18n: I18n, value: number, unit: MetricUnit, { hours = true }: { hours?: boolean } = {}): { before: string; shown: string; after: string } {
  const only = (shown: string) => ({ before: "", shown, after: "" });
  if (unit === "ratio") return only(minus(i18n.number(value, percent)));
  if (unit !== "minute") return only(minus(i18n.number(value, precise)));
  const shown = minus(i18n.number(value, { ...precise, ...minutes }));
  if (!hours || !(Math.abs(value) >= HOURS_FROM_MINUTES)) return only(shown);
  const words = i18n.t("compare.hours", { minutes: MINUTES, hours: minus(i18n.number(value / 60, { maximumFractionDigits: 1 })) });
  const at = words.indexOf(MINUTES);
  return at < 0 ? only(words) : { before: words.slice(0, at), shown, after: words.slice(at + MINUTES.length) };
}

/** A value with its unit; minutes of two hours or more also with their hours, unless `hours` is false (an interval's bounds). */
export function formatValue(i18n: I18n, value: number, unit: MetricUnit, opts: { hours?: boolean } = {}): string {
  const { before, shown, after } = formatValueParts(i18n, value, unit, opts);
  return before + shown + after;
}

/** scenario − baseline as the server computed it; a ratio's difference is in percentage points. */
export function formatAbsolute(i18n: I18n, value: number, unit: MetricUnit): string {
  if (unit === "ratio") return i18n.t("compare.points", { value: minus(i18n.number(value * 100, { maximumFractionDigits: 1, ...signed })) });
  if (unit === "minute") return minus(i18n.number(value, { ...precise, ...minutes, ...signed }));
  return minus(i18n.number(value, { ...precise, ...signed }));
}

/** The server's relative delta (absolute / baseline) as a signed percentage. */
export function formatRelative(i18n: I18n, value: number): string {
  return minus(i18n.number(value, { ...percent, ...signed }));
}
