# Twin IR / Model Contract

SOURCE：原報告 pp. 5–6、14–20、24–25、31–35。完整 closed-object schema、hash、dataset binding、patch allowlist 涉及 D-001 / D-005 / D-006 / D-014 / D-017（皆已批准）；本文件仍是 P0 草案契約，尚未凍結為 production contract。

## 1. 唯一 canonical representation

`schemas/model-ir.schema.json` 是 IR v0.1 的 source of truth，採 JSON Schema Draft 2020-12。IR 保留原報告的 arrays：entity_types、nodes、flows、resources、constraints、metrics；不能為了 UI 或 patch 方便而擅自改成 dictionaries。IDs 表示 identity；array index 不是穩定 identity。T-046 phase 2 另加 optional 的根層 `calendars` array（D-052，§2.1）。

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

`target_id` 可為任一上列資源（ResourceID）。`actor_id` 暫維持通用格式，因 actor_type 可為 worker／system，留待 T-035 定義。T-035 的提案（D-092，APPROVED，H-024）：user actor 為 `usr_` ID；worker 與 system actor 為 actor reference（字母開頭，之後是字母、數字、`_`、`.`、`:`、`-`，至多 128，即 `twin.actor_ref` 與 `AuditEvent.actor_id` 的 pattern），worker 為 `WORKER_ID`，system 為 `provision-tenant`。

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

resource 的 type 保留 human / machine / generic；capacity 為正整數；queue_discipline=fifo。可選 role 提供 domain catalog 語義。範例 pack 的 Software Delivery catalog 包含 developer、reviewer、ci_runner、test_environment、deployment_slot、generic_resource；電子產線 catalog 由 T-046 定義（H-016；見 [ELECTRONICS_LINE.md](domains/ELECTRONICS_LINE.md) §3，APPROVED，H-018）；kernel 不依角色字串决定行為。

requires 每項為 {resource_id,units}。同一 process 不可重複列同一 resource；units≤capacity。capacity=0、負值、非整數、unknown resource 一律 invalid，不以無限等待替代 validation。

### 2.1 產線流動的 optional 成員（ADR-026、D-049–D-058；T-046 phase 2）

Schema 已接受下列成員，全部 optional；沒有它們的模型與上面的 P0 行為完全相同（D-047：schema_version 維持 `0.1`，Go 型別不輸出缺少的成員，既有 fixtures 重新 encode 後 model_hash 不變）。**在對應的實作任務完成之前，validator 以 `PRIMITIVE_NOT_SUPPORTED`（error）拒絕任何用到它們的模型，engine 的 Compile 也各自拒絕（D-063 APPROVED，H-019，§4）**，所以這樣的模型不能 publish、不能建立 scenario、不能執行，也不會被當作沒有這些成員而模擬。queue `capacity` 的 gate 已由 T-047 解除（validator 檢查 D-050 R1，engine des-go-0.2.0 執行 D-049，SIMULATION_SPEC §3）；`breakdowns`、`lognormal`、`weibull` 的 gate 已由 T-048 解除（validator 檢查 R3、參數範圍與 resource 分布的 limits、binding、D-033，engine des-go-0.3.0 執行 D-051 與 D-056，SIMULATION_SPEC §3、§4）；`calendars`（含 `calendar_id`、`shift_capacity`）與 `arrival.schedule` 的 gate 已由 T-049 解除（validator 檢查 CALENDAR_INVALID 的形狀、R2、R4、RELEASE_SCHEDULE_INVALID，engine des-go-0.4.0 執行 D-052 與 D-053，SIMULATION_SPEC §2、§3；細節的解讀為 D-081、D-082、D-085，APPROVED，H-024）；其他成員（batch、setups、宣告式 metrics）仍被擋。

| 成員 | 位置與形狀 | Decision | 實作與解除 gate |
|---|---|---|---|
| `capacity` | queue node；整數 1–100000；缺＝無限 | D-049 | T-047（已實作、gate 已解除：validator 的 R1 與 des-go-0.2.0 的 kernel） |
| `breakdowns` | resource：`{time_to_failure: {distribution}, time_to_repair: {distribution}}` | D-051 | T-048（已實作、gate 已解除：validator 的 R3 與 TTF 的零時間檢查、des-go-0.3.0 的 kernel） |
| `lognormal`、`weibull` | distribution：`{type: lognormal, mean, cv}`、`{type: weibull, shape, scale}`；schema 只要求 > 0，候選範圍（0 < cv ≤ 10、0.1 ≤ shape ≤ 20，T-048 保留，D-075 APPROVED，H-022）屬 DISTRIBUTION_INVALID | D-056 | T-048（已實作、gate 已解除：任何分布位置，des-go-0.3.0） |
| `calendars` | 根層 array（1–100 個）：`{id, name?, period, shifts: [{start, end, label?}] (1–100), exceptions?: [{start, end, open}] (1–100)}`，時間以 model time_unit 表示、從 t=0 起、沒有時區 | D-052 | T-049（已實作、gate 已解除：validator 的形狀規則、R2、R4，des-go-0.4.0 的 kernel） |
| `calendar_id`、`shift_capacity` | resource：calendar 的 ID；`shift_capacity` 為 shift label → 0..100000 的 object（1–100 個 label），只能與 `calendar_id` 一起出現 | D-052 | T-049（同上，屬 `calendars` primitive） |
| `arrival.schedule` | source：與 `arrival.distribution` 二擇一；`{process: poisson｜deterministic, period, segments: [{start, rate}] (1–1000)}` 或 `{process: table, period, releases: [{at, quantity 1–10000}] (1–1000)}` | D-053 | T-049（已實作、gate 已解除：RELEASE_SCHEDULE_INVALID，des-go-0.4.0 的 kernel） |
| `batch` | process：`{min_size, max_size}`（1–10000）與 optional 的 `max_wait`（model time_unit） | D-054 | T-050 |
| `setups` | resource：`{initial?, default?, transitions?: [{from, to, distribution}] (1–9900)}`，`default` 與 `transitions` 至少一個 | D-055 | T-050 |
| `metrics[]` 的 `system_wip`、`flow_count`（`flow_id`）、`sink_count`（`sink_id`）、`period_throughput`（`period`） | 宣告式 generic 指標，只在宣告時計算 | D-058 | T-051 |

