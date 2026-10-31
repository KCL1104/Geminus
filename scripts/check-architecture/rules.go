package main

import "slices"

// The dependency rules of docs/ARCHITECTURE.md §2 and AGENTS.md §2–§3 that a
// package graph can show. A pattern is an import path; a trailing "/..."
// also matches everything below it. Module-relative patterns start with "./".

const module = "github.com/kcl1104/geminus"

// genericKernel is the Generic Twin IR, the simulation engine, results and
// the DomainPack interface: the code every domain shares.
var genericKernel = []string{
	"./internal/model/...",
	"./internal/simulation/...",
	"./internal/results/...",
	"./internal/domainpack", // the interface and registry only
	"./schemas",
}

// importPure are the import packages that only read and write bytes
// (T-016, T-017).
var importPure = []string{"./internal/import/profile/...", "./internal/import/report/...", "./internal/import/mapping/...",
	"./internal/import/validation/..."}

// Application layers that the kernel and the packs never reach.
var appLayers = []string{
	"./cmd/...",
	"./internal/auth/...",
	"./internal/tenant/...",
	"./internal/project/...",
	"./internal/models/...",      // model, draft and version service (T-012, ADR-022)
	"./internal/runs/...",        // simulation run service and its job handler (T-023)
	"./internal/proposals/...",   // AI proposal service and its job handler (T-025)
	"./internal/scenarios/...",   // scenario service: a person's scenarios, reads of both kinds (T-028)
	"./internal/comparisons/...", // baseline/scenario comparison service and HTTP adapter (T-029)
	"./internal/analyses/...",    // simulation analysis service and its job handler (T-030)
	"./internal/packs/...",       // the packs a deployment serves (T-023 review L2)
	"./internal/worker/...",      // the job loop and its handler interface (T-019, ADR-024)
	"./internal/storage/...",
	"./internal/import/...",
	"./internal/ai/...",
	"./internal/aievals/...",       // the live provider eval's runner (T-053)
	"./internal/audit/...",         // the audit catalogue (T-035)
	"./internal/observability/...", // structured logs and the privacy filter (T-035)
	"./internal/config/...",
	"./internal/httpapi/...",        // HTTP transport (T-009, ADR-019)
	"./internal/api/...",            // route assembly
	"./internal/investigations/...", // AI investigations, the API's side: the service and its executor (H-056, H-060, H-061)
	"./internal/investigator/...",   // AI investigations, the worker's side: the job handler (H-061)
}

// Standard-library packages that mean network, database, process or
// plugin I/O. net/url and net/netip are parsers (the JSON Schema library
// uses them for formats) and stay allowed.
var ioStd = []string{
	"net", "net/http/...", "net/rpc/...", "net/smtp", "net/mail",
	"database/sql/...", "os/exec", "plugin", "crypto/tls",
}

// kernelStd is every standard-library package kernel and pack code may
// import itself: computation, encoding, hashing and embedded data. The
// standard library uses the file system, raw system calls and unsafe
// underneath, so this applies to a package's own imports only. It leaves
// out os, syscall, unsafe (go:linkname needs it), runtime/cgo and plugin,
// and the many packages that reach the file system or the network without
// the caller importing os (io/ioutil, path/filepath, text/template,
// archive/zip, debug/elf, mime, crypto/x509, go/parser...): an allowlist,
// not a denylist (T-042 review N1). cgo's "C" and golang.org/x/sys are not
// standard packages; the third-party allowlist refuses them. Adding one
// is a reviewed change to this list. What import rules cannot see at all:
// writes to stdout and stderr through fmt.Print, log or the print builtins,
// time.LoadLocation reading the zoneinfo database, and file access inside
// an allowed third-party package (jsonschema's FileLoader reads file://
// URLs); review and the determinism goldens cover those.
var kernelStd = []string{
	"bytes", "cmp", "container/heap", "context", "crypto/sha256", "embed", "encoding/binary", "encoding/hex",
	"encoding/json/jsontext", "encoding/json/v2", "errors", "fmt", "io", "iter", "maps", "math", "math/bits",
	"reflect", "regexp", "runtime", "slices", "sort", "strconv", "strings", "sync", "sync/atomic", "time",
	"unicode", "unicode/utf8",
}

// Database drivers and LLM SDKs. This list cannot be complete; the kernel's
// third-party allowlist below is what stops an unlisted one.
var dbDrivers = []string{
	"github.com/jackc/pgx/...", "github.com/lib/pq/...", "github.com/go-sql-driver/...",
	"gorm.io/...", "github.com/jmoiron/sqlx/...",
}
var llmSDKs = []string{
	"github.com/anthropics/...", "github.com/openai/...", "github.com/sashabaranov/go-openai/...",
	"google.golang.org/genai/...", "github.com/google/generative-ai-go/...", "cloud.google.com/go/vertexai/...",
	"cloud.google.com/go/aiplatform/...", "github.com/aws/aws-sdk-go-v2/service/bedrockruntime/...",
	"github.com/tmc/langchaingo/...", "github.com/cohere-ai/...", "github.com/mistralai/...",
}

