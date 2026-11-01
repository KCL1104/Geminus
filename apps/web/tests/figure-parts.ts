// H-073 review: how a cell writes a figure in minutes. The <data value>
// holds the server's exact value and only the words for it ("1,028.79 min");
// from two hours on, the hours follow it outside the <data>, in an element of
// their own, after a place where a line may break (a space, or <wbr> in a
// language that writes none), so a narrow cell can set them under the
// minutes while neither part is ever broken (figure.tsx).

export type FigureParts = {
  /** The <data>'s value attribute: the server's number. */
  value: string | null;
  /** The <data>'s words. */
  data: string;
  /** What follows the <data> before the next element: a space, a <wbr>, or nothing. */
  gap: "space" | "wbr" | null;
  /** The words of the element right after the gap (the hours), or null when none follows. */
  after: string | null;
};

/** The first figure in cell, as its parts. */
export function figureParts(cell: Element): FigureParts {
  const data = cell.querySelector("data");
  if (data === null) throw new Error(`no <data> in ${cell.outerHTML}`);
  let next: ChildNode | null = data.nextSibling;
  let gap: FigureParts["gap"] = null;
  if (next !== null && next.nodeType === 3 && (next.textContent ?? "").trim() === "") {
    gap = "space";
    next = next.nextSibling;
  } else if (next !== null && next.nodeType === 1 && (next as Element).tagName === "WBR") {
    gap = "wbr";
    next = next.nextSibling;
  }
  return { value: data.getAttribute("value"), data: data.textContent ?? "", gap, after: next !== null && next.nodeType === 1 ? (next.textContent ?? "") : null };
}