`metrics[]` 的每種宣告在 Result 中的位置（CB-22 剩餘部分；D-090 (b)，APPROVED，H-024，T-029）：`entity_cycle_time` → `/metrics/lead_time_minutes`；`sink_throughput` 沒有 `sink_id` → `/metrics/throughput`，有 `sink_id` 時 generic Result 沒有對應成員（unavailable；每個 sink 的件數是宣告式的 `sink_count`）；`resource_utilization` → `/metrics/resources/<resource_id>/utilization_mean`；`queue_wait` → `/metrics/queues/<key>`（key 為 process 的顯式 queue 的 ID，沒有時為 process 的 ID）；`failure_count`、`rework_count` → 同名的 generic 計數（不是 DORA）；上表的四種 → `/metrics/additional_metrics/<metric id>` 與 `replication_summaries[i].declared[<metric id>]`。宣告不決定 baseline／scenario 比較的範圍：比較涵蓋 Result 中所有的統計值（D-088 (b)）。

## 3. Distributions / units

P0 僅 constant(value)、exponential(mean)、triangular(min,mode,max)、empirical(samples_ref)。lognormal、calendar-dependent、mixtures 為 P1（lognormal 與 Weibull 依 D-056 提前到 P0：APPROVED，H-018，實作於 T-048；schema 自 T-046 phase 2 起接受 `lognormal(mean, cv)` 與 `weibull(shape, scale)`，T-048 之前以 PRIMITIVE_NOT_SUPPORTED 拒絕，§2.1）。自 T-048（des-go-0.3.0）起兩者在任何分布位置都可用（arrival、service、delay 與 resource 的 breakdowns）；semantic validator 另驗 lognormal 的 mean 解碼後 > 0 且 0 < cv ≤ 10、Weibull 的 scale 解碼後 > 0 且 0.1 ≤ shape ≤ 20（DISTRIBUTION_INVALID，D-075 APPROVED，H-022）；mean 與 scale 是時間參數，照 D-029 換算後超過 365 天為 LIMIT_EXCEEDED（D-063 (c)），cv 與 shape 沒有單位。兩者的樣本至少 1 tick（與 exponential 相同，D-033 視為恆 > 0）；抽樣公式見 SIMULATION_SPEC §4。

Schema 驗型別與非負值；semantic validator 另驗 min≤mode≤max、finite numbers、empirical 可解析且非空、unit 可轉換。interarrival 必須嚴格正值；zero service/delay 可以存在，但可達的 zero-time cycle 不能執行。時間換算和 rounding 由 SIMULATION_SPEC 固定。

APPROVED D-017：day 在 P0 是連續 24 小時。報告 example mean=16 min 在此語義下是 90 arrivals/day，不應當作 30/day 或暗示有 8-hour work calendar。full template fixture 用 synthetic mean=48 min 示範 30/day，其餘新增 Test/Deploy 參數也標 synthetic，不能當客戶事實。

## 4. Semantic validation order

執行與資料修改之前依序驗：JSON parse（拒 duplicate keys / NaN / Infinity）→JSON Schema→primitive support（T-046 phase 2，D-063 APPROVED，H-019）→limits→ID uniqueness→references→graph rules→distribution/domain semantics→bundle feasibility（D-050 的 R2–R4；R3 自 T-048 起，R2、R4 屬 T-049）→constraint checks→dataset bindings→scenario/patch policy→risk classification。invalid 回 issue code、JSON Pointer path、severity/message。

