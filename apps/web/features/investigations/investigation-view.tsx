"use client";
// The AI investigation page (H-061 A8, PROPOSED; H-061 §3 steps 4, 6 and 10,
// §13). It shows what the server holds and drives nothing: the AI picks each
// step in the worker and the API carries it out, so the page only reads
// getInvestigation again every 2 s while it runs, and stops reading once it
// has ended. The header names the planner ("Done by Muse Spark 1.3 (Meta) for
// you"), the version, the aim and the budget, and the clock (H-063: it keeps
// going after the page closes, until the deadline). The steps are the
// ledger's rows in words: what the AI asked for, whether Geminus carried it
// out, refused it or failed, what it waits for, and a step still asked when
// it ended is "Not carried out". The AI's words (its idea, question and
// note) are data: labelled as the AI's, shown as text in a <bdi>, with what
// would hide or reorder them made visible (shown.ts). The candidates carry
// only the server's figures; the outcome gives the reason in words, the AI's
// claim labelled as its claim, and the server's closing line. Stop is for the
// requester or an owner, asked first; what the AI made stays.
import Link from "next/link";
import { useCallback, useEffect, useId, useRef, useState } from "react";
import { ProblemView } from "../../app/_components/problem-view";
import { RailModel } from "../../app/_components/rail";
import { Loading, RemoteView } from "../../app/_components/states";
import { useRemote } from "../../app/_components/use-remote";
import { type ApiClient, newIdempotencyKey, type Result } from "../../lib/api/client";
import { type Problem, protocolProblem } from "../../lib/api/problem";
import { useApi } from "../../lib/api/provider";
import { useAuth } from "../../lib/auth/session";
import type { Investigation, InvestigationCandidate, InvestigationObjectiveMetric, InvestigationStep, Model, ModelVersion } from "../../lib/contracts";
import { type I18n, useI18n } from "../../lib/i18n/provider";
import type { MessageKey } from "../../lib/i18n/translate";
import { formatTime } from "../../lib/i18n/translate";
import { Button } from "../../ui/button";
import { Callout } from "../../ui/callout";
import { ConfirmDialog } from "../../ui/dialog";
import { CloseIcon } from "../../ui/icons";
import list from "../../ui/list.module.css";
import { PageHeader } from "../../ui/page-header";
import { StatusRegion } from "../../ui/status";
import { useWriter } from "../ai-proposals/generated-by";
import { visible } from "../ai-proposals/shown";
import { data, useRich, Words } from "../ai-proposals/words";
import { formatAbsolute, formatRelative, formatValue, type MetricUnit } from "../results/format";
import { RunPairStatus } from "../simulations/run-status";
import { useRunPair } from "../simulations/use-run-pair";
import { AiMark } from "./ai-mark";
import styles from "./investigation.module.css";

const POLL_MS = 2000;

/** Where the page's links go: the product's routes, or the review page's own. */
export type InvestigationHrefs = {
  proposal: (proposalId: string) => string;
  scenario: (scenarioId: string) => string;
  results: (scenarioId: string, baselineRunId: string, runId: string, analysisId?: string) => string;
};
const productHrefs: InvestigationHrefs = {
  proposal: (id) => `/proposals/${id}`,
  scenario: (id) => `/scenarios/${id}`,
  results: (s, b, r, a) => `/scenarios/${s}/results?baseline=${b}&scenario=${r}${a ? `&analysis=${a}` : ""}`,
};

export type InvestigationPage = { investigation: Investigation; model: Model; base: ModelVersion };

const refuse = (message: string, requestId: string | null): Result<never> => ({ ok: false, problem: protocolProblem(200, message, requestId) });

/** The investigation, its model (the name) and its base version (the number); only server answers, the first failure wins. */
export async function loadInvestigationPage(api: ApiClient, investigationId: string, signal?: AbortSignal): Promise<Result<InvestigationPage>> {
  const r = await api.getInvestigation(investigationId, { signal });
  if (!r.ok) return r;
  const inv = r.data;
  if (inv.id !== investigationId) return refuse("The API answered with another investigation.", r.requestId);
  const [model, base] = await Promise.all([api.getModel(inv.model_id, { signal }), api.getModelVersion(inv.base_version_id, { signal })]);
  if (!model.ok) return model;
  if (!base.ok) return base;
  if (model.data.id !== inv.model_id || base.data.id !== inv.base_version_id || base.data.model_id !== inv.model_id) return refuse("The model or version is not the investigation's.", base.requestId);
  return { ok: true, data: { investigation: inv, model: model.data, base: base.data }, requestId: r.requestId, etag: null };
}

