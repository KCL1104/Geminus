"use client";
// "Earlier requests", above Ask AI's composer (the owner's decision of
// 2026-11-02; request-log.ts reads them): the model's latest few requests to
// the AI, oldest first, so the newest stands right above the composer. Each
// row is one link to its page (a proposal's or an investigation's), named by
// what the server gives: the AI's summary as text in its own <bdi>, with
// what hides or reorders text made visible (shown.ts), or what the AI was
// asked to make; then its state in the server's words, that it came from a
// photo, and its time. Its words take two lines at most; the page has the
// rest. One line says what the list is not: each request starts from the
// model's latest version, and the AI does not see these. The rail's AI
// proposals already links to the whole list, so there is no second link to
// it (H-027); the line says older ones are there. With none there is no
// list; a failed read says so with a retry, and the composer under it works
// as ever. Nothing here sends anything.
import Link from "next/link";
import { type ReactNode, useId } from "react";
import { useRemote } from "../../app/_components/use-remote";
import { useApi } from "../../lib/api/provider";
import type { Model } from "../../lib/contracts";
import { useI18n } from "../../lib/i18n/provider";
import { Button } from "../../ui/button";
import { type Entry, loadRequestLog, MAX_PAGES, requestState } from "./request-log";
import { visible } from "./shown";
import styles from "./ask.module.css";

export function EarlierRequests({ model, investigations }: {
  model: Model;
  /** The panel starts AI investigations (H-061 A8): list its own too. */
  investigations: boolean;
}) {
  const api = useApi();
  const { t, gap } = useI18n();
  const headingId = useId();
  const failedId = useId();
  const [remote, retry] = useRemote((signal) => loadRequestLog(api, model.id, { investigations, signal }), api, `${model.id} ${investigations}`);
  if (remote.state === "loading") return null;
  if (remote.state === "failed") {
    return (
      <div className={styles.failed} data-earlier-failed>
        <p id={failedId}>{t("ask.log.failed")}</p>
        <Button onPress={retry} aria-describedby={failedId}>
          {t("retry")}
        </Button>
      </div>
    );
  }
  const log = remote.data;
  if (log.tooMany) {
    return (
      <p className={styles.note} data-earlier-note>
        {t("ask.log.tooMany", { count: MAX_PAGES * 100 })}
      </p>
    );
  }
  if (log.entries.length === 0) return null;
  return (
    <section className={styles.earlier} aria-labelledby={headingId} data-earlier>
      <h3 id={headingId}>{t("ask.log.title")}</h3>
      <ol className={styles.log}>
        {log.entries.map((e) => (
          <Row key={e.kind === "proposal" ? e.proposal.proposal_id : e.investigation.id} entry={e} latest={model.latest_version} />
        ))}
      </ol>
      <p className={styles.note} data-earlier-note>
        {t("ask.log.note")}
        {log.older && (
          <>
            {gap}
            {t("ask.log.older")}
          </>
        )}
      </p>
    </section>
  );
}

function Row({ entry, latest }: { entry: Entry; latest: Model["latest_version"] }) {
  const { t, dateTime } = useI18n();
  const state = requestState(entry, latest);
  const sep = t("ask.log.sep");
  let href: string;
  let words: ReactNode;
  let kind: string | null = null;
  let photo = false;
  let at: string;
  if (entry.kind === "proposal") {
    const p = entry.proposal;
    href = `/proposals/${p.proposal_id}`;
    at = p.created_at;
    photo = p.source_image !== undefined;
    const summary = typeof p.summary === "string" && p.summary.trim() !== "" ? p.summary : null;
    // The AI's summary names it; without one, what it was asked to make (and the kind is not said twice).
    words = summary !== null ? <bdi>{visible(summary, "prose")}</bdi> : t(`ask.mode.${p.mode}`);
    if (summary !== null) kind = t(`proposals.mode.${p.mode}`);
  } else {
    const i = entry.investigation;
    href = `/investigations/${i.id}`;
    at = i.created_at;
    words = t(`ask.log.investigation.${i.mode}`);
  }
  return (
    <li data-request>
      <Link href={href} className={styles.entry}>
        <span className={styles.words} data-request-text>
          {words}
        </span>
        <span className={styles.meta}>
          <span className={styles.state} data-request-state>
            {t(state.key, state.version === undefined ? {} : { version: state.version })}
          </span>
          {kind !== null && (
            <>
              {sep}
              {kind}
            </>
          )}
          {photo && (
            <>
              {sep}
              {t("ask.log.photo")}
            </>
          )}
          {sep}
          <time dateTime={at}>{dateTime(at)}</time>
        </span>
      </Link>
    </li>
  );
}
