# T-056 獨立審查 第 1 輪：照片或平面圖 → 模型 proposal、照片路徑的 H-037 預留、API 的照片可用性（D-124）

- 日期：2026-10-26。審查者：獨立 reviewer（H-003；hackathon 模式 H-029 (4)：每個 demo 切片一輪）。我沒有參與這些程式的實作。
- 對象（本 worktree 的 HEAD `095a234`）：(1) T-056：merge `28f6941` 第二個 parent 那一側的 commit（`3c0038f`、`130f3c2`、`5a668d4`、`c57cdbd`、`5b981b4`、`6e45f32`、`b023026`、`268dbd7`、`b1f67ef`；t055-meta 的 commit 已在第一個 parent，不另算）；(2) H-037 在照片路徑上的作用：`5242282`（`internal/ai/provider/meta` 的 count_tokens、`provider` 的 quote 與 deadline）；(3) D-124：`5e57ed7..506370b`（文字）與 `992f5d4..404542f`（照片）：`internal/config/apiai.go`、`ai.go` 的 `decide`／`keyFree`／`liveGate`、`cmd/api`、`cmd/worker` 的 `photoBackends`／`photoPath`、`deploy/railway/variables.json`、`scripts/check_deploy.py`。T-057 的 web commit 不在範圍內。
- 報告：`checks/T-056_VALIDATION.md`（§1–§11、coordinator 一節、「API 端的照片可用性」與其 coordinator 驗證）；`checks/T-055_VALIDATION.md` 的「Review round 1 fixes」、「Live 執行（Meta）」（count：文字 899、demo 照片 6,661 tokens）與 §10。
- 依據：AGENTS.md（binding）、docs/AI_SPEC.md、docs/SECURITY.md、docs/domains/ELECTRONICS_LINE.md §13、H-029、H-033、H-035、H-037（APPROVED）、D-121 (b)、D-124、D-130–D-132（PROPOSED）、ADR-030。
- 方式：只讀（除本檔外沒有改任何 tracked 檔）。測試與 mutant 都在我自己目錄 `/tmp/claude-0/-home-user-Geminus/b4164204-ed28-580b-a7b5-c02b59026d38/scratchpad/rv056/` 中 `git archive HEAD` 的三份副本（`src` 跑基準、`mut` 跑 mutant、`probe` 放我的暫時測試；暫時測試沒有 commit）。**沒有任何 live AI 呼叫**（沒有 count_tokens、SAM、Muse Spark、Claude）；沒有讀取、印出或 grep 任何 key 的值；我的 script 一律 unset AI 變數（只有名稱）。DB 測試只在一次性 cluster（socket `/var/lib/postgresql/geminus-throwaway-hkb6`、port 55450）上我新建的 `dt_test_rv056a`、`dt_test_rv056b`，結束時已 DROP；沒有啟動或停止任何 cluster；沒有執行 e2e／playwright；Railway 沒有任何操作。
- **結論：CHANGES REQUESTED**（2 個 Medium：M1、M2；5 個 Low；7 個 Info）。

## 結論

程式的安全核心經得起核對與 mutant：AI 只產生 proposal（批准仍是既有的 approveProposal，AI 編輯的 allowlist 沒有 `views`）；server 自己驗證、算風險，並加上照片規則；0017 的表有 RLS、composite tenant FK、insert-only、key 前綴 CHECK 與「有影像恰好等於 intent 為空」的 commit 檢查；上傳只收 PNG／JPEG（看開頭 bytes）、5 MiB、header 先檢查像素、重新編碼去掉 metadata；影像中的文字只以像素到 vision model；log、audit、provider metadata 沒有影像、框或檔名。H-037 的 count-before-reserve 在照片路徑上正確：count 失敗或不合理時不預留、不送，預留恰為 3 × (⌈1.1 × count⌉ × 1,250 + 8,192 × 4,250) nano-USD，quote 在呼叫的 deadline 內（我的 C1–C9 全部被殺）；SAM 每個 concept 在呼叫前預留 (1 + retries) 張、未知 usage 依規則結算（S1–S5 全部被殺）。D-124 的 API 與 worker 以同一段 `decide` 判斷，API 不讀 key（D1–D5 全部被殺）。

兩個 Medium 都在 API 的上傳端，不在 live 呼叫的路徑上：

- **M1**：照片 endpoint 的 replay 在 base 變舊之後回 409，而不是第一次的 202（D-011；OpenAPI 的承諾；文字的 createProposal 照常 replay）。我以 DB 測試重現。
- **M2**：上傳的解碼記憶體比文件說的大 4–7 倍：一張 287 KB 的 4096×4096 progressive CMYK JPEG 在 `photo.Sanitize` 中約 449 MiB heap，兩個 decode slot 同時約 0.9 GB；任何 editor（包括 H-035 (2) 帳密寫在提交說明中的評審帳號）都能送出。

