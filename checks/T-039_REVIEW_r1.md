# T-039 獨立審查 第 1 輪（hackathon 切片，H-003／H-029 一輪審查）

**日期：2026-10-26｜審查者：獨立 reviewer（沒有參與實作）｜範圍：831278b、3b9f824、d229225、ca2c6b1（切片與 coordinator 驗證）、3bcd860、3a1c3b3（CI run 203／204 的修正）；HEAD 5b66648 上這些檔案與 3a1c3b3 相同（只有 `cmd/worker/main.go` 有其他切片的 3 行改動）｜方式：唯讀；mutant 與探測只在 `git archive HEAD` 的 scratch 複本中執行；沒有部署、沒有呼叫 Railway 或 Auth0、沒有讀取任何 token｜結論：**CHANGES REQUESTED（M1、M2；沒有 High）**

## 結論

Go 的部分（S3 adapter 與 SigV4、`cmd/migrate`／`internal/storage/migrations`、`/healthz`／`/readyz`、config）品質好：SigV4 另外對上 S3 文件的 4 個 S3 專屬範例（見 I8），migration 的 login 檢查、同一 transaction 的紀錄、advisory lock 與 ErrState 在新的 database 上通過，race 乾淨。Image、compose 與 CI 的 containers job 在 CI run 206 實際通過（我以 `gh` 唯讀確認）。

要修的兩項都在 Railway 的自動化（`deploy/railway/railway_deploy.py`），而且都是「安全聲明與實際行為不符，且沒有被測到」：

- **M1**：TCP proxy 的移除失敗時沒有任何訊息，執行仍回報 `deploy: done`；被中斷的執行留下的 proxy，之後每次執行都當成「別人開的」而不移除、不回報。結果是 PostgreSQL（含 superuser login）可能一直暴露在公開網路上，而 SECURITY.md 寫的是「結束即移除」。
- **M2**：腳本以 `railway variable list --json` 讀每個 service 的變數，owner 沒 sealed 的 `BFF_OIDC_CLIENT_SECRET`、AI key 的**值**會進入 agent 執行的 process。這與 H-032（「agent 不讀取也不搬移」）、H-033（「只看變數名稱、不讀值」）以及本切片四處「不設也不讀」的聲明不符。這些值也不在遮蔽清單中。Rehearsal 的 docstring 宣稱檢查了「不讀」，實際上沒有檢查。

## Findings

### Medium

**M1 — TCP proxy 的移除可能無聲失敗；被中斷的執行留下的 proxy 之後永遠不會被移除，也不會被回報**

- 位置：`deploy/railway/railway_deploy.py:344-349`（`proxy_open`：已有 proxy 時 `proxy_created` 保持 `None`）、`:351-354`（`proxy_close`：`check=False`，之後不看 `self.code`）、`:538-546`（只有 `try/finally`，沒有 SIGTERM handler）、`:488-526`（`verify` 不檢查 proxy）。聲明：`docs/SECURITY.md:149`（「只經本次暫開、結束即移除的 TCP proxy」）、`docs/adr/ADR-029-…md:41`、`docs/runbooks/deploy.md:295`（「移除本次開的 TCP proxy（失敗時也移除）」）。
- 問題：(a) `railway tcp-proxy delete` 非 0 時，輸出只有 `removing this run's TCP proxy`，run 繼續並以 `deploy: done` 結束。(b) Python 對 SIGTERM 的預設處理會直接結束 process，`finally` 不執行。`try` 區塊可以持續超過 20 分鐘（`admin()` 最多 300＋180 秒，migrate 的 `WAIT_SECONDS=900`，`go run` 編譯 seed），比 agent 的前景 Bash 上限（10 分鐘）還長。(c) 下一次執行看到已存在的 proxy，依 runbook 的設計（「已經有一個時沿用它，結束時也不移除：那是別人開的」）沿用它而不移除。腳本無法分辨「別人開的」與「上次沒刪掉的」，最後的 verify 也不列出還開著的 proxy。
- 失敗情境（以 rehearsal 自己的替身重現，`scratchpad/rv039/rvharness/harness.py`）：
  - 替身讓 `tcp-proxy delete` exit 1：第二次執行 `result: done | proxies still open: ['tcp_1']`，輸出中與 proxy 相關的只有 `tcp-proxy list`。
  - 先放一個「被中斷的執行留下的」proxy：連續兩次執行都是 `result: done | proxies still open: ['tcp_left_by_killed_run'] | warned: False`。
  - 結果：Postgres 的公開 TCP proxy（administrator 為 template 的 `postgres` superuser）無限期開著，沒有任何人被告知。
