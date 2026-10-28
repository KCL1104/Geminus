// T-030 live (2026-10-28): the table and the AI explanation write a pack
// metric the catalog names by its name, at its station (D-117 (h)), and
// one the catalog does not name by the server's key and unit.
import { renderToStaticMarkup } from "react-dom/server";
import { describe, expect, it } from "vitest";
import { MetricName } from "../features/results/metrics-table";
import type { MetricLabel } from "../features/results/metrics";
import { I18nProvider } from "../lib/i18n/provider";
import type { Locale } from "../lib/i18n/locale";

const html = (label: MetricLabel, locale: Locale = "en") => renderToStaticMarkup(<I18nProvider locale={locale}>{<MetricName label={label} />}</I18nProvider>);

describe("a pack metric's name", () => {
  const blocked: MetricLabel = { data: "blocking_ratio.ict", unit: null, name: { key: "domain.electronics_line.metric.blocking_ratio", station: "ICT station 1" } };

  it("is the catalog's, at the station's name, in the reader's language", () => {
    expect(html(blocked)).toBe("Blocked ratio: ICT station 1");
    expect(html(blocked, "zh-Hant")).toBe("阻塞比例：ICT station 1");
    expect(html({ data: "scrap_rate", unit: null, name: { key: "domain.electronics_line.metric.scrap_rate", station: null } })).toBe("Scrap rate");
  });

  it("is the server's key and unit when the catalog has none", () => {
    expect(html({ data: "made_up.ict", unit: "widgets" })).toBe('<code>made_up.ict</code> <span lang="en">(widgets)</span>');
  });
});
