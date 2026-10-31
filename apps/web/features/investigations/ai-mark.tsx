"use client";
// H-061 A8 (H-056 §13): the label on what an AI investigation made for the
// person who asked: "Saved by the AI" on a scenario, "Started by the AI" on a
// run. The violet accent marks what the AI does, never a status. A label is
// shown whenever the server's row carries it (made_by_ai), whatever the
// build's flag: what the AI made is always said to be the AI's. The way to
// the investigation is offered only in a build that has its page.
import Link from "next/link";
import { useAuth } from "../../lib/auth/session";
import type { MadeByAI } from "../../lib/contracts";
import { useI18n } from "../../lib/i18n/provider";
import list from "../../ui/list.module.css";
import { investigationsOffered } from "./flags";

export function AiMark({ kind }: { kind: "saved" | "started" }) {
  const { t } = useI18n();
  return (
    <span className={list.pill} data-tone="ai" data-ai-mark={kind}>
      {t(`aiMark.${kind}`)}
    </span>
  );
}

/** "See the investigation", to the page of the investigation that made a row; nothing in a build without that page. */
export function InvestigationLink({ id }: { id: string }) {
  const { t } = useI18n();
  if (!investigationsOffered()) return null;
  return <Link href={`/investigations/${id}`}>{t("aiMark.seeInvestigation")}</Link>;
}

/** "The AI, for you" (or "for the person who asked", by the label's requester), and the way to its investigation. */
export function MadeFor({ made }: { made: MadeByAI }) {
  const { t } = useI18n();
  const { userId } = useAuth();
  return (
    <span data-made-by-ai>
      {t(userId != null && made.requested_by === userId ? "aiMark.for.you" : "aiMark.for.other")}
      {investigationsOffered() && (
        <>
          {" · "}
          <InvestigationLink id={made.investigation_id} />
        </>
      )}
    </span>
  );
}