- 最小修正：
  1. `proxy_close` 檢查 `self.code`，失敗時記下來，讓整次執行以 exit 1 結束並寫出如何手動移除。
  2. 每次 `--apply` 結束前（也在 `verify` 中）`railway tcp-proxy list --service Postgres --json`，還有 proxy 就明確警告並以非 0 結束（例如除非設了 `GEMINUS_KEEP_DB_PROXY=1`），訊息中寫出 proxy 的 id。
  3. `signal.signal(SIGTERM, …)` 轉成 `SystemExit`，讓 `finally` 執行。
  4. Rehearsal 加兩個情境：delete 失敗、事先存在的 proxy。兩者都不得以沒有警告的 `done` 結束。

**M2 — Owner 的 secret 會被讀進部署程式（與 H-032／H-033 及本切片的聲明相反），不在遮蔽清單中；rehearsal 宣稱檢查卻沒有檢查**

- 位置：`deploy/railway/railway_deploy.py:185-188`（`variables_of` 回傳每個變數的值）。每次執行對每個 service 呼叫三次：`:320`（`variables()`）、`:459`（`seed()`，worker）、`:498`（`verify()`，只需要名稱）。`:324` 的註解寫「never set or read」。聲明：`docs/runbooks/deploy.md:233`（「不設、不讀（只看名稱是否存在）」）、`docs/adr/ADR-029-…md:40`、`docs/SECURITY.md:149`、`checks/T-039_VALIDATION.md:22`／`:114`。Rehearsal：`scripts/rehearse_railway_deploy.py:12-13` 宣稱檢查「never sets or reads」，但 `:229-234` 與 `:268-269` 只檢查「沒有設定、沒有改值」。
- 問題：`sources.txt` 記錄 CLI 5.64.2 只把 **sealed** 的值列為 `null`，所以沒 sealed 的變數，值會在 JSON 中回來。在 Railway UI 新增變數時預設不 sealed。Owner 在 UI 填入 `BFF_OIDC_CLIENT_SECRET`（web）、`GEMINUS_ANTHROPIC_API_KEY_A/_B` 或 `GEMINUS_META_API_KEY`（worker）之後，每次 `--apply` 都會把這些值讀進 `have` 這個 dict。它們不經 `self.secret()`，所以 `say()` 不會遮蔽：將來任何一行印出 CLI 輸出或錯誤時都可能帶出來。H-032 的原文是「Anthropic key 與 Auth0 的 secret 由 owner 在 Railway 介面填入，agent 不讀取也不搬移」，H-033 的原文是「key 只看變數名稱、不讀值、不印出」。
- 失敗情境（harness 情境 1，在 rehearsal 的第二次執行前以替身設定 owner 的兩個值，沒 sealed）：`owner values the deployer received: [('web', 'BFF_OIDC_CLIENT_SECRET'), ('worker', 'GEMINUS_ANTHROPIC_API_KEY_A')]`、`counted as secrets to redact: []`，結果 `done`，rehearsal 照樣 PASS。
- 最小修正：
  1. `as_variables`／`variables_of` 一收到資料，就把 `variables.json` 中 `source: owner` 的名稱的值換成「存在」的標記，不保留任何值。其他收到的非 null 值一律 `self.secret()`。
  2. `verify` 發現 owner 的變數沒 sealed（值非 null）時，提醒 owner 將它 sealed（只印名稱）。
  3. 文件改寫成實際行為：「CLI 會回傳沒 sealed 的值，腳本收到即丟棄」。是否接受這個做法由 coordinator 轉給 owner，因為它與 H-033 的字面仍有差距。
  4. Rehearsal 在第二次執行讓 owner 的值保持沒 sealed，並斷言 `Run` 的狀態與任何回傳的 dict 都不含它。

  （H-034 之後由 agent 經 stdin 寫入 Auth0 client secret 屬於 T-057，不列為本任務的缺陷；上面的修正與它相容。）

### Low