/** Loads the investigation page and shows it; `now` is the reader's clock (a test gives its own). */
export function InvestigationRoute({ investigationId, pollMs = POLL_MS, now = Date.now, hrefs = productHrefs }: { investigationId: string; pollMs?: number; now?: () => number; hrefs?: InvestigationHrefs }) {
  const api = useApi();
  const [remote, retry] = useRemote((signal) => loadInvestigationPage(api, investigationId, signal), api, investigationId);
  return (
    <RemoteView remote={remote} what="investigation" onRetry={retry}>
      {(page) => <InvestigationView key={page.investigation.id} initial={page} pollMs={pollMs} now={now} hrefs={hrefs} />}
    </RemoteView>
  );
}

/** A tool whose carried-out call waits for something the worker reads: a proposal, the runs or an explanation. */
const waits = (tool: InvestigationStep["tool"]): tool is "propose_what_if" | "propose_model_edit" | "run_what_if" | "explain_results" =>
  tool === "propose_what_if" || tool === "propose_model_edit" || tool === "run_what_if" || tool === "explain_results";

type StepState = "pending" | "not_carried_out" | "waiting" | "done" | "refused" | "failed";

function stepState(step: InvestigationStep, running: boolean): StepState {
  if (step.status === "pending") return running ? "pending" : "not_carried_out";
  if (step.status === "done" && step.observed_at === null && running && waits(step.tool)) return "waiting";
  return step.status;
}

/** A metric's name as the results table writes it, for the aims (H-061: the four objective metrics). */
const metricKey: Readonly<Record<InvestigationObjectiveMetric, MessageKey>> = {
  "/metrics/lead_time_minutes/p50": "metric.leadTimeMedian",
  "/metrics/lead_time_minutes/mean": "metric.leadTimeMean",
  "/metrics/lead_time_minutes/p95": "metric.leadTimeP95",
  "/metrics/throughput/mean": "metric.throughput",
};
const unitOf = (path: string): MetricUnit => (path.startsWith("/metrics/lead_time_minutes/") ? "minute" : "number");
const metricName = (t: I18n["t"], path: string): string => (Object.hasOwn(metricKey, path) ? t(metricKey[path as InvestigationObjectiveMetric]) : path);

