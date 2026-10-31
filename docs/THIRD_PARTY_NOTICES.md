# Third-party notices

Source code adapted from third parties, with the notices their licenses require. Dependencies pulled in as modules or packages are listed with their licenses in [ADR-011](adr/ADR-011-toolchain-bootstrap.md) and their lockfiles; this file covers code copied into this repository, the Go modules the live AI adapter links (T-024), the Go modules OpenTelemetry links (T-035), the Go modules Eino brings (H-058, H-059; ADR-031), the fonts the web app serves (T-045), the browser packages of the 2D editor's canvas (T-015), the SigV4 test vectors and the container base images (last two sections, T-039).

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

## Go modules brought by Eino (H-058, H-059; slice E0)

E0 pins the Eino framework for the AI role graphs ([ADR-031](adr/ADR-031-eino.md), **PROPOSED**). No package imports it yet; when one does, only `internal/aigraph/...` may (architecture rule `eino-fenced`, narrowed by H-061, approved by the owner in H-063), and only the worker and the live eval link it: never the API (`no-eino-in-the-api`), the kernel, a domain pack or the HTTP transport (`no-eino-in-kernel-packs-transport`), and eino-ext is not used (`no-eino-ext`). Versions are pinned in `go.mod` and `go.sum`, each published at least two weeks before 2026-10-31 (ADR-031 lists the dates); each module's own license file (and yaml.v3's `NOTICE`) ships in its module and applies to it. No module is copyleft. No code from these modules is copied into this repository.

| Module | Version | License | Linked because |
|---|---|---|---|
| `github.com/cloudwego/eino` | v0.9.21 | Apache-2.0 (`LICENSE-APACHE`), CloudWeGo Authors | the framework |
| `github.com/bytedance/sonic` | v1.15.0 | Apache-2.0, Copyright ByteDance Inc.; its `licenses/` directory adds the notices of the code it derives from: Boost Software License 1.0 (Drachennest; eisel_lemire, Copyright (c) Daniel Lemire), BSD-3-Clause (Go and golang-asm, Copyright (c) 2009 The Go Authors), Apache-2.0 (simdjson), MIT (yyjson, Copyright (c) 2020 YaoYuan) | Eino's JSON; the compat path (`encoding/json`) only, rule `json-without-jit` |
| `github.com/bytedance/gopkg` | v0.1.3 | Apache-2.0 | sonic |
| `github.com/eino-contrib/jsonschema` | v1.0.3 | MIT, Copyright (C) 2014 Alec Thomas | tool parameter schemas |
| `github.com/nikolalohinski/gonja` | v1.5.3 | MIT, Copyright (c) 2013-2014 Florian Schlachter | Eino's Jinja2 message templates (not used by the design) |
| `github.com/dustin/go-humanize` | v1.0.1 | MIT, Copyright (c) 2005-2008 Dustin Sallings | gonja |
| `github.com/goph/emperror` | v0.17.2 | MIT, Copyright (c) 2017 Márk Sági-Kazár | gonja |
| `github.com/json-iterator/go` | v1.1.12 | MIT, Copyright (c) 2016 json-iterator | gonja |
| `github.com/modern-go/concurrent` | v0.0.0-20180306012644-bacd9c7ef1dd | Apache-2.0 | json-iterator |
| `github.com/modern-go/reflect2` | v1.0.2 | Apache-2.0 | json-iterator |
| `github.com/mailru/easyjson` | v0.7.7 | MIT, Copyright (c) 2016 Mail.Ru Group | ordered maps |
| `github.com/pelletier/go-toml/v2` | v2.0.9 | MIT, Copyright (c) 2013 - 2022 Thomas Pelletier, Eric Anderton | gonja |
| `github.com/pkg/errors` | v0.9.1 | BSD-2-Clause, Copyright (c) 2015, Dave Cheney | gonja, emperror |
| `github.com/sirupsen/logrus` | v1.9.3 | MIT, Copyright (c) 2014 Simon Eskildsen | gonja |
| `github.com/slongfield/pyfmt` | v0.0.0-20220222012616-ea85ff4c361f | BSD-3-Clause, Copyright (c) 2018, Stephen Longfield, Jr. | Eino's Python-style message templates |
| `github.com/wk8/go-ordered-map/v2` | v2.1.8 | Apache-2.0 | Eino's `schema` and jsonschema |
| `github.com/yargevad/filepathx` | v1.0.0 | MIT, Copyright 2016 The filepathx Authors | gonja |
| `golang.org/x/exp` | v0.0.0-20230713183714-613f0c0eb8a1 | BSD-3-Clause, Copyright (c) 2009 The Go Authors | gonja |
| `gopkg.in/yaml.v3` | v3.0.1 | MIT and Apache-2.0 (its `NOTICE`: Copyright 2011-2016 Canonical Ltd.) | ordered maps, gonja |
| `github.com/bahlo/generic-list-go`, `github.com/buger/jsonparser`, `github.com/google/uuid`, `golang.org/x/sys` | v0.2.0, v1.1.2, v1.6.0, v0.48.0 (already required) | BSD-3-Clause, MIT, BSD-3-Clause, BSD-3-Clause | ordered maps; Eino's IDs; logrus |

