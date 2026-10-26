# T-055 獨立審查 第 1 輪：Meta Model API provider（Muse Spark、SAM 3.1）

- 審查者：獨立 reviewer（H-003；hackathon 模式 H-029 (4)：每個 demo 切片一輪）。我沒有寫這些程式。
- 對象：branch `claude/lucid-meitner-vq1awo`（本 worktree，HEAD `bce77a3`）在 `a01f423` 之後的 T-055 commit：`921627c`（e8ee746 的 cherry-pick）、`2e96437`（19fb02a）、`f3a5a51`（303974b）、`458695f`（339fea5）、`1fb4f1a`（c8a31fc）、`054de50`（1c0b309）、`9bdf03d`（c3307cf）、`bce77a3`。中間的 `5423d86`、`96e0a8a`、`c96dcfb` 是 T-052，不在範圍內（diff 以 `c96dcfb..HEAD` 計）。
- 報告：`checks/T-055_VALIDATION.md`；evidence：`checks/evidence/2026-10-26-T-055/`。
- 依據：AGENTS.md、docs/AI_SPEC.md、docs/SECURITY.md §7、H-018、H-025、H-029、H-031、H-033、H-035（APPROVED）、ADR-027（ACCEPTED）與其 addendum（PROPOSED）、ADR-030 與 D-121–D-123（PROPOSED）；參考已審查的 `internal/ai/provider/anthropic`（T-053）。
- 方式：只讀（除本檔外沒有改任何 tracked 檔；mutant 每次改一行、跑測試、以 `git checkout --` 還原並確認 `git status` 乾淨）。**沒有任何 live 呼叫**（沒有對 Meta、Anthropic 或任何 provider 送 request，沒有 count_tokens，沒有 model listing）；沒有讀取、印出或 grep 任何 key 的值；環境中的 AI 變數在我的 script 中一律 unset。官方文件只以 curl 從 `https://dev.meta.ai/docs/<page>.md` 與 `/legal/<page>` 取得（文件站，不是 API）。

## 結論

**CHANGES REQUESTED**（1 個 Medium：M1，只需改文件）。程式本身沒有 High 或 Medium：request 欄位、Standard-mode 的 model 檢查、schema 上限、圖片上限、SAM 的 `store:false` 與 grammar、價格、錯誤與重試、key 的處理、花費預留與結算、開關與 API 的隔離都經得起我的核對與 mutant；structured 的搬移逐 byte 相同。

**第一批 live 步驟**：M1 不影響 count_tokens、`make ai-smoke-live`（`AI_PROVIDER=meta`）、`make ai-eval-live` 會走的程式路徑（這些都是沒有圖片的文字 proposal）；Low 中只有 L2 可能在這些步驟出現（provider 回 Retry-After 超過 60 s 時提早重試一次），後果是多一次 request 與 ledger 多記，仍在預留之內。依 H-029 (4)，M1 修好並由 coordinator 驗證後即可依 H-033 由有 key 的新 session 執行這三步；建議同時先處理 L4（批准紀錄的 retention 來源改為 H-035 (1)），因為 D-122 自己的 gate 寫「Meta 的任何 live 呼叫前」。**第四步「一張 demo 圖片的 SAM 呼叫」在這個 build 沒有程式路徑**（I1）：worker 建好了 segmenter 但沒有任何 handler 呼叫它（T-056），`cmd/ai-eval-live` 沒有 SAM 模式；手動 curl 會繞過 ledger 與 adapter 的 parser，見 I1。

## 1. 官方文件核對（2026-10-26 約 18:54 UTC 由我自行取得）

16 個文件頁（models、protocols/messages、api-reference/messages/create-message、api-reference/messages/schemas、structured-output、image-understanding、reasoning、pricing-rate-limits、error-handling、authentication、protocols/responses、api-reference/responses/schemas、sam/overview、sam/segmenting、sam/reading-segmentation、media-segmentation）的 sha256 與報告 §1 **完全相同**。3 個法律頁（terms-of-service、geographic-use-policy、commitments）的 HTML hash 與報告不同，但去掉 script／style／tag 後的可見文字，與本 session scratch 中 hash 等於報告所列的那三份副本（16:16 取得）逐字相同（HTML 中有動態內容）；所以報告的 HTML hash 無法由他人重現，引文本身正確（I7）。

