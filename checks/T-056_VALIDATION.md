# T-056 驗收報告：照片或平面圖 → 產線佈局的模型 proposal（hackathon 切片）

- 日期：2026-10-26；分支 `t056-photo`（本機，未 push）；agent 的自我檢查，**不是驗收**（H-003 的獨立 review 尚未進行）。
- 依據：H-033（SAM 3.1 找機台與輸送帶、Muse Spark 判斷工作站與流向、Claude 的影像輸入為可切換的備援）、H-029（只用 demo 影像）、T-056 scope。
- `planning/tasks.json` 的 T-056 維持 `not_started`：`scripts/check_decisions.py` 要求 `in_progress` 的任務其相依任務都是 review／done，而 T-055 仍是 `not_started`；工作記在 `completion_evidence`。
- **沒有任何 live AI 呼叫**（SAM 3.1、Muse Spark、Claude 都沒有）；沒有讀取任何 key 或 secret 的值；測試只用 fake provider、fake segmenter 與 synthetic demo 影像。
- 本報告新增的決策 D-130、D-131、D-132 皆 **PROPOSED**，agent 沒有批准（`docs/DECISIONS_AND_TBD.md`）。

## 1. 結論

交付一條完整、可測的照片路徑（fake 為止）：

1. **Contract**（先寫）：`POST /v1/models/{id}/ai-proposals/image`（`createImageProposal`，multipart，editor／owner），`proposal.schema.json` 的 `source_image`（型別、像素、大小、digest；只能是 edit；沒有檔名），OpenAPI 範例、fixture（正向 2、反向 4）、TS 型別與 client contract test。
2. **上傳**：只收 PNG／JPEG（由開頭 bytes 判斷）、至多 5,242,880 bytes、每邊 32–4096 px（先讀 header）、解碼後以同一格式重新編碼，只留像素（EXIF、XMP、ICC、文字 chunk、檔尾 bytes 都不存、不送），影像存在 `tenant/<org>/assets/proposal-images/<aip>/image`，migration 0017 記錄它（RLS、tenant FK、insert-only、有影像恰好等於 intent 為空）。
3. **Worker**：讀回影像並與 record 比對 → T-055 的 `SegmentClient`（pack 的 8 個概念）→ server 的物件檢查（框在影像內、confidence 有限、至多 64 個、frame 為影像本身）→ T-055 的 judge（影像為 prompt 的影像部分）→ 一般 edit 審查 + 照片規則 → ready／invalid／needs_input／failed；沒有物件時 server 回 needs_input，不捏造步驟；每個失敗都是 typed failure，什麼都不寫一半。
4. **UI**：電子產線模型頁（editor／owner）在「請 AI 提出修改」區塊、「送給 AI」旁一個按鈕「從照片提出…」（開啟只收 PNG／JPEG 的選檔），下方一行說明（含「只使用示範影像」）；未存的編輯時與「送給 AI」一樣 blocked、說明理由、不開啟選檔；送出後到 `/proposals/<id>`，該頁標明以照片提出；en 與 zh-Hant；`/ui-gallery/proposal?state=photo` 範例。第一版是模型頁上獨立的一個區塊，第一次 `make e2e-test` 的 `editor.spec.ts`（滑鼠拖曳步驟）因此失敗：多出的區塊把畫布推到 900 px 高的視窗之外；改為放在既有的 ask 區塊內（一個 AI 區塊、畫布位置幾乎不變），第二次 `make e2e-test` 見 §7。
5. **Demo 影像**：`scripts/gen-photo-demo` 畫的 synthetic 平面圖（`testdata/photos/`），與 fake segmenter 的回答（`*.segments.json`）、fake vision model 的回答（`*.proposal.json`）。

與 T-055 的關係：開工時 T-055 的名稱尚未可見，T-056 先以自己的小介面實作（`internal/ai/vision`、`Request.Image`、`GenerateImageProposal`）。T-055 在 `t055-meta` 提交 `19fb02a`（`provider.Image`、`Prompt.Images`、`provider.Segmenter`）後，依 brief「看得到 T-055 的名稱就採用」，把它合併進本分支並改用它的名稱，刪掉自己的介面；T-055 再提交 `339fea5`（cmd/worker 的 `deps.photo`：judge 與 segmenter）後再合併一次，generator 改用它的 judge。因此本分支**包含 t055-meta 到 339fea5 的 commit**；coordinator 合併兩個分支時，這些 commit 是同一批（不需 rebase），之後 T-055 若再有 commit，本分支需要再合併一次（見 §9 問題 1）。

## 2. 設計

```
Browser (PhotoForm) ──multipart──> BFF ──> API createImageProposal
  1. role、AI 可用性、object storage（讀 body 前）
  2. 欄位 → 唯讀 tx：base 是否 latest、key 是否用過
  3. photo.Sanitize（判斷型別、header、解碼、重新編碼）→ 存 object
  4. tx：idempotency claim、proposal(generating, edit, intent='') + ai_proposal_images、
         model_proposal job、audit proposal.create{source:image}、202
Worker generator.Handle（proposal.Image != nil → generatePhoto）
  a. pack 的 PhotoConcepts；Vision 收影像嗎（否：AI_IMAGE_UNSUPPORTED，不花錢）
  b. dataset preload／verify（同文字 proposal）
  c. readImage：key 在本 org、讀回、photo.Verify（digest、大小、型別、像素）、provider.NewImage
  d. Segmenter.Segment（T-055 SegmentClient；live 時每個概念預留）
       invalid_output → review.Segmentation → invalid
       0 個物件 → server 的 needs_input；>64、frame 不符 → invalid
  e. aicontext.ImageProposal（photo-0.1.0；IMAGE、SEGMENT_CONCEPTS、SEGMENTS、pool、IR、index）
  f. Vision.GenerateModelProposal（judge；影像在 Prompt.Images）
  g. review.Review → review.Photo → datasets.Resolve → 存 outcome、audit proposal.generate
```

重點：

- **授權不變**：照片路徑只產生 proposal；server 驗證、算風險；人批准（既有的 approveProposal）。AI 編輯的 allowlist 沒有 `views`，所以 proposal 不帶 2D 位置（`TestAPhotoProposalEndToEnd` 檢查 version 2 沒有新的位置）。
- **注入**：segmenter 的回答沒有文字（只有 server 的概念、編號、框、confidence），且 aicontext 再檢查一次；影像中的文字只以像素到 vision model；vision model 的名稱與 prose 一律是資料（JSON 字串，block 不能被關閉），永遠要人批准。
- **花費**：T-055 的 `NewMeteredSegmentClient` 在每個概念呼叫前預留 (1 + retry) 張影像；judge 照 T-053 預留。被拒即不呼叫（`TestAMeteredPhotoCall`）。
- **照片規則**（`review.Photo`，D-132）：新的 process 步驟不得多於物件數；每個數字必須是 base 已有的值（capacity 另可為 1 到物件數）。

## 3. Contract 與資料

| 檔案 | 內容 |
|---|---|
| `schemas/proposal.schema.json` | `source_image`（`media_type` png／jpeg、`width`／`height` 32–4096、`size_bytes` 1–5,242,880、`content_hash`；`additionalProperties: false`）；有 `source_image` 時 `mode` 必須是 edit |
| `openapi/openapi.yaml` | `createImageProposal`（202 ProposalAccepted；400、401、403、404、409、413、415、422、503），getProposal 範例 `ready_from_photo` |
| `planning/api-operations.json` | 新 operation（PROPOSED、owner／editor） |
| `testdata/contracts/` | `valid-proposal-generating-from-image.json`、`valid-ready-proposal-from-image.json`；反向：scenario 帶影像（`/mode`）、GIF（`/source_image/media_type`）、太寬（`/source_image/width`）、帶檔名（`/source_image`） |
| `migrations/0017_proposal_images.{up,down}.sql` | `twin.ai_proposal_images`；trigger `protect_ai_proposal_image`；deferred `proposal_source_at_commit`；`db/tests/templates/proposal_images.sql.tmpl` |
| `apps/web/lib/contracts/proposal.ts` | 由 `scripts/gen_ts_contracts.py` 產生 |

上限由 10,000,000 bytes 改為 5,242,880：T-055 的 `provider.MaxImageBytes` 是 5 MiB，原本的上限會存得下而送不出（0017 尚未 push，直接修改；`TestWhatIsStoredIsWhatTheProviderSends` 守住兩者）。

## 4. 介面（T-055 應實作／已實作的部分）

T-056 依賴的都是 T-055 已提交的名稱（t055-meta `19fb02a`、`339fea5`）：

| 名稱 | 由誰 | T-056 怎麼用 |
|---|---|---|
| `provider.Image`、`provider.NewImage`、`Image.Check`、`ImagePNG`／`ImageJPEG`、`MaxImageBytes`、`MaxImageSide` | T-055 | worker 讀回影像後 `NewImage`；上傳上限不超過它們 |
| `provider.Prompt.Images`（封進 InputHash）、`provider.ImageInput`（`AcceptsImages() bool`） | T-055 | `aicontext.ImageProposal` 把影像放進 prompt；只有 `ImageInput` 收得到 |
| `provider.Segmenter`、`SegmentRequest`、`Segmentation`、`Segment`、`Box`、`SegmentMetadata`、`SegmentClient`、`NewSegmentClient`／`NewMeteredSegmentClient`、`SegmentBackend`、`BillableSegmenter`、`Disabled.SegmentOne`、`MaxSegmentConcepts`、`MaxSegmentsPerConcept` | T-055 | `generator.Config.Segmenter`；檢查失敗（`CodeInvalidOutput` 的 issue path）由 `review.Segmentation` 轉成 server 的句子 |
| `fake.NewSegmenter`（依概念 script）、`fake.Backend.AcceptsImages` | T-055 | 所有測試 |
| `spend.ImagePriceOf("sam-3.1")` | T-055 | 計量測試 |
| cmd/worker `deps.photo{Judge, Segmenter}`、`photoPath`、`photoBackends`（`AI_VISION_PROVIDER`） | T-055 | `handlers` 把它們交給 `generator.Config.Vision`／`Segmenter`（不放 typed nil） |

T-056 自己加的小介面（T-055 不需實作，但合併時要保留）：

- `provider.(*Client).AcceptsImages() bool`（`internal/ai/provider/accepts.go`）：讓 generator 在 segmenter 花錢之前知道 judge 收不收影像。
- `fake.Backend` 的 key：沒有 intent／question／columns 而有影像的 prompt，以第一張影像的 digest 對照 script（`fake.go` 的 `keyOf`）。
- `domainpack.PhotoVocabulary{ PhotoConcepts() []string }`（電子產線實作）。
- `generator.Config.Vision provider.LLMProvider`、`generator.ImageUnsupported = "AI_IMAGE_UNSUPPORTED"`。

Claude 的影像備援（H-033 (3)）：**沒有接**。T-053 已審查的 Anthropic adapter 不是 `provider.ImageInput`，接上要改它的 `Complete`（把 `Prompt.Images` 當 image content block 放在文字前）、`Quote`（影像的 input token 上限）、`AcceptsImages`（讀 approval record）與 approval record 的資料政策（H-018 沒有影像），不是小改動，所以只留 seam：`internal/ai/provider/anthropic/image_seam_test.go` 在它變成 `ImageInput` 時刻意失敗；judge 不收影像時照片 proposal 以 `AI_IMAGE_UNSUPPORTED` 失敗、不花錢。

## 5. 測試

Go（無 DB）：

| 測試 | 涵蓋 |
|---|---|
| `internal/proposals/photo`：`TestSanitizeKeepsOnlyThePixels`、`TestFormatIsReadFromTheBytes`、`TestSanitizeRefuses`、`TestRead`、`TestVerify`、`TestWhatIsStoredIsWhatTheProviderSends` | EXIF／文字 chunk／檔尾被去掉、像素相同；由 bytes 判斷型別（GIF、WebP、BMP、TIFF、SVG、PDF、HEIC、zip、ELF、文字都拒絕）；空檔、截斷、像素過小過大、header 宣稱 60000×60000 的 decompression bomb、超過 5 MiB、取消；讀取上限；record 比對；存的影像就是 provider client 送的 |
| `internal/ai/provider`：`TestTheClientSaysWhetherItTakesImages`；`fake`：`TestAPhotoIsMatchedOnItsImage`；`anthropic`：`TestTheAdapterSendsNoImageYet` | `AcceptsImages`；fake 以影像 digest 對照；Claude seam |
| `internal/ai/review`：`TestPhotoAcceptsWhatThePhotoShows`、`TestPhotoRefusesMoreStepsThanBoxes`、`TestPhotoRefusesNumbersOfItsOwn`、`TestPhotoIssuesAreClean`、`TestSegmentation` | 照片規則；segmenter 的拒絕變成 server 的句子、path 為 "" |
| `internal/ai/aicontext`：`TestImageProposalContext`、`TestImageProposalRefuses`、`TestImageProposalFitsTheBudget` | block 的種類與順序、影像在 `Prompt.Images`、沒有 intent、沒有 views；跨 tenant、概念、框在影像外、NaN／Inf confidence、太多物件、影像被改、WebP、預算 |
| `internal/domainpack/electronicsline`：`TestPhotoConcepts` | 8 個、合 T-055 的規則、回傳副本 |
| `internal/proposals/generator`：`TestThePhotoDemo`、`TestNoBoxAsksForInput`、`TestSegmentationsTheServerRefuses`、`TestPhotoAnswersTheServerRefuses`、`TestPhotoInjectionStaysText`、`TestPhotoFailures`、`TestAMeteredPhotoCall` | 見下表 |
| `internal/proposals`：`TestPublicShowsThePhoto`、`TestRefusedPhoto`；`internal/audit`；`prompts`；`cmd/worker`：`TestTheProposalHandlerTakesThePhotoPath`；`scripts/gen-photo-demo`：`TestTheCommittedFilesAreReproducible` | 公開形狀、錯誤對應、audit 欄位、template、組裝、demo 可重現 |

brief 要求的反向測試：

| 要求 | 測試 |
|---|---|
| 框在影像外 | `TestSegmentationsTheServerRefuses`（outside、huge、negative）、`TestImageProposalRefuses`、`TestPhotoOutcomes/a_box_outside_the_image`（DB） |
| 步驟過多 | `TestPhotoRefusesMoreStepsThanBoxes`、`TestPhotoAnswersTheServerRefuses/more stations than segments`、`TestPhotoOutcomes/too_many_steps`；物件過多：`more objects than a concept/photo takes` |
| NaN 或巨大的數字 | `TestSegmentationsTheServerRefuses`（NaN、Inf confidence、2^40 的框）、`TestPhotoAnswersTheServerRefuses`（a huge capacity 1e12、invented service time／yield） |
| OCR 般的標籤與模型輸出中的 prompt injection | `TestPhotoInjectionStaysText`（影像的標示只以像素送出、block 不變、`</untrusted_data>` 的名稱留作文字、不是批准）、`TestPhotoAnswersTheServerRefuses/an answer that claims authority`、`TestPhotoOutcomes/injection_stays_text`、`/an_answer_that_claims_authority`（DB：沒有版本、沒有決定者）；segmenter 沒有文字欄位，`TestImageProposalRefuses` 拒絕不是請求概念的物件 |
| 不支援的影像類型 | `TestFormatIsReadFromTheBytes`、`TestSanitizeRefuses`、`TestPhotoRefusals`（GIF、SVG、CSV、空檔、截斷）、UI 的 GIF |
| 過大的影像 | `TestSanitizeRefuses`、`TestRead`、`TestPhotoRefusals`（> 5 MiB 413、像素過大 422）、UI 的本地檢查 |
| 逾時與取消 | `TestPhotoFailures`（segmenter 逾時 `AI_TIMEOUT` 可重試；取消前、segmenter 工作中取消：context 的錯誤，不是 failure，vision 不被呼叫）；取消後的結算由 T-055 的 `SegmentClient` 測試 |
| 沒有步驟的回答 → needs_input | `TestNoBoxAsksForInput`（en、zh-Hant；vision 不被呼叫）、`TestPhotoOutcomes/no_box`、`/the_vision_model_asks` |

DB-backed（`dt_test_*`）：`db/tests/proposal_images.sql`（grant、RLS、跨 tenant、deferred 檢查、形狀、不可改、worker、建立者）、`internal/storage/proposals` `TestAnImageIsReadWithItsProposal`、`internal/api` `TestAPhotoProposalEndToEnd`（上傳帶 tEXt 的 PNG → 存的是重新編碼的影像 → ready → 批准 201 → version 2 有工作站、沒有新位置 → audit）、`TestPhotoRefusals`、`TestPhotoStaleBaseAndAvailability`、`TestPhotoIdempotency`、`TestPhotoOutcomes`（8 個子測試）。

Web（vitest）：`tests/photo-api.test.ts`（operation inventory 的 method、path、roles；multipart 的順序、固定檔名、沒有自設 Content-Type；送出前拒絕；拒絕的對應；上限等於 schema）、`tests/proposal-photo.test.tsx`（ask 區塊內一個按鈕與選檔、沒有自己的區塊、axe、送出與跟隨、zh-Hant、同檔同 key、本地 5 MiB、伺服器 415、blocked、viewer／未知角色／其他 domain、完整迴圈與批准）、`tests/proposal-simplicity.test.tsx`（電子產線模型頁加上照片：一個主要動作、沒有重複、blocked 有理由）、`tests/contracts.test.ts`（`source_image` 型別與 3 個反向）、`proposal-preview.test.tsx`（新範例的 patch hash 與 schema）。

