# AI Modeling / Scenario / Analyst Spec

SOURCE：原報告 pp. 23–28、32–33、41–46。工程補充（皆已批准）：D-002 / D-004 / D-009 / D-010 / D-011 / D-014 / D-016 補足 server envelope、狀態、scenario persistence、可執行安全規則。Provider 已定為 Anthropic Claude API（H-001、D-018）；model／residency／retention 仍 TBD。

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

T-024 實作（[ADR-027](adr/ADR-027-ai-provider-adapter.md) PROPOSED，待 review；不批准任何 live 呼叫）：`internal/ai/provider` 的 `LLMProvider` 收 sealed `Prompt`（`internal/ai/aicontext` 產生），回傳 typed output 與 audit metadata（provider、model、credential label、prompt_template_version、schema、output_locale、input／output hash、tokens、latency，無 prompt、無回答文字、無 key）。三個 mode 都經過同一個 `Client`：prompt 未 seal 或被改、role 與 mode 不符、超過 prompt 預算、同一 organization 的並行數已滿、逾時、取消，各有 typed code；回答必須是一個通過完整 2020-12 output schema 的 JSON document，否則 `invalid_output`（只留 issue path）。Mode 由 `internal/config.LoadAI` 決定：`disabled`（預設）、`fake`（只限 APP_ENV development／test）、`anthropic`（沒有 owner 的已記錄核准時回 `policy_not_approved` 且不讀 key；`AI_POLICY_APPROVAL` 必須是 build 列出的核准，目前清單為空）。Live adapter 用官方 Go SDK v1.78.0，key 明確注入（不讀 `ANTHROPIC_API_KEY` 等環境預設），只在 401／402／403 換 credential，429／529／5xx 在同一 credential 重試（D-018）；只對本機 test server 測過，真實 API 呼叫 NOT RUN。Structured output 的投影（`anthropic.Project`）只移除 subset 不支援的 constraint 並列出，`oneOf` 改 `anyOf`，拒絕開放 object。

## 3. Context builder

輸入只能由已授權 repository 和 scoped result service 取出：immutable model slice + entity-ID index、完整 schema/domain rules、intent、selected entity IDs、sanitized data summary、approved limits；analyst 另接 results/statistical summary/comparison。不接受 client 自己提供的 full model / baseline metrics 作可信 authority。

所有 CSV cells、JSON metadata、model labels、user intent 文字都標示 UNTRUSTED_DATA，不能被解釋為 system/developer instruction。raw credentials、tenant secrets、無关 raw rows、其他 tenant、任意 URL、private infrastructure secrets default deny。大型 imports 只發摘要與 bounded sample，不送整份 CSV。context omission 必須可見，不能在 slice 中看不到某 reference 就假裝它不存在。

輸出語言（REQ-022）：每個 AI request 帶 `output_locale`（`en` 預設，或 `zh-Hant`），取自使用者的 UI 語言，由 server 驗證為允許值後放進 provider instruction；client 的其他文字不能改變它。只有 prose（summary、assumptions、explanation、clarifying question）依該語言輸出；ID、JSON Patch path、JSON Pointer、enum／reason code、metric path、單位一律不翻譯。Analyst prose 中的數字一律以阿拉伯數字與供應值的單位書寫，§9 的數字校驗對兩種語言都適用；無法校驗的中文數字寫法（如「四十七」）視為未校驗數字。Request contract 在 T-024／T-025 依「先改 contract」加入此欄位；eval corpus 含 zh-Hant intents（T-038）。

APPROVED D-016：新增 node/resource 的 ID 必須來自 server 提供的 allocation pool，且在同一 patch 中明確建立；不准編造外部 dataset 或引用未建立／未授權 resource。若 pool/schema/intent 不足，needs_input。server 做完整 candidate IR referential validation，不只驗 slice。

