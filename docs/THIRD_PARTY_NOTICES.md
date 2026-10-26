# Third-party notices

Source code adapted from third parties, with the notices their licenses require. Dependencies pulled in as modules or packages are listed with their licenses in [ADR-011](adr/ADR-011-toolchain-bootstrap.md) and their lockfiles; this file covers code copied into this repository, the Go modules the live AI adapter links (T-024) and the Go modules OpenTelemetry links (last section, T-035).

## Go `math.log` (pinned logarithm)

`internal/simulation/engine/sample.go` (`ln`) and `scripts/engine_reference.py` (`pinned_log`) implement the algorithm and constants of the pure-Go natural logarithm in the Go standard library (`src/math/log.go`), adapted to round every product explicitly (T-020 review, D-038 PROPOSED). License: BSD-3-Clause.

```text
Copyright 2009 The Go Authors.

Redistribution and use in source and binary forms, with or without
modification, are permitted provided that the following conditions are
met:

   * Redistributions of source code must retain the above copyright
notice, this list of conditions and the following disclaimer.
   * Redistributions in binary form must reproduce the above
copyright notice, this list of conditions and the following disclaimer
in the documentation and/or other materials provided with the
distribution.
   * Neither the name of Google LLC nor the names of its
contributors may be used to endorse or promote products derived from
this software without specific prior written permission.

THIS SOFTWARE IS PROVIDED BY THE COPYRIGHT HOLDERS AND CONTRIBUTORS
"AS IS" AND ANY EXPRESS OR IMPLIED WARRANTIES, INCLUDING, BUT NOT
LIMITED TO, THE IMPLIED WARRANTIES OF MERCHANTABILITY AND FITNESS FOR
A PARTICULAR PURPOSE ARE DISCLAIMED. IN NO EVENT SHALL THE COPYRIGHT
OWNER OR CONTRIBUTORS BE LIABLE FOR ANY DIRECT, INDIRECT, INCIDENTAL,
SPECIAL, EXEMPLARY, OR CONSEQUENTIAL DAMAGES (INCLUDING, BUT NOT
LIMITED TO, PROCUREMENT OF SUBSTITUTE GOODS OR SERVICES; LOSS OF USE,
DATA, OR PROFITS; OR BUSINESS INTERRUPTION) HOWEVER CAUSED AND ON ANY
THEORY OF LIABILITY, WHETHER IN CONTRACT, STRICT LIABILITY, OR TORT
(INCLUDING NEGLIGENCE OR OTHERWISE) ARISING IN ANY WAY OUT OF THE USE
OF THIS SOFTWARE, EVEN IF ADVISED OF THE POSSIBILITY OF SUCH DAMAGE.
```

The Go code is in turn derived from FreeBSD's `/usr/src/lib/msun/src/e_log.c`, which came with this notice:

```text
====================================================
Copyright (C) 1993 by Sun Microsystems, Inc. All rights reserved.

Developed at SunPro, a Sun Microsystems, Inc. business.
Permission to use, copy, modify, and distribute this
software is freely granted, provided that this notice
is preserved.
====================================================
```

## Go `math.exp` (pinned exponential, T-048)

`internal/simulation/engine/sample.go` (`exp`) and `scripts/engine_reference.py` (`pinned_exp`) implement the algorithm and constants of the pure-Go exponential in the Go standard library (`src/math/exp.go`), adapted to round every product explicitly (D-056; D-075 APPROVED，H-022). License: BSD-3-Clause, under the same copyright and notice as the logarithm above (Copyright 2009 The Go Authors).

The Go code is in turn derived from FreeBSD's `/usr/src/lib/msun/src/e_exp.c`, which came with this notice:

```text
====================================================
Copyright (C) 2004 by Sun Microsystems, Inc. All rights reserved.

Permission to use, copy, modify, and distribute this
software is freely granted, provided that this notice
is preserved.
====================================================
```

## Algorithm AS 241 (normal quantile, T-048)

`internal/simulation/engine/sample.go` (`ndtri`) and `scripts/engine_reference.py` implement Wichura's algorithm AS 241, PPND16 (M. J. Wichura, "Algorithm AS 241: The Percentage Points of the Normal Distribution", Applied Statistics 37(3), 1988, 477–484), from its published description and its 48 published coefficients; no code from the journal, StatLib or another implementation was copied. The coefficients are numerical constants of a published method; they are listed with their bit patterns in `testdata/simulation/engine-vectors.json`.