| Code family | 驗證 |
|---|---|
| IR_SCHEMA_INVALID | schema/version/type/unknown field 不符 |
| DUPLICATE_ID | 同 catalog 重複 ID；候選 profile 要求 nodes/resources 不共用 ID，避免 selectedEntityId 歧義 |
| UNKNOWN_REFERENCE | flow endpoint、entity_type、resource、metric、constraint、view position 指向不存在的 ID |
| ROUTING_INVALID | 非 decision 多路、sink 出路、概率負值或總和偏離 1（容差候選 1e-9） |
| NO_REACHABLE_SINK | source 沿正概率路徑找不到 sink；0% 分支允許存在但不會執行 |
| ZERO_TIME_CYCLE | 可達的 zero-minimum-time graph 含 cycle；不能靠 event cap 當正常 termination |
| RESOURCE_REQUIREMENT_INVALID | demand 超 capacity／重複 resource／負數；bundle feasibility 階段的 D-050 R3（T-048 實作於 `internal/model/validate/ir.go` 的 `feasibility`）：對有 breakdowns 的 resource 的 request units≠1（path `/nodes/<i>/requires/<k>/units`），一個 bundle 有兩個以上有 breakdowns 的 resource（path `/nodes/<i>/requires`，message 依 ID 順序列出它們）；一起故障的一組設備要建模為一個會故障的 resource |
| DISTRIBUTION_INVALID | triangular ordering、source zero interval、empirical 空值或非 finite、exponential mean 的十進位值解碼成 binary64 後下溢為 0（如 `1e-400`）；lognormal mean、Weibull scale 下溢為 0，cv 不在 (0, 10]、shape 不在 [0.1, 20]（D-056、D-075，T-048）；resource 的 time_to_failure 可能為 0 tick（constant 0、triangular min 0；empirical 有 0 tick 的樣本時在 dataset binding 階段回報；D-051，path 為該 distribution；time_to_repair 可以為 0） |
| DATASET_BINDING_INVALID | logical ref 沒有 immutable authorized snapshot、binding 不符 schema（含 D-034 字元集）或重複，或 hash／organization／version／logical_ref／unit／sample_count 不一致（§5.2） |
| VIEW_REFERENCE_INVALID | view position 不對應 canonical node |
| CONSTRAINT_FAILED | P0 outgoing_probability_sum 不成立 |
| JSON_INVALID | 解析失敗：非法 JSON、重複 member、非法 UTF-8、尾隨資料（D-023） |
| LIMIT_EXCEEDED | D-029 上限：文件 >2 MiB、metadata／domain_attributes 深度 >8 或 canonical（RFC 8785）形式 >16 KiB（不受空白影響）、分布參數換算超過 365 天（含 lognormal 的 mean、Weibull 的 scale 與 resource 中 breakdowns 的分布，T-048）、pointer 超過 32 層 |
| UNREACHABLE_NODE | **warning**：沒有 source 能到達的 dormant node，不阻擋 publish |
| DOMAIN_MISMATCH | DomainPack 階段：IR 的 `domain` 不是該 pack 的 domain（REQ-003 fail closed；未註冊的 domain 由 `domainpack.Registry` 回 unsupported） |
| PRIMITIVE_NOT_SUPPORTED | **error**，primitive support 階段（D-063 APPROVED，H-019）：模型用到本 build 尚未實作的產線流動成員（§2.1）；path 為該成員，message 固定、不引用模型內容 |
| BLOCKING_CYCLE | graph rules 階段（D-050 R1，T-047 實作於 `internal/model/validate/blocking.go`）：阻塞相依圖投影到共用 resource 分量後有 cycle；path 為 cycle 上每個有限 queue 的 `/nodes/<i>/capacity`。Holder 是至少需要一個 resource 的 process；holder A 對 process P 有阻塞邊，若 A 的出路（p > 0 的 flow，經 decision／failure／rework，再直接或經命名 P 的 buffer 的 queue）可到達 P 的有限 buffer；delay、sink 與無限 buffer 終止出路。需要同一 resource 的 process 合為一個分量（union-find）；以 strongly connected components 找出在 cycle 上的阻塞邊，每個標記它們的有限 queue 一個 issue。依批准文字不限 source 到得了的 node（保守）。Message 依 ID 順序列出 cycle 上的有限 buffer、process 與造成合併的共用 resource，並說明兩種修正（cycle 上至少一個 buffer 改為無限，或讓被擋的 process 不與擋住它的 process 共用 resource）；AI review 存的是固定句（`internal/ai/review`） |
| CALENDAR_INVALID | T-049 實作於 `internal/model/validate/calendars.go`（S(t) 與 next_fit 的整數 tick 算術在 `internal/model/calendar`，與 kernel 共用；細節 D-085，APPROVED，H-024）。distribution/domain semantics 階段（D-052）：period 不在 (0, 365 天]、shifts 未排序／重疊／超出 period、量化後不嚴格遞增、例外日未排序／重疊／超出 365 天、`shift_capacity` 的 label 不在該 calendar 或值超過 capacity（path 為出錯的 `/calendars/<c>/period`、`/calendars/<c>/shifts/<k>`、`/calendars/<c>/exceptions/<k>` 或 `/resources/<r>/shift_capacity/<label>`）；bundle feasibility 階段（D-050 R2）：所有 period 的最小公倍數 > 365 天（path `/calendars`），或 bundle 在 [0, L) 中沒有任何 tick 湊得齊（path `/nodes/<i>/requires`） |
| RELEASE_SCHEDULE_INVALID | T-049 實作於 `internal/model/validate/calendars.go`（D-085 (d)）。distribution/domain semantics 階段（D-053）：period 不在 (0, 365 天]；rate 型的第一段 start≠0、start 不嚴格遞增或 ≥ period、rate 全為 0 或超過每 tick 1 件；table 型的 `at` 不嚴格遞增或不在 [0, period) |
| BATCH_INVALID | distribution/domain semantics 階段（D-054）：min_size > max_size；min_size < max_size 卻沒有 max_wait（path `/nodes/<i>/batch`）；有限 buffer 的 capacity 小於 max_size（path `/nodes/<i>/batch/max_size`） |
| SETUP_INVALID | distribution/domain semantics 階段（D-055）：transition 的 from＝to 或 (from, to) 重複（path 該 transition）；有 setups 的 resource 在 requires 中 units≠1、一個 process 需要兩個有 setups 的 resource、批次 process 需要有 setups 的 resource（path 該 requirement） |
| STALL_RISK | **error**（D-050 採 A），bundle feasibility 階段（D-050 R4，T-049 實作）：bundle 中會故障的 resource 上班時，其他 resource 有 tick 不夠（只看週期部分，在兩者 period 的 LCM 內；R2 不成立或 R3 已拒絕的 bundle 不再檢查，D-085 (b)）；path `/nodes/<i>/requires`，message 依 ID 指出不夠的夥伴，並建議給會故障的 resource 夥伴的 calendar |

