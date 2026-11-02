"use client";
// "Earlier requests", above Ask AI's composer (the owner's decision of
// 2026-11-02, H-079; request-log.ts reads them): the model's latest few
// requests to the AI, oldest first, so the newest stands right above the
// composer. Each row is a link to its page (a proposal's or an
// investigation's) and, beside it, Reuse. The link is named by the person's
// own words, as text in its own <bdi> with what hides or reorders text made
// visible (shown.ts), two lines at most (the page has the rest); a photo
// request says it came from a photo, since it has no words. Under them, in
// one line: the state in the server's words, what it asked for (a change or
// a scenario, or that the AI acts on it), and its time. Reuse puts the
// words, the mode and the model back in the composer (onReuse) and sends
// nothing; a photo has nothing to reuse, so no Reuse. Reuse is a quiet
// button beside the link, never inside it, named "Reuse" and described by
// the words it puts back. One line says what the list is: this model's
// latest requests from anyone in the organization, each starting from the
// latest version, which the AI does not see. The rail's AI proposals already
// links to the whole list, so there is no second link to it (H-027); the
// line says older ones are there. With none there is no list; a failed read
// says so with a retry, and the composer under it works as ever.
import Link from "next/link";
import { type ReactNode, useId } from "react";
import { useRemote } from "../../app/_components/use-remote";
import { useApi } from "../../lib/api/provider";
import type { Model } from "../../lib/contracts";
import { useI18n } from "../../lib/i18n/provider";
import { Button } from "../../ui/button";
import { type Entry, loadRequestLog, type Reused, reused, requestState, wordsOf } from "./request-log";
import { visible } from "./shown";
import styles from "./ask.module.css";

export function EarlierRequests({ model, investigations, onReuse }: {
  model: Model;
  /** The panel starts AI investigations (H-061 A8): list its own too. */
  investigations: boolean;
  /** Reuse was pressed: put this back in the composer (nothing is sent). */
  onReuse: (r: Reused) => void;
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
  if (log.entries.length === 0) return null;
  return (
    <section className={styles.earlier} aria-labelledby={headingId} data-earlier>
      <h3 id={headingId}>{t("ask.log.title")}</h3>
      <ol className={styles.log}>
        {log.entries.map((e) => (
          <Row key={e.kind === "proposal" ? e.proposal.proposal_id : e.investigation.id} entry={e} latest={model.latest_version} onReuse={onReuse} />
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

function Row({ entry, latest, onReuse }: { entry: Entry; latest: Model["latest_version"]; onReuse: (r: Reused) => void }) {
  const { t, dateTime } = useI18n();
  const wordsId = useId();
  const state = requestState(entry, latest);
  const sep = t("ask.log.sep");
  const own = wordsOf(entry);
  const again = reused(entry);
  let href: string;
  let at: string;
  // Without its own words a request is named by what it was: a photo, or (a server before H-079) what it asked for.
  let words: ReactNode;
  // What it asked for, under the words, unless the words already say it.
  let kind: string | null;
  if (entry.kind === "proposal") {
    const p = entry.proposal;
    href = `/proposals/${p.proposal_id}`;
    at = p.created_at;
    const photo = p.source_image !== undefined;
    words = own !== null ? <bdi>{visible(own, "prose")}</bdi> : photo ? t("ask.log.photo") : t(`ask.mode.${p.mode}`);
    kind = own !== null || photo ? t(`proposals.mode.${p.mode}`) : null;
  } else {
    const i = entry.investigation;
    href = `/investigations/${i.id}`;
    at = i.created_at;
    words = own !== null ? <bdi>{visible(own, "prose")}</bdi> : t(`ask.log.investigation.${i.mode}`);
    kind = own !== null ? t(`ask.log.investigation.${i.mode}`) : null;
  }
  return (
    <li className={styles.row} data-request>
      <Link href={href} className={styles.entry}>
        <span id={wordsId} className={styles.words} data-request-text>
          {words}
        </span>
        <span className={styles.meta} data-request-meta>
          <span className={styles.state} data-request-state>
            {t(state.key, state.version === undefined ? {} : { version: state.version })}
          </span>
          {kind !== null && (
            <>
              {sep}
              {kind}
            </>
          )}
          {sep}
          <time dateTime={at}>{dateTime(at)}</time>
        </span>
      </Link>
      {again !== null && (
        <Button variant="quiet" className={styles.reuse} aria-describedby={wordsId} onPress={() => onReuse(again)} data-reuse>
          {t("ask.log.reuse")}
        </Button>
      )}
    </li>
  );
}
