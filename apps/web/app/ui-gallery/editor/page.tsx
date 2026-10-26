import type { Metadata } from "next";
import { notFound } from "next/navigation";
import { galleryEnabled } from "../../../lib/gallery";
import { requestLocale } from "../../../lib/i18n/server";
import { createTranslator } from "../../../lib/i18n/translate";
import { Preview } from "./preview";
import { parseState } from "./sample-states";

// Like the gallery: a review tool, 404 unless the deployment turns it on.
export const dynamic = "force-dynamic";

export async function generateMetadata(): Promise<Metadata> {
  if (!galleryEnabled()) return {};
  return { title: createTranslator(await requestLocale())("preview.title") };
}

export default async function EditorPreviewPage({ searchParams }: { searchParams: Promise<{ state?: string | string[] }> }) {
  if (!galleryEnabled()) notFound();
  const raw = (await searchParams).state;
  const state = parseState(Array.isArray(raw) ? raw[0] : raw);
  return <Preview key={state} state={state} />;
}
