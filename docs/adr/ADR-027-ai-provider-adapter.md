# ADR-027 — AI provider adapter 與 sanitized context（T-024）

狀態：PROPOSED（2026-10-19，T-024 agent 提出並實作，待獨立 review 與 owner）。依據：H-001、H-017、D-002、D-016、D-018、D-046（皆 APPROVED）、AI_SPEC §2–§4、§9–§10、SECURITY §7、AGENTS.md §2。本 ADR 不批准任何 live AI 呼叫：model、token quota、資料政策仍待 owner（H-001、H-017）。

## Context

T-024 要交付 provider interface（fake／disabled／live 三種明確 mode）、tenant-safe context builder（最小 context、bounded summary、credential redaction、跨 tenant hard filter、UNTRUSTED_DATA 與 system rules 分離、可見的 omission、D-046 ID pool）、token／timeout／retry 預算、provider schema projection，以及 `output_locale`。H-001 指定 Anthropic Claude API；D-018 決定兩個獨立 organization 的 key 以有序 primary→secondary failover 使用；H-017 指定變數名稱 `GEMINUS_ANTHROPIC_API_KEY_A`／`_B`，並說明環境中沒有 key、live AI 的 gate 不變。

## Decision

### Package 與邊界

| Package | 內容 | 依賴限制（`scripts/check-architecture`） |
|---|---|---|
| `internal/ai/provider` | `LLMProvider`（AI_SPEC §2 的三個方法）、sealed `Prompt`、`Client`（所有 mode 共用的檢查）、`Backend`、`Disabled`、typed `Error`、`Budget`、provider output 型別 | `ai-suggests-only`：不得依賴任何 application layer（storage、tenant、models、runs、worker、httpapi、api、auth、config、packs…）、DB driver、`database/sql`、`os/exec`；`ai-is-pure-but-the-adapter`：不得有網路、DB、process I/O 或 LLM SDK，自己 import 的標準函式庫只能是 kernel 的計算類 allowlist，第三方只能是 kernel 的 allowlist |
| `internal/ai/provider/fake` | 確定性 fake backend | 同上 |
| `internal/ai/provider/anthropic` | live backend（官方 Go SDK）與 schema projection | `ai-suggests-only`；唯一可依賴 LLM SDK 的 package（`llm-sdks-only-in-ai` 改為只豁免它與 cmd）。依賴它的 package 也就依賴 SDK，所以只有 composition root（cmd）能組裝 live backend |
| `internal/ai/aicontext` | context builder（proposal、analysis、mapping） | 同 provider |
| `internal/ai/idpool` | D-046 pool 的產生、形狀檢查、candidate 檢查 | 同 provider |
| `internal/ai/locale` | `output_locale` 驗證與 provider instruction、prose 數字檢查 | 同 provider |
| `internal/ai/corpus` | `make ai-eval` 的 fake-provider corpus 測試（只有測試） | 同 provider |
| `prompts`（`prompts/embed.go`） | 嵌入版本化的 system prompt | — |
| `internal/secret` | `Secret`：任何 fmt verb、JSON、slog 都印 `[redacted]` | — |

tasks.json 原規劃的 `internal/ai/context/` 改名為 `internal/ai/aicontext`（`context` 會遮蔽標準函式庫的 package 名稱）。

### Mode 與設定（`internal/config`，`LoadAI`）

- `AI_PROVIDER` 未設或 `disabled`：每個 AI 呼叫回 `provider_not_configured`（HTTP 層對應 `AI_PROVIDER_NOT_CONFIGURED`，T-025）。
- `fake`：只在 `APP_ENV` 為 development／test 時接受（production 或未設時拒絕啟動），與 D-044 的 filesystem adapter 相同。
- `anthropic` 而沒有 `AI_POLICY_APPROVAL`：接受，但每個 AI 呼叫回 `policy_not_approved`（`AI_POLICY_NOT_APPROVED`），而且**不讀任何 key**。
- `AI_POLICY_APPROVAL` 必須是這個 build 列出的 owner 核准（`internal/config/aiapproval` 的登記清單，目前為空）；否則拒絕啟動（`not_approved`）。核准以 `aiapproval.Approval` token 表示：只有 `aiapproval.Recorded` 能從登記清單產生（零值無效，`Valid` 每次重查清單），config 的 `AI.Approval` 與 live adapter 的 `Config.Approval` 都是這個型別，adapter 的 `New` 拒絕無效的 token（review L5）。這個 leaf package 是 adapter 唯一能 import 的 `internal/config` 部分（`ai-suggests-only` 的 allow）；只有它自己目錄下的測試能暫時登記核准（`export_test.go`）。加入一筆是 owner 的決定（model、token quota、資料政策、兩個 organization 的 retention／ZDR 一致，D-018），寫入 DECISIONS_AND_TBD 並經 review 的變更；agent 不加。所以預設設定、甚至任何環境變數組合都無法啟用 live AI。
- 核准後才讀 `AI_MODEL`（必填，沒有預設：TBD「LLM model」由 owner 在 eval 後選）、`GEMINUS_ANTHROPIC_API_KEY_A`（primary，必填，label `org-a`）與 `_B`（secondary，選填，label `org-b`）。`AI_API_KEY` 一律拒絕（舊名稱）。`ANTHROPIC_API_KEY` 從不讀取。
- 預算：`AI_MAX_PROMPT_BYTES`、`AI_MAX_OUTPUT_TOKENS`、`AI_TIMEOUT_SECONDS`、`AI_ATTEMPT_TIMEOUT_SECONDS`、`AI_MAX_RETRIES`、`AI_MAX_CONCURRENT_PER_ORGANIZATION` 覆寫預設值，上限由 `provider.BudgetCaps` 檢查。

