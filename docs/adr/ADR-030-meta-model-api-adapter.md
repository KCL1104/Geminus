# ADR-030 — Meta Model API adapter（Muse Spark、SAM 3.1）與照片路徑的設定（T-055）

狀態：**PROPOSED**（2026-10-26，T-055 agent 提出並實作；agent 沒有批准，等獨立審查與 owner／coordinator 決定）。依據：H-029（APPROVED，owner 選 Meta Model API、Standard mode、只送 synthetic 資料與 demo 圖片）、H-031、H-033（APPROVED，恢復 T-055：自然語言 proposal 用 Muse Spark，照片辨識用 SAM 3.1＋Muse Spark，Claude 為可切換的備援）、ADR-027（ACCEPTED）、T-053 的 D-110–D-115（PROPOSED）、D-116（APPROVED）、AGENTS.md §2。細節的選擇列為 D-121、D-122、D-123（PROPOSED，`docs/DECISIONS_AND_TBD.md`）。**本 ADR 不批准、也沒有執行任何 live 呼叫**：所有測試只對本機 `httptest` server 與 fake；live smoke、live eval、token count、model listing 都 NOT RUN。

## Context

H-033 要求在 T-053 的 provider 邊界（`provider.Client`、`Billable`、`Meter`、spend ledger、開關）上加第二個 live provider：Meta Model API 的 Muse Spark（proposal）與 SAM 3.1（照片中的機台、輸送帶），並讓 T-056 的照片路徑能依設定選擇 provider（預設 Meta，Claude 為備援），API 不變。官方文件（https://dev.meta.ai/docs/ 下的 models、protocols/messages、api-reference/messages、structured-output、image-understanding、reasoning、pricing-rate-limits、error-handling、authentication、protocols/responses、api-reference/responses、sam/overview、sam/segmenting、sam/reading-segmentation，以及 https://dev.meta.ai/legal/ 的 Terms of Service、Data Commitments、Model Designation Policy；全部 2026-10-26 取得，原文與 sha256 記在 `checks/T-055_VALIDATION.md`）說明：

- Messages endpoint 與 Anthropic 相容（「Base URL | https://api.meta.ai」「Endpoints | POST /v1/messages」「Auth | Bearer token」），不支援 `stop_sequences`、`top_k`、`container`、`inference_geo`，未知的 top-level 欄位回 400；`thinking: {type: "disabled"}` 回 400；adapter 是 stateless、storage 關閉。
- Standard mode 由 model ID 決定（`muse-spark-1.3` 為 Standard，`muse-spark-1.3-contributor` 為 Contributor）；Terms 另寫「the system may route you to a Contributor Model」。
- 價格（Standard，每百萬 token）：cached input 0.15、input 1.25、output 4.25 美元；SAM「Image segmentation | $2.50 per 1,000 images」。
- Structured output 的 schema 有明確上限（nesting 10 層、5,000 properties、120,000 字元、enum 1,000 值等）。
- 圖片：每張至多 50 MB、每個 request 至多 50 張、cost 隨解析度變化（約 1280px 加 1,300–1,500 tokens），沒有每張圖片的 token 上限。
- SAM 3.1：一個 request 一個 concept（短名詞片語）與一個媒體；回答是文字 grammar（`<|box;id=…;x1=…;…|>` 與 mask record）；`metadata.include_confidence` 取得 confidence；`store` 預設 true。

Meta 沒有 Go SDK。

## Decision

### Package 與邊界

