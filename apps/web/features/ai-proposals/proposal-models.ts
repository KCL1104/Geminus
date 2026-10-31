// The models a person may choose for a proposal (H-049), and which of them
// this build offers (H-051: Muse Wonder 1.0 is off unless
// NEXT_PUBLIC_GEMINUS_OFFER_WONDER=1). Not a "use client" module, so a
// server component may read offeredModels() too (a function of a client
// module cannot be called on the server); since H-065 the landing does not:
// it names Muse Wonder 1.0 in no build.
import type { ProposalModel } from "../../lib/contracts";

/** The models a person may choose, the default first (common.schema.json ProposalModel). */
export const PROPOSAL_MODELS: readonly ProposalModel[] = ["muse-spark-1.3", "muse-wonder-1.0"];

export const DEFAULT_MODEL: ProposalModel = "muse-spark-1.3";

/**
 * The models this build offers: both when NEXT_PUBLIC_GEMINUS_OFFER_WONDER
 * is "1", the default alone otherwise. next.config.ts inlines it at build
 * time, set or not; it is read here, on each call, so a test can stub it.
 */
export function offeredModels(): readonly ProposalModel[] {
  return process.env.NEXT_PUBLIC_GEMINUS_OFFER_WONDER === "1" ? PROPOSAL_MODELS : [DEFAULT_MODEL];
}
