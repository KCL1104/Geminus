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