| Package | 內容 | 規則（`scripts/check-architecture`） |
|---|---|---|
| `internal/ai/provider/structured` | T-053 的投影與開放值的解碼，從 `anthropic` 原封不動搬出（只改 package 名稱，`checks/evidence/2026-10-26-T-055/move-diff.log`）；兩個 adapter 共用 | `ai-is-pure-but-the-adapter`：純計算，沒有網路與檔案 |
| `internal/ai/provider/meta` | Muse Spark backend（`provider.Billable`、`provider.ImageInput`）、SAM 3.1 backend（`provider.BillableSegmenter`）、schema 上限的 guard、SAM grammar | `live-ai-only-in-the-worker`（只有 worker 與 `cmd/ai-eval-live` 能連結）、新的 `live-adapters-apart`（不連結 `anthropic`，反之亦然）；`ai-is-pure-but-the-adapter` 豁免它（網路）；不 import LLM SDK、pgx、config（只用 leaf `aiapproval`） |
| `internal/ai/provider`（擴充） | `Image`（型別、大小、尺寸檢查）、`Prompt.Images`、`ImageInput`、`Segmenter`／`SegmentClient`／`SegmentBackend`／`BillableSegmenter`、`Disabled.SegmentOne` | 不變 |
| `internal/ai/provider/fake`（擴充） | fake backend 接受圖片；確定性的 fake `Segmenter`（script、fail、calls） | 不變 |

標準函式庫的 HTTP client，不加任何 dependency（`go.mod` 不變）。

### Muse Spark（proposal）

- **Request**：`POST https://api.meta.ai/v1/messages`（常數，只有測試能換），`Authorization: Bearer <key>`，不跟隨 redirect，回答至多讀 8 MiB。Body 只有 `model`、`max_tokens`、`system`（一個 text block）、一個 user message（先是 base64 的 image block，後是 prompt 的文字）、`output_config.format`（`json_schema`，T-053 的投影）。沒有 tools、thinking、sampling、metadata、`stop_sequences`、`top_k`、`container`、`inference_geo`、`cache_control`、`service_tier`、stream。推理程度用 model 預設（文件：「When you omit the parameter, the model still reasons at a model-determined level」；reasoning tokens 計入 output 與 `max_tokens`）。
- **投影與上限**：送出前以 `SchemaViolations` 檢查投影後的 schema（nesting 以每個巢狀 subschema 計 10 層、5,000 properties、120,000 字元、enum 1,000／大型 enum 250 值 15,000 字元、展開 200,000 節點、遞迴；另拒絕空 schema、沒有 `additionalProperties: false` 的 object、不認得的 keyword），超出即不送。測試以每個 prompt role 實際送出的 schema 檢查（proposal-output 2,623 bytes、7 層；analysis 6 層；mapping 5 層）。
- **回答**：嚴格的 JSON document，`type` 為 `message`、`role` 為 `assistant`、usage 不為負；`model` 必須等於 request 的 model，否則 `invalid_output`（Standard mode 的保證：Contributor 的回答不被接受，usage 照樣結算）；`end_turn` 取 text block（`thinking`／`redacted_thinking` 不讀即丟）、`max_tokens` 為 `output_truncated`、`refusal` 為 `refused`、其他 stop reason 或 content block 為 `invalid_output`；開放值以 `structured.Decode` fail closed 解碼，之後照樣經完整 schema 與 server review。
- **錯誤與重試**（D-121）：一把 key（label `meta`），沒有 failover。401／403 停用該 key 到 process 重啟，402 休息 15 分鐘，皆 `credentials_exhausted`；429 以 backoff（500 ms 加倍、jitter ≤ 500 ms、上限 8 s，Retry-After 至多 60 s，不超過呼叫的 deadline）重試，`rate_limited`；504 不重試（文件：「Do not simply retry」）；其他 5xx 重試，`unavailable`；其他 4xx 為 `request_rejected`。錯誤只記 HTTP status 與文件列出的 error type（Messages 的 Anthropic envelope 與其他 endpoint 的 OpenAI envelope，共 8 種），其他一律 `other`；不記 message、key、URL。
- **計量**：`Quote` 為 prompt 與投影 schema 的 bytes 加 4096（D-111 的規則）；有圖片時為整個 context window 1,048,576 tokens（沒有每張圖片的上限；約 1.35 美元一個 attempt，D-123）；預留 = (1 + retries) × (input × 1.25 + `max_tokens` × 4.25)，**沒有** Anthropic 的 1.1 倍 geo（`spend.GeoPriced` 只有 `claude-opus-5-5`）。可能已被計費的 attempt：連線中斷、attempt 逾時、body 讀不完、**每個 5xx**（文件沒有說錯誤 status 不計費）、讀不了的 2xx（至少以整個預留結算）。價格固定在 `internal/ai/spend`（來源 https://dev.meta.ai/docs/pricing-rate-limits，2026-10-26 取得）；cache 寫入以 input 價（文件沒有另列寫入價）。
- **圖片**：`provider.NewImage` 只接受 PNG、JPEG、WebP（signature 與宣告的型別一致），至多 5 MiB、每邊至多 8000 px、每個 prompt 至多 4 張，請求前檢查；`Prompt.Seal` 複製資料並把每張的 media type、sha256、尺寸算進 prompt hash（純文字 prompt 的 hash 不變）。`provider.Client` 只把有圖片的 prompt 交給 `ImageInput` 且 `AcceptsImages()` 的 backend，否則 `invalid_request`；Meta backend 只在批准的資料政策列出圖片時接受（H-029 有，H-018 沒有）。

