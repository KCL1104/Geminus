package electronicsline

// The issue codes of the pack's rules (ELECTRONICS_LINE §3.4), in the
// contract's catalogue (common.schema.json#/$defs/ValidationCode), which
// the UI localizes. DOMAIN_MISMATCH, every pack's, is in pack.go.
const (
	// StationResourceInvalid (error): a station process does not have
	// exactly one resource whose role is its station's (rule 1).
	StationResourceInvalid = "STATION_RESOURCE_INVALID"
	// RetestResourceMismatch (error): a retest does not share the primary
	// resource of its station's first test, or its station has no first
	// test (rule 3).
	RetestResourceMismatch = "RETEST_RESOURCE_MISMATCH"
	// TestResultNotIdentified (warning): a test result decision does not
	// have exactly one flow to a failure node, so the station's first-pass
	// yield is unavailable (rule 2).
	TestResultNotIdentified = "TEST_RESULT_NOT_IDENTIFIED"
	// DomainAttributesInvalid (warning): a station, pass or result_of
	// attribute the pack cannot use; the node is then not a station or a
	// test result (§3.3).
	DomainAttributesInvalid = "DOMAIN_ATTRIBUTES_INVALID"
)
