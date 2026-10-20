package review

// Issue codes of the review (common.schema.json#/$defs/ValidationCode;
// D-061 PROPOSED, CB-40 (a), (b)). The UI localizes them like the
// validator's; internal/model/validate's catalogue test reads this file.
const (
	// IDPoolViolation: a new node, flow or resource whose ID is not one the
	// proposal's ID allocation pool holds for its kind, or that another
	// entity of the base uses (D-046).
	IDPoolViolation = "ID_POOL_VIOLATION"
	// OutputInvalid: the provider's answer is not an acceptable proposal
	// (it fails the output schema, smuggles changes into a request for
	// input, changes nothing, or states a fabricated number); the code the
	// provider client issues for a schema-invalid answer (T-024).
	OutputInvalid = "AI_OUTPUT_INVALID"
)