`/ui-gallery/proposal?state=photo` 的伺服器部分（ready、medium、patch hash）是 `review.Review` + `review.Photo` 實際算的（`checks/evidence/2026-10-26-T-056/sample-server-review.log`，暫時測試保留為 `scripts/*.txt`）。

紅燈：測試先寫（commit `130f3c2`），紅燈 log 在 `checks/evidence/2026-10-26-T-056/red-*.log`（之後採用 T-055 的名稱時測試隨之改寫，改寫後的 Go 測試與實作同一 commit `5b981b4`）。UI 的測試與實作同一 commit，紅燈改以 mutation 證明（§6 M15–M19）。

## 6. Mutation

`checks/evidence/2026-10-26-T-056/scripts/mutants.py`，結果 `mutants.txt`、`mutants-final.txt`：

| Mutant | 結果 |
|---|---|
| M1 不套用照片規則 | KILLED |
| M2 沒有物件仍呼叫 vision | KILLED |
| M3 物件數不設上限 | KILLED |
| M4 轉向的 frame 被接受 | KILLED |
| M5 不先問 judge 是否收影像 | KILLED |
| M6 讀回的影像不與 record 比對 | 第一次 SURVIVED → 發現 `provider.NewImage` 之後又重複比對一次 digest；刪掉重複的比對，`photo.Verify` 成為唯一的檢查 → KILLED（`mutants-M6-rerun.txt`） |
| M7 原檔照存（metadata 保留） | KILLED |
| M8 SEGMENTS 變成 server_data | KILLED |
| M9 capacity 任意數字 | KILLED |
| M10 步驟多於物件 | KILLED |
| M11 fake 不以影像對照 | KILLED |
| M12 每個 client 都說收影像 | KILLED |
| M13 file 之後還有 part 也接受（DB） | KILLED |
| M14 0017 的 intent／影像檢查拿掉（DB，`make db-test`） | KILLED |
| M15 client 送出使用者的檔名 | KILLED |
| M16 blocked 時仍開啟選檔 | KILLED（照片按鈕移入 ask 區塊後 M15–M19 重跑仍 KILLED：`mutants-ui-inline.txt`） |
| M17 重試換新的 key | KILLED |
| M18 AI 的 prose 一律英文 | KILLED |
| M19 提案頁不標明照片 | KILLED |

## 7. 指令與 exit code

（log 都在 `checks/evidence/2026-10-26-T-056/`，每個 log 最後一行是 exit code。）

| 指令 | exit | log | 結果 |
|---|---|---|---|
| `make spec-check PYTHON=/home/user/Geminus/.venv/bin/python` | 0 | `spec-check.log` | contracts（43 個 operation）、planning、docs、SQL inventory、reference math、decisions、TS contracts、hashes、DB tests 是最新的、deploy 都 PASS；不含官方完整 OpenAPI validator（NOT_RUN_UNAVAILABLE） |
| `make backend-test GOFLAGS=-count=1` | 0 | `backend-test.log` | gofmt、`go vet`、所有 package（`GEMINUS_FULL_DIFFERENTIAL=1`）；DB-backed 測試在沒有 `GEMINUS_TEST_DATABASE_URL` 時 skip，由 integration-test 執行 |
| `make architecture-check` | 0 | `architecture-check-t055b.log` | 93 個 package、25 條規則、16 種 build configuration（第二次合併 T-055 之後的樹） |
| `make ai-eval` | 0 | `ai-eval.log` | 既有 fake corpus 的 context gate 與 server review；照片沒有 eval case（§8） |
| `go test -race -count=1`（改動的 18 個 package：proposals/...、ai/aicontext、ai/provider/...、ai/review、ai/spend、audit、domainpack/electronicsline、observability、cmd/worker、scripts/gen-photo-demo、prompts） | 0 | `race-changed.log` | 全部 ok |
| `make integration-test`（新的 `dt_test_t056c`，結束後 DROP） | 0 | `integration-test.log` | `make db-test`（0001–0017 up／down／up 與全部 SQL 測試，含 `proposal_images.sql`）＋ `make db-go-test`（`-race -p 1`，38 個 package 含 `internal/api` 的 5 個照片測試） |
| `make frontend-test` | 0 → 2 → 0 | `frontend-test.log`、`frontend-test-2.log`、`frontend-test-3.log` | 第一次 0（照片按鈕仍是獨立區塊時）；把按鈕移入 ask 區塊後第二次 exit 2：`tests/theme.test.tsx` 的 ThemeSwitcher 測試 5 秒逾時（與照片無關；當時機器 load average 約 21，單獨重跑 8/8 通過）；第三次 0：45 個檔案、990 個測試全部通過。沒有改 assertion 或加 skip |
| `make frontend-build` | 0 | `frontend-build.log`、`frontend-build-2.log` | Next.js production build（移入前、後各一次） |
| `npm audit --prefix apps/web --omit=dev` | 0 | `npm-audit.log` | found 0 vulnerabilities |
| `make e2e-test`（3100／3101 空閒、沒有其他 playwright 時才執行；另一個 e2e 進行時等候，沒有中止任何程式） | 2 → 0 | `e2e-test.log`、`e2e-test-2.log` | 第一次：503 passed、1 failed（`editor.spec.ts` 滑鼠拖曳：獨立的照片區塊把畫布推出視窗，見 §1 第 4 點）、32 skipped；修正後第二次：504 passed、32 skipped（screenshot 比對 NOT RUN）。兩次都有既有的 `[WebServer] failed to pipe response` 訊息（session 測試，與照片無關） |
| `make db-test`（開發中，`dt_test_t056a`／`t056b`） | 0 | `db-test-first.log`、`db-test-t055.log` | 0017 的 up／down／up 與 SQL 測試 |
| DB-backed 照片測試（`internal/storage/proposals`、`internal/api`） | 0 | `db-storage-proposals.log`、`db-api-photo.log`、`db-go-photo-t055.log` | 採用 T-055 名稱前、後 |
| mutation（`scripts/mutants.py`） | 1 → 0 → 0 → 0 | `mutants.txt`、`mutants-M6-rerun.txt`、`mutants-final.txt`、`mutants-ui-inline.txt` | 第一次 18/19（M6 survived，見 §6）；修正後全部 19/19 KILLED（DB mutant 用 `dt_test_t056m`、`dt_test_t056c`，結束後 DROP） |
| `go run ./scripts/gen-photo-demo -check`（經由 `TestTheCommittedFilesAreReproducible`） | 0 | `backend-test.log` | committed 的 demo 影像與 segments 可由腳本重現 |

## 8. NOT RUN

- **所有 live 步驟**：SAM 3.1 與 Muse Spark 的 live 呼叫（等 T-055 的獨立審查，H-033）；Claude 影像備援（adapter 沒有接，§4）；`make ai-eval-live`、`make ai-smoke-live`；以真實照片的辨識品質。
- 照片管線的 AI eval：`make ai-eval` 只跑既有的 fake corpus（context gate 與 server review），**沒有照片的 eval case**；照片的負向案例在 Go 單元與 DB 測試中，不在 corpus。
- `make backend-race` 全部（只跑了改動的 package，見 §7）。
- e2e 的 screenshot 比對（只在 CI 的 pinned container，`E2E_SCREENSHOTS=1`）；e2e 的產品 closed loop（T-040）。
- S3 adapter 上的照片（只用 filesystem adapter）；孤兒 object 的清理（transaction 失敗時 object 留下，與 T-016 相同，maintenance 尚未實作）。
- EXIF 方向的套用（刻意不做，見問題 4）。
- 遮罩（SAM 的 mask）：T-055 的介面只回框。

## 9. Owner／coordinator 的問題

1. **合併順序**：本分支已合併 t055-meta 到 `339fea5`。T-055 之後若再改 `provider.Image`、`Segmenter`、`deps.photo` 的名稱，T-056 需要再合併一次；建議 T-055 先進主線、T-056 之後。
2. **D-130–D-132**（PROPOSED）：上傳上限 5 MiB／32–4096 px、只留像素、0017 的 record；管線的順序、8 個概念、64 個物件、needs_input；照片規則（數字只能沿用 base）。是否照建議？
3. **Claude 影像備援**：要接嗎？需要改 T-053 已審查的 adapter（`Complete`、`Quote`、`AcceptsImages`）並修改 H-018 的資料政策（目前沒有影像）。或只用 Muse Spark？
4. **EXIF 方向**：重新編碼不套用 EXIF 的 orientation（像素依檔案儲存的順序），手機直拍的照片可能送出時是橫的。要在去掉 metadata 前套用方向嗎？（多一段旋轉的程式與測試）
5. **示範影像的 approval record**：H-029／H-033 說只送 demo 圖片；server 無法分辨 demo 與客戶的照片，只在 UI 寫明「只使用示範影像」。是否需要更強的保證（例如只接受 server 已知的 demo 影像 digest，或 live 照片路徑限定某些 organization）？
6. **API 端的照片可用性**：API 依文字 proposal 的設定（`AI_PROVIDER`、開關）判斷是否接受照片；T-055 的 `AI_VISION_PROVIDER` 可讓照片路徑另外關閉（例如 Claude 為主而沒有影像核准），此時 API 仍接受上傳，job 才以 `AI_IMAGE_UNSUPPORTED` 失敗。要讓 API 也讀照片路徑的設定（不讀 key）嗎？
7. **Segmenter 花費與 judge 被拒**：segmenter 先執行並花費（8 個概念 × 每張 USD 0.0025 = USD 0.02），之後 judge 的預留若被上限拒絕，segmenter 的花費已發生、proposal 失敗。要先替兩者一起預留，或接受這個小額損失？
8. **底稿模型**：demo 用的「bare line」（`testdata/photos/bare-line.json`：投入、SMT 前的緩衝、SMT、出貨）是測試用的 synthetic 模型。產品上從照片開始建模時，要以哪個範本為 base（例如 `electronics_assembly_test_line` 的精簡版）？
9. **概念清單**：8 個概念（`smt line`、`aoi machine`、`ict tester`、`fct tester`、`burn-in chamber`、`packing station`、`conveyor belt`、`workstation`）的措辭由 agent 決定，沒有在真實 SAM 3.1 上試過；live 時可能要調整。
10. **編號**：D-130 起是為了避開 T-055 進行中的 D-118–D-123，合併時可重新編號。

## 10. Commits（`t056-photo`）

| Commit | 內容 |
|---|---|
| `3c0038f` | contract first：`createImageProposal`、`source_image`、fixture、TS 型別、contracts-check |
| `130f3c2` | tests first（紅燈）：照片管線、segmenter／vision seam、0017 的 SQL 測試、demo fixture 與產生腳本 |
| `5a668d4` | 實作：上傳與重新編碼、0017、segmenter、vision proposer、server 審查、worker |
| `c57cdbd` | 合併 t055-meta `19fb02a`（衝突的檔案取 T-055 的版本；這個 merge commit 單獨無法 build T-056 的 package，下一個 commit 修正） |
| `5b981b4` | 改用 T-055 的名稱：`provider.Image`、`Prompt.Images`、`provider.Segmenter`；刪掉 `internal/ai/vision`；上限 5 MiB |
| `6e45f32` | UI、gallery 範例、文件、D-130–D-132 PROPOSED |
| `b023026` | mutation 檢查與 M6 的修正 |
| `268dbd7` | 合併 t055-meta `339fea5`：generator 改用 T-055 的 judge（`deps.photo`）；文件衝突兩邊都保留 |
| 最後一個 commit | 照片按鈕移入 ask 區塊（e2e 修正）、本報告、證據、`planning/tasks.json` 的 completion_evidence |

## 11. 證據

`checks/evidence/2026-10-26-T-056/`：`run.sh`（以 Go 1.27.3 執行並記錄 exit code，`-db` 只接受 `dt_test_*`）、`red-*.log`（測試先寫）、`green-*.log`（實作後，採用 T-055 前）、`green-after-t055.log`、`db-*.log`、`architecture-check-*.log`、`sample-server-review.log`、`mutants*.txt`、`scripts/`（mutation 腳本、暫時測試的副本）與 §7 的各 log。使用的資料庫：`dt_test_t056a`、`dt_test_t056b`、`dt_test_t056c`、`dt_test_t056m`，都由本任務建立，結束時都已 DROP。

## Coordinator 驗證（2026-10-26）

`t056-photo`（到 b1f67ef）以 `git merge --no-ff` 合併到已整合 T-055 的主線（merge commit 28f6941）：這個分支先前合併過 t055-meta，cherry-pick 會重複 T-055 的 commit。衝突處理：
- `internal/audit/audit.go`：保留 T-052 的 `"warnings": {shape: count}`，以及 T-056 的 `fieldsOf(providerCall, photoCall, …)`。
- `internal/domainpack/domainpack.go`：兩組介面都保留（T-052 的 `EventRulesProvider`／`EventIssue`、T-056 的 `PhotoVocabulary`）。
- `docs/domains/ELECTRONICS_LINE.md`：T-052 為 §12、T-056 的照片概念改為 §13；AI_SPEC 與 D-131 的引用改為 §13。
- `docs/DECISIONS_AND_TBD.md`、`planning/decisions.json`：分支帶來的重複 D-121–D-123 刪除，保留主線（T-055 review 前）的版本。

指令與結果（evidence：`checks/evidence/2026-10-26-T-056-coordinator/`，`exit-codes.txt` 依執行順序；go1.27.3、Node 22，`--engine-strict=false`）：

| 指令 | exit | 說明 |
|---|---|---|
| `make spec-check` | 0 | |
| `make backend-test GOFLAGS=-count=1` | 2 | 82 個 package ok；`scripts/check-architecture` 在 load 約 10–18 時超過 go test 的 10 分鐘預設（`panic: test timed out after 10m0s`） |
| 同一個測試單獨重跑，`-timeout 30m`（container 重啟後，load 約 2–3） | 0 | 296 s；`check-architecture-rerun.log` |
| `make architecture-check` | 0 | 95 packages、25 rules、16 build configurations |
| `make ai-eval` | 0 | fake provider |
| `go run ./scripts/gen-electronics-line-data -check` | 0 | |
| `go test -race -count=1 -timeout 30m`（ai、aievals、config、domainpack、results、cmd、generator、gen-photo-demo、proposals、storage/proposals、observability、prompts、audit） | 1 | 36 個 package ok；`internal/ai/provider/meta` 的 `TestRetriesBackOff` 失敗（`an HTTP date: waited [1.77961857s], want 2s`）：T-055 review 第 1 輪 L1 指出的問題，測試以真實時鐘產生秒精度的 HTTP date，等待時間會少於 2 s。不是 T-056 造成的；由 T-055 的修正固定時鐘，修好前不算通過 |
| `make integration-test`（新的 dt_test_hkb5c，跑完刪除） | 0 | 38 個 package ok，沒有 FAIL 或 SKIP |
| `npm ci`（apps/web、apps/web-e2e） | 0、0 | |
| `make frontend-test` | 0 | 45 個檔案、1005 個測試 |
| `make frontend-build` | 0 | |
| `npm audit --prefix apps/web --omit=dev` | 0 | 0 vulnerabilities |
| `make e2e-test`（等 3100–3101 沒有其他執行時才開始） | 0 | 504 passed、32 skipped（只在 CI 比對的 screenshot） |

NOT RUN：任何 live 呼叫（SAM 3.1、Muse Spark）；完整的 `make backend-race`；CI 的 screenshot 比對（push 後由 CI 執行）；獨立審查（H-029 的一輪，下一步）。

Coordinator 對 §9 問題的處理：
1. 合併順序：T-055 先進主線（85a7750），T-056 之後以 merge 合併（28f6941）。
2. D-130–D-132 維持 PROPOSED，比賽後放進決策頁（H-029 (4)）；照建議的做法使用，不視為已批准。
3. Claude 影像備援：owner 決定先不要（H-035 (3)），照片只走 Meta。
4. EXIF 方向：demo 影像由 `scripts/gen-photo-demo` 產生，沒有 orientation；先不做，比賽後再處理。
5. Demo 影像的保證：owner 選擇以 UI 的資料提醒為準（H-035 (1)），不做 digest 白名單。
6. API 端的照片可用性：coordinator 決定 API 也讀照片路徑的非 secret 設定（`AI_VISION_*`，不讀 key），照片路徑關閉時直接 503，不讓 job 才失敗；與 key-free availability 的後續一起實作並驗證。
7. Segmenter 先花費：照片 proposal 的預留改為 count_tokens 加 10%（H-037）後，judge 被上限拒絕的情況大幅減少；接受 segmenter 最多約 USD 0.02 的損失，比賽後再考慮一起預留。
8. 底稿模型：demo 使用 bare line；產品要用哪個範本比賽後決定。
9. 概念清單：live 時依 SAM 3.1 的實際結果調整，調整要記錄。
10. 編號：D-130–D-132 不重新編號（合併後沒有衝突）。

另：T-055 review 第 1 輪 M1（照片的預留約 USD 4.04、organization 日上限 USD 5）由 owner 選擇以 count_tokens 處理（H-037）；部署變數由 owner 填完即 sealed（H-036）。

下一步：T-055 的修正（H-037、L1–L5）與 API 的照片可用性；之後 T-056 的一輪獨立審查，範圍包含 count_tokens 的預留。

## API 端的照片可用性（T-056 的 follow-up，2026-10-26）

Branch `t056-apivision`（自 1425e3a，本機，**沒有 push**）。依 §9 問題 6 與 coordinator 的 answer 6（**coordinator 的決定**，不是 owner 的）；D-124 維持 **PROPOSED**。只用 fake、本機測試與一次性 database；**沒有任何 live AI 呼叫**，沒有讀取、印出或 grep 任何 key 的值（只有變數名稱），Railway 沒有任何操作。證據在 `checks/evidence/2026-10-26-T-056-apivision/`（`run.sh` 執行並在每個 log 最後一行記 exit code）。

