"use client";
// What a page that failed to render says (T-044 review M2): from the
// catalog, with the server's reference for its logs when there is one,
// never the error's own message (it can carry internals).
import { useI18n } from "../../lib/i18n/provider";
import { Button } from "../../ui/button";

export function CrashNotice({ error, retry }: { error: Error & { digest?: string }; retry: () => void }) {
  const { t } = useI18n();
  return (
    <section role="alert">
      <h1>{t("crash.title")}</h1>
      <p>{t("crash.body")}</p>
      {error.digest && <p>{t("crash.reference", { digest: error.digest })}</p>}
      <Button onPress={() => retry()}>{t("retry")}</Button>
    </section>
  );
}
