"use client";
// The AI's explanation of the results (T-030; AI_SPEC §9, D-026). An editor
// asks for it (with a question, if they have one); the server queues it,
// the worker asks the provider and checks every claim against these runs'
// results, and only a grounded answer is stored. The page follows it by
// the analysis in the address, so a reload shows it again.
//
// What the AI wrote is shown as text; every number beside it is the
// server's comparison's (the deltas the server stored, never the AI's own),
// and each piece of evidence is named as the metrics table names it. Its
// experiments are suggestions only: nothing here runs or changes anything.
import { type FormEvent, useEffect, useRef, useState } from "react";
import { ProblemView } from "../../app/_components/problem-view";
import { newIdempotencyKey } from "../../lib/api/client";
import { errorCodeStatus, type Problem, problemFromResponse } from "../../lib/api/problem";
import { useApi } from "../../lib/api/provider";
import { canEdit, useAuth } from "../../lib/auth/session";
import type { AnalysisOutput, AnalysisRecord, ErrorCode, Run } from "../../lib/contracts";
import { useI18n } from "../../lib/i18n/provider";
import { Button } from "../../ui/button";
import { TextField } from "../../ui/field";
import { StatusRegion } from "../../ui/status";
import { formatAbsolute, formatRelative } from "./format";
import type { MetricRow } from "./metrics";
import { MetricName } from "./metrics-table";
import styles from "./results.module.css";

const POLL_MS = 2000;

type Props = {
  baseline: Run;
  scenario: Run;
  rows: MetricRow[];
  /** The analysis in the address, if any; onAnalysis puts one there (null takes it away). */
  analysisId: string | null;
  onAnalysis: (id: string | null) => void;
  pollMs?: number;
};

export function Analysis({ baseline, scenario, rows, analysisId, onAnalysis, pollMs = POLL_MS }: Props) {
  const { role } = useAuth();
  const { t } = useI18n();
  if (analysisId === null && !canEdit(role)) return null;
  return (
    <section aria-labelledby="analysis-heading" className={styles.section}>
      <h2 id="analysis-heading">{t("analysis.title")}</h2>
      {analysisId === null ? (
        <AskForm baseline={baseline} scenario={scenario} onAnalysis={onAnalysis} />
      ) : (
        <Followed key={analysisId} id={analysisId} runs={{ run: scenario.id, baseline: baseline.id }} rows={rows} pollMs={pollMs}
          onAgain={canEdit(role) ? () => onAnalysis(null) : null} />
      )}
    </section>
  );
}

function AskForm({ baseline, scenario, onAnalysis }: Pick<Props, "baseline" | "scenario" | "onAnalysis">) {
  const api = useApi();
  const { t, locale } = useI18n();
  const [question, setQuestion] = useState("");
  const [sending, setSending] = useState(false);
  const [problem, setProblem] = useState<Problem | null>(null);
  // The same request again reuses its key (D-011), so nothing is queued twice.
  const sent = useRef<{ request: string; key: string } | null>(null);
  const submit = async (e: FormEvent) => {
    e.preventDefault();
    if (sending) return;
    const body = { baseline_run_id: baseline.id, output_locale: locale, ...(question.trim() ? { question: question.trim() } : {}) };
    const request = JSON.stringify(body);
    if (sent.current?.request !== request) sent.current = { request, key: newIdempotencyKey() };
    setSending(true);
    setProblem(null);
    const r = await api.createAnalysis(scenario.id, body, sent.current.key);
    setSending(false);
    if (!r.ok) return setProblem(r.problem);
    onAnalysis(r.data.id);
  };
  return (
    <form onSubmit={submit} noValidate aria-labelledby="analysis-heading" className={styles.ask}>
      <p className={styles.note}>{t("analysis.intro")}</p>
      <TextField label={t("analysis.question")} description={t("analysis.questionHint")} value={question} onChange={setQuestion} multiline maxLength={10000} />
      {problem && <ProblemView problem={problem} headingLevel={3} />}
      <Button type="submit">{t("analysis.ask")}</Button>
      <StatusRegion message={sending ? t("analysis.asking") : null} />
    </form>
  );
}

/** The stored failure as the problem the API would have answered with; its own code is details[0].code. */
function failureProblem(r: AnalysisRecord): Problem | null {
  if (r.error === null) return null;
  const code = r.error.error.code as string;
  const status = Object.hasOwn(errorCodeStatus, code) ? errorCodeStatus[code as ErrorCode] : 500;
  return problemFromResponse(status, new Headers(), r.error);
}