// kernelThirdParty is every non-standard package the kernel may link: RFC
// 8785 canonical JSON, the JSON Schema validator and its message catalog.
// For golang.org/x/text only the packages the catalog links, not the
// module: golang.org/x/text/unicode/cldr, for one, reads directories and
// zip files (T-042 third review L1). Adding one is a reviewed change to
// this list.
var kernelThirdParty = []string{
	"github.com/gowebpki/jcs/...",
	"github.com/santhosh-tekuri/jsonschema/v6/...",
	"golang.org/x/text/feature/plural",
	"golang.org/x/text/internal/...", // importable only through the packages above
	"golang.org/x/text/language",
	"golang.org/x/text/message/...",
}

// otelAPI is the OpenTelemetry API (T-035, ADR-028): attributes, codes,
// the metric and trace interfaces and their no-op implementations, and
// what they link. Not the SDK or an exporter (otel-sdk-only-in-export).
var otelAPI = []string{
	"go.opentelemetry.io/otel/attribute/...",
	"go.opentelemetry.io/otel/codes",
	"go.opentelemetry.io/otel/metric/...",
	"go.opentelemetry.io/otel/trace/...",
	"go.opentelemetry.io/otel/semconv/...",
	"github.com/cespare/xxhash/v2", // attribute set hashing
}

// Eino (H-058, H-059; ADR-031) orchestrates the AI roles. einoUsers are the
// only packages that import it (docs/design/H-056_INVESTIGATIONS_ON_EINO.md
// §6.2, narrowed by H-061, which the owner approved in H-063: "eino-fenced
// becomes ./internal/aigraph/... only"): the metered ChatModel adapters, the
// role graphs and the investigator agent. H-056's investigation loop is
// gone. None of them exists yet (E0 adds the rules).
var eino = []string{"github.com/cloudwego/eino/..."}
var einoUsers = []string{"./internal/aigraph/..."}

// einoFreeAPI is the API binary, its route assembly and the investigation
// packages only it links (H-061: "cmd/api links no Eino and no live
// adapter"; the executor "holds no Eino and no provider").
var einoFreeAPI = []string{"./cmd/api/...", "./internal/api/...", "./internal/investigations/..."}

// eino-ext's components call providers with their own SDKs and clients,
// around the metered client (ADR-031). eino-contrib/jsonschema, which Eino
// itself links, is not part of it.
var einoExt = []string{"github.com/cloudwego/eino-ext/..."}

// einoClosure is every non-standard package that the Eino packages the
// design uses (compose, schema, components/model, components/tool,
// components/tool/utils, callbacks, adk) link at v0.9.21 on any platform of
// the matrix, besides logrus's golang.org/x/sys, which einoBrings brings
// instead (TestEinoAgreedPlacementsConform reads the matrix). sonic is listed
// package by package: its compat path only (sonicJIT). ADR-031 has the
// modules, versions and licenses; adding one is a reviewed change to both.
var einoClosure = []string{
	"github.com/cloudwego/eino/...",
	"github.com/bytedance/sonic",
	"github.com/bytedance/sonic/ast",
	"github.com/bytedance/sonic/option",
	"github.com/bytedance/sonic/unquote",
	"github.com/bytedance/sonic/internal/caching",
	"github.com/bytedance/sonic/internal/compat",
	"github.com/bytedance/sonic/internal/native/types",
	"github.com/bytedance/sonic/internal/rt",
	"github.com/bytedance/sonic/internal/utils",
	"github.com/bytedance/gopkg/lang/dirtmake",
	"github.com/bahlo/generic-list-go",
	"github.com/buger/jsonparser",
	"github.com/dustin/go-humanize",
	"github.com/eino-contrib/jsonschema",
	"github.com/google/uuid",
	"github.com/goph/emperror",
	"github.com/json-iterator/go",
	"github.com/mailru/easyjson/buffer",
	"github.com/mailru/easyjson/jwriter",
	"github.com/modern-go/concurrent",
	"github.com/modern-go/reflect2",
	"github.com/nikolalohinski/gonja/...", // Jinja2 prompt templates; the design uses no ChatTemplate
	"github.com/pelletier/go-toml/v2/...",
	"github.com/pkg/errors",
	"github.com/sirupsen/logrus",
	"github.com/slongfield/pyfmt",
	"github.com/wk8/go-ordered-map/v2",
	"github.com/yargevad/filepathx",
	"golang.org/x/exp/constraints",
	"golang.org/x/exp/slices",
	"gopkg.in/yaml.v3",
}

// einoBrings are the imports of Eino's closure that aigraph-has-no-storage's
// I/O lists would refuse but that open nothing (ADR-031): google/uuid's
// database/sql/driver (its UUID is a driver.Valuer, as in
// db-only-in-storage) and net (net.Interfaces, for version 1 node IDs);
// jsonschema's net (the type net.IP); easyjson's buffer's net (net.Buffers,
// written to the writer it is given); logrus's golang.org/x/sys (its
// terminal check). Through any other package, or imported directly, each is
// still a violation (TestEinoBringsOnlyTheirImports).
var einoBrings = map[string][]string{
	"github.com/google/uuid":             {"database/sql/driver", "net"},
	"github.com/eino-contrib/jsonschema": {"net"},
	"github.com/mailru/easyjson/buffer":  {"net"},
	"github.com/sirupsen/logrus":         {"golang.org/x/sys/unix", "golang.org/x/sys/windows"},
}

