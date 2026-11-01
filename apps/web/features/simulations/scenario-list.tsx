"use client";
// The model's scenarios (T-032), every version's, newest version first
// (T-057 review r1 M5: a publish must not hide the scenarios of the version
// before it), ranked by what the server compared (H-073, "Which what-if
// wins?", ranking.ts): each one's latest completed run against its baseline
// on the median lead time, best first, as the results' Compare Table writes
// its figures. Every figure is the server's, with the exact value in
// <data value>; where there is none the row says why. The server's warnings
// on a comparison are written under its name, beside its figures, and one
// that every compared row carries once above the table. Each name opens the
// results its figures come from (or, before any run, the scenario's page).
// Nothing shows while the model has none.
import Link from "next/link";
import { type ReactNode, useId } from "react";
import { Callout } from "../../ui/callout";
import { MorePages, RemoteView } from "../../app/_components/states";
import { useRemote } from "../../app/_components/use-remote";
import { useApi } from "../../lib/api/provider";
import type { ComparisonWarning } from "../../lib/contracts";
import { useI18n } from "../../lib/i18n/provider";
import { AiMark } from "../investigations/ai-mark";
import { formatAbsolute, formatRelative, formatValue } from "../results/format";
import { MAX_RUN_PAGES, resultsHref } from "./load";
import { loadRanking, MAX_COMPARISONS, type RankedScenario, warningsOf } from "./ranking";
import { LineKey } from "./run-status";
import styles from "./simulations.module.css";
import ranking from "./ranking.module.css";

/** latestVersionId: the model's latest version, so a publish reads the list again; empty: what shows when there is none (H-042: the Scenarios page says so). */
export function ScenarioList({ modelId, latestVersionId, empty = null }: { modelId: string; latestVersionId: string; empty?: ReactNode }) {
  const api = useApi();
  const { t } = useI18n();
  const heading = useId();
  const [remote, retry] = useRemote((signal) => loadRanking(api, modelId, signal), api, `${modelId} ${latestVersionId}`);
  return (
    <RemoteView remote={remote} what="scenarios" onRetry={retry} headingLevel={2}>
      {({ rows, shared, more, runsCut, unread }) =>
        rows.length === 0 ? (
          empty
        ) : (
          <section aria-labelledby={heading} className={styles.section}>
            <h2 id={heading}>{t("scenarios.ranking.title")}</h2>
            {/* A warning every comparison below carries, said before the order it qualifies (as the results say theirs), and on no row. */}
            {shared.map((w) => (
              <Callout key={w} tone="info">
                {t("scenarios.ranking.shared", { warning: t(`compare.warning.${w}`) })}
              </Callout>
            ))}
            {/* On a phone each row is a grid (ranking.module.css), so the table states its roles: a table whose display changes may otherwise lose them. */}
            <div className={ranking.scroll}>
              <table role="table" className={ranking.table}>
                <caption>{t("scenarios.ranking.caption")}</caption>
                <thead role="rowgroup">
                  <tr role="row">
                    <th scope="col" role="columnheader" className={ranking.rank}>
                      {t("scenarios.ranking.rank")}
                    </th>
                    <th scope="col" role="columnheader">
                      {t("scenarios.ranking.whatIf")}
                    </th>
                    <th scope="col" role="columnheader" className={ranking.num}>
                      <LineKey side="baseline" />
                      {t("compare.baseline")}
                    </th>
                    <th scope="col" role="columnheader" className={`${ranking.num} ${ranking.scenarioCol}`}>
                      <LineKey side="scenario" />
                      {t("compare.scenario")}
                    </th>
                    <th scope="col" role="columnheader" className={ranking.num}>
                      {t("compare.absolute")}
                    </th>
                    <th scope="col" role="columnheader" className={ranking.num}>
                      {t("compare.relative")}
                    </th>
                  </tr>
                </thead>
                <tbody role="rowgroup">
                  {rows.map((row) => (
                    <Row key={row.scenario.id} row={row} shared={shared} />
                  ))}
                </tbody>
              </table>
            </div>
            <MorePages cursor={more ? "more" : null} shown={rows.length} what="scenarios" />
            {runsCut && <Callout tone="info">{t("scenario.earlier.cut", { count: MAX_RUN_PAGES * 100 })}</Callout>}
            {unread > 0 && <Callout tone="info">{t("scenarios.ranking.capped", { count: MAX_COMPARISONS })}</Callout>}
          </section>
        )
      }
    </RemoteView>
  );
}

