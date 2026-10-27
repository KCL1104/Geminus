# T-057 驗證報告：部署腳本的 Auth0 步驟（H-034）與評審帳號（H-035 (2)）

**日期：2026-10-26｜Branch：`t057-auth0`（從 095a234 開出，沒有 push、沒有 PR）｜Commits：見文末｜狀態：hackathon 切片（部署準備）交付；沒有部署任何東西、沒有登入或呼叫任何 Auth0 tenant、沒有呼叫 Railway 或任何 AI provider；每個命令都在沒有 Railway token 的環境中執行（`env -u RAILWAY_TOKEN -u RAILWAY_API_TOKEN`），rehearsal 只用假的 `REHEARSAL-TOKEN-NOT-REAL`｜決策：H-034、H-035 (2)（owner 的回答，本切片只實作）；新的部分記為 ADR-029 的 addendum（PROPOSED）｜Owner 的 Auth0 `user_id` 不在 repo 中（部署 session 以 `GEMINUS_SEED_OWNER_SUBJECT` 傳入）**

逐條輸出在 `checks/evidence/2026-10-26-T-057-auth0/`（`exit-codes.txt` 依執行順序）。

## 交付

| 項目 | 檔案 | 內容 |
|---|---|---|
| Auth0 CLI 的 pin | `deploy/railway/deploy.sh`（`--install-auth0-cli DIR`）、`deploy/railway/railway_deploy.py`（`AUTH0_CLI_*`）、`scripts/check_deploy.py`（`auth0_pin`） | Auth0 CLI 1.37.0；安裝時先核對 archive 的 SHA-256（release 的 `checksums.txt`）再核對 binary 的 SHA-256，兩者都對才安裝（照 Railway CLI 的 `--install-cli` 寫）；部署時在執行 CLI 之前計算 PATH 上 binary 的 SHA-256；兩處的 pin 由靜態檢查比對 |
| Auth0 步驟（2b） | `deploy/railway/railway_deploy.py` 的 `auth0()` 與 `a0_*` | 裝置登入、API、Regular Web Application、sign-up 關閉、應用程式只開 database connection、評審帳號、client secret 經 stdin 到 web 並 sealed、登出；`GEMINUS_AUTH0_SETUP=manual` 時跳過（T-039 原本的流程，也是手動備援） |
| 部署腳本的 secret 處理 | 同上（`Hidden`、`secret()`、`child_env()`、`run()`、`main()`／`reveal()`） | 要遮蔽的值只保存 HMAC digest；`secret()` 不回傳值；Railway token 只在 `run()` 加到 Railway CLI 的環境；評審密碼只在 `main()` 的 list 中，結束時寫到 stderr 一次 |
| 評審帳號的 membership | `cmd/provision-tenant`（`-member-issuer`／`-member-subject`／`-member-role`）、`internal/storage/provisioning`（`OrganizationWithMember`）、`internal/audit`（`organization.provision` 的 `member_user_id`、`member_role`） | 建立組織時同一個 transaction 加入一個 editor 或 viewer；seed 之後 administrator 列出評審帳號的 membership，必須只有 demo 組織的 editor |
| 變數 | `deploy/railway/variables.json` | `BFF_OIDC_CLIENT_SECRET` 的 `source` 由 `owner` 改為 `auth0`（只有它可以，`check_deploy.py` 檢查） |
| Rehearsal | `scripts/rehearse_railway_deploy.py` | Auth0 CLI 1.37.0 的替身（agent mode 的 JSON）與 17 個新情境（A6 兩次，共 18 次執行）；原本的執行改為明確的 `GEMINUS_AUTH0_SETUP=manual`，全部照舊通過 |
| 文件 | `docs/runbooks/deploy.md` §7（新的 §7.8，§7.3、§7.7、§6、§10 更新）、`docs/runbooks/auth0.md` §6（手動備援的 dashboard 步驟）、ADR-029 addendum、`docs/SECURITY.md` §6 的一段 | |

## 設計：`--apply` 的順序

1. preflight：`GEMINUS_AUTH0_SETUP` 是 `cli`（預設）或 `manual`；cli 時 `GEMINUS_AUTH0_ISSUER` 必須是 `https://<tenant>.<region>.auth0.com/`（CLI 只登入 auth0.com 的網域）、`GEMINUS_AUTH0_CLIENT_ID` 不得設定（它是這一步的輸出）、`GEMINUS_AUTH0_AUDIENCE` 不是 Management API 的 identifier；PATH 上的 `auth0` 的 SHA-256 必須是 pin 住的（不是就不執行它，連 `--version` 也不）。
2. 步驟 1、2（project、資源、web 與 api 的公開網域）照舊。
3. **步驟 2b（Auth0）**，全部在 `try` 中，`finally` 等價的處理（成功、失敗、SIGTERM、SIGHUP）都登出：
   1. CLI 的 home：`$XDG_STATE_HOME`（或 `~/.local/state`）`/geminus-deploy/auth0-cli`，0700；上一次被 SIGKILL 留下的先登出再刪除。
   2. `auth0 login --domain <tenant> --agent-mode --no-input --no-color`：逐行解析 stdout 的 JSON，只顯示 `verification_uri` 與 `user_code`（格式不對就不顯示並停下），等 owner 核准；登入的網域必須是 `GEMINUS_AUTH0_ISSUER` 的。
   3. `auth0 users show <owner>`：owner 自己的使用者必須在 `GEMINUS_AUTH0_CONNECTION` 中（在任何寫入之前）。
   4. API → Application → database connection 的 sign-up → 應用程式的 connection → 評審帳號：找到就比對，不同才更新，沒有才建立，寫入後讀回比對。
   5. Client secret：只在需要時以 `auth0 apps show <id> --reveal-secrets` 取得，只經 stdin 交給 `railway variable set BFF_OIDC_CLIENT_SECRET --stdin`，立刻 sealed。
   6. 登出：`auth0 logout <tenant>`（`auth0 tenants list` 列出的每一個），以 `tenants list` 確認沒有 session（沒有 keyring 時 CLI 的 logout 在移除 tenant 之後以 exit 1 結束），刪除 home；還有 session 或刪不掉時整次執行失敗。
4. 步驟 3：`GEMINUS_AUTH0_CLIENT_ID` 為 2b 的輸出，`BFF_OIDC_*`／`AUTH_OIDC_*`（issuer、audience、client ID、JWKS URL）照 `variables.json` 填入。
5. 步驟 4–5 照舊（`BFF_OIDC_CLIENT_SECRET` 不在步驟 4 的 seal 中：2b 已 sealed，且不受 `GEMINUS_RAILWAY_SEAL=off` 影響）。
6. 步驟 6：seed 以 `provision-tenant organization … -member-issuer <issuer> -member-subject <評審的 user_id> -member-role editor`；之後以 administrator（psql，`-v issuer=… -v subject=…`，SQL 經 stdin）列出評審帳號的 membership。
7. 步驟 7、8 照舊；步驟 6 記下的問題（評審帳號沒有 membership 或在別的組織）在 verification 失敗。
8. 結束時（不論怎麼結束）：評審的新密碼寫到 stderr 一次。

### 送給 Auth0 的設定（與 H-034 逐項對照）

| H-034 | 送出的值（CLI 1.37.0 的命令，JSON 經 stdin） |
|---|---|
| API：identifier = 部署的 audience | `apis create`：`identifier` = `GEMINUS_AUTH0_AUDIENCE`，`name` `Geminus demo API` |
| RS256、RFC 9068、600 秒、Allow Offline Access | `signing_alg` `RS256`、`token_dialect` `rfc9068_profile`、`token_lifetime` 600、`allow_offline_access` true（另 `skip_consent_for_verifiable_first_party_clients` true：first-party 的 BFF 不出現同意畫面） |
| Regular Web Application、Client Secret (Post) | `apps create`：`app_type` `regular_web`、`token_endpoint_auth_method` `client_secret_post`、`oidc_conformant` true、`jwt_configuration.alg` `RS256`（BFF 以 RS256 驗 ID token） |
| 只有 Authorization Code 與 Refresh Token | `grant_types` `["authorization_code", "refresh_token"]` |
| rotation、reuse interval 30 秒、inactivity 43200 秒、absolute 604800 秒 | `refresh_token`：`rotation_type` `rotating`、`expiration_type` `expiring`、`leeway` 30、`idle_token_lifetime` 43200、`infinite_idle_token_lifetime` false、`token_lifetime` 604800、`infinite_token_lifetime` false |
| callback 與 logout 只有 web 網域（BFF 的路徑） | `callbacks` `["https://<web 網域>/auth/callback"]`、`allowed_logout_urls` `["https://<web 網域>/"]`（`apps/web/lib/session/oidc.ts`：`${appOrigin}/auth/callback`、post-logout `${appOrigin}/`），`web_origins` 與 `allowed_origins` 為 `[]`（Auth0 runbook §3） |
| 關閉 database connection 的 sign-up | `connections update <id>`：`{"options": <現有的全部 options，disable_signup: true>, "display_name": <現有的>}`（PATCH 會取代整個 options、沒有 display_name 時改成 name）；options 中有腳本不保留的值（custom database 的 scripts、configuration）時停下改手動 |
| （H-035 (2) 關閉自行註冊） | `connections enabled-clients update`：應用程式只在 database connection 啟用，其他 connection 停用（見「待決定」1） |
| 評審帳號 | `users create`：`connection`、`email`（預設 `judge@geminus-demo.invalid`）、`password`（`secrets`，24 字元、四種字元、沒有三個相同字元相連）、`email_verified` true、`verify_email` false、`name` `Geminus demo judge`；已存在時不改，`GEMINUS_JUDGE_RESET_PASSWORD=1` 時 `users update <id>` `{"password", "connection"}` |

### Secret

- **Client secret**：`auth0 apps show --reveal-secrets` 的 JSON 以 `object_pairs_hook` 解析：`client_secret` 放進那一次呼叫自己的 list（登記為要遮蔽的值），JSON 中換成標記；交給 `set_from_stdin` 之後清空 list。其他 credential 欄位（`signing_secret`、`signing_keys`、`device_code`、`password`、`configuration`、`customScripts` 等；`access_token`／`refresh_token`／`id_token` 是字串時）在每一個 CLI 的輸出中都換成標記，所以別的應用程式的 secret、別的 API 的 signing secret、Google connection 的 client secret 都不會進入部署腳本。
- **評審密碼**：模組層級的 `judge_password()` 產生，登記遮蔽，只經 stdin 給 CLI，放進 `main()` 的 list；`main()` 的 `finally` 寫到 stderr 一次後清空。帶密碼的呼叫失敗時只顯示 error envelope 的類別（`code/reason (status)`），不顯示 CLI 的訊息。
- **Railway token**：T-039 的遮蔽清單是明文的 list（token 在部署腳本的狀態中），`child_env()` 回傳的 dict 在 `run()` 中被加上 token（那個 dict 同時是 method 的回傳值）。兩處都由新的 rehearsal 掃描找到（red 的 `A1: a secret is in its state`、修正途中的 `… returned or raised`）：改為 HMAC digest 的 `Hidden`、`secret()` 不回傳、`run()` 複製 `child_env()` 的 dict 後才加 token。
- 不寫入任何檔案：CLI 自己的 session（沒有 keyring 時的 `config.json`）只在 CLI 的 home 中，結束時刪除。

