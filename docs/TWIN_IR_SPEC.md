# Twin IR / Model Contract

SOURCE：原報告 pp. 5–6、14–20、24–25、31–35。完整 closed-object schema、hash、dataset binding、patch allowlist 是 PROPOSED D-001 / D-005 / D-006 / D-014 / D-017，須確認後才凍結為 production contract。

## 1. 唯一 canonical representation

`schemas/model-ir.schema.json` 是 IR v0.1 的 source of truth，採 JSON Schema Draft 2020-12。IR 保留原報告的 arrays：entity_types、nodes、flows、resources、constraints、metrics；不能為了 UI 或 patch 方便而擅自改成 dictionaries。IDs 表示 identity；array index 不是穩定 identity。

schema_version、model_id、domain、name、time_unit 及 views 為根層欄位。metadata 與 domain_attributes 是不可信資料容器，不是程式碼或 expression。closed objects 拒絕未知欄位；metadata 例外，但仍受深度／大小及 redaction 限制。原報告的「最小 IR」有空 nodes/flows，它只是 shape 示意，不是可執行模型；可執行模型須通過下述 semantic validation。

APPROVED D-001（2026-10-15）：伺服器產生的資源 ID 為 `<prefix>_<ULID>`，ULID 為 26 字元大寫 Crockford base32（首字 0–7），schema 以 `common.schema.json#/$defs/<Type>ID` 強制；SQL 為 text。使用者定義的 IR entity ID（node／resource／flow／entity_type 等）不受此限，仍用 `$defs/ID`。IR 的 model_id 由 server 指定、不可藉匯入／patch 改變 tenant 或 resource identity。

| 資源 | prefix | schema def |
|---|---|---|
| organization | `org` | OrganizationID |
| user | `usr` | UserID |
| project | `prj` | ProjectID |
| model | `mdl` | ModelID |
| model version | `mv` | ModelVersionID |
| dataset／dataset version | `ds`／`dsv` | DatasetID／DatasetVersionID |
| import | `imp` | ImportID |
| AI proposal | `aip` | ProposalID |
| scenario | `scn` | ScenarioID |
| simulation run | `sim` | RunID |
| AI analysis | `ana` | AnalysisID |
| job | `job` | JobID |
| audit event | `aud` | AuditEventID |
| request（X-Request-ID、error.request_id） | `req` | RequestID |
| trace artifact | `art` | ArtifactID |

`target_id` 可為任一上列資源（ResourceID）。`actor_id` 暫維持通用格式，因 actor_type 可為 worker／system，留待 T-035 定義。

## 2. Node、flow、resource catalog

| Type | Required configuration | P0 行為 |
|---|---|---|
| source | entity_type、arrival.distribution | 從 t=0 起抽第一個 interarrival，之後產生 entity；可選 max_entities 是 D-005 的測試/有限 source 提案 |
| queue | id/type | 顯式顯示 downstream process 的 FIFO wait；不是額外的第二次服務或重複等待計時 |
| process | service_time.distribution、requires[] | 一次性 acquire 全部 resource units，完成 service 後全部 release |
| decision | id/type | outgoing probability routing；沒有 script predicate |
| delay | delay_time.distribution | 不占 resource 的等待；delay_time 是 D-005 / D-014 補全欄位 |
| sink | 可選 outcome=completed/abandoned | 結束 entity；abandoned 不計 completed throughput |
| failure | 可選 reason | 記 generic failure event 後繼續指定 flow；不代表真實部署故障 |
| rework | id/type | 記 generic rework event 後繼續指定 flow；不隱含跳轉目標 |

所有 flows 都有 id/from/to/routing={type:probability,probability}。decision 可有多個 outgoing flows，概率和=1；其餘非 sink 節點恰有一個 probability=1 的 flow；sink 沒有 outgoing。queue 只接一個 process，是候選 P0 限制，不暗加 calendar/priority/preemption。

resource 的 type 保留 human / machine / generic；capacity 為正整數；queue_discipline=fifo。可選 role 提供 domain catalog 語義。Software Delivery catalog 包含 developer、reviewer、ci_runner、test_environment、deployment_slot、generic_resource；kernel 不依角色字串决定行為。

requires 每項為 {resource_id,units}。同一 process 不可重複列同一 resource；units≤capacity。capacity=0、負值、非整數、unknown resource 一律 invalid，不以無限等待替代 validation。

## 3. Distributions / units

P0 僅 constant(value)、exponential(mean)、triangular(min,mode,max)、empirical(samples_ref)。lognormal、calendar-dependent、mixtures 為 P1。

