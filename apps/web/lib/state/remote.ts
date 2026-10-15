// Server state as the UI sees it: loading, loaded, or failed with a Problem.
// It is a cache of what the server said, never edited locally.
import type { Result } from "../api/client";
import type { Problem } from "../api/problem";

export type Remote<T> =
  | { state: "loading" }
  | { state: "loaded"; data: T; requestId: string | null }
  | { state: "failed"; problem: Problem };

export function fromResult<T>(r: Result<T>): Remote<T> {
  return r.ok ? { state: "loaded", data: r.data, requestId: r.requestId } : { state: "failed", problem: r.problem };
}
