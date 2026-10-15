"use client";
import { useCallback, useEffect, useMemo, useState } from "react";
import type { Result } from "../../lib/api/client";
import { networkProblem } from "../../lib/api/problem";
import { fromResult, type Remote } from "../../lib/state/remote";

/**
 * Loads once per change of the inputs `a` and `b` (e.g. the API client and a
 * route ID), and again on `retry()`. An answer is shown only for the load
 * that asked for it: one that arrives after unmount or after a newer load is
 * dropped. A load that rejects instead of returning a Result is shown as a
 * failure, never as loading forever.
 */
export function useRemote<T>(load: (signal: AbortSignal) => Promise<Result<T>>, a: unknown, b?: unknown): [Remote<T>, () => void] {
  const [attempt, setAttempt] = useState(0);
  // A fresh token per input change identifies the load (a, b and attempt are its only inputs).
  // eslint-disable-next-line react-hooks/exhaustive-deps
  const token = useMemo(() => ({}), [a, b, attempt]);
  const [answer, setAnswer] = useState<{ token: object; remote: Remote<T> } | null>(null);
  useEffect(() => {
    const ctl = new AbortController();
    load(ctl.signal).then(
      (r) => {
        if (!ctl.signal.aborted) setAnswer({ token, remote: fromResult(r) });
      },
      () => {
        if (!ctl.signal.aborted) setAnswer({ token, remote: { state: "failed", problem: networkProblem("The request did not complete.") } });
      },
    );
    return () => ctl.abort();
    // eslint-disable-next-line react-hooks/exhaustive-deps -- `load` is a fresh closure each render; `token` tracks its inputs
  }, [token]);
  const retry = useCallback(() => setAttempt((n) => n + 1), []);
  return [answer !== null && answer.token === token ? answer.remote : { state: "loading" }, retry];
}
