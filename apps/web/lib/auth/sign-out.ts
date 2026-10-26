// Sign-out from the page (T-043 follow-up): POST /auth/logout, where the BFF
// clears the session cookie, revokes the refresh token and answers with the
// provider's logout URL (RP-initiated logout, ADR-018); the page then goes
// there. The request carries the header the BFF's CSRF check asks for.
import { networkProblem, type Problem, problemFromResponse } from "../api/problem";

export type SignOutDeps = {
  fetch: typeof fetch;
  /** Leaves the page for url (location.assign in the app). */
  navigate: (url: string) => void;
  /** Forgets what the page remembered for the session (the organization). */
  forget: () => void;
};

/** The provider's logout URL in the BFF's answer, or the start page when it has none that is http(s). */
function destination(body: unknown): string {
  const redirect = typeof body === "object" && body !== null ? (body as { redirect?: unknown }).redirect : undefined;
  if (typeof redirect !== "string") return "/";
  try {
    const url = new URL(redirect);
    return url.protocol === "https:" || url.protocol === "http:" ? url.toString() : "/";
  } catch {
    return "/";
  }
}

/** Ends the session; null once the page is on its way out, else the problem (the session may still be there). */
export async function signOut(deps: SignOutDeps): Promise<Problem | null> {
  let res: Response;
  let body: unknown;
  try {
    res = await deps.fetch("/auth/logout", {
      method: "POST",
      headers: { Accept: "application/json", "X-Geminus-Request": "1" },
      credentials: "same-origin",
      cache: "no-store",
      redirect: "error",
    });
    const text = await res.text();
    try {
      body = text === "" ? undefined : JSON.parse(text);
    } catch {
      body = undefined;
    }
  } catch {
    return networkProblem("The sign-out request did not complete.");
  }
  if (!res.ok) return problemFromResponse(res.status, res.headers, body);
  deps.forget();
  deps.navigate(destination(body));
  return null;
}