### Client（每個 mode 都經過）

依序：caller 的 context 已結束 → `cancelled`；disabled → 其 code；organization ID 格式、prompt 的 role 與 mode 相符、prompt 已 seal 且未被改（`InputHash` 重算）→ 否則 `invalid_request`；prompt bytes ≤ `MaxPromptBytes`（tokenizer 的每個 token 至少一個 byte，所以也是 input token 的上界）→ 否則 `context_too_large`；同一 organization 在這個 process 的並行呼叫 < `MaxConcurrentPerOrganization` → 否則 `concurrency_limited`（不等待）；`Timeout` 包住整個呼叫（所有 retry 與 failover）。回答必須非空、≤ `MaxOutputBytes`、是一個 JSON document（不得重複 member、無效 UTF-8、尾隨資料）、通過角色的完整 2020-12 output schema，否則 `invalid_output`：error 只帶 issue 的 JSON Pointer 與固定訊息，不帶回答的任何文字（AI_SPEC §4）。Schema-valid 不是 authorized：proposal 的語意驗證、risk、pool 檢查屬 T-025。

預算預設值（D-007 那樣的工程候選，benchmark 與 owner 的 quota 決定後可調）：prompt 128 KiB（上限 512 KiB）、output 8192 tokens（上限 32000）、output 256 KiB、整個呼叫 120 s（上限 300 s，D-007 的 job timeout）、每個 attempt 90 s、retry 2 次（上限 4）、每個 organization 並行 2（上限 16）。**沒有**依 tenant 與期間累計的 token quota：那需要持久化與 owner 的 quota 數字（H-001、H-017），屬 T-025／T-036。

### Context builder（`aicontext`）

- **跨 tenant hard filter**：每個輸入都帶 organization（與 model）：base version、ID pool、data summary、run result、import。任何一個不屬於 request 的 scope，整個 build 失敗（`cross_tenant_input`，附 JSON Pointer），不回部分 context。Run result 的 run ID 必須是該 run 的；只分析 completed 的 run。
- **System rules 只有可信文字**：版本化 template（`prompts/<role>/system.md`）、固定的 server rules（`context-rules-1`）、mode 規則（edit 的 pool 規則、scenario 的 D-021 allowlist）、domain pack 的靜態 `PromptContext`（domain-neutral：builder 不含任何 domain 詞彙）、`output_locale` 的固定 instruction。
- **User message 只有 block**：`<server_data name="…">`（ID_ALLOCATION_POOL、ALLOWED_METRIC_REFERENCES、CONTEXT_OMISSIONS）與 `<untrusted_data name="…">`（model IR、entity index、intent、question、selected IDs、data summary、results、columns、sample rows）。每個 block 的內容是一個 JSON 值，`Seal` 重新編碼並跳脫 `<`、`>`、`&`、U+2028、U+2029，所以資料無法關閉自己的 block 或偽造另一個；`Seal` 也拒絕含 block 的 system rules。`InputHash` = JCS＋SHA-256（role、template version、locale、system、user），是 audit 的 sanitized input hash。
- **Redaction（default deny）**：每個 untrusted 字串與 member name：private key、`sk-ant-`／`sk-`／`pk-`／`rk-` key、AWS、GitHub、Slack、Google key、JWT、bearer／basic token、`password=`／`token:` 類 key=value、任何 URL、e-mail、IPv4、D-001 的 server ID（大寫 ULID；pool 的小寫 ID 不受影響）、不可見的 control／format 字元（bidi override、zero-width、BOM）。以 `[REDACTED:<kind>]` 標示並計數。
- **Bounded 與 minimal**：字串 2000 字（intent 20000、question 10000、名稱 200、cell 200）；root metadata ≤ 2 KiB、node 的 domain_attributes ≤ 1 KiB；data summary ≤ 8 KiB；sample rows ≤ 20；columns ≤ 256；不送 model ID、version ID、run ID、hash、views、replication summaries、dataset version ID。
- **可見的 omission**：CONTEXT_OMISSIONS 與 prompt 的 `Disclosure` 列出省略的種類、數量與固定 reason code（不含任何值）。超出預算時依序：拿掉 metadata／domain attributes／data summaries → 只完整顯示選取 entity 的鄰近範圍、其他 entity 以 `{"id","omitted":"budget"}` 佔住原 index（JSON Patch path 的 index 不變）→ 不送 IR；ENTITY_INDEX 永遠列出每個 entity 與其真實 index。最小的 context 仍超過預算時回 `context_too_large`，不默默截斷。
- **D-046**：edit mode 的 context 帶 proposal 的 pool（server_data）；scenario 不帶（scenario 不新增 entity）。Pool 必須屬於 scope、形狀正確、不含 base 已用的 ID。