function InvestigationView({ initial, pollMs, now, hrefs }: { initial: InvestigationPage; pollMs: number; now: () => number; hrefs: InvestigationHrefs }) {
  const api = useApi();
  const { role, userId } = useAuth();
  const i18n = useI18n();
  const { t, locale } = i18n;
  const rich = useRich();
  const [inv, setInv] = useState(initial.investigation);
  const [pollProblem, setPollProblem] = useState<Problem | null>(null);
  const [pollAttempt, setPollAttempt] = useState(0);
  const [asking, setAsking] = useState(false);
  const [stopping, setStopping] = useState(false);
  const [stopProblem, setStopProblem] = useState<Problem | null>(null);
  const [announce, setAnnounce] = useState<string | null>(null);
  const stopKey = useRef<string | null>(null);
  const busy = useRef(false);
  const outcomeRef = useRef<HTMLElement>(null);
  // Focus moves to the outcome once, and only when it ended while the page was open.
  const sawRunning = useRef(initial.investigation.status === "running");
  const ids = { steps: useId(), candidates: useId(), outcome: useId() };
  const writer = useWriter({ provider: inv.planner.provider, model: inv.planner.model });
  const running = inv.status === "running";
  const id = inv.id;

  // While it runs, read it again; stop on an answer that has ended, or on a failure (until retried).
  useEffect(() => {
    if (!running) return;
    const ctl = new AbortController();
    const timer = setTimeout(async () => {
      const r = await api.getInvestigation(id, { signal: ctl.signal });
      if (ctl.signal.aborted) return;
      if (!r.ok) return setPollProblem(r.problem);
      if (r.data.id !== id || r.data.model_id !== inv.model_id || r.data.organization_id !== inv.organization_id) {
        return setPollProblem(protocolProblem(200, "The API answered with another investigation.", r.requestId));
      }
      setInv(r.data);
    }, pollMs);
    return () => {
      ctl.abort();
      clearTimeout(timer);
    };
  }, [api, id, inv, running, pollMs, pollAttempt]);

  useEffect(() => {
    if (running || !sawRunning.current) return;
    sawRunning.current = false;
    setAnnounce(t("investigation.ended"));
    outcomeRef.current?.focus();
  }, [running, t]);

  const stop = useCallback(async () => {
    if (busy.current) return;
    busy.current = true;
    setStopping(true);
    setStopProblem(null);
    // One key for this investigation's stop: a retry is the same request (D-011).
    const key = (stopKey.current ??= newIdempotencyKey());
    const r = await api.stopInvestigation(id, key);
    busy.current = false;
    setStopping(false);
    if (r.ok) {
      if (r.data.id === id) setInv(r.data);
      return;
    }
    // It ended before the stop arrived: read it again and show how it ended (no alarm).
    if (r.problem.code === "LIFECYCLE_CONFLICT") {
      const again = await api.getInvestigation(id);
      if (again.ok && again.data.id === id) return setInv(again.data);
    }
    setStopProblem(r.problem);
  }, [api, id]);

  const mine = userId != null && userId === inv.created_by;
  const canStop = running && (role === "owner" || (role === "editor" && mine));
  const title = rich("investigation.title", { model: data(initial.model.name) });
  const activePair = running ? lastPair(inv) : null;
  const left = Math.max(0, Math.min(inv.caps.minutes, Math.ceil((Date.parse(inv.deadline_at) - now()) / 60_000)));
  const used = (key: "what_ifs" | "runs" | "steps" | "changes", n: number, cap: number) => t(`investigation.used.${key}`, { used: String(n), cap: String(cap) });
  const budget = [
    ...(inv.mode === "scenario"
      ? [used("what_ifs", inv.used.what_ifs, inv.caps.what_ifs), used("runs", inv.used.runs, inv.caps.runs)]
      : [used("changes", inv.used.what_ifs, inv.caps.what_ifs)]),
    used("steps", inv.used.steps, inv.caps.steps),
    ...(running ? [t("investigation.left", { count: left })] : []),
  ].join(" · ");
  const status = running ? `${t("investigation.status.running")} · ${t(`investigation.activity.${inv.activity.state}`)}` : t(`investigation.status.${inv.status}`);

  return (
    <>
      <RailModel id={inv.model_id} />
      <PageHeader
        trail={[]}
        title={<Words parts={title} />}
        facts={[
          { label: t("band.status"), value: <span className={styles.state} data-investigation-status={inv.status}>{status}</span> },
          ...(writer === null ? [] : [{ label: t("investigation.doneBy"), value: t(mine ? "investigation.for.you" : "investigation.for.other", { model: writer }) }]),
          ...(inv.proposal_model !== inv.planner.model ? [{ label: t("investigation.proposalsBy"), value: t(`ai.model.${inv.proposal_model}`) }] : []),
          { label: t("investigation.version"), value: <data value={initial.base.version}>{initial.base.version}</data> },
          ...(inv.objective === null ? [] : [{ label: t("investigation.objective"), value: t(`investigation.objective.${inv.objective.metric_path}`) }]),
        ]}
        actions={
          canStop && (
            <Button icon={<CloseIcon />} onPress={() => setAsking(true)}>
              {t("investigation.stop")}
            </Button>
          )
        }
      />
      <p className={styles.budget} data-budget>
        {budget}
      </p>
      {running && (
        <p className={styles.meta} data-clock>
          {t("investigation.clock", { time: clockTime(locale, inv.deadline_at) })}
        </p>
      )}
      {running && !canStop && <Callout tone="info">{t("investigation.watchOnly")}</Callout>}
      {stopProblem && <ProblemView problem={stopProblem} />}
      {pollProblem && (
        <>
          <ProblemView problem={pollProblem} />
          <Button
            onPress={() => {
              setPollProblem(null);
              setPollAttempt((n) => n + 1);
            }}
          >
            {t("retry")}
          </Button>
        </>
      )}

      {!running && (
        <section ref={outcomeRef} tabIndex={-1} className={styles.section} data-outcome data-reason={inv.reason ?? undefined} aria-labelledby={ids.outcome}>
          <h2 id={ids.outcome}>{t("investigation.outcomeTitle")}</h2>
          <Outcome inv={inv} />
        </section>
      )}

      <section className={styles.section} aria-labelledby={ids.steps}>
        <h2 id={ids.steps}>{t("investigation.steps")}</h2>
        {inv.steps.length === 0 ? (
          <p className={styles.meta}>{t("investigation.steps.none")}</p>
        ) : (
          <ol className={styles.steps} data-steps>
            {inv.steps.map((s) => (
              <StepItem key={s.seq} step={s} inv={inv} hrefs={hrefs} />
            ))}
          </ol>
        )}
      </section>

      {activePair && <ActivePair baselineId={activePair.baseline} runId={activePair.run} />}

      {inv.mode === "scenario" && inv.candidates.length > 0 && (
        <section className={styles.section} aria-labelledby={ids.candidates}>
          <h2 id={ids.candidates}>{t("investigation.candidates")}</h2>
          <Candidates inv={inv} hrefs={hrefs} i18n={i18n} />
        </section>
      )}

      <ConfirmDialog
        isOpen={asking}
        onOpenChange={setAsking}
        heading={t("investigation.stop.heading")}
        confirmLabel={t("investigation.stop.confirm")}
        cancelLabel={t("investigation.stop.cancel")}
        onConfirm={() => void stop()}
      >
        <p>{t("investigation.stop.body")}</p>
      </ConfirmDialog>
      <StatusRegion message={stopping ? t("investigation.stopping") : announce} />
    </>
  );
}