## Go modules linked by the live AI adapter (T-024)

T-024 adds the official Anthropic Go SDK for the live provider adapter (`internal/ai/provider/anthropic`, ADR-027 ACCEPTED (H-019); ADR-011 records the pin). Only that package links it (architecture rule `llm-sdks-only-in-ai`). Versions are pinned in `go.mod` and `go.sum`; each module's own license file ships in its module and applies to it.

| Module | Version | License | Linked because |
|---|---|---|---|
| `github.com/anthropics/anthropic-sdk-go` | v1.78.0 (published 2026-09-30) | MIT, Copyright 2023 Anthropic, PBC | the SDK |
| `github.com/tidwall/gjson` | v1.18.0 | MIT | SDK JSON handling |
| `github.com/tidwall/sjson` | v1.2.5 | MIT | SDK JSON handling |
| `github.com/tidwall/match` | v1.1.1 | MIT | gjson |
| `github.com/tidwall/pretty` | v1.2.1 | MIT | gjson |
| `github.com/buger/jsonparser` | v1.1.2 | MIT | SDK dependency |
| `github.com/invopop/jsonschema` | v0.14.0 | MIT | SDK (schemas from Go types; not used by the adapter) |
| `github.com/bahlo/generic-list-go` | v0.2.0 | BSD-3-Clause | invopop/jsonschema |
| `github.com/pb33f/ordered-map/v2` | v2.3.1 | Apache-2.0 | the SDK and invopop/jsonschema (ordered maps) |
| `github.com/standard-webhooks/standard-webhooks/libraries` | v0.0.1 | MIT | SDK (webhook verification; not used by the adapter) |
| `go.yaml.in/yaml/v4` | v4.0.0-rc.2 (published 2025-08-28) | MIT and Apache-2.0 (its NOTICE: Copyright 2011-2016 Canonical Ltd.) | pb33f/ordered-map/v2, whose `yaml.go` gives ordered maps YAML (un)marshalling; nothing in this module or the adapter's path marshals YAML (`go mod why -m go.yaml.in/yaml/v4`). A release candidate, accepted as a transitive pre-release in ADR-011 |
| `golang.org/x/sync` | v0.23.0 (already required) | BSD-3-Clause | SDK (`errgroup`) |

The MIT notice of the SDK, as its `LICENSE` file states it:

```text
Copyright 2023 Anthropic, PBC.

Permission is hereby granted, free of charge, to any person obtaining a copy of this software and associated documentation files (the "Software"), to deal in the Software without restriction, including without limitation the rights to use, copy, modify, merge, publish, distribute, sublicense, and/or sell copies of the Software, and to permit persons to whom the Software is furnished to do so, subject to the following conditions:

The above copyright notice and this permission notice shall be included in all copies or substantial portions of the Software.

THE SOFTWARE IS PROVIDED "AS IS", WITHOUT WARRANTY OF ANY KIND, EXPRESS OR IMPLIED, INCLUDING BUT NOT LIMITED TO THE WARRANTIES OF MERCHANTABILITY, FITNESS FOR A PARTICULAR PURPOSE AND NONINFRINGEMENT. IN NO EVENT SHALL THE AUTHORS OR COPYRIGHT HOLDERS BE LIABLE FOR ANY CLAIM, DAMAGES OR OTHER LIABILITY, WHETHER IN AN ACTION OF CONTRACT, TORT OR OTHERWISE, ARISING FROM, OUT OF OR IN CONNECTION WITH THE SOFTWARE OR THE USE OR OTHER DEALINGS IN THE SOFTWARE.
```

## SimPy (independent simulation reference, T-047, T-048)

`scripts/simpy_reference.py` uses SimPy 4.1.2 (`requirements-spec.txt`, ADR-011) as an independent reference for `make simulation-reference-test` (finite buffers since T-047, breakdowns since T-048; no new dependency); it is a test dependency only, installed with the spec checks, never imported or called by the Go engine, and not part of any build. No SimPy code is copied into this repository. Its license, as the wheel's `LICENSE.rst` states it:

