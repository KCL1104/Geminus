# T-001 / T-002 Bootstrap 驗證報告

**日期：2026-10-15｜分支：`claude/lucid-meitner-vq1awo`｜結論：T-001／T-002 交付物已實跑通過列出的檢查，狀態為 `review`（待人類驗收），不是 `done`。產品功能仍未實作。**

原交付包的檢查紀錄保留在 [VALIDATION_REPORT.md](VALIDATION_REPORT.md)；本檔只記錄在 repository 內實際重跑的結果。逐條指令輸出在 `checks/evidence/2026-10-15/`（`summary.tsv` 為 exit code 總表）。

## 環境

Go 1.27.2（`go.mod` toolchain；預裝 1.24.7 已 EOL，由 `GOTOOLCHAIN=auto` 下載並經 Go checksum DB 驗證）、Node 24.21.0（官方 tarball，SHA256SUMS 驗證）、npm 11.19.0、Python 3.13 venv（`requirements-spec.txt`）、PostgreSQL 16.15（一次性 cluster，只監聽 unix socket）、linux/amd64。詳見 `evidence/2026-10-15/environment.txt` 與 [ADR-011](../docs/adr/ADR-011-toolchain-bootstrap.md)。

## 實際執行結果

| 指令 | Exit | 說明 |
|---|---|---|
| `make spec-check`（handoff 原狀、乾淨 venv） | **2（FAIL）** | `invalid-timestamp-format` 反例被接受：缺 `rfc3339-validator` 時 jsonschema 靜默略過 `date-time`（CB-34） |
| `make spec-check`（修正 pins 後） | 0 | 另新增 `spec-decisions`；以舊 pins 重跑會明確失敗：`No format checker installed for ['date-time', 'uri']` |
| `make spec-check`（加入 README 連結、本檔尚未存在時） | 2 | docs link check 正確抓到斷連結；建立本檔後重跑為 0（見 `spec-check.log`） |
| `make backend-test` | 0 | gofmt gate + `go vet` + `go test`（cmd/api、cmd/worker、internal/config） |
| `make backend-race` | 0 | `go test -race` |
| `make backend-build` | 0 | `bin/api`、`bin/worker` |
| `npm ci --prefix apps/web` | 0 | lockfile 重現安裝；Node 22 下 `npm ci` 以 `EBADENGINE` 失敗（engine lock 生效） |
| `make frontend-test` | 0 | ESLint（`--max-warnings=0`）、`tsc --noEmit`、Vitest 11 tests |
| `make frontend-build` | 0 | `next build`，僅靜態 `/` 與 `/_not-found` |
| `make db-test`（`DT_ALLOW_DB_TESTS=yes`、fresh `dt_test_t002`） | 0 | bootstrap roles → 0001/0002 up → invariants → down → up → invariants |
| `make db-test`（未設 guard） | 2 | runner 拒絕寫入 |
| `make integration-test` / `e2e-test` / `ai-eval` / `ai-eval-live` / `security-scan` | 2 | 仍為 NOT IMPLEMENTED，未改成假成功 |
| `bin/api`（無設定） | 1 | `DATABASE_URL (required)` |
| `bin/api`（`AI_PROVIDER=anthropic`） | 1 | `not_implemented`：T-024、D-018、資料政策、quota 未完成前拒絕 live AI |
| `bin/api`、`bin/worker`（有效設定） | 2 | 結構化 log `NOT IMPLEMENTED … (T-013 / T-019)`；log 不含 DB 密碼 |
| `npm audit --omit=dev` | 0 | production dependencies 0 vulnerabilities |
| `npm audit`（含 dev） | **1** | 5 high，同一 advisory：`braces` GHSA-vfj7-8cjw-p6xm（巢狀 glob pattern 造成 stack exhaustion DoS），只經 dev-only lint 鏈 `eslint-config-next → @next/eslint-plugin-next → fast-glob → micromatch → braces`；`braces *` 無修補版本，`npm audit fix --force` 會降到 eslint-config-next 14。**未接受此風險，待人類決定** |
| GitHub Actions [run 38720779374](https://github.com/KCL1104/Geminus/actions/runs/38720779374)（commit `4973718`，ubuntu-24.04） | success | spec-check（含 committed reports 與重跑一致）、backend（vet/test/race/build）、frontend（npm ci/lint/typecheck/unit/build）、db migration smoke（`postgres:16.15` service）四個 job 全部 success |
| `actionlint v1.7.12 .github/workflows/ci.yml` | 0 | workflow 語法與 expression 檢查 |
| `openapi-spec-validator 0.7.2`（scratch venv，非 repo 依賴） | 0 | `openapi.yaml: OK`；只證明結構，未證明對 `https://twin.example/...` `$id` 的跨檔 `$ref` 深度解析（D-015 SDK 選型仍需驗證） |

## 證明測試不是空殼（mutation / negative checks）

- Go：以 `go test -overlay` 注入「靜默接受 anthropic」與「錯誤訊息帶出設定值」兩個 mutant，分別被 `TestLoadRejects/anthropic_not_yet_implemented`、`TestErrorsNeverEchoSecrets` 擋下。
- Frontend：關閉 `no-restricted-imports` 後 5 個 forbidden-import 測試失敗；真實檔案 `import OpenAI from "openai"` 使 `make frontend-test` 的 lint 失敗；型別錯誤使 `typecheck` exit 2。
- DB：只套 0001 不套 0002 → invariants 失敗；在 0002 後關閉 `twin.projects` RLS → `unscoped SELECT leaked projects`。
- Decision register：自行把 D-001 改 approved（無 approver）、proposed 帶 approver、done 無 evidence、done 但 gate 未批准，四種情況 `check_decisions.py` 皆 exit 1。

## 探測型檢查（T-001 盤點用，不是 T-006/T-007 證據）

在一次性 DB 上實測 [CONTRACT_BLOCKERS.md](../docs/CONTRACT_BLOCKERS.md) 的兩項：CB-13 `twin_api` 對自己 organization row 的 `SELECT … FOR UPDATE` 回 `permission denied for table organizations`；CB-14 `'tenant/orgXa/…' LIKE 'tenant/org_a/%'` 為 true、`starts_with` 為 false。DB smoke 通過不代表 D-013 migration review 完成，也不取代 runtime-login、pool reuse、併發 approval／lease 測試。

## NOT RUN

| 項目 | 原因／下一個 gate |
|---|---|
| `go test -race` 以外的平台（arm64、macOS、Windows） | 本環境只有 linux/amd64 |
| Live AI、token 成本、provider key 驗證 | AI_PROVIDER 只允許 disabled；未呼叫 Anthropic API，也未以使用者提供的 key 發出任何請求 |
| Integration / E2E / security scan / performance / backup-restore / staging | 對應 task（T-013、T-019、T-036–T-041）尚未開始 |
| 完整 OpenAPI SDK codegen 與外部 `$ref` 相容性 | T-003 / D-015 |
| en_US collation 排序差異（CB-15） | 本環境未安裝 en_US locale |