| 要求 | 文件原文（摘錄） | 程式 | 結果 |
|---|---|---|---|
| 會 400 的欄位不送 | Messages：「These top-level fields are not supported and return HTTP 400: stop_sequences, top_k, container, and inference_geo. Unknown top-level fields are also rejected.」；「thinking: {type: "disabled"} … returns HTTP 400」 | `meta.go:274-280` 只有 `model`、`max_tokens`、`system`、`messages`、`output_config`；`TestRequestShape` 斷言 top-level key 正好這 5 個 | 符合 |
| Standard mode、拒收別的 model | Models：「`muse-spark-1.3` for Standard mode or `muse-spark-1.3-contributor` for Contributor mode」；ToS：「the system may route you to a Contributor Model」；H-035 (1)「adapter 拒收不是指定 model 的回答」 | config 要求 `AI_MODEL` 等於 record；`metaGap` 只收 Standard（`aiapproval.go` metaStandardModels）；`meta.go:392-396` 回答的 `model` 不同即 `invalid_output`，usage 照樣結算；SAM 同（`sam.go:165-166`） | 符合（`TestAnAnswerFromAnotherModelIsRefused`：contributor、1.2、空字串、大小寫、尾端空白） |
| Structured output 上限 | 「Nesting depth 10」「Total properties 5,000」「Total string length 120,000」「Enum values 1,000」「more than 250 values … 15,000」「200,000 nodes」、遞迴 400 | `limits.go`；`meta.go:189-207` 在 Quote 與 Complete 之前檢查並快取，超出不送、不預留 | 符合（`TestASchemaOutsideTheLimitsIsNeverSent`） |
| 圖片 | 「50 MB」「50 images」、MIME 清單、「cost scales with resolution」 | `image.go`：PNG／JPEG／WebP、signature、5 MiB、8000 px、4 張；`provider.go` 只交給 `ImageInput` 且 `AcceptsImages()`；Claude adapter 沒有實作（H-035 (3)） | 符合，比文件嚴 |
| SAM `store:false` | Responses：「Responses are stored when `store: true` (the default)」；SAM：非 streaming 可用、`include_confidence` 為字串 `"true"` | `sam.go:133-137`：`store:false`、`stream:false`、`metadata{include_confidence:"true", mask_encoding:"one_bit"}`；`TestSAMRequestShape` 以 DeepEqual 比對整個 body | 符合 |
| SAM grammar | reading-segmentation 的 grammar（`<0f>`、box、mask、`c`、base85 payload、空字串為零個結果） | `grammar.go` 嚴格解析，只保留整數、0..1 的小數與 request 自己的 concept；SAM 的任何文字都不保留 | 符合 |
| 價格 | 「Cached input $0.15 \| Input $1.25 \| Output $4.25」、「$2.50 per 1,000 images」 | `spend.go`：1250／150／4250 nano-USD、cache 寫入以 input 價、SAM 2,500,000 nano-USD；來源與日期常數；沒有 geo 加價 | 符合 |
| 504 不重試 | 「Do not simply retry — the same request will usually time out again」 | `conn.go:250-252` | 符合（mutant C1 killed） |
| 錯誤 type 封閉清單 | Messages 的 7 種＋error-handling 的 `server_error` | `conn.go:161-180`，其他一律 `other`，不記 message | 符合（I2：`billing_error` 記為 `other`） |
| Retry-After | 「If the response includes a Retry-After header, wait at least that long」 | `conn.go:124-125` 上限 60 s | **不完全符合**：見 L2 |

## 2. Findings

### Medium

**M1 — 有圖片時的預留金額在報告與 ADR 中算錯，且沒有指出真正的限制（organization 日上限）**

