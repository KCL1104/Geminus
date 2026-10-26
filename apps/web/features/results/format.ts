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

export function formatValue(i18n: I18n, value: number, unit: MetricUnit): string {
  if (unit === "ratio") return i18n.number(value, percent);
  if (unit === "minute") return i18n.number(value, { ...precise, ...minutes });
  return i18n.number(value, precise);
}

/** scenario − baseline as the server computed it; a ratio's difference is in percentage points. */
export function formatAbsolute(i18n: I18n, value: number, unit: MetricUnit): string {
  if (unit === "ratio") return i18n.t("compare.points", { value: i18n.number(value * 100, { maximumFractionDigits: 1, ...signed }) });
  if (unit === "minute") return i18n.number(value, { ...precise, ...minutes, ...signed });
  return i18n.number(value, { ...precise, ...signed });
}

/** The server's relative delta (absolute / baseline) as a signed percentage. */
export function formatRelative(i18n: I18n, value: number): string {
  return i18n.number(value, { ...percent, ...signed });
}
