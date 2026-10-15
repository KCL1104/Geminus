# 規格包驗證報告

**交付版本：handoff-0.1.0｜檢查日期：2026-10-15｜結論：規格包的有限靜態檢查通過；產品尚未實作與驗收。**

本報告只記錄本次實際執行的檢查。不把 schema-valid 等同於 semantic-valid、authorized 或 production-ready，也不把參考算術當成 Go DES engine 的測試結果。

## 實際已執行

| 檢查 | 結果 | 範圍與證據 |
|---|---|---|
| JSON Schema metaschema | PASS，13 份 | Draft 2020-12；`contracts-check.json` |
| Schema 正例 | PASS，20 筆 | 每筆實例符合指定 schema；hash shape 正確不表示 hash 值已驗證 |
| Schema 反例 | PASS，21 筆 | 每筆被指定 schema 拒絕；包括非法 AI 欄位、patch 操作、缺欄位與格式 |
| 有限語義反例 | PASS，7 筆 | 先確認 schema-valid，再由 fixture linter 拒絕引用／概率／容量／零時迴圈等；不是 production validator |
| Schema / OpenAPI 引用 | PASS，663 個 `$ref` 出現位置 | 使用本地 registry 解析，不需要連線到 `twin.example` |
| OpenAPI operation inventory | PASS，41 個 | 檢查 path parameters、operation IDs、tenant header、角色標記、response 與規劃清單 |
| OpenAPI 結構 model | PASS，有限檢查 | 本環境使用 FastAPI 的 OpenAPI Pydantic model；**不是完整獨立 OpenAPI validator** |
| AI corpus | PASS，70 筆可解析案例 | Modeler 50、Import Mapper 10、Analyst 10；檢查案例格式與 ID，未呼叫 LLM |
| 任務與追溯 | PASS | 42 個 task、無循環 dependency graph、21 條 requirement traceability、17 個 pending proposed decision；0 個任務標為完成 |
| Markdown 文件 | PASS | 相對連結存在、code fences 配對；精確數量见 `docs-check.json`。不代表內容經產品 owner 批准 |
| SQL 靜態 inventory | PASS，有限檢查 | 20 個 table、其中 19 個 tenant-owned；只檢查結構標記、RLS／immutable／budget 相關文字，**未解析或執行 SQL** |
| Deterministic fixture 算術 | PASS，2 個案例／20 個期望值比較 | 以單服務台的有限 recurrence 檢查等待、lead time、throughput、utilization；不是通用 simulation runtime |
| RNG 參考向量 | PASS | 5 個 SplitMix64 output、3 個 replication seed；只是候選算法的 Python 算術，不是 Go 實作／跨平台驗證 |
| 檔案語法 hygiene | PASS | Python scripts 可 compile、Bash script 通過 `bash -n`、JSON 與 JSONL 可嚴格解析 |
| 原始報告副本 | PASS | `sources/ORIGINAL_REPORT.pdf` 的 SHA-256 與上傳 PDF 相同，見 `environment-and-hygiene.json` |

詳細機器紀錄：`contracts-check.json`、`planning-check.json`、`docs-check.json`、`sql-inventory.json`、`reference-arithmetic.json`、`environment-and-hygiene.json`。`spec-check.log` 保留交付前最後一次執行輸出。

## 明確未執行

| 項目 | 狀態與下一個 gate |
|---|---|
| 完整獨立 OpenAPI validator / SDK codegen | **NOT RUN**。所需獨立 validator 在環境中不可用；安裝嘗試因連線／DNS 不可用而失敗。T-003 應加入正式 validation 與所選 generator 的外部 `$ref` 相容測試 |
| PostgreSQL SQL parser | **NOT RUN**。沒有 PostgreSQL parser；regex inventory 不能證明語法正確 |
| Migration up / down / up | **NOT RUN**。沒有 PostgreSQL server / `psql`；草案須先在一次性測試 DB 執行，再由人類 review |
| 真實 RLS／角色／pool isolation／FK／trigger | **NOT RUN**。附有 smoke SQL 與 guarded runner，但沒有實際測試資料庫；不能聲稱隔離已成立 |
| Go domain / API / DES／race／cancel／lease／idempotency | **NOT RUN**。規格包沒有 Go application，僅有任務、合約與 fixture |
| Next.js／React Flow／PlayCanvas／browser E2E | **NOT RUN**。規格包沒有 frontend application |
| SimPy differential tests | **NOT RUN**。尚未建立 independent reference harness，算術 fixture 不是替代品 |
| Live AI eval／品質分數／token 成本 | **NOT RUN**。沒有選定 provider、model、批准資料政策，也沒有真實 LLM 呼叫；70 筆案例不等於通過 70 次 AI eval |
| JCS／model hash／patch hash／result digest | **NOT RUN**。fixture 中全零 SHA-256 是格式占位；不是有效內容雜湊。Go implementation 後才建立真實 hash vectors／golden digest |
| 效能、負載、security scan、backup restore、staging／production | **NOT RUN**。原報告效能數字仍是候選工程目標，不是量測成果 |

Migration 中的 role、grant、policy、deferred trigger、manifest 與 retry budget 皆是待執行的候選設計。即使 static inventory 通過，也不得跳過 DB 實測、真實 runtime-login 測試、API RBAC 或 worker concurrency 測試。

## 可重跑的命令

```sh
python -m pip install -r requirements-spec.txt
make spec-check
python -m py_compile scripts/*.py
bash -n scripts/run_db_tests.sh
```

`make spec-check` 不執行 DB、live AI 或 production 操作。FastAPI 結構 model 是可選的額外檢查；沒有該套件的環境會記錄未執行，而不是冒充完整 OAS 驗證。正式 OAS validator 必須在 contract implementation task 加入。

DB 測試另依 `db/README.md`，僅對明確批准、fresh、名稱以 `dt_test_` 開頭的一次性測試 DB 執行；不得把交付的 down migration 用在 production。

## 交付前的工具版本

Python 3.13.5；jsonschema 4.26.0；referencing 0.37.0；PyYAML 6.0.3；可選 FastAPI 0.128.2。這是本次觀測到的環境版本，不是推薦最新版本。未鎖定 Go / Node / frontend framework / database provider 版本；由 bootstrap task 記錄選擇與 lockfiles。

原始報告 SHA-256：

```text
0dd63e4fcddbc3159e001970e9dfb624dfd62839afb5f0ef880bd63f5109be16
```

## 人類決策與 release 邊界

17 個 engineering proposal 仍為 `proposed`，`approved_by` 與 `approved_at` 皆空。它們讓候選合約能互相對齊，但不是已獲批准的產品決策。Coding agent 可以依任務開發測試／spike；遇 gate 必須取得對應決策，不能自行接受。

只有實際產品程式碼、正反向與併發測試、真實 DB／worker／UI／AI 測試、source traceability、資料政策與人類 release approval 都達成時，才可依 `docs/TEST_PLAN.md` 與 `docs/IMPLEMENTATION_PLAN.md` 宣告對應 milestone 完成。
