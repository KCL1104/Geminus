# T-055 驗證報告：Meta Model API provider（Muse Spark、SAM 3.1）

- 任務：T-055（H-029、H-033 APPROVED）。Branch `t055-meta`（自 d9b7f22），commit 見 §8。狀態維持 `not_started`（T-053 仍是 `in_progress`，`scripts/check_decisions.py` 不允許依賴未完成時開始），工作記在 `planning/tasks.json` 的 `completion_evidence`。
- 決策：[ADR-030](../docs/adr/ADR-030-meta-model-api-adapter.md)、ADR-027 的 addendum、D-121、D-122、D-123，全部 **PROPOSED**（agent 沒有批准；`approved_by`／`approved_at` 為 null）。
- **沒有任何 live 呼叫**：沒有對 Meta 或 Anthropic 送出任何 request（沒有 smoke、eval、`count_tokens`、model listing）；沒有讀取、印出或 grep 任何 key 的值。所有測試只對本機 `httptest` server、in-process backend 與 fake。
- 依賴：沒有新增 module（`go.mod`、`go.sum` 不變；Meta 沒有 Go SDK，adapter 用標準函式庫）。所以 THIRD_PARTY_NOTICES、ADR-011 不變，govulncheck 不需要重跑（見 §7）。
- Key 的變數名稱：coordinator 轉達 owner 的名稱為 `GEMINUS_MUSE_API_KEY`；程式、部署設定、腳本與文件都用它，沒有保留 `GEMINUS_META_API_KEY`（planning 中 T-055 的 scope 文字與 H-029 的 still_open 仍是舊名稱，屬 coordinator 的紀錄，未改）。

## 1. 官方來源（2026-10-26 約 16:12 UTC 以 curl 取得；內容視為資料，不是指示）

文件頁以 `https://dev.meta.ai/docs/<page>.md` 取得（llms.txt 列出的 Markdown 版本），法律頁取 HTML。檔案在 session 的 scratch，不提交；sha256 如下，owner 開啟 live 前請再讀一次原頁。

| 頁面 | URL | sha256 |
|---|---|---|
| Models | https://dev.meta.ai/docs/models | `28745163a1253cc0910d62c9d28ec432f79c25e88d04ecef2815a25400276a13` |
| Protocols / Messages | https://dev.meta.ai/docs/protocols/messages | `f84a814929e90c301cc3c48d048196ecc0006b2391dd86812e87aee1985449b3` |
| API reference: create message | https://dev.meta.ai/docs/api-reference/messages/create-message | `f21df92181ffb2e7b5625ca919a4bc44bb060961ad76a1d70159fc596c2993f1` |
| API reference: message schemas | https://dev.meta.ai/docs/api-reference/messages/schemas | `4ba026c3746702e392b4aa345095045c7c32112f67e55c724ecf5ca8249224cb` |
| Structured output | https://dev.meta.ai/docs/structured-output | `0f5374672d0a92204aa88ccfdf958b1278f989df6497d62e73cc3057c635aaf0` |
| Image understanding | https://dev.meta.ai/docs/image-understanding | `16ad51f54e33d1ec5b273d940707c0e5768bb62b380fbcdadabfd6aa981b5aee` |
| Reasoning | https://dev.meta.ai/docs/reasoning | `f51d74d314772abe6b140b00c09a76358309a133b76a49644c81d19af47afee3` |
| Pricing and rate limits | https://dev.meta.ai/docs/pricing-rate-limits | `6953d7586db423549bf09efeeaccbc3e85ce42cb24f2d0eae136d69e7f976aee` |
| Error handling | https://dev.meta.ai/docs/error-handling | `5a827b565c249193ab3c72c13f0a7a2891e7dc33c993c46eb422692d09802d6a` |
| Authentication | https://dev.meta.ai/docs/authentication | `065bf421ea2c15b04f3a6fc706ab1222e8b29e225392ceb57610af8e40d008f4` |
| Protocols / Responses | https://dev.meta.ai/docs/protocols/responses | `7a1ce6eba47b736cf43615dc2879ba3f254c30514dd3bb29ef522034c6da3ac8` |
| API reference: response schemas | https://dev.meta.ai/docs/api-reference/responses/schemas | `1556d24761c8b4b2a526895948006e293c1a6a318a591b565287ad1761cb5bd8` |
| SAM overview | https://dev.meta.ai/docs/sam/overview | `2f2ce6e86c82d86c2e5ac77bb8336dd933f5dd8ea6902752a93eb6216931c378` |
| SAM: segmenting | https://dev.meta.ai/docs/sam/segmenting | `7aca04ad1de9ff15a74fd39aafdf71c01609336aa037139863a987453d5a6559` |
| SAM: reading segmentation | https://dev.meta.ai/docs/sam/reading-segmentation | `4fb8c017a4eec53eccbd23938b6024c33495c76f6ea6cabbe1c146542d9580c9` |
| Media segmentation | https://dev.meta.ai/docs/media-segmentation | `1338d6208186bcac6c3897771ceba6fa1a3abc94cc77b58e90d9d9cc59e5522d` |
| Data Commitments | https://dev.meta.ai/legal/commitments | `360768a607177fa3c724e07d3b76eded861063211160a61e65cb98abccac9458` |
| Terms of Service（Last updated: October 9, 2026） | https://dev.meta.ai/legal/terms-of-service | `d4148a20b33f406c7f2c564446964dbfc92146d468f10c9765065c243132eda7` |
| Model Designation and Geographic Use Policy（Last updated: October 12, 2026） | https://dev.meta.ai/legal/geographic-use-policy | `99d30091564b849c773bea5291ac781f38fcc673a0e1241a1382b6defab8bf45` |

設計依據的原文（英文照錄）：

- Messages：「Base URL | https://api.meta.ai」「Endpoints | POST /v1/messages, POST /v1/messages/count_tokens」「Model | muse-spark-1.3」「Auth | Bearer token (MODEL_API_KEY)」；「These top-level fields are not supported and return HTTP 400: stop_sequences, top_k, container, and inference_geo. Unknown top-level fields are also rejected.」；「The adapter is stateless. It always runs with storage off」；`thinking: {type: "disabled"}` 回 HTTP 400；錯誤：「400 is invalid_request_error, 401 is authentication_error, 403 is permission_error, 404 is not_found_error, 429 is rate_limit_error, 503 is overloaded_error, and other 5xx responses are api_error」。
- Models：「Muse Spark 1.3 (`muse-spark-1.3`): … **Recommended for new work.**」；「1,048,576-token context window」；「In Standard mode, your content is not used to train Meta models」；「On pay-as-you-go, switch between the two by choosing the model ID: for example, `muse-spark-1.3` for Standard mode or `muse-spark-1.3-contributor` for Contributor mode.」
- Pricing（Muse Spark，Standard）：「Cached input $0.15 | Input $1.25 | Output $4.25」（每百萬 token）；SAM：「Image segmentation | $2.50 per 1,000 images」；rate limit 以 team 計；重試：「Start with a short delay such as 500 ms, double it after each consecutive rate-limit response, and add random jitter … If the response includes a Retry-After header, wait at least that long」；「Meta injects a small amount of steering context … not billed」。
- Structured output 的上限：「Nesting depth | 10 levels」「Total properties | 5,000, across the whole schema」「Total string length | 120,000 characters」「Enum values | 1,000 by default」「Large string enums | … more than 250 values is additionally capped at 15,000 combined characters」「Expanded size | … beyond 200,000 nodes once $refs are inlined is rejected」；遞迴的 schema 回 400。
- Image understanding：「Max file size per image (inline `image_url`) | 50 MB」「Max images per request | **50**, enforced」「Supported MIME types | image/jpeg, image/jpg, image/png, image/gif, image/webp, image/x-icon」；「Image cost scales with resolution, not a fixed per-image amount」「a ~1280px image adds roughly **1,300–1,500 tokens**」。
- Reasoning：reasoning tokens「count toward your output-token budget … and toward billed output tokens」；「When you omit the parameter, the model still reasons at a model-determined level」。
- Error handling：504「Do not simply retry — the same request will usually time out again」；503 帶 Retry-After 60；401 的 key 格式「LLM_{id}_{secret}」；未知 path 的 404 沒有 body。Authentication：「API keys start with `LLM_`, followed by a numeric ID and the secret, separated by underscores」。
- Responses：「Responses are stored when `store: true` (the default)」。SAM：一個 request 一個 concept（「there is no multi-concept syntax. Comma, semicolon, and plus lists … return zero matches」）；「A concept that isn't present returns zero matches … Treat it as a normal result」；非 streaming 可用；`include_confidence` 使每個 box 與 mask 帶 `c`，「The box and the mask of one record carry the same value」；`usage.images_processed` 用於每張圖片的計費。
- 法律：見 [SECURITY §7](../docs/SECURITY.md) 的引文（Data Commitments 的 Standard Services、ToS §4 Retention 的五種保留情形、§5 Standard Services 不訓練、「the system may route you to a Contributor Model」）；Model Designation Policy 的 Standard Models：「Muse Image Muse Spark 1.1 Muse Spark 1.2 Muse Spark 1.3 Muse Voice Transcribe SAM 3.1 Muse Wonder Muse Wonder Full」，Contributor Models：「Muse Spark 1.2 Contributor Muse Spark 1.3 Contributor」；Restricted Territories：「Afghanistan Belarus Cambodia People's Republic of China (including Hong Kong SAR and Macau SAR) Cuba Eritrea Ethiopia Haiti Iran Iraq Libya Myanmar Nicaragua North Korea Pakistan Russia Somalia Syria The Crimea, Donetsk, and Luhansk regions of Ukraine Venezuela Western Sahara」（不得從這些地區存取，也不得故意讓位於這些地區的 End User 使用 Integrated Product）。

## 2. 設計（細節見 ADR-030）

