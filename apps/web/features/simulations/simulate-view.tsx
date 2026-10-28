"use client";
// A scenario and the one thing to do with it here: run it beside its
// baseline (T-032, H-029). One form, its four settings and one run button
// (DESIGN.md: the scenario run in Deep Scenario Green; H-027: only the
// controls the view needs). Both runs are queued before the page moves on
// to follow them; a run that could not start says which, and a retry
// reuses its key (D-011), so nothing is queued twice. Under the form, the
// scenario's earlier runs open their results again.
import Link from "next/link";
import { type FormEvent, useRef, useState } from "react";
import type { Problem } from "../../lib/api/problem";
import { useApi } from "../../lib/api/provider";
import { canEdit, useAuth } from "../../lib/auth/session";
import type { ModelVersion, Scenario } from "../../lib/contracts";
import { useI18n } from "../../lib/i18n/provider";
import { ProblemView } from "../../app/_components/problem-view";
import { Button } from "../../ui/button";
import { Callout } from "../../ui/callout";
import { TextField } from "../../ui/field";
import { StatusRegion } from "../../ui/status";
import { MAX_RUN_PAGES, resultsHref } from "./load";
import type { RunPair } from "./run-pair";
import { defaultRunForm, type FormIssue, keysFor, parseRunForm, type RunField, type RunForm, type RunKeys, runRequests } from "./run-config";
import styles from "./simulations.module.css";

const issueKey = { required: "run.issue.required", number: "run.issue.number", positive: "run.issue.positive", warmup: "run.issue.warmup", replications: "run.issue.replications", seed: "run.issue.seed" } as const satisfies Record<FormIssue, string>;

export function SimulateView({ scenario, version, earlier, runsCut, onStarted }: {
  scenario: Scenario;
  version: ModelVersion;
  earlier: RunPair[];
  runsCut: boolean;
  onStarted: (baselineRunId: string, scenarioRunId: string) => void;
}) {
  const api = useApi();
  const { role } = useAuth();
  const { t, dateTime } = useI18n();
  const unit = version.ir.time_unit;
  const [form, setForm] = useState<RunForm>(() => defaultRunForm(unit));
  const [issues, setIssues] = useState<Partial<Record<RunField, FormIssue>>>({});
  const [failure, setFailure] = useState<{ side: "baseline" | "scenario"; problem: Problem } | null>(null);
  const [starting, setStarting] = useState(false);
  const keys = useRef<RunKeys | null>(null);
  const busy = useRef(false);
  const changes = scenario.patch.filter((op) => op.op !== "test").length;

  const start = async (e: FormEvent) => {
    e.preventDefault();
    if (busy.current) return;
    const parsed = parseRunForm(form, unit);
    if (!parsed.ok) {
      setIssues(parsed.issues);
      return;
    }
    setIssues({});
    setFailure(null);
    busy.current = true;
    setStarting(true);
    const k = (keys.current = keysFor(keys.current, scenario.id, parsed.config));
    const requests = runRequests(scenario, parsed.config, t("run.name.baseline", { scenario: scenario.name }));
    const b = await api.createSimulation(requests.baseline, k.baseline);
    const s = b.ok ? await api.createSimulation(requests.scenario, k.scenario) : null;
    busy.current = false;
    setStarting(false);
    if (!b.ok) return setFailure({ side: "baseline", problem: b.problem });
    if (s && !s.ok) return setFailure({ side: "scenario", problem: s.problem });
    if (s?.ok) onStarted(b.data.id, s.data.id);
  };

  const field = (name: RunField, label: string, description?: string, inputMode: "decimal" | "numeric" = "numeric") => (
    <TextField
      label={label}
      description={description}
      value={form[name]}
      onChange={(v) => setForm((f) => ({ ...f, [name]: v }))}
      inputMode={inputMode}
      autoComplete="off"
      isInvalid={issues[name] !== undefined}
      errorMessage={issues[name] ? t(issueKey[issues[name]], name === "seed" ? { max: Number.MAX_SAFE_INTEGER } : {}) : undefined}
    />
  );

  return (
    <>
      <h1>{scenario.name}</h1>
      <p>{t("scenario.summary", { version: String(version.version), model: version.ir.name, count: changes })}</p>
      <p>
        <Link href={`/models/${scenario.model_id}`}>{t("scenario.back")}</Link>
      </p>
      {canEdit(role) ? (
        <section aria-labelledby="run-heading" className={styles.section}>
          <h2 id="run-heading">{t("run.title")}</h2>
          <p className={styles.intro}>{t("run.intro", { version: String(version.version) })}</p>
          <form onSubmit={start} noValidate aria-labelledby="run-heading" className={styles.form}>
            <div className={styles.fields}>
              {field("horizon", t("run.horizon", { unit: t(`run.unit.${unit}`) }), undefined, "decimal")}
              {field("warmup", t("run.warmup", { unit: t(`run.unit.${unit}`) }), t("run.warmupHint"), "decimal")}
              {field("replications", t("run.replications"), t("run.replicationsHint"))}
              {field("seed", t("run.seed"), t("run.seedHint"))}
            </div>
            {failure && (
              <>
                <p>{t(`run.startFailed.${failure.side}`)}</p>
                <ProblemView problem={failure.problem} headingLevel={3} />
              </>
            )}
            <Button type="submit" variant="run">
              {t("run.start")}
            </Button>
            <StatusRegion message={starting ? t("run.starting") : null} />
          </form>
        </section>
      ) : (
        <Callout tone="info">{t(role === null ? "run.readOnly.unknownRole" : "run.readOnly.viewer")}</Callout>
      )}
      {earlier.length > 0 && (
        <section aria-labelledby="earlier-heading" className={styles.section}>
          <h2 id="earlier-heading">{t("scenario.earlier.title")}</h2>
          <ul className={styles.earlier}>
            {earlier.map(({ baseline, scenario: run }) => (
              <li key={run.id}>
                <Link href={resultsHref(scenario.id, baseline.id, run.id)}>
                  {t("scenario.earlier.item", { date: dateTime(run.created_at), count: run.config.replications, seed: String(run.config.seed) })}
                </Link>
              </li>
            ))}
          </ul>
        </section>
      )}
      {runsCut && <Callout tone="info">{t("scenario.earlier.cut", { count: MAX_RUN_PAGES * 100 })}</Callout>}
    </>
  );
}