T-046 phase 2 對既有 code 的延伸（規則在各自的任務實作）：RESOURCE_REQUIREMENT_INVALID 加入 D-050 R3（bundle feasibility 階段；對有 breakdowns 的 resource 的 request units≠1，path `/nodes/<i>/requires/<k>/units`；一個 bundle 有兩個以上有 breakdowns 的 resource，path `/nodes/<i>/requires`）；DISTRIBUTION_INVALID 加入 lognormal／Weibull 的參數範圍（D-056）與 TTF 可能為 0 tick（D-051，path 為該 distribution）；UNKNOWN_REFERENCE 加入 `calendar_id`、setups 的 entity type、metric 的 `flow_id`／`sink_id`；DUPLICATE_ID 加入 calendar ID 與宣告式 metric ID 等於內建 `additional_metrics` key（D-058，path `/metrics/<i>/id`）；LIMIT_EXCEEDED 照 D-029 涵蓋所有分布參數，包括 resource 中的分布（D-056 原文對 mean／scale 超過 365 天寫 DISTRIBUTION_INVALID，與 D-029 不一致，見 D-063 (c)）。`testdata/contracts/semantic-*.json` 中標 `layer: semantic` 的 16 個 fixture 是這些規則的手推期望（code 與 path）；gate 未解除前 Go 測試要求它們只回 PRIMITIVE_NOT_SUPPORTED，解除後要求回期望的 code 與 path（`TestAgreesWithSharedFixtures`）。T-047 解除 `finite_buffer` 後，`semantic-blocking-cycle-retest.json` 與 `semantic-blocking-cycle-shared-resource.json` 回 BLOCKING_CYCLE（`/nodes/1/capacity`、`/nodes/2/capacity`），與手推的期望相同，fixture 未改。T-048 解除 `breakdowns`、`lognormal`、`weibull` 後，`semantic-breakdown-units.json`、`semantic-two-breakdown-resources.json`（RESOURCE_REQUIREMENT_INVALID，`/nodes/1/requires/0/units`、`/nodes/1/requires`）、`semantic-breakdown-zero-time-to-failure.json` 與 `semantic-lognormal-cv-range.json`（DISTRIBUTION_INVALID）回手推的期望，fixture 未改；`semantic-stall-risk.json` 也用 calendars，仍回 PRIMITIVE_NOT_SUPPORTED，R4 屬 T-049。T-049 解除 `calendars`、`release_schedule` 後，`semantic-calendar-shifts-overlap.json`（`/calendars/0/shifts/1`）、`semantic-shift-capacity-unknown-label.json`（`/resources/0/shift_capacity/night`）、`semantic-calendar-unknown.json`（UNKNOWN_REFERENCE，`/resources/0/calendar_id`）、`semantic-calendar-bundle-never-fits.json`（`/nodes/1/requires`）、`semantic-calendar-periods-too-long.json`（`/calendars`）、`semantic-release-schedule-first-segment.json`（RELEASE_SCHEDULE_INVALID，`/nodes/0/arrival/schedule/segments/0/start`）與 `semantic-stall-risk.json`（STALL_RISK，`/nodes/1/requires`）都回手推的期望，fixture 未改。