### 為什麼不用 `auth0 api`

需要的每個操作在 1.37.0 都有 CLI 自己的命令（`apis`、`apps`、`connections`、`connections enabled-clients`、`users`，都接受 `--data @-`）。1.37.0 的 `auth0 api` 會原樣印出 `client_secret`（1.38.0 才預設遮蔽），所以不使用。

### 評審帳號的 role

`editor`。openapi 的 `x-required-roles`：`createProposal`、`createImageProposal`、`approveProposal`、`createModel`、`createSimulation` 都是 owner 或 editor；viewer 只能讀；owner 另外能管理成員（`createMembership` 等）與讀 `listAuditEvents`，demo 流程不需要。Membership 只能在建立組織時加入：migration 0004 的 `guard_provisioner_target` 讓 provisioning 只把成員加到它在同一個 transaction 建立的組織（T-010 review 2 L1），本切片沒有放寬它。

### 手動備援

`docs/runbooks/auth0.md` §6：同樣設定的 dashboard 步驟（API、Application 的 Settings／Credentials／Connections、Disable Sign Ups、評審帳號），之後以 `GEMINUS_AUTH0_SETUP=manual GEMINUS_AUTH0_CLIENT_ID=… GEMINUS_JUDGE_SUBJECT=…` 執行 `--apply`（seed 一樣把評審帳號設為 editor 並檢查）。

## Auth0 CLI 的選擇

| 項目 | 值 |
|---|---|
| 版本 | 1.37.0（release 2026-09-30，26 天；1.38.0 是 2026-10-19，不到兩週，不用） |
| Release | https://github.com/auth0/auth0-cli/releases/tag/v1.37.0 |
| 授權 | MIT（tag 中的 `LICENSE`：「Copyright (c) 2021 Auth0, Inc.」） |
| Archive | `auth0-cli_1.37.0_Linux_x86_64.tar.gz`，SHA-256 `277e8fa32b9e4f4feb8e30717892139251821c0c66cd3ad5f326ba759ec6ac4f`（release 的 `checksums.txt`，下載的 archive 相同） |
| Binary | `auth0`，SHA-256 `2bc2ef7c1ab3c340a50bebb2c6bfd9692ea23dfee539bf7a2e1aac3debb3491c`（2026-10-26 從該 archive 取出計算；沒有執行過） |

## 來源

`checks/evidence/2026-10-26-T-057-auth0/sources.txt`（2026-10-26）：CLI 在 v1.37.0 的原始碼（login、logout、tenants、api、apis、apps、connections、users、data_json、root 的 agent mode、analytics、config、keyring、auth 的 scopes 與 device flow）、go-auth0 v1.50.0 的 JSON 欄位、Auth0 文件（refresh token rotation 與 expiration、access token profiles、API settings、application settings、credentials、application connections、PATCH connections 與 connection clients）。「Disable Sign Ups」的 dashboard 文件頁沒有取得（兩個猜的網址 404），runbook 中的名稱不是從取得的頁面來的。Code host 的 REST API 在本 session 回 403，release 的資訊取自 release 檔案與 tag 的 CHANGELOG。

## 測試先行（red → green）

- **Go**：先寫 `TestOrganizationWithMember`（DB）、`TestProvisionWithMember`（DB）與 audit catalogue 的案例，執行：`red-go-provisioning.log`（exit 1：`undefined: provisioning.OrganizationWithMember`、`flag provided but not defined: -member-issuer`）、`red-go-audit.log`（exit 1：`member_user_id: not a field of this action`）；實作後 `green-go-db.log`、`green-go-audit.log`（exit 0）。
- **Rehearsal**：新的情境在實作之前寫好，在舊的部署腳本上執行（`git archive` 的複本；舊腳本的遮蔽清單是 list，只有兩個 registry 呼叫在複本中改成 list 的寫法）：`red-rehearsal.log`，exit 1、81 項失敗，全部在新的情境中（原本的 manual 執行全部通過），其中包括舊腳本把 Railway token 放在狀態中（`A1: a secret is in its state`）。實作途中又抓到一次：`child_env()` 回傳的 dict 被加上 token（修正：複製）。
- **`check_deploy.py`**：`red-check-deploy.log`（exit 1：`unknown source 'auth0'`），加入規則後 exit 0。
- **Mutation 之前另加的 assertion**（為了分辨兩層各自的效果，沒有放寬任何既有的）：帶 secret 的呼叫失敗時 CLI 的訊息完全不顯示（不只是被遮蔽）、評審密碼與 client secret 有登記遮蔽、登出後仍有 session 時失敗（A14）、`--install-auth0-cli` 停在 archive 自己的檢查。

## Rehearsal 的新情境（Auth0 CLI 的替身；`rehearsal.log`）

| | 情境 | 確認的事 |
|---|---|---|
| A1 | 新的 project 與 tenant | 寫入 Auth0 恰好 5 次，內容與上表完全相同（API、Application、connection 的 options 與 display_name、Google 停用、評審帳號）；sign-up 關、應用程式只在 database connection；web 的 `BFF_OIDC_CLIENT_SECRET` 是 client secret、只經 stdin 設一次、sealed；`BFF_OIDC_*`／`AUTH_OIDC_*` 為輸出；seed 一次並帶 `-member-… editor`；評審帳號只是 demo 組織的 editor；登入只顯示 URL 與 code 一行；密碼是強的、恰好一次在 stderr；client secret、密碼、token 與五個 decoy（別的應用程式與 API 的 secret、Google connection 的 secret、CLI 的 access token、device code）不在任何程式的引數、輸出、部署腳本的狀態、結果、method 的回傳值或 exception 中；CLI 最後登出、home 刪除，每次呼叫都在 agent mode、在那個 home、沒有 token、analytics 關、沒有 session bus |
| A2 | 同一個 tenant 與 project 重跑（替身以相反的順序列出 grant types） | Auth0 沒有任何寫入、不再讀 client secret、Railway 沒有 stdin 的設定、不再 seed、密碼不變也不顯示 |
| A3 | tenant 漂移 | 多的 loopback callback、implicit grant、overlap 0、web origin、API 的 dialect 與 lifetime、sign-up 又開、應用程式離開 database connection 並在 Google 上：恰好 5 次更新、內容與上表相同，沒有建立 |
| A12 | `GEMINUS_JUDGE_RESET_PASSWORD=1`，加上被中斷的執行留下的 home | 先登出並刪除留下的 home；只有一次 `users update`（密碼經 stdin）；新密碼不同、強、恰好一次，舊密碼與留下的 token 都不出現 |
| A4 | PATH 上的 `auth0` 不是 pin 住的 | preflight 停下，CLI 一次都沒有執行 |
| A5 | `apps create` 失敗 | 停在那裡、web 沒有 client secret、登出 |
| A7 | 登入到別的 tenant | 什麼都不改、那個 tenant 也登出 |
| A8 | Auth0 沒有保留 sign-up 關閉 | 失敗並指向手動 |
| A9 | CLI 在錯誤中引用密碼 | 失敗，只顯示錯誤類別；密碼不在任何地方 |
| A13 | owner 的使用者不在 database connection | 任何寫入之前失敗 |
| A14 | 登出沒有移除 session | 失敗，home 仍刪除 |
| A10 | demo 組織先以 manual 部署 seed 過 | verification 失敗並寫出評審的 `usr_…` 與加入方法；`provision-tenant user` 一次、沒有第二個組織 |
| A11 | 評審帳號也在別的組織 | verification 失敗並列出 membership |
| M1 | manual 加 `GEMINUS_JUDGE_SUBJECT` | seed 帶 `-member-… editor`，Auth0 CLI 沒有執行 |
| D1 | cli 的 `--dry-run`（在 process 內） | 印出 Auth0 的計畫（含 `--reveal-secrets` 那一行），沒有任何呼叫 |
| A6 ×2 | SIGTERM 在 `connections update`（評審帳號之前）與 `apps show --reveal-secrets`（之後），獨立的 process | exit 143、登出、home 刪除、client secret 沒有到 Railway；之前沒有密碼、之後密碼恰好一次在 stderr |
| I1 | `deploy.sh --install-auth0-cli`，curl 的替身給一個不是 pin 住的 archive | exit 1、`the Auth0 CLI archive's SHA-256 is not the pinned one; nothing installed`、沒有建立目錄、archive 中的程式沒有執行 |

SIGHUP 與 SIGTERM 是同一個 handler（T-039 的 P6 測過 SIGHUP），Auth0 步驟只以 SIGTERM 演練。

## Mutation

**結果：61 個 mutant，58 個 KILLED，3 個 EQUIVALENT（R5、R39、G7，理由寫在 mutant 自己的說明中，見下），0 個 SURVIVED，0 個 INVALID；每一個 mutant 之後檔案都還原並以 SHA-256 比對（`restored files: PASS`）。**

做法：`checks/evidence/2026-10-26-T-057-auth0/mutation/mutants.py`。每個 mutant 在 `git archive HEAD` 的複本（session 的 scratchpad，`t057a0-mut/copy1`–`copy5`，不是 worktree，也不是 `scratchpad/mut/`）中替換一段只出現一次的文字，執行一個檢查：`rehearsal`（`scripts/rehearse_railway_deploy.py`）、`deploy`（`scripts/check_deploy.py`）或 `go`（`go test -count=1` 於 `internal/storage/provisioning`、`cmd/provision-tenant`、`internal/audit`，DB 為一次性的 `dt_test_t057a*`）。每次先確認未改動的複本通過（BASELINE），沒有 Railway token，PATH 最前面是 git 的替身。