- 位置：`docs/adr/ADR-030-meta-model-api-adapter.md:63`（「每日上限 10 美元下約 7 次照片 proposal 的預留（以 3 個 attempt 計）」）、`checks/T-055_VALIDATION.md:179`（§9 問題 1，同一句）；`docs/DECISIONS_AND_TBD.md:454` D-121 (b) 只寫每個 attempt 約 1.35 美元；`docs/adr/ADR-030-meta-model-api-adapter.md:37` 的「約 1.35 美元一個 attempt，D-123」應為 D-121 (b)。程式：`internal/ai/provider/meta/meta.go:220-221`、`internal/ai/provider/meter.go`（`reserve`）、`internal/storage/aispend/aispend.go:163-167`。
- 問題：程式對有圖片的 proposal 預留 (1 + retries) × (1,048,576 × 1,250 + 8,192 × 4,250) nano-USD。預設 budget（`MaxRetries` 2、`MaxOutputTokens` 8192）下是 3 × 1,345,536,000 = **4,036,608,000 nano-USD ≈ USD 4.04 一次**，不是 USD 1.35。D-110 的預設下：deployment 日上限 USD 10 同時至多 **2** 個，不是「約 7 次」；每個 organization 的日上限 USD 5 只容得下 **1** 個，而且只在該 organization 當天的 committed 總額 ≤ USD 0.96 時（`aispend.go:167`：`committed_nano_usd + $1 <= $3`）。
- 失敗情境：H-035 (2) 的評審帳號只有 demo organization 的權限，owner 的 demo 也在同一個 organization 時，當天以文字 proposal 實際花了約 USD 1（每次幾美分，約二十多次）之後，到 UTC 午夜為止的每一個照片 proposal 都是 `AI_QUOTA_EXCEEDED`，雖然它實際只會花幾美分。Owner 依 D-121 (b) 決定是否保留「整個 context window 的預留」時，拿到的是錯的數字（差 3.5 倍以上），也不知道是 organization 日上限先擋住。
- 最小修正（只改文件，T-055 的程式不用改）：在 ADR-030:63、報告 §9 問題 1 與 D-121 (b) 寫上述數字（每次 USD 4.04；日上限下同時 2 個；organization 日上限下 1 個且僅在當天 committed ≤ USD 0.96 時），把 organization 日上限列為實際的限制，並列出 owner 可選的處理（D-121 已有的選項 C：免費的 count_tokens 先算；選項 B：估算公式；或把 `AI_ORGANIZATION_DAILY_LIMIT_USD` 提高到日上限）。T-056 的 demo 前必須有決定。ADR-030:37 的交叉引用改為 D-121 (b)。

### Low

**L1 — `TestRetriesBackOff` 的「an HTTP date」會隨機失敗；mutation 紀錄因此把兩個 mutant 記錯**

- 位置：`internal/ai/provider/meta/messages_test.go:490`；`checks/evidence/2026-10-26-T-055/mutation.log:51`（S10）、`mutation-2.log:19`（G10）；報告 `checks/T-055_VALIDATION.md:113`、`:146`。
- 問題：測試以牆上時鐘產生 `now+3s` 的 HTTP date（只到秒），adapter 以 `c.now()` 解析（`conn.go:114-115`），等待時間是 3 s − now 的小數部分 − 處理時間，小數部分接近 1 時低於測試要求的 2 s。我執行 `go test -count=300 -run TestRetriesBackOff ./internal/ai/provider/meta/`：**300 次中 4 次失敗**（waited 1.989–1.994 s，exit 1）。所以 `make backend-test` 約 1% 的機率無故失敗（負載下更高）。Mutation 的兩個「KILLED」都是這個測試的失敗：S10 我重跑後確實被 `TestSAMRefusesBeforeAnyRequest` 殺死；**G10（`grammar.go:117` 改成 `if false`）在所有 SAM 與 grammar 測試下存活**，而且是等價 mutant：`maskToken`（`grammar.go:151`）只在文字結尾或「`,<id><|box;`」之前結束一個 mask，所以 `:117` 的分支到不了。第二輪實際是 24 killed／6 survived（6 個都等價），不是報告寫的 25／5。
- 失敗情境：coordinator 驗收時 `make backend-test` 紅了，或 mutation 的結論被當成「G10 有測試保護」。
- 最小修正：測試中固定時鐘：`now := time.Date(2026, 10, 26, 12, 0, 0, 0, time.UTC); meta.SetClock(b, func() time.Time { return now })`，Retry-After 用 `now.Add(3*time.Second).Format(http.TimeFormat)`，期望正好 3 s；報告 §5 把 G10 列為等價（理由如上），數字改為 24／6。