APPROVED D-023（實作於 `internal/model/validate`，T-004）：同一階段收集全部 issue，該階段有 error 就不進下一階段；issues 依 JSON Pointer 排序再依 code、message、severity；token 中只有 RFC 6901 array index 形式（`0` 或不以 0 開頭的數字串）以數值比較（任意長度、不溢位）並排在其他 token 之前，其餘逐 byte 比較，因此順序是全序、與發現順序無關。T-004 的實作解讀（需 owner 確認時可另立決策）：(1) 任何 source 經 p>0 flow 能到達、卻無法到達 sink 的 node 都回 NO_REACHABLE_SINK，不只檢查 source 本身；(2) 指向 source 的 flow 回 ROUTING_INVALID，因為 source 不接收 entity；(3) metric／constraint 只檢查引用存在，不檢查被引用 node 的類型。D-033：zero-time 以分布可能的最小 tick 判定（constant=0、triangular min=0；exponential 的 mean 解碼後 >0 才合法，engine 對 exponential 樣本至少給 1 tick；empirical 在 dataset binding 階段判定）。

Primitive support 階段（T-046 phase 2，實作於 `internal/model/validate/primitives.go`；D-063 APPROVED，H-019）：`validate.Uses` 依 D-023 的 path 順序列出模型中每個產線流動成員的位置（§2.1 的表；分布型別在任何分布位置都算，包括 resource 中的 breakdowns 與 setups），`validate.Checked` 是本 build 已實作規則的 primitive（目前為 `finite_buffer`（T-047）、`breakdowns`、`lognormal`、`weibull`（T-048）與 `calendars`、`release_schedule`（T-049））。對每個未實作的 use 回一個 PRIMITIVE_NOT_SUPPORTED error；這個階段在 schema 之後、limits 之前，有 error 時之後的階段都不執行，所以既有的階段永遠只看到它們完整檢查得了的模型。Engine 的 Compile 另有自己的 kernel 支援清單（`engine.kernelExecutes`，目前也是這六個），對 validator 已接受、kernel 還不執行的成員同樣回 PRIMITIVE_NOT_SUPPORTED（path 在 `/model` 之下）。T-047–T-051 各自在同一個任務中加入規則、kernel、engine_version 與 D-057 的 risk 規則後，才把自己的 primitive 加進這兩個清單。

unreachable dormant node 可 warning；無 reachable sink 的活躍路徑是 error。valid graph 仍可能大量 rework，因此 runtime budgets 必須存在。來源未明確要求跨 catalog IDs 全域唯一；上述 node/resource uniqueness 為 D-014 的候選限制。

## 5. Version、hash 與 empirical freezing

SOURCE：Draft mutable；ModelVersion immutable、保存 parent；run 保存 model_version_id、model_hash、patch、engine、seed、replications、config。

APPROVED D-006：`model_hash = sha256(JCS(IR))`，保留原 array order；JCS 指 RFC 8785，不用 Python sort_keys / Go json.Marshal 的預設結果冒充。拒 duplicate JSON keys、不合法 Unicode 及非 finite number；數字採 interoperable IEEE-754 profile，seed 限 0..2^53-1。model hash 含 views/metadata；它是完整版本內容 hash，不是純 simulation semantic hash。

empirical logical ref（例如 dataset:review_duration）不是可變遠端網址。每個 published version 附 immutable DatasetBinding manifest：logical_ref、dataset_version_id、content_hash、unit、sample_count。版本批准前完整凍結；run 複製同一 manifest，worker 驗實際 object bytes hash 後 preload samples。晚到的新資料只能產生新 dataset version / model version，不能改變已提交 run。

`effective_model_hash = sha256(JCS(apply(base.IR,scenario_patch)))`。dataset_manifest_hash 對按 logical_ref 排序的 manifest 做 JCS/SHA-256。config_hash 對完整公開 config 做 JCS/SHA-256；execution_limits、engine profile 也在 run input 中固定，不讓重試改設定。

JSON Schema 只驗 `sha256:` 的格式，不能驗內容真的相符；hash consistency 是 T-005/T-021 的產品測試，不是本包 schema validation 所保證的事。

### 5.1 Hash 定義（D-006、D-019、D-034；實作於 `internal/model/hashing`，T-005）

所有 hash 寫作 `sha256:` + 64 個小寫 hex。除 content_hash 外，都是 SHA-256(JCS(payload))。JCS 使用 `github.com/gowebpki/jcs`（ADR-014）；另以 RFC 8785 原文範例、V8 `JSON.stringify` 數字向量與 Python `rfc8785` 獨立實作交叉驗證（`testdata/hashing/`、`make spec-hashes`）。

| Hash | Payload |
|---|---|
| model_hash | 完整 IR 文件 |
| effective_model_hash | `internal/model/patch.Apply` 的 candidate（base IR 套 scenario patch）；baseline `[]` 時等於 model_hash |
| patch_hash | JSON Patch 陣列；`[]` 亦有 hash（= SHA-256 of `[]`）；非陣列拒絕 |
| config_hash | `{horizon_ticks, warmup_ticks, replications, seed, trace_level}`；duration 先以 `model.Ticks` 量化，故 `120 hour` 與 `5 day` 同 hash |
| dataset_manifest_hash | DatasetBinding 陣列依 logical_ref 的 UTF-8 bytes 排序（SQL：`COLLATE "C"`）；同一 logical_ref 綁兩次拒絕；無 empirical 時為 `[]` 的 hash |
| result_digest | Result 的 engine_version、metrics、replication_summaries、measurement_window、reproducibility（去掉 result_digest）；不含 run_id、status、trace |
| content_hash | 儲存物件**原始 bytes** 的 SHA-256，不 parse、不 canonicalize；snapshot 不內含自身 hash |

