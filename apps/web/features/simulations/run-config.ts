// The run settings of a scenario and its baseline (T-032, T-023's
// RunRequest). Both runs take one explicit config, so they meet
// SIMULATION_SPEC §7's conditions for a comparison (same horizon, warm-up,
// replications and seed); the trace is always sampled, for the Marey chart.
// The form only catches what is plainly not a value; every other limit
// (365 days, the execution limits) is the server's to check and explain.
import { newIdempotencyKey } from "../../lib/api/client";
import type { ModelTimeUnit, RunConfig, RunRequest, Scenario } from "../../lib/contracts";

export type RunField = "horizon" | "warmup" | "replications" | "seed";
export type RunForm = Record<RunField, string>;
export type FormIssue = "required" | "number" | "positive" | "warmup" | "replications" | "seed";

const oneDay: Readonly<Record<ModelTimeUnit, string>> = { second: "86400", minute: "1440", hour: "24", day: "1" };

/** One day of the model's time unit, no warm-up, 5 replications, seed 1. */
export function defaultRunForm(unit: ModelTimeUnit): RunForm {
  return { horizon: oneDay[unit], warmup: "0", replications: "5", seed: "1" };
}

const decimal = /^\d+(\.\d+)?$/;
const whole = /^\d+$/;

export function parseRunForm(form: RunForm, unit: ModelTimeUnit): { ok: true; config: RunConfig } | { ok: false; issues: Partial<Record<RunField, FormIssue>> } {
  const issues: Partial<Record<RunField, FormIssue>> = {};
  const v = { horizon: form.horizon.trim(), warmup: form.warmup.trim(), replications: form.replications.trim(), seed: form.seed.trim() };
  for (const f of ["horizon", "warmup", "replications", "seed"] as const) if (v[f] === "") issues[f] = "required";

  const horizon = Number(v.horizon);
  if (!issues.horizon && !decimal.test(v.horizon)) issues.horizon = "number";
  else if (!issues.horizon && !(horizon > 0)) issues.horizon = "positive";
  const warmup = Number(v.warmup);
  if (!issues.warmup && !decimal.test(v.warmup)) issues.warmup = "number";
  else if (!issues.warmup && !issues.horizon && warmup >= horizon) issues.warmup = "warmup";
  const replications = Number(v.replications);
  if (!issues.replications && (!whole.test(v.replications) || replications < 1 || replications > 1000)) issues.replications = "replications";
  const seed = Number(v.seed);
  if (!issues.seed && (!whole.test(v.seed) || !Number.isSafeInteger(seed))) issues.seed = "seed";

  if (Object.keys(issues).length > 0) return { ok: false, issues };
  return {
    ok: true,
    config: { horizon: { value: horizon, unit }, warmup: { value: warmup, unit }, replications, seed, trace_level: "sampled" },
  };
}

/** A run's name, cut to the contract's 200 characters (code points). */
export function runName(name: string): string {
  return [...name].slice(0, 200).join("");
}

/** The baseline (the scenario's base version, patch []) and the scenario (by its saved ID), with one config. */
export function runRequests(scenario: Scenario, config: RunConfig, baselineName: string): { baseline: RunRequest; scenario: RunRequest } {
  return {
    baseline: { model_version_id: scenario.base_version_id, name: runName(baselineName), scenario_patch: [], config },
    scenario: { model_version_id: scenario.base_version_id, name: runName(scenario.name), scenario_id: scenario.id, config },
  };
}

export type RunKeys = { fingerprint: string; baseline: string; scenario: string };

/**
 * Idempotency keys for the two writes (D-011): the same ones while the
 * request is the same, so a retry after a lost answer queues nothing twice;
 * new ones for other settings.
 */
export function keysFor(previous: RunKeys | null, scenarioId: string, config: RunConfig, make: () => string = newIdempotencyKey): RunKeys {
  const fingerprint = JSON.stringify([scenarioId, config.horizon.value, config.horizon.unit, config.warmup.value, config.warmup.unit, config.replications, config.seed, config.trace_level]);
  if (previous !== null && previous.fingerprint === fingerprint) return previous;
  return { fingerprint, baseline: make(), scenario: make() };
}
