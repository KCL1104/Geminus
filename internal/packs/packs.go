// Package packs names the domain packs this deployment serves (T-023
// review L2). It is the one registry both binaries build: the API
// (internal/api) to validate models and accept runs, the worker
// (cmd/worker) to run them, so the API never accepts a run of a domain the
// worker cannot run. No other production package names a concrete pack
// (architecture rule packs-named-in-one-place); the generic kernel never
// imports this package.
package packs

import (
	"github.com/kcl1104/geminus/internal/domainpack"
	"github.com/kcl1104/geminus/internal/domainpack/electronicsline"
	"github.com/kcl1104/geminus/internal/domainpack/productionlogistics"
	"github.com/kcl1104/geminus/internal/domainpack/softwaredelivery"
)

// Registry returns a new registry of the packs this deployment serves: the
// P0 electronics assembly and test line (H-016, T-051), the production and
// logistics pack of traditional factories and a warehouse (H-045) and the
// software delivery example pack (T-011).
func Registry() (*domainpack.Registry, error) {
	return domainpack.NewRegistry(electronicsline.Pack{}, productionlogistics.Pack{}, softwaredelivery.Pack{})
}
