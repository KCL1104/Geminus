"use client";
// The key metrics side by side (T-032, DESIGN.md Compare Table): baseline,
// scenario, Δ and Δ %, the scenario column in its wash. Every figure is
// the server's, written for the reader's language, with the exact value in
// <data value> (formatting never changes a value). Where the server has no
// value or no difference, the cell says why instead of showing a number.
import { Fragment, type ReactNode } from "react";
import { useI18n } from "../../lib/i18n/provider";
import { LineKey } from "../simulations/run-status";
import { formatAbsolute, formatRelative, formatValue, type MetricUnit } from "./format";
import type { Interval, MetricGroup, MetricLabel, MetricRow } from "./metrics";
import styles from "./results.module.css";

const groups: readonly MetricGroup[] = ["run", "resources", "queues"];

/** A metric's name as the table writes it (the analysis names its evidence the same way). */
export function MetricName({ label }: { label: MetricLabel }) {
  const { t } = useI18n();
  if ("key" in label) return <>{t(label.key, label.params)}</>;
  if (label.name) {
    const metric = t(label.name.key);
    return <>{label.name.station === null ? metric : t("metric.atStation", { metric, name: label.name.station })}</>;
  }
  // A pack's own metric: its key and unit as the server gave them (data, not catalog text).
  return (
    <>
      <code>{label.data}</code>
      {label.unit && (
        <>
          {" "}
          <span lang="en">({label.unit})</span>
        </>
      )}
    </>
  );
}

/** A message whose placeholders are elements: each marker the translator filled in is replaced by its node. */
function withNodes(text: string, nodes: Record<string, ReactNode>): ReactNode[] {
  const parts = text.split(/(\u0000\w+\u0000)/);
  return parts.map((p, i) => {
    const m = /^\u0000(\w+)\u0000$/.exec(p);
    return m ? <Fragment key={i}>{nodes[m[1]!]}</Fragment> : p;
  });
}

function Value({ value, unit, ci }: { value: number | null; unit: MetricUnit; ci: Interval | null }) {
  const i18n = useI18n();
  if (value === null) return <span className={styles.missing}>{i18n.t("compare.unavailable")}</span>;
  const bound = (v: number) => <data value={String(v)}>{formatValue(i18n, v, unit)}</data>;
  return (
    <>
      <data value={String(value)} className={styles.figure}>
        {formatValue(i18n, value, unit)}
      </data>
      {ci && (
        <span className={styles.ci}>
          {withNodes(i18n.t("compare.ci", { lower: "\u0000lower\u0000", upper: "\u0000upper\u0000" }), { lower: bound(ci.lower), upper: bound(ci.upper) })}
        </span>
      )}
    </>
  );
}

function Deltas({ row }: { row: MetricRow }) {
  const i18n = useI18n();
  const { t } = i18n;
  const why = row.status === "missing" ? t("compare.reason.missing") : row.reason ? t(`compare.reason.${row.reason}`) : null;
  if (row.absolute === null) {
    return (
      <td colSpan={2} className={styles.reason}>
        {why}
      </td>
    );
  }
  return (
    <>
      <td className={styles.delta}>
        <data value={String(row.absolute)}>{formatAbsolute(i18n, row.absolute, row.unit)}</data>
      </td>
      {row.relative === null ? (
        <td className={styles.reason}>{why}</td>
      ) : (
        <td className={styles.delta}>
          <data value={String(row.relative)}>{formatRelative(i18n, row.relative)}</data>
        </td>
      )}
    </>
  );
}

function Head() {
  const { t } = useI18n();
  return (
    <thead>
      <tr>
        <th scope="col">{t("compare.metric")}</th>
        <th scope="col" className={styles.num}>
          <LineKey side="baseline" />
          {t("compare.baseline")}
        </th>
        <th scope="col" className={`${styles.num} ${styles.scenarioCol}`}>
          <LineKey side="scenario" />
          {t("compare.scenario")}
        </th>
        <th scope="col" className={styles.num}>
          {t("compare.absolute")}
        </th>
        <th scope="col" className={styles.num}>
          {t("compare.relative")}
        </th>
      </tr>
    </thead>
  );
}

function Row({ row }: { row: MetricRow }) {
  return (
    <tr data-status={row.status}>
      <th scope="row">
        <MetricName label={row.label} />
      </th>
      <td className={styles.num}>
        <Value value={row.baseline} unit={row.unit} ci={row.ci.baseline} />
      </td>
      <td className={`${styles.num} ${styles.scenarioCol}`}>
        <Value value={row.scenario} unit={row.unit} ci={row.ci.scenario} />
      </td>
      <Deltas row={row} />
    </tr>
  );
}

/** A pack's own measure (named from the catalog, else by the server's key): one of each step's, kept one click away (DESIGN.md, Simplicity 3). */
const isStepMeasure = (r: MetricRow) => r.group === "run" && "data" in r.label;

export function MetricsTable({ rows }: { rows: MetricRow[] }) {
  const { t } = useI18n();
  const main = rows.filter((r) => !isStepMeasure(r));
  // Those with a value in either run first; the order is otherwise the server's.
  const steps = rows.filter(isStepMeasure);
  const measured = [...steps.filter((r) => r.baseline !== null || r.scenario !== null), ...steps.filter((r) => r.baseline === null && r.scenario === null)];
  return (
    <>
      <table className={styles.table}>
        <caption>{t("compare.caption")}</caption>
        <Head />
        {groups.map((g) => {
          const inGroup = main.filter((r) => r.group === g);
          if (inGroup.length === 0) return null;
          return (
            <tbody key={g}>
              <tr>
                <th scope="colgroup" colSpan={5} className={styles.group}>
                  {t(`compare.group.${g}`)}
                </th>
              </tr>
              {inGroup.map((row) => (
                <Row key={row.path} row={row} />
              ))}
            </tbody>
          );
        })}
      </table>
      {measured.length > 0 && (
        <details className={styles.more}>
          <summary>{t("compare.steps", { count: String(measured.length) })}</summary>
          <table className={styles.table}>
            <caption>{t("compare.stepsCaption")}</caption>
            <Head />
            <tbody>
              {measured.map((row) => (
                <Row key={row.path} row={row} />
              ))}
            </tbody>
          </table>
        </details>
      )}
    </>
  );
}
