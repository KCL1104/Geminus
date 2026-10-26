// What the AI wrote into a proposal, as a person can see it (T-031 review
// round 1, M1). Names and values in its patch, and its prose, are data the
// server does not restrict to visible characters (an IR name is 1 to 200 of
// any characters). A character that shows nothing, joins or reorders the
// text around it (a bidi control such as U+202E, a zero-width space, a tag
// character, a control, a separator, a filler) could make a real change
// read as no change, or make the page say what the patch does not; here it
// becomes a visible ⟨U+XXXX⟩ instead. The text is never parsed as markup:
// the caller renders it as a text node (in a <bdi> for a name or value, so
// it cannot reorder the words around it either).

const marker = (c: string) => `⟨U+${c.codePointAt(0)!.toString(16).toUpperCase().padStart(4, "0")}⟩`;

// In a name or value: controls (tab and newline too: a name is one line),
// format characters (bidi controls, zero-width characters, the BOM, soft
// hyphen, tags), lone surrogates, line and paragraph separators,
// default-ignorable characters (fillers, variation selectors, the combining
// grapheme joiner), the braille blank, and every space but U+0020.
const HIDDEN_IN_NAME = /[\p{Cc}\p{Cf}\p{Cs}\p{Zl}\p{Zp}\p{Default_Ignorable_Code_Point}⠀]|[^\P{Zs} ]/gu;
// In prose (a summary, an assumption, a question): what reorders or hides
// text; its line breaks, tabs and spaces are the text's own.
const HIDDEN_IN_PROSE = /(?![\t\n\r])[\p{Cc}\p{Cf}\p{Cs}\p{Zl}\p{Zp}]/gu;
// Inside the exact JSON Patch's strings (its own layout is ASCII spaces and
// line breaks; JSON.stringify already escapes U+0000–U+001F and lone
// surrogates): the rest of what a name hides.
const HIDDEN_IN_JSON = /[\u007F-\u009F\p{Cf}\p{Cs}\p{Zl}\p{Zp}\p{Default_Ignorable_Code_Point}⠀]|[^\P{Zs} ]/gu;

/** The text with every character that shows nothing or reorders text as a visible ⟨U+XXXX⟩. */
export function visible(text: string, kind: "name" | "prose" = "name"): string {
  return text.replace(kind === "name" ? HIDDEN_IN_NAME : HIDDEN_IN_PROSE, marker);
}

/**
 * Two names with one key read the same to a person: compatibility forms
 * (full-width letters, ligatures), letter case, spacing and characters that
 * show nothing set aside.
 */
export function nameKey(name: string): string {
  return name
    .normalize("NFKC")
    .replace(/[\s\p{Z}]+/gu, " ")
    .replace(/[\p{Cc}\p{Cf}\p{Cs}\p{Default_Ignorable_Code_Point}⠀]/gu, "")
    .replace(/ {2,}/g, " ")
    .trim()
    .toLowerCase();
}

/** Two texts as shown read alike: the same once canonically and compatibly equivalent forms are one. */
export function readsAlike(a: string, b: string): boolean {
  return a.normalize("NFKC") === b.normalize("NFKC");
}

const escape = (c: string) => [...Array(c.length).keys()].map((i) => `\\u${c.charCodeAt(i).toString(16).padStart(4, "0")}`).join("");

/** A value as JSON in ASCII: every other character escaped, so two different values never read alike. */
export function asciiJson(v: unknown): string {
  return (JSON.stringify(v) ?? "null").replace(/[^\x20-\x7e]/g, escape);
}

/** The exact JSON Patch as JSON: the same value, with what would hide or reorder its text escaped. */
export function patchJson(v: unknown): string {
  return (JSON.stringify(v, null, 2) ?? "").replace(HIDDEN_IN_JSON, escape);
}
