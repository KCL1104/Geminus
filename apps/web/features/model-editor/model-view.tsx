"use client";
// /models/[modelId]: the model and its saved draft (T-014's loader and
// draft session), and the 2D editor on them (T-015). A reload of the page
// data (retry, token refresh, after a publish) merges into the open
// session, so pending edits survive it. An editor or owner can ask the AI
// for a change here (T-031), in words or, for a domain whose pack reads
// photos, from a photo of the line (T-056); the proposal opens on its own
// page, so either request waits while the editor has edits the page would
// lose. Under the editor, the model's scenarios lead back to their runs
// and results.
import Link from "next/link";
import { useEffect, useReducer } from "react";
import { RemoteView } from "../../app/_components/states";
import { useRemote } from "../../app/_components/use-remote";
import { useApi } from "../../lib/api/provider";
import { canEdit, useAuth } from "../../lib/auth/session";
import type { Model } from "../../lib/contracts";
import { useI18n } from "../../lib/i18n/provider";
import type { MessageKey } from "../../lib/i18n/translate";
import { en } from "../../messages/en";
import { loadModelPage, type ModelPage } from "../../lib/pages";
import { type DraftEvent, type DraftSession, loadedDraft, pendingOps, reduceDraft } from "../../lib/state/draft";
import { AskForm } from "../ai-proposals/ask-form";
import { PHOTO_DOMAINS } from "../ai-proposals/photo-form";
import { ScenarioList } from "../simulations/scenario-list";
import { ModelEditor } from "./model-editor";

type Page = { model: Model; session: DraftSession } | null;

function pageReducer(s: Page, e: DraftEvent | { type: "loaded"; page: ModelPage }): Page {
  if (e.type === "loaded") return { model: e.page.model, session: loadedDraft(s?.session ?? null, e.page.draft) };
  if (s === null) return s;
  const session = reduceDraft(s.session, e);
  return session === s.session ? s : { ...s, session };
}

/** The pack's name for its domain (D-117 (h)); the ID when the catalog has none. */
function domainName(domain: string, t: (k: MessageKey) => string): string {
  const k = `domain.${domain}.name`;
  return Object.hasOwn(en, k) ? t(k as MessageKey) : domain;
}

/** onProposal: where a proposal the AI was asked for goes (the route's navigation); without it the page offers no request. */
export function ModelView({ modelId, onProposal }: { modelId: string; onProposal?: (proposalId: string) => void }) {
  const api = useApi();
  const { role } = useAuth();
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
            <p>{t("model.summary", { version: String(page.model.latest_version.version), domain: domainName(page.model.domain, t) })}</p>
            {onProposal && canEdit(role) && (
              <AskForm
                model={page.model}
                blockedReason={pendingOps(page.session).length > 0 ? t("ask.saveFirst") : null}
                onCreated={onProposal}
                onReload={reloadPage}
                photo={PHOTO_DOMAINS.includes(page.model.domain)}
              />
            )}
            <ModelEditor model={page.model} session={page.session} dispatch={dispatch} reloadPage={reloadPage} />
            <ScenarioList modelId={page.model.id} latestVersionId={page.model.latest_version.id} />
          </>
        )
      }
    </RemoteView>
  );
}