// sonicJIT is sonic's native path: the JIT and its assembler, the loader
// that maps generated machine code into executable memory, and the native
// assembly. On go1.27 sonic v1.15.0 builds its compat path (encoding/json)
// instead; this keeps it there (ADR-031): a sonic release that supports
// go1.27 natively (the design names v1.15.4) would switch to the JIT
// without any import changing.
var sonicJIT = []string{
	"github.com/bytedance/sonic/loader/...",
	"github.com/bytedance/sonic/internal/jit/...",
	"github.com/bytedance/sonic/internal/decoder/jitdec/...",
	"github.com/bytedance/sonic/internal/encoder/x86/...",
	"github.com/bytedance/sonic/internal/native/...", // but native/types (sonicJITAllow)
	"github.com/twitchyliquid64/golang-asm/...",
	"github.com/cloudwego/base64x/...",
}
var sonicJITAllow = []string{"github.com/bytedance/sonic/internal/native/types"}

// The live provider adapters (T-024, T-055).
var liveAdapters = []string{"./internal/ai/provider/anthropic/...", "./internal/ai/provider/meta/..."}

// Test infrastructure: imported from _test.go files only.
var testInfra = []string{
	"./internal/contracttest/...",
	"./internal/domainpack/conformance/...",
	"./internal/auth/authtest/...",  // signs tokens: no production binary may contain it (T-008)
	"./internal/storage/dbtest/...", // provisions test tenants on the disposable database (T-009)
}

type rule struct {
	name string
	why  string
	// applies: the packages the rule constrains; except: packages it skips.
	applies, except []string
	// forbid: dependencies (transitive) the packages must not have, unless
	// they also match allow. thirdParty, when set, is the only non-standard,
	// non-module code allowed.
	forbid, allow, thirdParty []string
	// stdOnly, when set, is the only standard-library packages the package
	// itself may import (its dependencies may use others).
	stdOnly []string
	// moduleOnly, when set, is the only module packages the closure may
	// contain besides the package's own unit, so no other package of the
	// module can do for it what it may not do itself (T-042 review N1).
	moduleOnly []string
	// through: gateway packages the rule sends other packages through; their
	// own dependencies are not checked for the packages that use them.
	through []string
	// bring: a narrower gateway: the package (the key) may import exactly
	// these forbidden packages, which are not checked further; every other
	// dependency of it is checked as usual (T-035 review I8).
	bring map[string][]string
	// goOnly: the package has Go files only; assembly, C, C++, Fortran,
	// SWIG and .syso objects can make system calls no import shows (T-042
	// review N3).
	goOnly bool
	// unitDepth > 0 allows dependencies that share the package's first
	// unitDepth path elements (a pack may use its own subpackages).
	unitDepth int
	// tests: check the closure of the packages' test imports instead of the
	// package itself.
	tests bool
	// future: applies may match no package yet (the package arrives with a
	// later task); otherwise a rule that guards nothing is an error.
	future bool
}

func cat(lists ...[]string) []string {
	var out []string
	for _, l := range lists {
		out = append(out, l...)
	}
	return out
}

// without is list less the given entries.
func without(list []string, drop ...string) []string {
	var out []string
	for _, s := range list {
		if !slices.Contains(drop, s) {
			out = append(out, s)
		}
	}
	return out
}