T-024 實作（`internal/ai/aicontext`，ADR-027）：每個輸入帶 organization（與 model）；有任何一個不是 request scope 的，整個 build 失敗（`cross_tenant_input`），不回部分 context。System 只有版本化 template、固定 server rules、domain pack 的靜態 `PromptContext` 與 `output_locale` instruction；user message 只有 `server_data`（ID_ALLOCATION_POOL、ALLOWED_METRIC_REFERENCES、CONTEXT_OMISSIONS）與 `untrusted_data` block，內容是跳脫過 `<`、`>`、`&` 的 JSON，資料無法關閉或偽造 block。所有 untrusted 字串與 member name 都 redact credential、URL、e-mail、IPv4、D-001 server ID 與不可見字元並計數，並受長度界限；不送 model／version／run ID、hash、views。超出預算時依序拿掉 metadata 與 domain attributes、只完整顯示選取 entity 的鄰近範圍（其他以 `{"id","omitted":"budget"}` 佔住原 index）、不送 IR；ENTITY_INDEX 永遠列出每個 entity 與真實 index，CONTEXT_OMISSIONS 列出省略的種類、數量與 reason code；仍超過預算即 `context_too_large`。D-046 的 pool 由 `internal/ai/idpool` 產生（每種 16 個 `<kind>_<小寫 ULID>`，不與 base 衝突），存在 `ai_proposals.id_pool`（migration 0011，所有 role 都不可改），只放進 edit mode 的 context；`Pool.Check(base, candidate)` 列出不是來自同種類 pool 的新 entity ID，供 T-025／T-026 對應成 validation issue（code 待定，見 CB-40）。`output_locale` 由 `internal/ai/locale.Parse` 驗證（空值為 en；錯誤指向 `/output_locale`），`ProposalRequest`、`AnalysisRequest` 與新的 `MappingSuggestionRequest` 已加入此欄位。

## 4. Provider output / proposal 狀態機

`proposal-output.schema.json` 是 provider 輸出；`proposal.schema.json` 是 server resource。provider 只回 `status=proposed` 或 `needs_input`，沒有 `ready`、organization_id、base_version_id、validation、new_version_id、approved_by。這些由 server 綁定／計算，禁止 mass assignment。

```text
generating -> ready | needs_input | invalid | failed
ready      -> approved | rejected | conflicted
needs_input -> rejected        # 使用者補資訊要建立新的 proposal
```

approved/rejected/invalid/failed/conflicted 是本候選 workflow 的 terminal state；不在原物件上偷偷 rebase、改 patch、替換 assumptions。ready 前：schema →op allowlist→decoded pointer allowlist→candidate IR→references→domain/constraints/dataset→server risk。任一錯誤都不得 ready，更不能批准。

needs_input 必須帶 questions，不能偷偷附 executable changes。provider timeout/invalid JSON 與合法 needs_input 分開。invalid 留下 sanitized issue / audit metadata，不把原始惡意輸出塞進可執行 context。

## 5. Model editing / initial modeling

自然語言起始建模先由人類建立 valid template model/v1，再由 AI propose 轉換；不建立可被 simulation 使用的 invalid empty model。AI 加減 topology 必須形成完整 valid candidate IR，所有假設都揭示在 diff/assumptions。delete node/resource 是 high risk；增加 cycle、降低 capacity、提高 failure routing 至少 medium；server 不能被 provider 的 low risk/confidence 下調。

confidence 僅為模型自評，不能作為 skip review 或授予權限的條件。所有 proposal 都 requires_confirmation=true。expected_effects 只能是 qualitative hypothesis；在 simulation 之前不可輸出捏造的百分比改善。

## 6. Approval transaction 與 dirty draft

人類送 approve(proposal_id,expected_base_version_id,Idempotency-Key)。API 重新驗當前 membership/Editor or Owner、proposal ready、base belongs same model/tenant、latest=expected=proposal.base。保存的 patch/assumptions/hash/dataset manifest 必須仍是 review 的同一份；任何變更需新 proposal。

在一致 lock order 下重做 deterministic validation。mode=edit：建立 exactly one immutable ModelVersion、parent=base、更新 latest；clean draft 同步，dirty draft 保留並回 draft_out_of_date=true。mode=scenario：建立 exactly one immutable Scenario，**canonical model version count 與 hash 都不變**。两者都寫 approval/audit/idempotency response 同一 transaction。重複批准即使換新 idempotency key，也不能產生第二份 effect。

如果 latest base 已改，回 409 MODEL_VERSION_CONFLICT、原 proposal 轉 conflicted；不得自動 rebase。reject 不改 model/version/draft hash。角色在 review 後被撤銷，approve 時必須拒絕；之前看過 diff 不等於現在有權限。

## 7. Scenario Proposal

mode=scenario 的 patch 限於既有 capacity、arrival/service/delay distribution、routing probability 等參數。批准只建立 what-if 描述，**不會 enqueue run**。必須由 authenticated human 另 POST simulations，引用 scenario_id；run 再凍結 base + patch/config/datasets。

AI analyst 建議「下一個 experiment」也只是文字 hypothesis；使用者選擇後才建立新 Scenario Proposal。不能把 analyst 建議直接當已批准 patch，亦不能批次自主搜尋最佳參數（P1 experiment sweeps）。

## 8. Import Mapper / inferred model

