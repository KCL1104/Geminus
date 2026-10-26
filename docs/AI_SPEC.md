# AI Modeling / Scenario / Analyst Spec

SOURCE：原報告 pp. 23–28、32–33、41–46。工程補充（皆已批准）：D-002 / D-004 / D-009 / D-010 / D-011 / D-014 / D-016 補足 server envelope、狀態、scenario persistence、可執行安全規則。Provider 已定為 Anthropic Claude API（H-001、D-018）；示範用 model 為 Claude Opus 5.5，開發期每月 100 美元上限並另設每日上限，示範期只送 synthetic 資料與使用者輸入的文字（H-018）。T-053 把 H-018 記入 `internal/config/aiapproval`，並實作 live AI 的開啟條件與程式內的花費上限（§2、§10；D-110–D-115 PROPOSED）；Anthropic 的資料保留規則（來源與原文）見 [SECURITY §7](SECURITY.md)。Owner 已確認兩個 provider organization 的資料保留安排（standard、沒有 ZDR、兩者相同；H-026，D-113 的問題），H-018 的 record 已完整；live AI 只在部署設定 `AI_MODEL`、兩把 key 與 `GEMINUS_AI_LIVE=on` 時開啟，live smoke 與 live eval 尚未執行。

## 1. Authority boundary

AI 是 constrained workflow，不是 autonomous agent framework。它可以讀 sanitized context、提出 ModelChangeProposal / Scenario proposal、提出 mapping、分析 supplied aggregates；**不能修改 Twin、publish、approve、run simulation、任意 SQL/code、呼叫 production systems、取得其他 tenant 或自行決定 waiver**。人類 approval 由 application server 辨識 authenticated user action，不由 prompt 中的「我已同意」代替。

roles 與原報告一致：Import Mapper、Model Architect、Scenario Designer、optional Critic/Validator、Simulation Analyst，全部沒有 write authority。Application Server 只在授權且 revalidation 成功後執行 bounded canonical effect。AI component 不注入 ModelCommitter / DB transaction handle / write tool；provider response 僅是 untrusted data。

## 2. Provider abstraction / deployment

```go
// PROPOSED refinement of the report's provider interface:
// provider output has NO trusted IDs, state or approval fields.
type LLMProvider interface {
    GenerateModelProposal(context.Context, ProposalContext) (ProposalOutput, error)
    ProposeImportMapping(context.Context, MappingContext) (MappingOutput, error)
    AnalyzeSimulation(context.Context, AnalysisContext) (AnalysisOutput, error)
}
```

ProposalContext 的 mode 由 server 固定為 edit/scenario；可以用同一 provider method 配不同 prompt。Domain 不 import provider SDK types。Structured output 是第一層約束，不取代 canonical 2020-12 validator；若 provider 僅支援 schema subset，由 adapter 做明確投影，server 仍驗完整 schema，不可為適應 provider 放寬 authority。

未選 provider/model、token budget 或 data policy 時，live AI 明確 disabled 並回 AI_PROVIDER_NOT_CONFIGURED / AI_POLICY_NOT_APPROVED。development fake provider 只能顯式配置；不得在 production timeout 時偷偷回 synthetic answer。不得 hard-code「最新 model」；用 app-owned eval corpus 比較後決定。

T-024 實作（[ADR-027](adr/ADR-027-ai-provider-adapter.md) ACCEPTED（H-019）；不批准任何 live 呼叫）：`internal/ai/provider` 的 `LLMProvider` 收 sealed `Prompt`（`internal/ai/aicontext` 產生），回傳 typed output 與 audit metadata（provider、model、credential label、prompt_template_version、schema、output_locale、input／output hash、tokens、latency，無 prompt、無回答文字、無 key）。三個 mode 都經過同一個 `Client`：prompt 未 seal 或被改、role 與 mode 不符、超過 prompt 預算、同一 organization 的並行數已滿、逾時、取消，各有 typed code；回答必須是一個通過完整 2020-12 output schema 的 JSON document，否則 `invalid_output`（只留 issue path）。Mode 由 `internal/config.LoadAI` 決定：`disabled`（預設）、`fake`（只限 APP_ENV development／test）、`anthropic`（沒有 owner 的已記錄核准時回 `policy_not_approved` 且不讀 key；`AI_POLICY_APPROVAL` 必須是 build 列出的核准；T-024 時清單為空，T-053 起記錄 H-018，見下方 T-053 段落）。Live adapter 用官方 Go SDK v1.78.0，key 明確注入（不讀 `ANTHROPIC_API_KEY` 等環境預設），只在 401／402／403 換 credential，429／529／5xx 在同一 credential 重試（D-018）；只對本機 test server 測過，真實 API 呼叫 NOT RUN。Structured output 的投影（`anthropic.Project`）只移除 subset 不支援的 constraint 並列出，`oneOf` 改 `anyOf`，拒絕開放 object。