**L2 — Retry-After 大於 60 s 時提早重試，與文件「wait at least that long」不符**

- 位置：`internal/ai/provider/meta/conn.go:124-125`（註解 `:120-123` 說「at least what the answer's Retry-After asks for」，但 `min(…, maxRetryAfter)` 把它截到 60 s）；測試 `messages_test.go:489`（「above the bound」期望 600 s 的 Retry-After 等 60 s 後重試）。
- 問題：pricing-rate-limits：「If the response includes a Retry-After header, wait at least that long」；error-handling 的 503：「honor it before retrying」。
- 失敗情境：503 帶 `Retry-After: 300`，adapter 60 s 後重試，多半再得到 503；D-121 把每個 5xx 都算可能計費，ledger 因此多記一個 attempt 的上限（有圖片時 USD 1.35），也違反 provider 的指示。429 帶 120 s 時同樣浪費一次 attempt。
- 最小修正：`wait` 中 `if ra := retryAfter(h, c.now()); ra > maxRetryAfter { return false }`（以該 status 的 code 結束，不重試），改「above the bound」的期望為不重試、只 1 個 request，並更正註解。

**L3 — 沒有測試證明「200 的 message 沒有 usage」是讀不了的回答**

- 位置：`internal/ai/provider/meta/meta.go:361`（`u == nil`）；`messages_test.go:557-566`（`TestSpend` 讀不了的回答清單）。
- 問題：程式是對的，但我把 `u == nil ||` 拿掉（保留負數檢查）後整個 meta 套件仍然通過：沒有任何測試送出沒有 `usage` 的 200 message。這種 mutant 下 `answer`（`meta.go:382`）會 dereference nil，worker 的 job panic；「usage 不明時至少以整個預留結算」這個性質對這個形狀沒有被證明。
- 失敗情境：之後有人「簡化」`decodeMessage`，測試全綠，第一個沒有 usage 的 live 回答讓 job panic，且這次呼叫不會以預留結算。
- 最小修正：`TestSpend` 的清單加 `"no usage"`：`ok(answerJ)` 的 body 去掉 `,"usage":{…}`，期望 `unavailable`、1 個 request、spent ≥ 整個預留。

**L4 — H-029 record 的 retention 確認引用了 D-122 自己說「不是確認」的來源；H-035 (1) 才是**