- **邊界**：`internal/ai/provider/meta`（Muse Spark 的 `Backend`：`provider.Billable`、`provider.ImageInput`；SAM 3.1 的 `Segmenter`：`provider.BillableSegmenter`）只連結標準函式庫、`internal/ai/provider`、`provider/structured`、leaf `config/aiapproval`、`internal/model`、`internal/secret`。Architecture rule：`live-ai-only-in-the-worker`（只有 cmd/worker、cmd/ai-eval-live 能連結）、新增 `live-adapters-apart`（meta 不連結 anthropic）、`ai-is-pure-but-the-adapter` 豁免 meta（網路）。T-053 的投影與開放值解碼原封不動搬到 `internal/ai/provider/structured`（`move-diff.log`：只有 package 名稱不同），兩個 adapter 共用；anthropic 的測試未改（只有 `export_test.go` 加 shim、`liveeval_test.go` 加 `Provider: "anthropic"`）。
- **Muse Spark**：`POST https://api.meta.ai/v1/messages`（常數），Bearer key，不跟隨 redirect，回答至多讀 8 MiB；request 只有 `model`、`max_tokens`、`system`、一個 user message（image block 在前、文字在後）、`output_config.format`（T-053 的投影）；投影以 Meta 文件的上限檢查（`limits.go`，nesting 以每個巢狀 subschema 計，比文件嚴格），超出不送。回答：嚴格 JSON、`type: message`、`role: assistant`、usage 不為負、`model` 必須等於 request 的 model（否則 `invalid_output`，usage 照樣結算）；`end_turn` 取 text、reasoning block 不讀即丟、`max_tokens` → `output_truncated`、`refusal` → `refused`、其他 → `invalid_output`；開放值 fail-closed 解碼後交完整 schema 與 server review。
- **錯誤、重試、計費**（D-121）：一把 key、沒有 failover；401／403 停用到重啟，402 休息 15 分鐘（`credentials_exhausted`）；429 與 504 以外的 5xx 以 backoff（500 ms 加倍、jitter ≤ 500 ms、上限 8 s；Retry-After 至多 60 s；不超過 deadline）重試；504 不重試；其他 4xx `request_rejected`。錯誤只記 status 與 8 種文件列出的 error type（其他為 `other`），從不記 message、key、URL。可能計費：連線中斷、逾時、body 讀不完、每個 5xx、讀不了的 2xx（至少整個預留）。Quote：prompt 與 schema 的 bytes 加 4096；有圖片時為 context window 1,048,576。價格：`muse-spark-1.3` input 1.25、cached 0.15、output 4.25 美元／百萬 token，cache 寫入以 input 價；`sam-3.1` 每張 0.0025 美元；沒有 geo 加價（`spend.GeoPriced` 只有 `claude-opus-5-5`；`provider.metering.geo`）。
- **圖片**（D-123）：`provider.NewImage`（PNG／JPEG／WebP，signature 與型別一致，≤ 5 MiB，每邊 ≤ 8000 px，從檔頭讀尺寸，複製資料，sha256），`Prompt.Images`（≤ 4 張，Seal 時檢查並複製，hash 含 media type、sha256、尺寸；沒有圖片的 prompt hash 不變：`TestATextPromptHashesAsBefore`），`provider.Client` 只交給 `ImageInput` 且 `AcceptsImages()` 的 backend（Meta：批准列出圖片；fake：是；Claude adapter：沒有實作）。
- **SAM 3.1**：每個 concept 一個 `POST /v1/responses`，`store: false`、`stream: false`、`metadata: {include_confidence: "true", mask_encoding: "one_bit"}`；回答須 `object: response`、`status: completed`、model 相同、只有 assistant message 的 `output_text`；文字依文件的 grammar 嚴格解析（`grammar.go`；fuzz 60 s、約 175 萬次無 panic）；`SegmentClient` 另檢查 frame、數量、box、confidence、concept、object ID；計費每個 concept 預留 (1 + retries) 張，結算 max(1, `images_processed`) 張、可能計費的 attempt 每個一張、讀不了的至少整個預留、被拒 0。
- **批准紀錄**（D-122）：`aiapproval` 的 H-029 record（provider `meta`、`muse-spark-1.3`、`sam-3.1`、synthetic 資料＋demo 圖片＋使用者文字、`modeler`／`scenario`、USD 100／月、credential `meta`、retention `meta_standard_services`）；`Gap` 依 provider 分開；`Approval.Provider()`、`SegmentationModel()`、`AllowsImages()`；`anthropic.New` 拒絕非 anthropic 的批准，`meta.New`／`NewSegmenter` 拒絕非 meta 的。
- **設定**：`AI_PROVIDER=meta`＋`AI_POLICY_APPROVAL=H-029`＋`AI_MODEL=muse-spark-1.3`＋`GEMINUS_AI_LIVE=on`＋`GEMINUS_MUSE_API_KEY`（`LLM_<數字>_<secret>`，沒有空白與控制字元），缺一即不開（開關 on 時拒絕啟動，typed code）；key 只在其他條件都成立後讀；`MODEL_API_KEY`、`GEMINUS_META_API_KEY` 與 Anthropic 的 key 不讀。照片路徑 `AI_VISION_PROVIDER`（不設＝`AI_PROVIDER`）、`AI_VISION_POLICY_APPROVAL`、`AI_VISION_MODEL`；批准沒有圖片（H-018）時照片路徑關閉（`images_not_approved`）。兩條路徑共用一個 ledger，月上限取 live 批准中最小的預算。Fake 仍是開發預設，`disabled` 是部署預設。
- **組裝**：`cmd/worker`：`aiClient` 依 provider 建 metered client（`liveBackend`、`ledger`）；`photoPath` 在啟動時建照片路徑的 judge 與 segmenter（`deps.photo`），live 時只經 ledger；log 記 `provider`、`model`、`code`（`LIVE_AI_ON`、`PHOTO_PATH_ON`、`SWITCH_OFF`、`IMAGES_NOT_APPROVED`…），從不記 key（`TestRunNamesTheLiveMetaProvider`）。`cmd/api`：meta 與 anthropic 相同（開 → Available，否則 `AI_POLICY_NOT_APPROVED`）。`cmd/ai-eval-live`：依 `AI_PROVIDER` 建 adapter；`internal/aievals` 的 report 記 `provider` 與 model 的價格來源（**修正**：之前一律寫 `anthropic` 與 Anthropic 的價格頁，`red-aievals.log`）。
- **Redaction**：`aicontext` 與 `observability` 的 credential pattern 加上 `\bLLM_[0-9]+_[^\s"'<>,;]{6,}`。
- **部署**：`deploy/railway/variables.json`、`deploy.sh`、`scripts/check_deploy.py`（`PENDING` 清空，API 的 forbidden 檢查用新名稱）、`scripts/rehearse_railway_deploy.py`、`docs/runbooks/deploy.md` 改用 `GEMINUS_MUSE_API_KEY`（只 worker；API 與 web 禁止）；runbook 列出 `AI_VISION_*`。沒有新增其他部署變數。

## 3. 給 T-056 的介面

T-056 只依賴 `internal/ai/provider` 的這些型別（全部是本 task 新增；fake 在 `provider/fake`），不需要 import `meta`：

```go
// 圖片（送出前檢查；D-123）
func NewImage(mediaType string, data []byte) (Image, error) // ImagePNG, ImageJPEG, ImageWebP; ErrInvalidImage
type Image struct { MediaType string; Data []byte; Width, Height int; Digest string } // (Image).Check() error
const MaxImageBytes = 5 << 20; const MaxImageSide = 8000; const MaxImagesPerPrompt = 4
type Prompt struct { /* … */ Images []Image } // Seal 檢查、複製、算進 hash
type ImageInput interface { Backend; AcceptsImages() bool }

// Segmentation（SAM 3.1）
type Segmenter interface { Segment(ctx context.Context, req SegmentRequest) (Segmentation, error) }
type SegmentRequest struct { OrganizationID string; Role Role; Attribution Attribution; Image Image; Concepts []string }
type Segmentation struct { Width, Height int; Segments []Segment; Metadata SegmentMetadata }
type Segment struct { Concept string; ObjectID int; Box Box; Confidence float64 }
type Box struct{ X1, Y1, X2, Y2 int } // inclusive pixel corners of the image as sent
const MaxSegmentConcepts = 8; const MaxSegmentConceptBytes = 64; const MaxSegmentsPerConcept = 100
func NewSegmentClient(b SegmentBackend, budget Budget) (*SegmentClient, error)          // fake、Disabled
func NewMeteredSegmentClient(b BillableSegmenter, budget Budget, m Meter) (*SegmentClient, error) // live
// package fake: func NewSegmenter() *Segmenter — Script(concept, w, h, ...Segment)、Fail(concept, err)、Calls()
```

- 照片路徑的 judge 是一般的 `*provider.Client`：prompt 帶 `Images`，經 `GenerateModelProposal`（或 T-056 自己的 role，需要擴充 H-029 record 的角色，見 §9）；SAM 的結果以 T-056 的方式放進 prompt 的 untrusted block。
- `cmd/worker` 在啟動時建好 `deps.photo`（`photo{Judge *provider.Client; Segmenter *provider.SegmentClient}`），依 `config.AI.Vision`；T-056 的 handler 從 `deps.photo` 取用。關閉時兩者回 `provider_not_configured`／`policy_not_approved`（與 proposal 相同的 code），fake 模式是 `fake.New()` 與 `fake.NewSegmenter()`。
- 錯誤 code 與 proposal 相同（`invalid_request`、`invalid_output`（issue path `/concepts/<i>/…`）、`quota_exceeded`、`credentials_exhausted`、`rate_limited`、`unavailable`、`timeout`、`cancelled`、`concurrency_limited`、`policy_not_approved`、`provider_not_configured`）；計量的 attribution 同 proposal（`ai_proposal` target）。

## 4. 測試（先紅後綠）

每組測試先寫、先跑紅、再實作；紅與綠的輸出在 `checks/evidence/2026-10-26-T-055/`（`run.sh` 執行並在最後一行寫 exit code）。本機 server 的回答形狀取自文件的範例（`server_test.go`、`sam_test.go` 的 `docLine` 是 reading-segmentation 頁的範例行）。測試用的 key 是明顯的假字串（`LLM_000000000000_test-key-SENTINEL-not-a-real-key` 等），從不讀環境中的 key。

| 範圍 | 測試 | 紅 → 綠 |
|---|---|---|
| Meta 價格、來源、geo 只給 Anthropic | `spend/meta_test.go`（`TestTheMetaPrices`、`TestOnlyAnthropicPricesInferenceGeos`、`TestEveryPriceHasItsSource`）、`provider/listprice_test.go` | `red-spend.log`、`red-provider-listprice.log` → 全套件綠（`backend-test.log`） |
| 圖片 | `provider/image_test.go`（大小、型別、signature、尺寸、Check、`FuzzNewImage` 45 s、D-123 的上限）、`prompt_images_test.go`（Seal、hash、只交給接受圖片的 backend） | `red-provider-image.log`、`red-provider-prompt-images.log` → `green-provider-image.log`、`green-provider-prompt-images.log`、`fuzz-newimage.log` |
| Segmentation client | `provider/segment_test.go`（request／answer 檢查、逾時、計量、並行）、`fake/segment_test.go` | `red-provider-segment.log`（第一次 hang，改以 `-timeout 60s` 重跑）、`red-fake.log` → `green-provider-segment.log`、`green-fake.log` |
| H-029 record | `aiapproval/meta_test.go`（record、`Gap` 的每個 reason、Anthropic adapter 不收 Meta 的批准） | `red-aiapproval.log` → `green-aiapproval.log` |
| Muse Spark adapter | `meta/messages_test.go`：request 形狀（只有允許的 top-level 欄位、Bearer、沒有 `MODEL_API_KEY` 等環境預設）、圖片 part、quote（含超過 window）、stop reason 與 content block、另一個 model 的回答、開放值解碼（issue 的位置）、每種 error status（type allowlist、重試數、可能計費數）、被拒的 key（停用、402 冷卻）、backoff 與 Retry-After、spend（usage、cached、斷線、cut body、5xx、讀不了、超過 bound、另一個 type）、只有 metered client、逾時與取消、`New` 的拒絕、不跟隨 redirect、schema 超出上限不送、8 MiB 的邊界；`limits_test.go`（guard、每個 role 的投影）；`liveeval_test.go`（live eval 經本機 server：report 的 provider、價格來源、沒有 key 與回答文字） | `red-meta.log`（20 個 FAIL）→ `green-meta.log`、`green-meta-v.log`、`green-after-mutation.log` |
| SAM 3.1 adapter | `meta/sam_test.go`：request 形狀、文件範例 grammar、拒絕的輸出（約 30 種）、回答本身（未完成、另一個 model、function call、user message）、grammar 自己的拒絕、spend、I/O 前的拒絕、`NewSegmenter` 的拒絕、`FuzzParseSegmentation`（60 s，約 175 萬次） | 同上；`fuzz-parsesegmentation.log` |
| Architecture | `scripts/check-architecture/check_test.go`（meta 的 probe、synthetic graph 的 `live-adapters-apart`） | `red-architecture.log`（4 個 violation，exit 2）→ `green-architecture.log`、`green-architecture-tests-2.log`。**注意**：commit 303974b 的 `TestSyntheticGraph` 會失敗（新 rule 在 synthetic graph 中沒有任何 package，`green-wiring.log` exit 1）；之前的 `green-architecture-tests.log` 沒有記錄執行的指令，無法確認它是否包含該測試。339fea5 修正（synthetic graph 加 meta → anthropic 並要求該 violation）。 |
| 設定 | `config/meta_test.go`（meta 開啟、沒有開關、每種不合、照片路徑的組合與錯誤、`LiveEnabled` 只認自己 provider 的批准） | `red-config.log`（測試先寫，但紅是在實作之後以 `git archive` 的 303974b 副本執行的：未定義的符號）→ `green-config.log` |
| API、worker、eval 組裝 | `cmd/api/main_test.go`（meta 的可用性）、`cmd/worker/meta_test.go`（meta client 只經 ledger、照片路徑的 backend 與 code、啟動 log 沒有 key）、`cmd/ai-eval-live/main_test.go`、`aievals/meta_test.go`（report 的 provider 與價格來源、Main 拒絕 meta 的每種不合、沒有印出 key）、`aiapproval/metaeval_test.go`（Main 以 in-process backend 跑 meta：report、spend record、沒有 key） | `red-api-availability.log`、`red-worker.log`、`red-ai-eval-live.log`、`red-aievals.log`（report 寫成 `anthropic` 與 Anthropic 的價格頁）→ 對應的 `green-*.log` |
| Redaction | `aicontext/sanitize_test.go`、`observability/observability_test.go`（Meta key 的兩種形狀） | `red-redaction.log` → `green-redaction.log` |

