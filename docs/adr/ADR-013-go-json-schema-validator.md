# ADR-013 — Go JSON Schema validator and embedded schemas

狀態：PROPOSED（T-004 agent 選定，待人類 review）。日期：2026-10-15。

## Decision

Go 伺服器的 schema 階段使用 `github.com/santhosh-tekuri/jsonschema/v6` v6.0.3（Apache-2.0，2026-06-28），依賴 `golang.org/x/text` v0.42.0（BSD-3-Clause，2026-09-08）。`schemas/embed.go` 以 `go:embed` 把 `schemas/*.schema.json` 編進 binary，validator 以各檔 `$id` 註冊資源、拒絕載入任何其他 URL，因此驗證不讀檔案系統也不連網，且永遠對同一份 source of truth 驗證。`AssertFormat()` 開啟，`date-time` 等 format 與 Python spec checks 一樣強制。

## 選擇理由

- 支援 JSON Schema 2020-12，以 `$id` 解析跨檔 `$ref`，不需改寫 schemas。
- 錯誤樹帶 instance location，可產生精確 JSON Pointer；`oneOf` 錯誤只保留 discriminator（`type`／`op`）相符的分支，沒有相符時只報 discriminator 允許值。
- 函式庫的部分錯誤訊息依 Go map 順序列出欄位名，fuzz 測試因此抓到非決定性；`AdditionalProperties` 與 `Required` 改由本專案以排序後的名單組訊息。

## Consequences

- 初版帶入的 `golang.org/x/text` v0.14.0 經 `govulncheck v1.9.0` 掃出兩個模組層級漏洞（GO-2026-6629、GO-2026-5970，程式未呼叫到），已升級至 v0.42.0 並重掃為 0。CI backend job 執行 govulncheck，新依賴出現已知漏洞時會失敗。
- `make backend-*` 的 `GO_PKGS` 加入 `./schemas/...`。
- Go 與 Python 兩套 validator 對共用 fixtures 的結論由 `TestAgreesWithSharedFixtures` 比對；任一實作改變行為都會讓測試失敗。