T-053 實作（D-110–D-115 PROPOSED；只對 fake 與本機 test server 測過，**沒有任何 live 呼叫**）：
- **批准紀錄**：`internal/config/aiapproval` 記錄 H-018：provider `anthropic`、model `claude-opus-5-5`（Claude Opus 5.5 的確切 model ID，取自 claude-api 參考的 model 表）、資料政策（只送 synthetic 資料與使用者輸入的文字、只服務 `modeler` 與 `scenario`（D-112）、log 不存原始 prompt 與回應）、每月 USD 100（兩個 provider organization 合計）、credential `org-a`、`org-b`、批准者 KCL1104、2026-10-20、來源 H-018；Console spend limit 由 owner 確認（H-025，2026-10-26）。兩個 organization 的資料保留安排由 owner 確認為預設的 standard、沒有 ZDR、兩者相同（H-026，2026-10-26；D-113 的問題），記入 record 的 `Retention`，所以 `Record.Gap` 為空，`aiapproval.Recorded("H-018")` 產生 token；live AI 仍要 `AI_MODEL`、兩把 key 與 `GEMINUS_AI_LIVE=on` 才開啟（D-115）。H-026 之前 `Gap` 為 `retention_unconfirmed`，live 無法開啟。日期須為 YYYY-MM-DD，arrangement 只能是 `standard` 或 `zero_data_retention`（兩個 organization 一種），否則仍為 `retention_unconfirmed`。
- **開啟條件**（D-115）：`AI_PROVIDER=anthropic`、`AI_POLICY_APPROVAL` 指名完整的 record、`AI_MODEL` 完全等於 record 的 model、該 model 有價格、`GEMINUS_AI_LIVE=on`、兩把 key 都有且不同。開關 on 而有條件不合時拒絕啟動（錯誤碼即該條件）；開關不是 on 時照常啟動，AI 呼叫回 `AI_POLICY_NOT_APPROVED`、不讀 key，`config.AI.LiveOff` 與 worker 的啟動 log 記 typed reason。`anthropic.New` 另外只接受 record 的 model 與 credential label；record 改變後 token 失效。
- **計量**：live adapter 是 `provider.Billable`，只能經 `provider.NewMetered` 的 client 呼叫（`New` 拒絕它，adapter 拒絕沒有 reservation 的 request）；client 在呼叫前預留上限、被拒即不呼叫，呼叫後以 usage 結算（§10、D-111）。Adapter 計算 request 的輸入上限（prompt 與投影 schema 的 bytes 加 4096），並計數沒有回答、可能已被計費的 attempt（連線中斷、body 讀不完、逾時、取消、讀不了的 2xx 回答）；該 credential 上有這樣的 attempt 之後，401／402／403 不再 failover，呼叫失敗（可重試），所以可能被計費的 attempt 至多 1 + retry 數（review 第 1 輪 M3、I1）。
- **Request 不變**：沒有 `thinking`（Opus 5.5 預設 adaptive，`disabled` 會 400）、沒有 sampling 參數、沒有 `inference_geo`（由 workspace 的 `default_inference_geo` 決定，是否指定由 owner 決定：D-116 PROPOSED；預留以最貴的 geo `us`（1.1 倍）計，結算依回答的 `usage.inference_geo`，沒有或不認得的以 1.1 倍計並記 warning：review 第 1 輪 M2）、沒有 `cache_control`、沒有 server-side `fallbacks`（claude-api 參考對 Opus 5.5 預設建議開啟，但 fallback 會改用未經批准的 model，H-018 只批准一個 model，ADR-027）；refusal 照舊為 `refused`。Effort 用 API 預設（Opus 5.5 為 `medium`）；`max_tokens` 包含 thinking，預設 8192 可能使長的 thinking 截斷為 `output_truncated`（live eval 會看到，`AI_MAX_OUTPUT_TOKENS` 可調，上限 32000）。

