import type { Metadata } from "next";
import { notFound } from "next/navigation";
import { galleryEnabled } from "../../../lib/gallery";
import { requestLocale } from "../../../lib/i18n/server";
import { createTranslator } from "../../../lib/i18n/translate";
import { type PreviewState, ResultsPreview } from "./preview";

// The results view for review (T-032), off unless GEMINUS_UI_GALLERY=1,
// like the component gallery. One state per address: ?state=running,
// failed or incomparable; the results otherwise.
export const dynamic = "force-dynamic";

const states: readonly PreviewState[] = ["results", "incomparable", "running", "failed"];

export async function generateMetadata(): Promise<Metadata> {
  if (!galleryEnabled()) return {};
  return { title: createTranslator(await requestLocale())("galleryResults.title") };
}

export default async function ResultsPreviewPage({ searchParams }: { searchParams: Promise<Record<string, string | string[] | undefined>> }) {
  if (!galleryEnabled()) notFound();
  const asked = (await searchParams).state;
  const state = states.find((s) => s === asked) ?? "results";
  return <ResultsPreview state={state} />;
}
