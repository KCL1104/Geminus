"use client";
// Which AI wrote a result (H-043): the model and its provider, and for a
// photo the segmenter and how many objects it found, from the server's
// generated_by. Product names come from the catalog when it knows them,
// else the provider's own ID is shown as data.
import type { GeneratedBy } from "../../lib/contracts";
import { useI18n } from "../../lib/i18n/provider";
import type { MessageKey } from "../../lib/i18n/translate";
import { en } from "../../messages/en";

function useNames() {
  const { t } = useI18n();
  const known = (key: string) => Object.hasOwn(en, key);
  const model = (id: string | undefined, provider: string) => {
    if (id === undefined) return provider;
    return known(`ai.model.${id}`) ? t(`ai.model.${id}` as MessageKey) : id;
  };
  const provider = (id: string) => (known(`ai.provider.${id}`) ? t(`ai.provider.${id}` as MessageKey) : id);
  return { model, provider };
}

/** "Muse Spark 1.3 (Meta)": the model that wrote the answer, with its provider. */
export function useWriter(g: GeneratedBy | undefined): string | null {
  const { t } = useI18n();
  const names = useNames();
  if (g === undefined) return null;
  return g.model === undefined ? names.provider(g.provider) : t("ai.byProvider", { model: names.model(g.model, g.provider), provider: names.provider(g.provider) });
}

/** "SAM 3.1 · 12 objects": what read the photo, or null for a proposal asked for in words. */
export function useReader(g: GeneratedBy | undefined): string | null {
  const { t } = useI18n();
  const names = useNames();
  const s = g?.segmenter;
  if (s === undefined) return null;
  return t("ai.objects", { model: names.model(s.model, s.provider), count: s.objects });
}