- 位置：`internal/config/aiapproval/aiapproval.go:323-324`（`Retention.Confirmation.Source: "H-029 (1): Standard mode, never the Contributor variant …"`）；`docs/DECISIONS_AND_TBD.md:455` D-122 (b)「這是 owner 的 Standard mode 選擇的紀錄，不是 owner 對保留條款的另一次確認；需要 owner 確認」；報告 §9 問題 2、4。
- 問題：`metaGap` 要求 retention 已確認，record 以 H-029 (1) 填滿它，所以 live 能開；作者自己在 D-122 (b) 說那不是確認。base commit `a01f423` 中的 H-035 (1)（`docs/DECISIONS_AND_TBD.md:498`）已記錄 owner 接受 ToS（2026-10-09 版，coordinator 核對 §4.1、§4.4 的保留條款）；H-035 (3) 也回答了 §9 問題 4（照片只走 Meta，不擴大 H-018）。報告與 record 都沒有反映 H-035。
- 失敗情境：稽核時看到 record 宣稱 retention 由 H-029 (1) 確認，而決策紀錄說那不是確認；D-122 的 gate「Meta 的任何 live 呼叫前」看起來沒有滿足。
- 最小修正：Source 改為引用 H-035 (1)（例如「H-035 (1): the owner accepted the Meta Model API Terms of Service (2026-10-09), including §4.1 and §4.4 retention; Standard mode per H-029 (1)」），同步 `aiapproval/meta_test.go:42-44` 的期望；報告 §9 問題 2、4 標為已由 H-035 回答，問題 3（dashboard 的 spend limit）仍是 H-035 的 still_open。D-122 仍是 PROPOSED，是否在 live 前批准由 owner／coordinator 決定（H-033 已授權在審查過的程式上執行 live）。建議在第一個 live 步驟之前改。

**L5 — `live-adapters-apart` 只擋一個方向**

- 位置：`scripts/check-architecture/rules.go:245-250`（`applies` 只有 meta）；註解 `:241-244`「neither links the other」、ADR-030 §Decision 表「（不連結 anthropic，反之亦然）」。
- 問題：`live-ai-only-in-the-worker`（`rules.go:235`）把兩個 adapter 都列為例外，`llm-sdks-only-in-ai`（`:222`）只管第三方 SDK，所以 `internal/ai/provider/anthropic` import `internal/ai/provider/meta` 不違反任何 rule。
- 失敗情境：之後為了共用重試邏輯讓 Claude adapter 連結 meta 的程式，architecture check 照樣 PASS，與 ADR 的保證不符。
- 最小修正：加一條鏡像 rule（applies anthropic、forbid meta），並在 `check_test.go` 的 synthetic graph 加 anthropic → meta 的必須出現的 violation。

### Info

- **I1 — SAM 的 live 步驟沒有程式路徑。** 沒有 command 或 worker handler 會呼叫 segmenter（`deps.photo` 要到 T-056 才被使用；`cmd/ai-eval-live` 沒有 SAM 模式），count_tokens 也只能手動。手動對 `/v1/responses` 的 curl 會繞過 ledger、adapter 的 grammar parser 與 `SegmentClient` 的檢查。若要在 T-056 之前做：只送一張 demo 圖片、`"store": false`、key 只經環境變數不印出、花費手動記入 `checks/ai-evals/live-spend.jsonl`，並清楚註明這不是 adapter 的驗證；較好的做法是等 T-056 已審查的 handler。
- **I2** — error-handling 頁 402 範例的 `billing_error` 不在封閉清單（`conn.go:161`），記為 `other`。安全；報告「文件列出的 8 種」可加註。
- **I3 — live 時要看的事。** 回答的 `model` 字串是否正好是 `muse-spark-1.3`（有任何後綴時每個回答都 `invalid_output`，但仍計費）；Messages 的 `input_tokens` 是否包含 `cache_read_input_tokens`（Responses 的 `input_tokens` 包含 cached；若 Messages 沿用，結算會重複計 cached token：只會多算）；投影中的 `format` 與根的 `anyOf` 是否被接受（count_tokens 可看）；SAM 的 confidence 若以指數寫（如 `1e-05`）會使該 concept 的 grammar fail closed；非 streaming 的 SAM `output` 形狀。
- **I4** — SAM 的 mask 不解碼，但整個回答必須在 8 MiB 內讀完（`meta.go:90`）。One_bit mask 的大小隨 box 面積成長，8000 px 的照片中很多大物件時可能超過，成為 `unavailable` 並以整個預留結算。建議 T-056 先把 demo 圖片縮到例如 2048 px 以內。
- **I5** — 「只送 demo 圖片」（H-029、H-033、H-035）adapter 無法分辨；EXIF（GPS、相機）原樣送出。T-056 必須限制圖片來源並去掉 metadata。
- **I6** — `internal/ai/provider/structured/doc.go:9` 說「with their tests」，但測試留在 anthropic（經 `export_test.go` 的 shim），`structured` 本身沒有測試檔（`[no test files]`）。改註解即可。
- **I7** — 報告 §6、§8 寫的是 `t055-meta` branch 與原始 hash；本 branch 是 cherry-pick 到 T-052（`c96dcfb`）之後，hash 不同，evidence log 是在原 branch 產生的。我在本 HEAD 重跑的結果見 §5。法律頁的 HTML hash 無法重現（動態內容），可改記可見文字的 hash。
- **I8** — 回答中的 usage 或 `images_processed`（`sam.go:157` 截到 2^20）若是異常的大數，結算會很大（飽和運算），之後的呼叫全部被拒（fail closed，標示 over-reservation）。可接受。
- **I9** — `meta.go:6`「no environment default read」：clone 的 `http.DefaultTransport` 會依 `HTTPS_PROXY`／`NO_PROXY`（標準行為，TLS 端到端）。只是註記。

