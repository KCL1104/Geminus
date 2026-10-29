# ADR-016 — Frontend server-state boundary without a state library (T-014)

Status: ACCEPTED (chosen by the T-014 agent; owner KCL1104 agreed on 2026-10-15). Date: 2026-10-15. The original report lists TanStack Query, Zustand and Tailwind/shadcn as TBD (DECISIONS_AND_TBD.md "Frontend state / UI libraries").

Addendum of 2026-10-18: how the browser obtains credentials is now decided by [ADR-018](ADR-018-browser-session-and-oidc-provider.md) (BFF). From T-043 on, `lib/api/client.ts` calls the same-origin BFF instead (`credentials: 'same-origin'`, `X-Geminus-Request`); the browser holds no token and no longer sets `Authorization` itself; the other responsibilities in the table below do not change. Implemented on 2026-10-19 (T-043): the client calls only the same-origin `/api/v1/…`; `NEXT_PUBLIC_API_BASE_URL` and `lib/config.ts` have been removed; the BFF is in `lib/session` (server side only, `tests/server-only.test.ts`).

Addendum of 2026-10-26 (T-043 follow-up: the UI bootstrap after sign-in, H-029): the root layout mounts `app/_components/auth-bootstrap.tsx`. The server only tells it whether the session cookie can be opened (`hasSession` of `lib/session/server`, read only, no refresh) and the organization ID chosen before; the browser calls `GET /v1/me` (D-045) through the BFF, and the result is still provided through the same context of `lib/auth/session.tsx` (`session`, `role`, plus `account`: the bootstrap's state and choose/retry/signOut); there is no second state model. The organization choice is kept in the browser session's cookie `geminus_organization` (only a selector, D-008; used only when `/me` lists it) and sent as `X-Organization-ID`. See the section 「後續：登入後的 UI bootstrap」 ("Follow-up: the UI bootstrap after sign-in") of checks/T-043_VALIDATION.md.

## Decision

T-014 introduces no state or UI library and uses only React 19 and small self-written modules, because the boundary itself is what this task has to fix:

| Module | Responsibility |
|---|---|
| `lib/api/client.ts` | The only HTTP exit: calls only the same-origin BFF's `/api/v1/…`; sends no request when there is no session; carries `X-Organization-ID` (only a selector, D-008) and `X-Geminus-Request`, `credentials: 'same-origin'`, and neither holds nor sends a token (before T-043 it carried `Authorization` and sent no cookie); route parameters pass the D-001 ID check first; a draft response must have an ETag that matches the body's revision |
| `lib/api/problem.ts` | All failures converge into `Problem`; the status code mapping and the schema's `x-http-status` are locked by tests; an error code that does not match its status, an unknown code, or a non-API error body is always `protocol`, never guessed |
| `lib/state/draft.ts` | remote (saved on the server) / inFlight (sent) / unsaved (local) are kept apart; a 409 or a newer remote revision enters `conflict`, keeping all local edits, with no automatic rebase; it is left only when the user explicitly reloads/discards |
| `lib/state/selection.ts` | The `selectedEntityId` shared by 2D/3D, accepting only node/resource/flow IDs of the current IR (a precondition of TEST-025) |
| `lib/auth/session.tsx` | Without a mounted provider, always signed out; there is no demo/development session. An unknown role is treated as read only, but authorization is still decided by the API (from 2026-10-26 provided by the auth bootstrap from `/me`, see the addendum above) |

## Consequences

- If T-015 (2D editor) needs a query cache, background refresh or optimistic updates, TanStack Query or similar is evaluated then; if adopted, it must still go through the rules of `lib/api/client.ts` and `lib/state/draft.ts` (no bypassing If-Match, no automatic rebase).
- `useRemote` only does "load once, discard stale responses"; no cache, retry or polling.
- Real sign-in (OIDC) and the source of membership roles belong to T-008/T-010 (D-008, CB-17). Until then every page shows "sign-in required"; this is expected behaviour, not a defect.
