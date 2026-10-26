package structured

import "maps"

// MaxIssues bounds the issues Decode returns for one answer, as
// provider.Client bounds its own.
const MaxIssues = maxIssues

// ProjectFrom projects file from docs, schema documents by file name, as
// Project does from schemas/ (the tests' own schemas).
func ProjectFrom(docs map[string]any, file string) (Projection, error) { return project(docs, file) }

// DroppedKeywords are the constraint keywords the projection removes and
// lists in Projection.Dropped (a copy).
func DroppedKeywords() map[string]bool { return maps.Clone(dropped) }
