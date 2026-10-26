# Hackathon 計畫：Meta Global AI Developer Hackathon（H-029）

依據：H-029（2026-10-26，owner 已錄取 Indie 組並選定以下範圍與流程）。本檔是比賽期間的工作計畫，不改變 P0 的範圍與已批准的決策；比賽後恢復 H-003 的審查流程，延後的任務照 `planning/tasks.json` 進行。

## 比賽

- 線上，2026-10-26 開始，**2026-11-04 21:00 ET（2026-11-05 02:00 UTC）截止提交**；11-04 至 11-08 評審，11-09 公布。
- 評分：Impact 30%、Use of Meta AI Tooling 25%、Technical Depth & Execution 20%、Demo 25%（可運作的 demo 與影片）。
- 主題：「Push the Frontier」點名 manufacturing 與「understand and control a physical system」；Geminus 的電子組裝／測試線數位分身直接對題。
- 規則重點（Official Rules，2026-10-26 取得）：作品須為參賽者的原創、權利屬於參賽者；開源元件須遵守授權；提交後參賽者保有所有權，但授予主辦方永久、不可撤銷、免費、非專屬的使用授權；活動中分享的內容不視為機密。規則沒有寫「必須從零開始」，提交時說明哪些是比賽期間做的。

## Demo 的內容（H-029 (2)）

1. 電子產線範本（SMT、AOI、ICT、燒機批次、包裝）與 synthetic 資料。
2. **照片 → 模型**：上傳產線照片或平面圖，SAM 3.1 找出機台與輸送帶，Muse Spark 判斷工作站與流向，產生模型 proposal；伺服器驗證、算風險，人工批准才生效。
3. **2D 編輯器**：在畫布上檢視與調整模型。
4. **用自然語言提出修改**：例如「燒機改成每批 48 片」，Muse Spark 產生 scenario proposal，畫面並列差異、AI 的假設、伺服器算的風險。
5. **模擬與比較**：執行模擬，Marey 圖比較 baseline 與 scenario，加上主要指標。

不變的規則：AI 只提出建議、不能批准或直接改模型；風險與驗證由伺服器計算；租戶隔離；資料不足時回 needs_input；不捏造任何結果或數字。

## 切片與相依

| 切片 | 任務 | 相依 | 預定完成（UTC） |
|---|---|---|---|
| Meta provider（Muse Spark） | T-055 | T-024、T-025、T-053 | 10-28 |
| 電子產線範本與指標 | T-051 切片 | T-050 | 10-28 |
| Synthetic 產線資料 | T-052 切片 | T-051 | 10-29 |
| 2D 編輯器 | T-015 切片 | T-045 | 10-30 |
| Container 與部署 | T-039 切片 | owner 的雲端帳號與 Auth0 tenant | 10-31 |
| 照片 → 模型 proposal（SAM 3.1） | T-056 | T-055、T-051 | 10-31 |
| 自然語言修改與 proposal 審閱畫面 | T-031 切片 | T-055 | 11-01 |
| 模擬與 Marey 比較畫面 | T-032 切片 | T-031 切片 | 11-02 |
| 部署、demo 腳本、影片、提交 | T-057 | 以上全部 | 11-04 |

程式在 11-03 23:59 UTC 凍結；11-04 只做部署確認、影片與提交文字。

## 流程（H-029 (4)，比賽期間）

- 每個切片先寫測試，再實作；安全規則、反向測試與 assertion 照舊，不加 skip。
- 每個切片做一輪獨立審查；High／Medium 修完並由 coordinator 驗證即可完成，Low 記為比賽後的後續。
- 延到比賽後：其餘 P0 任務、第七張決策頁（D-096、D-097、D-110–D-116）、T-053 的 Anthropic live 執行（執行前先回答 D-116）。

## Owner 要做的事

| 時間 | 事項 |
|---|---|
| 現在 | 在 Meta Model API 建立 key（Standard mode），加到 Claude Code 的環境變數 `GEMINUS_META_API_KEY`（agent 只檢查名稱，不讀值；新開的 session 才看得到） |
| 10-29 前 | 選定雲端廠商（建議 Render：Docker 服務與 managed Postgres 設定最少）並建立帳號、連結 GitHub repo；建立 Auth0 的 dev tenant |
| 10-31 前 | 在雲端平台的 secret 設定填入 Meta key、Auth0 與 session 的 secret（agent 提供清單，secret 不進 repo） |
| 11-04 | 看過 demo 影片（需要的話錄旁白），以 Indie 組 Representative 身分在 Cerebral Valley 提交 |

## 資料與花費

- Meta 用 Standard mode（官方 pricing 頁：Standard mode 的內容不拿去訓練）；只送 synthetic 資料、demo 圖片與使用者輸入的文字；log 不存原始 prompt 與回應。
- 花費沿用 T-053 的程式內上限與開關；錄取者有 150 美元的 Meta API 額度（10 天）。官方價格（2026-10-26）：`muse-spark-1.3` 每百萬 token 輸入 1.25、輸出 4.25 美元；`sam-3.1` 每千張圖 2.50 美元。
