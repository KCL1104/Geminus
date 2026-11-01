"use client";
// H-073 (4): the run pair's reproducibility. Each run's fingerprints are its
// Result's (reproducibility: the pinned and the effective model, the
// settings, the datasets' manifest, the result digest), in Geist Mono,
// shortened to eight hex digits for the eye; the whole value is in the page
// for a screen reader and for copying, and shows on hover or focus (Escape
// hides it again: WCAG 1.4.13). Under them, for each run, how many of the
// model's other completed runs with the same inputs gave the same result
// digest, read from the server (reruns.ts) with capped reads that say what
// they left out. Nothing here is claimed beyond what was read.
//
// Mount (not yet: results-view.tsx belongs to another slice): in
// ResultsView's side panel, right after <Facts />, as
// <ResultProof baseline={baseline} scenario={scenario} /> with the page's
// RunData (each has run and result).
import { type ReactNode, useId, useState } from "react";
import { ProblemView } from "../../app/_components/problem-view";
import { useRemote } from "../../app/_components/use-remote";
import { useApi } from "../../lib/api/provider";
import type { Result as SimulationResult } from "../../lib/contracts";
import { useI18n } from "../../lib/i18n/provider";
import { Button } from "../../ui/button";
import { LineKey } from "../simulations/run-status";
import { checkReruns, RERUN_LIMITS, type Checked, type RerunLimits, type Reruns } from "./reruns";
import styles from "./proof.module.css";

const FIELDS = ["model_hash", "effective_model_hash", "config_hash", "dataset_manifest_hash", "result_digest"] as const satisfies readonly (keyof SimulationResult["reproducibility"])[];

/** A fingerprint: eight hex digits on screen, the whole value in the page and on hover or focus. */
function Hash({ value }: { value: string }) {
  const [dismissed, setDismissed] = useState(false);
  const hex = value.startsWith("sha256:") ? value.slice("sha256:".length) : value;
  return (
    <span
      className={styles.hash}
      tabIndex={0}
      data-dismissed={dismissed ? "" : undefined}
      onKeyDown={(e) => {
        if (e.key !== "Escape" || dismissed) return;
        e.stopPropagation();
        setDismissed(true);
      }}
      onBlur={() => setDismissed(false)}
      onMouseLeave={() => setDismissed(false)}
    >
      <span aria-hidden="true">{hex.slice(0, 8)}…</span>
      <code className={styles.full}>{value}</code>
    </span>
  );
}

function RerunLine({ tally }: { tally: Reruns }) {
  const { t, gap } = useI18n();
  const parts: ReactNode[] = [];
  if (tally.same > 0) parts.push(t("proof.reruns.same", { count: tally.same }));
  if (tally.different > 0)
    parts.push(
      <span key="differ" data-tone="warning" className={styles.warning}>
        {t("proof.reruns.differ", { count: tally.different })}
      </span>,
    );
  if (tally.same === 0 && tally.different === 0 && tally.unread === 0) parts.push(t("proof.reruns.none"));
  if (tally.unread > 0) parts.push(<span key="unread" className={styles.unread}>{t("proof.reruns.unread", { count: tally.unread })}</span>);
  return (
    <>
      {parts.map((p, i) => (
        <span key={i}>
          {i > 0 && gap}
          {p}
        </span>
      ))}
    </>
  );
}

export function ResultProof({ baseline, scenario, limits = RERUN_LIMITS }: { baseline: Checked; scenario: Checked; limits?: RerunLimits }) {
  const api = useApi();
  const { t } = useI18n();
  const heading = useId();
  const [reruns, retry] = useRemote(
    (signal) => checkReruns(api, [baseline, scenario], limits, signal),
    api,
    `${baseline.run.id}:${scenario.run.id}:${limits.pages}:${limits.reads}`,
  );
  const sides = [
    ["baseline", baseline],
    ["scenario", scenario],
  ] as const;
  return (
    <section aria-labelledby={heading} className={styles.card}>
      <h2 id={heading}>{t("proof.title")}</h2>
      <p className={styles.intro}>{t("proof.intro")}</p>
      <table role="table" className={styles.table}>
        <caption>{t("proof.caption")}</caption>
        <thead role="rowgroup">
          <tr role="row">
            <th scope="col" role="columnheader">
              {t("proof.fingerprint")}
            </th>
            {sides.map(([side]) => (
              <th key={side} scope="col" role="columnheader">
                <LineKey side={side} />
                {t(`compare.${side}`)}
              </th>
            ))}
          </tr>
        </thead>
        <tbody role="rowgroup">
          {FIELDS.map((f) => (
            <tr key={f} role="row">
              <th scope="row" role="rowheader">
                {t(`proof.${f}`)}
              </th>
              {sides.map(([side, c]) => (
                <td key={side} role="cell">
                  <Hash value={c.result.reproducibility[f]} />
                </td>
              ))}
            </tr>
          ))}
        </tbody>
      </table>
      <h3>{t("proof.reruns.title")}</h3>
      {reruns.state === "loading" && (
        <p data-reruns="checking" className={styles.note}>
          {t("proof.reruns.checking")}
        </p>
      )}
      {reruns.state === "failed" && (
        <div data-reruns="failed" className={styles.failed}>
          <p>{t("proof.reruns.failed")}</p>
          <ProblemView problem={reruns.problem} headingLevel={3} />
          <Button onPress={retry}>{t("retry")}</Button>
        </div>
      )}
      {reruns.state === "loaded" && (
        <div data-reruns="done">
          <ul className={styles.reruns}>
            {sides.map(([side], i) => (
              <li key={side} data-reruns-for={side}>
                <span className={styles.side}>
                  <LineKey side={side} />
                  {t(`compare.${side}`)}
                </span>
                <span>
                  <RerunLine tally={reruns.data.runs[i]!} />
                </span>
              </li>
            ))}
          </ul>
          {reruns.data.listCapped && <p className={styles.note}>{t("proof.reruns.listCapped", { count: reruns.data.listed })}</p>}
        </div>
      )}
    </section>
  );
}