function Followed({ id, runs, rows, pollMs, onAgain }: {
  id: string;
  /** This page's runs: an analysis of others (an old or edited address) is not shown here (T-030 review r1 M2). */
  runs: { run: string; baseline: string };
  rows: MetricRow[];
  pollMs: number;
  onAgain: (() => void) | null;
}) {
  const api = useApi();
  const { t } = useI18n();
  const [record, setRecord] = useState<AnalysisRecord | null>(null);
  const [problem, setProblem] = useState<Problem | null>(null);
  const [attempt, setAttempt] = useState(0);
  useEffect(() => {
    const ctl = new AbortController();
    let timer: ReturnType<typeof setTimeout> | undefined;
    const poll = async () => {
      const r = await api.getAnalysis(id, { signal: ctl.signal });
      if (ctl.signal.aborted) return;
      if (!r.ok) return setProblem(r.problem);
      setProblem(null);
      setRecord(r.data);
      const ours = r.data.run_id === runs.run && r.data.baseline_run_id === runs.baseline;
      if (ours && r.data.status === "generating") timer = setTimeout(() => void poll(), pollMs);
    };
    void poll();
    return () => {
      ctl.abort();
      clearTimeout(timer);
    };
  }, [api, id, runs.run, runs.baseline, pollMs, attempt]);

  if (problem) {
    return (
      <>
        <ProblemView problem={problem} headingLevel={3} />
        <Button onPress={() => setAttempt((n) => n + 1)}>{t("retry")}</Button>
      </>
    );
  }
  if (record !== null && (record.run_id !== runs.run || record.baseline_run_id !== runs.baseline)) {
    return (
      <>
        <p>{t("analysis.otherRuns")}</p>
        {onAgain && <Button onPress={onAgain}>{t("analysis.ask")}</Button>}
      </>
    );
  }
  const generating = record === null || record.status === "generating";
  return (
    <>
      <StatusRegion message={generating ? t("analysis.generating") : record.status === "completed" ? t("analysis.done") : t("analysis.notKept")} />
      {record?.status === "completed" && record.analysis && <Explained analysis={record.analysis} rows={rows} />}
      {record?.status === "failed" && <Failed record={record} />}
      {!generating && onAgain && <Button onPress={onAgain}>{t("analysis.again")}</Button>}
    </>
  );
}

function Failed({ record }: { record: AnalysisRecord }) {
  const { t } = useI18n();
  // An answer the server could not ground is the AI's, not a fault: said in words, with nothing of it shown.
  if (record.error?.error.details?.[0]?.code === "ANALYSIS_UNGROUNDED") return <p>{t("analysis.ungrounded")}</p>;
  const problem = failureProblem(record);
  return problem && <ProblemView problem={problem} headingLevel={3} />;
}

/** The metrics table's name of a path the AI cited: its own row, or the row of a statistic under it; else the path. */
function Cited({ path, rows }: { path: string; rows: MetricRow[] }) {
  const row = rows.find((r) => r.path === path) ?? rows.find((r) => r.path.startsWith(`${path}/`));
  return row ? <MetricName label={row.label} /> : <code>{path}</code>;
}

function Explained({ analysis, rows }: { analysis: AnalysisOutput; rows: MetricRow[] }) {
  const i18n = useI18n();
  const { t } = i18n;
  const byPath = new Map(rows.map((r) => [r.path, r]));
  const list = (heading: string, items: string[]) =>
    items.length > 0 && (
      <>
        <h3>{heading}</h3>
        <ul className={styles.explained}>
          {items.map((s, i) => (
            <li key={i}>{s}</li>
          ))}
        </ul>
      </>
    );
  return (
    <div className={styles.analysis}>
      <p className={styles.note}>{t("analysis.provenance")}</p>
      {/* The summaries and what changed say again what the table shows: one click away (H-039). */}
      <details className={styles.details}>
        <summary>{t("analysis.summaries")}</summary>
        <p className={styles.summary}>{analysis.scenario.summary}</p>
        {analysis.baseline && <p>{t("analysis.baseline", { summary: analysis.baseline.summary })}</p>}
        {analysis.delta.length > 0 && (
          <>
            <h3>{t("analysis.changed")}</h3>
            <ul className={styles.explained}>
              {analysis.delta.map((d) => {
                const row = byPath.get(d.metric_path);
                return (
                  <li key={d.metric_path}>
                    <strong>{row ? <MetricName label={row.label} /> : <code>{d.metric_path}</code>}</strong>
                    {d.absolute_delta !== null && (
                      <>
                        {" "}
                        <data value={d.absolute_delta} className={styles.delta}>
                          {formatAbsolute(i18n, d.absolute_delta, row?.unit ?? "number")}
                        </data>
                      </>
                    )}
                    {d.relative_delta !== null && (
                      <>
                        {" "}
                        (<data value={d.relative_delta}>{formatRelative(i18n, d.relative_delta)}</data>)
                      </>
                    )}
                    {d.reason && <span className={styles.reasonText}>{d.reason}</span>}
                  </li>
                );
              })}
            </ul>
          </>
        )}
      </details>
      {analysis.bottleneck.length > 0 && (
        <>
          <h3>{t("analysis.why")}</h3>
          <ul className={styles.explained}>
            {analysis.bottleneck.map((b, i) => (
              <li key={i}>
                <span className={styles.kind} data-kind={b.kind}>
                  {t(`analysis.kind.${b.kind}`)}
                </span>{" "}
                {b.statement}
                <span className={styles.evidence}>
                  {t("analysis.evidence")}{" "}
                  {b.evidence.map((e, j) => (
                    <span key={j} className={styles.cite}>
                      <Cited path={e.metric_path} rows={rows} /> · {t(`analysis.run.${e.run}`)}
                    </span>
                  ))}
                </span>
              </li>
            ))}
          </ul>
        </>
      )}
      {list(t("analysis.assumed"), analysis.assumptions)}
      {list(t("analysis.uncertain"), analysis.uncertainty)}
      {analysis.suggested_next_experiments.length > 0 && (
        <>
          <h3>{t("analysis.next")}</h3>
          <p className={styles.note}>{t("analysis.nextNote")}</p>
          <ul className={styles.explained}>
            {analysis.suggested_next_experiments.map((x, i) => (
              <li key={i}>
                <strong>{x.name}</strong>: {x.hypothesis}
              </li>
            ))}
          </ul>
        </>
      )}
    </div>
  );
}
