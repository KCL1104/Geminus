package patch

import (
	"errors"
	"strconv"
	"strings"
)

// MaxPointerTokens bounds pointer depth (D-029).
const MaxPointerTokens = 32

var (
	errRoot     = errors.New("the empty (root) pointer is not allowed")
	errSyntax   = errors.New("a pointer must start with '/'")
	errEscape   = errors.New("'~' must be followed by '0' or '1'")
	errTooDeep  = errors.New("pointer is deeper than the limit")
	errBadIndex = errors.New("array index must be 0 or a positive integer without leading zeros")
)

// parsePointer decodes an RFC 6901 pointer exactly once into its tokens.
// Authorization compares these decoded tokens, never the raw string.
func parsePointer(p string) ([]string, error) {
	if p == "" {
		return nil, errRoot
	}
	if p[0] != '/' {
		return nil, errSyntax
	}
	raw := strings.Split(p[1:], "/")
	if len(raw) > MaxPointerTokens {
		return nil, errTooDeep
	}
	out := make([]string, len(raw))
	for i, t := range raw {
		var b strings.Builder
		for j := 0; j < len(t); j++ {
			if t[j] != '~' {
				b.WriteByte(t[j])
				continue
			}
			if j+1 >= len(t) || (t[j+1] != '0' && t[j+1] != '1') {
				return nil, errEscape
			}
			b.WriteByte(map[byte]byte{'0': '~', '1': '/'}[t[j+1]])
			j++
		}
		out[i] = b.String()
	}
	return out, nil
}

// arrayIndex parses an RFC 6901 array index. "-" (append) is handled by callers.
func arrayIndex(tok string) (int, error) {
	if tok == "" || (len(tok) > 1 && tok[0] == '0') {
		return 0, errBadIndex
	}
	for _, c := range tok {
		if c < '0' || c > '9' {
			return 0, errBadIndex
		}
	}
	n, err := strconv.Atoi(tok)
	if err != nil {
		return 0, errBadIndex
	}
	return n, nil
}