**第一次 live SAM 3.1 呼叫（一張 demo 影像）**：M1、M2 修好並由 coordinator 驗證後**可以執行**。兩者都不在 SAM 與 judge 的程式路徑上；照片路徑的預留、結算、count 與拒絕都經過我的核對與 mutant。條件與要看的事見 I1：只經 worker 的照片管線（不要手動 curl）、只用 `testdata/photos/electronics-line-demo.png`、一次一個 proposal；L2 說明一個可重試的 judge 失敗會讓 SAM 在每個 job attempt 重跑（最多 24 張、約 USD 0.06）。

## 1. Findings

### Medium

**M1 — 照片 endpoint 的 replay 在 base 變舊（或 model 不見）之後回 409／404，而不是第一次的 202**

- 位置：`internal/proposals/image.go:176-191`（`CreateFromImage` 的唯讀 transaction 先 `checkBase`（:183）再 `idempotency.Seen`（:187）；base 不再是 latest 時在 replay 之前就回 `ErrStaleBase`）。對照 `internal/proposals/proposals.go:188-198`：文字的 `Create` 先 `idempotency.Claim`（:188），replay 在任何可用性（:197）或 base（`prepare`）檢查之前回傳。契約：`openapi/openapi.yaml:920-921`（createImageProposal：「a retry with the same key and file replays the first response」）、D-130 (e)（`docs/DECISIONS_AND_TBD.md:472`）、D-011。
- 重現（我的暫時 DB 測試 `probe/internal/api/zz_rv056_probe_test.go`，`dt_test_rv056b`）：同一個 editor 以 key `rv056-photo` 送 demo 照片 → `202 {"proposal_id":"aip_…CFHK","status":"generating"}`；同一個 model 發布 version 2（測試的 `publish`）；以同一個 key、同一個檔再送 → **`409 MODEL_VERSION_CONFLICT`**。同樣的順序對文字 proposal（key `rv056-words`）→ 第二次是 **202，與第一次相同**。
- 失敗情境：第一次的 202 在網路上遺失（BFF 逾時、連線中斷），proposal 與 job 已 commit；這時另一位 editor 發布了新版本，或使用者的頁面在重試前重新取得 model。UI 以同一個 key 重試（`photo-form.tsx` 的「同檔同 key」），得到「model 已變更」：使用者以為照片沒有送出，重新整理後再選一次檔（新的 key），**第二個照片 proposal**在背景與第一個一起產生，SAM 與 judge 各花兩次（每次約 USD 0.02＋0.13 的預留），而第一個 proposal 的 202 永遠沒有被告知。這違反 D-011 的 replay 語意與 OpenAPI 的明文承諾，且與已審查的 createProposal 行為不同。（照片路徑關閉時 replay 也是 503：那是 D-124「讀 body 前拒絕」的設計，見 I6，不在本項。）
- 最小修正：唯讀 transaction 中先 `seen, err = idempotency.Seen(ctx, tx, k)`，**只有 `!seen` 才** `checkBase(…, false)`；write transaction 不變（`Claim` 在前，replay 直接回；只有不是 replay 才 `checkBase(…, true)`）。加 DB 測試：202 → `publish` → 同 key 同檔 → 202、body 相同、`ai_proposals`／`ai_proposal_images`／object 都只有 1 個；並保留「同 key 不同檔 → 409 IDEMPOTENCY_KEY_CONFLICT」與「沒用過的 key、舊 base → 409 MODEL_VERSION_CONFLICT」兩個既有的斷言。

**M2 — 解碼的記憶體遠超過文件與 D-130 (b) 的上限：每邊 4096 px 與 2 個 decode slot 擋不住 0.3 MB 的「解壓縮炸彈」**

