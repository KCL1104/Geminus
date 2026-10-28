import styles from "./status.module.css";

/**
 * Where a page says that an action worked (ADR-021: a status message in the
 * page, not a toast). The region is always there, empty until it has
 * something to say, so a screen reader announces the message when it
 * arrives; it stays until replaced, so nobody has to read it in time.
 */
export function StatusRegion({ message, quiet = false }: {
  message: string | null;
  /**
   * Said to a screen reader only, from the start, so the region's look
   * never changes as it speaks; the page shows what needs showing itself
   * (ShownStatus).
   */
  quiet?: boolean;
}) {
  return (
    <p role="status" className={styles.status} data-empty={message ? undefined : ""} data-quiet={quiet ? "" : undefined}>
      {message ?? ""}
    </p>
  );
}

/** On screen, in the same frame, what a quiet StatusRegion has already said (so a screen reader does not read it twice). */
export function ShownStatus({ message }: { message: string }) {
  return (
    <p className={styles.status} aria-hidden="true">
      {message}
    </p>
  );
}
