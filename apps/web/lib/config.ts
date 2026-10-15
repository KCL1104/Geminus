// Deployment configuration read by the browser. Missing values mean "not
// configured", which every caller treats as unavailable (fail closed).
export function apiBaseUrl(): string | null {
  const v = process.env.NEXT_PUBLIC_API_BASE_URL;
  return v === undefined || v.trim() === "" ? null : v.trim();
}
