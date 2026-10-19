// Package objects is private object storage (D-044, API_DATABASE §8,
// SECURITY §6): a minimal S3-compatible interface for the artifacts and
// uploads the database only names (an object key, its SHA-256 and size).
//
//   - Keys are made by the server (NewKey), never taken from a client: each
//     starts with tenant/<organization ID>/ and an area, and the rest is
//     server-made ASCII segments, the rules twin.object_key_ok checks in the
//     database (D-035).
//   - Put streams an object, computes its SHA-256 and size and never
//     replaces an existing object: an object, once written, is what the row
//     naming it says it is.
//   - Get reads an object back; whoever serves it has authorized the caller
//     against the row that names it first, and streams it (no signed URLs,
//     D-044).
//   - Delete is controlled maintenance only (retention, orphans after a
//     failed commit, ARCHITECTURE §8): it is a separate interface no
//     request path holds.
//
// The only adapter is FS, for development and tests. A production adapter
// (cloud, bucket, region, encryption keys, retention) waits for the owner's
// cloud choice and its ADR; until then no real customer data is stored
// (SECURITY §6).
package objects

import (
	"context"
	"errors"
	"io"
	"regexp"
	"strings"
)

// Area is the part of a tenant's key space an object belongs to
// (SECURITY §6).
type Area string

const (
	Imports           Area = "imports"
	Datasets          Area = "datasets"
	SimulationResults Area = "simulation-results"
	Assets            Area = "assets"
)

// Object is a stored object as the database records it.
type Object struct {
	Key    string
	SHA256 string // "sha256:" + lowercase hex, as twin.sha256
	Size   int64
}

var (
	// ErrNotFound: no object has the key.
	ErrNotFound = errors.New("objects: not found")
	// ErrExists: an object already has the key; Put never replaces one.
	ErrExists = errors.New("objects: an object already has this key")
	// ErrTooLarge: the object is larger than Put's limit; nothing was kept.
	ErrTooLarge = errors.New("objects: object too large")
	// ErrInvalidKey: the key is not one the server makes (NewKey).
	ErrInvalidKey = errors.New("objects: invalid key")
)

// Store reads and writes objects. Implementations are safe for concurrent
// use and honour ctx.
type Store interface {
	// Put stores r's bytes, at most max of them, under key, and reports
	// their SHA-256 and size. It fails with ErrExists if key is taken and
	// ErrTooLarge (keeping nothing) if r has more than max bytes.
	Put(ctx context.Context, key string, r io.Reader, max int64) (Object, error)
	// Get opens the object key; ErrNotFound if there is none.
	Get(ctx context.Context, key string) (io.ReadCloser, error)
}

// Maintainer removes objects. Only controlled maintenance (retention,
// orphan cleanup) holds one, never a request or job path (D-044).
type Maintainer interface {
	Delete(ctx context.Context, key string) error
}

// MaxKeyBytes is the longest key (twin.object_key_ok).
const MaxKeyBytes = 1024

var (
	orgID   = regexp.MustCompile(`^org_[0-7][0-9A-HJKMNP-TV-Z]{25}$`)
	segment = regexp.MustCompile(`^[0-9A-Za-z_-][0-9A-Za-z._-]*$`)
)

func (a Area) valid() bool {
	switch a {
	case Imports, Datasets, SimulationResults, Assets:
		return true
	}
	return false
}

// NewKey is the key of an object of organization org in area, named by the
// server-made segments: tenant/<org>/<area>/<segment>/... Each segment is
// ASCII letters, digits, '_', '-' and '.', not starting with '.', and the
// key holds no ".." (D-035); anything else is ErrInvalidKey.
func NewKey(org string, area Area, segments ...string) (string, error) {
	if !orgID.MatchString(org) || !area.valid() || len(segments) == 0 {
		return "", ErrInvalidKey
	}
	for _, s := range segments {
		if !segment.MatchString(s) {
			return "", ErrInvalidKey
		}
	}
	key := "tenant/" + org + "/" + string(area) + "/" + strings.Join(segments, "/")
	if !ValidKey(org, key) {
		return "", ErrInvalidKey
	}
	return key, nil
}

// ValidKey reports whether key is a key of organization org that the server
// could have made: the rules of twin.object_key_ok(key, 'tenant/'||org||'/')
// (D-035): at most MaxKeyBytes, no "..", and after the prefix only
// segments as NewKey makes them.
func ValidKey(org, key string) bool {
	prefix := "tenant/" + org + "/"
	if !orgID.MatchString(org) || !strings.HasPrefix(key, prefix) || len(key) > MaxKeyBytes || strings.Contains(key, "..") {
		return false
	}
	for s := range strings.SplitSeq(key[len(prefix):], "/") {
		if !segment.MatchString(s) {
			return false
		}
	}
	return true
}

// InArea reports whether key is a valid key of org in area.
func InArea(org string, area Area, key string) bool {
	return area.valid() && ValidKey(org, key) && strings.HasPrefix(key, "tenant/"+org+"/"+string(area)+"/")
}
