// Package engine is the native Go discrete-event engine (T-020…T-022). This
// file is its random-number profile (docs/SIMULATION_SPEC.md §4, D-006):
// SplitMix64, the (0,1) uniform mapping and SHA-256 seed derivation. Changing
// any of it changes results and requires a new engine_version.
package engine

import (
	"crypto/sha256"
	"encoding/binary"
)

// SplitMix64 is the engine's only generator. It is not safe for concurrent
// use; every (replication, node, purpose) stream owns one.
type SplitMix64 struct{ state uint64 }

// NewSplitMix64 starts a generator at state.
func NewSplitMix64(state uint64) *SplitMix64 { return &SplitMix64{state: state} }

// Next returns the next 64-bit output.
func (r *SplitMix64) Next() uint64 {
	r.state += 0x9e3779b97f4a7c15
	z := r.state
	z = (z ^ (z >> 30)) * 0xbf58476d1ce4e5b9
	z = (z ^ (z >> 27)) * 0x94d049bb133111eb
	return z ^ (z >> 31)
}

// Uniform returns ((Next() >> 12) + 0.5) / 2^52, strictly inside (0, 1), so
// -ln(1-U) and inverse CDFs never see 0 or 1.
func (r *SplitMix64) Uniform() float64 {
	return (float64(r.Next()>>12) + 0.5) / (1 << 52)
}

// Stream purposes. Each draws from its own stream so, for example, adding a
// routing decision never shifts service samples.
const (
	PurposeArrival = "arrival"
	PurposeService = "service"
	PurposeDelay   = "delay"
	PurposeRouting = "routing"
	PurposeTrace   = "trace" // trace sampling; never consumes the streams above
)

// ReplicationSeed is the first 8 bytes, little-endian, of
// SHA256("dt-rep-v1\x00" || uint64LE(baseSeed) || uint32LE(index)).
func ReplicationSeed(baseSeed uint64, index uint32) uint64 {
	var buf [len("dt-rep-v1\x00") + 8 + 4]byte
	n := copy(buf[:], "dt-rep-v1\x00")
	binary.LittleEndian.PutUint64(buf[n:], baseSeed)
	binary.LittleEndian.PutUint32(buf[n+8:], index)
	sum := sha256.Sum256(buf[:])
	return binary.LittleEndian.Uint64(sum[:8])
}

// StreamSeed is the first 8 bytes, little-endian, of
// SHA256("dt-stream-v1\x00" || uint64LE(replicationSeed) || uint32LE(len(nodeID)) || nodeID ||
// uint32LE(len(purpose)) || purpose). Length prefixes keep ("ab","c") and
// ("a","bc") apart.
func StreamSeed(replicationSeed uint64, nodeID, purpose string) uint64 {
	buf := make([]byte, 0, len("dt-stream-v1\x00")+8+4+len(nodeID)+4+len(purpose))
	buf = append(buf, "dt-stream-v1\x00"...)
	buf = binary.LittleEndian.AppendUint64(buf, replicationSeed)
	buf = binary.LittleEndian.AppendUint32(buf, uint32(len(nodeID)))
	buf = append(buf, nodeID...)
	buf = binary.LittleEndian.AppendUint32(buf, uint32(len(purpose)))
	buf = append(buf, purpose...)
	sum := sha256.Sum256(buf)
	return binary.LittleEndian.Uint64(sum[:8])
}

// Stream returns the generator for one (replication, node, purpose).
func Stream(replicationSeed uint64, nodeID, purpose string) *SplitMix64 {
	return NewSplitMix64(StreamSeed(replicationSeed, nodeID, purpose))
}
