# ADR-028 — OpenTelemetry traces 與 metrics（T-035）

狀態：**ACCEPTED**（2026-10-26，owner H-024：「第五、六頁都照建議」，D-095 (b) 採 A）。2026-10-26 由 T-035 agent 提出並實作，獨立 review 第 2 輪 ACCEPT。依據：REQ-020（structured logs + traces/metrics；no customer content in product telemetry）、ARCHITECTURE §1 的 topology（Traces／Metrics、OTLP backend）與 §8、D-093–D-095（APPROVED，H-024）、ADR-011 的兩週規則與授權欄、AGENTS.md §3（不使用 floating `latest`）、§7（停止線）。本 ADR 不選任何雲端、廠商或 backend，也不決定 telemetry 的保存期限（retention／residency 仍為 TBD）。

## Context

原報告的 observability 選擇是 OpenTelemetry；ARCHITECTURE §8 的部署單位有 OTLP backend，但 cloud／provider／retention 都是 TBD。T-035 的要求：export 預設關閉（沒有 operator 明確設定 OTLP endpoint 就不送出任何資料）、不選廠商、metric 與 span 的 attribute 低基數且不含客戶內容或不能進 log 的 ID、generic kernel 不依賴 OTel。

## Decision

### 依賴

以 `go get` 精確 pin 在 `go.mod`／`go.sum`（`go mod verify`：all modules verified）。版本為 OpenTelemetry Go v1.47.0（2026-10-02 發布）與它的 `go.mod` 要求的版本；每個版本都在 2026-10-11 之前發布（滿兩週，發布時間取自 proxy.golang.org 的 `.info`）。授權取自各 module 自己的 LICENSE 檔（`checks/evidence/2026-10-25-T-035/licenses.txt`）；沒有 copyleft。

| Module | 版本（發布日） | License | 為何連結 |
|---|---|---|---|
| `go.opentelemetry.io/otel`、`/trace`、`/metric` | v1.47.0（2026-10-02） | Apache-2.0（LICENSE 另附 BSD-3-Clause，給取自 Go 的程式碼，Copyright 2009 The Go Authors） | API（attribute、codes、trace、metric、no-op） |
| `go.opentelemetry.io/otel/sdk`、`/sdk/metric` | v1.47.0（2026-10-02） | 同上 | SDK：batch span processor、periodic metric reader、resource |
| `go.opentelemetry.io/otel/exporters/otlp/otlptrace`、`/otlptrace/otlptracehttp`、`/otlpmetric/otlpmetrichttp` | v1.47.0（2026-10-02） | 同上 | OTLP／HTTP exporter（不含 gRPC exporter） |
| `go.opentelemetry.io/otel/log` | v1.47.0（2026-10-02） | 同上 | SDK 的依賴（不使用 log signal） |
| `go.opentelemetry.io/auto/sdk` | v1.2.1（2025-09-15） | Apache-2.0 | trace API 的依賴 |
| `go.opentelemetry.io/proto/otlp` | v1.11.0（2026-07-22） | Apache-2.0 | OTLP 的 protobuf 訊息 |
| `google.golang.org/protobuf` | v1.36.12（2026-08-10） | BSD-3-Clause | protobuf 編碼 |
| `google.golang.org/grpc` | v1.83.2（2026-08-25） | Apache-2.0（另有 NOTICE.txt） | `proto/otlp` 的 collector package 同時帶 gRPC stub；status 型別 |
| `google.golang.org/genproto/googleapis/api`、`/rpc` | v0.0.0-20260928230214-8a89bd6388cc（2026-09-28） | Apache-2.0 | gRPC 與 grpc-gateway 的依賴 |
| `github.com/grpc-ecosystem/grpc-gateway/v2` | v2.30.0（2026-08-05） | BSD-3-Clause | `proto/otlp` 的依賴 |
| `github.com/cenkalti/backoff/v5` | v5.0.3（2025-07-23） | MIT | exporter 的重試 |
| `github.com/cespare/xxhash/v2` | v2.3.0（2024-04-04） | MIT | attribute set 的 hash |
| `github.com/go-logr/logr` | v1.4.4（2026-07-20，原本是 v1.4.2 的間接依賴） | Apache-2.0 | OTel 的內部 logger；`export` 以 `logr.FromSlogHandler` 把它接到過濾後的 log |
| `github.com/go-logr/stdr` | v1.2.2（2021-12-14） | Apache-2.0 | OTel 預設的 logger |
| `github.com/google/uuid` | v1.6.0（2024-01-23） | BSD-3-Clause | SDK 的 resource |
| `golang.org/x/net` | **v0.60.0**（2026-10-08） | BSD-3-Clause | gRPC 的依賴。OTel v1.47.0 要求的 v0.59.0 有 GO-2026-6603、GO-2026-6610、GO-2026-6611、GO-2026-6612、GO-2026-6617（CI 釘住的 `govulncheck@v1.9.0` 會失敗），v0.60.0 修正並滿兩週 |
| `golang.org/x/sys` | v0.48.0（2026-08-31） | BSD-3-Clause | x/net、gRPC 的依賴 |