- 位置：`internal/proposals/photo/photo.go:43-44`（`MaxSide = 4096`）、`:127-129`（「an image of MaxSide² pixels takes about 64 MiB while it is decoded」、`decodeSlots` 2）、`:179-209`（`png.Decode`／`jpeg.Decode` 與重新編碼）；`docs/DECISIONS_AND_TBD.md:472` D-130 (b)；`docs/SECURITY.md:99`（「decompression bomb 在 header 即被拒，一個 process 同時至多 2 個解碼」）。
- 量測（我的暫時測試 `probe/internal/proposals/photo/zz_rv056_probe_test.go`，`runtime.MemStats` 每 2 ms 取樣，Go 1.27.3；影像由 Pillow 12.3.0 與 ImageMagick 在 scratch 產生，都通過 `Sanitize`）：

  | 上傳（4096×4096） | 檔案大小 | heap 峰值（約） | 時間 |
  |---|---|---|---|
  | 16-bit 灰階 PNG | 42 KB | 63 MiB | 0.5 s |
  | 16-bit RGBA PNG | 152 KB | **253 MiB** | 2.2 s |
  | progressive JPEG 4:4:4 | 219 KB | **241 MiB** | 0.6 s |
  | progressive CMYK JPEG | 287 KB | **449 MiB** | 1.5 s |

  原因：Go 的 PNG decoder 把 16-bit RGBA 解成 `NRGBA64`（每像素 8 bytes，128 MiB），重新編碼再一份；progressive JPEG 在解碼期間保存每個 block 的係數（每 block 256 bytes：4096² 的 4 個 component 為 256 MiB）加上影像本身。同一組在每邊 2048 px 下為 16、63、60、**112** MiB。
- 失敗情境：任何 editor（H-035 (2)：評審用的 demo 帳號，帳密寫在提交說明中）同時送兩張約 300 KB 的 CMYK progressive JPEG，並持續重送：API 的 heap 約 0.9 GB 以上（第三個請求等 slot，ctx 允許的期間內一直等）。`deploy/` 與 `Dockerfile.api` 沒有 `GOMEMLIMIT`，也沒有記錄 API 容器的記憶體上限；容器的上限低於這個數字時 API 被 OOM kill，demo 期間所有 tenant 的 API 一起中斷。文件說的 64 MiB 讓 owner 無從判斷。
- 最小修正（擇一，皆需改 D-130 (b) 的數字與註解）：(a) `MaxSide` 降為 2048（最壞約 112 MiB；demo 影像是 1200×520；T-055 review 第 1 輪 I4 也建議 SAM 的影像 ≤ 2048 px 以限制 mask 的回答大小；`proposal.schema.json`、0017 的 CHECK、UI 的說明、`TestWhatIsStoredIsWhatTheProviderSends` 一起改）；或 (b) 保留 4096，但由 header 拒絕 progressive JPEG（SOF2）、4 個 component 的 JPEG 與 16-bit PNG（IHDR 的 bit depth），並把 decode slot 改為依「寬 × 高 × 每像素 bytes」計的加權 semaphore。兩者都加一個測試：最壞的格式（progressive CMYK、16-bit RGBA）經 `Sanitize` 的 `TotalAlloc` 不超過文件寫的上限。另建議在部署設 `GOMEMLIMIT`（runbook）。

### Low

**L1 — 照片規則沒有讀到每個數字：metric 的 `period` 與 `domain_attributes` 中的數字可以是 AI 自己的；calendars 的檢查沒有測試**

- 位置：`internal/ai/review/photo.go:115-125`（`numbers`）與 `:130-185`（`Photo` 只看 nodes、flows、resources、calendars、constraints）；`internal/model/patch/policy.go:20`（`AIEdit` 允許 `metrics` 與整個 `nodes`，含 `domain_attributes`）；`schemas/model-ir.schema.json` 的 `domain_attributes`（`additionalProperties: true`）與 metric 的 `period`。D-132（`docs/DECISIONS_AND_TBD.md:474`）：「candidate 的每個數字必須是 base 已有的值」。
- 重現（`probe/internal/ai/review/zz_rv056_probe_test.go`，對 `testdata/photos/bare-line.json`，5 個 box）：加一個 `period_throughput` metric、`period: 437` → `Photo` **0 個 issue**；加一個新步驟，`domain_attributes` 為 `{"station":"aoi","pass":"first","takt_seconds":437,"first_pass_yield":0.973}` → **0 個 issue**；對照：加一個 period 437 的 calendar → 1 個 issue（規則對，但我的 mutant R5「不檢查 calendars」在所有測試下存活，沒有測試保護它）。
- 失敗情境：照片中的標示（或 vision model 自己）帶來「良率 97.3%」這類數字，寫在新工作站的 `domain_attributes`；server 回 ready，人看到的模型中有一個看似量測過、其實是捏造的數字（electronics-line pack 不讀這些 member，也不警告）。Metric 的 period 不是捏造的結果，但同樣是照片看不出的數字。仍需人批准，所以是 Low。
- 最小修正：`Photo` 也檢查 `candidate.Metrics` 的 `Period`，以及新的或改變的 node 的 `domain_attributes` 中每個 JSON number（不在 base 的數字集合即拒絕；或照片 proposal 的 `domain_attributes` 只接受 pack 認得的字串 member）；`photo.go:18-27` 的註解與 D-132 寫明範圍；`photo_test.go` 加 calendars、metric period、`domain_attributes` 數字三個反向案例。

