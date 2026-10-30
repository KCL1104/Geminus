"use client";
import Link from "next/link";
import { useRouter } from "next/navigation";
import { use, useEffect, useRef, useState } from "react";
import { NewModelForm } from "../../../features/models/new-model";
import { useApi } from "../../../lib/api/provider";
import { canEdit, useAuth } from "../../../lib/auth/session";
import { Button } from "../../../ui/button";
import { PlusIcon } from "../../../ui/icons";
import { PageHeader } from "../../../ui/page-header";
import { useI18n } from "../../../lib/i18n/provider";
import list from "../../../ui/list.module.css";
import { MorePages, RemoteView, RequireSession } from "../../_components/states";
import { useRemote } from "../../_components/use-remote";

function Models({ projectId }: { projectId: string }) {
  const api = useApi();
  const { t } = useI18n();
  const [remote, retry] = useRemote((signal) => api.listModels(projectId, { signal }), api, projectId);
  return (
    <RemoteView remote={remote} what="models" onRetry={retry}>
      {(page) =>
        page.items.length === 0 ? (
          <p className={list.empty}>{t("models.empty")}</p>
        ) : (
          <>
            <ul className={list.list}>
              {page.items.map((m) => (
                <li key={m.id}>
                  <Link href={`/models/${m.id}`}>{m.name}</Link>
                  <span className={list.meta}>{t("models.version", { version: String(m.latest_version.version) })}</span>
                  {m.draft.out_of_date && <span className={list.meta}>{t("models.outOfDate")}</span>}
                </li>
              ))}
            </ul>
            <MorePages cursor={page.next_cursor} shown={page.items.length} what="models" />
          </>
        )
      }
    </RemoteView>
  );
}

// An editor or owner can start a new model from a template (H-045): the
// header's action opens the form above the list in its place, and Cancel
// gives the action back with the focus on it. A viewer is not offered it.
export default function ProjectPage({ params }: { params: Promise<{ projectId: string }> }) {
  const { projectId } = use(params);
  const { t } = useI18n();
  const { session, role } = useAuth();
  const router = useRouter();
  const [creating, setCreating] = useState(false);
  const opener = useRef<HTMLButtonElement>(null);
  const refocus = useRef(false);
  useEffect(() => {
    if (!creating && refocus.current) {
      refocus.current = false;
      opener.current?.focus();
    }
  }, [creating]);
  const offered = session !== null && canEdit(role);
  return (
    <>
      <PageHeader
        trail={[]}
        title={t("models.title")}
        actions={
          offered && !creating ? (
            <Button ref={opener} icon={<PlusIcon />} onPress={() => setCreating(true)}>
              {t("newModel.open")}
            </Button>
          ) : undefined
        }
      />
      <RequireSession>
        {offered && creating && (
          <NewModelForm
            projectId={projectId}
            onCreated={(id) => router.push(`/models/${id}`)}
            onCancel={() => {
              refocus.current = true;
              setCreating(false);
            }}
          />
        )}
        <Models projectId={projectId} />
      </RequireSession>
    </>
  );
}