Schema 驗型別與非負值；semantic validator 另驗 min≤mode≤max、finite numbers、empirical 可解析且非空、unit 可轉換。interarrival 必須嚴格正值；zero service/delay 可以存在，但可達的 zero-time cycle 不能執行。時間換算和 rounding 由 SIMULATION_SPEC 固定。

PROPOSED D-017：day 在 P0 是連續 24 小時。報告 example mean=16 min 在此語義下是 90 arrivals/day，不應當作 30/day 或暗示有 8-hour work calendar。full template fixture 用 synthetic mean=48 min 示範 30/day，其餘新增 Test/Deploy 參數也標 synthetic，不能當客戶事實。

## 4. Semantic validation order

執行與資料修改之前依序驗：JSON parse（拒 duplicate keys / NaN / Infinity）→JSON Schema→limits→ID uniqueness→references→graph rules→distribution/domain semantics→constraint checks→dataset bindings→scenario/patch policy→risk classification。invalid 回 issue code、JSON Pointer path、severity/message。

| Code family | 驗證 |
|---|---|
| IR_SCHEMA_INVALID | schema/version/type/unknown field 不符 |
| DUPLICATE_ID | 同 catalog 重複 ID；候選 profile 要求 nodes/resources 不共用 ID，避免 selectedEntityId 歧義 |
| UNKNOWN_REFERENCE | flow endpoint、entity_type、resource、metric、constraint、view position 指向不存在的 ID |
| ROUTING_INVALID | 非 decision 多路、sink 出路、概率負值或總和偏離 1（容差候選 1e-9） |
| NO_REACHABLE_SINK | source 沿正概率路徑找不到 sink；0% 分支允許存在但不會執行 |
| ZERO_TIME_CYCLE | 可達的 zero-minimum-time graph 含 cycle；不能靠 event cap 當正常 termination |
| RESOURCE_REQUIREMENT_INVALID | demand 超 capacity／重複 resource／負數 |
| DISTRIBUTION_INVALID | triangular ordering、source zero interval、empirical 空值或非 finite |
| DATASET_BINDING_INVALID | logical ref 沒有 immutable authorized snapshot 或 hash/unit 不一致 |
| VIEW_REFERENCE_INVALID | view position 不對應 canonical node |
| CONSTRAINT_FAILED | P0 outgoing_probability_sum 不成立 |
| JSON_INVALID | 解析失敗：非法 JSON、重複 member、非法 UTF-8、尾隨資料（D-023） |
| LIMIT_EXCEEDED | D-029 上限：文件 >2 MiB、metadata／domain_attributes 深度 >8 或 >16 KB、分布參數換算超過 365 天、pointer 超過 32 層 |
| UNREACHABLE_NODE | **warning**：沒有 source 能到達的 dormant node，不阻擋 publish |

APPROVED D-023（實作於 `internal/model/validate`，T-004）：同一階段收集全部 issue，該階段有 error 就不進下一階段；issues 依 JSON Pointer 排序（array index 以數值比較）再依 code。T-004 的實作解讀（需 owner 確認時可另立決策）：(1) 任何 source 經 p>0 flow 能到達、卻無法到達 sink 的 node 都回 NO_REACHABLE_SINK，不只檢查 source 本身；(2) 指向 source 的 flow 回 ROUTING_INVALID，因為 source 不接收 entity；(3) metric／constraint 只檢查引用存在，不檢查被引用 node 的類型。D-033：zero-time 以分布可能的最小 tick 判定（constant=0、triangular min=0；exponential 恆 >0；empirical 在 dataset binding 階段判定）。

unreachable dormant node 可 warning；無 reachable sink 的活躍路徑是 error。valid graph 仍可能大量 rework，因此 runtime budgets 必須存在。來源未明確要求跨 catalog IDs 全域唯一；上述 node/resource uniqueness 為 D-014 的候選限制。

## 5. Version、hash 與 empirical freezing

SOURCE：Draft mutable；ModelVersion immutable、保存 parent；run 保存 model_version_id、model_hash、patch、engine、seed、replications、config。

PROPOSED D-006：`model_hash = sha256(JCS(IR))`，保留原 array order；JCS 指 RFC 8785，不用 Python sort_keys / Go json.Marshal 的預設結果冒充。拒 duplicate JSON keys、不合法 Unicode 及非 finite number；數字採 interoperable IEEE-754 profile，seed 限 0..2^53-1。model hash 含 views/metadata；它是完整版本內容 hash，不是純 simulation semantic hash。

