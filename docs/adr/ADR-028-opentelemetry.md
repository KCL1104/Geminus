# ADR-028 — OpenTelemetry traces and metrics (T-035)

Status: **ACCEPTED** (2026-10-26, owner H-024: 「第五、六頁都照建議」 ("pages five and six both as recommended"), D-095 (b) takes A). Proposed and implemented by the T-035 agent on 2026-10-26; independent review round 2 ACCEPT. Basis: REQ-020 (structured logs + traces/metrics; no customer content in product telemetry), the topology in ARCHITECTURE §1 (Traces/Metrics, OTLP backend) and §8, D-093–D-095 (APPROVED, H-024), ADR-011's two-week rule and license column, AGENTS.md §3 (no floating `latest`), §7 (stop line). This ADR does not choose any cloud, vendor or backend, and does not decide how long telemetry is kept (retention/residency is still TBD).

## Context

The original report's observability choice is OpenTelemetry; the deployment units in ARCHITECTURE §8 include an OTLP backend, but cloud/provider/retention are all TBD. T-035's requirements: export is off by default (no data is sent unless an operator explicitly sets an OTLP endpoint), no vendor is chosen, metric and span attributes are low-cardinality and contain no customer content and no ID that may not go into a log, and the generic kernel does not depend on OTel.

## Decision

### Dependencies