/** The figures of a compared row: each run's median, then Δ and Δ %, or the server's reason where it has none. */
function Figures({ standing }: { standing: Extract<RankedScenario["standing"], { kind: "ranked" | "compared" }> }) {
  const i18n = useI18n();
  const { t } = i18n;
  const m = standing.metric;
  if (m === null) return <Why words={t("compare.reason.missing")} span={4} />;
  const reason = m.reason ? t(`compare.reason.${m.reason}`) : t("compare.unavailable");
  const median = (v: number | null) =>
    v === null ? <span className={ranking.why}>{t("compare.unavailable")}</span> : <data value={String(v)} className={ranking.figure}>{formatValue(i18n, v, "minute")}</data>;
  return (
    <>
      <td role="cell" className={ranking.num}>
        {median(m.baseline)}
      </td>
      <td role="cell" className={`${ranking.num} ${ranking.scenarioCol}`}>
        {median(m.scenario)}
      </td>
      {m.absolute_delta === null ? (
        <Why words={reason} span={2} />
      ) : (
        <>
          <td role="cell" className={ranking.num}>
            <data value={String(m.absolute_delta)} className={ranking.delta}>
              {formatAbsolute(i18n, m.absolute_delta, "minute")}
            </data>
          </td>
          {m.relative_delta === null ? (
            <Why words={reason} span={1} />
          ) : (
            <td role="cell" className={ranking.num}>
              <data value={String(m.relative_delta)} className={ranking.delta}>
                {formatRelative(i18n, m.relative_delta)}
              </data>
            </td>
          )}
        </>
      )}
    </>
  );
}

function Why({ words, span }: { words: string; span: number }) {
  return (
    <td role="cell" colSpan={span} className={ranking.why}>
      {words}
    </td>
  );
}

/** shared: the warnings said once above the table, so not on the row. */
function Row({ row, shared }: { row: RankedScenario; shared: readonly ComparisonWarning[] }) {
  const { t } = useI18n();
  const { scenario, version, standing } = row;
  const warnings = warningsOf(standing).filter((w) => !shared.includes(w));
  const pair = standing.kind === "none" ? null : standing.pair;
  const href = pair === null ? `/scenarios/${scenario.id}` : resultsHref(scenario.id, pair.baseline.id, pair.scenario.id);
  return (
    <tr role="row" data-standing={standing.kind}>
      <td role="cell" className={ranking.rank} data-rank={standing.kind === "ranked" ? String(standing.rank) : undefined}>
        {standing.kind === "ranked" ? standing.rank : null}
      </td>
      <th scope="row" role="rowheader">
        <p className={ranking.name}>
          <Link href={href}>
            <bdi>{scenario.name}</bdi>
          </Link>
        </p>
        <p className={ranking.marks}>
          <span className={styles.meta}>{t("model.scenarios.version", { version: String(version) })}</span>
          {/* H-061 A8: a scenario an AI investigation saved for the person who asked. */}
          {scenario.made_by_ai && <AiMark kind="saved" />}
        </p>
        {/* The server's qualifiers on these figures (a single replication: the difference cannot be told from chance); they do not move the row. */}
        {warnings.length > 0 && (
          <ul className={ranking.warnings}>
            {warnings.map((w) => (
              <li key={w}>{t(`compare.warning.${w}`)}</li>
            ))}
          </ul>
        )}
      </th>
      {standing.kind === "ranked" || standing.kind === "compared" ? (
        <Figures standing={standing} />
      ) : (
        <Why words={t(`scenarios.ranking.${standing.kind === "none" ? "notRun" : standing.kind}`)} span={4} />
      )}
    </tr>
  );
}