| Log | Commit | 範圍 | 結果 |
|---|---|---|---|
| `mutation/mutants-1.log` | ae60084 | R1–R18（secret、登入、登出、環境） | 17/18；R5 SURVIVED（見下，改判 EQUIVALENT 後在 `mutants-5.log` 重跑） |
| `mutation/mutants-2.log` | ae60084 | R19–R39（設定值、sign-up、connection、idempotency、seal） | 20/21；R39 SURVIVED（見下） |
| `mutation/mutants-3.log` | ae60084 | R40–R45、S1–S2、D1–D5、G1–G8 | 21/21 KILLED，但 G 的結果不可信（見下），由 `mutants-4.log` 取代 |
| `mutation/mutants-4.log` | 1112af2 | R5、G1–G8，新的 `dt_test_t057a2` | G：7/7 KILLED，G7 EQUIVALENT；R5 EQUIVALENT |
| `mutation/mutants-5.log` | 1112af2 | R5（說明更正後重跑） | EQUIVALENT |
| `mutation/mutants-6.log` | eb6f17a | R39（改判）、R46（新） | R46 KILLED、R39 EQUIVALENT |

Mutation 找到的問題與處理（都沒有放寬任何 assertion）：

1. **G 的測試互相污染**（`mutants-3.log`）：`TestOrganizationWithMember` 被拒絕的情況共用固定的 owner subject `owner-refused`，G3（不檢查成員的 identity）讓一個情況通過、留下 user，之後的情況與之後在同一個 DB 上的執行都數到它，所以 G4–G8 的失敗有一部分是污染造成的，G7（預期 equivalent）也因此被「殺死」。修正（1112af2）：每個情況有自己的 owner 與成員 subject，三種都檢查（組織、owner、成員都沒有寫入）；新的 `dt_test_t057a2` 上連跑兩次都通過（`green-go-db-rerun1.log`、`green-go-db-rerun2.log`），G1–G8 重跑後結果乾淨（G5 只因 audit 的 metadata 失敗，G7 存活）。
2. **R39 → 新的 A2 assertion 與 R46**：R39（每個 list 都不計順序）存活。分析：H-034 要的 list 中，只有 `grant_types` 有兩個元素，而它本來就不計順序；其他（callbacks、logout URL、web origins、allowed origins）最多一個元素，同樣的元素換順序不存在，所以 R39 equivalent。但分析也顯示「`grant_types` 不計順序」本身沒有任何情境證明：A2 現在先讓替身以相反順序列出 grant types，重跑仍必須沒有任何寫入（eb6f17a），新的 R46（grant types 依順序比較）被它殺死。
3. **R5**（`device_code` 不在丟棄的欄位中）：CLI 1.37.0 agent mode 的登入只印 `verification_uri`、`user_code`、`expires_in`、`interval`（`internal/cli/login.go`）；替身另外加了 `device_code` 的 decoy，但 `a0_login` 只顯示 URL 與 code，解析的內容留在函式內，不回傳、不保存、不印出，所以兩種情況都觀察不到；欄位照樣丟棄（defence in depth）。
4. **G7**（指令接受不完整的 member 旗標）：provisioning 對不完整的 identity 或 role 回 `ErrInvalidInput`，指令也把它對應到 exit 2；指令自己的檢查只是在連 DB 之前就拒絕。

表中 R12、R13 的失敗訊息含 rehearsal 自己印出的密碼前 8 字元：那是替身執行時產生、執行結束即丟棄的值，不是任何帳號的密碼；`mutation/` 的原始 log 照原樣保留，下表以「替身執行的密碼前 8 字元」代替。