輸入規則：拒 duplicate member（含 escape 後相同者）、不合法 UTF-8、lone surrogate、超出 binary64 的數字（如 `1e400`）與多個 top-level value；另以 `encoding/json/jsontext` 獨立再 parse 一次，不只倚賴 jcs 的 parser。數字依 RFC 8785 以 binary64 值比較：只在 binary64 精度之外不同的文件（`9007199254740993` 與 `9007199254740992`、`0.30000000000000001` 與 `0.3`）同 hash，需要精確整數的欄位（seed、計數）由 schema 限制在 2^53−1 內。T-005 曾額外拒絕超過 2^53−1 的整數 literal，fuzz 發現這使 Canonical 拒絕自己的輸出（`1e17` 輸出為 `100000000000000000`，Go 重新 marshal 的 patch candidate 也是如此），已移除。錯誤訊息只帶 byte offset，不引用輸入內容（metadata 的 member name 可能是客戶資料）。

### 5.2 Dataset binding 階段（實作於 `internal/storage/datasets`，T-005）

`datasets.Resolve` 依序：(1) 每個 binding 過 schema（含 D-034 `^dataset:[a-z0-9_.-]+$`）、logical_ref 不得重複、IR 中每個 empirical `samples_ref` 都要有 binding（node 與 resource 的分布，後者為 breakdowns，T-048；path `/ir/resources/<i>/breakdowns/<time_to_failure｜time_to_repair>/distribution/samples_ref`）；(2) 經 tenant-scoped `Store` 取物件，**先比對 content_hash 再 parse**，再驗 organization、dataset_version_id、logical_ref、unit、sample_count 與每個 sample 的 365 天上限；(3) 以實際 sample 重跑 D-033：任一 sample 量化為 0 tick 的 empirical 視為零時間，source interarrival 或 resource 的 time_to_failure 可能為 0 回 DISTRIBUTION_INVALID（後者 D-051，T-048），零時間 cycle 回 ZERO_TIME_CYCLE（`validate.Bindings`）。Engine 的 Compile 對 manifest 與 datasets 做同樣的檢查，也涵蓋 resource 的分布（path 在 `/model/resources/...` 之下）。各階段有 error 即停（D-023）。

Issue path 指向 ModelVersion 形狀的文件：IR 為 `/ir/...`，manifest 為 `/dataset_bindings/<呼叫者給的 index>/...`。找不到或屬於其他 organization 的 dataset version 回同一訊息。在物件確認為本 organization 的凍結 snapshot 之前，訊息不含其大小或 hash；`Store.Open` 回 `io.ReadCloser`，Resolve 最多讀 MaxSnapshotBytes+1 bytes。限制：若 Store 實作未正確 scope，知道他租戶 dataset version ULID 的人仍可從「hash 不符」與「找不到」的差異推知該 ID 存在（不含內容或 hash）；Store 的 tenant scope 仍是主要防線。所有 issue 經 `model.NewIssue` 建立，message／path 一律符合 ValidationIssue schema（有效 UTF-8、≤2000／2048 字元）。Model 未使用的 binding 仍會被驗證並凍結（run 沿用 version manifest，scenario 可能已移除 empirical 分布）。Store 的 I/O 錯誤與 context cancellation 以 error 回傳，不轉成 issue、也不回假成功。Postgres／object storage 的 Store 實作屬 T-006／T-012。

## 6. JSON Patch profile / approval scope

SOURCE：RFC 6902、application/json-patch+json、array-index patch examples。APPROVED D-014：P0 只接受 add/remove/replace/test；move/copy、空字串 root path、不合法 `~` escape、超深 pointer 均拒絕。RFC 6901 pointer 只 decode 一次；authorization 不做字串 startsWith 判斷，必須比較解碼後 tokens。

manual draft patch 可操作 name、nodes、flows、resources、entity_types、constraints、metrics、views、metadata，但不能改 schema_version/model_id/domain/time_unit 或任何 server envelope identity。改 time_unit 需要明確 migration，P0 不隱式 reinterpret 已有 duration。