var rules = []rule{
	{
		name:       "generic-kernel",
		why:        "the generic kernel imports no domain pack, application layer, I/O, database or LLM SDK (AGENTS.md §2, REQ-003)",
		applies:    genericKernel,
		forbid:     cat([]string{"./internal/domainpack/..."}, appLayers, testInfra, ioStd, dbDrivers, llmSDKs, []string{"testing"}),
		allow:      []string{"./internal/domainpack"},
		thirdParty: kernelThirdParty,
		stdOnly:    kernelStd,
		moduleOnly: genericKernel,
		goOnly:     true,
	},
	{
		name:    "generic-kernel-tests",
		why:     "generic kernel tests use no concrete domain pack; a pack's behaviour through the engine is tested from the pack (T-022 review)",
		applies: genericKernel,
		forbid:  cat([]string{"./internal/domainpack/..."}, appLayers, dbDrivers, llmSDKs, []string{"net/http/...", "database/sql/..."}),
		allow:   []string{"./internal/domainpack", "./internal/domainpack/conformance"},
		tests:   true,
	},
	{
		name:       "domain-packs",
		why:        "a DomainPack builds on the generic kernel only: no other pack, application layer, I/O, database or LLM SDK (docs/ARCHITECTURE.md §2–§3)",
		applies:    []string{"./internal/domainpack/..."},
		except:     cat([]string{"./internal/domainpack"}, testInfra),
		forbid:     cat([]string{"./internal/domainpack/..."}, appLayers, testInfra, ioStd, dbDrivers, llmSDKs, []string{"testing"}),
		allow:      []string{"./internal/domainpack"},
		thirdParty: kernelThirdParty,
		unitDepth:  3,
		stdOnly:    kernelStd,
		moduleOnly: genericKernel,
		goOnly:     true,
	},
	{
		name:    "conformance-is-generic",
		why:     "the DomainPack conformance suite runs any pack through the generic engine, so it names no concrete pack (T-042)",
		applies: []string{"./internal/domainpack/conformance/..."},
		forbid:  cat([]string{"./internal/domainpack/..."}, appLayers, dbDrivers, llmSDKs, []string{"net/http/...", "database/sql/..."}),
		allow:   []string{"./internal/domainpack", "./internal/domainpack/conformance/..."},
	},
	{
		name:    "no-test-infra-in-production",
		why:     "test helpers and the testing package never link into production code",
		applies: []string{"./..."},
		except:  testInfra,
		forbid:  cat(testInfra, []string{"testing"}),
	},
	{
		// T-024: the adapter package itself, not all of internal/ai, so the
		// context builder, the interface and the fake never link an SDK.
		name:    "llm-sdks-only-in-ai",
		why:     "only the AI provider adapter may use an LLM SDK; SDK types stay out of domain signatures (docs/ARCHITECTURE.md §1)",
		applies: []string{"./..."},
		except:  []string{"./internal/ai/provider/anthropic/...", "./cmd/..."},
		forbid:  llmSDKs,
	},
	{
		// T-053: the live adapter costs money, and only the worker meters it
		// (the spend ledger, internal/storage/aispend) and the live eval its
		// run (its own cap). The API and every service it shares never link
		// it, so no request can make a live call outside the ledger, and the
		// ledger's grants are the worker's only (0016). T-055: the Meta
		// adapter (Muse Spark and SAM 3.1) is a live adapter too.
		name:    "live-ai-only-in-the-worker",
		why:     "only the worker (metered by the spend ledger) and the live eval command (metered by its cap) link a live provider adapter, never the API or a service (T-053, T-055)",
		applies: []string{"./..."},
		except:  []string{"./internal/ai/provider/anthropic/...", "./internal/ai/provider/meta/...", "./cmd/worker/...", "./cmd/ai-eval-live/..."},
		forbid:  []string{"./internal/ai/provider/anthropic/...", "./internal/ai/provider/meta/..."},
	},
	{
		// T-055: the two live adapters are separate: neither links the
		// other, so a Meta call never goes through Anthropic's SDK or
		// credentials and an Anthropic call never through Meta's. Two rules,
		// one each way (review round 1, L5: the first alone let the
		// Anthropic adapter link Meta's).
		name:    "live-adapters-apart",
		why:     "a live provider adapter links no other live adapter (T-055)",
		applies: []string{"./internal/ai/provider/meta/..."},
		forbid:  []string{"./internal/ai/provider/anthropic/..."},
	},
	{
		name:    "live-adapters-apart-anthropic",
		why:     "a live provider adapter links no other live adapter: the Anthropic adapter never links the Meta adapter (T-055 review round 1, L5)",
		applies: []string{"./internal/ai/provider/anthropic/..."},
		forbid:  []string{"./internal/ai/provider/meta/..."},
	},
	{
		// T-053: the live eval's runner reads the repository's corpus and
		// fixtures and writes a report; only its command links it.
		name:    "live-eval-only-in-its-command",
		why:     "internal/aievals runs the eval corpus against a client it is given; only cmd/ai-eval-live links it (T-053)",
		applies: []string{"./..."},
		except:  []string{"./internal/aievals/...", "./cmd/ai-eval-live/..."},
		forbid:  []string{"./internal/aievals/..."},
	},
	{
		// T-039: migrations run as their own step with their own login;
		// the API and the worker (runtime logins) never link the runner.
		name:    "migrations-only-in-their-command",
		why:     "internal/storage/migrations applies migrations/ as the migration login; only cmd/migrate links it (T-039)",
		applies: []string{"./..."},
		except:  []string{"./internal/storage/migrations/...", "./cmd/migrate/..."},
		forbid:  []string{"./internal/storage/migrations/..."},
	},
	{
		name:    "db-only-in-storage",
		why:     "SQL handles live in storage and the tenant transaction scope; other packages go through them",
		applies: []string{"./..."},
		except:  []string{"./internal/storage/...", "./internal/tenant/...", "./cmd/..."},
		forbid:  cat(dbDrivers, []string{"database/sql/..."}),
		through: []string{"./internal/storage/...", "./internal/tenant/..."},
		// github.com/google/uuid (linked by the OpenTelemetry SDK's
		// resource package, T-035) imports database/sql/driver only to make
		// its UUID type a driver.Valuer and sql.Scanner; it opens nothing.
		// That one import is exempt, nothing else of its closure (review I8).
		bring: map[string][]string{"github.com/google/uuid": {"database/sql/driver"}},
	},
	{
		// The gateways above include the raw foundation, whose Open takes
		// any connection string: through it a package could open its own
		// handle around tenant.DB.Run, CheckRuntimeRole and Guard (T-009
		// review M2). Only the gateways themselves and cmd use it.
		name:    "db-handles-only-in-gateways",
		why:     "connections are opened by cmd and used through internal/tenant and the storage packages, so every query runs in the tenant scope",
		applies: []string{"./..."},
		except:  []string{"./internal/storage/...", "./internal/tenant/...", "./cmd/..."},
		forbid:  []string{"./internal/storage/postgres/..."},
		through: []string{"./internal/storage/...", "./internal/tenant/..."},
	},
	{
		// tenant.DB.Run checks the member's role in every transaction; the
		// worker scope runs in an assigned organization without one (D-008).
		// Only the worker may link it (T-019; ADR-019 R3b).
		name:    "worker-scope-only-in-the-worker",
		why:     "the worker scope checks no membership: only the worker binary and its job loop may run in it, never the API or a service it shares",
		applies: []string{"./..."},
		except:  []string{"./cmd/worker/...", "./internal/worker/...", "./internal/tenant/workerscope/..."},
		forbid:  []string{"./internal/tenant/workerscope/..."},
	},
	{
		// A job handler runs a claimed job under the worker's lease; the
		// worker binary registers it (ADR-024). The API and the services it
		// shares never link one, so no request can run the engine or write
		// as the worker (T-023). H-061: the investigation handler is one.
		name:    "job-handlers-only-in-the-worker",
		why:     "job handlers run claimed jobs under the worker's lease: only the worker binary registers them, never the API or a service it shares (ADR-024)",
		applies: []string{"./..."},
		except: []string{"./cmd/worker/...", "./internal/runs/runner/...", "./internal/import/profiler/...", "./internal/import/validator/...", "./internal/proposals/generator/...", "./internal/analyses/analyzer/...",
			"./internal/investigator/..."},
		forbid: []string{"./internal/runs/runner/...", "./internal/import/profiler/...", "./internal/import/validator/...", "./internal/proposals/generator/...", "./internal/analyses/analyzer/...",
			"./internal/investigator/..."}, // T-023, T-016, T-017, T-025, T-030, H-061
	},
	{
		// T-026: a person decides a proposal, through the API's routes and
		// for the authenticated caller. Only the API links the decision
		// service, so the worker binary, its job handlers, the other
		// services and the AI packages can never approve or reject.
		name:    "decisions-only-in-the-api",
		why:     "AI only suggests and approval is a person's: only the API links the approval service, never the worker, a job handler, another service or internal/ai (AGENTS.md §2, AI_SPEC §1)",
		applies: []string{"./..."},
		except:  []string{"./cmd/api/...", "./internal/api/...", "./internal/proposals/approval/..."},
		forbid:  []string{"./internal/proposals/approval/..."},
	},
	{
		// T-028: a person saves a scenario through the API's route, for the
		// authenticated caller. Only the API links the scenario service, so
		// the worker binary, its job handlers, the other services and the
		// AI packages can never create one; the AI's scenario is an
		// approval's effect (decisions-only-in-the-api), and the worker has
		// no INSERT on scenarios (0002).
		name:    "scenarios-only-in-the-api",
		why:     "a scenario is a person's request or a person's approval: only the API links the scenario service, never the worker, a job handler, another service or internal/ai (AGENTS.md §2, AI_SPEC §7)",
		applies: []string{"./..."},
		except:  []string{"./cmd/api/...", "./internal/api/...", "./internal/scenarios/..."},
		forbid:  []string{"./internal/scenarios/..."},
	},
	{
		// H-061 (approved by the owner in H-063; H-056 design §6.2): a person
		// creates, lists and stops their investigations through the API, and
		// the API's background executor carries out each tool call the
		// worker's agent recorded, as twin_api under the stored scope. Only
		// the API links them, so the worker, its handlers (the investigation
		// handler included) and the other services never carry out an
		// effect in a person's name.
		name:    "investigations-only-in-the-api",
		why:     "the investigation service and its executor act for the requester: only the API links internal/investigations, never the worker, a job handler, another service or internal/ai (H-061, H-063)",
		applies: []string{"./..."},
		except:  []string{"./cmd/api/...", "./internal/api/...", "./internal/investigations/..."},
		forbid:  []string{"./internal/investigations/..."},
	},
	{
		// H-061: internal/tenant/actingfor is the API's one organization-only
		// scope (two fixed statements, no user): discovery of recorded tool
		// calls and the end of an investigation whose requester lost their
		// role. Only the executor and the binary that starts it link it
		// (ADR-019 R3b's exception, D-008 amended in H-063).
		name:    "acting-for-only-in-the-executor",
		why:     "the acting-for scope runs without a user: only internal/investigations/executor and cmd/api link internal/tenant/actingfor (H-061, H-063)",
		applies: []string{"./..."},
		except:  []string{"./internal/tenant/actingfor/...", "./internal/investigations/executor/...", "./cmd/api/..."},
		forbid:  []string{"./internal/tenant/actingfor/..."},
	},
	{
		// H-056 design §6.2, kept by H-061: internal/proposals/effect holds
		// the canonical effects a person's approval and the AI's
		// investigation share (SaveScenario, WriteDraft). Only the API, the
		// approval service and the investigation packages link it, so the
		// worker and its handlers never write a scenario or a draft.
		name:    "proposal-effects-only-in-the-api",
		why:     "the shared effects write scenarios and drafts in a person's name: only the API, internal/proposals/approval and internal/investigations link internal/proposals/effect, never the worker, a job handler or internal/ai (H-056 §6.2, H-061, H-063)",
		applies: []string{"./..."},
		except: []string{"./cmd/api/...", "./internal/api/...", "./internal/proposals/approval/...", "./internal/investigations/...",
			"./internal/proposals/effect/..."},
		forbid: []string{"./internal/proposals/effect/..."},
	},
	{
		// The parser and the report writer read and write bytes: the
		// upload service and the profile job own storage, tenants and
		// transport (T-016), so the file's handling can be tested, and
		// reasoned about, on its own.
		// T-017: the mapper and the full-rows validation are pure too; the
		// job that runs them over the stored upload is
		// internal/import/validator.
		name:       "import-parsing-is-pure",
		why:        "internal/import/profile, report, mapping and validation only read and write bytes: no storage, tenant, HTTP, database, file, process or LLM access (T-016, T-017)",
		applies:    importPure,
		forbid:     cat(appLayers, testInfra, ioStd, dbDrivers, llmSDKs, []string{"testing"}),
		allow:      importPure,
		thirdParty: kernelThirdParty,
		stdOnly:    cat(kernelStd, []string{"encoding/csv"}),
		moduleOnly: cat(genericKernel, importPure),
		goOnly:     true,
	},
	{
		// The API accepts runs the worker then runs: both must serve the
		// same packs, so only internal/packs names them (T-023 review L2).
		// Packs and their own subpackages are the domain-packs rule's.
		name:    "packs-named-in-one-place",
		why:     "internal/packs is the one place that names the packs a deployment serves, so the API and the worker always serve the same domains",
		applies: []string{"./..."},
		except:  []string{"./internal/packs/...", "./internal/domainpack/..."},
		forbid:  []string{"./internal/domainpack/..."},
		allow:   []string{"./internal/domainpack"},
		through: []string{"./internal/packs/..."},
	},
	{
		name:    "auth-authenticates-only",
		why:     "internal/auth verifies tokens only; users, memberships and roles come from the tenant scope, never from auth (docs/ARCHITECTURE.md §2, D-008)",
		applies: []string{"./internal/auth/..."},
		forbid:  cat([]string{"./internal/domainpack/...", "./internal/model/...", "./internal/simulation/..."}, appLayers, dbDrivers, llmSDKs, []string{"database/sql/..."}),
		allow:   []string{"./internal/auth/..."},
	},
	{
		// T-035: every log line of the API and the worker passes this
		// package's privacy filter, so it depends on nothing that could
		// write around it: the standard library's computation, slog and the
		// writer it is given; no other module package, no third party beyond
		// the kernel's own computation libraries.
		name:       "observability-is-a-leaf",
		why:        "internal/observability is the privacy filter every log line passes: the standard library's slog, regexp and strings (and at most the kernel's computation libraries), no other module package, no I/O of its own beyond the writer it is given (T-035, D-094)",
		applies:    []string{"./internal/observability"},
		forbid:     cat(ioStd, dbDrivers, llmSDKs, []string{"testing"}),
		thirdParty: kernelThirdParty,
		moduleOnly: []string{"./internal/observability"},
		// reflect: RuntimeError tells the Go runtime's errors by their
		// package (T-035 review L3).
		stdOnly: []string{"context", "fmt", "io", "log/slog", "reflect", "regexp", "slices", "strings", "sync", "unicode", "unicode/utf8"},
		goOnly:  true,
	},
	{
		// T-035 (ADR-028): the OpenTelemetry SDK and its exporters are what
		// can send data out of the process. Only the setup package that
		// installs them when an operator named an endpoint, and the binaries
		// that call it, link them; everything else records through the API
		// (internal/observability/telemetry), which is a no-op without them.
		name:    "otel-sdk-only-in-export",
		why:     "only internal/observability/telemetry/export and cmd link the OpenTelemetry SDK, its exporters, gRPC or protobuf: export is off unless configured (D-095 (b), ADR-028)",
		applies: []string{"./..."},
		except:  []string{"./internal/observability/telemetry/export/...", "./cmd/..."},
		forbid: []string{"go.opentelemetry.io/otel/sdk/...", "go.opentelemetry.io/otel/exporters/...", "go.opentelemetry.io/proto/...",
			"google.golang.org/grpc/...", "google.golang.org/protobuf/...", "github.com/grpc-ecosystem/..."},
	},
	{
		// T-035: the instruments every component records through use the
		// OpenTelemetry API only, and the log filter's shapes.
		name:       "telemetry-uses-the-api-only",
		why:        "internal/observability/telemetry records through the OpenTelemetry API only (no SDK, exporter, network or file access), with observability's attribute shapes (D-094 (f), D-095)",
		applies:    []string{"./internal/observability/telemetry"},
		forbid:     cat(ioStd, dbDrivers, llmSDKs, []string{"testing"}),
		thirdParty: cat(otelAPI, kernelThirdParty),
		moduleOnly: []string{"./internal/observability", "./internal/observability/telemetry"},
		stdOnly:    []string{"context", "sync/atomic", "time"},
		goOnly:     true,
	},
	{
		// T-035: the catalogue every audit event is checked against before
		// internal/storage/auditlog writes it: computation on the event
		// only. It uses observability's credential detector, nothing else.
		name:       "audit-catalogue-is-pure",
		why:        "internal/audit is the catalogue every audit event is checked against: computation on the event only, no storage, tenant, HTTP, file or process access (T-035, D-091)",
		applies:    []string{"./internal/audit"},
		forbid:     cat(ioStd, dbDrivers, llmSDKs, []string{"testing"}),
		thirdParty: kernelThirdParty,
		moduleOnly: []string{"./internal/audit", "./internal/observability"},
		stdOnly:    []string{"errors", "fmt", "reflect", "regexp", "slices", "unicode/utf8"},
		goOnly:     true,
	},
	{
		// T-024: internal/ai exists now. Besides storage, the tenant scope
		// and process execution, it reaches no application layer at all:
		// no write service (models, runs, worker), no HTTP handler, no
		// auth, no config, no pack registry.
		name:    "ai-suggests-only",
		why:     "AI only suggests: no SQL handle, storage, tenant scope, write service or process execution (AGENTS.md §2)",
		applies: []string{"./internal/ai/..."},
		forbid:  cat([]string{"os/exec"}, appLayers, dbDrivers, []string{"database/sql/..."}),
		// internal/config/aiapproval is the leaf that holds the recorded
		// approvals and the token the live adapter requires (review L5); the
		// rest of internal/config stays out of reach.
		allow: []string{"./internal/ai/...", "./internal/config/aiapproval"},
		// ADR-031: the photo pipeline links Eino through the photo graph,
		// and Eino links github.com/google/uuid, whose database/sql/driver
		// import opens nothing (as in db-only-in-storage); only that import.
		bring: map[string][]string{"github.com/google/uuid": {"database/sql/driver"}},
	},
	{
		// The provider adapters (Anthropic's; T-055: Meta's) are the only AI
		// packages that talk to the network; the context builder, the
		// interface, the fake, the ID pool, the locale checks and the shared
		// projection (structured) are computation on what the server read, so
		// they import computation, encoding and embed packages only.
		// ADR-031: the photo pipeline has a rule of its own with the same
		// lists and one gateway (photopipeline-is-pure-but-its-graph).
		name:    "ai-is-pure-but-the-adapter",
		why:     "outside the provider adapters, the AI packages do no I/O: no network, database, process or file access (AGENTS.md §2, SECURITY §7)",
		applies: []string{"./internal/ai/..."},
		except: []string{"./internal/ai/provider/anthropic/...", "./internal/ai/provider/meta/...", // T-055: the Meta adapter's own HTTP client
			"./internal/ai/photopipeline"},
		forbid:     cat(ioStd, llmSDKs),
		thirdParty: kernelThirdParty,
		stdOnly:    kernelStd,
		goOnly:     true,
	},
	{
		// ADR-031 (design §6.2, §6.4): the photo pipeline becomes a facade
		// over the photo role graph. Its own imports stay those of every pure
		// AI package (TestPhotopipelineRuleKeepsTheAIPackagesLists); it
		// reaches Eino's closure, which links net, os and syscall underneath,
		// only through internal/aigraph/photo, which aigraph-has-no-storage
		// holds. No other AI package has a gateway.
		name:       "photopipeline-is-pure-but-its-graph",
		why:        "the photo pipeline does no I/O of its own, like every AI package outside the adapters; it reaches Eino only through the photo role graph (ADR-031, AGENTS.md §2, SECURITY §7)",
		applies:    []string{"./internal/ai/photopipeline"},
		forbid:     cat(ioStd, llmSDKs),
		thirdParty: kernelThirdParty,
		stdOnly:    kernelStd,
		goOnly:     true,
		through:    []string{"./internal/aigraph/photo"},
	},
	{
		// H-058, H-059 (ADR-031): Eino orchestrates the AI roles and reaches a
		// model only through the metered ChatModel adapters in
		// internal/aigraph. Every other package links it only through
		// internal/aigraph (H-061, H-063: the only gateway), so no service,
		// handler, adapter or binary builds a graph, an agent, a model or a
		// tool of its own around the reviewed path.
		name:    "eino-fenced",
		why:     "only internal/aigraph (the metered ChatModel adapters, the role graphs and the investigator agent) imports Eino; everything else reaches it through it (H-058, H-059, H-061, ADR-031)",
		applies: []string{"./..."},
		except:  einoUsers,
		forbid:  eino,
		through: einoUsers,
	},
	{
		name:    "no-eino-ext",
		why:     "eino-ext's components call models and services with their own SDKs and clients, around the metered client, the Standard-mode check, strict decoding and no-store: no package links it (ADR-031)",
		applies: []string{"./..."},
		forbid:  einoExt,
	},
	{
		name:    "no-eino-ext-in-tests",
		why:     "no test links eino-ext either: it would enter go.mod and go.sum, one import away from production (ADR-031)",
		applies: []string{"./..."},
		forbid:  einoExt,
		tests:   true,
	},
	{
		// The kernel and the packs refuse Eino through their third-party
		// allowlists already; this says so, with no gateway, and covers the
		// HTTP transport, which has no allowlist.
		name:    "no-eino-in-kernel-packs-transport",
		why:     "the engine, the IR, results, the DomainPack interface, the packs and the HTTP transport never link Eino, directly or through another package (H-058, ADR-031)",
		applies: cat(genericKernel, []string{"./internal/domainpack/...", "./internal/httpapi/..."}),
		forbid:  cat(eino, einoExt),
	},
	{
		name:    "no-eino-in-kernel-packs-transport-tests",
		why:     "the tests of the engine, the IR, results, the DomainPack interface, the packs and the HTTP transport never link Eino either (H-058, ADR-031)",
		applies: cat(genericKernel, []string{"./internal/domainpack/...", "./internal/httpapi/..."}),
		forbid:  cat(eino, einoExt),
		tests:   true,
	},
	{
		// H-061 (approved by the owner in H-063): every model call is the
		// worker's, and the API carries out the investigation's tool calls
		// as plain Go over the existing services. So the API links no Eino,
		// with no gateway: not through internal/aigraph, the photo pipeline
		// or any package (aigraph-only-in-the-worker says the same of the
		// role graphs; this holds even if that rule gains an exception), and
		// sonic's stderr line (ADR-031) never appears in the API process.
		name:    "no-eino-in-the-api",
		why:     "the API binary, its route assembly and the investigation packages only it links never link Eino or eino-ext, directly or through any package (H-061, H-063, ADR-031)",
		applies: einoFreeAPI,
		forbid:  cat(eino, einoExt),
	},
	{
		// The API binary's own tests only. internal/api's tests run the
		// closed loop in process with the worker's handlers (the proposal
		// generator, the analyzer, the runner), which link Eino once H-059
		// moves them onto the role graphs; the investigation packages'
		// closed-loop tests will run the investigation handler the same way.
		name:    "no-eino-in-the-api-tests",
		why:     "the API binary's tests never link Eino either; the route assembly's and the investigation packages' closed-loop tests may, through the worker's handlers (H-061, H-063, ADR-031)",
		applies: []string{"./cmd/api/..."},
		forbid:  cat(eino, einoExt),
		tests:   true,
	},
	{
		// ADR-031: sonic (Eino's JSON library) runs its compat path on
		// go1.27, encoding/json, with no executable memory. The cost is one
		// constant line on stderr at init (TestSonicRunsItsCompatPath).
		name:    "json-without-jit",
		why:     "sonic stays on its compat path (encoding/json): nothing links its JIT, its assembler, its loader of machine code into executable memory or its native assembly (ADR-031)",
		applies: []string{"./..."},
		forbid:  sonicJIT,
		allow:   sonicJITAllow,
	},
	{
		// Design §6.2: the role graphs and the adapter are computation on
		// what the job handler read and hands them; every I/O node body
		// (preload, verify, bind, compare, SAM) is injected by the handler,
		// and the model is reached through provider.LLMProvider, the metered
		// client. Eino's closure links os underneath (gonja's template
		// loaders, logrus, uuid, filepathx, sonic's warning) and syscall
		// through logrus's golang.org/x/sys, so the standard-library allowlist
		// applies to the packages' own imports; the I/O list applies to the
		// whole closure, but for einoBrings.
		name:    "aigraph-has-no-storage",
		why:     "internal/aigraph is the AI role graphs and their metered ChatModel adapter: the pure AI packages, the kernel and Eino, no storage, tenant scope, write service, live adapter, network, process, file or SQL access of its own (ADR-031, AGENTS.md §2)",
		applies: []string{"./internal/aigraph/..."},
		forbid: cat(without(appLayers, "./internal/ai/..."), liveAdapters, testInfra, ioStd, dbDrivers, llmSDKs,
			einoExt, sonicJIT, []string{"testing"}),
		allow:      sonicJITAllow,
		thirdParty: cat(kernelThirdParty, einoClosure),
		stdOnly:    kernelStd,
		moduleOnly: cat(genericKernel, []string{"./internal/aigraph/...", "./internal/ai/...", "./internal/ids", "./prompts"}),
		bring:      einoBrings,
		goOnly:     true,
		future:     true,
	},
	{
		// Design §6.2: the role graphs call models, so like the live
		// adapters (live-ai-only-in-the-worker) only the worker's side links
		// them: the worker binary, its proposal, analysis and (H-061)
		// investigation handlers, the photo pipeline they use (a facade over
		// the photo graph, §6.4) and the live eval. Never the API or a
		// service it shares.
		name:    "aigraph-only-in-the-worker",
		why:     "the role graphs and the investigator agent call models through the metered adapters: only the worker, its AI job handlers, the photo pipeline and the live eval link internal/aigraph, never the API or a service it shares (H-059, H-061, ADR-031)",
		applies: []string{"./..."},
		except: []string{"./internal/aigraph/...", "./cmd/worker/...", "./cmd/ai-eval-live/...", "./internal/proposals/generator/...",
			"./internal/analyses/analyzer/...", "./internal/investigator/...", "./internal/aievals/...", "./internal/ai/photopipeline"},
		forbid: []string{"./internal/aigraph/..."},
	},
}
