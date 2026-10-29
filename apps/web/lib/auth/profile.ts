/**
 * Who signed in, as the ID token named them (H-042: the account menu shows
 * it to that person only): the OIDC profile and email claims, cleaned by the
 * BFF (lib/session/session.ts profileFromClaims). Either may be null. A type
 * only, so client modules can name it without reaching the session code.
 */
export type Profile = { name: string | null; email: string | null };
