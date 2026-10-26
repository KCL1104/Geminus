# ADR-016 — Frontend server-state boundary without a state library (T-014)

狀態：ACCEPTED（T-014 agent 選定；owner KCL1104 於 2026-10-15 同意）。日期：2026-10-15。原報告把 TanStack Query、Zustand、Tailwind/shadcn 列為 TBD（DECISIONS_AND_TBD.md「Frontend state / UI libraries」）。

2026-10-18 補記：瀏覽器如何取得 credentials 改由 [ADR-018](ADR-018-browser-session-and-oidc-provider.md)（BFF）決定：T-043 起 `lib/api/client.ts` 改呼叫同源 BFF（`credentials: 'same-origin'`、`X-Geminus-Request`），瀏覽器不持有 token、不再自行設 `Authorization`；下表其餘責任不變。2026-10-19（T-043）已實作：client 只呼叫同源的 `/api/v1/…`，`NEXT_PUBLIC_API_BASE_URL` 與 `lib/config.ts` 已移除；BFF 在 `lib/session`（只在伺服器端，`tests/server-only.test.ts`）。

2026-10-26 補記（T-043 後續：登入後的 UI bootstrap，H-029）：root layout 掛上 `app/_components/auth-bootstrap.tsx`。伺服器只告訴它 session cookie 是否打得開（`lib/session/server` 的 `hasSession`，只讀、不 refresh）與之前選過的 organization ID；瀏覽器經 BFF 呼叫 `GET /v1/me`（D-045），結果仍經 `lib/auth/session.tsx` 的同一個 context 提供（`session`、`role`，加上 `account`：bootstrap 的狀態與 choose／retry／signOut），沒有第二個 state model。organization 的選擇記在 browser session 的 cookie `geminus_organization`（只是 selector，D-008；`/me` 列出時才使用），以 `X-Organization-ID` 送出。見 checks/T-043_VALIDATION.md「後續：登入後的 UI bootstrap」。

## Decision

T-014 不引入 state 或 UI library，只用 React 19 與自寫的小型模組，因為邊界本身才是本任務要固定的東西：

| 模組 | 責任 |
|---|---|
| `lib/api/client.ts` | 唯一的 HTTP 出口：只呼叫同源 BFF 的 `/api/v1/…`；沒有 session 時不送 request；帶 `X-Organization-ID`（只是 selector，D-008）與 `X-Geminus-Request`，`credentials: 'same-origin'`，不持有也不送 token（T-043 前帶 `Authorization`、不送 cookie）；route 參數先過 D-001 ID 檢查；draft 回應必須有與 body revision 相符的 ETag |
| `lib/api/problem.ts` | 所有失敗收斂為 `Problem`；狀態碼對應與 schema `x-http-status` 由測試鎖定；錯誤碼與狀態不符、未知碼、非 API 錯誤 body 一律 `protocol`，不猜 |
| `lib/state/draft.ts` | remote（server 已存）／inFlight（已送出）／unsaved（本機）三者分開；409 或遠端較新 revision 進入 `conflict`，保留全部本機編輯，不自動 rebase；只有使用者明確 reload／discard 才離開 |
| `lib/state/selection.ts` | 2D／3D 共用的 `selectedEntityId`，只接受目前 IR 的 node／resource／flow ID（TEST-025 的前置） |
| `lib/auth/session.tsx` | 未掛上 provider 時一律未登入；沒有 demo／開發用 session。未知角色視為唯讀，但授權仍以 API 為準（2026-10-26 起由 auth bootstrap 以 `/me` 提供，見上方補記） |

## Consequences

- T-015（2D editor）若需要 query cache、背景重新整理或 optimistic update，再評估 TanStack Query 等；採用時仍須經由 `lib/api/client.ts` 與 `lib/state/draft.ts` 的規則（不得繞過 If-Match、不得自動 rebase）。
- `useRemote` 只做「一次載入、捨棄過期回應」；沒有 cache、重試或 polling。
- 真實登入（OIDC）與 membership 角色來源屬 T-008／T-010（D-008、CB-17）。在那之前所有頁面都顯示「需要登入」，這是預期行為而不是缺陷。