## 3. Context builder

輸入只能由已授權 repository 和 scoped result service 取出：immutable model slice + entity-ID index、完整 schema/domain rules、intent、selected entity IDs、sanitized data summary、approved limits；analyst 另接 results/statistical summary/comparison。不接受 client 自己提供的 full model / baseline metrics 作可信 authority。

所有 CSV cells、JSON metadata、model labels、user intent 文字都標示 UNTRUSTED_DATA，不能被解釋為 system/developer instruction。raw credentials、tenant secrets、無关 raw rows、其他 tenant、任意 URL、private infrastructure secrets default deny。大型 imports 只發摘要與 bounded sample，不送整份 CSV。context omission 必須可見，不能在 slice 中看不到某 reference 就假裝它不存在。

輸出語言（REQ-022）：每個 AI request 帶 `output_locale`（`en` 預設，或 `zh-Hant`），取自使用者的 UI 語言，由 server 驗證為允許值後放進 provider instruction；client 的其他文字不能改變它。只有 prose（summary、assumptions、explanation、clarifying question）依該語言輸出；ID、JSON Patch path、JSON Pointer、enum／reason code、metric path、單位一律不翻譯。Analyst prose 中的數字一律以阿拉伯數字與供應值的單位書寫，§9 的數字校驗對兩種語言都適用；無法校驗的中文數字寫法（如「四十七」）視為未校驗數字。Request contract 在 T-024／T-025 依「先改 contract」加入此欄位；eval corpus 含 zh-Hant intents（T-038）。

APPROVED D-016：新增 node/resource 的 ID 必須來自 server 提供的 allocation pool，且在同一 patch 中明確建立；不准編造外部 dataset 或引用未建立／未授權 resource。若 pool/schema/intent 不足，needs_input。server 做完整 candidate IR referential validation，不只驗 slice。

T-024 實作（`internal/ai/aicontext`，ADR-027）：每個輸入帶 organization（與 model）；有任何一個不是 request scope 的，整個 build 失敗（`cross_tenant_input`），不回部分 context。System 只有版本化 template、固定 server rules、domain pack 的靜態 `PromptContext` 與 `output_locale` instruction；user message 只有 `server_data`（ID_ALLOCATION_POOL、ALLOWED_METRIC_REFERENCES、CONTEXT_OMISSIONS）與 `untrusted_data` block，內容是跳脫過 `<`、`>`、`&` 的 JSON，資料無法關閉或偽造 block。所有 untrusted 字串與 member name 都 redact credential、URL、e-mail、IPv4、D-001 server ID 與不可見字元並計數，並受長度界限；名稱像 credential 的 member 或 import 欄位，其值整個遮蔽，看起來像角括號的字元先轉成 ASCII 再 escape，名稱遮蔽後相同的 member 列在 CONTEXT_OMISSIONS（T-024 review M2、L3、L4，細節見 ADR-027）；不送 model／version／run ID、hash、views。超出預算時依序拿掉 metadata 與 domain attributes、只完整顯示選取 entity 的鄰近範圍（其他以 `{"id","omitted":"budget"}` 佔住原 index）、不送 IR；ENTITY_INDEX 永遠列出每個 entity 與真實 index，CONTEXT_OMISSIONS 列出省略的種類、數量與 reason code；仍超過預算即 `context_too_large`。D-046 的 pool 由 `internal/ai/idpool` 產生（每種 16 個 `<kind>_<小寫 ULID>`，不與 base 衝突），存在 `ai_proposals.id_pool`（migration 0011，所有 role 都不可改），只放進 edit mode 的 context；`Pool.Check(base, candidate)` 列出不是來自同種類 pool 的新 entity ID，供 T-025／T-026 對應成 validation issue（code 待定，見 CB-40）。`output_locale` 由 `internal/ai/locale.Parse` 驗證（空值為 en；錯誤指向 `/output_locale`），`ProposalRequest`、`AnalysisRequest` 與新的 `MappingSuggestionRequest` 已加入此欄位。

