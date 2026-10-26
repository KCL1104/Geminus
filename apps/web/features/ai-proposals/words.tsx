"use client";
// A line of the proposal pages made of the catalog's words and data (T-031
// review round 1, M1): a name or value from the model or the patch is one
// data part, its hidden characters made visible (shown.ts), rendered as a
// text node in its own <bdi>, so it can neither become markup nor reorder
// the words around it. Catalog messages keep their placeholders; a
// placeholder takes text or parts.
import { Fragment } from "react";
import { useI18n } from "../../lib/i18n/provider";
import type { MessageKey } from "../../lib/i18n/translate";
import { visible } from "./shown";

/** The catalog's text, or data (shown in a <bdi>). */
export type Part = string | { data: string };
export type Rich = Part[];

// Where a placeholder's value goes in the catalog's text: private-use
// characters, which no catalog message contains, filled in only after the
// message is split (so data never passes through the replacement).
const SLOT = /(\d+)/;
const SLOTS = /(\d+)/;

/** The parts, adjacent text joined. */
export function joined(parts: Rich): Rich {
  const out: Rich = [];
  for (const p of parts) {
    const last = out[out.length - 1];
    if (typeof p === "string" && typeof last === "string") out[out.length - 1] = last + p;
    else if (p !== "") out.push(p);
  }
  return out;
}

/** How the parts read, each data part told apart from the text around it. */
export const flat = (r: Rich) => r.map((p) => (typeof p === "string" ? p : `⁨${p.data}⁩`)).join("");

/** One data part: a name or a value, with what it hides made visible. */
export const data = (s: string): Rich => [{ data: visible(s) }];

/** A catalog message whose placeholders hold text or parts. */
export function useRich(): (key: MessageKey, values?: Record<string, Rich | string>) => Rich {
  const { t } = useI18n();
  return (key, values = {}) => {
    const names = Object.keys(values);
    const text = t(key, Object.fromEntries(names.map((n, i) => [n, `${i}`])));
    return joined(
      text.split(SLOTS).flatMap((piece): Rich => {
        const m = SLOT.exec(piece);
        if (m === null) return [piece];
        const v = values[names[Number(m[1])]!]!;
        return typeof v === "string" ? [v] : v;
      }),
    );
  };
}

/** The parts as text: each data part in its own <bdi>. */
export function Words({ parts }: { parts: Rich }) {
  return (
    <span>
      {parts.map((p, i) => (typeof p === "string" ? <Fragment key={i}>{p}</Fragment> : <bdi key={i}>{p.data}</bdi>))}
    </span>
  );
}