```text
The MIT License (MIT)

Copyright (c) 2013 Ontje Lünsdorf and Stefan Scherfke (also see AUTHORS.txt)

Permission is hereby granted, free of charge, to any person obtaining a copy of
this software and associated documentation files (the "Software"), to deal in
the Software without restriction, including without limitation the rights to
use, copy, modify, merge, publish, distribute, sublicense, and/or sell copies of
the Software, and to permit persons to whom the Software is furnished to do so,
subject to the following conditions:

The above copyright notice and this permission notice shall be included in all
copies or substantial portions of the Software.

THE SOFTWARE IS PROVIDED "AS IS", WITHOUT WARRANTY OF ANY KIND, EXPRESS OR
IMPLIED, INCLUDING BUT NOT LIMITED TO THE WARRANTIES OF MERCHANTABILITY, FITNESS
FOR A PARTICULAR PURPOSE AND NONINFRINGEMENT. IN NO EVENT SHALL THE AUTHORS OR
COPYRIGHT HOLDERS BE LIABLE FOR ANY CLAIM, DAMAGES OR OTHER LIABILITY, WHETHER
IN AN ACTION OF CONTRACT, TORT OR OTHERWISE, ARISING FROM, OUT OF OR IN
CONNECTION WITH THE SOFTWARE OR THE USE OR OTHER DEALINGS IN THE SOFTWARE.
```

## Go modules linked by OpenTelemetry (T-035)

T-035 adds the OpenTelemetry Go API, SDK and OTLP/HTTP exporters for traces and metrics ([ADR-028](adr/ADR-028-opentelemetry.md), **ACCEPTED**, H-024; export is off unless an operator configures an OTLP endpoint, D-095). Only `internal/observability/telemetry/export` and `cmd/...` link the SDK, the exporters, gRPC and protobuf (architecture rule `otel-sdk-only-in-export`); `internal/observability/telemetry` uses the API only. Versions are pinned in `go.mod` and `go.sum`, each published at least two weeks before 2026-10-25 (ADR-028 lists the dates); each module's own license file (and gRPC's `NOTICE.txt`) ships in its module and applies to it. No module is copyleft.

| Module | Version | License | Linked because |
|---|---|---|---|
| `go.opentelemetry.io/otel`, `/trace`, `/metric`, `/log`, `/sdk`, `/sdk/metric`, `/exporters/otlp/otlptrace`, `/exporters/otlp/otlptrace/otlptracehttp`, `/exporters/otlp/otlpmetric/otlpmetrichttp` | v1.47.0 | Apache-2.0; the same LICENSE file adds BSD-3-Clause (Copyright 2009 The Go Authors) for code derived from Go | the API, the SDK and the OTLP/HTTP exporters |
| `go.opentelemetry.io/auto/sdk` | v1.2.1 | Apache-2.0 | the trace API |
| `go.opentelemetry.io/proto/otlp` | v1.11.0 | Apache-2.0 | the OTLP protobuf messages |
| `google.golang.org/protobuf` | v1.36.12 | BSD-3-Clause, Copyright (c) 2018 The Go Authors | protobuf encoding |
| `google.golang.org/grpc` | v1.83.2 | Apache-2.0 (with `NOTICE.txt`), Copyright 2014 gRPC authors | `proto/otlp`'s collector packages carry the gRPC stubs; status types |
| `google.golang.org/genproto/googleapis/api`, `/rpc` | v0.0.0-20260928230214-8a89bd6388cc | Apache-2.0 | gRPC and grpc-gateway |
| `github.com/grpc-ecosystem/grpc-gateway/v2` | v2.30.0 | BSD-3-Clause, Copyright (c) 2015, Gengo, Inc. | `proto/otlp` |
| `github.com/cenkalti/backoff/v5` | v5.0.3 | MIT, Copyright (c) 2014 Cenk Altı | the exporters' retries |
| `github.com/cespare/xxhash/v2` | v2.3.0 | MIT, Copyright (c) 2016 Caleb Spare | attribute set hashing |
| `github.com/go-logr/logr` | v1.4.4 | Apache-2.0 | OpenTelemetry's internal logger, routed to the filtered log |
| `github.com/go-logr/stdr` | v1.2.2 | Apache-2.0 | OpenTelemetry's default logger |
| `github.com/google/uuid` | v1.6.0 | BSD-3-Clause, Copyright (c) 2009,2014 Google Inc. | the SDK's resource |
| `golang.org/x/net` | v0.60.0 | BSD-3-Clause, Copyright 2009 The Go Authors | gRPC (v0.60.0, not the v0.59.0 OpenTelemetry asks for, which `govulncheck` flags) |
| `golang.org/x/sys` | v0.48.0 | BSD-3-Clause, Copyright 2009 The Go Authors | x/net and gRPC |