**L2 — 可重試的 judge 失敗會讓 segmentation 在每個 job attempt 重跑、重付；「最多約 USD 0.02」只對被上限拒絕的情況成立；segmenter 單獨失敗時的 `attempt_failed` audit 沒有測試**

- 位置：`internal/proposals/generator/photo.go:185-197`（每個 attempt 都先呼叫 segmenter）與 `:226-238`（judge）；`jobs.DefaultPolicy` 的 `MaxAttempts: 3`；`checks/T-056_VALIDATION.md:184`、`:242`（coordinator 的回答 7：「接受 segmenter 最多約 USD 0.02 的損失」）；`internal/proposals/generator/generator.go:228`（`asked` 包含只問過 segmenter 的 attempt）。
- 重現（`probe/internal/proposals/generator/zz_rv056_probe_test.go`，計量的 fake）：judge 的預留被上限拒絕（`AI_QUOTA_EXCEEDED`，不重試）→ 1 個 attempt、SAM 8 張（20,000,000 nano-USD）、judge 0：與報告相同。judge 回 429（`AI_RATE_LIMITED`，可重試；count_tokens 的 429／5xx／逾時、`spend_unavailable`、`concurrency_limited` 同理）→ **3 個 attempt、SAM 24 次呼叫、24 張（60,000,000 nano-USD）**。我的 mutant G7（`asked` 不含 segmenter）與 G8（`attempt_failed` 改用不含 segmenter 欄位的 `auditMeta`）在 generator 與 DB（`internal/api` 的照片測試）下**都存活**。
- 失敗情境：Meta 在 demo 期間對 count 或 Messages 回 429（T-055 live 觀察到 count 計入 RPM），每個照片 proposal 付三次 segmentation 才失敗；或之後有人「簡化」`failed`，segmenter 單獨失敗的 attempt 不再寫 `proposal.attempt_failed`，測試照樣綠（花費仍在 spend ledger 與 `ai_spend.settle`，但 proposal 的 audit 少了它）。
- 最小修正：文件（回答 7、D-131 (f)）寫明「每個 job attempt 至多一次 segmentation：可重試的失敗最多 3 次，約 USD 0.06」；或把 segmentation 的結果（框與 metadata，不含影像）以 proposal 的 checkpoint 保存，重試時沿用；或先以 judge 的 quote 預留再呼叫 segmenter。加測試：segmenter 單獨失敗的 attempt 寫一筆 `proposal.attempt_failed`，含 `segmenter_cost_nano_usd`、`segmenter_spend_reservations`。

**L3 — API 會為 worker 一定拒絕的 domain 存下照片（D-124 的「API 接受 worker 拒絕的東西」）**

- 位置：`internal/api/api.go:115`（`proposals.Service` 沒有 pack registry，雖然 `Handler` 有 `served`）；`internal/proposals/image.go:99-112`、`:194-215`（不看 model 的 domain 就解碼、存 object、建 proposal）；`internal/proposals/generator/photo.go:157-160`（`PHOTO_NOT_SUPPORTED`）。`internal/api/photo_test.go:778-800` 把這個行為當成預期（software_delivery 的 model：202，之後 failed）。
- 失敗情境：直接呼叫 API（UI 只在 electronics_line 顯示按鈕）為 software_delivery 的 model 上傳照片：API 解碼、存下使用者的影像、建 proposal 與 job、寫 audit，worker 才以 `PHOTO_NOT_SUPPORTED` 失敗。影像留在私有 storage，沒有任何用途，也沒有刪除的路徑。沒有 AI 花費，所以是 Low。
- 最小修正：`Service` 帶 pack registry；`CreateFromImage` 的唯讀 transaction 讀到 base version 時解出 domain，pack 不是 `domainpack.PhotoVocabulary`（或概念為空）即在存任何東西之前回 422（`/base_version_id`，details code `PHOTO_NOT_SUPPORTED`，openapi 先改）；`photo_test.go` 的這個子測試改為 422、0 個 object。

**L4 — 「用過的 key 不再解碼、不再存檔」沒有測試保護；同 key 的並行請求各存一個 object**