**L1 — `deploy.sh` 的 xtrace「拒絕」是不會執行到的程式碼**（`deploy/railway/deploy.sh:35-36`）
第 35 行先 `set +x`，第 36 行才檢查 `$-` 中有沒有 `x`，所以永遠不會成立。實測 `bash -x -c 'set -euo pipefail; set +x; case "$-" in *x*) …refusing…; esac; echo ran'` 的結果是 `ran`，exit 0。實際效果是「關掉 xtrace 繼續執行」：在 `set +x` 之前被 trace 的只有兩行，沒有 secret，所以安全上沒有洞。但 `docs/SECURITY.md:149`、`ADR-029:39`、`docs/runbooks/deploy.md:286` 與報告都寫「xtrace 時拒絕執行」。修正：把檢查移到 `set +x` 之前（真的拒絕），或把文件改成「關閉 xtrace」。

**L2 — Railway token 不在遮蔽清單中，也被傳給 psql 與 `go run`**（`deploy/railway/railway_deploy.py:99-118`、`:130`、`:203-205`）
ADR-029:39 寫「輸出中已知的 secret 一律替換為 `[redacted]`」，但 `RAILWAY_TOKEN`／`RAILWAY_API_TOKEN` 從沒經過 `self.secret()`。如果 CLI 的錯誤訊息（`run()` 會印 stderr 的最後 5 行）引用了 token，就會原樣印出。另外，`full_env = dict(os.environ, …)` 讓 psql 與編譯執行 repository 程式的 `go run ./cmd/provision-tenant` 也拿到 token。修正：在 `Run.__init__` 對兩個 token 呼叫 `self.secret()`；非 `railway` 的子程序把這兩個變數從環境中移除。

**L3 — `.dockerignore` 只排除根目錄的 env 檔；web image 可能帶入 `apps/web/.env`**（`.dockerignore:12-13`、`apps/web/Dockerfile:19`、`:30`、`scripts/check_deploy.py:83`）
Docker 的 pattern 以 context 根目錄為準，`.env.*` 不會比對到 `apps/web/.env.local`。`.gitignore:7-9` 沒有錨定，所以 `apps/web/.env`、`.env.local`、`.env.production` 都是常見的本機檔案，會經 `COPY apps/web/ ./` 進入 build stage。Next 16.4.1 的 standalone 會把 `.env` 與 `.env.production` 複製進 `.next/standalone`（`node_modules/next/dist/build/index.js:321-327`），再經 Dockerfile 第 30 行進入 runtime image。Next 執行時也會為沒設定的變數載入它們。
失敗情境：開發者在 `apps/web/.env` 放本機的 `BFF_OIDC_CLIENT_SECRET`／`SESSION_KEYS`，執行 `make build-containers`，image 的 `/app/.env` 就含有它們。`railway up` 依 `.gitignore` 不上傳這些檔案，CI 也沒有這些檔案，所以只影響本機建置的 image。修正：`.dockerignore` 加 `**/.env`、`**/.env.*`、`!**/.env.example`，並在 `check_deploy.py` 要求這些 pattern。

**L4 — Migration 的「其他 transaction 指令一律拒絕」以整行比對，會漏掉 `END;` 等寫法**（`internal/storage/migrations/migrations.go:59`、`:106-133`）
探測結果（scratch 的 `TestRVTxStatementsSlipThrough`）：`END;`（COMMIT 的同義詞）、`COMMIT WORK;`、`COMMIT AND CHAIN;`、`PREPARE TRANSACTION 'x';` 與同一行的 `…; COMMIT; …` 都被 `Load` 接受為 unframed。資料庫上的探測（`TestRVEndSplitsTheTransaction`）：unframed 的 0002 是 `CREATE TABLE …; END; SELECT 1/0;`，失敗後 `table of 0002 kept=true, records=1`，也就是半個 migration 留下而沒有紀錄。修正後的檔案重跑會以「already exists」失敗。這與 ADR-029 的「每個 migration 與它的紀錄在同一個 transaction」「其他位置的 transaction 指令一律拒絕」不符。目前的 16 個 migration 都不受影響。修正：先去掉註解與 dollar-quoted 的 body，再拒絕 frame 以外任何以 BEGIN／START TRANSACTION／COMMIT／END／ROLLBACK／ABORT／PREPARE TRANSACTION／COMMIT PREPARED／ROLLBACK PREPARED 開頭的 statement，並在 `TestLoadRefuses` 加上這些案例。