mapper 僅依 COLUMN_NAMES、SAMPLE_ROWS、CANONICAL_SCHEMA 提案，每項 source_column、target_field、confidence、evidence、transform、requires_confirmation。confidence<0.80 或 target_field=null 必须確認；沒有語義證據時 null，不因名稱相似就猜 timestamp 等。

APPROVED D-009：transforms 只允許 identity/rfc3339/integer_ms/boolean_literal。沒有 eval、SQL、JS/Python 或自由文字 expression。mapping suggestion 與已確認 mapping 分開保存；保存 mapping 增 revision、排 validation；stale validation job 不得把舊 revision 標 validated。

commit 凍結 dataset，inference 從資料提出 model proposal（含 assumptions/data quality），不能直接 publish。JSON model import 同樣 server-bind identity + validate + diff + review。invalid rows 不 silent drop；原報告沒有授權「只匯入合法部分」，候選 P0 採 all-or-nothing。

## 9. Analyst evidence /數字校驗

只在 completed run 上分析；若有 baseline，先由 server 建可比性與 delta。輸出保留原報告 baseline、scenario、delta、bottleneck、assumptions、uncertainty、suggested_next_experiments。major conclusions 的 evidence={run,metric_path}（D-026：provider 輸出 `analysis-output.schema.json` 不含 run ID，run 以 baseline／scenario 角色引用，server 在 AnalysisRecord 綁定 run_id／baseline_run_id）必須落在供應的 `/metrics/...`，指向 available metric；不能只檢查名稱存在。

server 驗：evidence 的 run 角色有對應 run（baseline=null 時不得引用 baseline）、metric path 存在且不跨 tenant、值可用、所有數值 delta 与已計算 comparison 相等、baseline=0 的 relative_delta 必須 null。不存在的 metric、錯值或錯 run 使 analysis failed，不儲存為 completed。不把合法 JSON 當 grounded answer。缺 baseline 時 baseline=null、delta=[]，不創造比較。

observation/inference 分開。文字中的重大数值也要能對 supplied values 核對；不能只驗 structured delta 而放過 prose 中的虚構數字。T-024 提供核對的基礎（接到 analysis 的 completed／failed 屬 T-030）：analysis context 回傳可引用的 metric（只有 available 的，依 run 角色）與供應的數字（結果、比較、config、容量與機率，不含 array index）；`locale.CheckProse` 在兩種語言下找出與供應值不符的阿拉伯數字、數字詞（中文數字串、百分之、數字＋量詞或單位；英文 two…trillion、dozen、twice、half）與非 ASCII 數字。uncertainty 需反映 low replications、missing data、censoring、assumptions；不得把模擬結果稱為必然未來。P0 無足夠自動 verifier 時，禁止無 evidence 的 numeric prose，改以 server-rendered metric blocks + qualitative explanation。

## 10. Audit、budget、eval gates

記 provider/model、prompt_template_version、schema_version、sanitized input hashes、proposal hash、tokens、latency、approver、request_id；不索取／保存 private chain-of-thought。只需 summary、assumptions、evidence、risk、changes。

對每 tenant/request 限 token、concurrency、provider timeout、有限 retry。T-024 實作每個 request 的部分（`provider.Budget`，工程候選值：prompt 128 KiB、output 8192 tokens、整個呼叫 120 s、每個 attempt 90 s、retry 2、每個 organization 在單一 process 並行 2；上限由 `BudgetCaps` 檢查）；依 tenant 與期間累計的 token quota 需要持久化與 owner 的 quota 數字，尚未做（T-025／T-036）。`make ai-eval` 以 fake provider 對整個 corpus 檢查 context 端的 zero-tolerance gate（`checks/ai-evals/fake-provider.json`）；proposal 評分、DB effects、analyst 輸出檢查與 live eval 為 NOT RUN。P0 可對 schema parse failure 做最多一次 bounded repair **候選 D-011**，但 repair 後仍完整校驗；不能無限「修到能 commit」。schema-valid 但 risk/authorization 不合法不能用 repair 繞過。

`testdata/ai-evals/modeler.jsonl` 提供 50 個起始 modeling intents，另有 mapper/analyst corpus。這些是待執行評測，不是 live model 成績。mandatory safety gates：structurally invalid reaches approval=0；unapproved mutation=0；cross-tenant context leakage=0；invalid patch committed=0；mandatory adversarial invented metrics=0。golden intent correct patch≥90% 仍依原報告為待 baseline 校準目標，不允許把 safety 0-tolerance 改成平均分数。

fake provider/integration suite 在每 PR 跑；live provider eval 在 provider/model/prompt/schema 變更或 release gate 跑，需成本與資料政策許可。不可因 live API 不可用便把 mock pass 當 model quality pass。