- 位置：`internal/proposals/image.go:192-215`（`!seen` 才解碼與存檔）；D-130 (e)；報告 §1、`image.go:44-45` 的註解（「A retry with the same key is only hashed, never stored again」）。
- 重現：我的 mutant I2（`if true || !seen`：replay 也重新解碼、另存一個 object）在 `TestPhotoIdempotency` 與全部照片 DB 測試下**存活**（測試只數 proposal 與 job，不數 object）。並行：`probe` 的 `TestRV056ConcurrentSameKey` 以同一個 key 同時送 4 次 → 4 個都是同一個 202，**1 個 proposal、3 個 object**（2 個孤兒）。
- 失敗情境：之後的修改讓 replay 每次都存一份使用者的照片，測試照樣綠；並行的重複請求（雙擊、BFF 重試）留下沒有 record 的使用者影像。報告只把 transaction 失敗列為孤兒的來源（§8）。
- 最小修正：`TestPhotoIdempotency` 在 replay 前後比較 `e.files(t)` 的數量；D-130 (e) 與報告 §8 加上「同 key 的並行請求」為孤兒的另一個來源（T-016 的 D-044 maintenance 處理）。

**L5 — 其他沒有測試保護的檢查（mutant 存活）**

- `internal/proposals/image_http.go:86`（H1：`filename` 參數的欄位被當成欄位接受）與 `:71`（H3：任意 `Content-Transfer-Encoding` 被接受）：在 `internal/proposals` 與全部照片 DB 測試下都存活；OpenAPI 說這些部分被拒絕。
- `internal/proposals/generator/photo.go:109`（G2：`objects.InArea` 拿掉）：存活，因為「a key of another organization」的案例在 fake／FS store 中本來就讀不到（`PHOTO_CORRUPT`「missing」），這個檢查從來不是拒絕的那一個。0017 的 key CHECK 是第一道防線，所以只是測試缺口；可讓測試的 store 對那個 key 回傳正確的 bytes，斷言仍是 `PHOTO_CORRUPT` 且沒有呼叫 segmenter。
- `internal/proposals/image.go:239`（I3：write transaction 不鎖 model 列）：存活；沒有「預檢與 commit 之間發布新版本」的並行測試（AGENTS.md §5 的 concurrency regression）。
- `internal/proposals/generator/photo.go:245`（G3：照片規則多算一個 box）：存活；邊界只在 `review` 的單元測試（R1、R3 被殺），經管線的物件數沒有邊界案例。
- `internal/ai/review/photo.go:178`（R5：calendars）：見 L1。
- 修正：各加一個反向測試（multipart 的 `filename` 欄位與 CTE 回 400；InArea；以兩個 transaction 交錯的 DB 測試；管線的 boxes = 新步驟數 − 1）。

### Info

- **I1 — 第一次 live SAM 3.1 呼叫的條件與要看的事。** 程式路徑只有 worker 的照片管線（`cmd/ai-eval-live` 沒有照片模式）：API 與 worker 都設 `AI_PROVIDER=meta`、`AI_POLICY_APPROVAL=H-029`、`AI_MODEL=muse-spark-1.3`（照片路徑跟隨）、`GEMINUS_AI_LIVE=on`，worker 有 key、DB ledger 與 object storage；一次一個 proposal，只上傳 `testdata/photos/electronics-line-demo.png`（1200×520，14,197 bytes）。預留：SAM 8 個 concept 各 7,500,000（依序，各自在呼叫前）、judge 約 131,928,000 nano-USD（count 6,661 時）；一個 job attempt 至多約 USD 0.19，三個 attempt 至多約 USD 0.58。要看：SAM 回答的 frame 是否正是 1200×520（不是時每個 concept 為 invalid，`FrameMismatch`／`/concepts/<i>/frame`）、`images_processed` 是否為 1（大於 3 時 `SpendOverReservation`）、8 個 concept 的措辭是否找得到東西（§9 問題 9）、count 與 Messages 回答的 `usage.input_tokens` 之差（H-037 的未決事項；超過 1.1 倍要回報 owner）、judge 回答的 `model` 字串。
- **I2 — D-124 的另一個（這個 build 到不了的）不一致。** `cmd/worker/main.go:285-294`：live 的照片路徑在批准沒有 segmentation model 時把 segmenter 設為 `Disabled{NotConfigured}`，而 API 的 `APIVision.LiveEnabled` 不看 segmentation model，所以 API 接受、worker 以 `AI_PROVIDER_NOT_CONFIGURED` 失敗。H-029 有 `sam-3.1`、H-018 沒有圖片，所以現在到不了；`config.AIVision.SegmentationModel` 的註解「"" for none: the provider judges the photo alone」也不是 generator 的行為（generator 一定要 segmenter）。`cmd/worker/apiphoto_test.go:65` 的等價測試只比 judge 的 `AcceptsImages`，沒有比 segmenter。之後加 record 時一併處理（liveGate 要求有圖片的批准也有 segmentation model，或測試也比 segmenter 是否為 Disabled）。Claude 收圖片的情況（coordinator 對問題 3 的處理）同理，已記錄。
- **I3 — OpenAPI 描述的順序與程式不同。** `openapi/openapi.yaml:908-916` 先描述檔案的 415／413／422，再說「Then, as createProposal does, base_version_id …（404、422、409）」；程式在唯讀 transaction 中先檢查 base（`image.go:183`），之後才 `Sanitize`（`:195`），所以舊 base 加上 GIF 得到 409 而不是 415。改描述即可（M1 修正後仍是 base 在前）。
- **I4 — 每個 organization 的同時呼叫數是每個 client 各自的。** 文字的 client、照片的 judge 與 segmenter 各有 `MaxConcurrentPerOrganization`（預設 2）的 `active` map（`provider.go:282`、`segment.go` 的 `acquire`），所以一個 organization 同時可有 2 個文字＋2 個照片的呼叫；花費仍由 DB ledger 的條件 UPDATE 限制。只是註記。
- **I5 — 花費紀錄分兩處。** Worker 的照片 proposal 記在 DB ledger（0016）；`checks/ai-evals/live-spend.jsonl` 今天已有 USD 2.363289（T-053 與 T-055 的 live run），DB ledger 看不到（D-113 (b) 已知的缺口）。第一次 live 照片 proposal 若在新的 database 上，部署日上限從 0 算起；coordinator 需手動合計。
- **I6 — 照片路徑關閉時 replay 也是 503。** `BeginImage` 在讀 body 前檢查（D-124 的設計），所以照片路徑在第一次請求之後被關閉時，同 key 的 replay 得到 503，與文字的 createProposal（Claim 在可用性之前）不同。符合 D-124 的文字；修 M1 時不必改，但可在 D-124 註明。
- **I7 — count_tokens 不進 ledger，且計入 RPM。** H-037 的 count 經同樣的重試規則（`MaxRetries` 2），所以一次照片 proposal 的 judge 最多 3 次 count＋3 次 Messages；T-055 live 觀察到 count 計入 `x-ratelimit-remaining-requests`。SAM 8 次加上這些，在 demo 的 RPM（3000）內沒有問題；只是註記。