**L5 — 幾個宣稱的拒絕沒有測試擋住（mutant 存活）**
(a) `migrations.go:178`：把 `'twin_provisioner'` 從 runtime role 清單拿掉，`internal/storage/migrations` 與 `cmd/migrate` 的 DB 測試仍全部通過（exit 0）。`migrations_db_test.go:134-140` 沒有同時屬於 twin_owner 與 twin_provisioner 的 login。
(b) `scripts/check_deploy.py` 的 `railway()`（`:159-219`）不檢查 `public_domain` 只給 api 與 web：給 worker 與 migrate 加上 `public_domain`，`check_deploy.py` exit 0、rehearsal 也 PASS。它也不檢查 healthcheck：刪掉 `api.railway.json` 的 `healthcheckPath: /readyz` 仍 exit 0。
修正：加一個 `IN ROLE twin_owner, twin_provisioner` 的 `dt_test_*` login 到 `TestApplyRefusesUnsafeLogins`；`check_deploy.py` 要求只有 api 與 web 有 `public_domain`，api 的 healthcheck 為 `/readyz`、web 為 `/`；rehearsal 斷言 `domain` 只對 api 與 web 呼叫。

**L6 — 經公開 TCP proxy 的 bootstrap 用 `sslmode=require`（不驗憑證）而沒有 channel binding**（`deploy/railway/railway_deploy.py:374`、`:462`、`:404`；`deploy/sql/create_logins.sql:70-81`）
Superuser 的認證，以及四個 login 以明文 literal 寫在 `CREATE/ALTER ROLE … PASSWORD` 中的密碼，都經過一條 server 憑證未驗證的 TLS 連線，跨越公開網路。路徑上的攻擊者可以轉送 SCRAM（沒有 channel binding 時可行），進而看到所有 login 的密碼並以 superuser 行事。Runbook §5 已寫「加密，不驗證 server 憑證」，而且時間窗短，所以列為 Low。修正：psql 的環境加 `PGCHANNELBINDING=require`（libpq 13 以上，SCRAM-SHA-256-PLUS；server 不支援時 fail closed）；需要更進一步時，在 Python 端先算出 SCRAM verifier，讓明文密碼不送到 server（這也避開 I7）。

**L7 — Mutant 的定義沒有 commit，17／18／17／12 無法重跑**
`checks/evidence/2026-10-26-T-039/mutants-db.log` 提到 `mutants-db.json`、`mutants-db2.json`，但 repository 中沒有它們，也沒有 S3、部署檢查、rehearsal 的 mutant 腳本（`git ls-files | grep -i mutant` 只有更早任務的 `mutants.py` 等）。數字與 log 一致，但無法驗證每個 mutant 改了什麼。修正：把 mutant 的定義或腳本放進 evidence。

### Info

