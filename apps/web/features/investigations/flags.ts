// H-061 A8 (PROPOSED): whether this build offers AI investigations at all,
// and which planners it offers. NEXT_PUBLIC_GEMINUS_INVESTIGATIONS=1 (the
// default of next.config.ts since A9 passed) shows the entry points and the
// investigation page (the API answers 503 INVESTIGATIONS_OFF while its own
// GEMINUS_INVESTIGATIONS is off, whatever the web says);
// NEXT_PUBLIC_GEMINUS_OFFER_WONDER_PLANNER=1 offers Muse Wonder 1.0 as the
// planner, set only after its planner evaluation passes (H-061 §14), apart
// from H-051's NEXT_PUBLIC_GEMINUS_OFFER_WONDER for proposals. next.config.ts
// fixes both at build time, set or not, so the server and the browser agree.
// Not a "use client" module: the landing, a server component, reads it too.
import type { PlannerModel } from "../../lib/contracts";

/** The build offers AI investigations (the entry points, the page, the revised Guardrails). Read on each call, so a test can stub it. */
export function investigationsOffered(): boolean {
  return process.env.NEXT_PUBLIC_GEMINUS_INVESTIGATIONS === "1";
}

/** The planners there are, the default first (common.schema.json PlannerModel). */
export const PLANNER_MODELS: readonly PlannerModel[] = ["muse-spark-1.3", "muse-wonder-1.0"];

export const DEFAULT_PLANNER: PlannerModel = "muse-spark-1.3";

/** The planners this build offers: Muse Wonder 1.0 only with NEXT_PUBLIC_GEMINUS_OFFER_WONDER_PLANNER=1. */
export function offeredPlanners(): readonly PlannerModel[] {
  return process.env.NEXT_PUBLIC_GEMINUS_OFFER_WONDER_PLANNER === "1" ? PLANNER_MODELS : [DEFAULT_PLANNER];
}

/** The request's planner_model: none for the default, the chosen planner otherwise. */
export function plannerField(planner: PlannerModel): { planner_model?: PlannerModel } {
  return planner === DEFAULT_PLANNER ? {} : { planner_model: planner };
}