## 2. 已確認的性質

- **AI 只建議**：照片路徑只產生 edit proposal（`generatePhoto` 的輸出照一般 edit 審查，之後 `review.Photo`、dataset binding），批准仍是人經 approveProposal；`AIEdit` allowlist 沒有 `views`、`metadata`、`time_unit`，所以沒有 2D 位置（`TestAPhotoProposalEndToEnd` 檢查 version 2 沒有新位置）。宣稱權限的回答為 invalid（`TestPhotoOutcomes/an_answer_that_claims_authority`）。
- **Server 的驗證與風險**：照片規則（新 process 步驟 ≤ box 數；數字為 base 的值，capacity 另可 1..box 數）由 server 計算；R1、R2、R3、R6、R8、R9 被殺，R4（capacity 0：schema 的 minimum 1）、R7（constraint 的 `equals` 為 const 1）等價。
- **Tenant**：0017 的 `ai_proposal_images` 有 `organization_id`、composite FK、RLS（ENABLE＋FORCE、USING＋WITH CHECK）、只有 `twin_api` 的 SELECT／INSERT 與 `twin_worker` 的 SELECT、UPDATE 對所有 role 拒絕、key 前綴 CHECK、trigger 要求同一 transaction、generating、edit、intent 為空、建立者為 scoped user；`proposal_source_at_commit` 在 commit 時檢查「intent 為空 ⇔ 有影像」。Storage 的 join 帶 `organization_id`。Worker 讀影像前檢查 key 在該 organization 的區域，讀回後以 record 比對 digest、大小、型別、像素（P4、P6 等價；P5 被殺）。`make integration-test`（含 `db/tests/proposal_images.sql`）在新 database 上通過。
- **上傳**：型別只看開頭 bytes；5 MiB 由 `MaxBytesReader` 與 `photo.Read` 強制，不依賴 Content-Length；像素先由 header 檢查（P1、P2、P7 被殺）；重新編碼後只留像素；檔名不存、不記、不進 audit；client 以固定檔名 `photo` 送出。記憶體見 M2。
- **注入**：segmenter 的回答只有 server 的 concept、整數框與 confidence，經 T-055 client 與 `aicontext.checkPhoto` 兩次檢查（A1、A2、A3 被殺）；SEGMENTS 是 untrusted_data；影像中的文字只以像素送出；fake 的注入回答（`electronics-line-injection.*`）的 prose 保持為文字。
- **花費**：每個 SAM concept 與 judge 都在呼叫前預留（`TestAMeteredPhotoCall`；S5 被殺）；judge 的 vision client 在 segmenter 之前被問是否收影像（G11 被殺）；H-037：count 的 body 即 Messages 的 body 去掉 `max_tokens`，count 失敗、為 0、超過 window、或批准沒有圖片時不預留、不送（C3–C6 被殺），margin 為 ⌈10%⌉（C1、C2 被殺），quote 在呼叫的 deadline 內且保留 typed code（C7、C8 被殺）；結算：未知的 attempt 以每 attempt 上限、讀不了的回答至少整個預留（S2–S4 被殺）。依 T-055 live 的 count 6,661，judge 的預留為 131,928,000 nano-USD，與報告相同。
- **D-124**：API 以 `decide(…, keyFree)`、worker 以 `decide(…, loadLive)`，同一段判斷；API 只把 key 變數的值與空字串比較後丟棄；照片路徑關閉時在讀 body 前 503，`details[0].code` 為 worker 的 `AI_IMAGE_UNSUPPORTED`；`variables.json` 的 api 與 worker 對七個設定宣告相同，`check_deploy.py` 檢查（D5 被殺）。D2（照片路徑的批准忽略開關）、D3（不要求圖片）、D4（API 的照片 provider 用文字的）都被殺。API 接受而 worker 拒絕的情況：L3（domain）與 I2（到不了）。
- **錯誤**：refusal 有 typed code 與 JSON Pointer（`/file`、`/base_version_id`）；取消在 segmenter 與 vision 之間傳遞，不是 failure（`TestPhotoFailures`）。
- **決策狀態**：D-124、D-130–D-132 仍是 PROPOSED；沒有把 PROPOSED 寫成 APPROVED。