empirical logical ref（例如 dataset:review_duration）不是可變遠端網址。每個 published version 附 immutable DatasetBinding manifest：logical_ref、dataset_version_id、content_hash、unit、sample_count。版本批准前完整凍結；run 複製同一 manifest，worker 驗實際 object bytes hash 後 preload samples。晚到的新資料只能產生新 dataset version / model version，不能改變已提交 run。

`effective_model_hash = sha256(JCS(apply(base.IR,scenario_patch)))`。dataset_manifest_hash 對按 logical_ref 排序的 manifest 做 JCS/SHA-256。config_hash 對完整公開 config 做 JCS/SHA-256；execution_limits、engine profile 也在 run input 中固定，不讓重試改設定。

JSON Schema 只驗 `sha256:` 的格式，不能驗內容真的相符；hash consistency 是 T-005/T-021 的產品測試，不是本包 schema validation 所保證的事。

## 6. JSON Patch profile / approval scope

SOURCE：RFC 6902、application/json-patch+json、array-index patch examples。PROPOSED D-014：P0 只接受 add/remove/replace/test；move/copy、空字串 root path、不合法 `~` escape、超深 pointer 均拒絕。RFC 6901 pointer 只 decode 一次；authorization 不做字串 startsWith 判斷，必須比較解碼後 tokens。

manual draft patch 可操作 name、nodes、flows、resources、entity_types、constraints、metrics、views、metadata，但不能改 schema_version/model_id/domain/time_unit 或任何 server envelope identity。改 time_unit 需要明確 migration，P0 不隱式 reinterpret 已有 duration。

AI edit allowlist 比 manual 更小：name、nodes、flows、resources、entity_types、constraints、metrics；禁止 views/metadata/identity。AI scenario 僅既有 resources/*/capacity、nodes/*/arrival/distribution、nodes/*/service_time/distribution、nodes/*/delay_time/distribution、flows/*/routing/probability；不能增刪 topology 或創造 dataset binding。物件整段 replace 仍逐欄驗 full candidate IR，不因 parent path 被允許就略過子欄。

所有 patch 都在 copy 上依序 apply，整體成功才提交；任一 op/test 失敗都不產生 partial mutation。array index 只在 pinned immutable base 有意義，diff 顯示 logical entity ID。APPROVED D-020（實作於 `internal/model/patch`）：修改、取代或刪除 nodes／flows／resources／entity_types／metrics／constraints 的既有元素前，同一 patch 必須先 `test` 該元素的識別欄位（constraints 沒有 id，以 `node_id` 識別，為 T-004 解讀）；中間若有插入、刪除或整個陣列取代，先前的 test 失效。append（`/-`）與插入新元素不需 test。test 不受 allowlist 限制、不算 business mutation，也不能取代 expected base。allowlist 以解碼後 token 做前綴比對；scenario 模式（D-021，人工與 AI 相同）只允許上列五種參數路徑；AI edit 另允許刪除同一 patch 刪掉之 node 的 `/views/2d/positions/{id}`（D-022）。patch 錯誤碼：PATCH_SCHEMA_INVALID、PATCH_PATH_INVALID、PATCH_PATH_FORBIDDEN、PATCH_TEST_REQUIRED、PATCH_TARGET_MISSING、PATCH_TEST_FAILED，路徑指向 patch 文件（如 `/3/path`）。刪除 node/resource 必須同時修復 references，否則 invalid。

## 7. View / canonical event schema

2D positions 放 views.2d.positions[node_id]；3D layout_mode=derived_from_2d。views 不可含另一份 nodes/flows。positions 缺省時 DomainPack.DefaultViews 生成；選取與 playback 使用同一 ID。

canonical-event.schema.json 保留 case_id/event_type/timestamp 必填，stage recommended，其餘 optional。timestamp 是 RFC3339；跨時區先正規化再排序，不能猜無時區時間。duration_ms 與 timestamp-derived duration 不一致時產生 data-quality issue，選用政策要由 domain inference 明列。CSV 的低 confidence mapping 和 dataset 推論永不直接更改 canonical Twin。

## 8. 相容性與擴充

未知 schema_version 拒絕，不默默盡力解析。v0.1 新欄位／新 node 或 constraints 需更新 schema、fixtures、Go/TS contract tests 和 version migration 策略。Future DomainPack 可使用既有 generic primitives 與 domain attributes；新增核心 primitive 應有獨立 ADR，不能以 arbitrary script 绕過 contract。

補充（D-010）：單純回到前一節點的 flow 不自動等於 domain rework。需要可辨識的 rework node 或已批准的 DomainPack mapping 才計數；不能以 f_retry 之類的字串名稱在 generic kernel 硬編語義。
