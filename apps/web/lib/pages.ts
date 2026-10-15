// Data needed by each route, loaded through the API client only. Kept out of
// the React components so the loading rules are testable without a browser.
import type { ApiClient, Result } from "./api/client";
import type { Draft, Model } from "./contracts";

export type ModelPage = { model: Model; draft: Draft };

/** A model and its saved draft; the first failure wins and nothing is half-shown. */
export async function loadModelPage(api: ApiClient, modelId: string, signal?: AbortSignal): Promise<Result<ModelPage>> {
  const [model, draft] = await Promise.all([api.getModel(modelId, { signal }), api.getDraft(modelId, { signal })]);
  if (!model.ok) return model;
  if (!draft.ok) return draft;
  if (draft.data.model_id !== model.data.id) {
    return {
      ok: false,
      problem: { kind: "protocol", code: null, status: 200, message: "The draft belongs to another model.", requestId: draft.requestId, details: [], retryAfterSeconds: null },
    };
  }
  return { ok: true, data: { model: model.data, draft: draft.data }, requestId: model.requestId, etag: draft.etag };
}