### SAM 3.1（照片路徑的 segmentation，T-056 用）

- `provider.Segmenter`：`Segment(ctx, SegmentRequest{OrganizationID, Role, Attribution, Image, Concepts}) (Segmentation, error)`；每個 concept 一個 request（文件：一個 request 一個 concept）。`SegmentClient` 檢查 request（organization、role、圖片、至多 8 個 concept、每個為 64 bytes 內的小寫名詞片語 `^[a-z0-9]+(?:[ -][a-z0-9]+)*$`、計量時要 attribution）與回答（frame 等於圖片尺寸（或轉 90°）、每個 concept 至多 100 個、object ID 不重複、box 在 frame 內、confidence 在 0..1、concept 相符），不合即 `invalid_output`（issue path `/concepts/<i>/…`）。並行數、timeout、retry 與 proposal 共用 budget。
- Backend：`POST /v1/responses`，`store: false`、`stream: false`、`metadata: {include_confidence: "true", mask_encoding: "one_bit"}`，一個 user message（`input_text` 為 concept、`input_image` 為 data URL）。回答必須是 `object: response`、`status: completed`、model 相同、只有 `output_text`；文字依文件的 grammar 嚴格解析（box 與 mask 各自的 confidence 必須相同、mask 只檢查形狀不解碼且丟棄、空字串為零個結果）。
- 計量：每個 concept 預留 (1 + retries) × 每張 2,500,000 nano-USD；有回答時以 max(1, `usage.images_processed`) 張結算，可能計費但沒有回答的 attempt 每個一張，讀不了的回答至少整個預留，被拒為 0。

### 批准紀錄（`internal/config/aiapproval`，D-122）

- `Record` 加 `SegmentationModel`；`Gap` 依 provider 分開：anthropic 的規則不變（另要求 `SegmentationModel` 為空）；meta 要求 credential `["meta"]`、model 為 Model Designation Policy 的 Standard Muse Spark（1.1／1.2／1.3）、segmentation model 為 Standard 的 `sam-3.1`（只在資料政策列出圖片時）、資料政策為 synthetic 資料、demo 圖片與使用者輸入的文字、角色為 `modeler`、`scenario`、每月預算 > 0、retention 為 `meta_standard_services`（owner、日期、根據）。不要求 Console spend limit 的確認（Meta 沒有對應的設定被 owner 確認過；程式內的 ledger 是上限）。
- H-029 的 record：provider `meta`、model `muse-spark-1.3`、segmentation `sam-3.1`、USD 100／月（H-029 沿用 T-053 的上限，解讀見 D-122）、approver KCL1104、2026-10-26、來源「H-029 (1), amended by H-033」。
- `Approval` 加 `Provider()`、`SegmentationModel()`、`AllowsImages()`；`anthropic.New` 現在拒絕非 anthropic 的批准，`meta.New`／`NewSegmenter` 拒絕非 meta 的批准。