AI edit allowlist 比 manual 更小：name、nodes、flows、resources、entity_types、constraints、metrics；禁止 views/metadata/identity。AI scenario 僅既有 resources/*/capacity、nodes/*/arrival/distribution、nodes/*/service_time/distribution、nodes/*/delay_time/distribution、flows/*/routing/probability；不能增刪 topology 或創造 dataset binding。物件整段 replace 仍逐欄驗 full candidate IR，不因 parent path 被允許就略過子欄。

所有 patch 都在 copy 上依序 apply，整體成功才提交；任一 op/test 失敗都不產生 partial mutation。array index 只在 pinned immutable base 有意義，diff 顯示 logical entity ID。APPROVED D-020（實作於 `internal/model/patch`）：修改、取代或刪除 nodes／flows／resources／entity_types／metrics／constraints 的既有元素前，同一 patch 必須先 `test` 該元素的識別欄位（constraints 沒有 id，以 `node_id` 識別，為 T-004 解讀）；中間若有插入或刪除，先前的 test 失效。整個 catalog 陣列（如 `replace /resources`、`remove /entity_types`，或對已存在成員的 `add`）在仍有元素時一律拒絕（PATCH_TEST_REQUIRED），因為它不經 id test 就改掉所有既有元素；只能建立不存在或空的 catalog（2026-10-15 review 後的 fail-closed 解讀）。append（`/-`）與插入新元素不需 test。test 不受 allowlist 限制、不算 business mutation，也不能取代 expected base。allowlist 以解碼後 token 做前綴比對；scenario 模式（D-021，人工與 AI 相同；人工 scenario 的 createManualScenario 與 createSimulation 的 inline patch、AI scenario 的 review 都用 `patch.Scenario`，T-028 以 `TestTheAIScenarioModeTakesTheSameRules` 比較人工與 AI 的結果）只允許上列五種參數路徑與 D-057 的產線參數路徑（下段）；AI edit 另允許刪除同一 patch 刪掉之 node 的 `/views/2d/positions/{id}`（D-022），且 patch 套用後不得仍有同 id 的 node（刪掉又加回同 id 時座標必須保留）。Scenario patch 不得引入 base model 沒有綁定的 `samples_ref`（「不能創造 dataset binding」；把既有 ref 改用在另一個分布可以）。Patch 文件本身超過 2 MiB 時在 parse 前回 LIMIT_EXCEEDED。patch 錯誤碼：PATCH_SCHEMA_INVALID、PATCH_PATH_INVALID、PATCH_PATH_FORBIDDEN、PATCH_TEST_REQUIRED、PATCH_TARGET_MISSING、PATCH_TEST_FAILED，路徑指向 patch 文件（如 `/3/path`）。刪除 node/resource 必須同時修復 references，否則 invalid。

APPROVED D-057（T-046 phase 2 實作於 `internal/model/patch`）：manual 與 AI edit 的 allowlist 加入根層 `calendars`；`calendars` 與其他 catalog 一樣適用 D-020（改元素前先 `test /calendars/<i>/id`，有元素時不可整段 replace）。Scenario allowlist 另加：`nodes/*/capacity`（replace、add、remove：有限↔無限）；以下只接受 `replace`：`resources/*/breakdowns/time_to_failure/distribution`、`resources/*/breakdowns/time_to_repair/distribution`、`nodes/*/arrival/schedule/segments/*/rate`、`nodes/*/arrival/schedule/releases/*/quantity`、`nodes/*/batch/min_size`、`max_size`、`max_wait`、`resources/*/setups/transitions/*/distribution`、`resources/*/setups/default`、`resources/*/calendar_id`（candidate 必須指向既有 calendar）、`resources/*/shift_capacity/<label>`、`calendars/*/shifts`、`calendars/*/exceptions`。凡是允許修改的分布都可換成 lognormal／Weibull。新增或刪除 breakdowns、setups、calendars、schedule、batch、transition、segment、release 與 `shift_capacity` 的 key（label），以及改 period、segment start、release 時刻、calendar 名稱，都是 PATCH_PATH_FORBIDDEN，要走 draft→publish。`replace calendars/*/shifts` 可以移動、增加或刪除 shift（「加一班」），也可以新增、刪除或改名 shift label（D-063 (b) 選項 B，owner 依建議選定，H-019；T-049 在解除 calendars 的 gate 前放寬 `afterApply`，`TestScenarioMayChangeShiftLabels`）：被 `shift_capacity` 參照的 label 若被刪除或改名，candidate 的 `shift_capacity` 就參照了該 calendar 沒有的 label，由 candidate validation 回 `CALENDAR_INVALID`（path `/resources/<r>/shift_capacity/<label>`，D-052；`TestScenarioDropsAReferencedLabel`），scenario 不能建立；沒被參照的 label 不影響 kernel（沒列出的 label 用全部 capacity）。shifts 與 exceptions 只接受 `replace`，所以替一個 shift 加上或拿掉 label 是 replace 該 shift 或整個 shifts，不是在它之下的 add 或 remove。T-049 之前（phase 2 的暫行解讀）每個 calendar 的 shift label 集合必須不變。Candidate 照舊完整驗證，所以 gate 解除前這些 scenario 都回 PRIMITIVE_NOT_SUPPORTED。「不能創造 dataset binding」也涵蓋 resource 中的分布。D-057 的 server risk（縮小或新增有限 buffer、任何 breakdown／換線／calendar／投入／批次參數至少 medium，擴大 buffer 或改回無限為 low）在解除對應 gate 的任務中加入 `internal/ai/review`；在那之前 candidate 已被 gate 拒絕，不會計算 risk。T-047 已加入 queue `capacity` 的規則（`review.ServerRisk`：base 與 candidate 中同一個 queue 的 capacity 變小，或由無限改為有限，至少 medium；變大或改回無限為 low），造成 BLOCKING_CYCLE 的 scenario 在 candidate validation 即為 invalid；T-048 加入 breakdowns 的規則（同一個 resource 的 breakdowns 新增或刪除（edit），或 time_to_failure／time_to_repair 的分布有任何不同（依值比較，任一方向），至少 medium；只改 arrival、service、delay 的分布（含 lognormal、Weibull）仍為 low），R3 不成立的 candidate 即為 invalid；proposal 的 expected effects 可以引用的數字也包括 breakdowns 與 lognormal、Weibull 的參數。T-049 加入 calendars 與投入排程的規則（`review.ServerRisk`：calendar 新增、刪除，或 period、shifts（含 label）、exceptions 有任何不同（名稱除外）；resource 的 `calendar_id` 改變或移除、`shift_capacity` 不同；source 的 release schedule 新增、刪除或任何成員（rate、quantity、period、segment、release）不同，任一方向，都至少 medium），R2、R4 不成立的 candidate 即為 invalid（CALENDAR_INVALID、STALL_RISK）；expected effects 可以引用的數字也包括 calendar 的時間、上班人數與排程的 rate、quantity。AI proposal 不能新增 calendar：ID allocation pool 不配發 calendar ID，candidate 中 base 沒有的 calendar 一律 `ID_POOL_VIOLATION`（D-083，APPROVED，H-024；T-046 review I3）。AI 的 scenario 規則（`aicontext.modeRules`、prompts/scenario `scenario-0.5.0`，T-049）列出 buffer、breakdowns、calendar（`calendar_id`、`shift_capacity/<label>`、shifts、exceptions）與投入排程（segment rate、release quantity）的參數、只能 replace，以及 lognormal 與 Weibull 的參數範圍。

## 7. View / canonical event schema

2D positions 放 views.2d.positions[node_id]；3D layout_mode=derived_from_2d。views 不可含另一份 nodes/flows。positions 缺省時 DomainPack.DefaultViews 生成；選取與 playback 使用同一 ID。

canonical-event.schema.json 保留 case_id/event_type/timestamp 必填，stage recommended，其餘 optional。timestamp 是 RFC3339；跨時區先正規化再排序，不能猜無時區時間。duration_ms 與 timestamp-derived duration 不一致時產生 data-quality issue，選用政策要由 domain inference 明列。CSV 的低 confidence mapping 和 dataset 推論永不直接更改 canonical Twin。

APPROVED D-059（T-046 phase 2）：新增 optional 的 `end_timestamp`（RFC 3339）、`shift`（≤ 64 字元）與 `product`（≤ 128 字元，對應 entity type；多產品的 log 必填）。`end_timestamp` ≥ `timestamp`、與 `duration_ms` 一致、多產品時必有 `product` 都是 data-quality 規則（T-017／T-052），不是 schema。停機紀錄的 `case_id` 是設備 ID。T-017（D-070 (a)，PROPOSED）已把這三個欄位加入 mapping 的 target field：`import-mapping.schema.json#/$defs/TargetField`，人工 mapping 與 `ai-mapping.schema.json` 共用，`internal/ai/aicontext.TargetFields` 以 test 對照。`end_timestamp` 不早於 `timestamp`、與 `duration_ms` 的差小於 1 ms 由 T-017 的 validation 檢查（D-072 (b)），不合者目前為 invalid row；上一段把 duration 不一致稱為 data-quality issue，兩者的差異列為 D-072 (b) 的選項，請 owner 決定（review 第 1 輪 I2）；多產品時必有 `product` 仍屬 T-052。

## 8. 相容性與擴充

未知 schema_version 拒絕，不默默盡力解析。v0.1 新欄位／新 node 或 constraints 需更新 schema、fixtures、Go/TS contract tests 和 version migration 策略。Future DomainPack 可使用既有 generic primitives 與 domain attributes；新增核心 primitive 應有獨立 ADR，不能以 arbitrary script 绕過 contract。H-016（ADR-025）需要的有限緩衝與阻塞、resource 故障、班表、隨時間變化的投入與批次即屬此類，由 T-046 提出 ADR 與 decisions（2026-10-20 APPROVED，H-018），實作前上表的 P0 限制（FIFO、不加 calendar/priority/preemption）照舊。提案：[ADR-026](adr/ADR-026-line-flow-primitives.md) 與 D-047–D-057（IR 的 optional 新欄位與相容性策略見 D-047），domain 規格見 [ELECTRONICS_LINE.md](domains/ELECTRONICS_LINE.md)；2026-10-20 APPROVED（H-018）。T-046 phase 2 已完成 schema 變更（§2.1）：依 D-047 A，schema_version 維持 `0.1`，所有新成員都是 optional，閉合物件讓不認得它們的舊 reader fail closed；「未知 schema_version 拒絕」不變。Schema_version 因此不表示模型用到哪些功能，要看 IR 內容（`validate.Uses`）。

補充（D-010）：單純回到前一節點的 flow 不自動等於 domain rework。需要可辨識的 rework node 或已批准的 DomainPack mapping 才計數；不能以 f_retry 之類的字串名稱在 generic kernel 硬編語義。
