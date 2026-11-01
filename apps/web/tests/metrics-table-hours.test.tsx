// @vitest-environment happy-dom
// H-073 (the audit's quick fixes): in the key metrics, a lead time of two
// hours or more also says its hours beside the server's minutes, which stay
// the exact value in <data value>; each run's 95% interval under it stays
// in minutes, so the cell does not repeat the hours three times.
import { renderToStaticMarkup } from "react-dom/server";
import { describe, expect, it } from "vitest";
import { MetricsTable } from "../features/results/metrics-table";
import type { MetricRow } from "../features/results/metrics";
import { I18nProvider } from "../lib/i18n/provider";
import type { Locale } from "../lib/i18n/locale";

const row: MetricRow = {
  path: "/metrics/lead_time_minutes/mean_of_replication_means",
  group: "run",
  label: { key: "metric.leadTimeReplicationMean" },
  unit: "minute",
  baseline: 986.38,
  scenario: 908.65,
  absolute: -77.73,
  relative: -0.0788,
  status: "available",
  reason: null,
  ci: { baseline: { lower: 967.89, upper: 1004.87 }, scenario: null },
};

function cells(locale: Locale): { values: string[]; data: string[] } {
  const host = document.createElement("div");
  host.innerHTML = renderToStaticMarkup(
    <I18nProvider locale={locale}>
      <MetricsTable rows={[row]} />
    </I18nProvider>,
  );
  const tr = host.querySelector("tr[data-status]")!;
  return {
    values: [...tr.querySelectorAll("td")].map((td) => (td.textContent ?? "").replace(/\s+/g, " ").trim()),
    data: [...tr.querySelectorAll("data")].map((d) => d.getAttribute("value") ?? ""),
  };
}

describe("a lead time of two hours or more in the key metrics (H-073)", () => {
  it("says its hours beside the minutes; the interval and the difference stay in minutes; the exact values are the server's", () => {
    const en = cells("en");
    expect(en.values).toEqual(["986.38 min (16.4 h)95% CI 967.89 min to 1,004.87 min", "908.65 min (15.1 h)", "−77.73 min", "−7.9%"]);
    expect(en.data).toEqual(["986.38", "967.89", "1004.87", "908.65", "-77.73", "-0.0788"]);
    const zh = cells("zh-Hant");
    expect(zh.values[0]).toContain("986.38 分鐘（16.4 小時）");
    expect(zh.values[1]).toBe("908.65 分鐘（15.1 小時）");
  });
});
