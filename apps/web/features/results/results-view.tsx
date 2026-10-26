"use client";
// The results of a scenario beside its baseline (T-032, H-029): whether
// the server could compare them (D-088), its warnings, the median lead time
// in one sentence, the key metrics, the Marey chart of the sampled traces
// and the settings both runs were frozen with (DESIGN.md The Provenance
// Always Rule). Presentational: everything comes from ResultsData.
import { Fragment } from "react";
import type { Duration, Run } from "../../lib/contracts";
import { useI18n } from "../../lib/i18n/provider";
import { Callout } from "../../ui/callout";
import { formatValue } from "./format";
import type { ResultsData } from "./load";
import { MareyChart } from "./marey-chart";
import { keyMetrics, leadTimeSummary } from "./metrics";
import { MetricsTable } from "./metrics-table";
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
    [t("facts.runs"), <><code>{baseline.id}</code>{" · "}<code>{scenario.id}</code></>],
    [t("facts.horizon"), both(span(bc.horizon), span(sc.horizon), sameSpan(bc.horizon, sc.horizon))],
    [t("facts.warmup"), both(span(bc.warmup), span(sc.warmup), sameSpan(bc.warmup, sc.warmup))],
    [t("facts.replications"), both(number(bc.replications), number(sc.replications), bc.replications === sc.replications)],
    [t("facts.seed"), both(String(bc.seed), String(sc.seed), bc.seed === sc.seed)],
    [t("facts.engine"), both(<code>{baseline.engine_version}</code>, <code>{scenario.engine_version}</code>, baseline.engine_version === scenario.engine_version)],
  ];
  return (
    <section aria-labelledby="facts-heading" className={styles.section}>
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

export function ResultsView({ data }: { data: ResultsData }) {
  const i18n = useI18n();
  const { t } = i18n;
  const { comparison, version, baseline, scenario } = data;
  const rows = keyMetrics(comparison, version.ir, { baseline: baseline.result, scenario: scenario.result });
  const lead = leadTimeSummary(comparison, rows);
  const leadText = (() => {
    if (lead === null) return null;
    const b = formatValue(i18n, lead.baseline, "minute");
    const s = formatValue(i18n, lead.scenario, "minute");
    const percent = i18n.number(Math.abs(lead.relative), { style: "percent", maximumFractionDigits: 1 });
    if (lead.relative === 0) return t("compare.summary.same", { baseline: b });
    return t(lead.relative < 0 ? "compare.summary.shorter" : "compare.summary.longer", { baseline: b, scenario: s, percent });
  })();

  return (
    <>
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
        {leadText && <p className={styles.summary}>{leadText}</p>}
        {/* On a phone the table keeps its figures whole and scrolls in its
            own region, reachable from the keyboard; the page does not scroll sideways. */}
        <div role="region" aria-labelledby="metrics-heading" tabIndex={0} className={styles.scroll}>
          <MetricsTable rows={rows} />
        </div>
      </section>
      <MareyChart
        ir={version.ir}
        baseline={{ trace: baseline.trace, endTick: baseline.result.measurement_window.horizon_ticks }}
        scenario={{ trace: scenario.trace, endTick: scenario.result.measurement_window.horizon_ticks }}
      />
      <Facts baseline={baseline.run} scenario={scenario.run} version={version.version} />
    </>
  );
}