`golang.org/x/text` v0.42.0 與 `golang.org/x/sync` v0.23.0 本來就在 `go.mod`。以 pin 版本執行 `go run golang.org/x/vuln/cmd/govulncheck@v1.9.0 ./cmd/... ./internal/... ./schemas/... ./scripts/...`（與 CI 相同）：No vulnerabilities found（`govulncheck.log`）。

### Package 與邊界（`scripts/check-architecture`）

| Package | 內容 | 規則 |
|---|---|---|
| `internal/observability/telemetry` | 以 OTel **API** 記錄的 span 與 instrument；attribute 名稱是封閉清單，值經 `observability.Fits`（與 log 相同的形狀，D-094 (f)），不合的記為 `[redacted]`；`Install`／`Current`；預設是 OTel 的 no-op。不使用 OTel 的 global provider，所以沒有其他 library 能經由它們 export | `telemetry-uses-the-api-only`：只能連結 OTel API（`attribute`、`codes`、`metric/...`、`trace/...`、`semconv/...`）與 kernel 的計算類第三方，沒有網路、檔案、DB、process |
| `internal/observability/telemetry/export` | 只在 config 有 endpoint 時建立 OTLP／HTTP exporter、SDK 的 tracer／meter provider，並 `Install`；SDK 的錯誤（`otel.SetErrorHandler`）與內部 log（`otel.SetLogger`）在建立任何 exporter **之前**就接到過濾後的 slog（exporter 建立時就解析 `OTEL_EXPORTER_OTLP_*`，解析不了的值會連同值一起報告；review M1）；內部 log 的 Error 以 `error`（自由文字，經過濾）帶它的錯誤，Warn（logr V(1)）為 WARN，Info／Debug 不記，訊息以 `telemetry: ` 開頭，其他 key（`input`、`value` 等原始值）一律 redacted（review L7） | `otel-sdk-only-in-export`：整個 module 只有它與 `cmd/...` 能連結 OTel SDK、exporter、`proto/otlp`、gRPC、protobuf、grpc-gateway |

`internal/observability/...` 是 application layer，所以 generic kernel、domain pack 與 `internal/ai` 都不能依賴 telemetry（probe：`the-kernel-records-telemetry`、`the-kernel-uses-the-otel-api`、`the-context-records-telemetry`、`the-provider-uses-the-otel-api`）。`github.com/google/uuid` 為了實作 `driver.Valuer`／`sql.Scanner` 而 import `database/sql/driver`，不開任何連線；`db-only-in-storage` 只豁免它的**這一個** import（規則的 `bring` 欄位，review I8：經由它連到 `database/sql` 或任何 driver 仍是 violation，直接 import `database/sql/driver` 的 package 也是，`TestTheUUIDGatewayBringsOnlyTheDriver`）。

### 設定（`config.LoadTelemetry`，D-095 (b)）