/** The deadline as a time of day; the API's text as it came when it is not a date-time (never a broken page). */
function clockTime(locale: I18n["locale"], iso: string): string {
  try {
    return formatTime(locale, iso);
  } catch {
    return iso;
  }
}

/** The candidate table links this run's results (scenario mode, a candidate with the run, its scenario and the baseline). */
function resultsInTable(inv: Investigation, runId: string): boolean {
  return inv.mode === "scenario" && inv.baseline_run_id !== null && inv.candidates.some((c) => c.run_id === runId && c.scenario_id !== null);
}

/** A step links this proposal already (the propose step that made it), so the table names its state without a second link. */
const proposalLinked = (inv: Investigation, proposalId: string) => inv.steps.some((s) => s.effect?.proposal_id === proposalId);

/** The runs a run_what_if step started and the AI now waits for, or null. */
function lastPair(inv: Investigation): { baseline: string; run: string } | null {
  const last = inv.steps.at(-1);
  if (last === undefined || last.tool !== "run_what_if" || last.status !== "done" || last.observed_at !== null) return null;
  const b = last.effect?.baseline_run_id;
  const r = last.effect?.run_id;
  return b && r ? { baseline: b, run: r } : null;
}

/** The two runs the AI waits for, as they go; no cancel here (Stop the AI is the one way to end what it does). */
function ActivePair({ baselineId, runId }: { baselineId: string; runId: string }) {
  const api = useApi();
  const pair = useRunPair(api, baselineId, runId);
  return (
    <div data-active-pair>
      {pair.problem && <ProblemView problem={pair.problem} />}
      {pair.baseline && pair.scenario ? (
        <RunPairStatus baseline={pair.baseline} scenario={pair.scenario} canCancel={false} cancelling={false} cancelProblem={null} onCancel={() => {}} />
      ) : (
        !pair.problem && <Loading what="runs" />
      )}
    </div>
  );
}

/** One of the AI's words, labelled as its own and isolated (H-061 §3 step 6: visible() and <bdi>). */
function AiText({ labelKey, text }: { labelKey: MessageKey; text: string }) {
  const { t, gap } = useI18n();
  return (
    <p>
      {t(labelKey)}
      {gap}
      <bdi className={styles.words} data-ai-text>
        {visible(text, "prose")}
      </bdi>
    </p>
  );
}

