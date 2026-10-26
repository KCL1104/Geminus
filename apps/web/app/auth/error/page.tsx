import type { Metadata } from "next";
import { requestLocale } from "../../../lib/i18n/server";
import { createTranslator } from "../../../lib/i18n/translate";

// Where a failed sign-in lands (ADR-018, T-043). The reason is one of three
// fixed words; anything else reads as "failed", including names an object
// inherits such as "toString" (an own-key lookup; `in` would find them, T-045
// review 1 I2). Nothing from the provider's answer is shown.
const reasons = { expired: "authError.expired", refused: "authError.refused", failed: "authError.failed" } as const;

export async function generateMetadata(): Promise<Metadata> {
  return { title: createTranslator(await requestLocale())("authError.title") };
}

export default async function AuthError({ searchParams }: { searchParams: Promise<Record<string, string | string[] | undefined>> }) {
  const t = createTranslator(await requestLocale());
  const reason = (await searchParams).reason;
  const key = typeof reason === "string" && Object.hasOwn(reasons, reason) ? reasons[reason as keyof typeof reasons] : reasons.failed;
  return (
    <>
      <h1>{t("authError.title")}</h1>
      <p>{t(key)}</p>
      <p>
        {/* A plain link: /auth/login is a route handler, not a page. */}
        <a href="/auth/login">{t("authError.retry")}</a>
      </p>
    </>
  );
}