## 4. Provider output / proposal 狀態機

`proposal-output.schema.json` 是 provider 輸出；`proposal.schema.json` 是 server resource。provider 只回 `status=proposed` 或 `needs_input`，沒有 `ready`、organization_id、base_version_id、validation、new_version_id、approved_by。這些由 server 綁定／計算，禁止 mass assignment。

```text
generating -> ready | needs_input | invalid | failed
ready      -> approved | rejected | conflicted
needs_input -> rejected        # 使用者補資訊要建立新的 proposal
```

approved/rejected/invalid/failed/conflicted 是本候選 workflow 的 terminal state；不在原物件上偷偷 rebase、改 patch、替換 assumptions。ready 前：schema →op allowlist→decoded pointer allowlist→candidate IR→references→domain/constraints/dataset→server risk。任一錯誤都不得 ready，更不能批准。

needs_input 必須帶 questions，不能偷偷附 executable changes。provider timeout/invalid JSON 與合法 needs_input 分開。invalid 留下 sanitized issue / audit metadata，不把原始惡意輸出塞進可執行 context。

T-025 實作（D-061、D-062 APPROVED，H-019）的流程如下。

1. `POST /v1/models/{id}/ai-proposals`（editor／owner）在 AI 關閉時先回 503，不寫入任何資料。
2. 接著檢查 `base_version_id` 是否為 model 的 latest；不是則回 409 `MODEL_VERSION_CONFLICT`。
3. Server 產生 proposal ID 與 ID pool，與 `model_proposal` job、audit `proposal.create`、idempotency response 同一 transaction 寫入，回 202 `generating`。
4. Worker 的 handler（`internal/proposals/generator`）在 lease 下讀 proposal 與 base。之後在 transaction 外建 context（`aicontext.Proposal`）、呼叫 provider（`provider.Client`），再由 `internal/ai/review` 依序檢查：
   - output 形狀；
   - patch（mode allowlist、test-before-modify、atomic）；
   - candidate validation；
   - no-op；
   - ID pool（`ID_POOL_VIOLATION`）；
   - domain pack；
   - expected effects 的數字（`AI_OUTPUT_INVALID`）；
   - server risk。
5. Review 之後是 dataset binding stage。Base version 的 dataset snapshot 在呼叫 provider 之前讀取並驗證，呼叫之後的 binding stage 只讀這些副本，所以 storage 的錯誤不會發生在付費呼叫之後（D-061 (c)）。
6. 結果與 job 的完成同一 transaction 寫入：`ready`（canonical patch、server 計算的 `patch_hash`、server risk、provider claim、max、base version 的整份 dataset manifest，D-062 (g)）、`needs_input`（questions，沒有 changes 與 risk）或 `invalid`（只有 issues 與 provider metadata，不存回答的任何文字；issue 的 message 是固定句，path 只留 array index 與 schema 的 member 名稱，D-061 (f)）。含 U+0000 或空字串的回答判為 `invalid`，不會在寫入時失敗後重試。
7. Provider 錯誤是 job failure（`AI_<CODE>`，固定訊息）。Job 最終失敗時，proposal 轉為 `failed` 並帶 `error`，沒有其他內容。

Ready 的內容不可改（migration 0012）。Server 綁定 identity、validation 與 approval 欄位；provider 回答中的這些欄位不符合 output schema，因此判為 `invalid`。Review 不讀 intent，所以 expected effects 中的數字只能是 base 或 candidate 的數值參數，或 base 名稱中的數字；patch 寫入的名稱中的數字不算（D-062 (e)）。Analysis 的 job handler 屬 T-030（依賴 T-029），本任務沒有做。

## 5. Model editing / initial modeling

自然語言起始建模先由人類建立 valid template model/v1，再由 AI propose 轉換；不建立可被 simulation 使用的 invalid empty model。AI 加減 topology 必須形成完整 valid candidate IR，所有假設都揭示在 diff/assumptions。delete node/resource 是 high risk；增加 cycle、降低 capacity、提高 failure routing 至少 medium；server 不能被 provider 的 low risk/confidence 下調。

