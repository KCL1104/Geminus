module github.com/kcl1104/geminus

go 1.27.0

// Pinned per docs/adr/ADR-011-toolchain-bootstrap.md; GOTOOLCHAIN=auto fetches it.
toolchain go1.27.2

// Go code vendored by npm is not part of the module: keep it out of ./... (CB-35).
ignore ./apps/web/node_modules

require (
	github.com/go-jose/go-jose/v4 v4.1.5
	github.com/gowebpki/jcs v1.0.2
	github.com/jackc/pgx/v5 v5.11.0
	github.com/santhosh-tekuri/jsonschema/v6 v6.0.3
	golang.org/x/text v0.42.0
)

require (
	github.com/jackc/pgpassfile v1.0.0 // indirect
	github.com/jackc/pgservicefile v0.0.0-20240606120523-5a60cdf6a761 // indirect
	github.com/jackc/puddle/v2 v2.2.2 // indirect
	golang.org/x/sync v0.23.0 // indirect
)