### 設計

- **一段判斷**：`internal/config` 的 `decide`（由 `LoadAI` 拆出，內容不變）決定文字 proposal（開關、`AI_POLICY_APPROVAL`、`AI_MODEL`）與照片路徑（`AI_VISION_PROVIDER`，不設＝跟 `AI_PROVIDER`；`AI_VISION_POLICY_APPROVAL`、`AI_VISION_MODEL`）。每個 live provider 的 gate 以參數傳入：worker 的 `LoadAI` 用 `loadLive`（gate 通過後才讀 key），API 的 `LoadAPIAI` 用 `keyFree`（`liveGate` 加 build 記錄的批准，從不讀 key）；`loadLive` 即 `keyFree` 再讀 key。四個 `LiveEnabled`（`AI`、`AIVision`、`APIAI`、`APIVision`）共用 `liveFor`。`APIAI.Vision`（新型別 `APIVision`）是沒有 credential 的 `AIVision`。所以沒有第二份規則。
- **`cmd/api`**：`photoAvailability` 與文字的 `aiAvailability` 用同一個對應 `availability`（fake 或已開的 live provider → `Available`；未開的 live provider → `PolicyNotApproved`；其他 → `NotConfigured`）；`handlerOptions` 把 `WithAI` 與新的 `WithPhotoAI` 交給 handler；照片路徑為 live provider 時啟動 log 寫 `photo proposals are accepted; the worker calls the provider`（`code` `PHOTO_PATH_ON`）或 `photo proposals are refused`（typed reason，例如 `IMAGES_NOT_APPROVED`），與 worker 的 `the photo path is on/off` 對應。
- **`internal/proposals`**：`Service.Photo`（zero 值拒絕照片，與文字的設定無關）取代 `BeginImage` 與 `CreateFromImage` 中的 `Service.AI`；照片路徑關閉時 503，`error.code` 依照片路徑的可用性（`AI_PROVIDER_NOT_CONFIGURED`，或 live provider 未開時 `AI_POLICY_NOT_APPROVED`），`details[0]` 為 `{path: "", code: "AI_IMAGE_UNSUPPORTED"}`，即 worker 會用的失敗碼（`generator.ImageUnsupported`；`TestTheAPIRefusesAPhotoWithTheWorkersCode` 守住兩者相同）。在讀 body 前拒絕，不存影像、不建 proposal、不寫 audit。
- **Contract 先改**（commit 9647e5a）：`openapi.yaml` 的 createImageProposal 描述與自己的 503（三個範例：照片路徑未設定、未批准、沒有 object storage）。錯誤碼目錄（D-025）沒有新碼，所以 schema、TS 型別與 client 不變（client 本來就保留 `details`：`apps/web/tests/api.test.ts` 的 storage_failed 測試）。
- **不變的**：文字 proposal（`createProposal`）；API 的環境中有任何 AI key 變數仍拒絕啟動（`key_forbidden`）；worker 的行為。**新的拒絕啟動**：照片路徑的設定不合時（例如未知的 `AI_VISION_PROVIDER`、production 的 `fake`、跟隨時設了 `AI_VISION_MODEL`、開關 on 而另一個 provider 沒有批准），API 與 worker 以相同的錯誤碼拒絕啟動（之前 API 不讀這些設定而照常啟動）。
- **例子**：Claude（H-018）為主、沒有另設照片路徑 → 文字接受，照片 503 `AI_POLICY_NOT_APPROVED`／`AI_IMAGE_UNSUPPORTED`（之前接受上傳、job 才以 `AI_IMAGE_UNSUPPORTED` 失敗）；Claude 為主、`AI_VISION_PROVIDER=meta`＋H-029＋`muse-spark-1.3`、開關 on → 兩者都接受；Meta（H-029）為主 → 兩者都接受；`AI_PROVIDER=disabled`、`AI_VISION_PROVIDER=fake`（只在 development／test）→ 照片接受、文字 503（worker 的照片管線本來就不經文字 provider）。
- **部署**（commit 4a77901）：`deploy/railway/variables.json` 的 api 與 worker 都宣告三個設定，宣告相同（`{"secret": false, "source": "owner", "required": false}`；worker 之前也沒有宣告）；`scripts/check_deploy.py` 的 `API_AI_SETTINGS` 為七個（兩邊宣告必須相同、不是 secret、API 沒有其他 AI 設定）；`checks/deploy-check.json` 重新產生；`scripts/rehearse_railway_deploy.py` 也檢查 deployer 從不設這三個；`deploy.sh` 開頭註解、runbook §4.4／§7.3。文件：SECURITY §7、ADR-027 addendum、D-124（與 `planning/decisions.json`）。

### 測試（先紅後綠）

| 範圍 | 測試 | 紅 → 綠 |
|---|---|---|
| `internal/config` | `TestTheAPIsPhotoDecisionIsTheWorkers`：8 種文字設定 × 開關 3 × `APP_ENV` 2 × `AI_VISION_PROVIDER` 6 × `AI_VISION_POLICY_APPROVAL` 4 × `AI_VISION_MODEL` 4 = **4,608 種組合**，API（沒有 key）與 worker（三把 key 都在）的錯誤碼、照片路徑（provider、on、typed reason、批准、model、segmentation model）與文字的判斷都相同；API 只讀它的設定與 key 變數的有無；每種結果都出現（拒絕啟動 4,014、fake 20、disabled 42、同 provider on 4、另一 provider on 2、`images_not_approved` 28、`switch_off` 34、`no_approval_named` 168、`model_mismatch` 128、`provider_mismatch` 168）。`TestAPIAIReadsThePhotoSettings`（15 種設定的預期結果；16 種 API 與 worker 都拒絕啟動的設定與錯誤碼）、`TestAPIVisionLiveEnabled`（7 種手造值）、`TestAPIAIIsKeyFree`（改：見下）、`TestAPIAIRefusesAnyKey`（多 5 種照片設定 × 8 個 key 變數） | `red-config.log`（以保留舊行為的暫時 stub：`APIVision` 型別存在但 `LoadAPIAI` 不填；stub 沒有 commit）→ `green-new-tests-v.log` |
| `cmd/api` | `TestAPIPhotoAvailability`（15 種設定的文字與照片可用性；6 種手造值）、`TestAPIAvailabilityNeedsNoKey`（多照片的 log）、`TestAPIAvailabilityRefusesAMismatch`（多 7 種照片設定，exit 1、typed code、不碰 database）、`TestAPIRefusesToStartWithAKey`（多 2 種照片設定）；DB：`TestTheServedHandlerDecidesPhotosOnTheirOwn`（經 `handlerOptions` 的 handler，6 種設定：照片 503＋`AI_IMAGE_UNSUPPORTED` 恰好在照片路徑關閉時，文字另外判斷，什麼都不存） | `red-api.log`（stub：照片跟文字的可用性）→ 綠；`RefusesToStartWithAKey` 在 stub 也成立（regression） |
| `cmd/worker` | `TestTheAPIsPhotoDecisionIsThePhotoPaths`：6 種文字 × 7 種照片 × 開關 3 × `APP_ENV` 2 = 252 種組合，API 接受照片恰好等於 worker 由同樣設定與 key 建出的 judge 收影像（`photoPath`，只建不呼叫），或兩者都拒絕啟動 | `red-worker.log` → 綠 |
| `internal/proposals`、`generator` | `TestRefusedPhotoPathOff`（兩種可用性的 503、`details[0]`；body 與 openapi.yaml 的範例相同；文字的 503 不變、沒有 details；`Service{AI: Available}` 不收照片）、`TestTheAPIRefusesAPhotoWithTheWorkersCode` | `red-proposals.log` → 綠 |
| `internal/api`（DB） | `TestPhotoStaleBaseAndAvailability`：7 種（文字與照片都關、都未批准；文字開而照片關／未批准／沒說；文字關而照片開；沒有 storage），照片的 code 與 `details[0].code`、存了幾個 proposal 與影像、同一部署的文字 proposal | 紅：見 Mutation 的 W1–W7（舊行為即 W1）；綠：`db-photo-green.log` |
| 部署 | `check_deploy_negatives.py`（18 個：照片設定缺一邊、兩邊都缺、deployer 設值、`required` 不同、宣告不同、secret、`fake`、限額與 budget 在 API、以及既有的 key 與四個設定的 regression；`later` 不同不算不同） | `red-deploy.log`：新的檢查對 ea7d220 的 `variables.json` 失敗（`AI_VISION_PROVIDER must be declared on the API and on the worker`），舊的檢查對新的 `variables.json` 失敗（`the API reads no other AI setting: [AI_VISION_*]`）→ `check-deploy-negatives.log` 18/18 |

**`TestAPIAIIsKeyFree` 的改動（不是放寬）**：原本它斷言 API 不讀 `AI_VISION_*`，即使值不合法（`openai`、跟隨時的 model）也照常啟動；這正是 coordinator 的決定要改的行為。現在它斷言照片路徑的判斷（Meta：on；Claude：`images_not_approved`）與 API 不讀限額與 budget；不合法的照片設定改由 `TestAPIAIReadsThePhotoSettings` 斷言為拒絕啟動（較嚴）。其他既有的 assertion 沒有改、沒有加 skip。`setupPhotos` 與 `TestPhotoStaleBaseAndAvailability` 既有的兩種情況多給 `WithPhotoAI`（照片路徑 zero 值即拒絕），預期結果不變。

### Mutation（`mutate.py`，5247cc4 的 `git archive` 複本，在 scratch 中）

37 個 mutant；build 失敗另列 INVALID，不算 killed（這次沒有）。

- **不需 database 的 30 個**（`mutation.log`，每個跑 `./internal/config/...`、`./cmd/api/`、`./cmd/worker/`、`./internal/proposals/`、`./internal/proposals/generator/` 中相關的部分）：**29 killed、1 survived（等價）**。範圍：`LoadAPIAI` 保留的照片路徑（V1–V8：不保留、丟掉批准、reason 用文字的、provider 用文字的、丟掉 segmentation model、API 用讀 key 的 gate、丟掉錯誤、fakeOK 一律 true）、`liveFor` 與 `APIVision.LiveEnabled`（L1–L5）、共用的 `decide`／`keyFree`（D1–D5：兩邊同時改變，由明確的預期測試殺掉）、`cmd/api` 的對應與 log（P1–P7）、拒絕的 body（R1–R5）。存活的 **L5**（`liveFor` 拿掉 `a.Valid()`）是等價的：`Approval.Provider()` 對無效的 token 回空字串，而 live provider 的名稱不是空字串（T-055 §10.5 的 L3 同理）。
- **需要 database 的 7 個**（`mutation-db.log`，`dt_test_t056av1`，`GEMINUS_REQUIRE_DB_TESTS=1`，只跑 `TestPhotoStaleBaseAndAvailability`、`TestTheServedHandlerDecidesPhotosOnTheirOwn`、`TestAPhotoProposalEndToEnd`）：**7 killed**。W1（`BeginImage` 問文字的可用性，即改動前的行為）、W2（拒絕沒有標為照片）、W3（`CreateFromImage` 問文字的可用性）、W4（handler 把文字的可用性給照片）、W5（`WithPhotoAI` 無效）、W6、W7（`handlerOptions` 給照片文字的可用性、沒有 `WithPhotoAI`）。
- 第一次執行（21:07）與另一個 agent 共用了 scratchpad 的 `mut/` 目錄，複本不是本分支的程式（25 個 NOT APPLIED、5 個 INVALID），結果作廢；改在自己的目錄重跑，上面的數字是重跑的結果（`mutation.log` 已覆寫）。已通知 coordinator 另一個 agent 的 mutation 結果可能受影響。

### 指令與 exit code（Go 1.27.3、`GOTOOLCHAIN=local`；Python `.venv/bin/python -I`；deploy 腳本以 `env -u RAILWAY_TOKEN -u RAILWAY_API_TOKEN` 執行）

DB 測試都在 coordinator 建立的**一次性 cluster**（PostgreSQL 16.15，socket `/var/lib/postgresql/geminus-throwaway-hkb6`，port 55450）上自己建立的 `dt_test_*` database 執行：共用的 cluster（55440）在 container 重啟後沒有執行，依指示沒有啟動它；也沒有啟動或停止一次性的 cluster。

| 指令 | exit | Log |
|---|---|---|
| `gofmt -l cmd internal schemas scripts prompts` | 0（空） | `gofmt.log` |
| `go vet ./internal/config/... ./cmd/... ./internal/proposals/... ./internal/api/` | 0 | `vet.log` |
| 紅：`go test -count=1`（stub）`./internal/config/`、`./cmd/api/`、`./cmd/worker/`、`./internal/proposals/ ./internal/proposals/generator/` | 1、1、1、1 | `red-config.log`（4,418 行，中間省略）、`red-api.log`、`red-worker.log`、`red-proposals.log` |
| `go test -count=1 ./internal/config/... ./cmd/api/ ./cmd/worker/ ./internal/proposals/... ./internal/api/` | 0（9 個 package；DB 測試在此 skip） | `tests.log`、`green-new-tests-v.log` |
| `go test -race -count=1 -timeout 30m`（同上） | 0 | `race.log` |
| `make db-test`（`dt_test_t056av1`） | 0 | `db-test-av1.log` |
| `go test -race -p 1 -v -run '…Photo…|TestProposalsNeedAI|TestRunServesAndStops|TestTheServedHandlerDecidesPhotosOnTheirOwn' ./internal/api/ ./cmd/api/`（`dt_test_t056av1`） | 0（8 個測試 PASS，沒有 SKIP） | `db-photo-green.log` |
| `make backend-test GOFLAGS=-count=1` | 0（gofmt、vet、83 個 package ok，0 FAIL，沒有 cached；`scripts/check-architecture` 的測試在預設 10 分鐘內通過） | `backend-test.log` |
| `make architecture-check` | 0（95 packages、25 rules、16 build configurations） | `architecture-check.log` |
| `make spec-check PYTHON=/home/user/Geminus/.venv/bin/python` | 0、0（本節寫完後再跑一次；`checks/*.json` 沒有再改變；不含官方完整 OpenAPI validator：NOT_RUN_UNAVAILABLE） | `spec-check.log`、`spec-check-final.log` |
| `scripts/check_deploy.py` | 0 | `check-deploy.log` |
| `check_deploy_negatives.py`、`red_deploy.py` | 0（18/18）、0（2/2 紅） | `check-deploy-negatives.log`、`red-deploy.log` |
| `scripts/rehearse_railway_deploy.py` | 0，`rehearsal: PASS`（Railway 本身 NOT RUN） | `rehearsal.log` |
| `bash deploy/railway/deploy.sh --dry-run` | 0（沒有呼叫任何遠端；三個照片設定是 owner 的，計畫中不設） | `deploy-dry-run.log` |
| mutation（不需 / 需要 database） | 0、0（29/30 killed＋1 等價；7/7 killed） | `mutation.log`、`mutation-db.log` |
| `make integration-test`（新建的 `dt_test_t056av2`） | **0**（`make db-test` 的 migration up／down／up 與全部 SQL 測試，加上 `make db-go-test`：38 個 package ok，`-race -p 1`，`GEMINUS_REQUIRE_DB_TESTS=1`，沒有 FAIL 或 SKIP；含 `internal/api` 的照片測試與 `cmd/api` 的 `TestTheServedHandlerDecidesPhotosOnTheirOwn`） | `integration-test.log` |

兩個 `dt_test_t056av*` database 在最後都已 DROP。

### NOT RUN

- 任何 live AI 呼叫；`make ai-eval-live`、`make ai-smoke-live`；Railway 上的任何操作（部署後 API 與 worker 的照片設定值是否相同，靜態檢查與 rehearsal 看不到值）。
- `make e2e-test` 與 frontend（`make frontend-test`、build）：依指示不執行；前端沒有改（錯誤碼目錄不變，client 照舊保留 `details`）。
- `make backend-race`（整個 repository 的 `-race`）：只跑了上面範圍的 `-race`。
- 照片路徑的 **record 變動**（`aiapproval.RecordForTest`）× 設定的 API-vs-worker 比較：`internal/config/aiapproval` 依指示不動，所以 4,608 種組合只用這個 build 的兩個 record（H-018、H-029）；record 的變動只在既有的 272 種比較（文字的 gate）中。
- 獨立審查：尚未進行。

### 問題

1. **錯誤碼**：照片路徑關閉時用既有的 `AI_PROVIDER_NOT_CONFIGURED`／`AI_POLICY_NOT_APPROVED`，加上 `details[0].code` `AI_IMAGE_UNSUPPORTED`，沒有在 D-025 的目錄加新碼（加新碼要改 schema、TS 型別與前端的對應）。這樣可以嗎？
2. **UI**：前端沒有改；照片按鈕在照片路徑關閉時仍出現，按下後顯示一般的 503 訊息，`details` 的 reason 是英文（`ProblemView` 對不認得的 code 顯示原文）。要加 `AI_IMAGE_UNSUPPORTED` 的中英文訊息，或讓 UI 事先知道照片路徑是否開啟（目前沒有 endpoint 說這件事）嗎？
3. **Claude 的照片路徑**：若之後的 record 讓 H-018 列有圖片，config 會把 Claude 的照片路徑打開，API 接受照片，但 worker 因為 Claude 的 adapter 不收影像而拒絕啟動（`photoBackends`），照片 proposal 停在 generating（與沒有 key 的情況相同）。要讓 config 也知道「哪個 provider 的 adapter 收影像」，在 API 端就拒絕嗎？
4. **record 變動的比較**：要在 `aiapproval` 的 `TestTheAPIsGateIsTheWorkers` 加上照片路徑（另一個 agent 改完 `aiapproval` 之後）嗎？
5. **舊的證據**：`checks/evidence/2026-10-26-T-055-apiavail/check_deploy_negatives.py` 的「the photo path's provider on the API」一例預期的訊息已不同（現在 API 有宣告這個設定）；那是 T-055 的歷史證據，沒有修改也沒有重跑。

