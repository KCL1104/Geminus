"use client";
// H-073 (4): the AI explanation's evidence, each piece linked to the row of
// the metrics table it was checked against (the server checks every cited
// path against these runs' results, AI_SPEC §9). Following a link opens the
// step measures' disclosure when the row is in it, brings the row into view
// and puts the focus on it, where Selected Row Tint marks it
// (results.module.css). A row cited again is named again but not linked
// again: no two links to one place (DESIGN.md Simplicity 2). A path no row
// has stays the server's path, as before.
import type { MouseEvent } from "react";
import type { AnalysisOutput } from "../../lib/contracts";
import { useI18n } from "../../lib/i18n/provider";
import type { MetricRow } from "./metrics";
import { MetricName } from "./metrics-table";
import { metricRowId } from "./row-id";

export { metricRowId };

/** The row a cited path names: its own, or the row of a statistic under it (T-030). */
export function citedRow(path: string, rows: readonly MetricRow[]): MetricRow | undefined {
  return rows.find((r) => r.path === path) ?? rows.find((r) => r.path.startsWith(`${path}/`));
}

/** Opens the disclosures around a metric's row, focuses it and brings it into view; false when the page has no such row. */
export function goToMetricRow(path: string): boolean {
  const row = document.getElementById(metricRowId(path));
  if (row === null) return false;
  for (let d = row.closest("details"); d !== null; d = d.parentElement?.closest("details") ?? null) d.open = true;
  row.focus({ preventScroll: true });
  row.scrollIntoView({ block: "center" });
  return true;
}

/** Which citations ("<statement>/<evidence>") are links: the first one of each row. */
export function linkedCites(bottleneck: AnalysisOutput["bottleneck"], rows: readonly MetricRow[]): Set<string> {
  const seen = new Set<string>();
  const linked = new Set<string>();
  bottleneck.forEach((b, i) =>
    b.evidence.forEach((e, j) => {
      const row = citedRow(e.metric_path, rows);
      if (row === undefined || seen.has(row.path)) return;
      seen.add(row.path);
      linked.add(`${i}/${j}`);
    }),
  );
  return linked;
}

/** One piece of evidence: the metric named as the table names it (linked to its row when `link`), then the run. */
export function EvidenceCite({ path, rows, run, link }: { path: string; rows: readonly MetricRow[]; run: "baseline" | "scenario"; link: boolean }) {
  const { t } = useI18n();
  const row = citedRow(path, rows);
  const go = (e: MouseEvent<HTMLAnchorElement>) => {
    // A new tab or window is the browser's to open.
    if (e.button !== 0 || e.metaKey || e.ctrlKey || e.shiftKey || e.altKey) return;
    if (row && goToMetricRow(row.path)) e.preventDefault();
  };
  const name = row === undefined ? <code>{path}</code> : link ? (
    <a href={`#${metricRowId(row.path)}`} onClick={go}>
      <MetricName label={row.label} />
    </a>
  ) : (
    <MetricName label={row.label} />
  );
  return (
    <>
      {name} · {t(`analysis.run.${run}`)}
    </>
  );
}