function StepItem({ step, inv, hrefs }: { step: InvestigationStep; inv: Investigation; hrefs: InvestigationHrefs }) {
  const { t, gap } = useI18n();
  const state = stepState(step, inv.status === "running");
  const args = step.arguments as Record<string, unknown> | null;
  const idea = typeof args?.idea === "string" ? args.idea : null;
  const question = typeof args?.question === "string" ? args.question : null;
  const suggestion = args?.from_suggestion as { index: number } | null | undefined;
  const e = step.effect ?? {};
  // The scenario a run or an explanation belongs to: the run step's argument, or the candidate whose run it explains.
  const scenarioOfRun = (runId: string | undefined) => (runId === undefined ? undefined : inv.candidates.find((c) => c.run_id === runId)?.scenario_id ?? undefined);
  const runScenario = typeof args?.scenario_id === "string" ? args.scenario_id : scenarioOfRun(e.run_id);
  const explained = typeof args?.run_id === "string" ? args.run_id : undefined;
  const words =
    state === "pending" ? t("investigation.stepStatus.pending")
    : state === "not_carried_out" ? t("investigation.stepStatus.notCarriedOut")
    : state === "waiting" && waits(step.tool) ? t(`investigation.waiting.${step.tool}`)
    : state === "done" ? t("investigation.stepStatus.done")
    : state === "refused" ? t("investigation.stepStatus.refused")
    : t("investigation.stepStatus.failed");
  return (
    <li data-step={step.seq} data-status={state}>
      <h3>{t("investigation.step", { n: String(step.seq), tool: t(`investigation.tool.${step.tool}`) })}</h3>
      <p>
        <span className={list.pill} data-tone={state === "refused" || state === "failed" ? "error" : state === "done" ? "ok" : state === "not_carried_out" ? "neutral" : "busy"}>
          {words}
        </span>
        {step.error && (
          <>
            {gap}
            {t("investigation.why")}
            {gap}
            {t(`investigation.code.${step.error.code}`)}
          </>
        )}
      </p>
      {idea !== null && <AiText labelKey="investigation.aiIdea" text={idea} />}
      {suggestion != null && <p className={styles.meta}>{t("investigation.fromSuggestion", { index: String(suggestion.index + 1) })}</p>}
      {question !== null && <AiText labelKey="investigation.aiQuestion" text={question} />}
      {step.note !== null && step.note !== "" && <AiText labelKey="investigation.aiNote" text={step.note} />}
      {/* One link per place on the page (H-027): a run's results are the candidate table's to link when it lists the
          run; the draft is the rail's Canvas. */}
      <p className={styles.links}>
        {e.proposal_id && <Link href={hrefs.proposal(e.proposal_id)}>{t("investigation.open.proposal")}</Link>}
        {e.scenario_id && <Link href={hrefs.scenario(e.scenario_id)}>{t("investigation.open.scenario")}</Link>}
        {e.baseline_run_id && e.run_id && runScenario && !resultsInTable(inv, e.run_id) && <Link href={hrefs.results(runScenario, e.baseline_run_id, e.run_id)}>{t("investigation.open.results")}</Link>}
        {e.analysis_id && explained && inv.baseline_run_id && scenarioOfRun(explained) && (
          <Link href={hrefs.results(scenarioOfRun(explained)!, inv.baseline_run_id, explained, e.analysis_id)}>{t("investigation.open.explanation")}</Link>
        )}
      </p>
    </li>
  );
}

function Candidates({ inv, hrefs, i18n }: { inv: Investigation; hrefs: InvestigationHrefs; i18n: I18n }) {
  const { t, gap } = i18n;
  const metric = inv.objective?.metric_path ?? "/metrics/lead_time_minutes/p50";
  return (
    <div className={styles.scroll}>
      <table className={styles.table} data-candidates>
        <caption>
          {t("investigation.candidates.caption")}
          {gap}
          {t("investigation.candidates.metric", { metric: metricName(t, metric) })}
        </caption>
        <thead>
          <tr>
            <th scope="col">{t("investigation.col.whatIf")}</th>
            <th scope="col">{t("investigation.col.proposal")}</th>
            <th scope="col">{t("investigation.col.baseline")}</th>
            <th scope="col">{t("investigation.col.scenario")}</th>
            <th scope="col">{t("investigation.col.change")}</th>
          </tr>
        </thead>
        <tbody>
          {inv.candidates.map((c) => (
            <CandidateRow key={c.index} c={c} inv={inv} hrefs={hrefs} i18n={i18n} />
          ))}
        </tbody>
      </table>
    </div>
  );
}

