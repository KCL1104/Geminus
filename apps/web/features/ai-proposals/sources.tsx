"use client";
// The web pages the AI asked its search tool to open for a proposal asked
// for with research on (H-046 (2)). Meta returns no tool result, so that a
// page loaded, or what it held, is not known, and the section says so
// (review round 1, M3). The server keeps a source only when the call itself
// asked to open its URL, only https, and cleans its title; this page still treats both as
// the AI's data: a URL that is not https, or does not parse, is not shown at
// all (defense in depth), the title is text with what it hides made visible
// (shown.ts) in its own <bdi>, and the site is the URL's host as the browser
// reads it (an internationalized name as its xn-- form, so a look-alike
// cannot pass for another site). Each link opens a new tab, says so to a
// screen reader, and sends no referrer and no opener; nothing here vouches
// for what the page says, and the section says that too. These are the only
// links on a proposal page that come from the AI's answer.
import { useId } from "react";
import type { GeneratedBy, WebSource } from "../../lib/contracts";
import { useI18n } from "../../lib/i18n/provider";
import { ExternalIcon } from "../../ui/icons";
import { visible } from "./shown";
import styles from "./proposal.module.css";

/** A source as the page shows it: its https URL, the text of its link and its site. */
export type ShownSource = { url: string; text: string; site: string };

/** The sources an https link can be made for, in the server's order, each address once; any other is left out. */
export function shownSources(sources: readonly WebSource[] | undefined): ShownSource[] {
  const out: ShownSource[] = [];
  const seen = new Set<string>();
  for (const s of sources ?? []) {
    let u: URL;
    try {
      u = new URL(s.url);
    } catch {
      continue;
    }
    if (u.protocol !== "https:" || u.hostname === "" || u.username !== "" || u.password !== "" || seen.has(u.href)) continue;
    seen.add(u.href);
    const title = s.title?.trim();
    out.push({ url: u.href, text: visible(title === undefined || title === "" ? s.url : title), site: u.hostname });
  }
  return out;
}

/** The section, or nothing when the proposal was not researched or names no page. */
export function Sources({ sources, search }: { sources: readonly WebSource[] | undefined; search: GeneratedBy["web_search"] }) {
  const { t } = useI18n();
  const headingId = useId();
  const shown = shownSources(sources);
  const dropped = search?.sources_dropped ?? 0;
  if (shown.length === 0 && dropped === 0) return null;
  return (
    <section className={styles.section} data-section="sources" aria-labelledby={headingId}>
      <h2 id={headingId}>{t("proposal.sources")}</h2>
      <p className={styles.hint}>{t("proposal.sourcesHint")}</p>
      {shown.length > 0 && (
        <ul className={styles.sources}>
          {shown.map((s, i) => (
            <li key={i} data-source={s.site}>
              <a href={s.url} target="_blank" rel="noopener noreferrer nofollow">
                <bdi>{s.text}</bdi>
                <span className={styles.offscreen}> {t("proposal.sources.newTab")}</span>
                <ExternalIcon />
              </a>
              <bdi className={styles.site}>{s.site}</bdi>
            </li>
          ))}
        </ul>
      )}
      {dropped > 0 && (
        <p className={styles.hint} data-dropped={dropped}>
          {shown.length > 0 ? t("proposal.sources.dropped", { count: dropped }) : t("proposal.sources.allDropped", { count: dropped })}
        </p>
      )}
    </section>
  );
}