### Commits（`t056-apivision`）

| Commit | 內容 |
|---|---|
| 9647e5a | contract first：createImageProposal 的 503 與範例 |
| ea7d220 | `config.decide`／`keyFree`／`liveFor`、`APIVision`、`photoAvailability`、`WithPhotoAI`、`Service.Photo`、照片的 503；全部測試（先寫，紅燈以未 commit 的 stub） |
| 4a77901 | `variables.json`、`check_deploy.py`、rehearsal、`deploy.sh` 註解、`deploy-check.json`、runbook §4.4／§7.3 |
| 5247cc4 | `cmd/api` 的 `handlerOptions` 與經 handler 的 DB 測試（為了殺掉 wiring 的 mutant） |
| （本節的 commit） | 本節、D-124、`planning/decisions.json`、SECURITY §7、ADR-027 addendum、證據 |

## Coordinator 驗證：API 端的照片可用性（2026-10-26）

`t056-apivision` 的五個 commit（9647e5a..1b9d125）cherry-pick 到已整合 T-055 review 修正的主線，沒有衝突。

指令與結果（evidence：`checks/evidence/2026-10-26-T-056apivision-coordinator/`，`exit-codes.txt` 依執行順序；HEAD e744429；go1.27.3、Node 22；Railway 的指令以 `env -u RAILWAY_TOKEN -u RAILWAY_API_TOKEN` 執行；DB 測試在暫時的 cluster（`/var/lib/postgresql/geminus-throwaway-hkb6`，port 55450），共用的 cluster 在 container 重啟後沒有在跑，依規則不啟動）：

| 指令 | exit | 說明 |
|---|---|---|
| `make spec-check` | 0 | 含 OpenAPI 的新 503 範例 |
| `scripts/check_deploy.py`、rehearsal、`deploy.sh --dry-run` | 0、0、0 | API 與 worker 宣告相同的七個 AI 設定 |
| `make backend-test "GOFLAGS=-count=1 -timeout=30m"` | 0 | 83 個 package ok |
| `make architecture-check` | 0 | 95 packages、26 rules |
| `go test -race -count=1 -timeout 30m`（config、cmd、proposals、internal/api） | 0 | 12 個 package ok |
| `make integration-test`（新的 dt_test_hkb8c，跑完刪除） | 0 | 38 個 package ok，沒有 FAIL 或 SKIP |
| `make frontend-test` | 0 | 47 個檔案、1037 個測試（generated client 不變） |

NOT RUN：e2e（UI 沒有改）；live 呼叫；完整的 `make backend-race`。

Coordinator 對本節問題的處理：
1. 沿用既有的錯誤碼加上 `details[0].code = AI_IMAGE_UNSUPPORTED`，不新增 D-025 的錯誤碼：接受。
2. UI 在照片路徑關閉時仍顯示照片按鈕、原因只有英文：demo 時照片路徑是開的，比賽後再處理（先知道照片路徑狀態或翻譯該原因）。
3. 讓 config 知道哪些 adapter 接受圖片：比賽後；在那之前 H-035 (3) 不讓 Claude 收圖片，批准紀錄也沒有。
4. `aiapproval` 的批准紀錄變化與照片設定的交叉測試：比賽後補。
5. T-055 舊 evidence 中的 negative case 訊息不同：歷史紀錄保留原樣。

下一步：T-056 的一輪獨立審查（H-029），範圍包含照片路徑、H-037 的 count_tokens 預留與 D-124 的 API 可用性（含照片）。

## Review round 1 fixes（2026-10-26）

Branch `t056-r1`（自 68d5344，本機，**沒有 push、沒有 PR**）。依 `checks/T-056_REVIEW_r1.md`（CHANGES REQUESTED：M1、M2；L1–L5；I1–I7）修正 M1、M2、L1、L4，並加上 I1 要的 live 照片 smoke。**沒有任何 live AI 呼叫**（SAM 3.1、Muse Spark、count_tokens、Claude 都沒有）；沒有讀取、印出或 grep 任何 key 的值（只有變數名稱，`run.sh` 一律 unset）；Railway 沒有任何操作。D-130–D-132 仍 **PROPOSED**（只改內容與量測的數字，沒有批准）。證據在 `checks/evidence/2026-10-26-T-056-r1/`（`run.sh` 執行並在每個 log 最後一行記 exit code；`verify.sh` 依序跑驗證並寫 `exit-codes.txt`）。

### 修正

| 項目 | 修正 | 測試（先紅後綠） |
|---|---|---|
| **M1** replay 在 base 變舊後回 409 | `internal/proposals/image.go`：唯讀 transaction 先 `idempotency.Seen`，**只有沒用過的 key** 才 `checkBase`；write transaction 本來就先 `Claim`（replay 直接回）。與文字的 createProposal 相同（D-011）。 | DB：`TestPhotoReplayAfterPublish`：202 → publish version 2 → 同 key 同檔 **202、body 與第一次相同**（修正前 409 `MODEL_VERSION_CONFLICT`）；新 key、舊 base → 409 `MODEL_VERSION_CONFLICT`；同 key 別的檔 → 409 `IDEMPOTENCY_KEY_CONFLICT`（修正前也是 409 `MODEL_VERSION_CONFLICT`）；proposal、image record、job、object 都只有 1 個。紅：`red-m1-l4.log`；綠：`green-m1-l4.log` |
| **L4** 同 key 並行各存一個 object | 同一個 API process 中，同一個 Idempotency-Key 的照片請求由讀取到 commit 依序執行（`keyLocks`：每個 key 一個 slot，等候至多到請求的 context 結束；沒有人持有或等候的 key 不留在表中）。後到的看到 key 已用過，只算 hash、不解碼、不存檔，Claim 再 replay。請求路徑仍**從不刪 object**（D-044：只有 maintenance 持有 `objects.Maintainer`），所以不同 API process 之間的競爭仍可能留下孤兒，與 commit 失敗的一樣由 D-044 的 maintenance 清理（D-130 (e)、`image.go` 註解）。 | DB：`TestPhotoConcurrentSameKey`：同 key 同時 4 個 → 4 個都是同一個 202、1 個 proposal、1 個 image record、1 個 job、**1 個 object**（修正前 4 個 object）；`TestPhotoIdempotency` 在 replay 與 conflict 前後數 object（review 的 mutant I2）；單元：`TestImageKeysRunOneAfterTheOther`（同 key 等候、別的 key 不等、context 逾時、表中不留 key） |
| **M2** 解碼記憶體遠超過文件 | `internal/proposals/photo`：`inspect` 在解碼任何像素之前（也在等 decode slot 之前）由 header 拒絕 `ErrEncoding`（415 `IMAGE_FORMAT_REJECTED`，details 在 `/file`）：JPEG 只收 SOF0／SOF1、8-bit、1 或 3 個 component；拒絕 SOF2（progressive）、SOF3／5–15（lossless、arithmetic、hierarchical）、4 個 component（CMYK、YCCK）、會被 `image/jpeg` 解成 RGB 複本的 3-component（檔案**任何位置**的 Adobe APP14 transform 0，包括 scan 之後：`image/jpeg` 在 scan 之後也讀它；component ID 為 R、G、B）、同一 component 在兩個 scan（T.81 sequential 每個 component 一個 scan）；marker 的走法與 `image/jpeg` 相同（segment 依長度、非 marker 的 bytes 略過、entropy-coded data 到下一個 marker）。PNG 拒絕 16-bit 與 interlaced（IHDR）。收下的解碼成每像素至多 4 bytes；重新編碼寫進 `capped`（超過 5 MiB 即停並回 413，不再為整個編碼長 buffer）。`MaxDecodeBytes` = 80 MiB（文件化的上限），`decodeSlots` 仍為 2。 | `TestSanitizeRefusesWhatTakesTooMuchMemory`（14 種：16-bit gray／RGBA、interlaced、progressive、progressive 4:4:4、lossless、arithmetic、CMYK、12-bit、Adobe transform 0（開頭與 scan 之後；並以 `jpeg.Decode` 確認兩者真的被解成 `*image.RGBA`）、R/G/B ID、兩個 scan、額外 bytes 之後的 progressive；每種都 `ErrEncoding` 且配置 < 1 MiB；9 種仍收下）、`TestADecodeStaysWithinItsBound`（4096² 的最壞接受：8-bit RGBA PNG、RGB PNG、baseline 4:4:4 header、重新編碼 11 MB 的 noise JPEG：每個 `TotalAlloc` ≤ `MaxDecodeBytes`；slot 數 × 上限 ≤ 160 MiB）、`TestAtMostTwoDecodesAtOnce`、`TestTheReencodingStopsAtMaxBytes`、`TestRefusedFormatsAreTheContracts`（body 即 openapi 的兩個 415 範例）；DB：`TestPhotoRefusals` 加 16-bit PNG 與 progressive JPEG（415、什麼都不存）。紅：`red-m2.log`（以未 commit 的 stub：16-bit RGBA PNG 被接受並配置 258.6 MiB、16-bit gray 65.6 MiB、interlaced 65.0 MiB、noise 的重新編碼 56.0 MiB 而且沒有 413）；綠：`green-m2.log` |
| **L1** 照片規則沒有讀到每個數字 | `internal/ai/review/photo.go`：metric 的 `period` 不是 base 的值 → `/metrics/<i>/period`；node 的 `domain_attributes` 中任何深度的 JSON number 不是 base 的值、或不在 float64 範圍內（1e400）→ `/nodes/<i>/domain_attributes`；base 的 metric period 與 `domain_attributes` 數字也算 base 的值；文字不讀。D-132 與 AI_SPEC 寫明範圍。 | `TestPhotoRefusesNumbersOutsideTheTypedMembers`：metric period 437、calendar 的 period 與 shift（review 存活的 mutant R5）、新步驟與既有步驟 attributes 的數字、深層的數字、1e400；沿用 base 的值（只在 base metric 的 300、只在 base attributes 的 5、calendar 的 480）被接受；從 base 抄來的 1e400 仍拒絕。紅：`red-l1.log`；綠：`green-l1.log` |
| **I3**（Info） | OpenAPI 的描述改成程式的順序：用過的 key 先判斷；沒用過的 key 先查 base（404／422／409）再查檔案。 | — |
| **I1** 第一次 live SAM 3.1 呼叫的程式路徑 | 見下面的「Live 照片 smoke」。 | — |

**Contract 先改**（commit 17ce21d）：`openapi.yaml` 的 createImageProposal 描述（M1 的順序、M2 拒絕的種類、超過 5 MiB 的重新編碼為 413）與自己的 415（兩個範例：不是 PNG／JPEG、解碼代價過高的種類）。錯誤碼與 details 碼都是既有的（`UNSUPPORTED_MEDIA_TYPE`、`IMAGE_FORMAT_REJECTED`），所以 schema、TS 型別與 generated client 不變；UI 的 `photo.problem.format`（en、zh-Hant）改為說明收哪些種類（「非交錯式的 8 位元 PNG，或基線 JPEG」），`make frontend-test` 見下。

### 量測：解碼的記憶體（修正前／後）

方式：`photo.Sanitize` 一次，`runtime.MemStats` 的 `TotalAlloc` 差（配置的總量，是 heap 峰值的上界）與每 1 ms 取樣的 `HeapInuse` 峰值，Go 1.27.3、`GOGC` 預設；影像由 ImageMagick 6.9.12 產生（`scripts/mkimgs.sh`，4096×4096，都在 5 MiB 內），probe 為 `scripts/zz_r1_measure_test.go.txt`（scratch 的 `git archive` 複本中執行，沒有 commit），log：`measure-before-4096.log`、`measure-after-4096.log`。

| 上傳（4096×4096） | 大小 | 修正前：配置／峰值 | 修正後 |
|---|---|---|---|
| progressive CMYK JPEG | 310,635 B | **329 MiB／330 MiB**，接受 | 415，0 MiB（header） |
| 16-bit RGBA PNG | 3,730,410 B | **267／255 MiB**，接受 | 415，0 MiB |
| progressive 4:4:4 JPEG | 280,266 B | **241／241 MiB**，接受 | 415，0 MiB |
| baseline CMYK JPEG | 286,282 B | 169／170 MiB，接受 | 415，0 MiB |
| interlaced 8-bit RGBA PNG | 842,071 B | 131／128 MiB，接受 | 415，0 MiB |
| 16-bit gray PNG | 1,404,072 B | 70／64 MiB，接受 | 415，0 MiB |
| 8-bit RGBA PNG（最壞的接受） | 598,701 B | 67／67 MiB | **66／66 MiB**，接受 |
| 8-bit RGB PNG | 542,022 B | 67／67 MiB | 66／66 MiB |
| baseline 4:4:4 JPEG | 246,901 B | 49／49 MiB | 48／48 MiB |
| baseline 4:2:0 JPEG | 135,806 B | 25／25 MiB | 24／24 MiB |
| gray JPEG、gray PNG、palette PNG | | 16–19 MiB | 17–21 MiB |

Reviewer 的四個檔（`rv056/imgs`：16-bit gray／RGBA PNG、progressive 4:4:4、progressive CMYK）修正後都在 header 被拒，配置 0 MiB（`measure-after-reviewer-imgs.log`）。Commit 的測試以 Go 自己產生的最壞接受（4096²）量測：8-bit RGBA PNG 65.8 MiB、RGB PNG 65.7 MiB、baseline 4:4:4 header 48.0 MiB、重新編碼為 11 MB 的 noise JPEG 修正前 56.0 MiB（且被接受）→ 修正後 29.0 MiB（413）。

**上限**：一次 `Sanitize` 至多配置 80 MiB（`MaxDecodeBytes`；量測最大 66 MiB），兩個 slot 同時解碼的存活資料至多 160 MiB。**Process 的 heap 會比這個大**：GC 在下一次回收前讓 heap 長到存活資料的約兩倍（`GOGC=100`）。量測（`scripts/zz_r1_conc_test.go.txt`，6 個 `Sanitize` 同時，`measure-*-concurrent.log`）：修正前 6 個 progressive CMYK JPEG heap in use 峰值 **+636 MiB**；修正後 6 個 8-bit RGBA PNG **+261 MiB**；`GOMEMLIMIT=192MiB` 時 +196 MiB，`256MiB` 時 +261 MiB（不受限）。等候 slot 的請求各持有至多 5 MiB 的 body，這部分不在 slot 的上限內（同時上傳的請求數沒有限制）。

**`GOMEMLIMIT`：沒有設定。** 理由：repository 沒有記錄 API 容器的記憶體上限（Railway 的方案與 service 的設定不在 `deploy/` 中），而 `GOMEMLIMIT` 是相對於那個上限的值；沒有它就無從說明某個值是對的。上面的量測顯示它有效（192 MiB 把 6 個並行的峰值壓到 +196 MiB）。建議（不是我改的，`deploy/railway/` 由另一個 agent 處理）：owner 知道 API service 的記憶體上限 M 之後，在 API（與 worker）的部署變數設 `GOMEMLIMIT` ≈ 0.75 × M（M 為 512 MiB 時約 `384MiB`），並在 `variables.json` 宣告、`check_deploy.py` 檢查。

### Live 照片 smoke（I1；make ai-smoke-photo-live）

第一次 live SAM 3.1 呼叫要經過真的照片管線、不手動 curl。做法：

- **共用的程式**：照片管線的 AI 部分（segmenter → server 的框檢查 → judge → `review.Review` → `review.Photo`）由 generator 原樣移到 `internal/ai/photopipeline`（`Concepts`、`TakesImages`、`Propose`）。Worker 的 generator 在讀回並驗證影像之後、dataset binding 之前呼叫它；generator 的所有既有測試不改即通過（行為相同）。Smoke 呼叫同一個 `Propose`，不是複本。
- **輸入**：`testdata/photos/electronics-line-demo.png` 經 `photo.Sanitize`（API 存的樣子）與 `photo.Verify`、`provider.NewImage`（worker 讀回時的檢查）；`testdata/photos/bare-line.json` 以 eval 的 synthetic organization／model ID。這些在開 spend record **之前**讀，讀不了即 exit 2。
- **Gate**（與 worker 相同）：`config.LoadAI` 的 `AIVision.LiveEnabled()`（照片路徑的 live provider、完整的 record 且列有圖片、它的 model 與 key、`GEMINUS_AI_LIVE=on`），provider 為 meta（這個 build 唯一收圖片的 adapter），批准列有 segmentation model；backend 以 worker 的 `photoBackends` 的方式建（`meta.New` 為 judge、`meta.NewSegmenter` 為 SAM 3.1，同一把 key；`cmd/ai-eval-live` 的 `livePhoto`），之後再確認 judge 收圖片。任何一項不成立：exit 2、不建 backend、不寫任何檔。
- **花費**：自己的 in-memory 上限，預設 **USD 1**（`AI_EVAL_LIVE_MAX_USD` 可改），須 ≤ 部署的日上限，且 ≤ `checks/ai-evals/live-spend.jsonl` 留給該 UTC 日與月的額度（與文字的 run 同一段 `begin`，訊息不變）；第一次呼叫前寫 open line（kind `smoke-photo`、judge 的 model、approval、上限、report 檔名），結束時不論結果寫 close line（花費與 meter 持有的金額）。預期：SAM 8 個 concept 依序各預留 3 張（USD 0.0075）、各結算為計費的張數（約 USD 0.02）；judge 以 count（T-055 live 時 demo 為 6,661）加 10% 預留 3 個 attempt（約 USD 0.132）；同時持有至多約 USD 0.152。
- **Report**：`checks/ai-evals/live-smoke-photo-<UTC 開始>-<run ID>-<model>.json`（可 commit；從不覆寫）：status、code、provider 的 status／error type、issue code、server／provider risk、patch hash、新的 process 步驟數；segmenter：概念數、呼叫數、有回答的概念數、有框的概念數、框數與各概念的框數、**每個概念的 `images_processed`**、計費的影像數、回答的 frame（例如 `1200x520`）、unknown attempt、預留數與金額、花費、延遲；judge：quote 次數與失敗數、**count（count_tokens 的 input_tokens）、預留的 input token（count 加 10%）、實際計費的 input token、兩者之比**、output token、預留與花費；總花費、meter 持有的金額、同時持有的最大值、上限；gate（`ready_without_server_validation`、`needs_input_kept_changes`、`pipeline_failed`）與 observation（`over_reservation`、`billed_input_over_reserved`、`images_processed_not_one`、`frame_not_the_image`、`no_boxes`：H-037 與 I1 要看的事，> 0 時 stderr 有 WARNING）。**不記** prompt、回答文字、框座標、影像 bytes、key、server 位址。為了記 count，`provider.Quote` 多一個 `Counted`（Meta adapter 的 count_tokens quote 填入；文字 request 為 0；預留仍只用 `MaxInputTokens`）。
- **Exit**：0 完整且 gate 為 0；1 管線以失敗結束（例如 count 失敗）、gate 失敗、或上限／取消停止（report 仍寫、record 關閉）；2 拒絕（沒有呼叫）；3 呼叫之後的錯誤或 report／close line 寫不進去。

