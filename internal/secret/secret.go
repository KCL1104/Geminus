// Package secret holds credential values so that they cannot be printed,
// logged or marshaled by accident (D-018: keys never logged).
package secret

import (
	"fmt"
	"io"
)

// Secret is a credential value. Every fmt verb prints [redacted], it
// marshals as "[redacted]", and only Reveal returns the value.
type Secret string

const redacted = "[redacted]"

func (Secret) String() string               { return redacted }
func (Secret) GoString() string             { return redacted }
func (Secret) Format(f fmt.State, _ rune)   { _, _ = io.WriteString(f, redacted) }
func (Secret) MarshalJSON() ([]byte, error) { return []byte(`"` + redacted + `"`), nil }
func (Secret) MarshalText() ([]byte, error) { return []byte(redacted), nil }

// Reveal returns the value, for the adapter that sends it.
func (s Secret) Reveal() string { return string(s) }
