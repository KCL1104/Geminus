// H-073 (4): the element ID of a metrics table row, so the AI explanation's
// evidence can link to it (evidence-link.tsx). Every character but A–Z,
// a–z, 0–9 and "-" is written as _<hex code point>_: safe in an address, and
// no two metric paths share one.
export function metricRowId(path: string): string {
  return `metric-${path.replace(/[^A-Za-z0-9-]/gu, (c) => `_${c.codePointAt(0)!.toString(16)}_`)}`;
}
