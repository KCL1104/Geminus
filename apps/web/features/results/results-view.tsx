"use client";
// The results of a scenario beside its baseline (T-032, H-029): whether
// the server could compare them (D-088), its warnings, the median lead time
// in one sentence, the key metrics, the line constraint by its stated rule
// (H-073 (3)), the Marey chart of the sampled traces, and in the side panel
// the AI explanation (the pair's saved one when there is one, H-073 (1)),
// the settings both runs were frozen with (DESIGN.md The Provenance Always
// Rule) and their reproducibility (H-073 (4)); under them the line in motion
// (H-072). Everything comes from ResultsData, except what the saved
// explanation and the proof read for themselves (the pair's analyses, the
// model's other runs).
import { Fragment } from "react";
import type { Duration, Run } from "../../lib/contracts";
import { useI18n } from "../../lib/i18n/provider";
import { Callout } from "../../ui/callout";
import { formatValue } from "./format";
import { LineConstraint } from "./line-constraint";
import type { ResultsData } from "./load";
import { MareyChart } from "./marey-chart";
import { keyMetrics, leadTimeSummary } from "./metrics";
import { MetricsTable, withNodes } from "./metrics-table";
import { ResultProof } from "./proof";
import { SavedExplanation } from "./saved-explanation";
import { Playback } from "../twin-3d/playback/playback";
import styles from "./results.module.css";

const secondsPer: Readonly<Record<Duration["unit"], number>> = { millisecond: 0.001, second: 1, minute: 60, hour: 3600, day: 86400 };

function Facts({ baseline, scenario, version }: { baseline: Run; scenario: Run; version: number }) {
  const { t, duration, number } = useI18n();
  // A run length as the locale writes it, with its exact value as an HTML duration.
  const span = (d: Duration) => {
    const text = d.unit === "millisecond" ? duration(d.value / 1000, "second") : duration(d.value, d.unit);
    return <time dateTime={`PT${(d.value * secondsPer[d.unit]).toFixed(3).replace(/\.?0+$/, "")}S`}>{text}</time>;
  };
  // One value when both runs share it, both (baseline first) when they do not.
  const both = (b: React.ReactNode, s: React.ReactNode, same: boolean) => (same ? b : <>{b}{" · "}{s}</>);
  const sameSpan = (a: Duration, b: Duration) => a.value === b.value && a.unit === b.unit;
  const { config: bc } = baseline;
  const { config: sc } = scenario;
  const facts: [string, React.ReactNode][] = [
    [t("facts.version"), t("facts.versionValue", { version: String(version) })],
    [t("facts.runs"), <span key="ids" className={styles.ids}><code>{baseline.id}</code><code>{scenario.id}</code></span>],
    [t("facts.horizon"), both(span(bc.horizon), span(sc.horizon), sameSpan(bc.horizon, sc.horizon))],
    [t("facts.warmup"), both(span(bc.warmup), span(sc.warmup), sameSpan(bc.warmup, sc.warmup))],
    [t("facts.replications"), both(number(bc.replications), number(sc.replications), bc.replications === sc.replications)],
    [t("facts.seed"), both(String(bc.seed), String(sc.seed), bc.seed === sc.seed)],
    [t("facts.engine"), both(<code>{baseline.engine_version}</code>, <code>{scenario.engine_version}</code>, baseline.engine_version === scenario.engine_version)],
  ];
  return (
    <section aria-labelledby="facts-heading" className={styles.settings}>
      <h2 id="facts-heading">{t("facts.title")}</h2>
      <dl className={styles.facts}>
        {facts.map(([label, value]) => (
          <Fragment key={label}>
            <dt>{label}</dt>
            <dd>{value}</dd>
          </Fragment>
        ))}
      </dl>
    </section>
  );
}

/**
 * The line in motion (H-072, replacing T-034's closed "See the line in 3D"):
 * the two runs' stages, Baseline above Scenario, under one playback bar,
 * playing once their traces are here (The Trace Motion Rule). A run without
 * a trace stands still in its results' colours (T-033) and says why. The
 * card takes the page's whole width, under the comparison and its side
 * panel (H-072 review): a long line is drawn at a scale where its boards
 * and its pile before a station can be seen, each station above its twin.
 */