function CandidateRow({ c, inv, hrefs, i18n }: { c: InvestigationCandidate; inv: Investigation; hrefs: InvestigationHrefs; i18n: I18n }) {
  const { t } = i18n;
  const best = inv.best?.index === c.index;
  const cmp = c.comparison;
  const o = cmp?.comparable ? cmp.objective : null;
  const unit = unitOf(o?.metric_path ?? "");
  const figure = (v: number | null, format: (n: number) => string) => (v === null ? t("compare.unavailable") : <data value={v}>{format(v)}</data>);
  return (
    <tr data-candidate={c.index} data-best={best ? "" : undefined}>
      <th scope="row">
        <p>
          <strong>{t("investigation.candidate", { index: String(c.index) })}</strong>
        </p>
        <p>
          <bdi className={styles.words} data-ai-text>
            {visible(c.idea, "prose")}
          </bdi>
        </p>
        <p className={styles.marks}>
          <span className={list.meta}>{t(`investigation.source.${c.source}`)}</span>
          {c.scenario_id !== null && <AiMark kind="saved" />}
          {best && (
            <span className={list.pill} data-tone="ok">
              {t("investigation.best")}
            </span>
          )}
        </p>
        {c.scenario_id !== null && c.run_id !== null && inv.baseline_run_id !== null && (
          <p>
            <Link href={hrefs.results(c.scenario_id, inv.baseline_run_id, c.run_id)}>{t("investigation.open.results")}</Link>
          </p>
        )}
      </th>
      <td>
        {proposalLinked(inv, c.proposal_id) ? t(`proposals.state.${c.proposal_status}`) : <Link href={hrefs.proposal(c.proposal_id)}>{t(`proposals.state.${c.proposal_status}`)}</Link>}
      </td>
      {o ? (
        <>
          <td>{figure(o.baseline, (n) => formatValue(i18n, n, unit))}</td>
          <td>{figure(o.scenario, (n) => formatValue(i18n, n, unit))}</td>
          <td>
            {figure(o.absolute_delta, (n) => formatAbsolute(i18n, n, unit))}
            {o.relative_delta !== null && (
              <>
                {" "}
                (<data value={o.relative_delta}>{formatRelative(i18n, o.relative_delta)}</data>)
              </>
            )}
            <Warnings codes={cmp?.warnings ?? []} />
          </td>
        </>
      ) : (
        <td colSpan={3}>
          {cmp === null ? (
            t("investigation.notRunYet")
          ) : (
            <>
              {t("investigation.notCompared")}
              {cmp.incomparable_reasons.length > 0 && (
                <ul>
                  {cmp.incomparable_reasons.map((r) => (
                    <li key={r}>{t(`compare.incomparable.${r}`)}</li>
                  ))}
                </ul>
              )}
              <Warnings codes={cmp.warnings} />
            </>
          )}
        </td>
      )}
    </tr>
  );
}

function Warnings({ codes }: { codes: readonly ("SCENARIO_PATCH_EMPTY" | "SINGLE_REPLICATION" | "CENSORED_ENTITIES" | "METRICS_TRUNCATED")[] }) {
  const { t } = useI18n();
  if (codes.length === 0) return null;
  return (
    <ul>
      {codes.map((w) => (
        <li key={w}>{t(`compare.warning.${w}`)}</li>
      ))}
    </ul>
  );
}

/** How it ended: the reason in words, the server's closing line, and the AI's claim as its claim. */
function Outcome({ inv }: { inv: Investigation }) {
  const i18n = useI18n();
  const { t, gap } = i18n;
  const line = inv.closing_line;
  const closing = (() => {
    if (line === null) return null;
    switch (line.key) {
      case "improved": {
        const path = line.metric_path ?? "";
        const unit = unitOf(path);
        if (line.candidate_index === null || line.absolute_delta === null || line.relative_delta === null) return null;
        return t("investigation.closing.improved", {
          index: String(line.candidate_index),
          metric: metricName(t, path),
          delta: formatAbsolute(i18n, line.absolute_delta, unit),
          percent: formatRelative(i18n, line.relative_delta),
        });
      }
      case "no_improvement":
        return t("investigation.closing.no_improvement", { count: line.compared, metric: metricName(t, line.metric_path ?? inv.objective?.metric_path ?? "") });
      case "draft_written":
        return t("investigation.closing.draft_written", { revision: String(line.draft_revision ?? "") });
      default:
        return t(`investigation.closing.${line.key}`);
    }
  })();
  return (
    <>
      {inv.reason !== null && <p className={styles.state}>{t(`investigation.reason.${inv.reason}`)}</p>}
      {closing !== null && <p data-closing>{closing}</p>}
      {inv.ai_outcome !== null && (
        <div className={styles.claim} data-claim>
          <p>
            {t("investigation.claim")}
            {gap}
            {t(`investigation.outcome.${inv.ai_outcome}`)}
          </p>
          <p className={styles.meta}>{t("investigation.claimNote")}</p>
        </div>
      )}
      {/* Edit mode: the closing line says to check the draft on the canvas, which the rail opens (no second link to it, H-027). */}
      {inv.mode === "scenario" && <p className={styles.meta}>{t("investigation.noSignificance")}</p>}
    </>
  );
}
