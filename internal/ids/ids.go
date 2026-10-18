// Package ids issues the server's public IDs (D-001): a kind prefix, "_",
// and a canonical ULID: 48 bits of Unix milliseconds and 80 random bits, in
// uppercase Crockford base32 (26 characters, the first 0–7). IDs of one kind
// sort by creation time to the millisecond; within a millisecond the order
// is random, so nothing may rely on it.
package ids

import (
	"crypto/rand"
	"encoding/binary"
	"time"
)

const alphabet = "0123456789ABCDEFGHJKMNPQRSTVWXYZ"

// New returns prefix + "_" + a new ULID.
func New(prefix string) string { return prefix + "_" + ULID(time.Now()) }

// ULID returns a ULID for t with fresh randomness from crypto/rand.
func ULID(t time.Time) string {
	var entropy [10]byte
	_, _ = rand.Read(entropy[:]) // crypto/rand.Read never fails (Go 1.24)
	return encode(uint64(t.UnixMilli()), entropy)
}

// encode writes the 128-bit value ms<<80 | entropy as 26 base32 digits,
// most significant first (the first digit holds the top 3 bits).
func encode(ms uint64, entropy [10]byte) string {
	var b [16]byte
	binary.BigEndian.PutUint64(b[0:8], ms<<16)
	copy(b[6:], entropy[:])
	hi := binary.BigEndian.Uint64(b[0:8])
	lo := binary.BigEndian.Uint64(b[8:16])
	var out [26]byte
	for i := 25; i >= 0; i-- {
		out[i] = alphabet[lo&31]
		lo = lo>>5 | hi<<59
		hi >>= 5
	}
	return string(out[:])
}
