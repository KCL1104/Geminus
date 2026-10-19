import type { Metadata } from "next";
import { notFound } from "next/navigation";
import { galleryEnabled } from "../../lib/gallery";
import { requestLocale } from "../../lib/i18n/server";
import { createTranslator } from "../../lib/i18n/translate";
import { Gallery } from "./gallery";

// Read the switch at request time, not at build time.
export const dynamic = "force-dynamic";

export async function generateMetadata(): Promise<Metadata> {
  if (!galleryEnabled()) return {};
  return { title: createTranslator(await requestLocale())("gallery.title") };
}

export default function GalleryPage() {
  if (!galleryEnabled()) notFound();
  return <Gallery />;
}
