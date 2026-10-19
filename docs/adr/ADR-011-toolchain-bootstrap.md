# ADR-011 — Toolchain / repository bootstrap versions

狀態：PROPOSED（T-002 agent 選定，待人類 review；不影響 ADR-001–010 的 SOURCE 邊界）。日期：2026-10-15。

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
| 瀏覽器檢查（T-045） | `@playwright/test` 1.56.1、`@axe-core/playwright` 4.13.0、`axe-core` 4.13.0（`apps/web-e2e`，不進 `apps/web`）；CI 的 e2e job 用 `mcr.microsoft.com/playwright:v1.56.1-noble`，以 index digest `sha256:f1e7e010…b837a` pin | Apache-2.0；axe 為 MPL-2.0（只用於測試，H-009） | 截圖 baseline 只在這個 image 中產生與比對（字型一致） |

HTTP 層依 ADR-001 使用 `net/http`，不引入 web framework。SQL generation（sqlc／explicit SQL）、frontend state／UI libraries、auth、cloud、object storage 仍為 TBD，本 ADR 不選。LLM provider 已由 H-001 指定 Anthropic，但 SDK（`github.com/anthropics/anthropic-sdk-go`）版本留給 T-024 選定。

## Consequences

- `make backend-*` 只涵蓋 `./cmd/... ./internal/...`，避免 npm 套件內附的 Go 檔被當成本 module。新增 Go 頂層目錄時需更新 `GO_PKGS`。
- `next build` 會改寫 `tsconfig.json`（加入 `esModuleInterop`、`.next/dev/types`）；已提交改寫後內容，避免 build 弄髒工作樹。
- `npm audit`（含 dev）有 5 high，皆為 dev-only lint 鏈上的 `braces` GHSA-vfj7-8cjw-p6xm，目前無修補版本；production dependencies 為 0。2026-10-15 owner 接受此 dev-only 風險（D-031）；CI frontend job 以 `npm audit --omit=dev --audit-level=high` 阻擋 production 依賴的 high/critical advisory，upstream 有修補即升級。
- 版本升級必須更新本 ADR、lockfile 並重跑 `make spec-check backend-test backend-race frontend-test frontend-build`。
