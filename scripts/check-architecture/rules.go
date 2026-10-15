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
	"./internal/storage/...",
	"./internal/import/...",
	"./internal/ai/...",
	"./internal/audit/...",
	"./internal/config/...",
}

// Standard-library packages that mean network, database, process or
// plugin I/O. net/url and net/netip are parsers (the JSON Schema library
// uses them for formats) and stay allowed.
var ioStd = []string{
	"net", "net/http/...", "net/rpc/...", "net/smtp", "net/mail",
	"database/sql/...", "os/exec", "plugin", "crypto/tls",
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

// kernelThirdParty is every non-standard module the kernel may link: RFC
// 8785 canonical JSON, the JSON Schema validator and its message catalog.
// Adding one is a reviewed change to this list.
var kernelThirdParty = []string{
	"github.com/gowebpki/jcs/...",
	"github.com/santhosh-tekuri/jsonschema/v6/...",
	"golang.org/x/text/...",
}

// Test infrastructure: imported from _test.go files only.
var testInfra = []string{
	"./internal/contracttest/...",
	"./internal/domainpack/conformance/...",
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
		name:      "domain-packs",
		why:       "a DomainPack builds on the generic kernel only: no other pack, application layer, I/O, database or LLM SDK (docs/ARCHITECTURE.md §2–§3)",
		applies:   []string{"./internal/domainpack/..."},
		except:    cat([]string{"./internal/domainpack"}, testInfra),
		forbid:    cat([]string{"./internal/domainpack/..."}, appLayers, testInfra, ioStd, dbDrivers, llmSDKs, []string{"testing"}),
		allow:     []string{"./internal/domainpack"},
		unitDepth: 3,
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
	},
	{
		name:    "ai-suggests-only",
		why:     "AI only suggests: no SQL handle, storage, tenant scope or process execution (AGENTS.md §2)",
		applies: []string{"./internal/ai/..."},
		forbid:  cat([]string{"./internal/storage/...", "./internal/tenant/...", "./cmd/...", "os/exec"}, dbDrivers, []string{"database/sql/..."}),
		future:  true,
	},
}
