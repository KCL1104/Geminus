import Link from "next/link";
import { requestLocale } from "../lib/i18n/server";
import { createTranslator } from "../lib/i18n/translate";

export default async function Home() {
  const t = createTranslator(await requestLocale());
  return (
    <>
      <h1>{t("home.title")}</h1>
      <p>
        <Link href="/projects">{t("home.toProjects")}</Link>
      </p>
    </>
  );
}