| ID | 改動 | 檢查 | 結果 | 第一個失敗（截短） | log |
|---|---|---|---|---|---|
| R1 | the client secret as an argument of `railway variable set` | rehearsal | KILLED | REHEARSAL FAILED: - A1: FAIL: refusing to pass a secret as a command-line argument - A1: web's BFF_OIDC_CLIENT_SECRET is not the application's client  | mutants-1.log |
| R2 | the client secret kept in the deployer's state | rehearsal | KILLED | REHEARSAL FAILED: - A1: a secret is in its state: ['A0-REHEARSAL'] | mutants-1.log |
| R3 | the captured client secret left in the parsed output (a return value) | rehearsal | KILLED | REHEARSAL FAILED: - A1: a secret is in what its methods returned or raised: ['A0-REHEARSAL'] | mutants-1.log |
| R4 | client_secret not among the dropped fields (another application's secret kept) | rehearsal | KILLED | REHEARSAL FAILED: - A1: a secret is in what its methods returned or raised: ['DEFAULT-APP-', 'GOOGLE-CONNE'] - A2: a secret is in what its methods ret | mutants-1.log |
| R5 | device_code not among the dropped fields (equivalent: the 1.37.0 CLI's agent-mode login prints only verification_uri, user_code, expires_in and interval (internal/cli/login.go); the stand-in adds a device_code decoy, but a0_login shows only verification_uri and user_code and its parsed lines stay local, never returned, stored or printed, so the decoy is unobservable either way; the field is dropped as defence in depth) | rehearsal | EQUIVALENT |  | mutants-5.log |
| R6 | the sign-in shows the CLI's whole line | rehearsal | KILLED | REHEARSAL FAILED: - A1: the sign-in URL and code are not shown once: ['   {"verification_uri": "https://auth0.auth0.com/activate?user_code=QWER-TYUI", | mutants-1.log |
| R7 | the CLI's SHA-256 is not checked | rehearsal | KILLED | REHEARSAL FAILED: - A4 (checksum mismatch) must stop before running the CLI: done 22 calls | mutants-1.log |
| R8 | no sign-out when the step fails | rehearsal | KILLED | REHEARSAL FAILED: - A5: the Auth0 CLI was not signed out last, or its home /tmp/tmpprwm7n1a/xdg-state/geminus-deploy/auth0-cli is left: ['apis create' | mutants-1.log |
| R9 | the CLI's home is not removed | rehearsal | KILLED | Traceback (most recent call last): FileExistsError: [Errno 17] File exists: '/tmp/tmpnqis9vxx/xdg-state/geminus-deploy/auth0-cli' | mutants-1.log |
| R10 | a home left by an interrupted run is not signed out first | rehearsal | KILLED | REHEARSAL FAILED: - A12: the CLI home left by an interrupted run was not signed out and removed first | mutants-1.log |
| R11 | the judge's password is not registered for redaction | rehearsal | KILLED | REHEARSAL FAILED: - A1: the judge's password or the client secret is not registered for redaction | mutants-1.log |
| R12 | the judge's password shown on stdout | rehearsal | KILLED | REHEARSAL FAILED: - A1: （替身執行的密碼前 8 字元）… is in the output 1 times, want 1 (on stderr) - A1: the judge's email is not shown with the password | mutants-1.log |
| R13 | the judge's password shown twice | rehearsal | KILLED | REHEARSAL FAILED: - A1: （替身執行的密碼前 8 字元）… is in the output 2 times, want 1 (on stderr) - A12: （替身執行的密碼前 8 字元）… is in the output 2 times, want 1 (on stderr) | mutants-1.log |
| R14 | a call that carried a secret shows the CLI's message | rehearsal | KILLED | REHEARSAL FAILED: - A9: the error of a call that carried a secret is shown (it is redacted, but must not be shown at all) | mutants-1.log |
| R15 | the Railway token kept as a plain value (the registry) | rehearsal | KILLED | REHEARSAL FAILED: - A1: a secret is in its state: ['36Bz-SVV8MnZ', 'A0-REHEARSAL', 'REHEARSAL-TO'] - A2: a secret is in its state: ['REHEARSAL-TO'] | mutants-1.log |
| R16 | child_env's own answer gets the Railway token (no copy) | rehearsal | KILLED | REHEARSAL FAILED: - A1: a secret is in what its methods returned or raised: ['REHEARSAL-TO'] - A2: a secret is in what its methods returned or raised: | mutants-1.log |
| R17 | the Auth0 CLI gets the whole environment | rehearsal | KILLED | REHEARSAL FAILED: - A1: an Auth0 CLI call ran with another home, the Railway token, analytics, a session bus or without agent mode - A2: an Auth0 CLI  | mutants-1.log |
| R18 | the Auth0 CLI's analytics on | rehearsal | KILLED | REHEARSAL FAILED: - A1: an Auth0 CLI call ran with another home, the Railway token, analytics, a session bus or without agent mode - A2: an Auth0 CLI  | mutants-1.log |
| R19 | the API's dialect is not RFC 9068 | rehearsal | KILLED | REHEARSAL FAILED: - A1: the API was created with {'name': 'Geminus demo API', 'signing_alg': 'RS256', 'token_dialect': 'access_token', 'token_lifetime | mutants-2.log |
| R20 | the API's token lifetime 86400 s | rehearsal | KILLED | REHEARSAL FAILED: - A1: the API was created with {'name': 'Geminus demo API', 'signing_alg': 'RS256', 'token_dialect': 'rfc9068_profile', 'token_lifet | mutants-2.log |
| R21 | the API without offline access | rehearsal | KILLED | REHEARSAL FAILED: - A1: the API was created with {'name': 'Geminus demo API', 'signing_alg': 'RS256', 'token_dialect': 'rfc9068_profile', 'token_lifet | mutants-2.log |
| R22 | the application authenticates with Client Secret (Basic) | rehearsal | KILLED | REHEARSAL FAILED: - A1: the application was created with {'name': 'Geminus demo web', 'app_type': 'regular_web', 'oidc_conformant': True, 'token_endpo | mutants-2.log |
| R23 | an implicit grant too | rehearsal | KILLED | REHEARSAL FAILED: - A1: the application was created with {'name': 'Geminus demo web', 'app_type': 'regular_web', 'oidc_conformant': True, 'token_endpo | mutants-2.log |
| R24 | no rotation overlap (leeway 0) | rehearsal | KILLED | REHEARSAL FAILED: - A1: the application was created with {'name': 'Geminus demo web', 'app_type': 'regular_web', 'oidc_conformant': True, 'token_endpo | mutants-2.log |
| R25 | the idle lifetime 30 days | rehearsal | KILLED | REHEARSAL FAILED: - A1: the application was created with {'name': 'Geminus demo web', 'app_type': 'regular_web', 'oidc_conformant': True, 'token_endpo | mutants-2.log |
| R26 | the callback on another path | rehearsal | KILLED | REHEARSAL FAILED: - A1: the application was created with {'name': 'Geminus demo web', 'app_type': 'regular_web', 'oidc_conformant': True, 'token_endpo | mutants-2.log |
| R27 | a web origin allowed | rehearsal | KILLED | REHEARSAL FAILED: - A1: the application was created with {'name': 'Geminus demo web', 'app_type': 'regular_web', 'oidc_conformant': True, 'token_endpo | mutants-2.log |
| R28 | sign-up not turned off | rehearsal | KILLED | REHEARSAL FAILED: - A1: FAIL: sign-up on Username-Password-Authentication is still on after the update: turn it off in the dashboard (docs/runbooks/au | mutants-2.log |
| R29 | the connection's other options not kept | rehearsal | KILLED | REHEARSAL FAILED: - A1: the database connection was updated with [['con_db'], {'options': {'disable_signup': True}, 'display_name': 'Username-Password | mutants-2.log |
| R30 | the connection's display name not kept | rehearsal | KILLED | REHEARSAL FAILED: - A1: the database connection was updated with [['con_db'], {'options': {'passwordPolicy': 'good', 'brute_force_protection': True, ' | mutants-2.log |
| R31 | the application left on other connections | rehearsal | KILLED | REHEARSAL FAILED: - A1: FAIL: the application is enabled on the connection google-oauth2 - A1: the writes to Auth0 are ['apis create', 'apps create',  | mutants-2.log |
| R32 | the owner's connection not checked first | rehearsal | KILLED | REHEARSAL FAILED: - A13 (the owner elsewhere) must fail before any change: done | mutants-2.log |
| R33 | another tenant's sign-in accepted | rehearsal | KILLED | REHEARSAL FAILED: - A7 (another tenant) must change nothing and sign that tenant out: FAIL: 'auth0 users show' failed | mutants-2.log |
| R34 | the application created again on every run | rehearsal | KILLED | REHEARSAL FAILED: - A2 (a rerun) wrote to Auth0 [['apps create', ['--data'], {'name': 'Geminus demo web', 'app_type': 'regular_web', 'oidc_conformant' | mutants-2.log |
| R35 | the application updated on every run | rehearsal | KILLED | REHEARSAL FAILED: - A2 (a rerun) wrote to Auth0 [['apps update', ['a0rehearsalclient000000000000001'], {'name': 'Geminus demo web', 'app_type': 'regul | mutants-2.log |
| R36 | the judge's password reset on every run | rehearsal | KILLED | REHEARSAL FAILED: - A2 (a rerun) wrote to Auth0 [['users update', ['auth0\|rehearsaljudge0001'], {'password': '&lt;on stdin&gt;', 'connection': 'Username-Pa | mutants-2.log |
| R37 | the client secret read again on every run | rehearsal | KILLED | REHEARSAL FAILED: - A2: the rerun read the client secret again or set a secret on Railway | mutants-2.log |
| R38 | the client secret not sealed | rehearsal | KILLED | REHEARSAL FAILED: - A1: web's BFF_OIDC_CLIENT_SECRET is not sealed | mutants-2.log |
| R39 | every list compared in any order, not only the grant types (equivalent: every other list H-034 wants -- callbacks, allowed_logout_urls, web_origins, allowed_origins -- has at most one element, so two lists of the same elements in another order cannot exist; Auth0's answers have no list where a scalar is wanted) | rehearsal | EQUIVALENT |  | mutants-6.log |
| R40 | the judge an owner of the demo organization | rehearsal | KILLED | REHEARSAL FAILED: - A1: the seed did not give the demo organization the judge as an editor, once: run ./cmd/provision-tenant organization -name Demo l | mutants-3.log |
| R41 | the seed without the judge | rehearsal | KILLED | REHEARSAL FAILED: - A1: FAIL: verification: - the judge's account auth0\|rehearsaljudge0001 has the memberships [], want only editor of org_01M5W4RHAF | mutants-3.log |
| R42 | any membership of the judge accepted | rehearsal | KILLED | Traceback (most recent call last): IndexError: list index out of range | mutants-3.log |
| R43 | a judge without membership not reported | rehearsal | KILLED | REHEARSAL FAILED: - A10 must name the missing membership and how to add it: done run ./cmd/provision-tenant user -issuer https://geminus-demo.us.auth0 | mutants-3.log |
| R44 | the other tenants a sign-in left are not signed out | rehearsal | KILLED | REHEARSAL FAILED: - A7 (another tenant) must change nothing and sign that tenant out: FAIL: the Auth0 CLI signed in to 'someone-else.eu.auth0.com', no | mutants-3.log |
| R45 | a sign-out that left the session passes | rehearsal | KILLED | REHEARSAL FAILED: - A14 (the sign-out keeps the session) must fail and still remove the home: done | mutants-3.log |
| R46 | the grant types compared in order (Auth0 listing them in another order is drift) | rehearsal | KILLED | REHEARSAL FAILED: - A2 (a rerun) wrote to Auth0 [['apps update', ['a0rehearsalclient000000000000001'], {'name': 'Geminus demo web', 'app_type': 'regul | mutants-6.log |
| S1 | the archive's SHA-256 not checked (only the binary's) | rehearsal | KILLED | REHEARSAL FAILED: run 1 stopped before verification: FAIL: preflight: - scripts/check_deploy.py failed (its message is above) | mutants-3.log |
| S2 | neither SHA-256 checked | rehearsal | KILLED | REHEARSAL FAILED: run 1 stopped before verification: FAIL: preflight: - scripts/check_deploy.py failed (its message is above) | mutants-3.log |
| D1 | another variable from the Auth0 step (the worker's Meta key) | deploy | KILLED | DEPLOY CHECK FAILED: variables.json: worker: GEMINUS_MUSE_API_KEY: only the web's BFF_OIDC_CLIENT_SECRET comes from the Auth0 step | mutants-3.log |
| D2 | deploy.sh pins another binary than the deployer | deploy | KILLED | DEPLOY CHECK FAILED: deploy.sh and railway_deploy.py pin different Auth0 CLI builds | mutants-3.log |
| D3 | the deployer pins another version | deploy | KILLED | DEPLOY CHECK FAILED: deploy.sh and railway_deploy.py pin different Auth0 CLI builds | mutants-3.log |
| D4 | the archive from the latest release | deploy | KILLED | DEPLOY CHECK FAILED: deploy.sh: AUTH0_CLI_URL https://github.com/auth0/auth0-cli/releases/latest/download/v${AUTH0_CLI_VERSION}/auth0-cli_${AUTH0_CLI_ | mutants-3.log |
| D5 | the install without the binary's check | deploy | KILLED | DEPLOY CHECK FAILED: deploy.sh --install-auth0-cli does not check the archive's and the binary's SHA-256 | mutants-3.log |
| G1 | a member may be a second owner | go | KILLED | --- FAIL: TestOrganizationWithMember (0.04s) provisioning_test.go:418: a second owner: audit: organization.provision: member_role: not one of the fiel | mutants-4.log |
| G2 | the member may be the owner again | go | KILLED | --- FAIL: TestOrganizationWithMember (0.04s) provisioning_test.go:418: the owner again: postgres: unique constraint violated (SQLSTATE 23505): constra | mutants-4.log |
| G3 | the member's identity not checked | go | KILLED | --- FAIL: TestOrganizationWithMember (0.05s) provisioning_test.go:418: an http issuer: &lt;nil&gt;, want ErrInvalidInput provisioning_test.go:428: an http i | mutants-4.log |
| G4 | the member inserted as an owner | go | KILLED | --- FAIL: TestOrganizationWithMember (0.05s) provisioning_test.go:371: memberships map[judge-x_01M5X8Y7ZE4C42D177195B1QXF:owner owner-x_01M5X8Y7ZEB3JE | mutants-4.log |
| G5 | the member not in the audit event | go | KILLED | --- FAIL: TestOrganizationWithMember (0.04s) provisioning_test.go:386: audit: 1 events, metadata {"owner_user_id": "usr_01M5X8Y9FJVHFPSVKTMXY0GJER"} F | mutants-4.log |
| G6 | the catalogue lets a member be an owner | go | KILLED | --- FAIL: TestRefused (0.00s) audit_test.go:309: a member as a second owner: &lt;nil&gt; FAIL | mutants-4.log |
| G7 | the command accepts partial member flags (equivalent: provisioning refuses the incomplete identity or role with ErrInvalidInput, which the command also maps to exit 2; the command's own check only refuses before connecting) | go | EQUIVALENT |  | mutants-4.log |
| G8 | the command ignores the member flags | go | KILLED | --- FAIL: TestProvisionWithMember (0.02s) main_test.go:101: organization with a member: 0 "{\"organization_id\":\"org_01M5X8YD8AKJ92Z3P7H9WY129S\",\"o | mutants-4.log |

## 實際執行結果

最後一輪在 eb6f17a（加上本報告與文件尚未 commit 的修改；這些修改只有文件與 evidence）上，依序執行（`exit-codes.txt` 有時間與完整命令；每個命令都是 `env -u RAILWAY_TOKEN -u RAILWAY_API_TOKEN …`，Go 為 go1.27.3、`GOTOOLCHAIN=local`，Python 為 `/home/user/Geminus/.venv/bin/python`）：

| 命令 | Exit | Log | 摘要 |
|---|---|---|---|
| `python -I scripts/check_deploy.py` | 0 | `final-check-deploy.log` | `PASS_STATIC_DEPLOY_CHECKS`；`auth0_cli` 為 1.37.0 與兩個 SHA-256 |
| `bash -n deploy/railway/deploy.sh` | 0 | `final-bash-n.log` | |
| `bash deploy/railway/deploy.sh --help` | 0 | `final-deploy-help.log` | |
| `bash deploy/railway/deploy.sh --dry-run` | 0 | `final-deploy-dry-run.log` | 步驟 2b 的計畫（Auth0 CLI 的每個命令，含 `--reveal-secrets` 那一行與登出）、seed 的 `-member-… editor` 與 membership 的檢查；`dry run: done, nothing was called` |
| `python -I scripts/rehearse_railway_deploy.py` | 0 | `final-rehearsal.log` | `rehearsal: PASS`：T-039 原本的執行（改為明確的 `GEMINUS_AUTH0_SETUP=manual`）全部照舊通過，加上 Auth0 的新情境；`A1 5 writes to Auth0, A2 0`；SIGTERM 於 `connections update` 與 `apps show --reveal-secrets` 都是 143 |
| `go test -count=1 ./internal/storage/provisioning ./cmd/provision-tenant ./internal/audit ./internal/storage/auditlog`（DB：`dt_test_t057a2`，`GEMINUS_REQUIRE_DB_TESTS=1`） | 0 | `final-go-test-db.log` | 4 個 package ok，DB 測試沒有 skip |
| 同上加 `-race` | 0 | `final-go-race-db.log` | 4 個 package ok |
| `make backend-test`（沒有 DB 的環境，照 Makefile） | 0 | `final-backend-test.log` | gofmt、`go vet`、`GEMINUS_FULL_DIFFERENTIAL=1 go test`：83 個 package ok、0 個 cached、0 個 FAIL |
| `make spec-check PYTHON=/home/user/Geminus/.venv/bin/python` | 0 | `final-spec-check.log`；填入本列之後再跑一次：`final-spec-check-again.log` | 每個子檢查都 PASS（`PASS_LIMITED_SPEC_CHECKS`、`PASS_STATIC_DEPLOY_CHECKS`、`PASS_DB_TESTS_FRESH` 等）；`checks/docs-check.json` 因本報告更新（138 個 Markdown、135 個相對連結）。spec-check 不執行 SQL，也不是完整的 OpenAPI 官方 validator |

之前的執行（同一個目錄）：

| 命令 | Exit | Log |
|---|---|---|
| 新的 Go 測試，實作之前（DB：`dt_test_t057a1`） | 1 | `red-go-provisioning.log`、`red-go-audit.log` |
| 實作之後 | 0 | `green-go-db.log`、`green-go-audit.log` |
| 新的 rehearsal 情境，在舊的部署腳本上 | 1 | `red-rehearsal.log` |
| `check_deploy.py`，`variables.json` 先改、規則未加 | 1 | `red-check-deploy.log` |
| `scripts/run_db_tests.sh`，新的 `dt_test_t057a2` | 0 | `db-dt_test_t057a2-setup.log` |
| `TestOrganizationWithMember`、`TestProvisionWithMember`，修正後在 `dt_test_t057a2` 連跑兩次 | 0、0 | `green-go-db-rerun1.log`、`green-go-db-rerun2.log` |
| Mutation（6 次執行） | 見上 | `mutation/` |

`dt_test_t057a1` 與 `dt_test_t057a2` 在最後刪除（`db-drop.log`：剩下 0 個 `dt_test_t057*`）；只在既有的 throwaway cluster 上建立與刪除這兩個 database，沒有啟動或停止任何 cluster。

## NOT RUN

- 任何真正的 Auth0、Railway 或 AI provider 呼叫：`deploy.sh --apply`、`--install-auth0-cli`（真的下載與安裝；archive 是為了計算 SHA-256 另外下載的，沒有執行）、裝置登入、Auth0 的任何寫入。Auth0 是否接受每個 payload（例如 `--data` 的本機 schema 驗證、`refresh_token` 與 `jwt_configuration` 的 PATCH 行為、新應用程式預設啟用哪些 connection）只依 CLI 原始碼、go-auth0 的型別與 Auth0 文件，由部署 session 第一次執行時確認。
- 真正的 keyring 環境（桌面）：部署腳本關掉 session bus，CLI 的 session 只在它的 home；有 keyring 的機器上沒有試。
- `make e2e-test`（依指示不執行；`apps/` 沒有改）、`make frontend-test`（沒有改前端）、`make integration-test` 的全部（只跑了改過的 package 的 DB 測試，見上表）、`make backend-race` 的全部（只跑了改過的 package 的 `-race`）。
- shellcheck（沒有安裝）。
- SIGHUP 在 Auth0 步驟中（同一個 handler，見上）。

## 發現

1. T-039 的部署腳本把 Railway token 明文放在遮蔽清單（部署腳本的狀態）中，`child_env()` 的回傳值在 `run()` 中被加上 token：都已修正（digest、複製）。
2. CLI 1.37.0 沒有 keyring 時，`auth0 logout` 在把 tenant 從 config 移除之後，刪 keyring 失敗、exit 1（`internal/cli/logout.go`）：部署腳本以 `tenants list` 判斷。
3. CLI 1.37.0 的 `auth0 api` 不遮蔽 `client_secret`（1.38.0 才改）；也會把 tenant 名稱送到 analytics，除非 `AUTH0_CLI_ANALYTICS=false`。
4. 新 tenant 的社群 connection 讓任何人可在 Auth0 建立使用者；只關 database connection 的 sign-up 不足以「關閉自行註冊」（H-035 (2)），所以應用程式只開 database connection。
5. Provisioning 不能把成員加到已存在的組織（migration 0004，刻意的）：評審帳號只能在 seed 時加入；demo 組織先 seed 過時由 owner 以 membership API 加入（目前沒有 UI）。
6. 評審密碼只能在執行結束時顯示：若部署 session 的 stderr 被存成檔案（例如 evidence），密碼就會在檔案中。部署 session 不要把 `--apply` 的 stderr 存進 evidence。
7. Mutation 找到兩個測試的問題（見「Mutation」）：被拒絕的成員的 Go 測試共用固定的 subject，在同一個 DB 上互相污染（已修正，1112af2）；grant types 不計順序的比較沒有任何情境證明（A2 已補上，eb6f17a）。

## 部署當天 owner 要做的事

1. 事先：Auth0 tenant、自己的使用者（在 `Username-Password-Authentication`），告知 tenant 網域與自己的 `user_id`。
2. `--apply` 到步驟 2b 時，在自己的瀏覽器打開畫面上的網址、確認 code、核准。
3. 執行結束時把 stderr 上的評審 email 與密碼抄進提交說明。

## 待決定（coordinator／owner）

1. 應用程式只開 database connection（其他 connection 停用），超出 H-034 的字面，是否接受。
2. 評審帳號的 email：`judge@geminus-demo.invalid`（不是真的信箱）或 owner 指定的。
3. `provision-tenant` 的成員只在建立組織時加入、不放寬 migration 0004，是否接受；audit catalogue 的兩個新欄位要不要另記 D 編號（D-091 的延伸）。
4. ADR-029 的 addendum（PROPOSED）。
5. `planning/tasks.json` 中 T-057 的 completion_evidence 由 coordinator 記錄（本切片沒有改 planning）。

## Commits（branch `t057-auth0`，從 095a234；沒有 push、沒有 PR）

| Commit | 內容 |
|---|---|
| fd404ad | `provision-tenant` 在建立組織時多加一個成員（editor 或 viewer）、audit catalogue 的 `member_user_id`／`member_role`、Go 的測試（H-035 (2)） |
| 5317e7f | 部署腳本的 Auth0 步驟（2b）、Auth0 CLI 的 pin 與 `--install-auth0-cli`、`variables.json` 的 `auth0` source、`check_deploy.py` 的規則、rehearsal 的替身與新情境、secret 改為 digest（H-034、H-035 (2)） |
| ae60084 | Rehearsal 分辨兩層各自的效果（帶 secret 的錯誤完全不顯示、遮蔽登記、A14、archive 的檢查） |
| 1112af2 | 被拒絕的成員的測試每次都用新的 subject（mutation 發現的污染） |
| eb6f17a | A2 讓替身以另一個順序列出 grant types（mutation 發現的缺口） |
| 最後一個 | 本報告、evidence、deploy runbook §7、Auth0 runbook §6、ADR-029 addendum、SECURITY §6 |

## Coordinator 驗證（2026-10-27）

`t057-auth0` 的六個 commit（fd404ad..da6cde6）cherry-pick 到主線；只有產生的 `checks/docs-check.json` 衝突（取分支的版本後以 `make spec-check` 重新產生，c285be8）。

指令與結果（evidence：`checks/evidence/2026-10-27-T-057auth0-coordinator/`，`exit-codes.txt` 依執行順序；HEAD c285be8；go1.27.3；全部以 `env -u RAILWAY_TOKEN -u RAILWAY_API_TOKEN` 執行；DB 測試在暫時的 cluster（`/var/lib/postgresql/geminus-throwaway-hkb6`，port 55450））：

| 指令 | exit | 說明 |
|---|---|---|
| `make spec-check` | 0 | |
| `scripts/check_deploy.py` | 0 | |
| `scripts/rehearse_railway_deploy.py` | 0 | `rehearsal: PASS`（替身；Railway 與 Auth0 本身 NOT RUN） |
| `bash -n`、`deploy.sh --help`、`deploy.sh --dry-run` | 0、0、0 | |
| `make backend-test "GOFLAGS=-count=1 -timeout=30m"` | 0 | 83 個 package ok |
| `make architecture-check` | 0 | 95 packages、26 rules |
| `go test -race -count=1 -timeout 30m`（provision-tenant、tenant、audit、storage） | 0 | 19 個 package ok |
| `make integration-test`（新的 dt_test_hkb10c，跑完刪除） | 0 | 38 個 package ok，沒有 FAIL 或 SKIP |

NOT RUN：任何真實的 Auth0、Railway 或 AI 呼叫（`--apply`、真正的 `--install-auth0-cli`、裝置登入）；e2e。

Coordinator 對本報告問題的處理：
1. App 只開在 database connection（Google 等社群連線關閉）：接受。這是 H-035 (2)「只有 owner 與一個評審帳號能登入」的必要條件，不是擴大 H-034。
2. 評審帳號的 email：預設 `judge@geminus-demo.invalid`；owner 要用其他 email 時在部署前告知。
3. 成員只在建立 organization 時加入（不放寬 migration 0004）：接受，demo 是新的部署；新的兩個 audit 欄位是例行的實作，不另立 D 編號。
4. 本步驟處理 Auth0 client secret、評審密碼與 Railway token，部署前做一輪獨立審查（H-029），進行中；ADR-029 的 addendum 維持 PROPOSED。

## Review round 1 fixes（2026-10-27，coordinator）

審查報告：`checks/T-057_AUTH0_REVIEW_r1.md`（**依 coordinator 保存的結論重建**，原檔沒有保存）；CHANGES REQUESTED，M1、M2 與 L1–L6。依 H-029 修 M1、M2；L1、L2 的修法很小，一併修正；**L3–L6 延後**（Low）。全部以 `env -u RAILWAY_TOKEN -u RAILWAY_API_TOKEN`（以及不帶任何 AI key）執行；沒有呼叫 Railway、Auth0 tenant 或 AI；沒有 `--apply`。Evidence：`checks/evidence/2026-10-27-T-057-auth0-r1/`。

### 修改

- **M1**：`A0_SECRET_FIELDS` 移除 `password`，新的 `A0_VALUE_FIELDS`（原 `A0_TOKEN_FIELDS` 加 `password`）只在值**不是**物件時丟棄。所以 `options.authentication_methods.password`（`{"enabled": true}`）照常保留，帳號的密碼字串仍然丟棄。物件中的欄位經同一個 hook，物件內若有已知的 secret 欄位仍會被丟棄。替身 tenant 的 database connection 加上 `authentication_methods`（password、passkey）。
- **M2**：stdout 寫不出去（任何 `OSError`：EPIPE、EIO）時，`say()` 記下 `out_lost`，把 stdout 的 fd 換成 `/dev/null`（child 與 Python 結束時的 flush 都不再碰到壞掉的 stream），並在下一個不在 cleanup 中的訊息處 raise `OutputLost`。Cleanup 包在 `@cleanup` 裡（`a0_close`、`proxy_close`、`check_proxies`），裡面的訊息不 raise。`exit_on_signal` 的訊息也容錯。最後的訊息走 `tell()`，不 raise。結束碼為 **74**（`EX_IOERR`）；signal 照舊 128+n；`reveal` 寫 stderr 失敗時不 traceback（密碼遺失，`GEMINUS_JUDGE_RESET_PASSWORD=1` 可重設）。
- **L1**：`seal_now` 回傳成功與否。Client secret 沒有 sealed 時記入 `late`，所以 verification 失敗（不是 `done`），訊息寫明：重跑會 seal，或在 UI 手動 seal。重跑時 web 已有這個應用程式的 secret 但沒有 sealed 的話，不再讀 secret，直接 seal。
- **L2**：preflight 讀 PATH 上的 `auth0` 一次並 hash。通過時，把**同一份 bytes** 寫進 `tempfile.mkdtemp`（0700）中的新檔（`O_EXCL`，0700），只執行該副本；deployer 結束時刪除（`atexit`）。替身的設定檔改由 rehearsal 寫入絕對路徑（因為執行的是副本），替身的 log 記下被執行的路徑。
- Runbook §7.8：sign-up 列（M1）、client secret 列（L1）、登出列（M2）、CLI 的 pin（L2）。

### Rehearsal 的新情境

| 情境 | 檢查 |
|---|---|
| A1–A15 的 connection 帶 `authentication_methods.password`（M1） | 第一次：5 次寫入、設定與 H-034 相同；重跑：0 次寫入（`Auth0 step: A1 5 writes to Auth0, A2 0`） |
| M2 stdout closed at the logins（TCP proxy 開著） | exit 74、沒有 traceback、proxy 已刪除 |
| M2 epipe in the Auth0 step | exit 74、沒有 traceback、CLI 已登出且 home 已刪、pipe 關閉後沒有任何寫入（沒有 `connections update`） |
| M2 hup in the Auth0 step（pipe 關閉後 SIGHUP） | exit 129，其餘同上 |
| A15.1 Railway 拒絕 seal client secret（L1） | `FAIL: verification:`，訊息含復原步驟，沒有 `deploy: done` |
| A15.2 重跑（L1） | `done`；`BFF_OIDC_CLIENT_SECRET` 已 sealed；沒有再呼叫 `apps show --reveal-secrets` |
| 每個 Auth0 情境（L2） | 被執行的不是 PATH 上的 `auth0`（替身 log 的路徑） |

Pipe 的關閉以 EPIPE 模擬。Hangup 的終端機回的是 EIO，同樣是 `OSError`，走同一條路徑；真正的 tty hangup NOT RUN。

### Red → green（每個修正單獨拿掉，其他保留）

| 拿掉 | rehearsal exit | 失敗的情境 |
|---|---|---|
| M1 | 1（`m1-red.log`） | A2、A3、A6、A8–A12、A14 停在「the options of … hold settings this script does not keep (custom database scripts …)」，正是審查描述的錯誤 |
| M2 | 1（`m2-red.log`） | 三個 M2 情境皆為 exit 120 並有 `BrokenPipeError` traceback；proxy `tcp_1` 留著；CLI 沒有登出 |
| L1 | 1（`l1-red.log`） | A15.1 為 `done`；A15.2 沒有 seal |
| L2 | 1（`l2-red.log`） | A1、A10、A12、A13、A15 等：執行的是 PATH 上的 `auth0` |
| 全部套上 | 0（`m1-green.log`、`m2-green.log`、`l1-green.log`、`l2-green.log`、`rehearsal.log`） | `rehearsal: PASS` |

### 指令與 exit code（最後的程式碼；`exit-codes.txt`）

| 指令 | exit |
|---|---|
| `python3 scripts/check_deploy.py` | 0 |
| `python3 scripts/rehearse_railway_deploy.py` | 0（`rehearsal: PASS`） |
| `bash -n deploy/railway/deploy.sh` | 0 |
| `deploy.sh --help` | 0 |
| `deploy.sh --dry-run`（go1.27.3 在 PATH 上） | 0（第一次 PATH 上沒有 `go`，preflight exit 1：`deploy-dry-run-no-go-on-path.log`） |
| `python3 -m py_compile`（deployer、rehearsal） | 0 |
| `make spec-check` | 0 |

Go 沒有修改，所以沒有重跑 `make backend-test`（這次的 commit 只動 Python 與文件）。

### 延後（Low，H-029）與部署時的處理

- **L3**（補 rehearsal mutants V2–V7、V10、V12、V14 與 Go mutant GX2）：審查原文沒有保存，各 mutant 的定義無法重建；延後。
- **L4**（評審密碼在背景 `--apply` 時會寫進輸出檔）：部署由 coordinator 在前景執行，密碼交給 owner 後不留存；延後。
- **L5**（psql、`go run`、Railway CLI 會繼承 session 的 AI key）：部署時 coordinator 只把 `RAILWAY_TOKEN` 載入 `--apply` 的環境，AI key 不在其中；延後。
- **L6**（報告與 runbook 說得過頭的兩處）：延後。

### NOT RUN

- 真實的 Auth0、Railway、AI 呼叫（`--apply`、`--install-auth0-cli`、裝置登入）。
- 真正的 tty hangup（以 pipe 的 EPIPE 代替）。
- L3–L6。

注意：原本負責修正的 agent 在用量上限停止、工作遺失，所以這次的修正由 coordinator 自己完成並驗證（本節），**修正者與驗證者是同一人**，沒有另一個人檢查修正本身。依 H-029，M1、M2 修完並經 coordinator 驗證即可部署。

CI（0bc6161，run 40495844724）：**spec-check 失敗**，其餘 5 個 job 成功。原因是 commit 漏了重新產生的 `checks/docs-check.json`：新增的 `T-057_AUTH0_REVIEW_r1.md` 讓 `markdown_files` 由 139 變成 140，CI 的「Committed check reports match a fresh run」因此失敗。這不是程式的失敗，下一個 commit 會帶上重新產生的檔案。

## 第一次 `--apply`（2026-10-27，coordinator）：project token 無法建立服務

Owner 授權部署；`.env` 的 `RAILWAY_TOKEN` 是 owner 確認過的 project token（owner 的既有 project，environment `production`）。環境只有 `RAILWAY_TOKEN`、`GEMINUS_AUTH0_ISSUER`、`GEMINUS_SEED_OWNER_SUBJECT`、`GEMINUS_AUTH0_AUDIENCE`、`GEMINUS_RAILWAY_BUCKET_REGION`，沒有任何 AI key。

**經過：**
1. Preflight 通過。`railway status` 成功。
2. `railway add --database postgres` 以 exit 1 結束：「Project not found. Run `railway link`」。
3. 腳本以 `FAIL` 結束，到 Auth0 步驟之前就停了。

**原因**（CLI v5.64.2 的原始碼：`src/commands/add.rs`、`src/config.rs` 的 `get_linked_project`／`get_linked_project_mut`／`link_service`、`src/commands/link.rs`）：
- 在 project token 下，`get_linked_project` 由 token 取得 project，所以 `status` 成功。
- `add` 建立服務之後，`link_service` 要改本目錄在 `~/.railway/config.json` 的 link。這個目錄從來沒有被 link 過，所以回 `ProjectNotFound`。
- `railway link` 要列出 workspace，project token 做不到。

所以 **`Postgres` 服務已經建立了**（之後以 `railway status --json` 唯讀確認：project 中有 `Postgres` 和一個原本就有的 `Geminus`，後者不是腳本建的，不會被動到）。

Rehearsal 的 Railway 替身沒有模擬這個行為，所以沒有抓到。

**修正：**
- `resources()`：在 project token 下，只要缺 database 或任何服務，就在任何 `add`、`bucket create` 之前停下，訊息說明原因，並要求改用 `RAILWAY_API_TOKEN` 加上 `GEMINUS_RAILWAY_PROJECT=<project 的名稱>`。
- Rehearsal 新增 T1：project token、沒有服務的 project。必須在建立任何東西之前停下。
  - Red：拿掉這個檢查時 T1 失敗，因為它一路建立服務（`project-token/rehearsal-red.log`）。
  - Green：`rehearsal: PASS`（`project-token/rehearsal-green.log`）。
- Runbook §7.7 的 Token 段落：project token 只適用於服務都已存在的 project。

**下一步**：owner 建立 account 或 workspace token（`RAILWAY_API_TOKEN`）後，以 `GEMINUS_RAILWAY_PROJECT=<那個 project 的名稱>` 重跑 `--apply`。已經存在的 `Postgres` 會被找到並沿用，不會重建。

### 沒有 token 時改用 CLI 的裝置登入（2026-10-27）

Owner 當時碰不到電腦，無法把 account token 寫進 `.env`；token 也不經對話傳遞。所以改用 Railway CLI 的裝置登入：
1. Coordinator 執行 `railway login --browserless`。
2. Owner 在手機打開 CLI 印出的網址並核准，核准前先核對代碼。
3. 登入成功（`whoami` 列出 owner 的 workspace）。Session 只存在 CLI 自己的 `~/.railway`，腳本不讀。

Deployer 的 preflight 改為：沒有 `RAILWAY_TOKEN`、`RAILWAY_API_TOKEN` 時，以 `railway whoami --json` 確認 CLI 已登入。已登入就視同 account token；沒有登入就在 preflight 停下。

Rehearsal 新增兩個情境：
- **T2**：沒有 token、已登入 → `done`，並且走 `link` 的路徑。
- **T3**：沒有 token、沒有登入 → preflight 停下，除了 `--version` 與 `whoami` 之外不呼叫任何東西。

Red／green（evidence 在 `session/`）：
- Red：拿掉這個修改時 T2、T3 失敗（`rehearsal-red.log`）。
- Green：`rehearsal: PASS`（`rehearsal-green.log`）。
- `check_deploy.py`、`bash -n` 都是 exit 0。

Runbook §7.7 的 Token 段落已更新。部署完成後執行 `railway logout`。

## 第二次 `--apply`（2026-10-27，coordinator）：`enabled-clients update` 不認得 `--data @-`

使用已登入的 Railway CLI，沒有任何 token 或 AI key 在環境中。前面的步驟都成功：
- 步驟 1、2：project、bucket、四個服務、config files、網域。已經存在的 `Postgres` 沿用，沒有重建。
- 步驟 2b：owner 核准 Auth0 的裝置登入之後，owner 的使用者檢查通過，API 建立，web application 建立，sign-up 本來就是關的。真實 tenant 上 M1 的修正有效：connection 的 options 沒有讓腳本停下。

接著 `auth0 connections enabled-clients update <id> --data @-` 以 exit 1 結束：「invalid --data value: must be a JSON array of {client_id, status}: invalid character '@'」。腳本的清理都有執行：CLI 已登出、home 已刪除、沒有開著的 TCP proxy、stderr 是空的。

**原因**（CLI v1.37.0 的原始碼）：
- `internal/cli/connections.go:499-507`：`enabled-clients update` 直接 `json.Unmarshal` `--data` 的值。
- 其他指令（`apis`、`apps`、`users`、`connections create/update`）經 `readJSONInput`（`internal/cli/data_json.go:125-127`），那裡會從 stdin 讀 `@-`。
- 沒有 `--data` 時，`ResolveData` 以 piped stdin 為 payload（`data_json.go:163-188`）。

逐一核對過，只有這一個指令是例外。

**修正：**
- 這個呼叫不加 `--data`，JSON 仍經 stdin。
- Rehearsal 的替身照真實 CLI 處理這個指令（`--data` 的值就是 JSON；沒有 `--data` 時讀 stdin）。
- Red：只拿掉 deployer 的修正時，A1 失敗，訊息正是 live 的 `auth0 connections enabled-clients failed`（`enabled-clients/rehearsal-red.log`）。
- Green：`rehearsal: PASS`（`enabled-clients/rehearsal-green.log`）。
- Runbook §7.8 的表格已更新。

## 第三次 `--apply`（2026-10-27，coordinator）：Postgres 沒有 `DATABASE_PUBLIC_URL`

Owner 再次核准 Auth0 的裝置登入。步驟 1、2、2b、3 都成功：
- **API、application**：與 H-034 相同，沒有改動。
- **社群登入**：Google connection 停用這個 application，application 只經 database connection 登入。
- **評審帳號** `judge@geminus-demo.invalid`：已建立，密碼只在 stderr 出現一次，存在 coordinator 的私有檔，交給 owner 後刪除。
- **Client secret**：經 stdin 設在 web 上，並且 sealed。
- **變數**：已設定。

接著步驟 4 開了 TCP proxy，但 Postgres 的變數裡**沒有 `DATABASE_PUBLIC_URL`**（以 `railway variable list` 唯讀確認，只看名稱）。等了 300 s 後，腳本以「the TCP proxy did not become active」停下。它開的 proxy 已移除，沒有開著的 proxy。

**原因**：這個 database 是第一次執行時以 `railway add --database postgres`（CLI 5.64.2）建立的，它的變數有 `DATABASE_URL`、`PGHOST` 等，但沒有範本平常會定義的 `DATABASE_PUBLIC_URL`。

**修正**：`admin()` 在等待之前先看名稱。沒有這個變數時，照範本的寫法以 `--skip-deploys` 定義它：`postgresql://${{PGUSER}}:${{POSTGRES_PASSWORD}}@${{RAILWAY_TCP_PROXY_DOMAIN}}:${{RAILWAY_TCP_PROXY_PORT}}/${{PGDATABASE}}`。值全部是 reference，argv 中沒有任何 secret；proxy 生效後由 Railway 解析。

Rehearsal 的替身新增 `REHEARSAL_NO_PUBLIC_URL`（一個沒有定義這個變數的 database）與 T4 情境：
- T4 必須只定義一次、只用 reference、帶 `--skip-deploys`，並且繼續跑完。
- Red：拿掉 deployer 的修正時，T4 停在 live 的同一句錯誤（`public-url/rehearsal-red.log`，在另一個 worktree 跑，真的等滿 300 s）。
- Green：`rehearsal: PASS`（`public-url/rehearsal-green.log`）。

## 第四次 `--apply`（2026-10-27，coordinator）：`environment edit` 的修改全部沒有生效

Owner 第三次核准 Auth0 的裝置登入。步驟 1–4 成功：`DATABASE_PUBLIC_URL` 已定義，TCP proxy 連通，四個 login 已建立。步驟 5 的 migrate 部署 **CRASHED**。

Log（`railway logs`，URL 中的密碼已遮蔽）顯示容器執行的是 `ai-eval-live`（live AI 是關的，所以它拒絕執行，沒有任何 provider 呼叫），不是 `geminus-migrate`。Railway 以 RAILPACK 建置 Go，取了第一個 `cmd/`。

唯讀查看 `railway environment config`（只看 build／deploy 的鍵，不看變數）：四個服務都是 `builder: RAILPACK`，**沒有 `configFile`**。

**原因**（CLI v5.64.2，`src/commands/environment/edit.rs` 的 `get_edit_config`）：
- stdin 不是終端機時，CLI 以 piped stdin 為 change set，**優先於** `--service-config` 參數。
- stdin 是空的時候，它印出 `{"staged": false, "committed": false, "message": "No changes to apply"}`，並以 exit 0 結束。
- Deployer 以子程序執行 CLI，stdin 不是終端機，而且只看 exit code。所以**每一次** `environment edit` 都被丟棄了：config-as-code 的設定，以及所有的 seal。
- Deployer 卻回報了「sealed」。

以變數清單唯讀確認，只看值是不是 null（sealed 時 CLI 回 null），不讀值：
- migrate、api、worker、web 的 9 個 secret 全部 **NOT sealed**（`GEMINUS_*_DB_PASSWORD`、`OBJECT_STORAGE_S3_*`、`BFF_OIDC_CLIENT_SECRET`、`SESSION_KEYS`）。
- 它們只在這個 Railway project 內可見（owner 的 project，沒有其他成員），沒有出現在 repo、log、報告或對話中。

**修正：**
- `env_edit()`：change set 以 JSON 經 stdin 送出（`{"services": {<service ID>: {...}}}`），回應必須是 `committed: true` 才算成功。
- Config 檔的設定沒有 commit 時，以 `Failed` 停下。
- Seal 沒有 commit 時，記入 `late`，讓 verification 失敗：web 的 client secret 照 L1 處理，產生與複製的 secret 也一樣；重跑會重新 seal。
- 送出的 patch 與 `--service-config variables.X.isSealed true` 產生的完全相同（`ServiceInstance.variables[X] = {"isSealed": true}`，同一個 `environmentPatchCommit`），只是改從 stdin 送出。

Rehearsal：
- 替身照 CLI 5.64.2 處理 `environment edit`：piped stdin 是 change set，空的就是「No changes to apply」並 exit 0。
- Run 1 新增檢查：四個服務的 config 檔都必須 commit。
- Red：只拿掉 deployer 的修正時，sealed 與 config 檔的檢查失敗，A1 的 client secret 也沒有 sealed（`env-edit/rehearsal-red.log`）。
- Green：`rehearsal: PASS`（`env-edit/rehearsal-green.log`）。

**另外**：project 中原本就有的 `Geminus` 服務接了這個 repo 的 `claude/lucid-meitner-vq1awo` branch（RAILPACK）。它不是 deployer 建的，所以不會被動到；是否刪除由 owner 決定。

### 第五次 `--apply`（2026-10-27）：`configFile` 被 commit 了，Railway 卻沒有保存

改成以 stdin 送出之後，config-as-code 的 patch 得到 `committed: true`。但讀回 `railway environment config`（只看 build／deploy）時，四個服務仍然沒有 `configFile`，builder 仍是 RAILPACK。所以 coordinator 在 owner 核准 Auth0 之前，就以 SIGTERM 停止了這次執行：exit 143，CLI 已登出，沒有開著的 proxy。

**修正：**
- 同一個 patch 中，直接寫入每個服務 config 檔的 `build` 與 `deploy`（不送 `numReplicas`，replica 數由 `multiRegionConfig` 管理）。這些欄位都是 CLI 5.64.2 `BuildConfig`／`DeployConfig` 的欄位，名稱一一對應。`configFile` 照樣送。
- Commit 之後以 `railway environment config --json` 讀回，確認 builder 是 `DOCKERFILE`，而且 `dockerfilePath` 正確，否則在任何部署之前停下。讀回時，`variables` 物件在解析的當下就被丟掉。

Rehearsal：
- 替身新增 `environment config`。它刻意在回應中帶上變數的值，讓 leak 檢查能確認 deployer 沒有留下任何值。
- 新增 `REHEARSAL_DROP_BUILD`（committed 了但沒有保存）與 T5 情境：必須在任何部署之前停下。
- Run 1 新增檢查：四個服務的 build 設定都必須與 config 檔相同。
- Red：拿掉 deployer 的修正時，build 檢查與 T5 失敗（`builds/rehearsal-red.log`）。
- Green：`rehearsal: PASS`（`builds/rehearsal-green.log`）。

## 第六、七次 `--apply` 與 web 的重新部署：部署完成（2026-10-27，coordinator）

- **第六次**：build 設定的讀回檢查通過，四個服務都是 `DOCKERFILE`。Owner 核准 Auth0 時，Auth0 回「Invalid network change. Login again from a trusted network.」，腳本以 FAIL 結束；cleanup 照常，沒有改動任何東西。
- **第七次**（owner 再次核准）：
  - **步驟 2b**：Auth0 的設定沒有任何改動；評審帳號已存在，密碼不變；web 的 client secret 不再讀取，直接 seal。
  - **步驟 3–6**：變數已設定；四個 login 已建立；migration 17 個；seed 建立了 demo organization，評審帳號只是它的 editor，沒有其他 membership。
  - **步驟 7**：api SUCCESS、worker SUCCESS，web **FAILED**。Build log 只有一行「builder v4」，沒有錯誤訊息，17 秒內結束。
- **web 的重新部署**（coordinator 以 deployer 同樣的 `railway up --service web …` 執行，沒有重跑整個 `--apply`，避免 owner 再核准一次）：
  - 第一次重新部署被標為 **SKIPPED**：web 的 `watchPatterns` 只有 `/apps/web/**`，而這次上傳與上一次的檔案相同，Railway 判定沒有符合的變動。
  - 以 `environment edit`（JSON 經 stdin，committed）清空 web 的 watch patterns 後再部署：**SUCCESS**（約 120 s）。
  - Deployer 改為在 patch 中送出 `watchPatterns: []`。替身沒有模擬 Railway 的 watch pattern 跳過，所以這個修正**沒有 red**；rehearsal 只確認送出的 patch 帶有 `[]`（`watch-patterns/rehearsal-green.log`，PASS）。Live 的 web 重新部署就是它的證據。

**部署後的驗證**（`watch-patterns/live-verify.txt`，2026-10-27T14:48Z；等同 deployer 步驟 8 的 HTTP 部分，由 coordinator 執行）：
- api：`/healthz` 200、`/readyz` 200、`/v1/me` 401。
- web：`/` 200；`/auth/login` 303，導向 tenant 的 `/authorize`。
- Security header：CSP（`default-src 'none'`、nonce、`strict-dynamic`）、HSTS、`X-Frame-Options: DENY`、`nosniff`、`Referrer-Policy: no-referrer`、`Permissions-Policy`。
- 9 個 secret 都 **sealed**（以變數清單唯讀確認，只看值是否為 null）。
- Postgres 沒有開著的 TCP proxy。

**NOT RUN**（部署後要確認的事，見 runbook §8 與交接 4.2）：
- 以評審帳號實際登入。
- 整個 demo 流程與照片流程（live AI 是關的：worker 的 `AI_PROVIDER=disabled`；要不要開由 owner 決定，key 由 owner 填並 seal）。
- `GOMEMLIMIT`：API 服務的記憶體上限還沒確認，所以沒有設定。
- 部署結束後執行 `railway logout`。

project 中原本就有的 `Geminus` 服務（接了這個 repo 的 branch）沒有動到，是否刪除由 owner 決定。

## 部署後確認：live AI 與完整流程（2026-10-27，coordinator，owner 授權）

**Live AI**（owner 的決定）：
- Railway CLI 以裝置登入，owner 核准。
- worker 設 `GEMINUS_MUSE_API_KEY`：由 `.env` 經 shell builtin `printf` 傳入 stdin，不在任何程式的參數中，之後立刻 seal（以 stdin JSON，`committed: true`）。
- api 與 worker 都設 `AI_PROVIDER=meta`、`AI_POLICY_APPROVAL=H-029`、`AI_MODEL=muse-spark-1.3`、`GEMINUS_AI_LIVE=on`。`AI_VISION_*` 不設，照片路徑跟 `AI_PROVIDER`。
- 讀回確認：key 只在 worker 上而且 sealed，api 沒有 key。
- 重新部署後，worker 的 log 有 `LIVE_AI_ON`、`PHOTO_PATH_ON`。

**發現 1：demo 沒有內容。**
- Seed 只建立 organization 與評審的 membership。評審登入後看到「No projects yet.」，介面也沒有建立 project 的入口。
- 處理：以評審的 session，經網站自己的 BFF（`/api/v1`，同源、帶 `X-Geminus-Request: 1`，`Idempotency-Key`）建立 project「Demo electronics line (synthetic)」，並以範本 `electronics_line`／`electronics_assembly_test_line` 建立 model（version 1）。資料在資料庫中，重新部署不會消失。
- 待辦：seed 或 runbook 要加上這一步。

**發現 2：Meta 從 us-west2 回 404。**
- 第一次跑完整流程時，文字提案與照片提案的第一個呼叫都得到 `provider_status=404`、`not_found_error`（`AI_REQUEST_REJECTED`，花費 0）。
- 已確認：
  - `.env` 的 key 在本機的 `/v1/models` 回 200，列出 `muse-spark-1.3`、`sam-3.1`。
  - 無效的 key 是 401，不是 404。
  - 從 Railway 的 web 容器不帶 key，三個端點都是 401，與本機相同，所以網路沒有被整個擋掉。為此暫時登錄了一把專用的 SSH key，測完已從 Railway 刪除並在本機銷毀。
- 以真正的 key 從容器測 `/v1/models` 這一步，被 Claude Code 的自動權限檢查擋下，沒有執行。
- Owner 選擇把 worker 搬到 `asia-southeast1-eqsg3a`（Singapore；environment patch，committed，讀回確認）。之後就正常了。
- 原因推定為 Meta 依地區回應（未以 key 直接證實）。
- 待辦：把 worker 的 region 寫進 deployer 的 patch。

**完整流程**（Playwright 1.56.1，照 `apps/web-e2e` 的 lockfile 安裝；評審帳號；live 網站；腳本 `workflow.cjs.txt`，密碼只經環境變數，不在檔案中；log `workflow-singapore.log`；截圖在同一個資料夾）：

| 步驟 | 結果 |
|---|---|
| 以評審帳號經 Auth0 登入 | 成功 |
| 文字提案（scenario）：「Add a second ICT tester…」 | 約 50 s 後 **ready**：ICT testers 的 capacity 2 → 3。Server：**Low risk**，所有檢查通過；AI 自評 90% |
| 批准 → 開啟 scenario → 執行 baseline 與 scenario | 兩者都 **Completed 5 of 5 replications** |
| 比較 | 中位 lead time **469.51 min → 461.11 min（縮短 1.8%）**，並有 95% CI 與「有工件在結束時仍在線上」的提示 |
| 照片提案（demo 平面圖） | SAM 找到 10 個框；judge 回答（output 15,418 tokens，在 32,000 的上限內）；**server 拒絕（`invalid`）**：6 個檢查失敗，新增的站沒有任何 source 能到達，另有一個 routing 機率無效 |

花費（worker 的 log）：文字 USD 0.042401、SAM USD 0.02、照片 judge USD 0.08250775，合計約 **USD 0.145**。

照片 judge 的 output 為 15,418 tokens，超過舊的 8,192：今天提高到 32,000 確實是必要的。

**發現 3：照片提案在完整範本上被拒。**
- 上午的照片 smoke 以空的 `bare-line` 為 base，結果是 ready。
- 在已經有各站的範本上，AI 新增了沒有接進產線的站，server 照規則拒絕。這是安全檢查應有的行為，但 demo 的照片流程需要決定做法（owner／coordinator）。

**NOT RUN**：照片提案的批准；以 owner 帳號登入；手機尺寸的畫面。

## 部署後確認（二）：照片流程、audit 修正、結果頁 polish、Meta 的 404（2026-10-28，coordinator）

證據：`checks/evidence/2026-10-28-live-photo-and-polish/`（腳本 `bare.cjs.txt`、`shots.cjs.txt`，密碼只經環境變數；各次 log；截圖）。

**照片 demo 的 base**：
- 在 demo project 建立一個模型，再經網站的 API 把它的 draft patch 成 `testdata/photos/bare-line.json`。照 D-020 的規則，每個陣列元素先 `test` 它在 `internal/model/patch/policy.go` 中的識別欄位：`constraints` 用 `node_id`，其餘用 `id`。之後發佈成 version 2。
- 第一次嘗試 patch 被拒（422 `PATCH_TEST_REQUIRED`），而**腳本沒有停下**，照樣在範本版本上跑了照片提案（被 server 拒絕，約 USD 0.1）。之後腳本改為 patch 或發佈失敗就停止，不再呼叫 AI。

**照片提案在空產線上：audit 的錯誤**：
- 照片提案寫 audit 時超過欄位上限（`more than 32 fields`），job 重試了 3 次，約 USD 0.19。
- 修正見 `checks/T-056_VALIDATION.md` 的「Live：成功的照片提案寫 audit 時超過欄位上限」（79938df）。

**部署**：fa057f5（audit 修正與結果頁的 disclosure）以 `railway up` 部署 api、worker、web，三者都 SUCCESS。沒有重跑 `--apply`，避免 owner 再核准一次 Auth0。

**Meta 的 404 與出口 IP**：
- 部署之後，Singapore 的 worker 又得到 404（文字與 SAM 都是，花費 0）。
- 只重新部署 worker，換一個執行個體之後，就恢復正常了。
- 所以 Meta 是否接受，和 worker 這一次的出口位置有關，不只是 region。這是 demo 的風險（見下方的待決事項）。

**修正後的 live 結果**：

| 步驟 | 結果 |
|---|---|
| 文字提案（scenario） | ready，批准之後模擬 5 of 5 完成，比較的數字與前一次相同（469.51 → 461.11 min，同一個 seed） |
| 照片提案，在空產線 version 2 上 | **ready**：AI 依序加入 AOI、ICT（2 台測試機）、FCT、Pack，並改接 SMT 之後的連線。Server：**Medium risk**，所有檢查通過；AI 自評 80%。批准之後成為 **version 3** |
| 結果頁（polish 之後） | 主表 27 列；「Each step's measures (110)」預設關閉；Marey 圖緊接在表格下方。另有 390 px 的截圖 |

**發現（交給 owner／之後處理）**：
1. **出口 IP**：Meta 對部分 Railway 出口回 404。可行的方向：
   - owner 查看 Meta dashboard 上這把 key 有沒有網路或地區的限制；
   - 改用 Railway 的固定出口 IP；
   - demo 前先以一個文字提案確認，404 時重新部署 worker。
2. **結果頁的網址**：結果頁的網址帶著兩次執行的 id（`?baseline=…&scenario=…`），只有在按下 Run 之後才會到達；scenario 頁面沒有連到既有結果的連結。離開之後只能重跑，或用瀏覽器的上一頁。
3. **AI 加的站的位置**：照片（或任何 AI）加入的站沒有 2D 位置，被排到右邊，而原本的 `Shipped` 留在原位，所以 version 3 的畫布上「出貨」出現在產線中間。
4. **AI 的文字**：「What the AI assumed」會出現 prompt 內部的區塊名稱（`CURRENT_MODEL_IR`、`USER_INTENT`、`CONTEXT_OMISSIONS`）。這是 AI 寫的內容，要改 prompt。
5. **（Low）重試會重複花錢**：AI 回答之後才發生的失敗（例如 audit），job 重試時會重新呼叫 AI。

**花費**（2026-10-27／28 的 live，依 worker 的 log）：文字提案 3 次約 USD 0.13、照片 judge 與 SAM 約 USD 0.75（含 audit 錯誤造成的重試）。都在 organization 每日 USD 5 的上限內。精確數字以資料庫的 spend ledger 為準。

### Worker 的 region 寫進 config（2026-10-28）

`deploy/railway/worker.railway.json` 的 `deploy` 加上 `multiRegionConfig: {"asia-southeast1-eqsg3a": {"numReplicas": 1}, "us-west2": null}`。這和 owner 選擇後手動送出的 patch 相同。Deployer 會把 config 檔的 `deploy` 設定（`numReplicas` 除外）一起送出，所以之後重跑 `--apply` 時 worker 會留在 Singapore。

`check_deploy.py`、rehearsal（PASS）、`make spec-check` 都是 exit 0（`checks/evidence/2026-10-28-worker-region/`）。

Rehearsal 的替身不模擬 region，所以這個改變**沒有 red**。

這只處理 region：Meta 對部分出口回 404 的問題仍然存在（見上一節）。