既有測試的修改（沒有降低任何 assertion）：`aievals/live_test.go` 的 helper 加 `Provider: "anthropic"`（`Config.Provider` 成為必填），「AI_PROVIDER is not anthropic」的期望訊息改為新的「AI_PROVIDER is not a live provider」；`anthropic/liveeval_test.go` 加 `Provider: "anthropic"`；`anthropic/export_test.go` 加搬移後的 shim；`check_test.go` 的 synthetic graph 多一個 package 與一個必須出現的 violation。

## 5. Mutation testing

腳本在 session 的 scratch（`mutate.py`，不提交），只在 `git archive HEAD` 解開的副本上改檔、跑 `go test -count=1`、還原；環境沒有任何 AI 變數（不傳任何 key），`GOPROXY=off`。每個 mutant 是一處確切的替換（必須只出現一次）；KILLED＝測試失敗或無法 build。

- **第一輪**（commit 339fea5 的副本，`mutation.log`）：118 個 mutant，**88 killed、30 survived**，0 個 invalid。範圍：conn.go 19、meta.go 18、sam.go 10、grammar.go 10、limits.go 12、segment.go 17、image／prompt／provider／meter 13、config 12、aiapproval 7。
- 為存活的 mutant 補測試（commit c8a31fc，見 §4）；`meta.Backend` 的投影改為測試可替換的欄位（`project`，production 仍是 `structured.Project`），以測試 guard 自己的路徑。
- **第二輪**（c8a31fc 的副本，只跑第一輪存活的 30 個，`mutation-2.log`）：log 記為 25 killed、5 survived；**review 第 1 輪 L1 更正為 24 killed、6 survived**：log 中 G10 的「KILLED」是 `TestRetriesBackOff` 的隨機失敗（「an HTTP date」用牆上時鐘），G10 實際上存活且是等價 mutant（見下表）；S10 的「KILLED」（第一輪 `mutation.log`）同樣出自那次隨機失敗，但 reviewer 重跑後確認它確實被 `TestSAMRefusesBeforeAnyRequest` 殺死，所以 S10 維持 killed。原始的 log 不改，更正記在這裡與「Review round 1 fixes」。

仍存活的 6 個，判斷為等價（不加 assertion 也無法區分，所以沒有降低或繞過測試）：

| Mutant | 為什麼等價 |
|---|---|
| C12 超過 8 MiB 的 200 當作回答讀 | 超過 bound 時 `attempt` 不保留 body，解碼一定失敗，結果同樣是 `unavailable`、undecoded、至少整個預留（`TestTheResponseBound` 的 +1 byte 案例兩者相同） |
| S8 `NewSegmenter` 不檢查 `AllowsImages()` | `aiapproval.metaGap` 對有 segmentation model 的 record 要求資料政策列出圖片，否則 record 不完整、`Recorded` 不產生 token；所以有效的批准一定 `AllowsImages()`（縱深防禦） |
| G1 grammar 接受換行 | 之後的每個 token（ID、box 欄位、mask payload `[\x21-\x7e]`）都不接受換行，結尾的換行先被 `TrimSpace` 去掉 |
| G8 box token 不檢查 `\|` | box 的欄位由 `boxRE` 嚴格比對（只有數字與固定名稱），`\|` 一定不符 |
| G10 `grammar.go:117` 改為 `if false {`（review 第 1 輪 L1 更正） | `maskToken`（`grammar.go:151`）只在文字結尾或「`,<id><\|box;`」之前結束一個 mask，所以 `:117` 的分支到不了；SAM 與 grammar 的測試全部通過 |
| F7 月上限取 max 而非 min | 本 build 的兩個 record（H-018、H-029）預算都是 USD 100，min 與 max 相同；config 的測試套件無法登記其他 record（`RecordForTest` 只在 aiapproval 的測試中）。預算不同時才可區分 |

第一輪 killed 的例子：504 被重試（C1）、5xx 不計費（C2）、402 永久停用（C4）、跟隨 redirect（C6）、多一次重試（C7）、忽略 Retry-After（C8）、等待超過 deadline（C10）、任何 error type 都記（C11）、接受另一個 model 的回答（B1、S3）、沒有預留就呼叫（B3、S10）、沒有圖片政策也送圖片（B4、S6）、`store: true`（S1）、不要 confidence（S2）、box 在邊上（K3）、confidence > 1（K4）、只預留一次 attempt（K9）、可能計費的 attempt 免費（K11）、讀不了的回答低於預留（K12）、圖片給任何 backend（I9、I10）、每個 model 都乘 geo（I11）、圖片不算進 hash（I13）、另一個 provider 的批准（F1、B15）、key 形狀不檢查（F2）、照片路徑不要求圖片（F4、F6）、Contributor model 進 record（R1）、retention 不要求（R2）。完整清單在 `mutation.log`。

## 6. 指令與結果

Go 1.27.3（`/root/go/pkg/mod/golang.org/toolchain@v0.0.1-go1.27.3.linux-amd64/bin`，`GOTOOLCHAIN=local`），在 `checks/evidence/2026-10-26-T-055/run.sh` 中執行（輸出寫進 log，最後一行是 exit code）。最後一輪在 commit 1c0b309（加上本報告）。

| 指令 | 結果 | Log |
|---|---|---|
| `gofmt -l cmd internal schemas scripts prompts`（`make backend-test` 的第一步） | 沒有輸出 | `backend-test.log` |
| `go vet ./cmd/... ./internal/... ./schemas/... ./scripts/... ./prompts/...`（同上） | exit 0 | `backend-test.log` |
| `make backend-test GOFLAGS=-count=1` | **exit 0**（79 個 package ok、12 個沒有測試檔；DB 測試在沒有 database 時 skip，由 integration-test 執行） | `backend-test.log` |
| `make architecture-check` | **exit 0**：`PASS architecture: 91 packages, 25 rules, 16 build configurations` | `architecture-check.log` |
| `go test -count=1 ./scripts/check-architecture/` | **exit 0** | `green-architecture-tests-2.log` |
| `make ai-eval` | **exit 0**（fake provider 的 context 與 review suite；`checks/ai-evals/fake-*.json` 沒有變） | `ai-eval.log` |
| `go test -race -count=1 ./internal/ai/... ./internal/aievals/... ./internal/config/... ./cmd/...` | **exit 0**（18 個 package ok，沒有 data race） | `race.log` |
| `make spec-check PYTHON=/home/user/Geminus/.venv/bin/python` | **exit 0**（decisions、planning、docs、deploy 等全部 PASS；`checks/*-check.json` 重新產生） | `spec-check.log` |
| `make integration-test`（新建的 `dt_test_t055a`，共用 cluster 的 socket，沒有啟動或停止它） | **exit 0**（db-test 與 db-go-test，37 個 package ok，`GEMINUS_REQUIRE_DB_TESTS=1` 所以沒有 skip） | `integration-test.log` |
| `go test -count=1 -v -run TestMetaChargesAreLedgered ./internal/storage/aispend/`（同一個 database） | **exit 0**（PASS） | `aispend-meta-db.log` |
| `make integration-test`（再一個新建的 `dt_test_t055b`，在 1c0b309，含上一項的新測試） | **exit 0**（37 個 package ok，含 `internal/storage/aispend`） | `integration-test-final.log` |
| `python3 scripts/check_deploy.py`、`python3 scripts/rehearse_railway_deploy.py` | exit 0、`rehearsal: PASS`（Railway 本身 NOT RUN） | （於改名後執行，輸出未存檔；`spec-check.log` 含 deploy check） |
| Fuzz：`FuzzNewImage`（45 s）、`FuzzParseSegmentation`（60 s，約 175 萬次） | 沒有失敗 | `fuzz-newimage.log`、`fuzz-parsesegmentation.log` |
| Mutation（§5） | 第一輪 118：88 killed／30 survived；第二輪 30：24 killed／6 survived（等價；log 寫 25／5，G10 的 kill 是 `TestRetriesBackOff` 的隨機失敗，review 第 1 輪 L1 更正） | `mutation.log`、`mutation-2.log` |

兩個 `dt_test_t055*` database 在最後都已 drop。

## 7. NOT RUN

- **每一個 live 步驟（NOT RUN）**：對 Meta Model API 的任何 request（`POST /v1/messages`、`POST /v1/messages/count_tokens`、`POST /v1/responses`、model listing）；`make ai-smoke-live` 與 `make ai-eval-live`（`AI_PROVIDER=meta` 或 `anthropic`）；SAM 3.1 對真實圖片的 segmentation；Muse Spark 是否接受投影後的 schema、是否寫出開放值的 JSON text、回答的 `model` 是否就是 request 的 model（Standard mode 的實際行為）；`usage` 與 `images_processed` 的實際值；5xx 是否真的計費；rate limit 與 Retry-After 的實際值。H-033：只在獨立審查過的 T-055 程式上、由有 key 的新 session 執行。
- 沒有讀取、印出或 grep 任何 key 的值；沒有確認 `GEMINUS_MUSE_API_KEY` 是否存在於任何環境（名稱由 coordinator 轉達）。
- `make e2e-test`、frontend 的測試：依指示不執行。
- `make backend-race`（整個 repository 的 `-race`）：沒有執行；只執行了指定範圍的 `-race`（§6）。
- govulncheck：沒有執行，因為 `go.mod`、`go.sum` 沒有改變（`git diff d9b7f22 HEAD -- go.mod go.sum` 為空），沒有新的 dependency。
- 部署：Railway 上的任何操作（只執行了 `check_deploy.py` 與以 stand-in 進行的 rehearsal）。
- API 的 key-free 可用性（T-053 I5）：T-055 時沒有實作（§9 問題 6）；之後由 follow-up 實作（§10）。
- 照片路徑的 handler、prompt 與 UI：T-056 的範圍；本 task 只提供介面與 worker 中建好的 client。Claude adapter 的圖片：沒有實作。
- 獨立審查（H-029 (4) 的 hackathon 模式要求每個 demo 切片一輪）：尚未進行。
- 已知限制：照片路徑的 client 與 proposal 的 client 各自計算每個 organization 的並行數（同一個 process 內最多兩倍）；Meta 的兩個 backend（Muse Spark、SAM）各有自己的 key 狀態（一個被 401 停用時，另一個在自己的下一次 401 才停用）。

## 8. Commit

Branch `t055-meta`（自 d9b7f22，沒有 push）：

| Commit | 內容 |
|---|---|
| e8ee746 | 投影與解碼搬到 `internal/ai/provider/structured`（內容不變） |
| 19fb02a | Meta 價格、prompt 的圖片、segmentation 介面與 fake（先寫測試） |
| 303974b | Meta adapter（Muse Spark、SAM 3.1）、H-029 record、architecture rule（先寫測試） |
| 339fea5 | `AI_PROVIDER=meta`、照片路徑的設定、worker／API／ai-eval-live 的組裝、redaction、部署的改名、文件與決策（先寫測試） |
| c8a31fc | Mutation 存活者的測試、`Backend.project` 欄位 |
| 1c0b309 | Ledger 的 DB 測試、ARCHITECTURE、驗證 log |
| （本報告的 commit） | `checks/T-055_VALIDATION.md` 與最後的 integration log |

## 9. 給 owner／coordinator 的問題