## 3. 我的 mutant（`mut` 副本；每個只改一處、跑指定的測試、還原並逐 byte 比對；結束後 `diff -r` 與 HEAD 相同）

60 個 mutant：**42 killed、18 survived**（其中 8 個等價）；第一輪有 7 個寫法不能 build（INVALID，不算 killed），改寫後重跑。DB mutant 用 `dt_test_rv056a`（已由 `make integration-test` migrate），`GEMINUS_REQUIRE_DB_TESTS=1`。

| 範圍 | Killed | Survived |
|---|---|---|
| `review.Photo` | R1（steps+1）、R2（flow 機率）、R3（capacity box+1）、R6（resource 的分布）、R8（queue capacity）、R9（未知的 segmenter issue） | R4（等價：capacity ≥ 1）、R5（calendars：L1）、R7（等價：equals const 1） |
| 照片管線 | G1（65 個物件）、G4（invalid → failure）、G5（scenario role）、G6（只問第一個 concept）、G9（metadata 沒有 segmenter）、G11（不問是否收影像）、G12（audit 沒有影像 hash） | G2（InArea：L5）、G3（box+1：L5）、G7、G8（attempt_failed：L2）、G10（等價：讀取上限只影響記憶體） |
| aicontext | A1（框的右邊界）、A2（別的 concept）、A3（別的 organization） | — |
| 上傳 | P1、P2（像素邊界）、P5（Verify 的像素）、P7（> 5 MiB 也解碼） | P3（等價：`Objects.Put` 的上限同樣 413）、P4、P6（等價） |
| HTTP | H2（重複的欄位，DB） | H1、H3（L5） |
| SAM 計量 | S1–S5 | — |
| H-037 | C1–C9 | — |
| D-124 | D1–D5 | — |
| 照片 endpoint（DB） | I1（fingerprint 不含檔案）、I4（audit 的 source） | I2（replay 重新存檔：L4）、I3（不鎖 model：L5）、I5、I6、I7（等價：FS 自算 digest；BeginImage 已檢查；缺 base_version_id 仍是 422） |

## 4. 執行的指令與結果

Go 1.27.3（`/root/go/pkg/mod/golang.org/toolchain@v0.0.1-go1.27.3.linux-amd64/bin`，`GOTOOLCHAIN=local`、`GOPROXY=off`，不設 `GOFLAGS` 以外的旗標），HEAD `095a234`。Log 在 scratch 的 `rv056/logs/`（不提交）。