function LineInMotion({ data }: { data: ResultsData }) {
  const { t } = useI18n();
  return (
    <section aria-labelledby="motion-heading" className={`${styles.section} ${styles.wide}`}>
      <h2 id="motion-heading">{t("playback.stage")}</h2>
      <Playback ir={data.version.ir} runs={{ baseline: data.baseline, scenario: data.scenario }} />
    </section>
  );
}

/** analysis: the AI explanation in the address and how to change it (T-030); without it the page offers none. */
export function ResultsView({ data, analysis }: { data: ResultsData; analysis?: { id: string | null; onChange: (id: string | null) => void } }) {
  const i18n = useI18n();
  const { t } = i18n;
  const { comparison, version, baseline, scenario } = data;
  const rows = keyMetrics(comparison, version.ir, { baseline: baseline.result, scenario: scenario.result });
  const lead = leadTimeSummary(comparison, rows);
  // DESIGN.md Words around it: the scenario value in green at 600.
  const leadText = (() => {
    if (lead === null) return null;
    const b = formatValue(i18n, lead.baseline, "minute");
    const s = formatValue(i18n, lead.scenario, "minute");
    const shown = { style: "percent", maximumFractionDigits: 1 } as const;
    const percent = i18n.number(Math.abs(lead.relative), shown);
    if (lead.relative === 0 || lead.baseline === lead.scenario) return t("compare.summary.same", { baseline: b });
    // H-073 review: a change that rounds to 0% at one decimal is said as under 0.1%, never as a directional 0%.
    const text = /[1-9]/.test(percent)
      ? t(lead.relative < 0 ? "compare.summary.shorter" : "compare.summary.longer", { baseline: b, scenario: "\u0000scenario\u0000", percent })
      : t("compare.summary.tiny", { baseline: b, scenario: "\u0000scenario\u0000", limit: i18n.number(0.001, shown) });
    return withNodes(text, {
      scenario: (
        <data value={String(lead.scenario)} className={styles.pairScenario}>
          {s}
        </data>
      ),
    });
  })();
  const { replications: br } = baseline.run.config;
  const caption = br === scenario.run.config.replications ? t("compare.summary.caption", { count: br }) : t("compare.summary.captionMixed");

  // H-040: the comparison on the left, the AI's explanation and the frozen settings in the side panel;
  // H-072: the line in motion under both, the page's whole width (out of their grid, so the sticky
  // side panel ends with the comparison and never covers it).
  return (
    <>
    <div className={styles.layout}>
      <div className={styles.main}>
      {!comparison.comparable && (
        <Callout tone="warning" heading={t("compare.incomparable.heading")}>
          <p>{t("compare.incomparable.body")}</p>
          <ul>
            {comparison.incomparable_reasons.map((r) => (
              <li key={r}>{t(`compare.incomparable.${r}`)}</li>
            ))}
          </ul>
        </Callout>
      )}
      {comparison.warnings.map((w) => (
        <Callout key={w} tone="info">
          {t(`compare.warning.${w}`)}
        </Callout>
      ))}
      <section className={styles.section}>
        <h2 id="metrics-heading">{t("compare.title")}</h2>
        {leadText && (
          <>
            <p className={`${styles.summary} ${styles.lead}`}>{leadText}</p>
            <p className={styles.summaryCaption}>{caption}</p>
          </>
        )}
        {/* On a phone the table keeps its figures whole and scrolls in its
            own region, reachable from the keyboard; the page does not scroll sideways. */}
        <div role="region" aria-labelledby="metrics-heading" tabIndex={0} className={styles.scroll}>
          <MetricsTable rows={rows} />
        </div>
      </section>
      <LineConstraint rows={rows} />
      <MareyChart
        ir={version.ir}
        baseline={{ trace: baseline.trace, endTick: baseline.result.measurement_window.horizon_ticks }}
        scenario={{ trace: scenario.trace, endTick: scenario.result.measurement_window.horizon_ticks }}
      />
      </div>
      <div className={styles.side}>
        {analysis && <SavedExplanation baseline={baseline.run} scenario={scenario.run} rows={rows} analysisId={analysis.id} onAnalysis={analysis.onChange} />}
        <Facts baseline={baseline.run} scenario={scenario.run} version={version.version} />
        <ResultProof baseline={baseline} scenario={scenario} />
      </div>
    </div>
    <LineInMotion data={data} />
    </>
  );
}
