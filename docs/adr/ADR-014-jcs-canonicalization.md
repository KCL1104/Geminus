# ADR-014 — RFC 8785 canonicalization library and hash verification

狀態：PROPOSED（T-005 agent 選定，待人類 review）。日期：2026-10-15。前提：D-006、D-019、D-034（皆已批准）。

## Decision

`internal/model/hashing` 以 `github.com/gowebpki/jcs` v1.0.2（Apache-2.0，2026-09-21 發布）做 RFC 8785 canonicalization。它是 RFC 作者 Anders Rundgren（cyberphone）參考實作的 Go fork，經原作者同意維護；API 為 `jcs.Transform([]byte) ([]byte, error)`。`hashing.Canonical` 在其前面：

- 以 `encoding/json/jsontext` 獨立讀一遍：拒 duplicate member（含 escape 後相同者）、不合法 UTF-8、多個 top-level value。jcs 本身也拒絕這些（1.6M 次 differential fuzz 未找到 jsontext 拒絕而 jcs 接受的輸入）；第二個 parser 讓規則不只倚賴一個實作，並提供錯誤種類與 byte offset。
- 錯誤訊息只帶 byte offset，不轉傳 jsontext／jcs 會引用輸入 token 的訊息。
- 數字依 RFC 8785 為 binary64：只在 binary64 精度外不同的文件同 hash；精確整數欄位由 schema 限制在 2^53−1。曾考慮拒絕超過 2^53−1 的整數 literal（Python `rfc8785` 以型別這樣做），但 fuzz 證實它使 Canonical 拒絕自身輸出（`1e17` → `100000000000000000`），也會讓 Go 重新 marshal 的 patch candidate 無法計算 effective hash，故不採用。對這類輸入 Go 接受、Python 拒絕；兩者不會對同一輸入給出不同 hash。

## 為何可信：三組獨立 oracle

| Oracle | 內容 | 位置 |
|---|---|---|
| RFC 8785 原文 | §3.2.2–§3.2.4 範例（逐 byte）、§3.2.3 UTF-16 排序、附錄 B 全部 26 個數字（含 NaN／Infinity 須報錯） | `testdata/hashing/rfc8785-vectors.json`，由 RFC 文字機械擷取 |
| V8 | 6,000 個 IEEE-754 值（邊界、10 的冪、fixed／exponent 切換點、亂數 bit pattern）的 `JSON.stringify` 結果；RFC §3.2.2.3 指定 V8 為 reference | `testdata/hashing/v8-number-vectors.txt`，`scripts/gen_number_vectors.mjs` 可重現（Node 24.21.0） |
| Python `rfc8785` 0.1.4（Trail of Bits，Apache-2.0） | 所有 golden model、patch、effective model、config（獨立以 `Decimal` 實作 quantizer）、manifest、result digest、content hash；另有 400 份隨機文件（含 UTF-16 與 code point 排序不同的 astral／私用區 member name、escape、控制字元）逐 byte 比對 | `scripts/check_hashes.py` → `testdata/hashing/expected-hashes.json`、`rfc8785-documents.jsonl`；`make spec-hashes` 在 CI 重算 |

Go 測試與 Python 檢查讀同一份 expected 檔，任一實作改變行為 CI 即失敗。擴充執行（不入 repo）：`HASHING_V8_VECTORS` 指向 `gen_number_vectors.mjs 1000000` 的輸出、`HASHING_PY_DOCUMENTS` 指向 `check_hashes.py --documents 100000 PATH` 的輸出；T-005 已以 1,000,000 個數字與 100,000 份文件執行並全數一致（checks/T-005_VALIDATION.md）。

## Consequences

- 新直接依賴 `github.com/gowebpki/jcs`；其 `go.mod` 只帶 test 依賴（testify、yaml），不編進 binary。CI 的 govulncheck 涵蓋它。
- 只保證已測平台（目前 linux/amd64）的 digest（D-006）；跨架構 byte-identical 未驗證不得宣稱。
- 換 JCS 實作或 profile 會改變既有 hash：必須先讓三組 oracle 全部通過，且視為 hash 版本變更處理。
