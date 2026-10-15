// Draft ETags are strong and name the revision: "draft-revision-12"
// (openapi/openapi.yaml components DraftETag / IfMatch).
const DRAFT_ETAG = /^"draft-revision-([1-9][0-9]{0,15})"$/;

export function draftETag(revision: number): string {
  if (!Number.isSafeInteger(revision) || revision < 1) throw new RangeError(`invalid draft revision ${revision}`);
  return `"draft-revision-${revision}"`;
}

/** The revision an ETag names, or null when it is not a draft ETag (weak ETags included). */
export function parseDraftETag(etag: string): number | null {
  const m = DRAFT_ETAG.exec(etag);
  if (m === null || m[1] === undefined) return null;
  const n = Number(m[1]);
  return Number.isSafeInteger(n) ? n : null;
}