**執行方式**（coordinator 在有 key 的 session，依 I1：一次、只用 demo 影像）：

```sh
AI_PROVIDER=meta AI_POLICY_APPROVAL=H-029 AI_MODEL=muse-spark-1.3 GEMINUS_AI_LIVE=on \
  make ai-smoke-photo-live            # = go build -o bin/ai-eval-live ./cmd/ai-eval-live && bin/ai-eval-live -smoke-photo -root .
```

`GEMINUS_MUSE_API_KEY` 由環境提供（只看名稱）；`AI_VISION_PROVIDER`、`AI_VISION_POLICY_APPROVAL`、`AI_VISION_MODEL` 不設（照片路徑跟隨 `AI_PROVIDER`，與 worker 讀法相同；若要設，值必須與 worker 的相同）；`AI_EVAL_LIVE_MAX_USD` 不設即 USD 1；日／月上限（`AI_DAILY_LIMIT_USD`、`AI_MONTHLY_LIMIT_USD`）照部署。Run 之後 commit report 與 `live-spend.jsonl`。今天的 record 已有約 USD 3.03（T-053／T-055 的 run），預設日上限 USD 10 下還有餘裕。

**Smoke 的測試**（都沒有呼叫任何 provider）：

| 範圍 | 測試 |
|---|---|
| 在 process 內的 fake（billed 如 Meta） | `internal/aievals`：`TestThePhotoSmokeRunsThePipeline`（ready、8 個概念各 1 張、frame 1200x520、count 6661／預留 7328／計費 6702、預留與花費的金額、同時持有的最大值、report 不含回答文字、pool、影像的 base64、框、`untrusted_data`；檔名與不覆寫）、`TestAFailedCountSendsNothing`（count 失敗：judge 不預留不送、SAM 的花費照記、failed）、`TestTheCapStopsThePhotoSmoke`（上限低於 judge 的預留：SAM 之後停；低於一個概念：什麼都不呼叫）、`TestThePhotoSmokeAsksTheJudgeFirst`（judge 不收圖片：segmenter 不花錢）、`TestThePhotoSmokeObserves`（沒有框、轉向的 frame、SAM 說 2 張）、`TestMainRefusesThePhotoSmokeWhileThePhotoPathIsOff`（什麼都沒設、H-029 沒有開關、H-018（沒有圖片）、與 `-smoke`／`-cases` 同用、build 沒有 photo backend：exit 2、不建 backend、不寫檔、不印 key） |
| 兩個 Meta adapter + 本機 server（Responses、Messages、count_tokens） | `internal/ai/provider/meta`：`TestThePhotoSmokeOverHTTP`（8 個 `/v1/responses`、然後 count、然後一個 `/v1/messages`；count 的 body 即 call 的 body 去掉 `max_tokens`；ready；預留與花費依 Meta 的價格；report 不含 key、server 位址、回答、影像、框）、`TestThePhotoSmokeStopsAtAFailedCount`（500 重試後放棄、`1.5`、超過 context window：沒有 Messages request、judge 不預留、SAM 的花費照記）；`TestTheCountsMargin` 也檢查 `Quote.Counted` |
| Main 的 live 路徑（in-process fake、fake key） | `internal/config/aiapproval`：`TestThePhotoSmokeRunsBehindTheWorkersGate`（backend 以照片路徑的設定建：meta、muse-spark-1.3、sam-3.1、一把 key；record 兩行：open kind `smoke-photo`、close `complete` 與花費；report 檔名；不含 key）、`TestThePhotoSmokeCapFitsTheDay`（上限高於日上限、高於 record 留下的額度、open line 以整個上限計、不是金額、沒有 record：exit 2、不建 backend、record 不變；在額度內則執行）、`TestThePhotoSmokeReportsAFailedCount`（exit 1，record `gate_failed` 與 SAM 的花費，WARNING 行）；`cmd/ai-eval-live`：`TestThePhotoBackendsAreThePhotoPaths` |
| 共用的管線 | `internal/ai/photopipeline`：`TestThePipelinesChecks`、`TestASegmenterFailureKeepsItsMetadata`；其餘由 generator、`internal/api` 的照片測試涵蓋 |

### Mutation（`/tmp/claude-0/…/scratchpad/t056r1-mut/`，`git archive` 的複本；`scripts/mutate.py`）

每個 mutant 只改一處、跑指定的測試、逐 byte 還原；build 失敗記 INVALID（不算 killed）。DB mutant 在 `dt_test_t056r1m`（`make db-test` migrate），`GEMINUS_REQUIRE_DB_TESTS=1`。結束時以 `diff -rq` 確認複本的 `internal/`、`cmd/` 與本 branch 相同。

- 第一輪（`mutants-run1.log`／`.json`，HEAD 95375a5）：52 個，**44 killed、5 survived、3 INVALID**。存活：R3（base 的 attributes 數字不算已知）、R4（base 的 metric period 不算已知）：測試用的值在 base 的其他地方也有（2、480），殺不掉；G4（segmenter 失敗時丟掉它的 metadata：review 的 L2 同一個缺口，在管線層）；S10（SAM 的 `images_processed` 不是 1 時不記）；S15（不做 `photo.Verify`）。INVALID：P3、R7、S6（寫法不能 build，之後改寫）。
- 補測試（commit 40f2d25）：只在 base 一處出現的值（300、5）、從 base 抄來的 1e400、segmenter 失敗時 `Result.Segmenter` 仍在、SAM 說 2 張；並拿掉 `ParseFloat` 之後永遠不成立的 NaN／Inf 檢查（JSON number 不會是 NaN／Inf，超出範圍是 `ParseFloat` 的錯誤）。第二輪（`mutants-run2.log`）：P3、R3、R4、G4、S6、S10 killed，R7、S15 存活，R7 由 1e400 的案例補上。**那個 1e400 的案例本身寫錯了**（借用了前一個案例的 base，拿掉了它的數字 5，修正後的程式上測試也失敗）：我在它之上跑的一次 R 系列 mutant 因此全部「killed」而沒有意義，結果已作廢、不列入；這個錯誤由下面第一次 `verify.sh` 的 `test-changed` 抓到（`test-changed-first.log`、`exit-codes-first.txt`），commit 83df7e1 修正。之後 `mutate.py` 先在未改的複本上跑每一組測試（baseline 必須 exit 0）才跑 mutant。
- **最終一輪**（`mutants-final.log`／`.json`，83df7e1，6 組 baseline 都 exit 0）：52 個，**51 killed、1 survived（S15，等價）、0 INVALID**。S15 等價：`Sanitize` 的輸出一定通過 `Verify`，smoke 做這一步只為與 worker 相同。範圍：M1（K1、K2）、L4（K3–K7：不等候、表不清除、逾時不扣、永不等候、replay 重新存檔）、M2（P1–P16：每一種拒絕、scan 之後的 marker、非 marker 的 bytes、截斷、slot 數、header 在 slot 之前、上限常數往上與往下）、HTTP 的對應（H1）、L1（R1–R7）、共用管線（G1–G5）、smoke（S1–S16：count、quote 的 count、judge 先問、gate、日額度、record 的 close 與 kind、上限、peak、observation、report 名稱、失敗的 gate、日上限）。P11、G5 以 panic 失敗（仍是測試失敗）。第一輪的 P1、P2 因 `TestAtMostTwoDecodesAtOnce` 的 header 檢查以 `context.Background()` 等 slot 而卡到 10 分鐘才被殺；該測試改用自己的 2 秒 context（40f2d25），最終一輪 P1、P2、P13 都在幾秒內被殺。

### 指令與 exit code

（Go 1.27.3、`GOTOOLCHAIN=local`；Python `.venv/bin/python`；`run.sh` unset 所有 AI 變數。DB 測試只在一次性 cluster（socket `/var/lib/postgresql/geminus-throwaway-hkb6`、port 55450）上本任務新建的 `dt_test_t056r1a`（開發）、`dt_test_t056r1b`（integration）、`dt_test_t056r1m`（mutant），最後都已 DROP；沒有啟動或停止任何 cluster。）

| 指令 | exit | Log | 結果 |
|---|---|---|---|
| 紅：`go test -count=1 -p 1 -run '^(TestPhotoIdempotency\|TestPhotoReplayAfterPublish\|TestPhotoConcurrentSameKey)$' ./internal/api/`（修正前，`dt_test_t056r1a`） | 1 | `red-m1-l4.log` | M1：replay 409 `MODEL_VERSION_CONFLICT`；L4：4 個 object |
| 綠：同上加 `TestPhotoStaleBaseAndAvailability`、`TestPhotoRefusals`、`TestAPhotoProposalEndToEnd`，`-race` | 0 | `green-m1-l4.log` | |
| 紅：`go test -run 'TestSanitizeRefusesWhatTakesTooMuchMemory\|TestADecodeStaysWithinItsBound\|…' ./internal/proposals/photo/`（舊的 `photo.go` 加未 commit 的 stub） | 1 | `red-m2.log` | 14 種都沒有被 header 拒絕；16-bit RGBA PNG 配置 258.6 MiB；重新編碼沒有截斷 |
| 綠：`go test -count=1 -v ./internal/proposals/photo/` | 0 | `green-m2.log` | |
| 紅／綠：`go test -run TestPhoto ./internal/ai/review/` | 1／0 | `red-l1.log`、`green-l1.log` | 紅：metric period 與 4 種 attributes 的數字為 0 個 issue |
| 量測（scratch 的複本，未 commit 的 probe） | 0 | `measure-*.log` | 見「量測」 |
| `make spec-check PYTHON=/home/user/Geminus/.venv/bin/python`（contract 的 commit 之後） | 0 | `spec-check-contract.log` | `contracts-check.json` 的 `resolved_local_ref_occurrences` 870 → 871 |
| 第一次 `verify.sh`（40f2d25） | gofmt 0、vet 0、**test-changed 1** | `exit-codes-first.txt`、`test-changed-first.log` | 我在 40f2d25 加的 1e400 案例寫錯（見 Mutation）；之後的步驟停止（沒有結果），83df7e1 修正 |
| `gofmt -l cmd internal schemas scripts prompts` | 0（空） | `gofmt.log` | |
| `go vet ./internal/proposals/... ./internal/ai/... ./internal/aievals/... ./internal/config/... ./cmd/ai-eval-live/ ./cmd/worker/ ./cmd/api/ ./internal/api/` | 0 | `vet.log` | |
| `go test -count=1`（同上） | 0 | `test-changed.log` | 22 個 package ok（DB 測試在此 skip，由 integration-test 跑） |
| `go test -race -count=1 -timeout 30m`（同上） | 0 | `race-changed.log` | 22 個 package ok，沒有 data race（`internal/proposals/photo` 在 race 下 146 s） |
| `make backend-test "GOFLAGS=-count=1 -timeout=30m"` | 0 | `backend-test.log` | gofmt、`go vet`、84 個 package ok、12 個沒有測試檔、0 FAIL |
| `make architecture-check` | 0 | `architecture-check.log` | `PASS architecture: 96 packages, 26 rules, 16 build configurations`（多了 `internal/ai/photopipeline`） |
| `make ai-eval` | 0 | `ai-eval.log` | fake corpus；`checks/ai-evals/fake-*.json` 沒有改變 |
| `make integration-test`（新建的 `dt_test_t056r1b`） | 127 → **0** | `integration-test-first.log`、`integration-test.log` | 第一次是 `verify.sh` 的參數順序錯（`run.sh -db` 必須在最前，沒有執行任何測試）；修正後：`make db-test`（0001–0017 up／down／up 與全部 SQL 測試）＋ `make db-go-test`（`-race -p 1`、`GEMINUS_REQUIRE_DB_TESTS=1`）：38 個 package ok，沒有 FAIL 或 SKIP（含 `internal/api` 的照片測試） |
| `npm ci --prefix apps/web --engine-strict=false` | 0 | `npm-ci.log` | |
| `make frontend-test` | 0 | `frontend-test.log` | lint、typecheck、48 個檔案、1062 個測試 |
| mutation（`scripts/mutate.py`；第一輪、第二輪、最終） | 0、0、0 | `mutants-run1.*`、`mutants-run2.*`、`mutants-final.*` | 最終 51／52 killed、1 等價 |
| `make spec-check PYTHON=/home/user/Geminus/.venv/bin/python`（本節寫完後） | 0 | `spec-check.log` | contracts、planning、docs、SQL inventory、reference math、decisions、TS contracts、hashes、DB tests、deploy 都 PASS；`checks/*-check.json` 沒有改變（不含官方完整 OpenAPI validator：NOT_RUN_UNAVAILABLE） |
| `dropdb` `dt_test_t056r1a`、`dt_test_t056r1b`、`dt_test_t056r1m`；`SELECT count(*) FROM pg_database WHERE datname LIKE 'dt_test_t056r1%'` | 0、0、0；0 | — | 已刪除；沒有啟動或停止任何 cluster |

### 沒有修的（延後）

- **L2**（可重試的 judge 失敗讓 SAM 在每個 job attempt 重跑；segmenter 單獨失敗時 `attempt_failed` 的 audit 沒有測試）：沒有改程式。管線層的缺口由 `TestASegmenterFailureKeepsItsMetadata` 守住（`Result.Segmenter` 在失敗時仍在，generator 以它決定是否寫 `attempt_failed`），但經 worker 的 audit 測試仍沒有。文件的「最多約 USD 0.02」應為「每個 job attempt 至多一次 segmentation：可重試的失敗最多 3 次，約 USD 0.06」，留給 coordinator 決定是否接受（問題 2）。
- **L3**（API 為 worker 一定拒絕的 domain 存照片）、**L5**（multipart `filename`／CTE、`InArea`、鎖 model 的並行、管線的框數邊界的測試缺口）：沒有改。
- **I2、I4、I5、I6、I7**：沒有改（I5：這個 smoke 的花費記在 `live-spend.jsonl`，worker 的照片 proposal 記在 DB ledger，仍要手動合計；I6：照片路徑關閉時 replay 仍是 503，D-124 的設計）。

### NOT RUN

- 任何 live 呼叫：`make ai-smoke-photo-live` 本身（由 coordinator 在有 key 的 session 執行）、`make ai-smoke-live`、`make ai-eval-live`；SAM 3.1 對 demo 影像的實際回答、count 與計費的實際差。
- `make e2e-test`（依指示；3100–3102 共用）；`make frontend-build`。
- 整個 repository 的 `make backend-race`：只跑了改動的 package 的 `-race`（見上表）。
- API 容器在 Railway 的記憶體上限與 `GOMEMLIMIT`（沒有設定，見上）；部署檔沒有改（另一個 agent 處理 `deploy/railway/` 等）。
- S3 adapter 上的照片（只用 FS adapter）；多個 API process 之間的同 key 競爭（只有單一 process 的鎖；DB 測試是單一 process）。
- Fuzz（`inspectJPEG` 適合 fuzz：任意 bytes 不 panic、`ErrEncoding` 時 `image/jpeg` 確實會解成較大的型別；沒有執行）。

### 問題

1. **Progressive JPEG 一律拒絕**：網頁存下的 JPEG 常是 progressive（例如 mozjpeg 的預設），使用者會看到 415 與「請選擇基線 JPEG」。要改為只在較小的像素數（例如 ≤ 2048²，量測約 60 MiB）接受 progressive 嗎？（多一條以像素數計的規則與測試）
2. **L2 的花費說明**：可重試的 judge 失敗會讓 SAM 在每個 attempt 重跑（最多 3 次、約 USD 0.06）。接受並改文件，或保存 segmentation 的結果給重試沿用？
3. **`GOMEMLIMIT`**：需要 API service 的記憶體上限才能決定值；請 owner 提供，或接受目前「兩個 slot、每個至多 80 MiB、GC 前約兩倍」的上限。
4. **Smoke 的 kind**：spend record 多了 kind `smoke-photo`（D-114 (b) 的 record 格式之內，report 名稱 `live-smoke-photo-…`）。D-114 是否要補一句？（沒有改 D-114 的文字，只在 D-131 (g) 記錄）

