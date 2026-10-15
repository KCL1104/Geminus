# T-014 驗證報告：Frontend app／server-state boundary

**日期：2026-10-15｜狀態：`review`（待 owner 驗收）｜需求：REQ-004、REQ-020｜Gate：無**

逐條輸出在 `checks/evidence/2026-10-15-T-014/`（`summary.tsv` 為 exit code 總表）。本任務只建立邊界與狀態呈現，沒有視覺設計（「不得先做漂亮 UI」）。

## 交付內容

| 項目 | 內容 |
|---|---|
| `lib/api/client.ts` | 唯一 HTTP 出口；無 API URL 或 session 時不送 request（fail closed）；帶 Bearer 與 `X-Organization-ID`，`credentials: "omit"`；route 參數先過 D-001 ID 檢查；draft 讀寫要求 ETag 與 body revision 相符；PATCH 帶 `If-Match` 與 `application/json-patch+json` |
| `lib/api/problem.ts` | 401／403／404／409／400·413·415·422／428／429／500／503 與 network、protocol 的單一 union；狀態碼對應以測試鎖定等於 schema `x-http-status`；未知碼、碼與狀態不符、非 API body 一律 `protocol`；request ID 只收合法 `req_` ULID |
| `lib/state/draft.ts` | remote／inFlight／unsaved 分開；409 或較新的遠端 revision → `conflict`，保留全部編輯、不自動 rebase；save 進行中不接受 refresh 與 discard；clean、current、無 error 才可 publish（D-028） |
| `lib/state/selection.ts`、`use-selection.ts` | 2D／3D 共用 `selectedEntityId`，只接受目前 IR 的 node／resource／flow ID，實體消失即清除 |
| `lib/auth/session.tsx` | 未掛 provider 即未登入，沒有 demo session；未知角色唯讀 |
| `app/` | `/projects`、`/projects/[projectId]`、`/models/[modelId]`；`ProblemView`、`Loading`、`RequireSession`、`RemoteView`、`ReadOnlyNotice`、`DraftStatus`（conflict 時給 editor「重新載入／捨棄」兩個選擇） |
| 文件 | ADR-016（PROPOSED：不引入 state library）、ARCHITECTURE §7 |

## 實際執行結果

| 指令 | Exit | 說明 |
|---|---|---|
| `make spec-check` | 0 | |
| `make frontend-test` | 0 | lint（`--max-warnings=0`）、typecheck、unit |
| `vitest run --reporter=verbose` | 0 | 5 個檔案、53 個 test PASS |
| `make frontend-build` | 0 | Next.js production build；`/projects` 靜態，`/projects/[projectId]`、`/models/[modelId]` 動態 |
| `npm audit --omit=dev --audit-level=high` | 0 | found 0 vulnerabilities（未新增依賴） |
| Frontend mutation（scratch 複本，`vitest` + `tsc`） | 0 | 19/19 被擋 |
| Browser smoke（Chromium，production build） | 0 | 5 個路徑皆顯示「Sign in required」；**0 個離開本站的 request**；0 個 console error／4xx |
| `make backend-test` | 0 | 未改 Go，確認無回歸 |
| `actionlint v1.7.12` | 0 | |

Browser smoke 用 scratch 安裝的 `playwright-core` 1.63.0 與預裝 Chromium，不是 repo 依賴；腳本保存在 evidence 目錄。第一次執行發現 `/favicon.ico` 404 的 console error，補上 `app/icon.svg` 後重跑為 0。

## 驗收測試對照

- **TEST-009**（tenant／role matrix）前端部分：403 顯示權限說明；404 對「不存在」與「其他 tenant」顯示相同內容，且不回顯 server 訊息；viewer 與未知角色沒有任何編輯按鈕；未登入時頁面不呼叫 API。完整 read／write／list／download／SSE 的權限矩陣需 API（T-009、T-013），**NOT RUN**。
- **TEST-025**（2D/3D adapters）前置部分：共用 selection store 只保存 IR ID、拒絕不存在的 ID、實體消失即清除。實際 2D／3D adapter 與「3D 不能寫 IR」屬 T-015／T-017，**NOT RUN**。
- Scope「區分 remote server state、saved draft 與 unsaved editor state」：`draft.ts` 單元測試加 2,000 組隨機事件序列的性質測試（編輯不遺失、不重複、不重排；save 永遠針對當前 remote revision；clean 時沒有待存編輯）。

## 證明測試會失敗（mutation）

19 個 mutant 全部被擋：client 忽略缺少的 session、送 cookie、漏 tenant header、漏 If-Match、信任 route ID、不檢查 draft ETag 與 revision；problem 接受碼與狀態不符、信任任意 request ID；ETag 接受 weak；ID 不分大小寫；draft 自動 rebase、409 丟棄編輯、conflict 中仍可 save、dirty 可 publish；selection 接受未知 ID；viewer 可編輯；404 回顯 server 訊息；未登入仍顯示內容；loader 顯示一半資料。

## 開發中發現並修正

1. React compiler lint 擋下「render 中讀 ref」與「effect 內同步 setState」：client 改為依 session 重建，`useRemote` 改以每次載入的 token 判斷是否為過期回應。
2. Draft 狀態機初版在 save 進行中收到較新 revision 會誤判 conflict（可能是自己的 save），也允許此時 discard；改為由 save 的回應決定。
3. `/favicon.ico` 404 造成 console error（見上）。

## 剩餘風險

| 風險 | 說明 |
|---|---|
| `useRemote` 的過期回應丟棄 | 邏輯簡單但沒有 DOM 測試（未引入 jsdom／testing-library）；完整 browser 流程待 e2e（T-040） |
| 成功回應只做型別斷言 | 2xx body 未在瀏覽器做 JSON Schema 驗證，依賴 API 契約測試（T-013） |
| 無登入能力 | 目前所有頁面都是未登入狀態；這是 fail-closed 的預期結果，真實 session 待 T-008 |

## NOT RUN／延後

| 項目 | 原因 |
|---|---|
| 對真實 API 的頁面流程、權限矩陣 | API 尚未實作（T-013） |
| 2D／3D adapter 與互選 | T-015／T-017 |
| E2E closed loop | T-040 |
| GitHub Actions | 推送後補記 |
