// A result figure in a table cell (T-032), as formatValue writes it: the
// server's exact value in <data value> with only the words for it. From two
// hours on, a time also says its hours (H-073); they follow the <data>, whole,
// after a place where a line may break (the message's own space, or <wbr>
// where the language writes none), so a narrow cell sets them under the
// minutes instead of running past its edge. Neither part is ever broken
// (H-073 review: on a phone the Compare Table's and the ranking's cells are
// about 130-160 px, and "1,028.79 分鐘（17.1 小時）" is 188 px on one line).
import { useI18n } from "../../lib/i18n/provider";
import { formatValueParts, type MetricUnit } from "./format";
import styles from "./figure.module.css";

/** The message's words after the figure: the space that touches it (or a <wbr>), then the words, whole. */
function After({ words }: { words: string }) {
  const space = /^\s*/u.exec(words)![0];
  return (
    <>
      {space || <wbr />}
      <span className={styles.whole}>{words.slice(space.length)}</span>
    </>
  );
}

/** The message's words before the figure, whole, then the space that touches it (or a <wbr>). */
function Before({ words }: { words: string }) {
  const space = /\s*$/u.exec(words)![0];
  return (
    <>
      <span className={styles.whole}>{words.slice(0, words.length - space.length)}</span>
      {space || <wbr />}
    </>
  );
}

/** value written for the reader in unit; className styles the figure as a whole (its <data> alone when it has no hours). */
export function Figure({ value, unit, className }: { value: number; unit: MetricUnit; className?: string }) {
  const i18n = useI18n();
  const { before, shown, after } = formatValueParts(i18n, value, unit);
  if (before === "" && after === "") {
    return (
      <data value={String(value)} className={className}>
        {shown}
      </data>
    );
  }
  return (
    <span className={className}>
      {before !== "" && <Before words={before} />}
      <data value={String(value)}>{shown}</data>
      {after !== "" && <After words={after} />}
    </span>
  );
}
