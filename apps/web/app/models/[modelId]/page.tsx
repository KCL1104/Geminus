"use client";
import Link from "next/link";
import { use, useEffect, useReducer } from "react";
import { useApi } from "../../../lib/api/provider";
import { loadModelPage, type ModelPage } from "../../../lib/pages";
import { type DraftEvent, type DraftSession, loadedDraft, reduceDraft } from "../../../lib/state/draft";
import { DraftStatus, RemoteView, RequireSession } from "../../_components/states";
import { useRemote } from "../../_components/use-remote";

// A reload of the page data (retry, token refresh) merges into the open
// session, so pending edits survive it.
function sessionReducer(s: DraftSession | null, e: DraftEvent | { type: "loaded"; page: ModelPage }): DraftSession | null {
  if (e.type === "loaded") return loadedDraft(s, e.page.draft);
  return s === null ? s : reduceDraft(s, e);
}

function ModelView({ modelId }: { modelId: string }) {
  const api = useApi();
  const [remote, retry] = useRemote((signal) => loadModelPage(api, modelId, signal), api, modelId);
  const [session, dispatch] = useReducer(sessionReducer, null);
  useEffect(() => {
    if (remote.state === "loaded") dispatch({ type: "loaded", page: remote.data });
  }, [remote]);
  const reload = async () => {
    const r = await api.getDraft(modelId);
    if (!r.ok) return r.problem;
    dispatch({ type: "remoteLoaded", draft: r.data });
    return null;
  };
  return (
    <RemoteView remote={remote} what="model" onRetry={retry}>
      {(page) => (
        <>
          <h1>{page.model.name}</h1>
          <p>
            <Link href={`/projects/${page.model.project_id}`}>Back to project</Link>
          </p>
          <p>
            Latest version {page.model.latest_version.version}; domain {page.model.domain}.
          </p>
          {session && <DraftStatus session={session} onReload={reload} onDiscard={() => dispatch({ type: "discardLocal" })} />}
          <p>The 2D editor and 3D viewer are not part of this build (T-015, T-017).</p>
        </>
      )}
    </RemoteView>
  );
}

export default function ModelPageRoute({ params }: { params: Promise<{ modelId: string }> }) {
  const { modelId } = use(params);
  return (
    <RequireSession>
      <ModelView modelId={modelId} />
    </RequireSession>
  );
}