## 3. 已確認的性質

- **Key**（`GEMINUS_MUSE_API_KEY`）：只在 `AI_PROVIDER=meta`、H-029 的 record 完整、model 相符、開關 on 之後才讀（`ai.go:355-416`；`TestMetaWithoutTheSwitch`、`TestMetaMismatchesKeepLiveOff` 以 recorder 證明沒讀）；格式 `^LLM_[0-9]+_\S+$`、沒有空白與控制字元；`MODEL_API_KEY`、`GEMINUS_META_API_KEY`、Anthropic 的 key 不讀。只以 `Authorization: Bearer` 送出（`conn.go:277`）；`secret.Secret` 對所有 verb 與 JSON 印 `[redacted]`；錯誤只寫變數名稱；HTTP 錯誤（可能含 URL）不離開 package（`conn.go:265-269`）；worker 啟動 log、eval report、spend record、錯誤與 metadata 都有哨兵 key 的測試。Redaction 加了 `\bLLM_[0-9]+_…`（我的 mutant N3 被殺）。Evidence log 中只有變數名稱，沒有任何 key 的值。
- **Redirect**：`CheckRedirect` 回 `http.ErrUseLastResponse`，3xx 是 `request_rejected`（`TestNoRedirects`、mutant C6）。
- **回答上限**：`io.LimitReader(…, 8 MiB + 1)`，超過即 undecoded、至少整個預留（`TestTheResponseBound` 的邊界兩側）。
- **花費**：每次呼叫以 (1 + retries) × attempt 上限預留，attempt 數由同一個 `maxRetries` 決定（`conn.go:218-219`）；有圖片時 input 以 context window 計（input + max_tokens 不超過 window，所以是上限）；SAM 每個 concept (1 + retries) 張。結算：usage 以牌價；可能計費的 attempt（斷線、逾時、body 讀不完、每個 5xx、504）各以上限；讀不了的 2xx 至少整個預留；4xx 為 0；SAM 有回答時 max(1, `images_processed`)。只有一把 key，沒有 failover。兩條路徑共用一個 DB ledger（不同 meter 實例，限額由 DB 的條件 UPDATE 保證）。DB 測試 `TestMetaChargesAreLedgered` 在新 database 上通過。
- **開關**：`GEMINUS_AI_LIVE=on` 時任何不符（provider、record、model、價格、key、照片路徑的批准）都以 typed code 拒絕啟動；批准沒有圖片的照片路徑是 `images_not_approved`（關閉，不是錯誤），符合 H-035 (3)。
- **API 沒有 AI key**：`deploy/railway/variables.json:35` API 的 `forbidden` 含 `GEMINUS_MUSE_API_KEY` 與 `GEMINUS_AI_LIVE`（拿掉時 `scripts/check_deploy.py` exit 1，mutant N5）；web 也禁止；`config.Load` 只在開關 on 時讀 key；architecture rule `live-ai-only-in-the-worker` 禁止 API 連結 meta。
- **Structured output 的搬移**：`git show c96dcfb:internal/ai/provider/anthropic/{encoded,projection}.go` 與 `internal/ai/provider/structured/` 只差 package 子句；T-053 的測試原封不動、經 alias 跑同一份程式並通過；meta 的 `answer` 與 anthropic 的相同（`stop_sequence` 另判為 invalid，更嚴）。所以 T-053 的 fail-closed 性質（一個嚴格 JSON document、≤ 64 層、issue 保留、解不開的值不會 `ready`）不變。
- **Prompt injection 與惡意輸入**：SAM 的結果只保留數字與 server 自己的 concept（concept 以 `^[a-z0-9]+(?:[ -][a-z0-9]+)*$`、≤ 64 bytes、≤ 8 個限制，不能夾帶 box／point prompt 的 token）；grammar 拒絕換行、第二個 frame、prose、JSON、超過 7 位數、指數、confidence > 1、box 與 mask 的 confidence 不同、超過 100 筆、重複 ID、box 出界（client）、NaN（client，mutant N2）；無效 UTF-8 由嚴格 JSON 拒絕。Muse 的回答經 schema 限制、fail-closed 解碼與 server review，注入的文字只是資料（`TestEncodedValues`）。
- **決策狀態**：ADR-030、ADR-027 addendum、D-121–D-123 都是 PROPOSED，`planning/decisions.json` 的 `approved_by`／`approved_at` 為 null；T-055 維持 `not_started`、`reviewer: null`。沒有把 PROPOSED 寫成 APPROVED。