### Commits（`t056-r1`）

| Commit | 內容 |
|---|---|
| 17ce21d | contract first：createImageProposal 的描述（key 先判斷、拒絕的種類、413）與自己的 415 範例；`contracts-check.json` |
| 0b0f9ff | M1、L4、M2：`image.go`（key 先於 base、同 key 依序）、`photo.go`（header 檢查、截斷、`MaxDecodeBytes`）、HTTP 的對應、UI 的訊息、全部的測試 |
| 76074c4 | L1：`review.Photo` 讀 metric 的 period 與 `domain_attributes` 的數字 |
| 95375a5 | I1：`internal/ai/photopipeline`、`-smoke-photo`（`MainWith`、`PhotoRun`、`PhotoReport`）、`provider.Quote.Counted`、`make ai-smoke-photo-live`、測試 |
| 40f2d25 | 第一輪 mutation 存活的 mutant 的測試；`attributeNumbers` 拿掉不可能成立的檢查 |
| 83df7e1 | 修正 40f2d25 寫錯的 1e400 測試案例（自己的 base） |
| （本節的 commit） | 本節、D-130 (b)(e)、D-131 (g)、D-132、SECURITY §6、AI_SPEC、`planning/tasks.json` 的 completion_evidence、證據 |

## Coordinator 驗證：review 第 1 輪修正（2026-10-27）

`t056-r1` 的七個 commit（17ce21d..c5b101b）cherry-pick 到已整合 Auth0 步驟的主線，沒有衝突；之後在 D-114 記下 spend record 的第三種 kind `smoke-photo`（1abfa01）。

指令與結果（evidence：`checks/evidence/2026-10-27-T-056r1-coordinator/`，`exit-codes.txt` 依執行順序；HEAD 1abfa01；go1.27.3、Node 22；DB 測試在暫時的 cluster（`/var/lib/postgresql/geminus-throwaway-hkb6`，port 55450））：

| 指令 | exit | 說明 |
|---|---|---|
| `make spec-check` | 0 | 含 OpenAPI 的 key-first replay 與 415 |
| `make backend-test "GOFLAGS=-count=1 -timeout=30m"` | 0 | 84 個 package ok |
| `make architecture-check` | 0 | 96 packages、26 rules |
| `make ai-eval` | 0 | fake provider |
| `go test -race -count=1 -timeout 30m`（ai、proposals、api、aievals、cmd） | 0 | 22 個 package ok |
| `make integration-test`（新的 dt_test_hkb11c，跑完刪除） | 0 | 38 個 package ok，沒有 FAIL 或 SKIP |
| `npm ci`（apps/web、apps/web-e2e） | 0、0 | |
| `make frontend-test` | 0 | 1062 個測試 |
| `make frontend-build`、`npm audit --omit=dev` | 0、0 | 0 vulnerabilities |
| `make e2e-test`（ports 空出後、以 flock） | 0 | 514 passed、32 skipped（只在 CI 比對的 screenshot） |

NOT RUN：任何 live 呼叫（包括照片 smoke 本身）；完整的 `make backend-race`；S3 adapter；JPEG header scanner 的 fuzz。

Coordinator 對本輪問題的處理：
1. Progressive JPEG 一律拒絕：比賽期間維持（demo 圖片是產生的 PNG）；較小的 progressive JPEG 是否接受，比賽後再決定。
2. L2（judge 可重試的失敗會讓 SAM 在每個 attempt 重跑，最多約 USD 0.06）：接受並記錄；快取 segmentation 比賽後再做。
3. `GOMEMLIMIT`：部署時依 Railway 給 API service 的記憶體上限設為約 0.75 倍，由部署 session 決定並記錄。
4. Spend record 的 `smoke-photo`：已記入 D-114（1abfa01）。

結論：review 第 1 輪的 M1、M2 已修正並由 coordinator 驗證（L1、L4 一併修正）；依 H-029，T-056 可以進行第一次 SAM 3.1 的 live 呼叫：以有 key 的新 session 執行 `make ai-smoke-photo-live`，一張 demo 圖片、一次一個 proposal、上限 USD 1，只經 worker 的照片 pipeline，不用 curl。

## Live 執行：照片（SAM 3.1＋Muse Spark）（2026-10-27）

依 H-029、H-033、H-035 與 review 第 1 輪 I1，在 coordinator 驗證過的程式（HEAD 98a800a，branch `claude/lucid-meitner-vq1awo`）上，由有 key 的 session（T-055 live 的同一個）執行**一次**照片 smoke。證據在 `checks/evidence/2026-10-27-T-056-live/`（`environment.txt`、`smoke-photo.log`）。

- **Key**：`GEMINUS_MUSE_API_KEY` 只檢查名稱；值、長度、前綴沒有印出、記錄或出現在 argv。`AI_VISION_*` 都沒有設（照片路徑跟隨 `AI_PROVIDER`）。沒有對 SAM 手動 curl，沒有經 API 送照片 proposal，沒有動 Railway。
- **不記**：prompt、回答文字、框座標、影像 bytes、key（report 本身也不含這些）。

### 指令與結果

| 指令 | exit | 結果 |
|---|---|---|
| `AI_PROVIDER=meta AI_POLICY_APPROVAL=H-029 AI_MODEL=muse-spark-1.3 GEMINUS_AI_LIVE=on make ai-smoke-photo-live`（00:53:28Z–00:53:34Z） | **0** | run `01M5XDJ3T0TTGE55K3A3JVK29N`：status **needs_input**（server 自己回，D-131 (d)），server risk none；gates `needs_input_kept_changes` 0、`pipeline_failed` 0、`ready_without_server_validation` 0；observations `no_boxes` **1**（stderr 有 WARNING），其餘（`over_reservation`、`billed_input_over_reserved`、`frame_not_the_image`、`images_processed_not_one`）0；`complete` true |

- **Report**：`checks/ai-evals/live-smoke-photo-2026-10-27T005328Z-01M5XDJ3T0TTGE55K3A3JVK29N-muse-spark-1.3.json`。
- **Spend record**（`checks/ai-evals/live-spend.jsonl`）：`open` kind `smoke-photo`、model `muse-spark-1.3`、approval H-029、cap 1,000,000,000；`close` settled＝committed 20,000,000 nano-USD、result complete。
- **影像與 base**：`testdata/photos/electronics-line-demo.png`（PNG 1200×520，14,197 bytes，以 worker 的方式驗證）；base `testdata/photos/bare-line.json`（electronics_line）。
- **Segmenter（SAM 3.1）**：8 個 concept（`smt line`、`aoi machine`、`ict tester`、`fct tester`、`burn-in chamber`、`packing station`、`conveyor belt`、`workstation`），8 個呼叫，**8 個都有回答，0 個有框，框數 0**；每個 concept 的 `images_processed` 皆為 **1**（符合預期）；計費 8 張；unknown attempt 0；8 個預留共 60,000,000、花費 **20,000,000 nano-USD（USD 0.02）**；同時持有的最大值 25,000,000。**回答的 frame 沒有記錄**（`frames` 是空的：沒有任何結果，所以無從比對 1200×520；`frame_not_the_image` 0）。
- **Judge（Muse Spark）**：**沒有呼叫**（`asked` false）：沒有任何框時 server 自己回 needs_input、不呼叫 vision model（D-131 (d)）。所以 count_tokens、預留、`usage.input_tokens` 與 count 的比較、judge 回答的 `model` 字串都**沒有資料**；`served_models` 只有 `sam-3.1`。
- **花費**：總計 **USD 0.02**（cap 1.00）；今天（UTC 2026-10-27）的 record 只有這一筆。

### 意外的結果

1. **SAM 3.1 在 demo 圖片上找不到任何一個 concept**（`no_boxes`）。Demo 圖片是產生的平面圖（淺色底上的標字方框與箭頭：SMT、AOI、ICT 1／2、FCT、PACK），不是實物照片；SAM 的文字提示是找實物（機台、輸送帶），對畫出來的方框可能就是找不到。這是 §9 問題 9（8 個 concept 的措辭是否找得到東西）的第一個 live 答案：**在目前的 demo 圖片上找不到**。照片 proposal 的 judge 路徑因此在 live 從未走到。可能的下一步（owner／coordinator 決定，本 session 沒有做）：(a) 換一張更像實物的 synthetic demo 影像（例如 3D 渲染的產線）；(b) 改 concept 的措辭（例如 `box`、`rectangle`、`arrow`），但這會改 D-131 (b) 與 ELECTRONICS_LINE §13；(c) 平面圖跳過 SAM、只用 Muse Spark 讀圖（改 D-131 (a)）。不論哪一個都需要 reviewed change，之後再跑一次 smoke。
2. **Segmenter 的 `latency_ms` 為 0**（run 實際約 6 s）：`provider.SegmentClient`（`internal/ai/provider/segment.go:288`）以 `defer` 寫 `meta.LatencyMillis`，但函式以 `return out, err` 回傳 `out` 的值（非具名回傳），回傳值在 defer 執行前已複製，所以回傳的 metadata 的延遲永遠是 0（影響 report 與 audit 的 segmenter 延遲；不影響花費或安全）。本 session 只記錄、沒有修。

### NOT RUN

- Judge 的 live 呼叫（count_tokens 與 Messages）、count 與計費 input 的比較（H-037 的未決事項）、judge 的 `model` 字串：因為沒有框，管線沒有走到。
- SAM 回答的 frame 是否為 1200×520：沒有結果可比對。
- 照片 proposal 經 API／worker／DB 的路徑、人工批准、Railway。
- 第二次 run：依指示只跑一次。

`make spec-check PYTHON=/home/user/Geminus/.venv/bin/python`（加上本節之後、commit 之前）：**exit 0**（`spec-check.log`）。

## SAM 概念調整（第一次 live 之後）

Branch `t056-concepts`（自 89bdfdb，本機，**沒有 push、沒有 PR**）。依 coordinator 對 §9 問題 9 的回答（概念的措辭依 live 結果調整，每次調整都記錄）。**沒有任何 live 呼叫**（SAM 3.1、Muse Spark、count_tokens、Claude 都沒有）；沒有讀取、印出或 grep 任何 key 的值（`run.sh` 一律 unset AI 變數，只有名稱）；Railway 沒有任何操作；沒有執行 `make e2e-test`；沒有改 `deploy/`、`scripts/rehearse_railway_deploy.py`、`scripts/check_deploy.py`、`cmd/provision-tenant`。D-131 仍 **PROPOSED**（新增 (h)，沒有批准）。證據在 `checks/evidence/2026-10-27-T-056-concepts/`（`run.sh` 執行並在每個 log 最後一行記 exit code；`verify.sh` 依序驗證並寫 `exit-codes.txt`）。

### 問題與證據

- 第一次 live（run `01M5XDJ3T0TTGE55K3A3JVK29N`）：8 個概念都回答、`images_processed` 各 1、0 個框、`no_boxes=1`，server 回 needs_input，judge 沒有執行，USD 0.02。
- Demo 影像是畫出的平面圖（標字的方框 SMT、AOI、ICT 1、ICT 2、FCT、PACK，以粗線與箭頭相連，加上標題與註腳），不是實物照片。
- Meta 的 SAM 文件（2026-10-27 讀取）：「Give it a short noun phrase naming one thing」、「Concrete nouns match best」、「Attributes change the match set predictably. On an image of six squares」`square` 找到 6 個（https://dev.meta.ai/docs/sam/segmenting「Write the concept prompt」；https://dev.meta.ai/docs/sam/overview）。SAM 3 論文 appendix B：「SAM 3 struggles to generalize to fine-grained out-of-domain concepts (e.g., aircraft types, medical terms)」（arXiv 2511.16719）。也就是：形狀名詞（正方形、方框）在畫出來的圖上可以找到；`aoi machine`、`ict tester` 這類細分領域的名詞是它已知的弱點。文件沒有提到平面圖或示意圖（只有照片的例子），所以 `rectangle` 與 `arrow` 在 demo 上是否找得到，仍要以 live 確認（見下面的「Live session 的指令」）。

### 修改

| 項目 | 內容 | 測試（先紅後綠） |
|---|---|---|
| 1. 延遲的 bug | `provider.SegmentClient.Segment`（`segment.go`）以 defer 寫 `LatencyMillis`，但寫到 `return out, err` 已複製之後的區域變數，所以 segmenter 的 `latency_ms` 永遠是 0（audit 的 `segmenter_latency_ms`、smoke 的 report）。改為具名回傳值，defer 寫進回傳值本身。 | `TestASegmentationsLatencyIsReturned`（有回答、沒找到、provider 錯誤、逾時）：紅 `red-latency.log`（每種都是 0）→ 綠 `green-latency.log` |
| 2. Smoke 專用的概念覆寫 | `bin/ai-eval-live -smoke-photo -concepts "rectangle,box,arrow"` 或 `AI_SMOKE_PHOTO_CONCEPTS`（擇一；兩者都設、或 `-concepts` 沒有 `-smoke-photo` 都拒絕）：segmenter 改問這些概念，經同一條 `photopipeline.Propose`（judge 的 SEGMENT_CONCEPTS 就是這些）、同一個 gate、上限（預設 USD 1）與 spend record（kind `smoke-photo`）。檢查與 segmenter client 相同：`provider.CheckConcepts`（由 client 的規則 export：1 到 8 個、不重複、各 64 bytes 內的小寫 ASCII 名詞片語），空的項目也拒絕；在建 backend、碰 spend record 之前 exit 2。Report 新增 `concepts_overridden`、`segmenter.concept_list` 與 `segmenter.by_concept`（每個呼叫：是否回答、框數、每個框的寬與高相對於回答的 frame 的比例 `width_share`／`height_share`，小數 3 位；frame 外的框不算尺寸）；**不記**框的位置、confidence、影像。Summary 行寫出覆寫與每個概念的框數。只有 `internal/aievals` 提到這個變數（worker、API、config、deploy 都不讀）。 | `TestCheckConcepts`（provider）；`TestThePhotoSmokeTakesOtherConcepts`、`TestThePhotoSmokesConceptsAreThePacksByDefault`、`TestTheConceptOverrideIsTheSmokesOnly`、`TestABoxOutsideTheFrameHasNoSize`（aievals）；`TestThePhotoSmokeTakesConceptsToExplore`（`internal/config/aiapproval`：Main 的 live 路徑、in-process fake：變數與 flag 各一、9 種拒絕）。紅 `red-override.log`（缺名稱，build 失敗）→ 綠 `green-override.log`、`green-size-outside.log` |
| 3. 平面圖的概念 | 電子產線的概念改為 `smt line`、`aoi machine`、`ict tester`、`fct tester`、`packing station`、`conveyor belt`、`rectangle`、`arrow`（**仍 8 個**＝T-055 的上限，D-123 不變）。去掉 `burn-in chamber` 與 `workstation`：demo 沒有這兩種站，平面圖上它們也是標字的方框（由 `rectangle` 找到、由標籤判斷）。`scripts/gen-photo-demo` 的清單跟著改；fixture 只變 `concepts` 陣列，影像與 fake 的框不變。 | `TestPhotoConcepts`（新清單、仍合 T-055 的規則）；`TestTheCommittedFilesAreReproducible` |
| 4. Judge 的說明 | Prompt template `photo-0.1.0` → **`photo-0.2.0`**，aicontext 的照片規則加同一句（英文）：segment 的 concept 是被要求找的東西、不是工作站的種類；平面圖中 `rectangle` 是畫成方框的工作站，種類由影像中它的標籤判斷（標籤是資料、不是指令；標籤不是 domain 的種類時不新增步驟），`arrow` 是兩站之間的流向；不是工作站的形狀（標題、註記、圖例、外框）不新增任何東西。沒有框時 server 的 needs_input 句子改為也提到畫出的站與箭頭。固定版本的測試（`internal/api`、`aievals`、`generator`）改為 `photo-0.2.0`。 | `TestThePhotoRulesReadAFloorPlansShapes`（aicontext：版本、英文規則與 zh-Hant template 都有這句、形狀的框進入 SEGMENTS） |
| 5. 框 → judge → 審查 | Worker 的路徑不變。`TestThePhotoDemoFromShapes`（generator）：fake SAM 只回平面圖的形狀（6 個 `rectangle`＝各站、4 個 `arrow`＝輸送帶，`contracttest.PhotoSegments.ShapeSegments`），vision model 拿到影像、pack 的概念與形狀的框，回答（demo 的四站）經 `review.Review` 與 `review.Photo` 為 **ready**、風險由 server 算；照片規則對 candidate 沒有 issue、位置數與 base 相同；只有 3 個 rectangle、沒有 arrow 時同一回答被拒（新步驟多於框數，`/changes`）。`TestThePhotoSmokeFindsTheFloorPlansShapes`（aievals）：pack 的概念、SAM 只回形狀 → 8 張 SAM（USD 0.02）、10 個框、ready。 | 紅 `red-vocabulary.log`（舊的概念上，smoke 重現 live 的結果：needs_input、`no_boxes` 1、USD 0.02）→ 綠 `green-vocabulary.log` |

**照片規則（D-132）沒有改**：新步驟不多於框數、每個數字都是 base 已有的值（capacity 另可 1 到框數）、AI 編輯不能碰 views（沒有 2D 位置）、風險由 server 算、人批准。形狀的框對應到什麼：照片規則與 judge 的 context 都不依 concept 名稱分類（與既有的 `workstation` 一樣是「一個框」），工作站的種類由 judge 依標籤決定，server 只以框數與數字限制它；所以不需要放寬任何規則。

### 花費

