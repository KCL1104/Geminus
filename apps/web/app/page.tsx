import type { Metadata } from "next";
import { Landing } from "../features/landing/landing";
import { requestLocale } from "../lib/i18n/server";
import { createTranslator } from "../lib/i18n/translate";

// The start page is the landing (H-048): what DryLine is and the loop it
// runs, on real screens of the product, with the sign-in (or, with a
// session, the projects) as its one action. The rail stays (H-042): its
// brand is the way to the projects and its settings hold the language and
// the theme, which a first-time reader needs here too. H-088: here it is
// the bar across the top at every width, and the page is centred under it
// (app/_components/rail.tsx, ui/shell.module.css).
export async function generateMetadata(): Promise<Metadata> {
  return { description: createTranslator(await requestLocale())("landing.lede") };
}

export default async function Home() {
  const t = createTranslator(await requestLocale());
  return <Landing t={t} />;
}