### `output_locale`

`locale.Parse`：空值為 `en`，否則必須完全等於 `en` 或 `zh-Hant`，錯誤指向 `/output_locale` 且不引用 client 的值。Instruction 是每個 locale 的固定 server 文字：prose 用該語言；ID、JSON Patch path、JSON Pointer、member name、enum、reason／error code、metric path、單位一律照抄；數字一律用阿拉伯數字。`locale.CheckProse` 找出 prose 中無法核對的數字：與供應值（依顯示的小數位四捨五入，或百分比）都不符的阿拉伯數字、數字詞（中文數字串、百分之、數字＋量詞或單位；英文 two…trillion、dozen、twice、half；「一個」「一台」、「one」這類冠詞用法不算）、非 ASCII 數字。Analysis context 回傳供應的數字（結果、比較、config、容量與機率，不含 array index）與可引用的 metric（只有 available 的）；把它接到 analysis 的 completed／failed 是 T-030。

### Live adapter（`anthropic`）

- 官方 SDK `github.com/anthropics/anthropic-sdk-go` **v1.78.0**（2026-09-30 發布，滿兩週；v1.79.x／v1.80.0 未滿兩週）、MIT；連帶的 module 與授權見 THIRD_PARTY_NOTICES。
- 不用 `NewClient`：它的預設 chain 讀 `ANTHROPIC_API_KEY`、`ANTHROPIC_AUTH_TOKEN`、`ANTHROPIC_BASE_URL`、`ANTHROPIC_CUSTOM_HEADERS`、SDK profile 與 workload identity。每個 credential 一個 `NewMessageService`，只帶明確的 base URL、HTTP client、key、retry 數與 attempt timeout。Base URL 是常數 `https://api.anthropic.com/`；只有測試（`export_test.go`）能改。HTTP client 不跟隨 redirect（否則 key header 會跟著走）。
- D-018：依序嘗試 credential；只有 401、402、403 把它標為不可用（process 存活期間）並換下一個；408、409、429、5xx（含 529）與連線錯誤在同一 credential 以 SDK 的 backoff 重試至預算的次數後失敗（`rate_limited`／`unavailable`，可重試），不換 organization；其他 4xx 為 `request_rejected`，不重試、不換。全部被拒為 `credentials_exhausted`。Response、metadata 與 error 只有 label。
- Request：approved model、`max_tokens`、system 一個 text block、user 一個 text block、`output_config.format` 為投影後的 schema。沒有 tools、tool_choice、thinking 設定、sampling 參數或 metadata。Stop reason：`end_turn`／`stop_sequence` 取 text block（thinking block 丟棄、不存），`max_tokens` → `output_truncated`，`refusal` → `refused`，其他（含 tool_use、pause_turn）與任何 tool block → `invalid_output`。
- **Schema projection**：解開 `$ref`（拒絕遞迴）、`oneOf` 改 `anyOf`（每個 output schema 的分支以 const member 區分，兩者允許的值相同），移除 subset 不支援的 constraint（長度、數值界限、pattern、array 界限、uniqueItems、if／then／else、not、不支援的 format）並列在 `Dropped`；拒絕開放的 object、`patternProperties` 等無法投影而不放寬的 keyword。只放寬 constraint、不新增 member、不開放 object；server 以完整 schema 檢查每個回答。

## Consequences

- 所有 mode 走同一個 `Client`，fake 的回答也要通過完整 schema；E2E（T-040）以 fake 的 script 驅動。
- Live adapter 只對本機 httptest server 測過。對真實 API 的任何呼叫、投影後的 schema 是否被 API 接受、`output_config` 在 owner 選定的 model 上的行為：**NOT RUN**。
- 被 401／402／403 標為不可用的 credential 在 process 重啟前不再使用；沒有自動重新探測（credits 補充後需重啟 worker）。
- 並行限制是每個 process 的；多個 worker 會倍增。期間 quota、prompt caching（資料保留屬資料政策）、refusal fallback 到其他 model（換 model 屬 owner）都沒有做。
- 連帶 module 中有 `go.yaml.in/yaml/v4` 的 release candidate（SDK 的 profile 設定使用；本 adapter 不呼叫該路徑）。

## Alternatives

- 手寫 HTTP client：任務指示只允許官方 SDK 或 NOT IMPLEMENTED 的 stub。
- 以 forced tool 取得結構化輸出：新 model 拒絕 forced `tool_choice`，且「沒有 mutation tool」較易稽核；選 `output_config.format`。
- 429 也 failover：實質合併兩個 organization 的 rate limit，D-018 不採。
