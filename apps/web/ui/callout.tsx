import type { ReactNode } from "react";
import styles from "./callout.module.css";

export type Tone = "info" | "warning" | "danger" | "success";

/**
 * A boxed message. Danger is an alert (announced at once); the others are
 * notes unless the caller says it is a status that changes.
 */
export function Callout({ tone, role, heading, headingLevel = 2, children, ...data }: {
  tone: Tone;
  role?: "alert" | "note" | "status";
  heading?: ReactNode;
  /** 3 when the callout sits in a section that already has an h2. */
  headingLevel?: 2 | 3;
  children?: ReactNode;
} & { [k: `data-${string}`]: string | undefined }) {
  const Heading = headingLevel === 3 ? "h3" : "h2";
  return (
    <section {...data} role={role ?? (tone === "danger" ? "alert" : "note")} data-tone={tone} className={styles.callout}>
      {heading && <Heading className={styles.heading}>{heading}</Heading>}
      {children}
    </section>
  );
}
