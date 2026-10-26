// What the proposal review page reads (T-031): the proposal, its model (its
// name and latest version), the base version the patch applies to, the
// latest version when the model moved past the base (what changed), and
// the version an approved edit created. Only server answers; the first
// failure wins and nothing is half-shown. A proposal that does not exist,
// or is another organization's, is not found and nothing else is read.
import type { ApiClient, Result } from "../../lib/api/client";
import { protocolProblem } from "../../lib/api/problem";
import type { Model, ModelVersion, Proposal } from "../../lib/contracts";

export type ProposalPage = {
  proposal: Proposal;
  model: Model;
  /** The immutable version the proposal is on: its changes read against it. */
  base: ModelVersion;
  /** The model's latest version, when it is not the base. */
  latest: ModelVersion | null;
  /** The version an approved edit created. */
  created: ModelVersion | null;
};

const refuse = (message: string, requestId: string | null): Result<never> => ({ ok: false, problem: protocolProblem(200, message, requestId) });

export async function loadProposalPage(api: ApiClient, proposalId: string, signal?: AbortSignal): Promise<Result<ProposalPage>> {
  const p = await api.getProposal(proposalId, { signal });
  if (!p.ok) return p;
  const proposal = p.data;
  if (proposal.proposal_id !== proposalId) return refuse("The API answered with another proposal.", p.requestId);
  const [model, base] = await Promise.all([api.getModel(proposal.model_id, { signal }), api.getModelVersion(proposal.base_version_id, { signal })]);
  if (!model.ok) return model;
  if (!base.ok) return base;
  if (model.data.id !== proposal.model_id || base.data.id !== proposal.base_version_id || base.data.model_id !== proposal.model_id) {
    return refuse("The model or version is not the proposal's.", base.requestId);
  }
  const latestId = model.data.latest_version.id;
  const createdId = proposal.status === "approved" && proposal.mode === "edit" ? proposal.new_version_id : undefined;
  const read = async (id: string | undefined): Promise<Result<ModelVersion> | null> => (id === undefined ? null : api.getModelVersion(id, { signal }));
  const [latest, created] = await Promise.all([read(latestId === base.data.id ? undefined : latestId), read(createdId === undefined || createdId === latestId ? undefined : createdId)]);
  if (latest && !latest.ok) return latest;
  if (created && !created.ok) return created;
  const latestVersion = latest?.ok ? latest.data : null;
  if (latestVersion !== null && (latestVersion.id !== latestId || latestVersion.model_id !== proposal.model_id)) return refuse("The latest version is not the model's.", latest!.ok ? latest!.requestId : null);
  const createdVersion = createdId === undefined ? null : createdId === latestId ? latestVersion : created?.ok ? created.data : null;
  return { ok: true, data: { proposal, model: model.data, base: base.data, latest: latestVersion, created: createdVersion }, requestId: p.requestId, etag: null };
}