1. **ADR-030、D-121–D-123 是否接受**（全部 PROPOSED）。D-121 的兩個安全側選擇：每個 5xx 算可能計費（文件沒有說不計費）、有圖片的呼叫以整個 context window 預留。**數字更正（review 第 1 輪 M1）**：原本寫「每個 attempt 約 1.35 美元；日上限 10 美元下約 7 次照片 proposal 的預留」是錯的。預設 `MaxRetries` 2、`MaxOutputTokens` 8192 下，一次照片 proposal 預留 3 × (1,048,576 × 1,250 + 8,192 × 4,250) = 4,036,608,000 nano-USD（約 **USD 4.04**）；部署日上限 USD 10 下同時至多 **2** 個；實際的限制是 **organization 日上限 USD 5**：只容得下 **1** 個，而且只在該 organization 當天 committed ≤ 約 USD 0.96 時。可選的處理：以 `POST /v1/messages/count_tokens` 先算（D-121 選項 C）、估算公式（選項 B，可能少記），或把 `AI_ORGANIZATION_DAILY_LIMIT_USD` 提高到日上限。**之後：owner 選 count_tokens 加 10%（H-037），review 第 1 輪的修正實作**：demo 大小的照片 proposal 以悲觀的 count 估計預留約 USD 0.27（連同 SAM 的預留在 organization 日上限內 15 次）；細節見「Review round 1 fixes」。
2. **Meta 的資料保留條款**（D-122 (b)）：Standard Services 不以內容訓練，但 Meta 為提供服務、法律、被系統標記、安全與政策審查保留內容（ToS §4，沒有 ZDR 的選項）。H-029 選了 Standard mode；是否也接受這些保留條款？record 目前記為 `meta_standard_services`（H-029 (1)），若 owner 要求另一次確認，改為 `retention_unconfirmed` 直到確認。（**已由 H-035 (1) 回答**：owner 接受 2026-10-09 版的 Terms of Service；review 第 1 輪 L4 把 record 的確認來源改為 H-035 (1)。）
3. **Meta dashboard 的 spend limit**（D-122 (c)）：是否有、是否設定？目前只靠程式內的 ledger（月 100、日 10、organization 日 5 美元）。H-033 提到的 150 美元額度（10 天）不是上限。
4. **Claude 作為照片路徑的備援**（D-123 (c)）：H-018 的資料政策只有 synthetic 資料與使用者文字，沒有圖片；照片路徑選 Claude 時維持關閉（`images_not_approved`）。是否擴充 H-018 到 demo 圖片？（Claude adapter 也還沒有實作圖片，屬 T-056 或之後。）（**已由 H-035 (3) 回答**：先不要，照片只走 Meta。）
5. **照片路徑的 role**：H-029 record 只服務 `modeler`、`scenario`；T-056 若要新的 role（例如 photo modeler），需要 reviewed change 擴充 record 的角色（與 migration 0016 的 role CHECK）。
6. **API 的 key-free 可用性**（T-053 I5）：沒有在 T-055 處理。部署的 API 仍維持 `AI_PROVIDER=disabled`，所以 demo 的 proposal（文字與照片）無法經由部署的 API 使用；要做的話需要決定 API 是否讀開關（`variables.json` 目前禁止 API 有 `GEMINUS_AI_LIVE`，`check_deploy.py` 檢查）。這是安全設計的選擇，留給 coordinator／owner。（之後：coordinator 決定 API 讀這四個設定、不讀 key，見 §10。）
7. **Geographic Use Policy**：Restricted Territories 包含中華人民共和國（含香港、澳門）等；「you may not … knowingly permit access by End Users located in」這些地區。公開 demo 以 owner 的 Auth0 登入保護，是否需要其他限制由 owner 決定。
8. **決策編號**：D-118 已由 T-052 使用；本 task 用 D-121–D-123，D-119、D-120 留給同時進行的 T-056。合併時若衝突請 coordinator 重新編號（程式註解中的編號一起改）。
9. **Planning 的舊變數名稱**：`planning/tasks.json` T-055 scope 與 `planning/decisions.json` H-029 still_open 仍寫 `GEMINUS_META_API_KEY`（coordinator 的紀錄，本 task 沒有改）。
10. **Live 的第一步**（H-033：只在獨立審查過的 T-055 程式上、由有 key 的新 session）：建議順序為免費的 `count_tokens`（確認 key、投影的 schema 被接受）→ `make ai-smoke-live`（`AI_PROVIDER=meta`）→ `make ai-eval-live`；SAM 的第一次呼叫在 T-056 的 handler 之前以一張 demo 圖片確認 grammar 與 `images_processed`。

## Coordinator 驗證（2026-10-26）

`t055-meta` 的七個 commit（e8ee746..c3307cf）cherry-pick 到 T-052 的修正之後，沒有衝突；之後 bce77a3 把 T-055 scope 中舊的 key 名稱改為 `GEMINUS_MUSE_API_KEY`。

指令與結果（evidence：`checks/evidence/2026-10-26-T-055-T-052r1-coordinator/`，`exit-codes.txt` 依執行順序；go1.27.3）：

| 指令 | exit | 說明 |
|---|---|---|
| `make spec-check` | 0 | |
| `make backend-test GOFLAGS=-count=1` | 0 | 81 個 package ok |
| `make architecture-check` | 0 | 93 packages、25 rules、16 build configurations |
| `make ai-eval` | 0 | fake provider |
| `go run ./scripts/gen-electronics-line-data -check` | 0 | 資料與釘住的 hash 不變 |
| `go test -race -count=1 -timeout 30m`（ai、aievals、config、domainpack、results、cmd、generator） | 0 | 26 個 package ok |
| `scripts/rehearse_railway_deploy.py`（沒有 Railway token 的環境） | 0 | |
| `deploy/railway/deploy.sh --dry-run`（同上） | 0 | |
| `make integration-test`（新的 dt_test_hkb4c，跑完刪除） | 0 | |
| 舊的 key 名稱掃描（`GEMINUS_META_API_KEY`，排除測試、報告與決策紀錄） | 0 | 只剩刻意的負向測試與歷史紀錄 |

NOT RUN：任何 live 呼叫；前端與 e2e（這兩批沒有改 UI）；完整的 `make backend-race`。

Coordinator 對本任務問題的處理：
1. ADR-030、D-121–D-123 維持 PROPOSED，比賽後放進決策頁；保守的選擇（5xx 一律算可能收費、帶圖的呼叫預留整個 context）照用。
2. Meta 的保留條款：owner 已接受（H-035，coordinator 核對過 2026-10-09 版的原文）。
3. Meta dashboard 的花費上限：請 owner 確認（H-035 的未決事項）；在那之前只有程式內的上限。
4. 照片的 Claude 備援：owner 決定先不要（H-035）。
5. 照片路徑的角色：T-056 合併時確認。
6. API 不讀 key 也能判斷 AI 是否可用（T-053 I5）：coordinator 決定實作（API 只讀非 secret 的設定：provider、approval、model、`GEMINUS_AI_LIVE`；環境中出現任何 key 就拒絕啟動），由後續的切片實作並驗證；這不讓 API 拿到任何 secret。
7. Restricted Territories：owner 決定公開 demo 關閉註冊、只有 owner 與一個評審帳號（H-035）。
8. 決策編號沒有衝突（T-052 為 D-118，T-055 為 D-121–D-123，T-056 為 D-130–D-132）。
9. Planning 中舊的變數名稱：T-055 scope 已改；H-029 的紀錄保留原文。
10. 第一批 live 步驟（count_tokens → `AI_PROVIDER=meta` 的 smoke → eval → 一次 SAM）在獨立審查通過之後、由有 key 的新 session 執行。

下一步：一輪獨立審查（H-029），進行中。

## 10. Follow-up：API 的 key-free 可用性（T-053 I5）

2026-10-26，branch `t055-apiavail`（自 bce77a3，沒有 push）。只用 fake、本機測試與一次性 database；**沒有任何 live AI 呼叫**，沒有讀取、印出或 grep 任何 key 的值，沒有確認任何 key 變數是否存在於任何環境。證據在 `checks/evidence/2026-10-26-T-055-apiavail/`。

### 10.1 決定（**coordinator 的決定**，不是 owner 的）

API 只以非 secret 的設定判斷是否接受 AI proposal：`AI_PROVIDER`（`anthropic` 或 `meta`）、`AI_POLICY_APPROVAL`（批准紀錄的 ID）、`AI_MODEL`（必須等於 record 的 model 且有固定價格）、`GEMINUS_AI_LIVE=on`。檢查與 worker 的 live gate 相同，只是 API 從不讀、也不要求任何 key。Defense in depth：API 的環境中有任何 AI key 的變數即拒絕啟動，typed code，訊息只寫名稱。任何不合時維持既有的 typed 可用性（`PolicyNotApproved`／`NotConfigured`）；開關 on 而不合時 API 拒絕啟動，與 worker 相同。Worker 不變。API 接受而 worker 不能呼叫（沒有 key）時，proposal 在 worker 以 typed status 安全地失敗。

### 10.2 行為

- **同一個 gate**：`internal/config` 的 `liveGate` 由原本的 `loadLive` 拆出、內容不變：build 記錄的批准、屬於該 provider（否則 `provider_mismatch`）、record 不缺任何項目（`Gap`）、`AI_MODEL` 等於 record 的 model（`model_mismatch`；沒有時 `required`）、model 與 segmentation model 有價格（`model_unpriced`）、開關（`switch_off`）。Worker 的 `loadLive` 在 gate 通過後才讀 key（行為不變）；API 的 `config.LoadAPIAI` 只用 gate，不讀 key。`AI_PROVIDER` 與開關的解析抽成共用的 `aiProvider`、`switchOn`（`LoadAI` 的錯誤與順序不變）。
- **`config.APIAI`**：`Provider`、`Approval`（只有 `aiapproval.Recorded` 能產生的 token）、`Model`、`LiveOff`（與 `AI.LiveOff` 相同的 typed reason）；沒有 credential 欄位。`LiveEnabled` 只對屬於該 provider 的有效 token 成立。`config.LoadAPI` 是以 `LoadAPIAI` 取代 `LoadAI` 的 `Load`；worker 仍用 `Load`／`LoadAI`。
- **Key 變數**（`internal/config/apiai.go` 的 `apiKeyVars`）：`GEMINUS_ANTHROPIC_API_KEY_A`、`GEMINUS_ANTHROPIC_API_KEY_B`、`GEMINUS_MUSE_API_KEY`、`GEMINUS_META_API_KEY`、`ANTHROPIC_API_KEY`、`ANTHROPIC_AUTH_TOKEN`、`AI_API_KEY`、`MODEL_API_KEY`。不論其他設定，有值即 `key_forbidden`（exit 1），一個變數一個錯誤、排在其他錯誤之前；值只與空字串比較後即丟棄。`GEMINUS_META_API_KEY`（Meta key 的舊名稱）與 `ANTHROPIC_AUTH_TOKEN`（Anthropic SDK 預設讀取的 credential）是依 coordinator 清單的「…」補上的。`AI_API_KEY` 在 API 上由 `invalid` 改為 `key_forbidden`（worker 仍為 `invalid`）。
- **`cmd/api`**：`run` 用 `LoadAPI` 與 `LoadAPIAI`；`aiAvailability(APIAI)`：fake → `Available`；`LiveEnabled` → `Available`；其他 live provider → `PolicyNotApproved`；其餘 → `NotConfigured`。啟動 log（live provider 時）：`AI proposals are accepted; the worker calls the provider`（`provider`、`model`、`code` `LIVE_AI_ON`），或 `AI proposals are refused`（`code` 為大寫的 typed reason，例如 `SWITCH_OFF`、`MODEL_MISMATCH`、`NO_APPROVAL_NAMED`）。API 不讀 `AI_VISION_*`、花費上限與 budget（worker 的設定）。
- **API 接受、worker 不能呼叫**：worker 開關 on 而沒有 key → worker 拒絕啟動（`GEMINUS_MUSE_API_KEY (required)`，或 Anthropic 的兩把），不建 handler、不連線，所以不 claim job、不寫入；proposal 停在 `generating`，直到有 worker 啟動（沒有逾時，見 10.9）。worker 開關不是 on → `AI_POLICY_NOT_APPROVED`；`AI_PROVIDER` disabled → `AI_PROVIDER_NOT_CONFIGURED`；key 被 provider 拒絕 → `AI_CREDENTIALS_EXHAUSTED`（HTTP 層 `SERVICE_UNAVAILABLE`）。都在第一個 attempt 失敗、不重試；只寫 proposal 的 error、job 的狀態與三個 audit event（`proposal.create`、`proposal.attempt_failed`、`job.attempt_failed`），沒有內容、patch、risk、validation、provider metadata、version、draft、scenario 或花費預留。

### 10.3 改了什麼