## 4. 我的 mutant（worktree 內，一次一個，`git checkout --` 還原後 `git status` 乾淨）

| ID | 改動 | 結果 |
|---|---|---|
| S10 | `sam.go:127` 拿掉 `call.Reservation == "" \|\|` | KILLED：`TestSAMRefusesBeforeAnyRequest` |
| G10 | `grammar.go:117` 改為 `if false {` | SURVIVED（SAM 與 grammar 測試全過）：等價，見 L1 |
| N1 | `meta.go:361` 接受沒有 usage 的 message | SURVIVED：L3 |
| N2 | `segment.go:388` 不檢查 NaN | KILLED：`TestASegmentationAnswerIsChecked`（「a NaN confidence」） |
| N3 | `redact.go:267` `{6,}` → `{60,}` | KILLED：`TestTextIsSanitized` |
| N4 | `cmd/worker/main.go:271` 不檢查 `v.LiveEnabled()` | KILLED：`TestThePhotoPath/meta_without_the_switch`、`TestRunNamesTheLiveMetaProvider` |
| N5 | `variables.json:35` API 的 forbidden 拿掉 `GEMINUS_MUSE_API_KEY` | KILLED：`check_deploy.py` exit 1（「the API must forbid every AI key」） |

## 5. 執行的指令與結果

Go 1.27.3（`/root/go/pkg/mod/golang.org/toolchain@v0.0.1-go1.27.3.linux-amd64/bin`，`GOTOOLCHAIN=local`、`GOPROXY=off`），本 worktree 的 HEAD `bce77a3`，輸出在 session 的 scratch（不提交）。

