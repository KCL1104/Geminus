"use client";
// The Marey chart of the sampled traces (T-032, DESIGN.md Marey Strip):
// time left to right, the steps top to bottom, one line per sampled work
// item; the baseline dashed graphite, the scenario solid green (The Two
// Inks Rule: the dash carries the difference, the hue confirms it). It is
// an image with a title and a description, a key in words, and the same
// content as a table one click away (WCAG 1.1.1, H-027 progressive
// disclosure). Nothing in it is interactive, so nothing in it takes focus.
// The geometry is marey.ts's, for the width the chart actually has.
import { useEffect, useId, useMemo, useRef, useState } from "react";
import type { ModelIR, TraceArtifact } from "../../lib/contracts";
import { useI18n } from "../../lib/i18n/provider";
import { ProblemView } from "../../app/_components/problem-view";
import { LineKey, type RunSide } from "../simulations/run-status";
import { formatValue } from "./format";
import type { TraceState } from "./load";
import { entityPaths, mareyLayout, rowResolver, stepRows, traceSummary, type StepSummary } from "./marey";
import styles from "./results.module.css";

export type ChartRun = { trace: TraceState; endTick: number };

/** Width before the chart is measured (the first render, or no layout at all). */
const FALLBACK_WIDTH = 1040;

function useWidth(ref: React.RefObject<HTMLElement | null>, shown: boolean): number {
  const [width, setWidth] = useState(0);
  useEffect(() => {
    const el = ref.current;
    if (!el) return;
    const measure = () => setWidth(Math.floor(el.getBoundingClientRect().width));
    measure();
    if (typeof ResizeObserver === "undefined") return;
    const observer = new ResizeObserver(measure);
    observer.observe(el);
    return () => observer.disconnect();
  }, [ref, shown]);
  return width > 0 ? width : FALLBACK_WIDTH;
}

/** An SVG polyline's points attribute. */
const svgPoints = (points: [number, number][]) => points.map(([x, y]) => `${x},${y}`).join(" ");

/** A trace with events to draw, or null. */
const drawable = (s: TraceState): TraceArtifact | null => (s.state === "loaded" && s.trace.events.length > 0 ? s.trace : null);

