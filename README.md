# AI-native Digital Twin — Coding Agent Handoff

**套件版本：handoff-0.1.0｜狀態：實作規格與合約草案｜日期：2026-10-15**

本套件依據 [54 頁原報告](sources/ORIGINAL_REPORT.pdf)，將需求、架構、資料契約、安全與開發任務拆分成 repository-ready 檔案。它**不包含已完成的 Go API、DES engine 或 Next.js application**。合約檢查通過不等於產品驗收通過；實際執行情況見 [VALIDATION_REPORT.md](checks/VALIDATION_REPORT.md)。

## 開始使用

先讀 [AGENTS.md](AGENTS.md)，再讀 [決策與 TBD](docs/DECISIONS_AND_TBD.md)。將這個目錄作為新 repository 起點；整合到既有 repository 時先比對同名檔案，不要覆寫既有 AGENTS.md 或 migration history。

```sh
python -m venv .venv
. .venv/bin/activate
python -m pip install -r requirements-spec.txt
make spec-check
```

以上只檢查規格包。

Bootstrap（T-002）後的 toolchain 版本見 [ADR-011](docs/adr/ADR-011-toolchain-bootstrap.md)；實際執行結果見 [BOOTSTRAP_VALIDATION.md](checks/BOOTSTRAP_VALIDATION.md)：

```sh
make backend-test backend-race backend-build   # Go 1.27.3 (go.mod toolchain)
npm ci --prefix apps/web                       # Node 24.21.0 (apps/web/.nvmrc)
make frontend-test frontend-build
```

`cmd/api`、`cmd/worker` 目前只驗證設定後以 exit 2 回報 NOT IMPLEMENTED，不是可服務的 application。

TypeScript 契約型別 `apps/web/lib/contracts/` 由 `make ts-contracts` 從 `schemas/` 產生，不可手改；`make spec-check` 會檢查是否過期。Go 型別在 `internal/model`、`internal/simulation`，以 contract tests 對 schema 做欄位與 round-trip 比對。

Go / frontend / DB / live AI 測試要由 coding agent 在對應任務完成後建立並執行；不得把缺少程式碼或未執行測試當作成功。

## 七組交付

| 類別 | 主文件 | 機器可讀／支援檔案 |
|---|---|---|
| Coding Agent rules | [AGENTS.md](AGENTS.md) | Makefile、scripts/、[啟動指令](START_HERE.md) |
| PRD / Architecture | [PRD](docs/PRD.md)、[Architecture](docs/ARCHITECTURE.md) | docs/adr/ |
| Twin IR / Simulation | [Twin IR](docs/TWIN_IR_SPEC.md)、[Simulation](docs/SIMULATION_SPEC.md) | schemas/、testdata/golden-models/、testdata/simulation/ |
| AI Spec | [AI_SPEC.md](docs/AI_SPEC.md) | prompts/、testdata/ai-evals/ |
| API / Database | [API_DATABASE.md](docs/API_DATABASE.md) | openapi/openapi.yaml、schemas/、migrations/、db/ |
| Security / Test Plan | [Security](docs/SECURITY.md)、[Test Plan](docs/TEST_PLAN.md) | db/tests/、testdata/contracts/ |
| Implementation Plan | [IMPLEMENTATION_PLAN.md](docs/IMPLEMENTATION_PLAN.md) | planning/tasks.json、planning/traceability.csv |

## 重要的來源邊界

報告原則標為 SOURCE；補足可實作細節的候選標為 PROPOSED；原本的未決事項保留 TBD。ID 格式、scenario approval 回傳物件、dirty draft、tick/RNG、empirical dataset freezing、import commit、缺值與 confidence、lease/RLS 都存在原報告未完整定義的部分，詳見 decisions register。

機器契約目前代表**候選的一致版本**，不是聲稱原報告已定義所有欄位。受影響的工程提案經人類確認前，可以開發測試與 spike，但不可凍結 production data contract 或宣稱 ready for production。

## 交付與重現

[來源索引](sources/SOURCE_MAP.md) 保留頁碼與原始檔 hash；[validation report](checks/VALIDATION_REPORT.md) 記錄實際跑過與未跑過的檢查。`MANIFEST.sha256` 是交付檔案校驗清單；不包含它自己的 hash。

## 授權

專有軟體，保留所有權利（[LICENSE](LICENSE)，owner 決定 H-015）。未經著作權人書面同意，不得使用、複製、修改、散布或作為服務提供。第三方元件依其各自的授權，見 [THIRD_PARTY_NOTICES](docs/THIRD_PARTY_NOTICES.md) 與 ADR-011、ADR-021 的授權欄。
