"use client";
// H-073 (5): the next step after a photo proposal is approved, in its
// outcome (or, on a page opened after the approval, its decided section):
// one default button, "Look up typical times for the new stations", that
// leaves a filled-in request for the model page's Ask AI (station-research.ts)
// and opens the model page. Under it, why (a photo shows no times, so the
// stations it added took numbers the model already had), naming those
// stations as data, each in its own <bdi>; what the button opens and that
// nothing is sent; and the research note the ask form shows beside its
// opt-in. The button is the button's description of both lines. Offered
// only to a person who can ask the AI (an editor or owner), only where the
// page can open the model (onAsk), and only when the proposal added a
// station. Nothing here asks the AI: the person sends the request, and
// approves what comes back.
import { useId, useMemo } from "react";
import { canEdit, useAuth } from "../../lib/auth/session";
import type { ModelIR, Proposal } from "../../lib/contracts";
import { useI18n } from "../../lib/i18n/provider";
import { Button } from "../../ui/button";
import { AskIcon } from "../../ui/icons";
import { newStations, noteAskPrefill, stationTimesRequest } from "./station-research";
import { data, joined, type Rich, useRich, Words } from "./words";
import styles from "./station-research.module.css";

export function StationResearchOffer({ proposal, base, onAsk }: {
  proposal: Proposal;
  /** The base version's IR, which the proposal's changes apply to. */
  base: ModelIR;
  /** Opens the model page (the route's navigation); without it nothing is offered. */
  onAsk: ((modelId: string) => void) | undefined;
}) {
  const { role } = useAuth();
  const { t, locale, gap } = useI18n();
  const rich = useRich();
  const whyId = useId();
  const noteId = useId();
  // Every station the proposal added is counted; a name they share is listed once (H-073 review).
  const { names: stations, count } = useMemo(() => newStations(proposal, base), [proposal, base]);
  if (onAsk === undefined || !canEdit(role) || stations.length === 0) return null;

  // Each name quoted and listed the reader's way, the names as data.
  const parts = new Intl.ListFormat(locale, { type: "conjunction" }).formatToParts(stations.map((_, i) => String(i)));
  const list: Rich = joined(parts.flatMap((p) => (p.type === "element" ? rich("proposal.value.text", { text: data(stations[Number(p.value)]!) }) : [p.value])));

  const open = () => {
    noteAskPrefill(proposal.model_id, { from: "photoStations", intent: stationTimesRequest(t, locale, stations, count), mode: "edit", research: true });
    onAsk(proposal.model_id);
  };

  return (
    <div className={styles.offer} data-station-research>
      <Button icon={<AskIcon />} aria-describedby={`${whyId} ${noteId}`} onPress={open}>
        {t("proposal.stationTimes.offer")}
      </Button>
      <p id={whyId} className={styles.hint} data-station-research-why>
        <Words parts={rich("proposal.stationTimes.why", { stations: list })} />
        {gap}
        {t("proposal.stationTimes.opens", { form: t("ask.title") })}
      </p>
      <p id={noteId} className={styles.hint}>
        {t("ask.researchHint")}
      </p>
    </div>
  );
}
