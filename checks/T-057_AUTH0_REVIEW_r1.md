# T-057 Auth0 步驟：獨立審查第 1 輪（重建）

**依 coordinator 保存的結論重建。** 獨立 reviewer 的原始報告檔沒有保存：寫它的 session 在帳號的用量上限停止，檔案沒有 commit。本檔依 coordinator 交接文件（2026-10-27）記下的結論重寫，**不是** reviewer 的原文；行號是審查時（e44ccbc 之前的 Auth0 commits）的位置。

**結論：CHANGES REQUESTED。** 依 H-029（hackathon 模式），M1、M2 修完並由 coordinator 驗證即可部署；Low 可延後。

## Medium

- **M1**（`deploy/railway/railway_deploy.py` 約 173-174、616-627、884-888；rehearsal 的替身約 474-476）：CLI JSON 的 hook 會丟掉任何名為 `password` 的欄位，包括 database connection 的 `options.authentication_methods.password`。這個欄位不是 secret，而是設定物件（go-auth0 v1.50.0 `management/connection.go:643`）。有這個欄位時，step 2b 在 `apis create`、`apps create` 之後停下，錯誤訊息還錯指 custom database scripts；sign-up 已關時重跑也一樣失敗。
  修：只遮蔽已知路徑上真正的 secret，對未知的 secret 值保留保守的攔截；替身 tenant 要回傳這個欄位；重跑要能通過。
- **M2**（約 291-292、249-255、698-719、1071-1078）：`say()` 在 stdout 寫不出去時會 raise，而 cleanup 是先印再做。所以 pipe 關閉或終端機 hangup（tty 回 EIO）時：
  - Auth0 CLI 沒有登出。
  - CLI 的 home（含 token 檔）留在磁碟上。
  - Postgres 的 TCP proxy 沒有關。
  - process 以 traceback 結束。

  修：先 cleanup 再印；cleanup 期間 `say()` 要容錯；以清楚的 exit code 結束。rehearsal 要涵蓋 epipe、hup、proxy 三種情況。

## Low

- **L1**：Railway 拒絕 seal `BFF_OIDC_CLIENT_SECRET` 時，run 仍然 `done`，之後也不再 seal。
  修：不得 `done`；重跑要偵測並 seal（或給出確切的復原步驟）。
- **L2**：驗過 checksum 的 binary 與實際執行的可能不是同一個，在 hash 與執行之間可以被換掉。
  修：把驗證後的 binary 複製進 0700 的目錄，只執行該副本，並對副本的內容做 hash。
- **L3**：要補的測試：
  - rehearsal mutants V2–V7、V10、V12、V14：`deploy.sh:116-118` 的安裝絕不能在 checksum 之前，`check_deploy.py` 也要抓得到。
  - Go mutant GX2（`audit_test.go:304`）。
- **L4**：「評審密碼不寫入檔案」的說法，在背景執行 `--apply` 時不成立，因為輸出會被寫進檔案。
  建議：密碼只寫一次到私有 run 目錄中的 0600 檔，輸出只印路徑與 email；runbook 說明讀出後交給 owner 並刪除；測試確認沒有任何輸出含密碼。
- **L5**：psql、`go run`、Railway CLI 會繼承 session 的 AI key。
  修：它們也只拿 allowlist 的環境。
- **L6**：報告與 runbook 有兩處說得過頭：
  - 被丟棄欄位的清單；
  - tenant 的 Default App 與社群連線（要說清楚還可能有什麼）。
