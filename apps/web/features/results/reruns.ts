// H-073 (4): "same inputs, same result as N other runs", read, never
// assumed. A Result's digest covers the engine version, the metrics, the
// replication summaries, the measurement window and the reproducibility
// block (SIMULATION_SPEC §9, D-006), so two runs bound to the same inputs
// must have the same digest. This reads the model's runs (oldest first, a
// capped number of pages), keeps the completed ones whose Run has the same
// pinned and effective model hashes, settings hash and engine version as the
// run checked (not the page's other run), reads their Results (a capped number for each run checked),
// keeps those whose Result names the same inputs (with the datasets'
// manifest hash, the seed and the replications), and compares the result
// digests. Every count is of what was read; what was not read is counted
// as such, never guessed.
import type { ApiClient, Result } from "../../lib/api/client";
import type { Result as SimulationResult, Run } from "../../lib/contracts";

/** A completed run and its Result. */
export type Checked = { run: Run; result: SimulationResult };
/** At most `pages` pages of the model's runs (100 each) and `reads` Results for each run checked. */
export type RerunLimits = { pages: number; reads: number };
export const RERUN_LIMITS: RerunLimits = { pages: 3, reads: 5 };
/** Of one run checked: other runs with its inputs whose Result was read, by digest, and those not read (over the cap). */
export type Reruns = { same: number; different: number; unread: number };
/** `listed` runs were read from the list; `listCapped` when the model has more than the pages read. */
export type RerunCheck = { listed: number; listCapped: boolean; runs: Reruns[] };

const sameRunInputs = (a: Run, b: Run) =>
  a.model_hash === b.model_hash && a.effective_model_hash === b.effective_model_hash && a.config_hash === b.config_hash && a.engine_version === b.engine_version;

function sameResultInputs(a: SimulationResult, b: SimulationResult): boolean {
  const x = a.reproducibility;
  const y = b.reproducibility;
  return a.engine_version === b.engine_version && x.model_hash === y.model_hash && x.effective_model_hash === y.effective_model_hash &&
    x.config_hash === y.config_hash && x.dataset_manifest_hash === y.dataset_manifest_hash && x.seed === y.seed && x.replications === y.replications;
}

export async function checkReruns(api: ApiClient, checked: readonly Checked[], limits: RerunLimits = RERUN_LIMITS, signal?: AbortSignal): Promise<Result<RerunCheck>> {
  const modelId = checked[0]?.run.model_id;
  if (modelId === undefined) return { ok: true, data: { listed: 0, listCapped: false, runs: [] }, requestId: null, etag: null };
  const runs: Run[] = [];
  let cursor: string | undefined;
  let requestId: string | null = null;
  // At least the first page: a check that read nothing would have nothing to say.
  for (let page = 0; page < Math.max(1, limits.pages); page++) {
    const r = await api.listSimulations(modelId, { cursor, signal });
    if (!r.ok) return r;
    requestId ??= r.requestId;
    runs.push(...r.data.items);
    cursor = r.data.next_cursor ?? undefined;
    if (cursor === undefined) break;
  }
  const own = new Set(checked.map((c) => c.run.id));
  const results = new Map<string, SimulationResult>();
  const out: Reruns[] = [];
  for (const c of checked) {
    // The page's own two runs are not each other's reruns: "other" runs are the model's others.
    const candidates = runs.filter((r) => r.status === "completed" && !own.has(r.id) && sameRunInputs(r, c.run));
    const read = candidates.slice(0, Math.max(0, limits.reads));
    const tally: Reruns = { same: 0, different: 0, unread: candidates.length - read.length };
    for (const r of read) {
      let result = results.get(r.id);
      if (result === undefined) {
        const got = await api.getSimulationResults(r.id, { signal });
        if (!got.ok) return got;
        result = got.data;
        results.set(r.id, result);
      }
      if (!sameResultInputs(result, c.result)) continue;
      if (result.reproducibility.result_digest === c.result.reproducibility.result_digest) tally.same++;
      else tally.different++;
    }
    out.push(tally);
  }
  return { ok: true, data: { listed: runs.length, listCapped: cursor !== undefined, runs: out }, requestId, etag: null };
}