Pinned exactly with `go get` in `go.mod`/`go.sum` (`go mod verify`: all modules verified). The versions are OpenTelemetry Go v1.47.0 (released 2026-10-02) and the versions its `go.mod` requires; every version was released before 2026-10-11 (two full weeks; release times taken from proxy.golang.org's `.info`). Licenses are taken from each module's own LICENSE file (`checks/evidence/2026-10-25-T-035/licenses.txt`); no copyleft.

| Module | Version (release date) | License | Why it is linked |
|---|---|---|---|
| `go.opentelemetry.io/otel`, `/trace`, `/metric` | v1.47.0 (2026-10-02) | Apache-2.0 (the LICENSE also carries BSD-3-Clause, for code taken from Go, Copyright 2009 The Go Authors) | API (attribute, codes, trace, metric, no-op) |
| `go.opentelemetry.io/otel/sdk`, `/sdk/metric` | v1.47.0 (2026-10-02) | Same as above | SDK: batch span processor, periodic metric reader, resource |
| `go.opentelemetry.io/otel/exporters/otlp/otlptrace`, `/otlptrace/otlptracehttp`, `/otlpmetric/otlpmetrichttp` | v1.47.0 (2026-10-02) | Same as above | OTLP/HTTP exporter (no gRPC exporter) |
| `go.opentelemetry.io/otel/log` | v1.47.0 (2026-10-02) | Same as above | A dependency of the SDK (the log signal is not used) |
| `go.opentelemetry.io/auto/sdk` | v1.2.1 (2025-09-15) | Apache-2.0 | A dependency of the trace API |
| `go.opentelemetry.io/proto/otlp` | v1.11.0 (2026-07-22) | Apache-2.0 | OTLP's protobuf messages |
| `google.golang.org/protobuf` | v1.36.12 (2026-08-10) | BSD-3-Clause | protobuf encoding |
| `google.golang.org/grpc` | v1.83.2 (2026-08-25) | Apache-2.0 (with a NOTICE.txt) | The collector package of `proto/otlp` also carries gRPC stubs; status types |
| `google.golang.org/genproto/googleapis/api`, `/rpc` | v0.0.0-20260928230214-8a89bd6388cc (2026-09-28) | Apache-2.0 | A dependency of gRPC and grpc-gateway |
| `github.com/grpc-ecosystem/grpc-gateway/v2` | v2.30.0 (2026-08-05) | BSD-3-Clause | A dependency of `proto/otlp` |
| `github.com/cenkalti/backoff/v5` | v5.0.3 (2025-07-23) | MIT | The exporter's retries |
| `github.com/cespare/xxhash/v2` | v2.3.0 (2024-04-04) | MIT | Hash of attribute sets |
| `github.com/go-logr/logr` | v1.4.4 (2026-07-20; previously an indirect dependency at v1.4.2) | Apache-2.0 | OTel's internal logger; `export` connects it to the filtered log with `logr.FromSlogHandler` |
| `github.com/go-logr/stdr` | v1.2.2 (2021-12-14) | Apache-2.0 | OTel's default logger |
| `github.com/google/uuid` | v1.6.0 (2024-01-23) | BSD-3-Clause | The SDK's resource |
| `golang.org/x/net` | **v0.60.0** (2026-10-08) | BSD-3-Clause | A dependency of gRPC. The v0.59.0 that OTel v1.47.0 requires has GO-2026-6603, GO-2026-6610, GO-2026-6611, GO-2026-6612, GO-2026-6617 (the `govulncheck@v1.9.0` pinned in CI fails); v0.60.0 fixes them and is two full weeks old |
| `golang.org/x/sys` | v0.48.0 (2026-08-31) | BSD-3-Clause | A dependency of x/net and gRPC |

`golang.org/x/text` v0.42.0 and `golang.org/x/sync` v0.23.0 were already in `go.mod`. Running `go run golang.org/x/vuln/cmd/govulncheck@v1.9.0 ./cmd/... ./internal/... ./schemas/... ./scripts/...` with the pinned versions (the same as CI): No vulnerabilities found (`govulncheck.log`).

### Packages and boundaries (`scripts/check-architecture`)

| Package | Contents | Rule |
|---|---|---|
| `internal/observability/telemetry` | Spans and instruments recorded with the OTel **API**; attribute names are a closed list, and values go through `observability.Fits` (the same shape as for logs, D-094 (f)); a value that does not fit is recorded as `[redacted]`; `Install`/`Current`; the default is OTel's no-op. OTel's global providers are not used, so no other library can export through them | `telemetry-uses-the-api-only`: may link only the OTel API (`attribute`, `codes`, `metric/...`, `trace/...`, `semconv/...`) and the kernel's computational third parties; no network, files, DB or process |
| `internal/observability/telemetry/export` | Only when the config has an endpoint: builds the OTLP/HTTP exporters and the SDK's tracer/meter providers, and calls `Install`; the SDK's errors (`otel.SetErrorHandler`) and internal log (`otel.SetLogger`) are connected to the filtered slog **before** any exporter is built (the exporter parses `OTEL_EXPORTER_OTLP_*` when it is built and reports a value it cannot parse together with the value; review M1); the internal log's Error carries its error as `error` (free text, filtered), Warn (logr V(1)) is WARN, Info/Debug are not recorded, messages start with `telemetry: `, and every other key (raw values such as `input`, `value`) is redacted (review L7) | `otel-sdk-only-in-export`: in the whole module, only it and `cmd/...` may link the OTel SDK, exporters, `proto/otlp`, gRPC, protobuf, grpc-gateway |

`internal/observability/...` is the application layer, so the generic kernel, domain packs and `internal/ai` may not depend on telemetry (probes: `the-kernel-records-telemetry`, `the-kernel-uses-the-otel-api`, `the-context-records-telemetry`, `the-provider-uses-the-otel-api`). `github.com/google/uuid` imports `database/sql/driver` to implement `driver.Valuer`/`sql.Scanner` and opens no connection; `db-only-in-storage` exempts only **this one** import of it (the rule's `bring` field, review I8: reaching `database/sql` or any driver through it is still a violation, and so is a package that imports `database/sql/driver` directly, `TestTheUUIDGatewayBringsOnlyTheDriver`).

### Configuration (`config.LoadTelemetry`, D-095 (b))

- There is no default endpoint at all (not even localhost): without configuration no exporter is built, and the process sends no telemetry.
- `OTEL_EXPORTER_OTLP_ENDPOINT` (base; each signal appends `v1/traces`, `v1/metrics`), `OTEL_EXPORTER_OTLP_TRACES_ENDPOINT`, `OTEL_EXPORTER_OTLP_METRICS_ENDPOINT` (full URL, takes precedence over the base); an endpoint must be an absolute **https** URL, or an http URL whose host is local (`localhost`, 127.0.0.0/8, `::1`) (review L6: the collector's credential never crosses the network in clear text), with a host and no user information, query (including a bare `?`) or fragment.
- The collector's credential goes in `OTEL_EXPORTER_OTLP_HEADERS` (or `_TRACES_HEADERS`, `_METRICS_HEADERS`), which the exporter reads itself; `config.LoadTelemetry` checks it with the exporter's rules (comma-separated `name=value`, name an HTTP token, value URL-decodable), refuses to start when it does not match, and the error names only the variable (review M1). So the exporter never reports a header's value because it cannot parse it; even if the config is bypassed, the report goes only through the filtered log.
- `OTEL_SDK_DISABLED=true` turns everything off; `OTEL_TRACES_EXPORTER`/`OTEL_METRICS_EXPORTER` accept only `otlp` (or unset) and `none`; `OTEL_EXPORTER_OTLP_PROTOCOL` accepts only `http/protobuf`. Errors name only the variable, never the value.
- The resource has only `service.name` (`geminus-api`/`geminus-worker`), plus other attributes the operator sets with `OTEL_RESOURCE_ATTRIBUTES` (merged by the SDK; `service.name` is always the name above, and `OTEL_SERVICE_NAME` does not override it, `TestExportWhenConfigured`); no host or process detector is added. Sampling uses the SDK's default (ParentBased(AlwaysOn), which the operator can change with `OTEL_TRACES_SAMPLER`); the exporter's timeout, compression and headers are read by the SDK from `OTEL_EXPORTER_OTLP_*`.
- `cmd/api` and `cmd/worker` flush and stop the SDK on exit, within 5 seconds.

### What is recorded (D-095 (c))

| Signal | Name | Attribute |
|---|---|---|
| span (server) | Each request, named by its route pattern (`POST /v1/models/{id}/versions`; `unmatched` when nothing matches) | `http.request.method`, `http.route`, `http.response.status_code`, `geminus.request_id`; 5xx is an error. A new root: the client's `traceparent` is not read |
| span (consumer) | `job <type>`, one per job attempt | `geminus.job.type`, `geminus.job_id`, `geminus.job.attempt`, `geminus.request_id` (of the request that created it), `geminus.outcome` (completed/cancelled/requeued/failed/lease_lost/not_recorded), `geminus.code` |
| span (child spans of the job, recorded afterwards with their start time) | `simulation engine`, `ai provider call`, `import profile`/`import validate` | outcome, event count; provider, model, role, code, token counts; stage, outcome, row count |
| histogram (s) | `http.server.request.duration` | method, route, status code |
| histogram (s) | `geminus.job.attempt.duration` | job type, outcome, code |
| histogram (s)/counter | `geminus.simulation.engine.duration`/`geminus.simulation.engine.events` | outcome |
| histogram (s)/counter | `gen_ai.client.operation.duration`/`geminus.ai.tokens` | `gen_ai.system`, `gen_ai.request.model`, `geminus.ai.role` (duration also has code); tokens also have `gen_ai.token.type` |
| histogram (s)/counter | `geminus.import.duration`/`geminus.import.rows` | `geminus.import.stage`, outcome |

Metrics never carry an organization, user, request, job or resource ID; there is no path, query, header, body, name, column name, field value, prompt or answer (tests check span names, attributes, metric attributes and the OTLP payload sent to a local collector with sentinels).

### Not done

- Inbound trace context is not accepted (a trace ID the client sends is the client's data; whether trace context is passed between the BFF and the API is for T-039 to decide).
- No contrib auto-instrumentation (`otelhttp` etc.); no OTel log signal (logs remain JSON lines on stderr, D-093); no gRPC exporter; no Prometheus endpoint.
- No decision on backend, vendor or retention; the staging/production collector belongs to T-039.

## Consequences

- Both binaries additionally link gRPC, protobuf and grpc-gateway (the collector package of `proto/otlp` carries gRPC stubs), even though only the HTTP exporter is used; the binaries grow, and so does govulncheck's scope (x/net was raised to v0.60.0 because of this).
- A deployment without a configured endpoint behaves as before (no-op), so export can only be turned on explicitly by an operator.
- If the owner does not adopt it (D-095 option B): remove `internal/observability/telemetry/...`, `config.LoadTelemetry`, `export.Setup` in `cmd`, the two rules and these dependencies in `go.mod`; the operational metrics in logs (D-095 (a)) are not affected.
- Once adopted, ADR-011's tool table and the Go module section of THIRD_PARTY_NOTICES (added by this task) are the record of these pins; later upgrades still follow the two-week rule and govulncheck.
