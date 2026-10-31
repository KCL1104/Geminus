import type { Metadata } from "next";
import { notFound } from "next/navigation";
import { galleryEnabled } from "../../../lib/gallery";
import { requestLocale } from "../../../lib/i18n/server";
import { createTranslator } from "../../../lib/i18n/translate";
import { InvestigationPreview } from "./preview";
import { parseState } from "./samples";

// The AI investigation page for review (H-061 A8), off unless
// GEMINUS_UI_GALLERY=1, like the component gallery. One sample per address:
// ?state=<state>; the running one otherwise.
export const dynamic = "force-dynamic";

export async function generateMetadata(): Promise<Metadata> {
  if (!galleryEnabled()) return {};
  return { title: createTranslator(await requestLocale())("galleryInvestigation.title") };
}

export default async function InvestigationPreviewPage({ searchParams }: { searchParams: Promise<Record<string, string | string[] | undefined>> }) {
  if (!galleryEnabled()) notFound();
  const raw = (await searchParams).state;
  const state = parseState(Array.isArray(raw) ? raw[0] : raw);
  return <InvestigationPreview key={state} state={state} />;
}
