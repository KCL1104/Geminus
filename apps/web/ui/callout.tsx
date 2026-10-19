import type { ReactNode } from "react";
import styles from "./callout.module.css";

export type Tone = "info" | "warning" | "danger" | "success";

/**
 * A boxed message. Danger is an alert (announced at once); the others are
 * notes unless the caller says it is a status that changes.
 */
export function Callout({ tone, role, heading, children, ...data }: {
  tone: Tone;
  role?: "alert" | "note" | "status";
  heading?: ReactNode;
  children?: ReactNode;
} & { [k: `data-${string}`]: string | undefined }) {
  return (
    <section {...data} role={role ?? (tone === "danger" ? "alert" : "note")} data-tone={tone} className={styles.callout}>
      {heading && <h2 className={styles.heading}>{heading}</h2>}
      {children}
    </section>
  );
}