| 指令 | 結果 |
|---|---|
| `curl -sS https://dev.meta.ai/docs/<page>.md`（16 頁）與 `/legal/<page>`（3 頁）＋ `sha256sum` | 16 頁 hash 與報告相同；法律頁 HTML 不同、可見文字相同（§1） |
| `go test -count=1 ./internal/ai/... ./internal/config/... ./internal/aievals/... ./cmd/worker/ ./cmd/api/ ./cmd/ai-eval-live/ ./scripts/check-architecture/ ./internal/observability/` | exit 0（18 個 package ok；`structured` 沒有測試檔）。註：這一次我誤設了 `GOFLAGS=-mod=mod`，check-architecture 測試的子程序因而改寫了 `go.mod`（x/sync 由 indirect 改 direct，來自該測試的 probe，T-042 起就有）；已 `git checkout -- go.mod` 還原，之後都不設 GOFLAGS。與 T-055 無關 |
| `go test -count=300 -run TestRetriesBackOff ./internal/ai/provider/meta/` | **exit 1：300 次中 4 次 FAIL**（L1） |
| 7 個 mutant（§4） | 5 killed、2 survived（G10 等價、N1 為 L3） |
| `createdb dt_test_rv055a`；`DATABASE_URL=… DT_ALLOW_DB_TESTS=yes bash scripts/run_db_tests.sh` | exit 0（migration up／down／up 與 SQL 測試） |
| `GEMINUS_TEST_DATABASE_URL=… GEMINUS_REQUIRE_DB_TESTS=1 go test -count=1 -race -v ./internal/storage/aispend/` | exit 0：8 個測試 PASS，含 `TestMetaChargesAreLedgered`，沒有 skip |
| `dropdb dt_test_rv055a`；`SELECT count(*) FROM pg_database WHERE datname='dt_test_rv055a'` | exit 0；0（已刪）。共用 cluster 沒有啟動或停止 |
| `make backend-test GOFLAGS=-count=1`（gofmt、go vet、全部 Go 測試，`GEMINUS_FULL_DIFFERENTIAL=1`） | **exit 0**：81 個 package ok、12 個沒有測試檔、0 FAIL（本 branch 含 T-052，所以比報告的 79 多）；這一次 `TestRetriesBackOff` 沒有碰上 L1 |
| `make architecture-check` | **exit 0**：`PASS architecture: 93 packages, 25 rules, 16 build configurations`（報告為 91，差的是 T-052 的 package） |
| `make spec-check PYTHON=/home/user/Geminus/.venv/bin/python` | **exit 0**（contracts、planning、docs、SQL inventory、reference math、decisions、TS contracts、hashes、DB tests、deploy 全部 PASS）；重新產生的 `checks/*-check.json` 與提交的相同（之後 `git status` 乾淨） |
| `go test -race -count=1 ./internal/ai/... ./internal/aievals/... ./internal/config/... ./cmd/...` | **exit 0**：18 個 package ok，沒有 data race |

## 6. NOT RUN

- 任何 live 呼叫（Meta、Anthropic 或其他 provider；count_tokens、model listing、smoke、eval、SAM）：依指示不執行。
- 沒有讀取、印出或 grep 任何 key 的值，也沒有確認 `GEMINUS_MUSE_API_KEY` 是否存在。
- `make e2e-test`、frontend 測試：依指示不執行。
- 完整的 `make integration-test`（37 個 package 的 db-go-test）：只執行了 db-test（SQL）與 `internal/storage/aispend` 的 DB 測試（T-055 唯一新增的 DB 測試在其中）。
- 整個 repository 的 `make backend-race`：只執行了 AI、config、cmd 範圍的 `-race`。
- Fuzz（`FuzzParseSegmentation`、`FuzzNewImage`）：沒有重跑，只檢查了 evidence（60 s、約 175 萬次；45 s、約 185 萬次，皆 PASS）。
- `make ai-eval`、govulncheck（`go.mod`、`go.sum` 在 T-055 沒有改）：沒有執行。
- Railway 上的任何操作、`scripts/rehearse_railway_deploy.py`。

## 7. 給 coordinator 的摘要

- 必修（Medium）：M1，只改 ADR-030:37、:63、報告 §9 問題 1 與 D-121 (b) 的文字。
- 建議在 live 前一起修：L4（record 的 retention 來源改 H-035 (1)，附測試更新）；L1（固定時鐘，避免驗收時的隨機紅燈）。
- 其餘 Low（L2、L3、L5）可在本切片或 T-056 前修。
- M1 驗證後，count_tokens → `make ai-smoke-live`（`AI_PROVIDER=meta`）→ `make ai-eval-live` 可在這個程式上執行；SAM 的那一步要等 T-056 的 handler，或依 I1 的條件手動進行並註明它不是 adapter 的驗證。
