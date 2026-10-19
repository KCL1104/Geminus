package main

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

// Application layers that the kernel and the packs never reach.
var appLayers = []string{
	"./cmd/...",
	"./internal/auth/...",
	"./internal/tenant/...",
	"./internal/project/...",
	"./internal/models/...", // model, draft and version service (T-012, ADR-022)
	"./internal/runs/...",   // simulation run service and its job handler (T-023)
	"./internal/worker/...", // the job loop and its handler interface (T-019, ADR-024)
	"./internal/storage/...",
	"./internal/import/...",
	"./internal/ai/...",
	"./internal/audit/...",
	"./internal/config/...",
	"./internal/httpapi/...", // HTTP transport (T-009, ADR-019)
	"./internal/api/...",     // route assembly
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
		name:    "llm-sdks-only-in-ai",
		why:     "only the AI provider adapter may use an LLM SDK; SDK types stay out of domain signatures (docs/ARCHITECTURE.md §1)",
		applies: []string{"./..."},
		except:  []string{"./internal/ai/...", "./cmd/..."},
		forbid:  llmSDKs,
	},
	{
		name:    "db-only-in-storage",
		why:     "SQL handles live in storage and the tenant transaction scope; other packages go through them",
		applies: []string{"./..."},
		except:  []string{"./internal/storage/...", "./internal/tenant/...", "./cmd/..."},
		forbid:  cat(dbDrivers, []string{"database/sql/..."}),
		through: []string{"./internal/storage/...", "./internal/tenant/..."},
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
		name:    "auth-authenticates-only",
		why:     "internal/auth verifies tokens only; users, memberships and roles come from the tenant scope, never from auth (docs/ARCHITECTURE.md §2, D-008)",
		applies: []string{"./internal/auth/..."},
		forbid:  cat([]string{"./internal/domainpack/...", "./internal/model/...", "./internal/simulation/..."}, appLayers, dbDrivers, llmSDKs, []string{"database/sql/..."}),
		allow:   []string{"./internal/auth/..."},
	},
	{
		name:    "ai-suggests-only",
		why:     "AI only suggests: no SQL handle, storage, tenant scope or process execution (AGENTS.md §2)",
		applies: []string{"./internal/ai/..."},
		forbid:  cat([]string{"./internal/storage/...", "./internal/tenant/...", "./cmd/...", "os/exec"}, dbDrivers, []string{"database/sql/..."}),
		future:  true,
	},
}
