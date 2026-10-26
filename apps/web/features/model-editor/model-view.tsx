"use client";
// /models/[modelId]: the model and its saved draft (T-014's loader and
// draft session), and the 2D editor on them (T-015). A reload of the page
// data (retry, token refresh, after a publish) merges into the open
// session, so pending edits survive it.
import Link from "next/link";
import { useEffect, useReducer } from "react";
import { RemoteView } from "../../app/_components/states";
import { useRemote } from "../../app/_components/use-remote";
import { useApi } from "../../lib/api/provider";
import type { Model } from "../../lib/contracts";
import { useI18n } from "../../lib/i18n/provider";
import { loadModelPage, type ModelPage } from "../../lib/pages";
import { type DraftEvent, type DraftSession, loadedDraft, reduceDraft } from "../../lib/state/draft";
import { ModelEditor } from "./model-editor";

type Page = { model: Model; session: DraftSession } | null;

function pageReducer(s: Page, e: DraftEvent | { type: "loaded"; page: ModelPage }): Page {
  if (e.type === "loaded") return { model: e.page.model, session: loadedDraft(s?.session ?? null, e.page.draft) };
  if (s === null) return s;
  const session = reduceDraft(s.session, e);
  return session === s.session ? s : { ...s, session };
}

export function ModelView({ modelId }: { modelId: string }) {
  const api = useApi();
  const { t } = useI18n();
  const [remote, retry] = useRemote((signal) => loadModelPage(api, modelId, signal), api, modelId);
  const [page, dispatch] = useReducer(pageReducer, null);
  useEffect(() => {
    if (remote.state === "loaded") dispatch({ type: "loaded", page: remote.data });
  }, [remote]);
  const reloadPage = async () => {
    const r = await loadModelPage(api, modelId);
    if (!r.ok) return r.problem;
    dispatch({ type: "loaded", page: r.data });
    return null;
  };
  return (
    <RemoteView remote={remote} what="model" onRetry={retry}>
      {() =>
        page && (
          <>
            <h1>{page.model.name}</h1>
            <p>
              <Link href={`/projects/${page.model.project_id}`}>{t("model.back")}</Link>
            </p>
            <p>{t("model.summary", { version: String(page.model.latest_version.version), domain: page.model.domain })}</p>
            <ModelEditor model={page.model} session={page.session} dispatch={dispatch} reloadPage={reloadPage} />
          </>
        )
      }
    </RemoteView>
  );
}
