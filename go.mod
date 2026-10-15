module github.com/kcl1104/geminus

go 1.27.0

// Pinned per docs/adr/ADR-011-toolchain-bootstrap.md; GOTOOLCHAIN=auto fetches it.
toolchain go1.27.2

require (
	github.com/santhosh-tekuri/jsonschema/v6 v6.0.3
	golang.org/x/text v0.42.0
)