- 沒有任何預設 endpoint（也不連 localhost）：沒有設定時不建立 exporter，process 不送出任何 telemetry。
- `OTEL_EXPORTER_OTLP_ENDPOINT`（base，各 signal 加上 `v1/traces`、`v1/metrics`）、`OTEL_EXPORTER_OTLP_TRACES_ENDPOINT`、`OTEL_EXPORTER_OTLP_METRICS_ENDPOINT`（完整 URL，優先於 base）；endpoint 必須是絕對的 **https** URL，或 host 為本機（`localhost`、127.0.0.0/8、`::1`）的 http URL（review L6：collector 的 credential 不以明文經過網路），有 host，沒有 user information、query（含只有 `?`）、fragment。
- Collector 的 credential 放 `OTEL_EXPORTER_OTLP_HEADERS`（或 `_TRACES_HEADERS`、`_METRICS_HEADERS`），由 exporter 自己讀；`config.LoadTelemetry` 以 exporter 相同的規則檢查（逗號分隔的 `name=value`，name 為 HTTP token、value 可 URL-decode），不合時拒絕啟動、錯誤只寫變數名稱（review M1）。所以 exporter 不會因為解析不了而報告 header 的值；即使繞過 config，報告也只經過濾後的 log。
- `OTEL_SDK_DISABLED=true` 全部關閉；`OTEL_TRACES_EXPORTER`／`OTEL_METRICS_EXPORTER` 只接受 `otlp`（或未設）與 `none`；`OTEL_EXPORTER_OTLP_PROTOCOL` 只接受 `http/protobuf`。錯誤只寫變數名稱，不寫值。
- Resource 只有 `service.name`（`geminus-api`／`geminus-worker`），加上 operator 以 `OTEL_RESOURCE_ATTRIBUTES` 設定的其他 attribute（SDK 合併；`service.name` 一律是上述名稱，`OTEL_SERVICE_NAME` 不覆蓋它，`TestExportWhenConfigured`）；不加 host 或 process detector。取樣用 SDK 的預設（ParentBased(AlwaysOn)，operator 可用 `OTEL_TRACES_SAMPLER` 改）；exporter 的 timeout、壓縮、header 照 SDK 讀 `OTEL_EXPORTER_OTLP_*`。
- `cmd/api`、`cmd/worker` 結束時以 5 秒為限 flush 並停止 SDK。

### 記錄的內容（D-095 (c)）

| Signal | 名稱 | Attribute |
|---|---|---|
| span（server） | 每個 request，名稱為 route pattern（`POST /v1/models/{id}/versions`，沒有對到為 `unmatched`） | `http.request.method`、`http.route`、`http.response.status_code`、`geminus.request_id`；5xx 為 error。新的 root：不讀 client 的 `traceparent` |
| span（consumer） | `job <type>`，每個 job attempt | `geminus.job.type`、`geminus.job_id`、`geminus.job.attempt`、`geminus.request_id`（建立它的 request）、`geminus.outcome`（completed／cancelled／requeued／failed／lease_lost／not_recorded）、`geminus.code` |
| span（job 的子 span，事後以開始時間記錄） | `simulation engine`、`ai provider call`、`import profile`／`import validate` | outcome、事件數；provider、model、role、code、token 數；stage、outcome、列數 |
| histogram（s） | `http.server.request.duration` | method、route、status code |
| histogram（s） | `geminus.job.attempt.duration` | job type、outcome、code |
| histogram（s）／counter | `geminus.simulation.engine.duration`／`geminus.simulation.engine.events` | outcome |
| histogram（s）／counter | `gen_ai.client.operation.duration`／`geminus.ai.tokens` | `gen_ai.system`、`gen_ai.request.model`、`geminus.ai.role`（duration 另有 code）；tokens 另有 `gen_ai.token.type` |
| histogram（s）／counter | `geminus.import.duration`／`geminus.import.rows` | `geminus.import.stage`、outcome |

Metric 從不帶 organization、user、request、job 或資源的 ID；沒有 path、query、header、body、名稱、欄名、欄位值、prompt 或回答（測試以 sentinel 檢查 span 名稱、attribute、metric attribute 與送到本機 collector 的 OTLP payload）。

### 不做

- 不接受 inbound trace context（client 送來的 trace ID 是 client 的資料；BFF 與 API 之間是否傳遞 trace context 待 T-039 決定）。
- 不用 contrib 的自動 instrumentation（`otelhttp` 等）；不加 OTel 的 log signal（log 仍是 stderr 的 JSON lines，D-093）；不加 gRPC exporter；不加 Prometheus endpoint。
- 不決定 backend、廠商、retention；staging／production 的 collector 屬 T-039。

## Consequences

- 兩個 binary 多連結 gRPC、protobuf 與 grpc-gateway（`proto/otlp` 的 collector package 帶 gRPC stub），即使只用 HTTP exporter；binary 變大，govulncheck 的範圍也變大（x/net 已因此升到 v0.60.0）。
- 沒有設定 endpoint 的 deployment 行為不變（no-op），所以 export 只能由 operator 明確開啟。
- 若 owner 不採用（D-095 選項 B）：移除 `internal/observability/telemetry/...`、`config.LoadTelemetry`、`cmd` 的 `export.Setup`、兩條規則與 `go.mod` 的這些依賴；log 的營運指標（D-095 (a)）不受影響。
- 採用後，ADR-011 的工具表與 THIRD_PARTY_NOTICES 的 Go module 一節（本任務已加）是這些 pin 的紀錄；之後升級仍依兩週規則與 govulncheck。
