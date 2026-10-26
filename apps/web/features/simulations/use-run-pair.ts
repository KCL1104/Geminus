"use client";
// Follows a scenario's run and its baseline's (T-032): reads both runs,
// again every pollMs while either is queued or running, and stops when
// both are done, when a read fails (until retry) or when the pair is
// refused. The worker writes progress about once a second (T-023), so that
// is the default. Each cancel is one request per run still going, with a
// key kept for its retries (D-011); the server's answer moves the state on
// only if it is newer (run-pair.ts).
import { useCallback, useEffect, useReducer, useRef } from "react";
import { type ApiClient, newIdempotencyKey } from "../../lib/api/client";
import { networkProblem, type Problem } from "../../lib/api/problem";
import type { Run } from "../../lib/contracts";
import { initialPair, isActive, type PairState, reducePair } from "./run-pair";

export type RunPair = PairState & {
  /** The pair was refused (not this scenario's): nothing more is read. */
  refused: Problem | null;
  retry: () => void;
  cancel: () => Promise<void>;
};

export function useRunPair(api: ApiClient, baselineId: string, scenarioId: string, opts: { pollMs?: number; accept?: (baseline: Run, scenario: Run) => Problem | null } = {}): RunPair {
  const pollMs = opts.pollMs ?? 1000;
  const [state, dispatch] = useReducer(reducePair, initialPair);
  const [attempt, nextAttempt] = useReducer((n: number) => n + 1, 0);
  const [refused, setRefused] = useReducer((_: Problem | null, p: Problem | null) => p, null);
  const accept = useRef(opts.accept);
  const latest = useRef(state);
  // Read by the poll and the cancel, which run outside render.
  useEffect(() => {
    accept.current = opts.accept;
    latest.current = state;
  });
  const keys = useRef(new Map<string, string>());

  useEffect(() => {
    const ctl = new AbortController();
    let timer: ReturnType<typeof setTimeout> | undefined;
    const poll = async () => {
      let b, s;
      try {
        [b, s] = await Promise.all([api.getSimulation(baselineId, { signal: ctl.signal }), api.getSimulation(scenarioId, { signal: ctl.signal })]);
      } catch {
        b = s = { ok: false as const, problem: networkProblem("The request did not complete.") };
      }
      if (ctl.signal.aborted) return;
      if (!b.ok || !s.ok) {
        dispatch({ type: "pollFailed", problem: !b.ok ? b.problem : (s as { ok: false; problem: Problem }).problem });
        return;
      }
      const problem = accept.current?.(b.data, s.data) ?? null;
      if (problem !== null) {
        setRefused(problem);
        return;
      }
      dispatch({ type: "polled", baseline: b.data, scenario: s.data });
      if (isActive(b.data) || isActive(s.data)) timer = setTimeout(poll, pollMs);
    };
    void poll();
    return () => {
      ctl.abort();
      if (timer !== undefined) clearTimeout(timer);
    };
  }, [api, baselineId, scenarioId, pollMs, attempt]);

  const retry = useCallback(() => {
    dispatch({ type: "retried" });
    nextAttempt();
  }, []);

  const cancel = useCallback(async () => {
    const runs = [latest.current.baseline, latest.current.scenario].filter((r): r is Run => r !== null && isActive(r));
    if (runs.length === 0) return;
    dispatch({ type: "cancelStarted" });
    for (const run of runs) {
      const key = keys.current.get(run.id) ?? newIdempotencyKey();
      keys.current.set(run.id, key);
      const r = await api.cancelSimulation(run.id, key);
      if (!r.ok) {
        dispatch({ type: "cancelFailed", problem: r.problem });
        return;
      }
      dispatch({ type: "cancelAnswered", run: r.data });
    }
    dispatch({ type: "cancelDone" });
    // Follow the cancel to its end, in case polling had stopped.
    nextAttempt();
  }, [api]);

  return { ...state, refused, retry, cancel };
}
