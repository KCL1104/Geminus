"use client";
// H-073 (3): the line-constraint card. One stated rule over the server's
// comparison rows (constraint.ts): the station it names, that station's
// work ratio and scheduled utilization in both runs with the server's
// difference (baseline → scenario, the scenario in green at 600: DESIGN.md
// Words around it), the largest blocked-ratio change the server computed,
// and the rule itself in the caption. It claims no cause. A pack without a work ratio per station gets
// nothing; a baseline without one, the rule and "unavailable".
//
// Mount (not yet: results-view.tsx belongs to another slice): in the main
// column of ResultsView, right after the Key metrics section and before the
// 3D view, as <LineConstraint rows={rows} />, with the same rows the
// metrics table gets.
import { useId } from "react";
import { useI18n } from "../../lib/i18n/provider";
import { lineConstraint, type BlockedChange, type StationRow } from "./constraint";
import { formatAbsolute, formatValue } from "./format";
import type { MetricRow } from "./metrics";
import { withNodes } from "./metrics-table";
import styles from "./line-constraint.module.css";

/** A station's name as the table names it (the IR's), else its key. */
function stationName({ station, row }: StationRow): string {
  return "data" in row.label && row.label.name?.station ? row.label.name.station : station;
}

/** A station measure's name without the station: the catalog's, else the pack's key. */
function useMeasureName() {
  const { t } = useI18n();
  return (row: MetricRow): string => {
    if ("data" in row.label) return row.label.name ? t(row.label.name.key) : row.label.data.slice(0, row.label.data.indexOf("."));
    return t(row.label.key, row.label.params);
  };
}

/** The server's figures for one row: each run's exact value and the difference (or why there is none), in <data value>. */
function usePairNodes() {
  const i18n = useI18n();
  const { t } = i18n;
  return (row: MetricRow) => {
    const value = (v: number | null, side: "baseline" | "scenario") =>
      v === null ? (
        <span className={styles.missing}>{t("compare.unavailable")}</span>
      ) : (
        <data value={String(v)} data-run={side} className={side === "scenario" ? styles.scenario : undefined}>
          {formatValue(i18n, v, row.unit)}
        </data>
      );
    const why = row.status === "missing" ? t("compare.reason.missing") : row.reason ? t(`compare.reason.${row.reason}`) : t("compare.unavailable");
    return {
      baseline: value(row.baseline, "baseline"),
      scenario: value(row.scenario, "scenario"),
      delta:
        row.absolute === null ? (
          <span className={styles.missing}>{why}</span>
        ) : (
          <data value={String(row.absolute)} className={styles.delta}>
            {formatAbsolute(i18n, row.absolute, row.unit)}
          </data>
        ),
    };
  };
}

const marks = { baseline: "\u0000baseline\u0000", scenario: "\u0000scenario\u0000", delta: "\u0000delta\u0000" };

/** Baseline → scenario (the difference), as the catalog writes the pair. */
function Pair({ row }: { row: MetricRow }) {
  const { t } = useI18n();
  const nodes = usePairNodes()(row);
  return <>{withNodes(t("constraint.pair", marks), nodes)}</>;
}

function Blocked({ change }: { change: BlockedChange }) {
  const { t } = useI18n();
  const pair = usePairNodes();
  if (change.kind === "unavailable") return <span className={styles.missing}>{t("constraint.blockedUnavailable")}</span>;
  if (change.kind === "unchanged") return <>{t("constraint.blockedUnchanged")}</>;
  return (
    <>
      {withNodes(t("constraint.blockedAt", { station: "\u0000station\u0000", ...marks }), {
        station: <bdi>{stationName(change.at)}</bdi>,
        ...pair(change.at.row),
      })}
    </>
  );
}

export function LineConstraint({ rows }: { rows: readonly MetricRow[] }) {
  const { t } = useI18n();
  const measure = useMeasureName();
  const heading = useId();
  const c = lineConstraint(rows);
  if (c.state === "none") return null;
  return (
    <section aria-labelledby={heading} className={styles.card}>
      <h2 id={heading}>{t("constraint.title")}</h2>
      {c.state === "unavailable" ? (
        <p className={styles.named}>{t("constraint.unavailable")}</p>
      ) : (
        <>
          <p className={styles.named}>
            {withNodes(t("constraint.named", { station: "\u0000station\u0000" }), { station: <bdi className={styles.station}>{stationName(c.at)}</bdi> })}
          </p>
          <dl className={styles.figures}>
            <dt>{measure(c.at.row)}</dt>
            <dd>
              <Pair row={c.at.row} />
            </dd>
            {c.scheduled && (
              <>
                <dt>{t("constraint.counts", { metric: measure(c.scheduled) })}</dt>
                <dd>
                  <Pair row={c.scheduled} />
                </dd>
              </>
            )}
            <dt>{t("constraint.blocked")}</dt>
            <dd>
              <Blocked change={c.blocked} />
            </dd>
          </dl>
        </>
      )}
      <p className={styles.rule}>{t("constraint.rule")}</p>
    </section>
  );
}