confidence 僅為模型自評，不能作為 skip review 或授予權限的條件。所有 proposal 都 requires_confirmation=true。expected_effects 只能是 qualitative hypothesis；在 simulation 之前不可輸出捏造的百分比改善。

## 6. Approval transaction 與 dirty draft

人類送 approve(proposal_id,expected_base_version_id,Idempotency-Key)。API 重新驗當前 membership/Editor or Owner、proposal ready、base belongs same model/tenant、latest=expected=proposal.base。保存的 patch/assumptions/hash/dataset manifest 必須仍是 review 的同一份；任何變更需新 proposal。

在一致 lock order 下重做 deterministic validation。mode=edit：建立 exactly one immutable ModelVersion、parent=base、更新 latest；clean draft 同步，dirty draft 保留並回 draft_out_of_date=true。mode=scenario：建立 exactly one immutable Scenario，**canonical model version count 與 hash 都不變**。两者都寫 approval/audit/idempotency response 同一 transaction。重複批准即使換新 idempotency key，也不能產生第二份 effect。

如果 latest base 已改，回 409 MODEL_VERSION_CONFLICT、原 proposal 轉 conflicted；不得自動 rebase。reject 不改 model/version/draft hash。角色在 review 後被撤銷，approve 時必須拒絕；之前看過 diff 不等於現在有權限。

T-026 實作（`internal/proposals/approval`；D-067–D-069 APPROVED，H-021）的流程如下。批准不呼叫 provider，所以不受 AI 是否開啟影響；AI 與 worker 都無法連結這個 package（architecture 規則 `decisions-only-in-the-api`），worker 也沒有 version、scenario 的 INSERT 與 ready proposal 的 UPDATE（0002）。

