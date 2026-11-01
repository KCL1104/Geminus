// H-073 (3): the line's constraint by one stated rule, read only from the
// server's comparison rows (keyMetrics). The rule is ELECTRONICS_LINE §5's
// "candidate bottleneck" (D-058, D-117 (d)): the station with the highest
// work ratio w_s (∫min(P, S) / ∫S, processing on shift) in the baseline, a
// tie going to the smaller node ID; "a common reading, not a proof". It is
// w_s and not scheduled utilization because utilization counts the time a
// station holds work it cannot pass on (blocked) and changes over, so a
// station starved of space downstream would look like the constraint.
//
// Nothing is computed but the choice: the figures, the differences and their
// absence are the server's. A pack whose comparison has no work ratio per
// station gets no card ("none"); one whose baseline has none gets
// "unavailable", never a guess.
import { metricKeys, type MetricRow } from "./metrics";

/** The rule's measure: a station metric `<name>.<station>` of the pack (ELECTRONICS_LINE §5's w_s). */
export const RULE_METRIC = "work_ratio";
/** Shown beside it for the named station, labelled as counting blocked and changeover time (H-073). */
export const SCHEDULED_METRIC = "scheduled_utilization";
/** The station measure whose largest change the card gives. */
export const BLOCKED_METRIC = "blocking_ratio";

/** A pack's station measure: the row and the station's key (its first test's node ID, D-117 (d)). */
export type StationRow = { station: string; row: MetricRow };
export type BlockedChange = { kind: "changed"; at: StationRow } | { kind: "unchanged" } | { kind: "unavailable" };
export type LineConstraint =
  | { state: "none" }
  | { state: "unavailable" }
  | { state: "named"; at: StationRow; scheduled: MetricRow | null; blocked: BlockedChange };

/** The rows of one station measure, `/metrics/domain/<name>.<station>/mean`, in the server's order. */
export function stationRows(rows: readonly MetricRow[], name: string): StationRow[] {
  const out: StationRow[] = [];
  for (const row of rows) {
    const keys = metricKeys(row.path);
    if (keys === null || keys.length !== 4 || keys[0] !== "metrics" || keys[1] !== "domain" || keys[3] !== "mean") continue;
    const key = keys[2]!;
    const dot = key.indexOf(".");
    if (dot <= 0 || key.slice(0, dot) !== name || dot === key.length - 1) continue;
    out.push({ station: key.slice(dot + 1), row });
  }
  return out;
}

// Node IDs are ASCII (model-ir.schema.json), so this is their byte order, as the pack sorts them.
const first = (a: StationRow, b: StationRow) => (a.station < b.station ? a : b);

/** The station the rule names, its scheduled utilization when the pack lists one, and the largest blocked-ratio change. */
export function lineConstraint(rows: readonly MetricRow[]): LineConstraint {
  const work = stationRows(rows, RULE_METRIC);
  if (work.length === 0) return { state: "none" };
  let named: StationRow | null = null;
  for (const s of work) {
    const v = s.row.baseline;
    if (v === null) continue;
    const best = named?.row.baseline;
    if (named === null || best === null || best === undefined || v > best) named = s;
    else if (v === best) named = first(named, s);
  }
  if (named === null) return { state: "unavailable" };
  const at = named;
  const scheduled = stationRows(rows, SCHEDULED_METRIC).find((s) => s.station === at.station)?.row ?? null;
  return { state: "named", at, scheduled, blocked: largestChange(stationRows(rows, BLOCKED_METRIC)) };
}

/** The server's largest blocked-ratio difference in either direction (a tie to the smaller ID); none without a difference. */
function largestChange(blocked: StationRow[]): BlockedChange {
  let most: StationRow | null = null;
  for (const s of blocked) {
    const d = s.row.absolute;
    if (d === null) continue;
    const m = most?.row.absolute;
    if (most === null || m === null || m === undefined || Math.abs(d) > Math.abs(m)) most = s;
    else if (Math.abs(d) === Math.abs(m)) most = first(most, s);
  }
  if (most === null) return { kind: "unavailable" };
  return most.row.absolute === 0 ? { kind: "unchanged" } : { kind: "changed", at: most };
}
