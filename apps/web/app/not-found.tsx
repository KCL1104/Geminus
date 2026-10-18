import Link from "next/link";
import type { Metadata } from "next";
import { requestLocale } from "../lib/i18n/server";
import { createTranslator } from "../lib/i18n/translate";

// Any address without a page, in the reader's language (T-044 review M2);
// Next.js's own 404 page is English only.
export async function generateMetadata(): Promise<Metadata> {
  return { title: createTranslator(await requestLocale())("notFound.title") };
}

export default async function NotFound() {
  const t = createTranslator(await requestLocale());
  return (
    <>
      <h1>{t("notFound.title")}</h1>
      <p>{t("notFound.body")}</p>
      <p>
        <Link href="/">{t("notFound.home")}</Link>
      </p>
    </>
  );
}