| 檔案 | 內容 |
|---|---|
| `internal/config/apiai.go`（新） | `APIAI`、`LoadAPIAI`、`CodeKeyForbidden`、`apiKeyVars` |
| `internal/config/ai.go` | 抽出 `aiProvider`、`switchOn`、`liveGate`（行為不變）；註解 |
| `internal/config/config.go` | `Load`／`LoadAPI` 共用 `load`；`AILive` 的註解 |
| `cmd/api/main.go` | `LoadAPI`、`LoadAPIAI`、`aiAvailability(APIAI)`、`logAI` |
| `deploy/railway/variables.json`、`scripts/check_deploy.py` | 見下 |
| `deploy/railway/deploy.sh` | 只改開頭的註解（owner 在 API 與 worker 設相同的四個設定） |
| `checks/deploy-check.json` | 重新產生（多了 `api_ai`） |
| runbook §4.4、§7.3；SECURITY §7；ADR-027 addendum 一項；ADR-030 Consequences 一句 | 文件 |

**給 coordinator 合併用**（T-039 review 第 1 輪同時在改 `railway_deploy.py` 與 rehearsal；這兩個檔案我沒有改）：

- `variables.json`：`about` 的「(no AI key or live switch on the API,」改為「(no AI key on the API, whose AI settings are the worker's (T-053 I5),」；api 的 `AI_PROVIDER` 只改 `later` 的文字（值仍為 `disabled`、`when: absent`）；api 新增 `AI_POLICY_APPROVAL`、`AI_MODEL`、`GEMINUS_AI_LIVE`，三者皆 `{"secret": false, "source": "owner", "required": false}`；api 的 `forbidden` 移除 `GEMINUS_AI_LIVE`，新增 `GEMINUS_META_API_KEY`、`ANTHROPIC_API_KEY`、`ANTHROPIC_AUTH_TOKEN`、`MODEL_API_KEY`；worker 的 `AI_PROVIDER` 的 `later` 句尾加「; the API's four AI settings are these (T-053 I5)」；worker 新增 `AI_POLICY_APPROVAL`、`AI_MODEL`（同上的宣告；worker 本來就讀它們）；web 不變。
- `check_deploy.py`：docstring 的 variables.json 項；`railway()` 最後原本的 `ai_keys` 檢查（要求 API 禁止 `GEMINUS_AI_LIVE`）換成：API 的 `forbidden` 包含 `apiKeyVars` 的每一個（由 Go 原始碼讀取）、web 的 `forbidden` 包含三把 live key、四個 AI 設定在 API 與 worker 都有宣告且除 `later` 外完全相同、不是 secret、API 沒有其他 `AI_`／`GEMINUS_AI_` 設定；新增常數 `API_AI_SETTINGS`、`LIVE_KEYS` 與函式 `api_key_variables()`；`railway()` 的回傳多 `api_ai`。
- Rehearsal（沒有改，仍 PASS）：第 233 行把 `GEMINUS_AI_LIVE` 算成「the API got an AI key or the live switch」，docstring 也寫 API 不會拿到開關。現在 owner 可以在 API 設開關；deployer 仍從不設定 owner 的變數（第 229–231 行的檢查涵蓋），所以檢查照樣通過。建議改 rehearsal 的 agent 拿掉第 233 行的 `GEMINUS_AI_LIVE` 與 docstring 的「or the switch」，並可加上 deployer 不設 `AI_POLICY_APPROVAL`、`AI_MODEL` 的檢查。

### 10.4 測試（先紅後綠）

| 範圍 | 測試 | 紅（以舊行為的暫時 stub：API 的 AI 即 `LoadAI`；stub 沒有 commit）→ 綠 |
|---|---|---|
| `internal/config` | `TestAPIAIIsKeyFree`（Meta 與 Anthropic 開關 on、沒有 key → 接受；只讀四個設定與 `APP_ENV`；worker 的 `LoadAI` 以同樣設定仍要求 key；`LiveEnabled` 的五種手造值）、`TestAPIAIMismatches`（14 種不合 × 開關 on／不設／off）、`TestAPIAISelection`（16 種 provider 與開關；7 種不合法的開關值）、`TestAPIAIRefusesAnyKey`（8 個變數 × 6 種設定，錯誤中沒有值；全部同時：每個一個錯誤、排在前面）、`TestLoadAPI` | `red-config.log`：IsKeyFree、RefusesAnyKey、LoadAPI 失敗 → `tests.log` |
| `internal/config/aiapproval` | `TestTheAPIsGateIsTheWorkers`：H-018 的 16 種、H-029 的 18 種 record 變動 × 8 種設定（272 次比較），API（沒有 key）與 worker（有 key）的錯誤碼、live、typed reason、批准與 model 相同；開關 on 時每種變動的預期 reason | `red-aiapproval.log`：完整的 record 在 API 要求 key → 綠 |
| `cmd/api` | `TestAPIAvailability`（13 種設定；手造的 `APIAI` 不會 available）、`TestAPIAvailabilityNeedsNoKey`（`run` 開關 on、沒有 key → 走到 database；log 的 provider、model、code）、`TestAPIAvailabilityRefusesAMismatch`（7 種，exit 1、typed code、不碰 database）、`TestAPIRefusesToStartWithAKey`（8 個變數 × 3 種設定，exit 1、`key_forbidden`、沒有值） | `red-api.log`：前兩個與 key 的測試失敗（RefusesAMismatch 在舊行為也成立，作為 regression） |
| `cmd/worker` | `TestAProposalTheAPIAcceptsWithoutTheWorkersKey`：API 接受的設定，worker 沒有 key、開關 on → exit 1、沒有建 handler、不連線；開關不設 → client 回 `policy_not_approved`（不可重試），不碰 ledger | `red-worker.log` → 綠 |
| `internal/proposals/generator`（DB） | `TestAProposalTheWorkerCannotAskFailsSafely`：三種 worker（live off、disabled、key 被拒）→ failed、typed code、第一個 attempt、row 只有 error、恰好三個 audit event、model 的 version／draft／scenario 與花費預留不變 | 針對既有程式的安全性質（沒有紅）；第一次在 `dt_test_t055fa` 失敗，是我的 assertion 漏了 worker loop 自己的 `job.attempt_failed`（D-091 (b)）；改為精確檢查該 event（code、outcome、attempt、retryable）與總數 3，沒有放寬 → `generator-db-keyless.log` PASS |

紅的 log 是以 stub 執行的行為紅；更早一次只是 build 失敗的紅（函式還不存在），其 log 被同名覆蓋，沒有保留。

### 10.5 Mutation（65a61c3 的 `git archive` 複本，在 scratch；`mutate.py`、`mutation.log`、`mutation-a6.log`）

46 個 mutant（`LoadAPIAI` 的 key 檢查 10、選擇與 gate 14、`LiveEnabled` 4、共用的 gate 9、`cmd/api` 9），每個跑 `./internal/config/... ./cmd/api/ ./cmd/worker/` 的測試：**44 killed、2 survived（等價）**。

- 第一次執行時 S1–S3 因未使用的變數而 build 失敗，被誤算為 killed；我停掉那次執行，修正 S1–S3 與 S10 使其能編譯，讓 runner 把 build 失敗另列為 INVALID，然後整批重跑（`mutation.log`）。重跑中 A6 為 INVALID（`strings` 未使用），改為 `ToLower` 後單獨重跑：killed（`mutation-a6.log`；該 log 末行的「46 mutants」是清單長度，實際只跑 A6）。
- 存活的兩個是等價的：**L3**（`APIAI.LiveEnabled` 拿掉 `Approval.Valid()`）：`Approval.Provider()` 對無效的 token 回空字串，而 live provider 的名稱不是空字串，結果相同（`AI.LiveEnabled` 是同樣的寫法）。**A4**（`run` 拿掉 `LoadAPIAI` 錯誤的合併）：`LoadAPI` 以同一個 getenv 呼叫同一個 `LoadAPIAI`，錯誤已經包含在內。
- `check_deploy.py` 的新規則：16 個 negative case（`check_deploy_negatives.py`，只在記憶體中改 `variables.json` 或 Go 的清單），全部如預期失敗，原檔通過（`check-deploy-negatives.log`）。

### 10.6 指令與結果（Go 1.27.3、`GOTOOLCHAIN=local`；Python `.venv/bin/python -I`；deploy 腳本以 `env -u RAILWAY_TOKEN -u RAILWAY_API_TOKEN` 執行）

| 指令 | 結果 | Log |
|---|---|---|
| `gofmt -l cmd internal schemas scripts prompts` | 空，exit 0 | `gofmt.log` |
| `go vet ./internal/config/... ./cmd/... ./internal/proposals/...` | exit 0 | `vet.log` |
| `go test -count=1 ./internal/config/... ./cmd/... ./internal/proposals/...` | exit 0（10 個 package ok；DB 測試在這裡 skip） | `tests.log`、`green-new-tests-v.log` |
| `go test -race -count=1`（同上） | exit 0 | `race.log` |
| `make backend-test` | exit 0（gofmt、vet、81 個 package ok、0 FAIL、沒有 cached） | `backend-test.log` |
| `make architecture-check` | exit 0（93 packages、25 rules、16 build configurations） | `architecture-check.log` |
| `make spec-check PYTHON=/home/user/Geminus/.venv/bin/python` | exit 0（只有 `checks/deploy-check.json` 改變） | `spec-check.log` |
| `scripts/check_deploy.py` | exit 0 | `check-deploy.log` |
| `check_deploy_negatives.py` | exit 0（16/16） | `check-deploy-negatives.log` |
| `scripts/rehearse_railway_deploy.py` | exit 0、`rehearsal: PASS`（Railway 本身 NOT RUN） | `rehearsal.log` |
| `make integration-test`（新建的 `dt_test_t055fa`，commit 之前） | exit 2：只有 `TestAProposalTheWorkerCannotAskFailsSafely` 失敗（10.4 的 assertion），其他全部 ok | `integration-test-a.log` |
| `go test -race -run TestAProposalTheWorkerCannotAskFailsSafely ./internal/proposals/generator/`（同一個 database，修正後） | exit 0（3 個 subtest PASS） | `generator-db-keyless.log` |
| `make integration-test`（新建的 `dt_test_t055fb`，在 0ef69ed） | **exit 0**（db-test 與 db-go-test，37 個 package ok，`GEMINUS_REQUIRE_DB_TESTS=1`，沒有 FAIL 或 SKIP；含 `TestAProposalTheWorkerCannotAskFailsSafely`） | `integration-test.log` |
| Mutation（10.5） | 44 killed、2 等價 | `mutation.log`、`mutation-a6.log` |

PostgreSQL 是共用的 cluster（socket `/var/lib/postgresql/geminus-t009-fresh`，port 55440），沒有啟動或停止它；兩個 `dt_test_t055f*` database 在最後都已 drop。

### 10.7 NOT RUN

- 任何 live AI 呼叫（Anthropic、Meta）；`make ai-smoke-live`、`make ai-eval-live`。
- Railway 上的任何操作；部署後 API 與 worker 的四個設定值是否相同（owner 在 UI 設定，靜態檢查與 rehearsal 都看不到值；deployer 也不讀 owner 的變數）。
- `make e2e-test`、frontend 的測試：依指示不執行。
- `make backend-race`（整個 repository 的 `-race`）：沒有執行；只執行了指定範圍的 `-race`。
- govulncheck：`go.mod`、`go.sum` 沒有改變，沒有新的 dependency。
- 獨立審查：尚未進行。

### 10.8 Commit（branch `t055-apiavail`，自 bce77a3，沒有 push）

| Commit | 內容 |
|---|---|
| 65a61c3 | `LoadAPIAI`、共用的 gate、`LoadAPI`、`cmd/api` 的組裝與 log；全部測試（先寫測試） |
| bdc9b15 | `variables.json`、`check_deploy.py`、`deploy.sh` 的註解、`deploy-check.json`、negative cases |
| 0ef69ed | runbook §4.4／§7.3、SECURITY §7、ADR-027 addendum、ADR-030 |
| （本節的 commit） | 本節與驗證 log |

### 10.9 給 coordinator 的說明與問題

1. **決策的紀錄**：這是 coordinator 的決定；我沒有在 `docs/DECISIONS_AND_TBD.md` 新增 D 編號（避免與同時進行的 task 衝突），ADR-027 addendum 標為 PROPOSED 至獨立審查。要不要登記為 D-xxx 由 coordinator 決定。
2. **Key 清單**：`GEMINUS_META_API_KEY`、`ANTHROPIC_AUTH_TOKEN` 是補上的（10.2），請確認。
3. **等待的 proposal**：worker 開關 on 而沒有 key 時 worker 不啟動，API 接受的 proposal 停在 `generating`，沒有逾時（依指示 worker 側不變）。部署的 verification 會看到 worker 不是 SUCCESS；是否給 generating 的 proposal 一個期限，另行決定。
4. **空字串**：key 變數以「有值」判斷（`getenv` 無法分辨未設與設為空字串）；設為空字串的變數不視為 key。
5. **照片路徑**：API 不讀 `AI_VISION_*`；T-056 若需要 API 知道照片路徑是否可用，另行決定（同樣的方法可以延伸）。
6. **Planning 與舊報告**：`planning/tasks.json` T-039 scope (3)（「API 的部署不設 key，開關 on 時 config 會讀它們」）與 `checks/T-053_VALIDATION.md` 的 I5 是當時的描述，我沒有改（coordinator 的紀錄）。

## Coordinator 驗證：§10 的 API key-free availability（2026-10-26）

`t055-apiavail` 的四個 commit（65a61c3..1e893be）cherry-pick 到已合併 T-056 的主線；只有本報告衝突（兩邊都在最後加了一節，兩節都保留）。合併後與 T-039 review 第 1 輪修正的部署檔衝突，處理見 `checks/T-039_VALIDATION.md` 最後一節。驗證結果與 T-039、T-031 的修正一起記錄在該節（evidence：`checks/evidence/2026-10-26-T-039r1-T-031r1-apiavail-coordinator/`；backend-test、race（cmd、config、proposals）、integration、部署檢查、rehearsal 都是 exit 0）。

Coordinator 對 §10 問題的處理：
1. 決策編號：D-124（PROPOSED，coordinator 的決定，待獨立審查；與 API 的照片可用性一起在 T-056 的審查中看）。
2. Worker 開關 on 而沒有 key 時 proposal 停在 `generating`：demo 可以接受；部署驗證會看到 worker 沒有執行，比賽後再考慮逾時。
3. 空字串的 key 變數不算存在：接受（空值無法呼叫 provider，API 也不會用到）。
4. T-039 scope (3) 與 T-053 報告的 I5 已更新（1425e3a）。
5. 多拒絕的 `GEMINUS_META_API_KEY`（舊名稱）與 `ANTHROPIC_AUTH_TOKEN`：接受，只會讓 API 在多一種 key 出現時拒絕啟動。

## Review round 1 fixes（2026-10-26）

Branch `t055-r1`（自 1425e3a，本機，沒有 push、沒有 PR）。修正 `checks/T-055_REVIEW_r1.md` 的 M1、L1–L5，並實作 owner 的 H-037。證據在 `checks/evidence/2026-10-26-T-055-r1/`（`run.sh` 執行指令、以名稱移除所有 AI 與部署變數（不讀值）、log 最後一行是 exit code）。

- **沒有任何 live 呼叫**：沒有對 `api.meta.ai`、Anthropic 或任何 provider 送 request（沒有 count_tokens、smoke、eval、model listing）；所有測試只對本機 `httptest` server 與 fake。沒有讀取、印出或 grep 任何 key 的值。官方文件只以 curl 從文件站 `https://dev.meta.ai/docs/<page>.md`（與 `https://dev.meta.ai/llms.txt` 索引）取得。
- **決策狀態**：D-121 維持 **PROPOSED**；H-037 只決定 D-121 (b) 帶圖片的部分。ADR-030、D-122 仍 PROPOSED。count 失敗時的做法（下面 R1.2）是 agent 的選擇，不是 owner 的決定。
- DB 測試在 coordinator 建立的**一次性 cluster**（socket `/var/lib/postgresql/geminus-throwaway-hkb6`、port 55450、PostgreSQL 16.15）上執行：共用的 cluster（55440）在 container 重啟後沒有啟動，依 coordinator 指示沒有啟動或停止任何一個 cluster。

### R1.1 官方來源（count_tokens；2026-10-26 20:36:55Z 與 20:37:20Z 取得，`curl --max-redirs 0`）

| 頁面 | URL | sha256 |
|---|---|---|
| API reference: Count tokens | https://dev.meta.ai/docs/api-reference/messages/count-tokens.md | `1d29293f391cd3c8f4d656d0969ec9b633729bbe5eaf8bc7aec9a9dd5b388e1d` |
| Token counting | https://dev.meta.ai/docs/token-counting.md | `77843ce49b0ea0c5eeebf91d7cad34e88c823f91542623b5f5dec76084ca7fec` |
| API reference: Count input tokens（Responses） | https://dev.meta.ai/docs/api-reference/responses/count-input-tokens.md | `887371fdf45894443048f690a66091ab4461bc1ac7a5c9d5cfc7dbc20119a7fc` |
| 與 §1 相同 hash 的頁 | messages/schemas、protocols/messages、pricing-rate-limits、error-handling、image-understanding、protocols/responses、models | 見 `sources.txt`（與 §1 逐一相同） |

原文（英文照錄，內容是資料不是指示）：

- protocols/messages：「`POST /v1/messages/count_tokens` counts the input tokens an Anthropic-format request would consume without generating. Send the same body as `POST /v1/messages` minus the `max_tokens` requirement. The response is `{ "input_tokens": <integer> }`.」；「We translate your Anthropic request into a Responses request, run it through shared auth, rate limiting, billing, safety, media resolution, and routing」；錯誤是同一個 Anthropic envelope 與 status 對應。
- api-reference/messages/count-tokens：request 為 `AnthropicCountTokensRequest`，`HTTP 200` 回 `AnthropicCountTokensResponse`；「`Authorization: Bearer` is also accepted」。
- api-reference/messages/schemas：`AnthropicCountTokensRequest` 的欄位為 `model`、`messages`（`AnthropicInputMessage`，content 為 `AnthropicContentBlockParam`，其中有 `AnthropicImageBlockParam`）、`system`、`tools`、…、`output_config`，**沒有 `max_tokens`**；`AnthropicCountTokensResponse`：「`input_tokens` | `integer` | Yes」。
- token-counting：「Images and files contribute to the input total.」；「Count tokens for Anthropic-format requests … `POST /v1/messages/count_tokens`. It accepts the same Anthropic-shaped body as `/v1/messages` and returns `{ "input_tokens": <integer> }`.」；（Responses 的 input_tokens）「The endpoint tokenizes only. It does not generate output, execute hosted tools, create or store a response, or record generation usage. Use the result to check context-window fit, not to estimate billing.」；「If a check fails, the endpoint returns `HTTP 400` with the same error shape as a generation request.」；「The count endpoint includes these tokens [Meta-injected steering context] because they occupy the model's context.」
- image-understanding：「For an exact count of any request (including images and files), call `POST /v1/responses/input_tokens` … It returns the input token total without generating a response, and is the authoritative way to estimate cost.」
- pricing-rate-limits：「Model API bills text models per token」；「those injected tokens are not billed and are excluded from the token counts reported in your `usage` and by the token-counting endpoints」；rate limit 以 team 計，`muse-spark-1.3` 3,000 RPM、4,000,000 TPM，沒有另列 count endpoint 的限制。

結論：

1. **count_tokens 接受圖片**：schema 的 messages 收 image block，且「Images and files contribute to the input total」。所以沒有 STOP 的理由。
2. **是否計費**：文件**沒有一句話直接說「免費」**；它沒有 count 的價格，count endpoint「does not … record generation usage」，而計費依 usage。所以判斷為不計費：count 的 attempt 不記為可能計費、不預留（`meta.count` 丟棄它的 tally）。live 時確認（H-037 的未決事項）。
3. **rate limit**：count 走「shared … rate limiting」，文件沒有寫它是否計入 RPM／TPM；adapter 對 count 的 429 照 Messages 的規則重試。每次照片 proposal 多一個 request（帶同一張圖片）。
4. **文件之間的兩處不一致**（請 owner 知悉）：(a) token-counting 說「not to estimate billing」，image-understanding 說「the authoritative way to estimate cost」；(b) token-counting 說 count 包含 Meta 注入的 steering context，pricing 說 count 不包含。兩種情形下，沒有 hosted tool 的 request（本 adapter 不送 tool）其計費的 input 都不會多於 count（不包含時相等，包含時 count 較大），再加 10%。若 live 時結算仍超過預留，既有的 `spend_over_reservation`（metadata、audit、metric、WARN log）會標示，依 H-037 回報 owner。

### R1.2 設計

- **介面**（`internal/ai/provider`）：`Billable.Quote(ctx, req)`：quote 可能詢問 provider，所以拿到呼叫的 context。`Client.call` 把 budget 的 `Timeout` 移到 quote 之前開始：**count 與 Messages 呼叫共用一個 deadline**，取消也傳到 count。Quote 回傳 `*provider.Error` 時，該錯誤（code、status、credential、error type）就是呼叫的錯誤，不預留、不呼叫 backend；其他錯誤照舊為 `invalid_request`。Anthropic adapter 與測試用的 backend 只改簽名、忽略 ctx（行為不變）。
- **Meta 的 Quote**：文字 request 照 D-111 (a)（bytes＋schema＋4096），**不呼叫 count**。有圖片的 request：`count` 先檢查 approval 的資料政策（角色與圖片；不合即 `policy_not_approved`，不送），以 `countBody`（與 `messagesBody` 同一個 `messagesOf`，只少 `max_tokens`）`POST v1/messages/count_tokens`，經 `conn.post`：key 只在 `Authorization: Bearer`（只在 attempt 中讀）、不跟隨 redirect、8 MiB 的回答上限、呼叫的 deadline 與每個 attempt 的逾時、同樣的重試（429、5xx 除 504、連線中斷）、401／403 停用 key、402 休息、封閉的 error type 清單、錯誤不含 provider 的文字或 URL。回答以 `decodeCount` 讀：一個嚴格的 JSON 物件（UTF-8、沒有重複欄位、大小寫正確），`input_tokens` 必須寫成 1 至 1,048,576 的整數（不接受小數、指數、字串、null、超過 int64）；其他欄位不讀、不存。預留的 input 為 count ＋ ⌈count / 10⌉，至多 context window（input + `max_tokens` 必須在 window 內，所以 window 仍是可計費 input 的上限）；output 上限與 attempt 數照 D-111。
- **count 失敗或不合理時（agent 的選擇：讓呼叫失敗，不以 window 預留）**：以 typed code 結束（`unavailable`（可重試）、`rate_limited`、`credentials_exhausted`、`request_rejected`、`timeout`、`cancelled`、`policy_not_approved`），**不預留、不送 Messages**，所以預留永遠不會小於規則的數。理由：(1) 改以 context window 預留（USD 4.04）在 organization 日上限 USD 5 下多半被拒（`AI_QUOTA_EXCEEDED`），正是 M1 的問題，使用者看到的會是額度錯誤；(2) count 的失敗（key、429、5xx、400 的驗證）多半預示 Messages 呼叫也會失敗，照送只多用掉 D-121 (c) 算作可能計費的 attempt；(3) 不合理的 count 表示 provider 的行為與文件不同，依 AGENTS.md 回 unavailable、不猜；(4) count 不計費，失敗不花錢；可重試的 code 由 job 的重試處理（每次重試只多一個免費的 count）。
- **不寫進 log、audit、DB 的東西**：count 的回答只取一個整數，不存；沒有新的 metadata、audit 欄位或 migration。預留的金額照舊記在 `ai_spend.reserve`。
- **照片路徑**（T-056）：worker 的 judge 是同一個 metered client，所以照片 proposal 的 Muse Spark 呼叫自動先 count；SAM 3.1 的 segmentation 以每張圖片計價，不變。
- **已知限制**：(a) count 被 401／403／402 拒絕或 key 已被停用時，錯誤帶 key 的 label（`Error.Credential`），但 metadata 的 `FailedOver` 是空的（Quote 沒有 Response 可以帶它；文字呼叫的 metadata 照舊有）。(b) 每次照片 proposal 圖片送兩次（count 與 Messages；每張至多 5 MiB）。(c) count 與 Messages 之間 provider 的 tokenizer 若改變，count 可能不準；超過時見 R1.1 的 4。

### R1.3 預留的數字（M1；預設 `MaxRetries` 2、`MaxOutputTokens` 8192，`muse-spark-1.3` input 1,250、output 4,250 nano-USD／token）

| | 一次照片 proposal 的 judge 預留 | organization 日上限 USD 5 下可容 |
|---|---|---|
| 修正前（context window） | 3 × (1,048,576 × 1,250 + 8,192 × 4,250) = **4,036,608,000 nano-USD（USD 4.04）**；部署日上限 USD 10 下同時至多 2 個 | **1**，且只在當天 committed ≤ 約 USD 0.96 時 |
| 修正後（H-037），count 3,000 | 3 × (3,300 × 1,250 + 34,816,000) = 116,823,000（USD 0.12） | — |
| 修正後，demo 大小（悲觀的 count 40,147：demo prompt 的大小 18,428 bytes 每 byte 一個 token、schema、4096，加上圖片 15,000 tokens＝文件 ~1280px 約 1,500 的十倍） | 3 × (44,162 × 1,250 + 34,816,000) = **270,055,500 nano-USD（USD 0.27）** | **15**（連同 SAM 8 個 concept 的整個預留 60,000,000；只算 judge 為 18） |

output 的部分固定為 104,448,000（USD 0.10）。count 達 window 時與修正前相同（上限）。實際的 count 要 live 才知道（NOT RUN）；數字出自 `TestADemoPhotoProposalFitsTheOrganizationCap`（`green-meta.log` 的 log 行）。

### R1.4 Review findings 的處理

| Finding | 修正 |
|---|---|
| M1 | ADR-030「計量」與 Consequences（舊的 4.04 美元、同時 2 個、organization 日上限 1 個且 ≤ 0.96；新的 count 規則與數字）；Alternatives 記 count 失敗的兩種做法；ADR-030 的交叉引用改為 D-121 (b)（原「D-123」）；本報告 §9 問題 1；D-121 (b)（舊、新兩種預留）；`planning/decisions.json` D-121；AI_SPEC 的圖片段落 |
| L1 | `TestRetriesBackOff` 以 `meta.SetClock` 固定時鐘，HTTP date 為 now+3 s，**期望正好 3 s**（其他案例也改為正好相等）；`go test -count=300 -run TestRetriesBackOff` 0 失敗。§5 的 mutation 紀錄改為第二輪 24 killed／6 survived，G10 列為等價（理由照 review），S10 維持 killed |
| L2 | `conn.wait`：Retry-After 超過 60 s 不重試，以該 status 的 code 結束（429 → `rate_limited`，503 → `unavailable` 且該 attempt 算可能計費）；正好 60 s 仍等 60 s 後重試；註解與 D-121 (d)、ADR-030 改寫 |
| L3 | `TestSpend` 加「no usage」（去掉 `usage` 的 200）：`unavailable`、1 個 request、至少整個預留 |
| L4 | H-029 record 的 retention 確認來源改為 H-035 (1)（ToS 2026-10-09 版、§4.1／§4.4 的保留、§5.1、Standard mode 依 H-029 (1)）；`aiapproval/meta_test.go` 的期望同步；record 的註解、D-122 (b) 加註；§9 問題 2、4 標為已由 H-035 回答 |
| L5 | 新 rule `live-adapters-apart-anthropic`（applies anthropic、forbid meta）；synthetic graph 加 anthropic → meta 並要求該 violation；rule 表加一項；真實 repository 的 probe `the-anthropic-adapter-links-meta`；ARCHITECTURE 與 ADR-030 的表；`make architecture-check` 為 26 rules |
| I2–I9 | **沒有做**（review 列為 Info，不要求）：I2（`billing_error` 記為 `other`）、I3（live 時要看的事）、I4（SAM 回答的 8 MiB 與大圖）、I5（EXIF 與 demo 圖片的保證）、I6（`structured/doc.go` 的註解）、I7（報告 §6、§8 的 hash 與法律頁的 HTML hash）、I8（異常大的 usage）、I9（`HTTPS_PROXY` 的註記）。I1（SAM 的 live 步驟）不在本修正範圍 |

### R1.5 測試（先紅後綠）

步驟：先把 `Quote` 的簽名機械地改為帶 ctx（行為不變，`step-a-mechanical.log` 全綠），再寫測試、跑紅，再實作。

| 範圍 | 測試 | 紅 → 綠 |
|---|---|---|
| provider | `quote_test.go`：`TestTheQuoteRunsWithinTheCall`（quote 的 deadline 是呼叫的、與 backend 的 deadline 相同；取消傳到 quote）、`TestATypedQuoteErrorIsTheCallsError`（6 種 typed 錯誤，code／status／credential／retryable 保留，不預留、不呼叫） | `red-provider.log`（quote 沒有 deadline、typed 錯誤變 `invalid_request`）→ `green-provider.log` |
| meta：count | `count_test.go`：`TestAnImageRequestIsCountedFirst`（順序 count → message、count body 等於 message body 去掉 `max_tokens`、Bearer、預留 3 × bound(3,300)）、`TestTheCountsMargin`（1→2、9→10、10→11、11→13、3000→3300、3001→3302、953,250→1,048,575、953,251 與 1,048,576→window；其他欄位不讀）、`TestACountThatCannotBeTrustedSendsNothing`（35 種：0、負數、小數、指數、字串、null、true、物件、超過 window、超過 int64、缺欄位、大小寫、陣列、裸數字、非 JSON、兩個 document、重複欄位、無效 UTF-8、空、201、超過 8 MiB、斷線、body 中斷、400、404、413、401、402、403、429、500、503、504、未知 type、redirect（不跟隨）：每個都是 typed code、正確的 attempt 數、**0 個 message、0 預留、0 花費**、沒有 provider 文字與 URL）、`TestTheCountKeepsTheCallsDeadline`（逾時與取消，不送不預留）、`TestARefusedCountRefusesTheKey`、`TestTheCountIsRetried`、`TestTextIsNeverCounted`（0 個 count、D-111 的預留）、`TestTheCountSendsOnlyWhatTheApprovalNames`、`TestADemoPhotoProposalFitsTheOrganizationCap`（≥ 10 次，舊規則 1 次） | `red-meta.log`（沒有 count、預留 4,036,608,000）→ `green-meta.log` |
| meta：既有 | `TestImageParts`（count 為 3000 時 quote 3300、預留依 count）、`TestRetriesBackOff`（L1、L2：固定時鐘、正好的等待、61 s／600 s／61 s 的 date／503 120 s 不重試與花費）、`TestSpend`（L3） | 同上（L2 的 4 個案例紅；L1、L3 的案例對現有程式本來就綠，由 mutant R4、U1 證明它們會抓到錯誤） |
| aiapproval | `TestTheH029Record`（H-035 (1) 的來源） | `red-aiapproval.log` → `green-aiapproval.log` |
| architecture | `TestSyntheticGraph`、`TestEveryRuleEntryIsEnforced`、`TestProbesAreCaught/the-anthropic-adapter-links-meta` | `red-architecture.log`（沒有 violation）→ `green-architecture-targeted.log` |

沒有降低或刪除任何 assertion、沒有加 skip。改動的既有期望只有：`TestImageParts` 的 quote 與預留（context window → count＋10%，H-037 的規則變了）、`TestRetriesBackOff` 的「above the bound」（等 60 s 後重試 → 不重試，L2 的規則變了；HTTP date 由「2–3 s 之間」改為更嚴格的「正好 3 s」）、`TestTheH029Record` 的來源字串（L4）、synthetic graph 多一條 edge 與一個必須出現的 violation。

### R1.6 Mutation（`mutate.py`、`mutation.log`；commit 4ac9a76 的 `git archive` 副本，在 session 的 scratch 自己的資料夾）

每個 mutant 一處確切的替換（必須只出現一次），先 `go vet` 該 package（含測試；build 失敗記為 INVALID，不算 killed），再 `go test -count=1`；環境沒有任何 AI 變數，`GOPROXY=off`。**30 個：30 killed、0 survived、0 invalid**。

- count 與預留（meta.go，21 個）：K1 圖片不 count、K2 沒有 10%、K3 10% 無條件捨去、K4 5%、K5 不以 window 為上限、K6 count 失敗改用文字的 quote（較小）、K7 count 失敗改以 window 預留（另一種做法）、K8 接受 0、K9 接受超過 window、K10 接受小數與指數、K11 接受字串、K12 欄位名不分大小寫、K13 接受重複欄位、K14 接受無效 UTF-8、K15 count body 帶 `max_tokens`、K16 不查角色、K17 不查圖片政策、K18 count 送到 `/v1/messages`、K19 count 不重試、K20 不合理的 count 記為 `invalid_output`（不可重試）、K21 count 不受呼叫的 deadline 與取消約束（`context.WithoutCancel`）。
- client（meter.go）：P1 quote 拿呼叫者的 ctx（沒有呼叫的 deadline）、P2 typed 的 quote 錯誤變 `invalid_request`。
- L2／L1（conn.go）：R1 超過 60 s 仍等、R2 正好 60 s 不重試、R3 舊的「截到 60 s 再重試」、R4 Retry-After 以牆上時鐘解析（L1 的固定時鐘抓到它）。
- L3：U1 拿掉 `u == nil`（`TestSpend` 的「no usage」：nil dereference 的 panic，是測試失敗，不是 build 失敗）。L4：H1 來源改回 H-029 (1)。L5：A1 鏡像 rule 不禁止 meta（`check-architecture` 只跑相關的三個測試）。
- 注意：第一次執行與另一個 agent 的 mutation runner 共用了 scratch 的同一個資料夾（coordinator 通知：21:07–21:09 UTC 對方刪除並重建了那裡的檔案），那次的結果已丟棄，在自己的新資料夾從新的 `git archive` 重跑；上面是重跑的結果。

### R1.7 指令與結果（Go 1.27.3、`GOTOOLCHAIN=local`；Python `.venv/bin/python`）

| 指令 | exit | 結果 | Log |
|---|---|---|---|
| `go test -count=1`（provider/...、spend、proposals/...、config/...、aievals、storage/aispend、cmd/worker、cmd/ai-eval-live、check-architecture；機械改動後） | 0 | 全部 ok | `step-a-mechanical.log` |
| 紅：provider、meta、aiapproval、architecture 的新測試 | 1、1、1、1 | 預期的失敗 | `red-*.log` |
| 綠：同上 | 0、0、0、0 | | `green-*.log` |
| `go test -count=1 -timeout 30m`（provider/...、ai/spend、proposals/...、config/...、aievals、storage/aispend、cmd/worker、cmd/ai-eval-live、scripts/check-architecture） | 0 | 16 個 package ok（DB 測試在這裡 skip，由 integration-test 執行） | `changed-packages.log` |
| `go test -race -count=1 -timeout 30m`（同上） | 0 | 16 個 package ok，沒有 data race | `changed-packages-race.log` |
| `go test -count=300 -run TestRetriesBackOff ./internal/ai/provider/meta/` | 0 | **0 失敗**（300 次） | `retries-backoff-300.log` |
| mutation（R1.6） | 0 | 30／30 killed | `mutation.log` |
| `make backend-test GOFLAGS=-count=1`（gofmt、go vet、全部 Go 測試，`GEMINUS_FULL_DIFFERENTIAL=1`） | 0 | 83 個 package ok、12 個沒有測試檔、0 FAIL（DB 測試在這裡 skip，由 integration-test 執行） | `backend-test.log` |
| `make architecture-check` | 0 | `PASS architecture: 95 packages, 26 rules, 16 build configurations` | `architecture-check.log` |
| `make ai-eval` | 0 | fake provider 的 context 與 review suite；`checks/ai-evals/fake-*.json` 沒有變 | `ai-eval.log` |
| `make spec-check PYTHON=/home/user/Geminus/.venv/bin/python` | 0 | 全部 PASS（含 decisions、docs、DB tests fresh、deploy）；`checks/*-check.json` 沒有變 | `spec-check.log` |
| `make integration-test`（新建的 `dt_test_t055r1a`，在一次性 cluster `/var/lib/postgresql/geminus-throwaway-hkb6`、port 55450，`GEMINUS_REQUIRE_DB_TESTS=1`） | 0 | db-test（0001–0017 up／down／up 與全部 SQL 測試）與 db-go-test：38 個 package ok，0 FAIL、0 SKIP；含 `internal/proposals/generator`（照片路徑的計量）、`internal/storage/aispend`、`internal/api`、`cmd/worker` | `integration-test.log` |
| `dropdb dt_test_t055r1a`；`SELECT count(*) FROM pg_database WHERE datname='dt_test_t055r1a'` | 0 | 0（已刪）；沒有啟動或停止任何 cluster | — |
| `make spec-check`（加上本節之後再一次） | 0 | 全部 PASS；`checks/*-check.json` 沒有變 | `spec-check-final.log` |

### R1.8 NOT RUN

- 任何 live 呼叫：`POST /v1/messages/count_tokens`（對圖片的實際行為、是否真的不計費、是否計入 RPM／TPM、實際的 count 值）、`POST /v1/messages`、SAM、`make ai-smoke-live`、`make ai-eval-live`。
- `make e2e-test` 與 frontend 的測試：依指示不執行（另一個 run 使用 3100–3102）。
- 整個 repository 的 `make backend-race`：只執行了改動的 package 的 `-race`（R1.7）。
- Fuzz：沒有重跑（`FuzzNewImage`、`FuzzParseSegmentation` 的程式沒有改）。
- govulncheck：`go.mod`、`go.sum` 沒有改。
- Railway 上的任何操作；`scripts/check_deploy.py` 只經 `make spec-check` 執行。
- I2–I9（Info）：見 R1.4。

### R1.9 給 coordinator／owner 的問題

1. **count 失敗時的做法**（agent 的選擇）：讓呼叫失敗、不預留不送（R1.2）。若 owner 想要「以整個 window 預留後照送」，是一處改動（mutant K7 的形狀），測試要跟著改。
2. **文件的不一致**（R1.1 的 4）：count 是否包含注入的 steering context、count 能不能用來估計費用，兩頁說法相反。live 的第一次照片 proposal 請比較 count、預留與 `usage.input_tokens`（`spend_over_reservation` 不應出現）。
3. **count 是否計費**：文件沒有直接寫「免費」；本修正以不計費處理（沒有價格、不記錄 usage）。live 時在 Meta dashboard 確認；若會計費，count 也需要預留（目前的設計在預留之前呼叫它）。
4. **FailedOver**：count 被拒時 metadata 沒有 `FailedOver`（錯誤本身帶 label）。需要的話另做。
5. **D-121 的狀態**：維持 PROPOSED；(b) 帶圖片的部分是 H-037 的決定，其餘（5xx 算可能計費、Retry-After 的新規則等）仍待 owner。

### R1.10 Commit（branch `t055-r1`，自 1425e3a，沒有 push）

| Commit | 內容 |
|---|---|
| 4ac9a76 | 程式與測試：`Billable.Quote(ctx, …)`、呼叫的 deadline 包含 quote、typed 的 quote 錯誤；Meta 的 count（H-037）；L2；L1 與 L3 的測試；L4 的 record；L5 的 rule |
| a9abd99 | ADR-030、D-121 (b)／(d)、D-122 (b) 的註、`planning/decisions.json`、AI_SPEC、ARCHITECTURE；本報告 §5、§9 與本節；evidence |
| （本表的 commit） | 本表與最後一次 `spec-check-final.log` |

## Coordinator 驗證：review 第 1 輪修正（2026-10-26）

`t055-r1` 的三個 commit（4ac9a76..6e66c6c）cherry-pick 到主線；只有本報告衝突（兩邊都在最後加了一節，兩節都保留）。

指令與結果（evidence：`checks/evidence/2026-10-26-T-055r1-coordinator/`，`exit-codes.txt` 依執行順序；HEAD d88fcb4；go1.27.3；DB 測試在暫時的 cluster：`/var/lib/postgresql/geminus-throwaway-hkb6`，port 55450，因為共用的 cluster 在 container 重啟後沒有在跑，依規則不啟動它）：

| 指令 | exit | 說明 |
|---|---|---|
| `make spec-check` | 0 | |
| `make backend-test "GOFLAGS=-count=1 -timeout=30m"` | 0 | 83 個 package ok（`scripts/check-architecture` 323 s，load 約 8–10） |
| `make architecture-check` | 0 | 95 packages、**26 rules**（L5 的鏡像 rule）、16 build configurations |
| `make ai-eval` | 0 | fake provider |
| `go test -count=300 -run TestRetriesBackOff ./internal/ai/provider/meta` | 0 | L1：固定時鐘後 300 次沒有失敗 |
| `go test -race -count=1 -timeout 30m`（ai、aievals、config、proposals、storage/aispend、cmd、check-architecture） | 0 | 24 個 package ok |
| `make integration-test`（新的 dt_test_hkb7c，跑完刪除） | 0 | 38 個 package ok，沒有 FAIL 或 SKIP |

NOT RUN：任何 live 呼叫（count_tokens、Messages、SAM）；前端與 e2e（本輪沒有改 UI）；完整的 `make backend-race`。

Coordinator 對本輪問題的處理：
1. count 失敗時「呼叫失敗、不預留、不送出」：接受。H-037 允許兩種，這種不會少預留，也不會因為整個 context 的預留又碰到 M1 的上限問題。
2. 文件對 count 是否包含 Meta 注入的 token 互相矛盾：第一個 live 照片 proposal 比對 count、預留與 `usage.input_tokens`，記錄在 live 的報告。
3. Count 是否收費：live 後在 Meta dashboard 確認；若收費，count 本身也要先預留（改為 reviewed change）。
4. Count 的 key 被拒時 `FailedOver` 為空：照舊（Meta 只有一把 key，沒有 failover）。
5. D-121 維持 PROPOSED；H-037 只決定帶圖片時的 (b)。

結論：review 第 1 輪的 M1 與 L1–L5 已修正並由 coordinator 驗證；依 H-029，T-055 可以進行第一批 live 步驟（count_tokens → `AI_PROVIDER=meta` 的 smoke → eval），由有 key 的新 session 執行。照片（SAM 3.1）的 live 呼叫要等 T-056 的獨立審查。

## Live 執行（Meta）（2026-10-26）

依 H-029、H-033、H-035、H-037，在 coordinator 驗證過的 review 第 1 輪程式（HEAD 9374173，branch `claude/lucid-meitner-vq1awo`）上，由有 key 的新 session 執行 §9 問題 10 的第一批 live 步驟。證據在 `checks/evidence/2026-10-26-T-055-live/`（`environment.txt`、兩個 count 紀錄、`smoke.log`、`eval.log`、產生 body 的 scratch 測試 `zz_body_test.go.txt` 與其輸出 `request-bodies.log`）。

- **Key**：`GEMINUS_MUSE_API_KEY` 只檢查名稱存在；值、長度、前綴沒有印出、記錄或 grep，也沒有出現在任何 argv（curl 以 `-H @<(printf 'Authorization: Bearer %s\n' "$GEMINUS_MUSE_API_KEY")` 帶入）。沒有讀取任何 Anthropic key。
- **工具**：Go 1.27.3（`GOTOOLCHAIN=local`；toolchain 目錄在 session 開始時不存在，由 `/usr/local/go` 依 `go.mod` 的 `toolchain go1.27.3` 自動下載後使用）。
- **沒有 commit 任何 prompt 或 model 的回答**：request body 只記 sha256 與大小；smoke 的報告只有 ID、狀態、token 與花費。

### 執行的步驟

| # | 指令 | exit | 結果 |
|---|---|---|---|
| 1 | `POST https://api.meta.ai/v1/messages/count_tokens`（文字；body 為 adapter 對一個小的 synthetic modeler request 送出的 Messages body 去掉 `max_tokens`，3,220 bytes，sha256 `009732fc…9be90`） | curl 0 | **HTTP 200**，`{"input_tokens":899}`。key 有效、`muse-spark-1.3` 被接受、投影後的 proposal-output schema（2,623 bytes，sha256 `71bb98bf…f254`，與 T-053 的投影相同）被接受 |
| 2 | 同上，照片 judge 對 `testdata/photos/electronics-line-demo.png` 的 count body（`aicontext.ImageProposal` → adapter 的 Quote；40,861 bytes，sha256 `221aa4ce…f962b`） | curl 0 | **HTTP 200**，`{"input_tokens":6661}` |
| 3 | `AI_PROVIDER=meta AI_POLICY_APPROVAL=H-029 AI_MODEL=muse-spark-1.3 GEMINUS_AI_LIVE=on make ai-smoke-live` | **0** | run `01M5X37ZBG0JCJAYB1WBK59DMW`：MOD-002 **ready**（golden 1/1，server risk low，`semantic_effect_held` true），input 4,701、output 2,515 tokens，**USD 0.016565**（cap 2.00），latency 15.7 s；四個 zero-tolerance gate 皆 0；`over_reservation` 0；`served_models` 只有 **`muse-spark-1.3`**（回答的 `model` 與 request 完全相同） |
| 4 | 同樣的設定 `make ai-eval-live`（預設 cap USD 10） | **2** | **REFUSED，沒有任何 provider 呼叫**：「the run's cap (USD 10.00) is more than the UTC day has left (the spend record holds USD 2.363289 of the daily limit USD 10.00)」 |

報告與 ledger：`checks/ai-evals/live-smoke-2026-10-26T215310Z-01M5X37ZBG0JCJAYB1WBK59DMW-muse-spark-1.3.json`；`checks/ai-evals/live-spend.jsonl` 新增兩行（`open` kind smoke、approval H-029、cap 2,000,000,000；`close` settled＝committed 16,565,000 nano-USD、result complete）。步驟 4 被拒在開 run 之前，ledger 沒有新增任何行。

### 照片 count 與預留（H-037）

實際的 count 6,661（coordinator 刻意悲觀的假設 40,147 的 16.6%）。預設 `MaxRetries` 2、`max_tokens` 8192 下：每個 attempt 預留 input 6,661 + ⌈666.1⌉ = 7,328 tokens；7,328 × 1,250 + 8,192 × 4,250 = 43,976,000 nano-USD；一次照片 proposal 的 judge 3 × 43,976,000 = **131,928,000 nano-USD（約 USD 0.132）**，假設下為 270,055,500（約 USD 0.270）。連同 SAM 8 個 concept 的預留（60,000,000），organization 日上限 USD 5 下可容 **26** 次（假設下 15 次）。count_tokens 接受圖片（H-037 的未決事項之一），所以 H-037 的設計可行。

### 意外的結果（依指示在此停止）

1. **Eval 被當天的剩餘額度拒絕**（步驟 4）。ledger 是兩個 provider 共用的（D-122 (a)）：今天（UTC 2026-10-26）已有 T-053 的 Anthropic smoke 與 eval（USD 0.04244＋2.304284）與本次 Meta smoke（0.016565），共 **USD 2.363289**，部署日上限 USD 10 只剩約 USD 7.64，而 eval 的預設 cap 是 USD 10，所以 `ai-eval-live` 在任何呼叫之前拒絕（設計上的行為，不是錯誤）。照指示停止，沒有以較小的 cap 重試。可選的下一步（owner／coordinator 決定）：(a) 以 `AI_EVAL_LIVE_MAX_USD`（例如 7.50；Anthropic 的 50 個案例花了 USD 2.30，Meta 的價格較低）在同一天執行；(b) 等 UTC 2026-10-27 再以預設 cap 執行。
2. **Rate-limit header 的觀察**（記錄，不是錯誤）：第一個 count 的回答 `x-ratelimit-remaining-requests` 為 3000，第二個為 2998；`x-ratelimit-remaining-tokens` 兩次都是 4,000,000。看來 count 計入 RPM（R1.1 的 3：文件沒有寫），是否計入 TPM 看不出來。

### NOT RUN

- `make ai-eval-live`（50 個 modeler 案例）：被拒（上面 1），沒有任何案例執行；所以沒有 pass 數、golden 比例與 eval 的 gate 數字。
- SAM 3.1 的任何呼叫、照片 proposal（count 之後的 Messages 呼叫）、count 與實際計費 `usage.input_tokens` 的比較（R1.9 問題 2）：依指示等 T-056 的審查。
- count_tokens 是否計費：Meta dashboard 的確認（owner）。
- Railway 上的任何操作。
- Anthropic 的任何呼叫。

### 指令（本節之後）

`make spec-check PYTHON=/home/user/Geminus/.venv/bin/python`（加上本節之後、commit 之前）：**exit 0**（`spec-check.log`）；`checks/*-check.json` 沒有變。
