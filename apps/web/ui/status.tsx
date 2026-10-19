import styles from "./status.module.css";

/**
 * Where a page says that an action worked (ADR-021: a status message in the
 * page, not a toast). The region is always there, empty until it has
 * something to say, so a screen reader announces the message when it
 * arrives; it stays until replaced, so nobody has to read it in time.
 */
export function StatusRegion({ message }: { message: string | null }) {
  return (
    <p role="status" className={styles.status} data-empty={message ? undefined : ""}>
      {message ?? ""}
    </p>
  );
}