| 指令 | exit | 結果 |
|---|---|---|
| `go test -count=1`（`internal/proposals/...`、`internal/ai/...`、`internal/config/...`、`cmd/api`、`cmd/worker`、`internal/audit`、`internal/domainpack/electronicsline`、`internal/observability`、`scripts/gen-photo-demo`、`prompts`；worktree） | 0 | 24 個 package ok |
| `createdb … dt_test_rv056a`；`make integration-test`（`src`，`DT_ALLOW_DB_TESTS=yes`） | 0、0 | `make db-test`（0001–0017 up／down／up 與全部 SQL 測試，含 `proposal_images.sql`）＋ `make db-go-test`（`-race -p 1`、`GEMINUS_REQUIRE_DB_TESTS=1`）：38 個 package ok，沒有 FAIL 或 SKIP |
| `createdb … dt_test_rv056b`；`make db-test`（`probe`） | 0、0 | 給暫時測試用 |
| `go test -run TestRV056 ./internal/api/`（`probe`，`dt_test_rv056b`） | 1 | **M1**：照片的 replay 409、文字的 replay 202（刻意斷言失敗）；並行同 key：1 個 proposal、3 個 object（L4） |
| `go test -run TestRV056 ./internal/ai/review/`（`probe`） | 0 | L1 的三個案例（記錄 issue 數） |
| `go test -run TestRV056 ./internal/proposals/photo/`（`probe`，4096 與 2048 兩組影像） | 0、0 | M2 的量測 |
| `go test -run TestRV056 ./internal/proposals/generator/`（`probe`） | 0 | L2 的量測 |
| `python -I bin/mutants.py mut …`（三輪：不需 DB、DB 與修正的寫法） | 0、0、0 | §3：42 killed、18 survived |
| `make backend-test "GOFLAGS=-count=1 -timeout=30m"`（`src`） | **0** | gofmt、`go vet`、83 個 package ok、12 個沒有測試檔、0 FAIL（`scripts/check-architecture` 283 s） |
| `go test -race -count=1 -timeout 30m`（同第一列的範圍，`mut` 還原後） | **0** | 23 個 package ok，沒有 data race |
| `make architecture-check`（`src`） | **0** | `PASS architecture: 95 packages, 26 rules, 16 build configurations` |
| `make ai-eval`（`src`） | **0** | fake corpus 的 context gate 與 server review；照片沒有 eval case（報告 §8） |
| `dropdb dt_test_rv056a`、`dropdb dt_test_rv056b`；`SELECT count(*) FROM pg_database WHERE datname LIKE 'dt_test_rv056%'` | 0、0；0 | 已刪除；cluster 沒有啟動或停止 |
| `make spec-check PYTHON=/home/user/Geminus/.venv/bin/python`（worktree，本檔寫完後） | **0** | contracts、planning、docs、SQL inventory、reference math、decisions、TS contracts、hashes、DB tests、deploy 都 PASS（不含官方完整 OpenAPI validator）。重新產生的 `checks/docs-check.json` 只有 `markdown_files` 137 → 138（本檔）；依指示只提交本檔，所以以 `git checkout -- checks/docs-check.json` 還原，由 coordinator 合併時重新產生（T-055 review 第 1 輪的 commit 同樣只含 review 檔）。其他 `checks/*-check.json` 沒有改變 |

## 5. NOT RUN

- 任何 live 呼叫（SAM 3.1、Muse Spark、count_tokens、Claude）；`make ai-eval-live`、`make ai-smoke-live`。
- `make e2e-test`、playwright（依指示；另一個執行使用 3100–3102）；`make frontend-test`、`make frontend-build`（worktree 沒有 `node_modules`；不在 scratch 中連到主 checkout 的 `node_modules`，以免寫入其 cache）。UI 的照片按鈕只讀了程式（`photo-form.tsx`、`client.ts`）。
- 整個 repository 的 `make backend-race`：只跑了 §4 範圍的 `-race`。
- S3 adapter 上的照片（只用 FS adapter）；API 容器在 Railway 的實際記憶體上限（M2 的後果取決於它）；Railway 上的任何操作。
- Fuzz：沒有執行。
- T-057 的 web commit（範圍外）。

## 6. 給 coordinator 的摘要

- 必修（Medium）：**M1**（`image.go` 的唯讀 transaction 先 `Seen` 再 `checkBase`，加 replay-after-publish 的 DB 測試）；**M2**（`MaxSide` 2048，或由 header 拒絕 progressive／CMYK JPEG 與 16-bit PNG；改 D-130 (b) 與註解的數字；加記憶體上限的測試）。
- 建議一起修：L1（照片規則讀 metric 的 period 與 `domain_attributes` 的數字；calendars 的測試）、L4（replay 不存檔的測試）。L2 至少改文件的「最多約 USD 0.02」。
- 其餘 Low（L3、L5）可在本切片或之後修。
- 第一次 live SAM 3.1 呼叫（一張 demo 影像）：M1、M2 修好並驗證後可以執行，條件與觀察項目見 I1、I5。