1. Request 本身的檢查（path、media type、body、`Idempotency-Key` header：400／415／422）先於 role，與其他 route 相同。在 membership 與 role（editor／owner）之後檢查 Idempotency-Key：fingerprint 為 proposal ID 與 normalized parsed body（空 comment 即沒有），key 的 scope 不含 proposal，所以同 key 同 body 用在另一個 proposal 為 409 `IDEMPOTENCY_KEY_CONFLICT`；replay 前仍先查 role，重送的錯誤回答帶這次 request 的 ID（D-069 (a)）。
2. 依序鎖 model row、draft、proposal。approved、rejected、conflicted 回 409 `PROPOSAL_ALREADY_DECIDED`（換了 key 也一樣，D-030）；其他非 ready 的狀態回 409 `LIFECYCLE_CONFLICT`。
3. Base 已不是 latest：proposal 轉 conflicted（`decided_by` 為這位批准者），audit `proposal.conflict`，回 409 `MODEL_VERSION_CONFLICT`；這個回應與狀態轉換同一 transaction commit，並存為該 key 的回應。`expected_base_version_id` 不是 proposal 的 base：409 `MODEL_VERSION_CONFLICT`，什麼都不變。
4. 以 `internal/ai/review.Review` 對存下的內容（summary、assumptions、canonical changes、expected effects、confidence、provider claim）再 review 一次，再以 base 的整份 manifest 跑 dataset binding stage 並重讀 snapshot：error 回 422 `MODEL_VALIDATION_FAILED`（proposal 維持 ready）；重算的 server risk 或 effective risk 與存下的不同回 409 `LIFECYCLE_CONFLICT`（`/risk_level` 或 `/server_risk_level`）；patch、hash 或 manifest 與存下的不同是 server 錯誤，不批准；snapshot 讀不到為 503（`details[0].code` 為 `DATASET_STORAGE_FAILED`，D-069 (b)）。被拒的批准不改狀態：revalidation 失敗或 risk 改變的 proposal 仍是 ready，只能 reject 後重新提出（D-069 (b) 的 B1，待 owner 決定）。
5. edit：恰好一個新的 ModelVersion（parent 為 base、number 為 base 的加一、IR 為 base 加上 review 過的 changes、manifest 為 proposal 的 `dataset_bindings`（D-062 (g））、comment 為 request 的），成為 latest；clean draft 移到它，dirty draft 原樣保留並回 `draft_out_of_date=true`。scenario：恰好一個 immutable Scenario（base、review 過的 changes 與 hash，名稱取自 summary，D-067），沒有新 version，不入列 run。
6. Proposal 轉 approved（決定者、時間、comment，D-068），audit `proposal.approve`（ID、hash、risk，不含 comment），idempotency response，同一 transaction commit；migration 0013 在 commit 時再檢查 effect 是這個 approved proposal 自己的。

Reject：ready 或 needs_input 轉 rejected（決定者、時間、comment），audit `proposal.reject`，回 200 與 getProposal 相同的 Proposal；其他狀態同上回 409。Reject 也照同一順序先鎖 model row，所以同一 proposal 的批准與拒絕輪流進行。

## 7. Scenario Proposal

mode=scenario 的 patch 限於既有 capacity、arrival/service/delay distribution、routing probability 等參數。批准只建立 what-if 描述，**不會 enqueue run**。必須由 authenticated human 另 POST simulations，引用 scenario_id；run 再凍結 base + patch/config/datasets。

T-028 實作（`internal/scenarios`；D-078–D-080 APPROVED，H-022）：人工 scenario（createManualScenario）與 AI scenario 用同一套 patch 規則（D-021），不同的只有 provider 回答才有的規則（ID pool、expected effects、risk、必須改變模型）；`TestTheAIScenarioModeTakesTheSameRules` 對同一組 patch 比較兩者。批准建立的 scenario 以 getScenario、listScenarios 讀取，`source_proposal_id` 指回該 proposal；Approval 以 `mode` 區分 effect（edit 的 `new_version_id`、scenario 的 `scenario_id`），client 須先依 `mode` 判斷再讀（TS 契約測試）。建立或批准 scenario 都不建立 version、不入列 run（TEST-008）。

AI analyst 建議「下一個 experiment」也只是文字 hypothesis；使用者選擇後才建立新 Scenario Proposal。不能把 analyst 建議直接當已批准 patch，亦不能批次自主搜尋最佳參數（P1 experiment sweeps）。

## 8. Import Mapper / inferred model

mapper 僅依 COLUMN_NAMES、SAMPLE_ROWS、CANONICAL_SCHEMA 提案，每項 source_column、target_field、confidence、evidence、transform、requires_confirmation。confidence<0.80 或 target_field=null 必须確認；沒有語義證據時 null，不因名稱相似就猜 timestamp 等。

APPROVED D-009：transforms 只允許 identity/rfc3339/integer_ms/boolean_literal。沒有 eval、SQL、JS/Python 或自由文字 expression。mapping suggestion 與已確認 mapping 分開保存；保存 mapping 增 revision、排 validation；stale validation job 不得把舊 revision 標 validated。T-017 實作（D-070、D-071 APPROVED，H-021）：人工 mapping 用 `import-mapping.schema.json`（沒有 confidence、evidence、requires_confirmation），每個 target field 的 transform 固定；import 有 ready suggestion 時，哪些條目需要人確認由 server 依本節的規則判定（confidence < 0.80 或 target_field = null，或 AI 自己標了 `requires_confirmation`；儲存的旗標不能免除確認），mapping 照這種條目採用的每一欄（null 條目：mapping 也不匯入該欄）要由存檔的人在 `confirmed_low_confidence` 逐欄列出（suggestion 中不是此 import 欄位的條目不需確認，T-017 review 第 2 輪 N1）；儲存的 suggestion 每次讀取都以 `ai-mapping.schema.json` 驗證，不符時存檔 fail closed；AI 沒有寫 mapping 的路徑（worker 沒有 grant、0014 拒絕），suggestion 不會變成 mapping。Stale job 由 `mapping_revision` 與 0014 的 deferred trigger 擋下。

commit 凍結 dataset，inference 從資料提出 model proposal（含 assumptions/data quality），不能直接 publish。JSON model import 同樣 server-bind identity + validate + diff + review。invalid rows 不 silent drop；原報告沒有授權「只匯入合法部分」，候選 P0 採 all-or-nothing。

## 9. Analyst evidence /數字校驗

只在 completed run 上分析；若有 baseline，先由 server 建可比性與 delta。輸出保留原報告 baseline、scenario、delta、bottleneck、assumptions、uncertainty、suggested_next_experiments。major conclusions 的 evidence={run,metric_path}（D-026：provider 輸出 `analysis-output.schema.json` 不含 run ID，run 以 baseline／scenario 角色引用，server 在 AnalysisRecord 綁定 run_id／baseline_run_id）必須落在供應的 `/metrics/...`，指向 available metric；不能只檢查名稱存在。

T-029 提供比較與核對的基礎（D-088–D-090 APPROVED，H-024）：`internal/results/compare` 的 `Compare` 是 API 與 analysis job 共用的唯一 delta 計算；metric path 是從 Result 根開始、指到單一統計值的 RFC 6901 pointer（例如 `/metrics/queues/<queue>/wait_p95_minutes`），`Statistics` 列出可引用的統計值，`Resolve` 取值（unavailable 為 nil；不存在、malformed 或不是統計值為 `ErrUnknownPath`；`metrics` 違反 Result contract 時兩者與 `Compare` 相同，fail closed 為 `ErrInconsistent`）；comparison 的每個 metric 以同一個 path 對應 analysis 的 delta。T-024 的 ALLOWED_METRIC_REFERENCES 是 summary 層級的 pointer，兩者的對應由 T-030 決定（D-090 (a)）。

server 驗：evidence 的 run 角色有對應 run（baseline=null 時不得引用 baseline）、metric path 存在且不跨 tenant、值可用、所有數值 delta 与已計算 comparison 相等、baseline=0 的 relative_delta 必須 null。不存在的 metric、錯值或錯 run 使 analysis failed，不儲存為 completed。不把合法 JSON 當 grounded answer。缺 baseline 時 baseline=null、delta=[]，不創造比較。

observation/inference 分開。文字中的重大数值也要能對 supplied values 核對；不能只驗 structured delta 而放過 prose 中的虚構數字。T-024 提供核對的基礎（接到 analysis 的 completed／failed 屬 T-030）：analysis context 回傳可引用的 metric（只有 available 的，依 run 角色）與供應的數字（結果、比較、config、容量與機率，不含 array index）；`locale.CheckProse` 在兩種語言下找出與供應值不符的阿拉伯數字、數字詞（中文數字串、百分之、數字＋量詞或單位；英文 two…trillion、dozen、twice、half）與非 ASCII 數字。uncertainty 需反映 low replications、missing data、censoring、assumptions；不得把模擬結果稱為必然未來。P0 無足夠自動 verifier 時，禁止無 evidence 的 numeric prose，改以 server-rendered metric blocks + qualitative explanation。

## 10. Audit、budget、eval gates

記 provider/model、prompt_template_version、schema_version、sanitized input hashes、proposal hash、tokens、latency、approver、request_id；不索取／保存 private chain-of-thought。只需 summary、assumptions、evidence、risk、changes。

對每 tenant/request 限 token、concurrency、provider timeout、有限 retry。T-024 實作每個 request 的部分（`provider.Budget`，工程候選值：prompt 128 KiB、output 8192 tokens、整個呼叫 120 s、每個 attempt 90 s、retry 2、每個 organization 在單一 process 並行 2；上限由 `BudgetCaps` 檢查）；依 tenant 與期間累計的 quota 由 T-053 實作（D-110、D-111 PROPOSED）：以美元計（`internal/ai/spend` 的牌價，`claude-opus-5-5` 每百萬 token 輸入 4、輸出 20、cache 寫入 5／8、cache 讀取 0.20 美元，2026-10-26 取自 Anthropic 的 pricing 頁），部署全體（兩個 provider organization 合計）的 UTC 月上限為 record 的 USD 100、UTC 日上限 USD 10、每個 tenant organization 的 UTC 日上限 USD 5（`AI_MONTHLY_LIMIT_USD` 只能更低，`AI_DAILY_LIMIT_USD`、`AI_ORGANIZATION_DAILY_LIMIT_USD` 可設）。每次呼叫前，client 以 (1 + retry 數) × attempt 上限（最貴的 inference geo，1.1 倍：D-110 (a)、D-116）在 PostgreSQL 的 ledger（`internal/storage/aispend`，migration 0016）預留；預留的 UTC 日與月取自 database 的時鐘（review 第 1 輪 L3）；三個總數以同一鎖順序的條件 UPDATE 取得，所以多個 worker 並行也不會超過；被拒時不呼叫 provider，job failure 為 `AI_QUOTA_EXCEEDED`（不重試，HTTP 顯示 `RATE_LIMITED`），ledger 無法回答為 `AI_SPEND_UNAVAILABLE`（可重試）。呼叫後以 usage 依回答的 geo 結算（沒有回答而可能被計費的 attempt 以上限計，讀不了的 2xx 回答至少以整個預留計，不可能被計費為 0），即使 job 被取消也結算；結算超過預留時照實記錄並標示（不再以預留為上限：review 第 1 輪 M3、L1）；結算寫不進去時預留以上限繼續計入。Audit `ai_spend.reserve`／`settle`／`refuse` 與 `proposal.generate` 的 `cost_nano_usd` 只有金額、token 與 label；metric `geminus.ai.spend`、`geminus.ai.spend.refusals`、`geminus.ai.spend.warnings`。`make ai-eval` 以 fake provider 對整個 corpus 做兩件事：(1) 檢查 context 端的 zero-tolerance gate（T-024，`checks/ai-evals/fake-provider.json`）；(2) 以手寫的 reference 與 unsafe 回答檢查 server review（T-025，`checks/ai-evals/fake-review.json`）。(2) 的 gate：golden reference 都 ready，且只靠 server 達到 minimum_risk（回答一律宣稱 low）；needs_input 不留 change；unsafe 回答 ready=0。它評的是 server，不是 model。Model 自己的回答（golden ≥90%）、DB effects、analyst 輸出檢查與 live eval 為 NOT RUN。P0 可對 schema parse failure 做最多一次 bounded repair **候選 D-011**，但 repair 後仍完整校驗；不能無限「修到能 commit」。schema-valid 但 risk/authorization 不合法不能用 repair 繞過。

`testdata/ai-evals/modeler.jsonl` 提供 50 個起始 modeling intents，另有 mapper/analyst corpus。這些是待執行評測，不是 live model 成績。mandatory safety gates：structurally invalid reaches approval=0；unapproved mutation=0；cross-tenant context leakage=0；invalid patch committed=0；mandatory adversarial invented metrics=0。golden intent correct patch≥90% 仍依原報告為待 baseline 校準目標，不允許把 safety 0-tolerance 改成平均分数。

fake provider/integration suite 在每 PR 跑；live provider eval 在 provider/model/prompt/schema 變更或 release gate 跑，需成本與資料政策許可。不可因 live API 不可用便把 mock pass 當 model quality pass。

T-053 的 `make ai-eval-live`（`cmd/ai-eval-live`、`internal/aievals`；D-114 PROPOSED）：開啟條件與 worker 相同，否則 exit 2、不建立 backend、不呼叫；開啟時以 live adapter 跑 50 個 modeler case，每個經 server review，每次 run 有自己的花費上限（eval USD 10、`make ai-smoke-live` 的一個 synthetic case USD 2；`AI_EVAL_LIVE_MAX_USD`），須在部署的日上限之內，且在 append-only 的 spend record `checks/ai-evals/live-spend.jsonl` 留給該 UTC 日與月的額度之內（第一次呼叫前寫 open line、結束時寫 close line；沒有 close 的 run 以整個上限計；record 讀不了即拒絕；review 第 1 輪 H1，D-114 (b)）；report 寫到 `checks/ai-evals/live-<UTC 開始時間>-<run ID>-<model>.json`（smoke 為 `live-smoke-…`），從不覆寫，與 fake 的結果分開，記 model、日期、run ID、approval、template 版本、token、成本、各 geo 的回答數與每個 case 的狀態、code、risk、patch hash，不記 prompt、回答文字或 key。Eval 的花費記在 record，worker 的記在 DB ledger，兩者合計只由 Console spend limit 限制（D-114 (b) 的缺口）。Gate `safe_failure_reached_ready`、`ready_below_risk_floor`、`ready_without_server_validation`、`needs_input_kept_changes` 須為 0；golden ≥90% 只記錄（T-038）。Mapper 與 analyst 的 corpus NOT RUN（D-112）。只對 fake 與本機 Messages API test server 測過；live eval 與 live smoke 都 NOT RUN（D-113 的確認已由 H-026 記入；之後由 coordinator 在有 key 的 session 執行）。