Also in `go.mod` and `go.sum`, downloaded by `go mod download` but linked on no platform this repository builds for (sonic's native path, which go1.27 excludes): `github.com/bytedance/sonic/loader` v0.5.0 (Apache-2.0), `github.com/cloudwego/base64x` v0.1.6 (Apache-2.0), `github.com/klauspost/cpuid/v2` v2.2.9 (MIT, Copyright (c) 2015 Klaus Post), `github.com/twitchyliquid64/golang-asm` v0.15.1 (BSD-3-Clause, Copyright (c) 2009 The Go Authors), `golang.org/x/arch` v0.11.0 (BSD-3-Clause, Copyright 2015 The Go Authors).

## Fonts served by the web app (T-045)

`apps/web` serves three font families to the browser from its own origin (`ui/global-styles.ts`; the build copies the WOFF2 files into `/_next/static/media`). They come from npm packages pinned in `apps/web/package.json` and `package-lock.json`, with no install script. The subset files the browser downloads do not reliably carry the license text themselves (checked for Archivo and Noto Sans TC in T-045; Geist and Geist Mono replaced Archivo in H-041, 2026-10-29), so the copyright and the license are recorded here.

| Package | Version | Font | License | Copyright |
|---|---|---|---|---|
| @fontsource-variable/geist 5.3.0 | 5.3.0 (2026-07-19) | Geist, variable weight (latin, latin-ext, cyrillic, vietnamese) | OFL-1.1 | Copyright 2024 The Geist Project Authors (https://github.com/vercel/geist-font) |
| @fontsource-variable/geist-mono 5.3.0 | 5.3.0 (2026-07-19) | Geist Mono, variable weight (latin, latin-ext, cyrillic, vietnamese) | OFL-1.1 | Copyright 2024 The Geist Project Authors (https://github.com/vercel/geist-font) |
| @fontsource-variable/noto-sans-tc 5.3.0 | 5.3.0 (2026-07-19) | Noto Sans TC, variable weight (Traditional Chinese, split by unicode-range) | OFL-1.1 | Google Inc. (the package's notice; Noto fonts, https://github.com/google/fonts) |

The license, as the packages ship it:

```text
This Font Software is licensed under the SIL Open Font License, Version 1.1.
This license is copied below, and is also available with a FAQ at:
http://scripts.sil.org/OFL


-----------------------------------------------------------
SIL OPEN FONT LICENSE Version 1.1 - 26 February 2007
-----------------------------------------------------------

PREAMBLE
The goals of the Open Font License (OFL) are to stimulate worldwide
development of collaborative font projects, to support the font creation
efforts of academic and linguistic communities, and to provide a free and
open framework in which fonts may be shared and improved in partnership
with others.

The OFL allows the licensed fonts to be used, studied, modified and
redistributed freely as long as they are not sold by themselves. The
fonts, including any derivative works, can be bundled, embedded,
redistributed and/or sold with any software provided that any reserved
names are not used by derivative works. The fonts and derivatives,
however, cannot be released under any other type of license. The
requirement for fonts to remain under this license does not apply
to any document created using the fonts or their derivatives.

DEFINITIONS
"Font Software" refers to the set of files released by the Copyright
Holder(s) under this license and clearly marked as such. This may
include source files, build scripts and documentation.

"Reserved Font Name" refers to any names specified as such after the
copyright statement(s).

"Original Version" refers to the collection of Font Software components as
distributed by the Copyright Holder(s).

"Modified Version" refers to any derivative made by adding to, deleting,
or substituting -- in part or in whole -- any of the components of the
Original Version, by changing formats or by porting the Font Software to a
new environment.

"Author" refers to any designer, engineer, programmer, technical
writer or other person who contributed to the Font Software.

PERMISSION & CONDITIONS
Permission is hereby granted, free of charge, to any person obtaining
a copy of the Font Software, to use, study, copy, merge, embed, modify,
redistribute, and sell modified and unmodified copies of the Font
Software, subject to the following conditions:

1) Neither the Font Software nor any of its individual components,
in Original or Modified Versions, may be sold by itself.

2) Original or Modified Versions of the Font Software may be bundled,
redistributed and/or sold with any software, provided that each copy
contains the above copyright notice and this license. These can be
included either as stand-alone text files, human-readable headers or
in the appropriate machine-readable metadata fields within text or
binary files as long as those fields can be easily viewed by the user.

3) No Modified Version of the Font Software may use the Reserved Font
Name(s) unless explicit written permission is granted by the corresponding
Copyright Holder. This restriction only applies to the primary font name as
presented to the users.

4) The name(s) of the Copyright Holder(s) or the Author(s) of the Font
Software shall not be used to promote, endorse or advertise any
Modified Version, except to acknowledge the contribution(s) of the
Copyright Holder(s) and the Author(s) or with their explicit written
permission.

5) The Font Software, modified or unmodified, in part or in whole,
must be distributed entirely under this license, and must not be
distributed under any other license. The requirement for fonts to
remain under this license does not apply to any document created
using the Font Software.

TERMINATION
This license becomes null and void if any of the above conditions are
not met.

DISCLAIMER
THE FONT SOFTWARE IS PROVIDED "AS IS", WITHOUT WARRANTY OF ANY KIND,
EXPRESS OR IMPLIED, INCLUDING BUT NOT LIMITED TO ANY WARRANTIES OF
MERCHANTABILITY, FITNESS FOR A PARTICULAR PURPOSE AND NONINFRINGEMENT
OF COPYRIGHT, PATENT, TRADEMARK, OR OTHER RIGHT. IN NO EVENT SHALL THE
COPYRIGHT HOLDER BE LIABLE FOR ANY CLAIM, DAMAGES OR OTHER LIABILITY,
INCLUDING ANY GENERAL, SPECIAL, INDIRECT, INCIDENTAL, OR CONSEQUENTIAL
DAMAGES, WHETHER IN AN ACTION OF CONTRACT, TORT OR OTHERWISE, ARISING
FROM, OUT OF THE USE OR INABILITY TO USE THE FONT SOFTWARE OR FROM
OTHER DEALINGS IN THE FONT SOFTWARE.
```

## React Flow and the packages it brings (T-015)

The 2D model editor (`apps/web/features/model-editor/`) draws its canvas with React Flow, `@xyflow/react` 12.12.0 (2026-09-24), pinned in `apps/web/package.json` and `package-lock.json` with the packages below (19 lock entries, none with an install script; versions, dates and the choice in [ADR-011](adr/ADR-011-toolchain-bootstrap.md)). They are bundled into the JavaScript the browser downloads, where the minifier may drop their license comments, so the notices are recorded here. React Flow's attribution link stays on the canvas (ADR-021, H-009 (4)).

| Package | Version | License | Copyright |
|---|---|---|---|
| @xyflow/react | 12.12.0 | MIT | Copyright (c) 2019-2025 webkid GmbH |
| @xyflow/system | 0.0.83 | MIT | Copyright (c) 2019-2025 webkid GmbH |
| zustand (React Flow's internal store; the app does not use it, ADR-016) | 4.5.7 | MIT | Copyright (c) 2019 Paul Henschel |
| classcat | 5.0.5 | MIT | Copyright © Jorge Bucaran <https://jorgebucaran.com> |
| d3-color, d3-dispatch, d3-drag, d3-interpolate, d3-selection, d3-timer, d3-transition, d3-zoom | 3.1.0, 3.0.1, 3.0.0, 3.0.1, 3.0.0, 3.0.1, 3.0.1, 3.0.0 | ISC | Copyright 2010-2021 Mike Bostock (d3-color: 2010-2022) |
| d3-ease | 3.0.1 | BSD-3-Clause | Copyright 2010-2021 Mike Bostock; Copyright 2001 Robert Penner |
| @types/d3-color, -drag, -interpolate, -selection, -transition, -zoom (type declarations only; nothing reaches the bundle) | 3.1.3, 3.0.7, 3.0.4, 3.0.12, 3.0.9, 3.0.9 | MIT | Copyright (c) Microsoft Corporation |

The MIT license, as `@xyflow/react` ships it (zustand, classcat and the type packages ship the same terms under their own copyright lines above):

```text
MIT License

Copyright (c) 2019-2025 webkid GmbH

Permission is hereby granted, free of charge, to any person obtaining a copy
of this software and associated documentation files (the "Software"), to deal
in the Software without restriction, including without limitation the rights
to use, copy, modify, merge, publish, distribute, sublicense, and/or sell
copies of the Software, and to permit persons to whom the Software is
furnished to do so, subject to the following conditions:

The above copyright notice and this permission notice shall be included in all
copies or substantial portions of the Software.

THE SOFTWARE IS PROVIDED "AS IS", WITHOUT WARRANTY OF ANY KIND, EXPRESS OR
IMPLIED, INCLUDING BUT NOT LIMITED TO THE WARRANTIES OF MERCHANTABILITY,
FITNESS FOR A PARTICULAR PURPOSE AND NONINFRINGEMENT. IN NO EVENT SHALL THE
AUTHORS OR COPYRIGHT HOLDERS BE LIABLE FOR ANY CLAIM, DAMAGES OR OTHER
LIABILITY, WHETHER IN AN ACTION OF CONTRACT, TORT OR OTHERWISE, ARISING FROM,
OUT OF OR IN CONNECTION WITH THE SOFTWARE OR THE USE OR OTHER DEALINGS IN THE
SOFTWARE.
```

The ISC license of the d3 packages:

```text
Copyright 2010-2021 Mike Bostock

Permission to use, copy, modify, and/or distribute this software for any purpose
with or without fee is hereby granted, provided that the above copyright notice
and this permission notice appear in all copies.

THE SOFTWARE IS PROVIDED "AS IS" AND THE AUTHOR DISCLAIMS ALL WARRANTIES WITH
REGARD TO THIS SOFTWARE INCLUDING ALL IMPLIED WARRANTIES OF MERCHANTABILITY AND
FITNESS. IN NO EVENT SHALL THE AUTHOR BE LIABLE FOR ANY SPECIAL, DIRECT,
INDIRECT, OR CONSEQUENTIAL DAMAGES OR ANY DAMAGES WHATSOEVER RESULTING FROM LOSS
OF USE, DATA OR PROFITS, WHETHER IN AN ACTION OF CONTRACT, NEGLIGENCE OR OTHER
TORTIOUS ACTION, ARISING OUT OF OR IN CONNECTION WITH THE USE OR PERFORMANCE OF
THIS SOFTWARE.
```

The BSD-3-Clause license of d3-ease:

```text
Copyright 2010-2021 Mike Bostock
Copyright 2001 Robert Penner
All rights reserved.

Redistribution and use in source and binary forms, with or without modification,
are permitted provided that the following conditions are met:

* Redistributions of source code must retain the above copyright notice, this
  list of conditions and the following disclaimer.

* Redistributions in binary form must reproduce the above copyright notice,
  this list of conditions and the following disclaimer in the documentation
  and/or other materials provided with the distribution.

* Neither the name of the author nor the names of contributors may be used to
  endorse or promote products derived from this software without specific prior
  written permission.

THIS SOFTWARE IS PROVIDED BY THE COPYRIGHT HOLDERS AND CONTRIBUTORS "AS IS" AND
ANY EXPRESS OR IMPLIED WARRANTIES, INCLUDING, BUT NOT LIMITED TO, THE IMPLIED
WARRANTIES OF MERCHANTABILITY AND FITNESS FOR A PARTICULAR PURPOSE ARE
DISCLAIMED. IN NO EVENT SHALL THE COPYRIGHT OWNER OR CONTRIBUTORS BE LIABLE FOR
ANY DIRECT, INDIRECT, INCIDENTAL, SPECIAL, EXEMPLARY, OR CONSEQUENTIAL DAMAGES
(INCLUDING, BUT NOT LIMITED TO, PROCUREMENT OF SUBSTITUTE GOODS OR SERVICES;
LOSS OF USE, DATA, OR PROFITS; OR BUSINESS INTERRUPTION) HOWEVER CAUSED AND ON
ANY THEORY OF LIABILITY, WHETHER IN CONTRACT, STRICT LIABILITY, OR TORT
(INCLUDING NEGLIGENCE OR OTHERWISE) ARISING IN ANY WAY OUT OF THE USE OF THIS
SOFTWARE, EVEN IF ADVISED OF THE POSSIBILITY OF SUCH DAMAGE.
```

## PlayCanvas (the read-only 3D view, T-033)

The 3D view (`apps/web/features/twin-3d/`) draws with the PlayCanvas engine, `playcanvas` 2.23.2 (2026-10-09), pinned in `apps/web/package.json` and `package-lock.json` with two type-declaration packages (3 lock entries, none with an install script; versions, dates and the choice in [ADR-011](adr/ADR-011-toolchain-bootstrap.md), the engine itself by [ADR-008](adr/ADR-008-playcanvas-visualization.md)). The engine is bundled into a chunk the browser downloads only when a person opens the 3D view, where the minifier may drop its license comment, so the notice is recorded here.

| Package | Version | License | Copyright |
|---|---|---|---|
| playcanvas | 2.23.2 | MIT | Copyright (c) 2011-2026 PlayCanvas Ltd. |
| @types/webxr (type declarations only; nothing reaches the bundle) | 0.5.24 | MIT | Copyright (c) Microsoft Corporation |
| @webgpu/types (type declarations only; nothing reaches the bundle) | 0.1.74 | BSD-3-Clause | Copyright 2022 WebGPU Developers |

The MIT license, as `playcanvas` ships it:

```text
Copyright (c) 2011-2026 PlayCanvas Ltd.

Permission is hereby granted, free of charge, to any person obtaining a copy
of this software and associated documentation files (the "Software"), to deal
in the Software without restriction, including without limitation the rights
to use, copy, modify, merge, publish, distribute, sublicense, and/or sell
copies of the Software, and to permit persons to whom the Software is
furnished to do so, subject to the following conditions:

The above copyright notice and this permission notice shall be included in
all copies or substantial portions of the Software.

THE SOFTWARE IS PROVIDED "AS IS", WITHOUT WARRANTY OF ANY KIND, EXPRESS OR
IMPLIED, INCLUDING BUT NOT LIMITED TO THE WARRANTIES OF MERCHANTABILITY,
FITNESS FOR A PARTICULAR PURPOSE AND NONINFRINGEMENT. IN NO EVENT SHALL THE
AUTHORS OR COPYRIGHT HOLDERS BE LIABLE FOR ANY CLAIM, DAMAGES OR OTHER
LIABILITY, WHETHER IN AN ACTION OF CONTRACT, TORT OR OTHERWISE, ARISING FROM,
OUT OF OR IN CONNECTION WITH THE SOFTWARE OR THE USE OR OTHER DEALINGS IN
THE SOFTWARE.
```

## AWS Signature Version 4 test suite (test data, T-039)

`testdata/sigv4/` holds 15 request vectors of the AWS Signature Version 4 test suite (`aws4_testsuite`), unmodified, as botocore mirrors it (`tests/unit/auth/aws4_testsuite`, branch `develop`, fetched 2026-10-26; each file's SHA-256 is in `testdata/sigv4/SOURCE.txt`). Only `internal/storage/objects/sigv4_test.go` reads them, to check the S3 adapter's own signer (no SDK is linked); they are not part of any binary or image. License: Apache License 2.0 (<https://www.apache.org/licenses/LICENSE-2.0>). botocore's NOTICE:

```text
Botocore
Copyright 2012-2022 Amazon.com, Inc. or its affiliates. All Rights Reserved.
```

## Container base images (T-039)

The images of `Dockerfile.api`, `Dockerfile.worker` and `apps/web/Dockerfile` are built on these, pinned by digest (versions and dates in [ADR-011](adr/ADR-011-toolchain-bootstrap.md)); nothing of them is copied into this repository. Each image carries its packages' own license files (Debian keeps them under `/usr/share/doc/*/copyright`).

| Image | Used as | License |
|---|---|---|
| `golang:1.27.3-trixie` (Docker Official Image) | build stage of the Go images only | Go: BSD-3-Clause; Debian packages: their own licenses |
| `gcr.io/distroless/static-debian13:nonroot` | runtime of the API and worker images | distroless: Apache-2.0; its Debian packages (ca-certificates, tzdata, base-files, netbase): their own licenses (MPL-2.0 for the CA bundle, public domain for tzdata, GPL-2.0+ for base-files and netbase files) |
| `node:24.21.0-trixie-slim` (Docker Official Image) | build and runtime of the web image (npm, corepack and yarn removed at run time) | Node.js: MIT (with the third-party licenses in Node's `LICENSE`); Debian packages: their own licenses |
| `postgres:16.15` (Docker Official Image) | local staging smoke only (`deploy/local/compose.yaml`), never deployed | PostgreSQL License; Debian packages: their own licenses |