- 每個概念一張 SAM 影像（USD 0.0025），概念仍 8 個：**一個照片 proposal 的 SAM 至多 USD 0.02**（與之前相同；每個概念呼叫前預留 3 張＝USD 0.0075）。
- Judge（現在框找得到時才會執行）：T-055 live 的 count 6,661 下預留 3 × USD 0.044 ≈ **USD 0.132**；實際以約 6,900 input token（photo-0.2.0 多兩句）與 Muse Spark 文字 smoke 的 2,515 output token 推估約 **USD 0.02–0.03**（照片上還沒有量過）。
- 一個照片 proposal 約 **USD 0.04–0.05**，同時持有至多約 USD 0.155；smoke 的上限 USD 1。

### Live session 的指令（本 session 沒有執行）

在有 key 的 session、repository 根目錄、依序執行；每次之後 commit report（`checks/ai-evals/live-smoke-photo-*.json`）與 `checks/ai-evals/live-spend.jsonl`。

(a) 探索（只在 smoke；6 個概念）：

```sh
AI_PROVIDER=meta AI_POLICY_APPROVAL=H-029 AI_MODEL=muse-spark-1.3 GEMINUS_AI_LIVE=on \
AI_SMOKE_PHOTO_CONCEPTS="rectangle,box,square,arrow,line,bar" make ai-smoke-photo-live
```

預期花費：SAM 6 × USD 0.0025 = USD 0.015；有框時 judge 約 USD 0.02–0.03（預留約 USD 0.133）→ 約 **USD 0.035–0.045**，沒有框時 USD 0.015。Report 的 `segmenter.by_concept` 給每個概念的框數與尺寸（站的方框約 0.10–0.16 × 0.19–0.23，輸送帶約 0.08 × 0.03，整張圖 1 × 1）；全部合計超過 64 個框時 server 回 invalid（judge 不執行），以 by_concept 判斷哪個措辭太廣。可以換清單再跑（每次都記錄）。

(b) 新的詞彙（pack 的 8 個概念；**不要**設 `AI_SMOKE_PHOTO_CONCEPTS`）：

```sh
AI_PROVIDER=meta AI_POLICY_APPROVAL=H-029 AI_MODEL=muse-spark-1.3 GEMINUS_AI_LIVE=on make ai-smoke-photo-live
```

預期花費：SAM 8 × USD 0.0025 = USD 0.02；judge 約 USD 0.02–0.03 → 約 **USD 0.04–0.05**（同時持有至多約 USD 0.155）。若 (a) 顯示 `rectangle` 或 `arrow` 找不到而別的措辭找得到，先改 pack 的概念（並在 D-131 (h) 再記一次調整）再跑 (b)。

### Mutation（`scripts/mutate.py`；複本在自己的 `scratchpad/t056c2-mut/`，`git archive` 203e15e 之前的 HEAD d03f618）

27 個 mutant，**27 killed、0 survived、0 INVALID**（`mutants.log`、`mutants.json`；每組測試先在未改的複本上 exit 0；結束後 `diff -rq` internal、cmd、scripts、prompts 與 branch 相同）：L1（延遲寫到複本，即原本的 bug）；C1–C5（CheckConcepts 的數量、字元、重複、長度，client 不用它）；O1–O14（空項目略過、Main 不先檢查、Run 不檢查、覆寫被忽略、沒有標示覆寫、flag 蓋過變數、`-concepts` 沒有 `-smoke-photo`、寬高對調、小數 2 位、沒有 by_concept、summary 沒寫覆寫、不讀變數、frame 外的框也算尺寸、worker 提到變數）；V1–V6（舊的概念、沒有 arrow、英文規則沒有那句、template 版本沒升、template 沒有那句、demo 腳本的清單不同）；R1（步驟多一個也過）。

### 指令與 exit code

（Go 1.27.3、`GOTOOLCHAIN=local`；Python `/home/user/Geminus/.venv/bin/python`；DB 只在一次性 cluster（socket `/var/lib/postgresql/geminus-throwaway-hkb6`、port 55450）上本任務新建的 `dt_test_t056c2a`，最後 DROP；沒有啟動或停止任何 cluster。）

| 指令 | exit | Log | 結果 |
|---|---|---|---|
| 紅／綠：`go test -run TestASegmentationsLatencyIsReturned ./internal/ai/provider/` | 1／0 | `red-latency.log`、`green-latency.log` | 紅：4 種情況的延遲都是 0 |
| 紅／綠：覆寫的測試（provider、aievals、aiapproval） | 1／0 | `red-override.log`、`green-override.log`、`green-size-outside.log` | 紅：缺 `PhotoConfig.Concepts` 等名稱（build 失敗） |
| 紅／綠：詞彙的測試（electronicsline、aicontext、generator、aievals） | 1／0 | `red-vocabulary.log`、`green-vocabulary.log` | 紅：舊清單；smoke 在形狀的 fake 上重現 live（needs_input、`no_boxes` 1、USD 0.02） |
| `go run ./scripts/gen-photo-demo`（重新產生 fixture） | 0 | `gen-photo-demo.log` | 只有兩個 `*.segments.json` 的 `concepts` 改變 |
| mutation（`scripts/mutate.py`） | 0 | `mutants.log`、`mutants.json` | 27／27 killed |
| `gofmt -l cmd internal schemas scripts prompts` | 0（空） | `gofmt.log` | |
| `go vet`（改動的 package：`./internal/ai/... ./internal/aievals/... ./internal/config/... ./internal/proposals/... ./internal/domainpack/... ./internal/contracttest/ ./internal/audit/ ./internal/api/ ./scripts/gen-photo-demo/ ./prompts/ ./cmd/ai-eval-live/ ./cmd/worker/`） | 0 | `vet.log` | |
| `go test -count=1`（同上） | 0 | `test-changed.log` | 28 個 package ok（DB 測試在此 skip，由 integration-test 跑） |
| `go test -race -count=1 -timeout 30m`（同上） | 0 | `race-changed.log` | 28 個 package ok，沒有 data race |
| `go run ./scripts/gen-photo-demo -check` | 0 | `gen-photo-demo-check.log` | committed 的 fixture 可重現 |
| `make backend-test "GOFLAGS=-count=1 -timeout=30m"` | 0 | `backend-test.log` | gofmt、`go vet`、84 個 package ok、12 個沒有測試檔、0 FAIL |
| `make architecture-check` | 0 | `architecture-check.log` | `PASS architecture: 96 packages, 26 rules, 16 build configurations` |
| `make ai-eval` | 0 | `ai-eval.log` | fake corpus 的 context gate 與 server review |
| `make integration-test`（新建的 `dt_test_t056c2a`） | 0 | `integration-test.log` | `make db-test`（migration up／down／up 與全部 SQL 測試）＋ `make db-go-test`（`-race -p 1`、`GEMINUS_REQUIRE_DB_TESTS=1`）：38 個 package ok，沒有 FAIL 或 SKIP（含 `internal/api` 的照片測試：`photo-0.2.0`、新的 fixture） |
| `make spec-check PYTHON=/home/user/Geminus/.venv/bin/python`（驗證時，與本節寫完後再一次） | 0、0 | `spec-check.log`、`spec-check-final.log` | contracts、planning、docs、SQL inventory、reference math、decisions、TS contracts、hashes、DB tests、deploy 都 PASS；`checks/*-check.json` 沒有改變（不含官方完整 OpenAPI validator：NOT_RUN_UNAVAILABLE） |
| `dropdb dt_test_t056c2a`；`SELECT count(*) FROM pg_database WHERE datname LIKE 'dt_test_t056c%'` | 0；0 | — | 已刪除；沒有啟動或停止任何 cluster |

### NOT RUN

- 任何 live 呼叫：上面 (a)、(b) 由 live session 執行；SAM 3.1 對 `rectangle`、`arrow` 的實際回答、judge 在照片上的 count 與計費、judge 回答的 `model` 字串。
- `make e2e-test`（依指示）；`make frontend-test`／`make frontend-build`（前端沒有改；fixture 的改動只在 `testdata/photos/*.segments.json` 的 `concepts`，web 的測試不讀它）。
- 整個 repository 的 `make backend-race`：只跑了改動的 package 的 `-race`（見上表）。
- 真實照片：H-029 只用 demo 影像。

### 問題

1. **箭頭的框計入框數**：照片規則（D-132 (a)）以全部的框數限制新步驟，`arrow` 與既有的 `conveyor belt` 一樣計入；6 個方框加 4 個箭頭時規則允許至多 10 個新步驟。若要「每個新站都對應一個站的框」，要讓 pack 標出哪些概念是站、規則只數那些（比現在嚴，但是 D-132 的修改，需要決定）；沒有做。
2. **`rectangle` 在真實照片上**：可能找到很多長方形的東西（面板、螢幕），合計超過 64 個時 server 回 invalid（安全的失敗，不放寬）。目前只用 demo 影像（H-029）；之後用真實照片時要再看。
3. **去掉的兩個概念**：照片中的燒機室與人工站（除錯、重工）不再有自己的概念。Demo 不受影響；要保留的話需要第 9 個概念（T-055 的上限 8，D-123）或換掉別的。
4. **Fixture**：committed 的 fake 回答仍以機台概念找到 demo 的各站（不是 live 會給的形狀）；形狀的回答由 `ShapeSegments` 在測試中產生。Live 探索之後，要把 fake 的 demo 回答換成實際觀察到的形狀嗎？
5. **`photo-0.2.0`** 還沒有任何 live model 看過。

### Commits（`t056-concepts`）

| Commit | 內容 |
|---|---|
| 1798fec | segmenter 的 `latency_ms` 修正與測試 |
| a614a1f | smoke 的 `-concepts`／`AI_SMOKE_PHOTO_CONCEPTS`、`provider.CheckConcepts`、report 的 `concepts_overridden`、`concept_list`、`by_concept` |
| 07c4726 | 平面圖的概念、`photo-0.2.0`、形狀的框 → judge → 審查的測試 |
| 828a089 | D-131 (h)、ELECTRONICS_LINE §13、AI_SPEC |
| d03f618 | frame 外的框沒有尺寸（測試） |
| 203e15e | mutation 的證據與 `verify.sh` |
| （本節的 commit） | 本節、驗證的 log、D-131 (h) 的花費估計更正 |

## Coordinator 驗證：SAM 概念調整（2026-10-27）

`t056-concepts` 的八個 commit（1798fec..11cc4a9）cherry-pick 到主線（89bdfdb 之後），沒有衝突。

指令與結果（evidence：`checks/evidence/2026-10-27-T-056concepts-coordinator/`，`exit-codes.txt` 依執行順序；HEAD e4adcfc；go1.27.3；DB 測試在暫時的 cluster（`/var/lib/postgresql/geminus-throwaway-hkb6`，port 55450，container 重啟後由 coordinator 重新啟動））：

| 指令 | exit | 說明 |
|---|---|---|
| `make spec-check` | 0 | |
| `make backend-test "GOFLAGS=-count=1 -timeout=30m"` | 0 | 84 個 package ok |
| `make architecture-check` | 0 | 96 packages, 26 rules |
| `make ai-eval` | 0 | fake provider |
| `go run ./scripts/gen-photo-demo -check` | 0 | demo 的 fixture 可重現 |
| `go test -race -count=1 -timeout 30m`（ai、aievals、domainpack、proposals、prompts、cmd） | 0 | 26 個 package ok |
| `make integration-test`（新的 dt_test_hkb12c，跑完刪除） | 0 | 38 個 package ok，FAIL 或 SKIP 共 0 個 |

NOT RUN：任何 live 呼叫（新的詞彙與 `photo-0.2.0` 尚未經 live model）；e2e 與前端（沒有改 UI）；完整的 `make backend-race`。

Coordinator 對本節問題的處理：
1. Arrow 的框計入步驟上限：維持 D-132 (a)（server 仍有上限）；只算站點概念的較嚴格規則比賽後再考慮。
2. 真實照片上 `rectangle` 可能匹配很多東西：只用 demo 圖片（H-029、H-035）；超過 64 個框時 server 回 `invalid`，是安全的失敗。
3. Burn-in 與手工站沒有自己的概念：demo 沒有這兩種；比賽後再考慮第 9 個概念（需改 D-123）。
4. 提交的 fake 回答仍以機台概念找站：live 探索後，把 fixture 換成 SAM 實際回的形狀（後續）。
5. 新的 prompt 版本 `photo-0.2.0`：下一步由有 key 的 session 先跑探索（`AI_SMOKE_PHOTO_CONCEPTS`），再以新詞彙跑一次 `make ai-smoke-photo-live`。

## Live 執行：照片 — SAM 概念探索（2026-10-27）

依 coordinator 的下一步（「SAM 概念調整」與其 coordinator 驗證，HEAD 138d863），在有 key 的 session 執行 (a) 探索一次。Key 的規則同前（只看名稱、不在 argv）；`AI_VISION_*` 沒有設；沒有 curl、沒有經 API 的照片 proposal、Railway 沒有操作、沒有提高任何上限。證據：`checks/evidence/2026-10-27-T-056-live/smoke-photo-explore.log`、`environment.txt`。不記 prompt、回答文字、影像 bytes、框座標、key。

### (a) 探索

| 指令 | exit | 結果 |
|---|---|---|
| `AI_PROVIDER=meta AI_POLICY_APPROVAL=H-029 AI_MODEL=muse-spark-1.3 GEMINUS_AI_LIVE=on AI_SMOKE_PHOTO_CONCEPTS="rectangle,box,square,arrow,line,bar" make ai-smoke-photo-live`（02:15:43Z–02:16:12Z） | **1**（`ai-eval-live: exit 1`；make 為 2） | run `01M5XJ8Q4RJCQBESJZWZPG4BPJ`：status **failed**，code **`AI_OUTPUT_TRUNCATED`**，server risk none；gates `pipeline_failed` **1**、`needs_input_kept_changes` 0、`ready_without_server_validation` 0；observations 全部 0（含 `billed_input_over_reserved`、`frame_not_the_image`、`images_processed_not_one`、`no_boxes`）；prompt template `photo-0.2.0` |

- **Report**：`checks/ai-evals/live-smoke-photo-2026-10-27T021543Z-01M5XJ8Q4RJCQBESJZWZPG4BPJ-muse-spark-1.3.json`。
- **Spend record**：`open` kind `smoke-photo`、cap 1,000,000,000；`close` settled＝committed 59,302,250 nano-USD、result **`gate_failed`**。
- **Segmenter（SAM 3.1）**：6 個概念皆回答，`images_processed` 各 1，frame **`1200x520`**（等於影像），latency 3,882 ms（延遲的修正在 live 有效），30 個框，花費 15,000,000（USD 0.015）。每個概念（`segmenter.by_concept`，尺寸為相對於 frame 的寬 × 高）：

| 概念 | 框 | 相對尺寸 |
|---|---|---|
| `rectangle` | **6** | 4 × 0.10×0.19、0.142×0.231、0.158×0.231（＝6 個站的方框：AOI、ICT 1、ICT 2、FCT 為 0.10×0.19，SMT、PACK 較大） |
| `box` | 6 | 與 `rectangle` 相同的 6 個尺寸 |
| `square` | 7 | 同樣 6 個，加 1 個 0.101×0.115 |
| `arrow` | **4** | 0.073–0.085 × 0.031–0.035（＝4 段輸送帶的箭頭） |
| `line` | 7 | 2 個 0.014×0.308（ICT 兩側的直條）、1 個 0.013×0.01、4 個約 0.063–0.085 × 0.031–0.035（與 arrow 相同的位置） |
| `bar` | 0 | — |

  結論：pack 的新詞彙 `rectangle`、`arrow` 在 demo 平面圖上**找得到**，數目正好是 6 個站與 4 段流向（D-131 (h) 的預期）。
- **Judge（Muse Spark）**：有執行。count_tokens **7,793**，預留 input 8,573（＋10%），**計費 input 7,589（計費／count 0.974，沒有超過 count，也沒有超過預留）**；output **8,192 ＝ `max_tokens` 上限** → `stop_reason` 為截斷，server 以 `AI_OUTPUT_TRUNCATED` 結束（一個呼叫，沒有重試）。預留 136,596,750，花費 44,302,250（7,589 × 1,250 + 8,192 × 4,250）。`served_models` 為 `muse-spark-1.3` 與 `sam-3.1`（judge 回答的 model 字串正好是 `muse-spark-1.3`）。H-037 的未決事項：demo 的照片 request 計費 input **低於** count（約 −2.6%），10% 的 margin 足夠。
- **花費**：**USD 0.05930225**（cap 1.00）；同時持有的最大值 151,596,750。今天（UTC 2026-10-27）的 record：0.02 ＋ 0.05930225 ＝ USD 0.07930225。

### 意外的結果，以及為什麼沒有執行 (b)

1. **Judge 的 output 用完 8,192 tokens 被截斷**（`AI_OUTPUT_TRUNCATED`）。D-121 (f) 已預見：Muse Spark 的推理 tokens 計入 `max_tokens`，預設 8192 可能使長的推理截斷。這次的 SEGMENTS 有 30 個重疊的框（6 個概念彼此重複），可能使推理更長；但文字 smoke 的 output 也已有 2,515 tokens，照片的 context（system 12 KB 以上、影像、框）多半更長。
2. **(b) 沒有執行（NOT RUN）**：coordinator 的條件（`rectangle`／`arrow` 有框）成立，但 (a) 的 judge 以管線失敗結束，這是之前約定的「第一個意外的結果就停」；(b) 用同一個 judge、同一個 `max_tokens`，很可能同樣截斷（約 USD 0.06）。是否繼續由 coordinator／owner 決定。可選：(i) 照原計畫直接跑 (b)（10 個框而非 30 個，看是否仍截斷）；(ii) 先提高照片 judge 的 `MaxOutputTokens`（例如 16,384；預留與 output 的價格跟著變，屬 D-121 (f)／D-111 的設定，需 reviewed change）；(iii) 縮短照片 prompt 或要求更短的回答。

