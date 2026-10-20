# ADR-011 — Toolchain / repository bootstrap versions

狀態：ACCEPTED（2026-10-19，owner H-010；T-002 agent 選定；不影響 ADR-001–010 的 SOURCE 邊界）。日期：2026-10-15。

## Decision

| 工具 | 版本 | License | 選擇理由／查核 |
|---|---|---|---|
| Go | 1.27.2（`go.mod`：`go 1.27.0`、`toolchain go1.27.2`） | BSD-3-Clause | go.dev/dl 目前只列 1.27.x、1.26.x 為受支援版本；環境預裝 1.24.7 已 EOL。1.27 支援期涵蓋 16–20 週計畫 |
| Node.js | 24.21.0 LTS (Krypton)；`apps/web/.nvmrc`、`engines >=24.21.0 <25`、`engine-strict` | MIT | 22.x 於 2027-04-30 EOL，接近 Beta 時程；24.22.0 為前一日發布且非 security release，故取 24.21.0 |
| npm | 隨 Node 24.21.0（11.19.0）；lockfile `apps/web/package-lock.json` | Artistic-2.0 | `npm ci` 重現 |
| Next.js | 16.4.1 | MIT | 16.4.1 與 15.5.28、React 四條線同日修補，推定為 security backport；`npm audit` 對 16.4.0 與 16.4.1 當下皆 0 advisory。**風險：發布僅 1 天** |
| React / React DOM | 19.3.1 | MIT | 同上 |
| TypeScript | 6.0.3 | Apache-2.0 | typescript-eslint 8.71.2 peer `<6.1.0`，TS 7 不相容 |
| ESLint | 10.12.0 + eslint-config-next 16.4.1 | MIT | ESLint 9 已被標為 unsupported。`eslint-plugin-react`/`-import`/`-jsx-a11y` 的 peer range 尚未宣告 v10；以 `tests/lint-guards.test.ts` 驗證三者規則實際執行，相容性失效時測試會失敗 |
| Vitest | 5.0.4 | MIT | 需 Node ^22.12 或 ^24 |
| Python（spec checks） | 3.13；`requirements-spec.txt` 精確 pin | PSF | 新增 `rfc3339-validator`、`rfc3986-validator`（見 CB-34） |
| PostgreSQL（CI smoke） | 16.15（`postgres:16.15` service） | PostgreSQL | **只用於草案 migration smoke**；production DB major 仍是 TBD，不構成選型 |
| GitHub Actions | checkout v7.0.1、setup-go v7.0.0、setup-node v7.1.0、setup-python v7.0.0、upload-artifact v7.0.1（T-045；v7.0.2 於 2026-10-07 發布，未滿兩週），以 commit SHA pin | MIT | 防止 tag 被移動 |
| OIDC client（T-043） | `openid-client` 6.8.8，帶入 `oauth4webapi` 3.8.8 與 `jose` 6.2.12（三者皆 2026-09-05 發布，滿兩週） | MIT | ADR-018 指定通用 OIDC／OAuth client。只在伺服器端使用（`apps/web/lib/session`，`tests/server-only.test.ts` 檢查沒有 client module 能引用）；ID token 另以 `enableNonRepudiationChecks` 驗簽 |
| 瀏覽器檢查（T-045） | `@playwright/test` 1.56.1、`@axe-core/playwright` 4.13.0、`axe-core` 4.13.0（`apps/web-e2e`，不進 `apps/web`）；CI 的 e2e job 用 `mcr.microsoft.com/playwright:v1.56.1-noble`，以 index digest `sha256:f1e7e010…b837a` pin | Apache-2.0；axe 為 MPL-2.0（只用於測試，H-009） | 截圖 baseline 只在這個 image 中產生與比對（字型一致） |

HTTP 層依 ADR-001 使用 `net/http`，不引入 web framework。SQL generation（sqlc／explicit SQL）、frontend state／UI libraries、auth、cloud、object storage 仍為 TBD，本 ADR 不選。LLM provider 已由 H-001 指定 Anthropic，但 SDK（`github.com/anthropics/anthropic-sdk-go`）版本留給 T-024 選定。T-024（2026-10-19，ADR-027 ACCEPTED，H-019）：`github.com/anthropics/anthropic-sdk-go` **v1.78.0**（2026-09-30 發布，滿兩週；v1.79.0／v1.79.1／v1.80.0 為 2026-10-07～09 發布，未滿兩週），MIT；`go.mod`／`go.sum` pin。只有 `internal/ai/provider/anthropic` 連結它（architecture rule `llm-sdks-only-in-ai`）；它連帶的 module 與授權列在 [THIRD_PARTY_NOTICES](../THIRD_PARTY_NOTICES.md)。尚未以它呼叫真實 API（live AI 待 owner 核准 model、quota、資料政策，H-017）。**接受的 transitive pre-release**（T-024 review L7）：SDK 經 `github.com/pb33f/ordered-map/v2` v2.3.1（其 `yaml.go`）帶入 `go.yaml.in/yaml/v4` **v4.0.0-rc.2**（2025-08-28 發布，`go.sum` pin）。這是 release candidate，不是穩定版；SDK v1.78.0 的依賴圖沒有不含它的選擇，所以接受。風險：rc 的 API 與解析行為可能在正式版前改變，安全修補可能只出在之後的版本；本 module 不做 YAML 編解碼（adapter 的 request／response 都是 JSON），所以只是被連結、沒有被呼叫的程式。升級 SDK 時重查此依賴，`go.yaml.in/yaml/v4` 有正式版即升級。

## Consequences

- `make backend-*` 只涵蓋 `./cmd/... ./internal/...`，避免 npm 套件內附的 Go 檔被當成本 module。新增 Go 頂層目錄時需更新 `GO_PKGS`。
- `next build` 會改寫 `tsconfig.json`（加入 `esModuleInterop`、`.next/dev/types`）；已提交改寫後內容，避免 build 弄髒工作樹。
- `npm audit`（含 dev）有 5 high，皆為 dev-only lint 鏈上的 `braces` GHSA-vfj7-8cjw-p6xm，目前無修補版本；production dependencies 為 0。2026-10-15 owner 接受此 dev-only 風險（D-031）；CI frontend job 以 `npm audit --omit=dev --audit-level=high` 阻擋 production 依賴的 high/critical advisory，upstream 有修補即升級。
- 版本升級必須更新本 ADR、lockfile 並重跑 `make spec-check backend-test backend-race frontend-test frontend-build`。