### 設定（`internal/config.LoadAI`）與組裝

- `AI_PROVIDER=meta`：與 anthropic 相同的開關規則，加上批准必須屬於該 provider（否則 `provider_mismatch`）；key 只讀 `GEMINUS_MUSE_API_KEY`（owner 取的名稱），只在開關 on 且其他條件都成立後讀，必須沒有空白與控制字元、符合文件的 `LLM_<數字 ID>_<secret>`；`MODEL_API_KEY`（Meta 文件的慣例名稱）與 Anthropic 的 key 一律不讀。
- 照片路徑（D-123）：`AI_VISION_PROVIDER` 不設時跟 `AI_PROVIDER`；`disabled` 關閉；`fake` 只在 development／test；另一個 live provider 需要 `AI_VISION_POLICY_APPROVAL`、`AI_VISION_MODEL`（同樣的規則）、自己的 key 與同一個開關。批准的資料政策沒有圖片（H-018）時照片路徑維持關閉（`images_not_approved`），proposal 不受影響。兩條路徑共用一個 ledger，月上限取 live 批准中最小的預算。
- `cmd/worker`：`aiClient` 依 provider 建 Claude 或 Muse Spark 的 metered client；啟動時以 `photoPath` 建照片路徑的 judge（`provider.Client`）與 segmenter（`provider.SegmentClient`），放在 handler 的 `deps.photo`，給 T-056 的 handler 用；log 記 provider、model、typed code（`LIVE_AI_ON`、`PHOTO_PATH_ON`、`SWITCH_OFF`、`IMAGES_NOT_APPROVED`…），從不記 key。`cmd/api` 的可用性把 meta 與 anthropic 視為相同。`cmd/ai-eval-live` 依 `AI_PROVIDER` 建 adapter；report 記 provider 與該 model 的價格來源。
- Redaction：`aicontext` 與 `observability` 的 credential pattern 加上 `LLM_<數字>_<secret>`。

## Consequences

- 兩個 live adapter 共用投影、解碼、計量、ledger、開關與審查；任何一個都只能經 metered client 呼叫，也只接受自己 provider 的批准。
- 照片路徑的 provider 是設定，不是 API 的一部分；T-056 只依賴 `provider.Segmenter`、`provider.Client` 與 `provider.Image`。
- 有圖片的呼叫預留很大（context window 為上限），每日上限 10 美元下約 7 次照片 proposal 的預留（以 3 個 attempt 計）；結算依實際 usage。
- 可能被高估：5xx 一律當作可能計費。可能被低估的情形：文件之外的計費項目（例如 Meta 注入的 steering context 寫明不計費）。
- API 仍不能在不讀 key 的情況下得知 live AI 是否開啟（T-053 I5 沒有在本 ADR 處理），部署的 API 維持 `AI_PROVIDER=disabled`。（之後的 follow-up 已處理：API 以四個非 secret 的設定判斷、不讀 key，部署的 API 與 worker 設相同的設定；見 ADR-027 addendum 最後一項，coordinator 的決定。）

## Alternatives

- **OpenAI 相容的 Chat Completions 或 Responses endpoint 給 Muse Spark**：可行，但 Messages 與 T-053 的 request／回答形狀與錯誤處理相同，可以共用投影與測試；SAM 只能用 Responses。
- **社群 SDK**：沒有官方 Go SDK；加 dependency 需要 ADR-011 的審核，標準函式庫已足夠。
- **以免費的 `count_tokens` 預估圖片的 token**：可以把預留降到實際大小，但每次呼叫多一個 request，且必須是 live 呼叫（本 task 不做）；列為 owner／coordinator 的問題。
- **5xx 視為不計費**：預留較小，但文件沒有這樣寫；選擇安全側。