### NOT RUN

- (b) pack 詞彙的 smoke（見上）；照片 proposal 為 ready 的 live 結果；judge 對新詞彙的回答。
- 經 API／worker／DB 的照片 proposal、人工批准、Railway。

`make spec-check PYTHON=/home/user/Geminus/.venv/bin/python`（加上本節之後、commit 之前）：**exit 0**（`spec-check-explore.log`）。

### (b) Pack 的詞彙（coordinator 選 (i)）

Coordinator 看過 (a) 之後選 (i)：照原計畫以 pack 的 8 個概念跑一次（不設 `AI_SMOKE_PHOTO_CONCEPTS`，沒有其他改動、不重試、不改上限）。

| 指令 | exit | 結果 |
|---|---|---|
| `AI_PROVIDER=meta AI_POLICY_APPROVAL=H-029 AI_MODEL=muse-spark-1.3 GEMINUS_AI_LIVE=on make ai-smoke-photo-live`（02:17:59Z–02:18:30Z，HEAD 6b07d7b） | **1**（make 為 2） | run `01M5XJCVYRB05MV25ZJ5R6Q8V7`：status **failed**，code **`AI_OUTPUT_TRUNCATED`**（**再一次截斷**），server risk none；gates `pipeline_failed` **1**、其他 0；observations 全部 0；prompt template `photo-0.2.0` |

- **Report**：`checks/ai-evals/live-smoke-photo-2026-10-27T021759Z-01M5XJCVYRB05MV25ZJ5R6Q8V7-muse-spark-1.3.json`（`smoke-photo-vocabulary.log`）。
- **Spend record**：`open` kind `smoke-photo`、cap 1,000,000,000；`close` settled＝committed 63,224,750 nano-USD、result `gate_failed`。
- **Segmenter**：8 個概念皆回答，`images_processed` 各 1，frame `1200x520`，latency 5,342 ms，**10 個框**：`rectangle` 6（4 × 0.10×0.19、0.142×0.231、0.158×0.231）、`arrow` 4（0.073–0.085 × 0.031–0.035）；`smt line`、`aoi machine`、`ict tester`、`fct tester`、`packing station`、`conveyor belt` 皆 0（與第一次 live 相同）。花費 20,000,000（USD 0.02），預留 60,000,000。
- **Judge**：count_tokens **6,931**，預留 input 7,625，計費 input **6,727**（計費／count 0.971）；**output 8,192 ＝ `max_tokens`**，截斷，一個呼叫。預留 133,041,750，花費 43,224,750（6,727 × 1,250 + 8,192 × 4,250）。`served_models`：`muse-spark-1.3`、`sam-3.1`。
- **花費**：**USD 0.06322475**；同時持有的最大值 153,041,750。今天（UTC 2026-10-27）的 record 合計：0.02 ＋ 0.05930225 ＋ 0.06322475 ＝ **USD 0.142527**。

結論：框數由 30 降為 10、input 由 7,589 降為 6,727，judge 仍用滿 8,192 個 output token；截斷與框數無關，是照片 judge 的推理／回答本身超過預設的 `max_tokens`（D-121 (f) 的已知風險）。依 coordinator 的說明，`max_tokens` 的調整由 coordinator 以 reviewed change 進行；本 session 沒有改任何程式或上限。count 的 margin 兩次都足夠（計費 input 比 count 少約 3%）。

`make spec-check PYTHON=/home/user/Geminus/.venv/bin/python`（加上本小節之後、commit 之前）：**exit 0**（`spec-check-vocabulary.log`）。

## 照片 judge 的輸出上限（live 截斷之後，2026-10-27，coordinator）

**原因。** 上面的 (a)、(b) 兩次 live 中，照片 judge（Muse Spark）都用滿了 `max_tokens` 8,192 而截斷（`AI_OUTPUT_TRUNCATED`）。截斷與框數無關：30 個框、10 個框都一樣。文字 proposal 的 output 只有 2,515。

**依據**（dev.meta.ai，2026-10-27 讀取）：
- `/docs/reasoning`：「Reasoning tokens count toward your output-token budget … and toward billed output tokens」；不送 effort 時，推理的深度由模型決定。
- `/docs/api-reference/messages/schemas`：`AnthropicOutputConfig.effort` 為 low／medium／high／xhigh／max；`max_tokens` 為 ≥1 的整數（沒有列出上限）；`AnthropicUsage.output_tokens_details.thinking_tokens` 是選填欄位。

**修法**（最小變更）：
- `provider.ImageMaxOutputTokens` = 32,000（= `BudgetCaps.MaxOutputTokens`）。有圖片的 request 用它，文字 request 維持 budget 的 8,192。預留自動跟著改，因為 meter 用的是 `req.MaxOutputTokens`。
- `provider.Usage.ThinkingTokens`（`thinking_tokens,omitzero`）：Meta adapter 解析 `output_tokens_details.thinking_tokens`，值為負數或大於 `output_tokens` 的回答一律拒收。
- 照片 smoke 報告的 judge 部分加上 `thinking_tokens` 與 `max_output_tokens`。
- `output_config.effort` **先不送**：它會影響回答的品質。若 32,000 仍然截斷，屬於行為變更，先問 owner 再考慮 `low`。
- 文件：D-121、D-131 的說明、ADR-030 的 addendum、`planning/decisions.json` 的 D-121。`decisions.json` 本來就沒有 D-130–D-132，這次沒有處理。

**預留。** 以 demo 的 live count 6,661 計算，judge 每次照片 proposal 預留約 USD 0.435（3 個 attempt），加上 SAM 的預留 USD 0.06，在 organization 日上限 USD 5 之下約可做 10 次。

**放寬一個門檻（容量數字，不是安全 assertion）。** `TestADemoPhotoProposalFitsTheOrganizationCap` 在使用刻意偏高的 count（40,147）時，門檻從「至少 10 次」改為「至少 7 次」；用 live 的 count 時仍是 10 次。

**Red → green。**
- Red：測試先寫好，常數未定義，所以編譯失敗（`red.log`）。
- Green：21 個 package ok（`green.log`）。
- Mutants 4/4 killed（`mutants.txt`）：拿掉圖片的上限、拿掉 thinking 的對應、拿掉 thinking 的上界、拿掉報告欄位，各自都讓測試失敗。

**指令與 exit code**（e44ccbc 加上這次的修改；go1.27.3、`GOTOOLCHAIN=local`；WSL2；evidence：`checks/evidence/2026-10-27-T-056-judge-budget/`）：

| 指令 | exit | 說明 |
|---|---|---|
| `make spec-check` | 0 | |
| `make backend-test "GOFLAGS=-count=1 -timeout=30m"` | 0 | 84 個 package ok |
| `make architecture-check` | 0 | |
| `make ai-eval` | 0 | |
| `go test -race -count=1 -timeout 30m`（ai、aievals、proposals、cmd） | 0 | 21 個 package ok |
| `make integration-test`（一次性 PostgreSQL 16.15 instance，與 CI 相同：trust、`dt_admin`、localhost:54329，用完刪除） | **0** | 38 個 package ok，沒有 FAIL 或 SKIP |

之前在本機 system cluster 上嘗試過幾次 integration-test，都是環境問題，沒有一次是程式的結果，所以都不算數：
- 有三次在 Go 測試開始之前就停了：PATH 上沒有 `go`；PATH 含空白被切開；`/usr/local/go` 是 1.25.5。這三次的 log 沒有保留。
- 有一次經 Unix socket 連線，peer authentication 拒絕了測試的 login，`internal/tenant` 因此卡住，最後由 coordinator 停止（exit 143；`integration-test-system-cluster-peer-auth.log`）。這次留下的資料庫與 role 都已刪除。

**獨立審查**（H-029，一輪，reviewer 是一個新開的 agent）：**ACCEPT**，另有 3 個 Medium 與幾個 Low。Reviewer 沒有 shell 可用，所以沒有自己跑測試，結論是讀程式與 evidence 得出的。它確認了：
- 預留和送出的 `max_tokens` 是同一個變數，沒有少預留的路徑。
- `count_tokens` 與這個上限無關。
- 10→7 的門檻是容量數字（5e9 ÷ 633,607,500 = 7.89），安全的那一半（`before != 1`）沒有變。
- Thinking 的內容仍然不讀，只留數字。

依 H-029 修正 Medium，由 coordinator 驗證：

| # | 審查的發現 | 修正 |
|---|---|---|
| M1 | 有圖片的 request 不理會部署的 `AI_MAX_OUTPUT_TOKENS`，文件仍寫「這是 provider 的 `max_tokens`」 | 刻意如此：預設的 8,192 正是截斷的原因。沒有另加一個部署設定，而是在 `budget.go`、runbook 的 budget 列、D-121、D-131、ADR-030 寫明這個例外。花費的上限仍是 D-110 的日上限 |
| M2 | attempt timeout 90 s 短於 32,000 個 token 所需的時間（live 速度約 340 token/s → 約 94 s）。可能以 `AI_TIMEOUT` 結束，並以整個預留結算兩個 attempt | 有圖片的 request 的 attempt timeout 與整個呼叫的 timeout，依同一比例（32,000 ÷ budget 的 `max_tokens`）放大，至多 `BudgetCaps.Timeout` 300 s（`Budget.ImageTimeout`）：預設的 90 s／120 s 都成為 300 s。Worker 的 lease 由 heartbeat 續期，不限制呼叫的長度。`TestTheCountKeepsTheCallsDeadline` 的輸入由 300 ms 改為 100 ms（放大後是 390 ms，測試的 1.5 s 上限不變） |
| M3 | 報告會把 provider 沒有送的 `thinking_tokens` 記為 0 | `provider.Usage.ThinkingTokens` 與報告的欄位改為 `*int64`，沒有回報時為 null。測試新增：沒有回報 → nil；負值 → 拒收 |

Low 延後（H-029）：
- 不合理的 `thinking_tokens` 會讓整個回答作廢。
- 測試偏薄：沒有非零 thinking 的照片 smoke 測試。
- 報告的 `MaxOutput` 是常數，沒有記實際送出的值。
- 文件中的舊數字：`photo.go` 的註解**已順手改正**；ADR-030 與 D-131 前段的舊數字由 addendum 取代，沒有在原處標註。
- 32,000 是否會被 Meta 以 400 拒收，要 live 才知道。

修正的 mutants 6/6 killed（`mutants-review-r1.txt`），各自拿掉的部分如下：
- 圖片的 timeout 全部；
- 只拿掉整個呼叫的 timeout；
- 300 s 的上限；
- 沒有回報時記為 0；
- 負值的檢查；
- 報告記 0。

**修正後的指令與 exit code**：`checks/evidence/2026-10-27-T-056-judge-budget/review-r1/exit-codes.txt`（結果見下一節）。

**NOT RUN**：live 照片 smoke（以 32,000 的上限）；e2e。

### 審查修正之後的指令與 exit code（最後的程式碼；`review-r1/exit-codes.txt`）

| 指令 | exit | 說明 |
|---|---|---|
| `make spec-check` | 0 | |
| `make backend-test "GOFLAGS=-count=1 -timeout=30m"` | 0 | 84 個 package ok |
| `make architecture-check` | 0 | |
| `make ai-eval` | 0 | |
| `go test -race -count=1 -timeout 30m`（ai、aievals、proposals、cmd） | 0 | 21 個 package ok |
| `make integration-test`（新的一次性 PostgreSQL 16.15 instance，與 CI 相同，跑完刪除） | 0 | 38 個 package ok，沒有 FAIL 或 SKIP |
| Mutants（`mutants-review-r1.txt`） | 6/6 killed | |

M1–M3 由 coordinator 驗證；依 H-029 可以執行 live 的照片 smoke。

## Live 執行：照片 smoke，judge 上限 32,000（2026-10-27，coordinator）

- HEAD a30d488（審查 ACCEPT，Medium 修正後）。
- 只把 `GEMINUS_MUSE_API_KEY` 載入這一個指令的環境（不在 argv、不印出）；`AI_VISION_*` 沒有設。
- 只跑一次，沒有重試，沒有提高 cap。
- 不記 prompt、回答文字、影像、框的座標或 key。
- Evidence：`checks/evidence/2026-10-27-T-056-judge-budget/live/`。

| 指令 | exit | 結果 |
|---|---|---|
| `AI_PROVIDER=meta AI_POLICY_APPROVAL=H-029 AI_MODEL=muse-spark-1.3 GEMINUS_AI_LIVE=on make ai-smoke-photo-live` | 第一次 2 | **REFUSED，沒有任何 provider 呼叫**：`.env` 那一行以 CR 結尾（Windows 的行尾），key 被判為 invalid。移除行尾的 CR 後重跑（`smoke-photo-refused-crlf.log`） |
| 同上（11:52:33Z–11:53:06Z） | **0** | run `01M5YK8XZ88D7PSECX9E7DWG0Y`：status **ready**、server risk **medium**、新的 process 步驟 4 個；gates 全部 0、observations 全部 0；`complete` true |

- **Report**：`checks/ai-evals/live-smoke-photo-2026-10-27T115233Z-01M5YK8XZ88D7PSECX9E7DWG0Y-muse-spark-1.3.json`。
- **Spend record**：`open`／`close`，settled＝committed 59,546,500 nano-USD，result `complete`。
- **Segmenter（SAM 3.1）**：8 個概念都有回答，`images_processed` 各 1；10 個框（`rectangle` 6、`arrow` 4，其餘 0，與前兩次相同）；延遲 9,392 ms；花費 USD 0.02。
- **Judge（Muse Spark）**：
  - count_tokens 6,892，預留 input 7,582，計費 input 6,688（計費／count 0.970）。
  - **output 7,338，其中 thinking 5,313**（`max_output_tokens` 32,000）；一個呼叫，延遲 21,446 ms。
  - 預留 436,432,500，花費 39,546,500。
  - `served_models`：`muse-spark-1.3`、`sam-3.1`。
- **花費**：**USD 0.0595465**（cap 1.00）。今天（UTC 2026-10-27）的 record 合計：0.142527 ＋ 0.0595465 ＝ **USD 0.2020735**。

**解讀。**
- 這一次的 output（7,338）**低於** 8,192，所以單看這一次，舊的上限也可能不會截斷。前兩次則都用滿 8,192，可見推理長度每次差異很大。32,000 留有約 4 倍的空間。
- 這次的速度：7,338 個 token 用了 21.4 s。
- Meta 沒有以 400 拒收 `max_tokens` 32,000（審查 Low 的未決事項已確認）。
- `output_config.effort` 仍然不送。

**NOT RUN**：經 API／worker／DB 的照片 proposal、人工批准、Railway 上的照片流程（部署後確認）。

## Live：成功的照片提案寫 audit 時超過欄位上限（2026-10-27／28，coordinator）

**怎麼發現的**：在 live 網站上，以評審帳號在空產線上做照片提案。空產線是一個模型的 version 2，由 coordinator 經網站的 API 建立：以 D-020 的規則（每個元素先 `test` 它的識別欄位）把 draft patch 成 `testdata/photos/bare-line.json`，再發佈。

結果 SAM 找到 10 個框，judge 也回答了，但 job 以 `INTERNAL` 失敗：`audit: proposal.generate: metadata: more than 32 fields`。job 把它當成可重試的錯誤，**重試了 3 次**，每次都重新呼叫 SAM 與 judge：花費 3 × (USD 0.02 + 約 0.035–0.05)，約 **USD 0.19**。

**原因**：
- `audit.MaxFields` = 32。
- `proposal.generate` 的 catalogue 宣告了 **34** 個欄位：provider 呼叫 17、照片 10、狀態與風險 7。
- 被拒絕的提案沒有 4 個風險欄位，所以剛好過得去，這也是之前那次 invalid 的照片提案沒有出錯的原因。
- 上午的 smoke 不寫 DB，integration test 使用假 provider，所以都沒有抓到。

**修正**：
- `MaxFields` = 40：不小於任何 action 宣告的欄位數。每個欄位的型別與格式仍然逐一檢查，沒有放寬。
- 新增 `internal/audit/catalogue_internal_test.go` 的 `TestEveryDeclaredFieldFits`：catalogue 裡任何一個 action 宣告的欄位數超過上限，測試就失敗。
- `TestShapes` 的「超過上限會被拒絕」改用 `MaxFields+1`（原本寫死 33），檢查的強度不變。

**驗證**（`checks/evidence/2026-10-27-audit-max-fields/`）：
- Red：修正前 `TestEveryDeclaredFieldFits` 失敗（「proposal.generate declares 34 fields」）。

| 指令 | exit | 說明 |
|---|---|---|
| `go test ./internal/audit/... ./internal/proposals/...` | 0 | 5 個 package ok |
| `make spec-check` | 0 | |
| `make backend-test "GOFLAGS=-count=1 -timeout=30m"` | 0 | 84 ok |
| `make architecture-check` | 0 | |
| `go test -race`（audit、proposals、worker、cmd） | 0 | 11 ok |
| `make integration-test`（一次性 PostgreSQL 16.15，與 CI 相同） | 0 | 38 ok，沒有 FAIL 或 SKIP |

**仍然存在（Low，延後）**：audit 寫入失敗屬於可重試的 `INTERNAL`，所以 job 重試時會重新呼叫 AI，花費會重複。比較好的做法是：AI 回答之後才發生的失敗，不要再呼叫 AI。

**NOT RUN（寫入本節時）**：修正後在 live 上的照片提案（部署後執行）。
