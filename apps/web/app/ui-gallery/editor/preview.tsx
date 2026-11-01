"use client";
// The model page with the 2D editor on sample data, for review and the
// browser checks (T-015): the real ModelView, API client and draft session,
// signed in as an editor (a viewer in "viewer"), talking to the in-page
// stand-in (stand-in.ts) instead of the Go API. Asking the AI for a change
// (T-031) goes to the proposal review page's sample being written: the
// stand-in calls no AI. Not part of the product. H-072: in "motion" the
// stand-in has the electronics line's baseline run, which the page plays;
// "still" holds it paused at Day 1, 22:00 for screenshots.
import { useRouter } from "next/navigation";
import { useMemo, useState } from "react";
import { createApiClient } from "../../../lib/api/client";
import { ApiProvider } from "../../../lib/api/provider";
import { AuthProvider } from "../../../lib/auth/session";
import { useI18n } from "../../../lib/i18n/provider";
import { Callout } from "../../../ui/callout";
import { ModelView } from "../../../features/model-editor/model-view";
import { StillPlayback } from "../../../features/playback/still";
import { type ElectronicsLineData, STILL_AT } from "../../../features/results/fixtures/electronics-line-objects";
import { StateMenu } from "../state-menu";
import { SAMPLE_MODEL_ID } from "./sample-line";
import { type SampleState, standInFor, states } from "./sample-states";
import { createStandIn } from "./stand-in";

export function Preview({ state, line }: { state: SampleState; line?: ElectronicsLineData }) {
  const { t } = useI18n();
  const router = useRouter();
  const [standIn] = useState(() => createStandIn({ modelId: SAMPLE_MODEL_ID, ...standInFor(state, line) }));
  const client = useMemo(() => createApiClient({ session: () => ({ organizationId: standIn.organizationId }), fetch: standIn.fetch }), [standIn]);
  const auth = useMemo(() => ({ session: { organizationId: standIn.organizationId }, role: state === "viewer" ? ("viewer" as const) : ("editor" as const) }), [standIn, state]);
  return (
    <>
      <Callout tone="info">{t("preview.note")}</Callout>
      <StateMenu
        label={t("preview.states")}
        current={state}
        href={(s) => `/ui-gallery/editor?state=${s}`}
        groups={[{ title: null, states: states.map((s) => ({ id: s, label: t(`preview.state.${s}`) })) }]}
      />
      <AuthProvider value={auth}>
        <ApiProvider client={client}>
          {state === "still" ? (
            <StillPlayback at={STILL_AT}>
              <ModelView modelId={SAMPLE_MODEL_ID} onProposal={() => router.push("/ui-gallery/proposal?state=generating")} />
            </StillPlayback>
          ) : (
            <ModelView modelId={SAMPLE_MODEL_ID} onProposal={() => router.push("/ui-gallery/proposal?state=generating")} />
          )}
        </ApiProvider>
      </AuthProvider>
    </>
  );
}
