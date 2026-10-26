import type { Metadata } from "next";
import { notFound } from "next/navigation";
import { galleryEnabled } from "../../../lib/gallery";
import { requestLocale } from "../../../lib/i18n/server";
import { createTranslator } from "../../../lib/i18n/translate";
import { ProposalPreview } from "./preview";
import { parseState } from "./samples";

// The AI proposal review for review (T-031), off unless GEMINUS_UI_GALLERY=1,
// like the component gallery. One sample per address: ?state=<state>; the
// ready change otherwise.
export const dynamic = "force-dynamic";

export async function generateMetadata(): Promise<Metadata> {
  if (!galleryEnabled()) return {};
  return { title: createTranslator(await requestLocale())("galleryProposal.title") };
}

export default async function ProposalPreviewPage({ searchParams }: { searchParams: Promise<Record<string, string | string[] | undefined>> }) {
  if (!galleryEnabled()) notFound();
  const raw = (await searchParams).state;
  const state = parseState(Array.isArray(raw) ? raw[0] : raw);
  return <ProposalPreview key={state} state={state} />;
}
