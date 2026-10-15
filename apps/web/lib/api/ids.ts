// D-001: server IDs are <prefix>_<ULID>. Route parameters come from the URL
// bar, so they are checked before they become part of an API path.
const ULID = "[0-7][0-9A-HJKMNP-TV-Z]{25}";

export type IDPrefix = "org" | "usr" | "prj" | "mdl" | "mv" | "ds" | "dsv" | "imp" | "aip" | "scn" | "sim" | "ana" | "job" | "aud" | "req" | "art";

const patterns = new Map<IDPrefix, RegExp>();

export function isID(prefix: IDPrefix, value: string): boolean {
  let re = patterns.get(prefix);
  if (re === undefined) {
    re = new RegExp(`^${prefix}_${ULID}$`);
    patterns.set(prefix, re);
  }
  return re.test(value);
}
