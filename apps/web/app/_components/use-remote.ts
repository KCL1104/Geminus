"use client";
import { useEffect, useMemo, useState } from "react";
import type { Result } from "../../lib/api/client";
import { fromResult, type Remote } from "../../lib/state/remote";

/**
 * Loads once per change of the inputs `a` and `b` (e.g. the API client and a
 * route ID). An answer is shown only for the load that asked for it: one that
 * arrives after unmount or after a newer load is dropped.
 */
export function useRemote<T>(load: (signal: AbortSignal) => Promise<Result<T>>, a: unknown, b?: unknown): Remote<T> {
  // A fresh token per input change identifies the load (a and b are its only inputs).
  // eslint-disable-next-line react-hooks/exhaustive-deps
  const token = useMemo(() => ({}), [a, b]);
  const [answer, setAnswer] = useState<{ token: object; remote: Remote<T> } | null>(null);
  useEffect(() => {
    const ctl = new AbortController();
    load(ctl.signal).then((r) => {
      if (!ctl.signal.aborted) setAnswer({ token, remote: fromResult(r) });
    });
    return () => ctl.abort();
    // eslint-disable-next-line react-hooks/exhaustive-deps -- `load` is a fresh closure each render; `token` tracks its inputs
  }, [token]);
  return answer !== null && answer.token === token ? answer.remote : { state: "loading" };
}
