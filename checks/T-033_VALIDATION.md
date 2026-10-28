# T-033 驗證報告：PlayCanvas 唯讀 3D 檢視（hackathon 切片）

**日期：2026-10-28｜範圍：H-038 排定的產品完整度第三項；模型的唯讀 3D 投影，與 2D 共用 IR、entity ID 與選取｜需求：REQ-017（播放屬 T-034，不在本切片）、REQ-022、REQ-023｜驗收測試：TEST-025（3D 端）｜依據：ADR-008（PlayCanvas，SOURCE_LOCKED）、docs/ARCHITECTURE.md §7、DESIGN.md（一套色盤、2D／3D segmented）**

`planning/tasks.json` 的 T-033 **仍是 `not_started`**：它依賴的 T-015 仍是 `in_progress`，`check_decisions.py` 的規則要求依賴為 `review` 或 `done`（T-015 也遇過同一規則）。狀態留給 coordinator。

## 1. 結論

| 項目 | 交付 |
|---|---|
| 依賴 | `playcanvas` 2.23.2（MIT，2026-10-09，滿兩週），連同兩個型別宣告套件；ADR-011、THIRD_PARTY_NOTICES；`npm audit --omit=dev` 0 |
| 場景（純計算） | `features/twin-3d/scene.ts`：每個步驟一個物件、ID 就是 IR 的 ID，位置取自 2D 畫布的同一個 `canvasLayout`（`layout_mode: derived_from_2d`），每個 flow 一條線；給了結果時，process 依它需要的資源的平均使用率上色，queue 依平均排隊長度升高；`pick` 以射線找出點到的步驟 |
| 繪製 | `features/twin-3d/engine.ts`：PlayCanvas 只畫 scene、只有 orbit 相機（轉、移、縮放、重設）與點選，不寫任何東西；顏色全部取自 design tokens（先由瀏覽器轉成 `#rrggbb`：build 後的 CSS 把 `#ffffff` 寫成 `#fff`，PlayCanvas 會讀成藍色） |
| 元件 | `features/twin-3d/viewer.tsx`：動態載入 PlayCanvas；hover 顯示名稱；點一下選取（共用 T-014 的 selection store）；鍵盤：方向鍵轉、+／− 縮放、0 重設、Escape 取消選取；無法執行（沒有 WebGL、載入失敗）時只說明，其他不受影響 |
| 模型頁 | 編輯器工具列右側的 2D／3D segmented（`ui/segmented.tsx`，React Aria ToggleButtonGroup，DESIGN.md 的 2D／3D 尺寸）；3D 時沒有「新增步驟」與「顯示所有步驟」（3D 不 author），屬性面板照常顯示選取的步驟 |
| 結果頁 | 預設關閉的「以 3D 檢視產線與情境執行中忙碌的工作站」；打開時才載入，使用率與排隊長度取自情境執行的 Result |

沒有新的 API、schema 或 migration；3D 不寫回任何資料。

## 2. 設計重點

- **同一份 topology**：場景每次 render 由 IR 算出，不存、不改；位置用 2D 的 `canvasLayout`，所以 AI 加入步驟後的重排（T-057 demo 修正）在 3D 也一樣。
- **選取**：3D 的點選呼叫與 2D 相同的 `store.select(id)`；2D 選取的步驟在 3D 以 Graphite Ink 顯示，3D 中按 Escape 也會清掉 2D 的選取。
- **無障礙**：canvas 是 `role="img"`，名稱說明步驟數，`aria-describedby` 指向操作說明與目前選取；可以 Tab 到 canvas、用鍵盤轉動與縮放。用鍵盤**選取**步驟要在 2D（每個步驟都在 Tab 順序裡），3D 是投影，不是唯一的入口。
- **失敗不擋路**：PlayCanvas 只在打開 3D 時載入（build 後一個 2.5 MB、gzip 644 KB 的 chunk，不在任何頁面的初始載入中）；`new pc.Application` 或載入失敗即顯示說明。
- **截圖**：編輯器不在截圖 baseline 裡；結果頁的 3D 區塊只在真的結果頁出現（UI gallery 不傳），所以 32 張 baseline 不變。

## 3. 測試

| 測試 | 內容 |
|---|---|
| `tests/twin-3d.test.tsx`（4 個） | 出貨範本：物件 ID 與 IR 相同、位置是 `canvasLayout` 的換算、線與 flows 相同；結果只改顏色與高度，位置不變、使用率是需要的資源中最高的；射線從上方與側面命中、錯過與反方向為 null；沒有 WebGL 時說明、沒有 canvas |
| `tests/results-view.test.tsx` 3D 區塊 | 預設關閉、沒有 canvas；打開後（沒有 WebGL）顯示說明、表格仍在 |
| `apps/web-e2e` `the editor's 3D view` | 真的 Chromium（有 WebGL）：2D 選取後切到 3D，沒有新增與 fit、沒有 React Flow；場景出現、載入結束、「已選取：Burn-in」；Tab 到場景、方向鍵、Escape 清掉選取；axe 無違規；切回 2D；沒有 page error |
| 目視 | 本機 Playwright 截圖（淺色、深色）：深色與淺色背景各自正確，選取的步驟為 Graphite Ink；第一版背景是藍色（`#fff` 的問題）與 queue 幾乎看不見，已修正 |

## 4. NOT RUN

- **T-034（播放）**：沿時間播放 sampled trace 不在本切片。
- **觸控手勢**（兩指縮放、旋轉）：只測滑鼠、滾輪與鍵盤。
- **獨立 review**：見之後的紀錄。

## 5. 命令與結果（2026-10-28，本機 WSL2；Node 24.21.0、Playwright 1.56.1）

| 命令 | 結果 |
|---|---|
| `npm install playcanvas@2.23.2 --save-exact --ignore-scripts` | 3 個 lock entry；`npm audit --omit=dev`：0 vulnerabilities |
| `make frontend-test` | exit 0：eslint、tsc、vitest **49 files、1080 tests passed** |
| `npx playwright test tests/editor.spec.ts -g "3D view"` | 1 passed（真的 Chromium，WebGL 可用） |
| `make spec-check` | exit 0 |
| `make e2e-test` | 見提交後的紀錄 |
