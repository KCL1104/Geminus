# 給 Coding Agent 的第一個工作指令

以下文字可作為首輪任務。不要要求 agent 一次完成全部產品。

```text
讀取本 repository 的 AGENTS.md、README.md、docs/DECISIONS_AND_TBD.md、
docs/IMPLEMENTATION_PLAN.md 及 planning/tasks.json，並依 sources/SOURCE_MAP.md
定位原始需求。所有 proposed decision 都尚未獲批准；不可自行批准或改為 accepted。

先盤點 repository，再執行 make spec-check，保留完整結果。
這是規格包，不是已實作系統；缺少 Go / Next.js / PostgreSQL runtime 不算成功。

首輪只處理 T-001、T-002：
1. 列出 P0 contracts 的阻擋項及需要人類確認的 decision IDs。
2. 建立不含 production secret 的 repo / CI / toolchain skeleton。
3. 保留 schema、OpenAPI、SQL source of truth，不另創 React Flow 專用 domain schema。
4. 未確認的技術與 provider 用 interface + 明確 disabled configuration；不得默認開真實 AI。
5. 不要先做 3D 或漂亮 dashboard，不要新增 P1 connector / broker / autonomous tools。

下一輪先完成 T-003 / T-004 的 contract + validator tests；需要定案的範圍先提出決策。
每次只選 dependencies 已完成的一個 bounded task，測試後交付：
changed files、requirement IDs、decision IDs、實際執行命令、通過/失敗/未執行項目、
剩餘風險及下一個 ready task。不得把 mock、TODO、skip 或預估數字當完成證據。
```