export function MareyChart({ ir, baseline, scenario }: { ir: ModelIR; baseline: ChartRun; scenario: ChartRun }) {
  const { t, number } = useI18n();
  const id = useId();
  const box = useRef<HTMLDivElement>(null);
  const traces = { baseline: drawable(baseline.trace), scenario: drawable(scenario.trace) };
  const width = useWidth(box, traces.baseline !== null || traces.scenario !== null);
  const endTick = Math.max(baseline.endTick, scenario.endTick);
  const rows = useMemo(() => stepRows(ir), [ir]);
  const paths = useMemo(() => {
    const row = rowResolver(ir, rows);
    const of = (tr: TraceArtifact | null) => (tr === null ? null : entityPaths(tr, row, endTick));
    return { baseline: of(traces.baseline), scenario: of(traces.scenario) };
  }, [ir, rows, traces.baseline, traces.scenario, endTick]);
  const layout = useMemo(() => mareyLayout({ rows, baseline: paths.baseline, scenario: paths.scenario, endTick, width }), [rows, paths, endTick, width]);

  const sides = ["baseline", "scenario"] as const;
  const state = { baseline: baseline.trace, scenario: scenario.trace };
  const notes = sides.flatMap((side) => {
    const s = state[side];
    const other = sides.find((x) => x !== side)!;
    if (s.state === "failed") return [<div key={side}><p>{t(`marey.failed.${side}`)}</p><ProblemView problem={s.problem} headingLevel={3} /></div>];
    if (traces[side] === null && traces[other] !== null) return [<p key={side} className={styles.note}>{t(`marey.missing.${side}`)}</p>];
    if (traces[side]?.truncated) return [<p key={side} className={styles.note}>{t(`marey.truncated.${side}`)}</p>];
    return [];
  });
  const sample = traces.baseline ?? traces.scenario;
  const capped = [layout.baseline, layout.scenario].find((r) => r !== null && r.drawn < r.total);

  return (
    <section aria-labelledby={`${id}-h`} className={styles.section}>
      <h2 id={`${id}-h`}>{t("marey.title")}</h2>
      {sample === null ? (
        <>
          {notes}
          {baseline.trace.state !== "failed" && scenario.trace.state !== "failed" && <p className={styles.note}>{t("marey.none")}</p>}
        </>
      ) : (
        <>
          <ul className={styles.legend}>
            {sides.map((side) => (
              <li key={side}>
                <LineKey side={side} />
                {t(`marey.${side}`)}
              </li>
            ))}
            <li className={styles.hint}>{t("marey.hint")}</li>
          </ul>
          <p className={styles.note}>
            {t("marey.sampling", { replication: sample.replication_index + 1, every: Math.round(1 / sample.sampling.rate) })}
          </p>
          {notes}
          {capped && <p className={styles.note}>{t("marey.capped", { drawn: capped.drawn, total: capped.total })}</p>}
          <div ref={box} className={styles.chart}>
            <svg role="img" aria-labelledby={`${id}-t ${id}-d`} width={layout.width} height={layout.height} viewBox={`0 0 ${layout.width} ${layout.height}`} className={styles.marey}>
              <title id={`${id}-t`}>{t("marey.title")}</title>
              <desc id={`${id}-d`}>{t("marey.desc", { baseline: layout.baseline?.total ?? 0, scenario: layout.scenario?.total ?? 0 })}</desc>
              {layout.minor.map((x, i) => (
                <line key={`m${i}`} className={styles.gridMinor} x1={x} y1={layout.top} x2={x} y2={layout.bottom} />
              ))}
              {layout.ticks.map((tk) => (
                <line key={`M${tk.value}`} className={styles.gridMajor} x1={tk.x} y1={layout.top} x2={tk.x} y2={layout.bottom} />
              ))}
              {layout.rows.map((r) => (
                <line key={`r${r.id}`} className={styles.rowRule} x1={0} y1={r.y} x2={layout.x1} y2={r.y} />
              ))}
              <line className={styles.rowRule} x1={0} y1={layout.bottom} x2={layout.x1} y2={layout.bottom} />
              {layout.stations.map((y) => (
                <line key={`s${y}`} className={styles.station} x1={layout.x0} y1={y} x2={layout.x1} y2={y} />
              ))}
              <text className={styles.axisTitle} x={8} y={14}>
                {t(`marey.axis.${layout.unit}`)}
              </text>
              {layout.ticks.map((tk) => (
                <text key={`a${tk.value}`} className={styles.axis} x={tk.x} y={14} textAnchor="middle">
                  {number(tk.value)}
                </text>
              ))}
              {layout.rows.map((r) => (
                <text key={`l${r.id}`} className={styles.rowLabel} x={8} y={r.y + r.height / 2} dominantBaseline="central">
                  {r.label}
                </text>
              ))}
              {sides.map((side) =>
                layout[side]?.lines.map((line) => (
                  <polyline key={`${side}${line.entity}`} data-run={side} className={styles.line} points={svgPoints(line.points)} />
                )),
              )}
              {sides.map((side) =>
                layout[side]?.releases.map(([x, y], i) => <circle key={`${side}d${i}`} data-run={side} className={styles.release} cx={x} cy={y} r={2.2} />),
              )}
            </svg>
          </div>
          <TraceTable rows={rows.map((r) => r.name)} summaries={{ baseline: paths.baseline && traceSummary(paths.baseline, rows), scenario: paths.scenario && traceSummary(paths.scenario, rows) }} />
        </>
      )}
    </section>
  );
}

function TraceTable({ rows, summaries }: { rows: string[]; summaries: Record<RunSide, StepSummary[] | null> }) {
  const { t, number } = useI18n();
  const i18n = useI18n();
  const sides = (["baseline", "scenario"] as const).filter((s) => summaries[s] !== null);
  const wait = (ticks: number | null) => (ticks === null ? t("marey.table.none") : <data value={String(ticks / 60_000)}>{formatValue(i18n, ticks / 60_000, "minute")}</data>);
  return (
    <details className={styles.details}>
      <summary>{t("marey.table")}</summary>
      <table className={styles.table}>
        <caption>{t("marey.table.caption")}</caption>
        <thead>
          <tr>
            <th scope="col">{t("marey.table.step")}</th>
            <th scope="col">{t("runs.title")}</th>
            <th scope="col">{t("marey.table.items")}</th>
            <th scope="col">{t("marey.table.longest")}</th>
            <th scope="col">{t("marey.table.atEnd")}</th>
          </tr>
        </thead>
        {rows.map((name, r) => (
          <tbody key={r}>
            {sides.map((side, i) => {
              const s = summaries[side]![r]!;
              return (
                <tr key={side}>
                  {i === 0 && (
                    <th scope="rowgroup" rowSpan={sides.length}>
                      {name}
                    </th>
                  )}
                  <td>{t(`runs.${side}`)}</td>
                  <td>{number(s.items)}</td>
                  <td>{wait(s.longestWait)}</td>
                  <td>{number(s.waitingAtEnd)}</td>
                </tr>
              );
            })}
          </tbody>
        ))}
      </table>
    </details>
  );
}