- **I1** `/readyz` 公開、不需 token，每個 request 執行一次 `pool.Ping`（`cmd/api/main.go:235`），不經 `/v1` 的限流。我確認它不洩漏資訊：只回 `ok`／`ready`／`unavailable`，其他 method 與 path 都交回 `/v1`（`TestProbes`），也不繞過認證。建議把結果快取 1 秒或用 singleflight。不列為缺陷。
- **I2** Bucket 的 key 由腳本讀出再複製（`variables.json:31-32`、`:52-53`；`railway_deploy.py:286-295`、`:333-335`）。Runbook §7.4 寫 bucket 的 credential「可用 reference variable 帶給 service」，§7.1（`deploy.md:207`）也提到 `${{geminus-objects.ENDPOINT}}`，但 `variables.json` 沒有用 reference。改用 reference 的話，agent 完全不經手 bucket 的 secret，輪替也會自動生效。這屬於設計選擇，也是文件不一致。
- **I3** CLI 的 JSON 形狀未經查證（報告已揭露）。另外 `status()`（`railway_deploy.py:423-425`）取 listing 中第一個 `status`：如果 CLI 先列舊的 deployment，api、web 的等待可能停在舊的 SUCCESS。migrate 以 DB 的紀錄數判斷，不受影響。第一次 `--apply` 時一併核對。
- **I4** 出處：DECISIONS 中「純 Railway」是 **H-031**（H-030 是 Railway＋Cloudflare），而 ADR-029、報告、`variables.json` 的 `about` 與 `deploy.sh` 的開頭都寫成 H-030。H-032 寫「session secret 由 Railway 產生」，腳本改為在本機產生：這已列為 owner 問題，處理方式正確。
- **I5** 報告最後一段寫「staging smoke 在 `up --wait` 之後的步驟 CI 尚未執行過」，已經過時。CI run 206（01363b7）的 6 個 job 全部 success，containers job 的 log 有 13 行 `staging-smoke: ok` 與 `staging-smoke: PASS`。應由 coordinator 補記。
- **I6** H-034（Auth0 以裝置登入、client secret 經 stdin 寫入）`deploy.sh` 尚未實作，依指示屬於 T-057，不列為本任務的缺陷。
- **I7** `CREATE/ALTER ROLE … PASSWORD '<明文>'` 在 `log_statement=ddl/all` 或啟用 `pg_stat_statements` 時會留下明文。Railway Postgres 的預設不明。L6 的 client-side SCRAM 可一併解決。
- **I8 確認無誤的部分（正向）**：
  - **SigV4**：除了 15 個通用向量，我另外以 S3 文件的 4 個 header-based 範例驗證 `canonicalRequest`／`signature`，全部吻合：GET Object（含 Range）、PUT Object（`/test$file.text` 編碼為 `%24`，只編碼一次）、`?lifecycle`、`?max-keys=2&prefix=J`。向量的出處與 Apache-2.0 notice 正確。
  - **Credential 不外洩**：`cmd/migrate` 對 4 種格式錯誤的 URL 都把密碼印成 `xxxxx`；`objects.S3` 與 config 的 `%v %+v %#v` 不含 secret。
  - **網路暴露**：web → api 走 `https://${{api.RAILWAY_PUBLIC_DOMAIN}}`（`variables.json:70`），BFF 拒絕 http 與 https 混用。Worker 與 migrate 沒有網域也沒有 listener；DB 只經 TCP proxy（M1 的保留）。
  - **測試用 provider 只在本機**：只在 `deploy/local`，`apps/web-e2e` 被排除在 build context 外（`.dockerignore:26`）；compose 只綁 127.0.0.1，postgres 沒有對外 port。
  - **Image**：非 root、以 digest pin、沒有 `ARG`、有 `STOPSIGNAL`。
  - **Migration**：login 檢查、與紀錄同一 transaction、advisory lock、ErrState（被改過的檔案、資料庫比 build 新、有 twin schema 卻沒有紀錄）在新的 database 上通過。
  - **部署順序**：logins → seal → migrate（以 DB 紀錄數判斷）→ seed → 關 proxy → api／worker → web。
  - **CLI pin**：`--install-cli` 核對 SHA-256，`--apply` 核對版本。
- **PROPOSED／APPROVED**：ADR-029 維持 PROPOSED；D-093、D-095 以 APPROVED（H-024）引用；本切片沒有把任何 PROPOSED 或 TBD 寫成 approved。

## 報告與 evidence 的核對

- 數字與 log 一致：
  - backend-test：作者 76 行、coordinator 77 行 `ok`。
  - frontend-test：547（作者）、770（coordinator）。
  - e2e：257 passed、32 skipped。
  - architecture：88 packages（現在 HEAD 為 91，其他切片加入）。
  - mutant：S3 17/17、DB 18/18（第一輪 M2、M10 存活並據實記錄）、部署檢查 17/17、rehearsal 12/12。
  - rehearsal：我重跑得到同樣的 91 個 CLI 呼叫與 7 個 psql 呼叫。
- NOT RUN 都據實標示（image build、compose、`--apply`、`TestLiveBucket`、hadolint 等）。CI 的結果見 I5。
- 不符之處：M1、M2、L1 的聲明與行為不符；L7 的 mutant 定義沒有 commit；I4 的出處錯誤。

## 執行的命令（exit code）

Scratch 複本：`git archive HEAD` 解到 `scratchpad/rv039/copy`。DB 測試在複本中把 database 前綴改為 `dt_test_rv039_mig_`、測試 login 改為 `dt_test_rv039_*`，避免與其他 agent 的名稱重疊。

