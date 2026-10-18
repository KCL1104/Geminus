package domainpack

// NewRegistryAnyPackage is NewRegistry without the package check, for test
// packs declared in test packages.
func NewRegistryAnyPackage(packs ...DomainPack) (*Registry, error) {
	return newRegistry(false, packs...)
}

// CheckPackPackage is the package check of NewRegistry on a package path.
var CheckPackPackage = checkPackPackage