| 命令 | Exit | 結果 |
|---|---|---|
| `go test -count=1` objects、config、cmd/api、cmd/migrate、storage/migrations（複本，沒有 DB） | 0 | 全部 ok |
| `psql … CREATE DATABASE dt_test_rv039a`；`bootstrap_roles.sql` 與 16 個 up migration | 0 | |
| `GEMINUS_REQUIRE_DB_TESTS=1 go test -p 1 -count=1 -v ./internal/storage/migrations/ ./cmd/migrate/` | 0 | 9 個測試 PASS |
| `go test -race -p 1 -count=1` objects、migrations、config、cmd/migrate、cmd/api、cmd/worker（含 DB） | 0 | |
| `go vet`（同上的 package） | 0 | |
| Mutant：`CheckLogin` 去掉 `twin_provisioner` → DB 測試 | 0（存活） | L5(a)；已還原 |
| 探測測試 `TestRVTxStatementsSlipThrough`、`TestRVEndSplitsTheTransaction`（DB）、`TestRVS3DocExamples`、`TestRVMalformedURLHidesThePassword` | 0 | 結果見 L4、I8 |
| `python -I scripts/check_deploy.py`（複本） | 0 | |
| Mutant：worker 與 migrate 加 `public_domain` → `check_deploy.py`／rehearsal | 0／0（存活） | L5(b)；已以 `cmp` 確認還原 |
| Mutant：刪掉 api 的 `healthcheckPath` → `check_deploy.py` | 0（存活） | L5(b)；已還原 |
| `env -u RAILWAY_TOKEN -u RAILWAY_API_TOKEN python scripts/rehearse_railway_deploy.py`（worktree；複本加 git 替身） | 0／0 | PASS；91 個 CLI 呼叫、7 個 psql |
| `python -I scratchpad/rv039/rvharness/harness.py <複本>`（替身，不連網，沒有 token） | 0 | 3 個情境，見 M1、M2 |
| `env -u RAILWAY_TOKEN -u RAILWAY_API_TOKEN ./deploy/railway/deploy.sh --dry-run` | 0 | 沒有呼叫 Railway |
| `bash -n deploy/railway/deploy.sh`、`bash -n deploy/local/staging-smoke.sh`；xtrace 的單行測試 | 0 | L1 |
| `make spec-check PYTHON=/home/user/Geminus/.venv/bin/python`（worktree） | 0 | 之後 `git status` 乾淨 |
| `make architecture-check` | 0 | `PASS architecture: 91 packages, 24 rules, 16 build configurations` |
| `gh run list`／`gh run view 40363753707`（唯讀） | 0 | run 206 的 6 個 job success；containers 的 staging smoke PASS |
| 清理：`DROP DATABASE dt_test_rv039a`、`DROP ROLE dt_test_rv039_migrator_login` | 0 | `dt_test_rv039%` 的 database 與 role 皆為 0 |
| `make spec-docs`（加入本檔之後） | 0 | 128 個 markdown 檔、121 個相對連結；生成的 `checks/docs-check.json` 因多了本檔而改變 1 行，已還原、**沒有 commit**（只改本檔）：整合時由 coordinator 重新產生 |

## 沒有執行（NOT RUN）

- `deploy/railway/deploy.sh --apply`、`--install-cli`、任何 Railway 或 Auth0 的命令（依指示）。Railway CLI 的實際 JSON 形狀未查證（I3）。
- `make build-containers`、`make staging-smoke`：本機沒有 Docker daemon。改以 CI run 206 的 log 確認。
- `make e2e-test`、frontend 測試（依指示）；完整的 `make backend-test` 與 `make integration-test`（只跑了改動的 package 與它們的 DB 測試）。
- `TestLiveBucket`（沒有 bucket 的 credential）、hadolint、shellcheck、actionlint、container scan。
- 沒有讀取、印出或移動任何 token 或 secret：rehearsal 與 dry run 都以 `env -u RAILWAY_TOKEN -u RAILWAY_API_TOKEN` 執行。

## 判定

**CHANGES REQUESTED**：

- **M1**：TCP proxy 的移除失敗與遺留的 proxy 必須被偵測、回報，並以非 0 結束；SIGTERM 時也要執行清理。
- **M2**：owner 變數的值收到即丟棄，其他收到的值都要遮蔽；文件與 rehearsal 改成如實描述並實際檢查。

Low 的各項建議在同一輪一併處理（L1、L2、L3 各只需幾行），但不阻擋。
