package profile

import (
	"encoding/json/jsontext"
	"regexp"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"
)

// The value shapes a type is inferred from, exactly as written: JSON's
// number grammar (so "007", "+5" and " 5" are strings) and RFC 3339
// date-times with an offset (JSON Schema's date-time; a time without an
// offset is a string). An offset is RFC 3339's time-numoffset, 00:00 to
// 23:59, as canonical-event.schema.json's date-time checks it (T-017
// review round 1, M3: Go's time.Parse also takes +24:00 and +05:60).
// "-00:00" is RFC 3339's "the offset is unknown" (section 4.3): no time
// zone is assumed for it (D-072 (a); review round 1, L6).
var (
	integerText   = regexp.MustCompile(`^-?(0|[1-9][0-9]*)$`)
	numberText    = regexp.MustCompile(`^-?(0|[1-9][0-9]*)(\.[0-9]+)?([eE][+-]?[0-9]+)?$`)
	timestampText = regexp.MustCompile(`^[0-9]{4}-[0-9]{2}-[0-9]{2}[Tt][0-9]{2}:[0-9]{2}:[0-9]{2}(\.[0-9]+)?([Zz]|[+-]([01][0-9]|2[0-3]):[0-5][0-9])$`)
)

func isTimestamp(s string) bool {
	if !timestampText.MatchString(s) || strings.HasSuffix(s, "-00:00") {
		return false
	}
	_, err := time.Parse(time.RFC3339Nano, strings.ToUpper(s)) // the pattern is ASCII
	return err == nil
}

func isBoolean(s string) bool {
	// ASCII case only: strings.EqualFold would also take Unicode folds
	// such as "falſe".
	return len(s) <= 5 && (strings.ToLower(s) == "true" || strings.ToLower(s) == "false") && isASCII(s)
}

func isASCII(s string) bool {
	for i := 0; i < len(s); i++ {
		if s[i] >= utf8.RuneSelf {
			return false
		}
	}
	return true
}

func finite(lit string) bool {
	_, err := strconv.ParseFloat(lit, 64)
	return err == nil
}

// classifyText is the type of a CSV value.
func classifyText(s string) Type {
	switch {
	case s == "":
		return Empty
	case isBoolean(s):
		return Boolean
	case integerText.MatchString(s):
		return Integer
	case numberText.MatchString(s) && finite(s):
		return Number
	case isTimestamp(s):
		return Timestamp
	}
	return String
}

// classifyJSON is the type of a JSON value and its length in characters:
// a string's decoded text, any other value's compact JSON text. Null and
// the empty string are empty; objects and arrays are mixed.
func classifyJSON(v jsontext.Value) (Type, int, error) {
	switch v.Kind() {
	case 'n':
		return Empty, 0, nil
	case 't', 'f':
		return Boolean, len(v), nil
	case '0':
		if strings.ContainsAny(string(v), ".eE") {
			return Number, len(v), nil
		}
		return Integer, len(v), nil
	case '"':
		s, err := unquote(v)
		if err != nil {
			return "", 0, err
		}
		n := utf8.RuneCountInString(s)
		switch {
		case s == "":
			return Empty, 0, nil
		case isTimestamp(s):
			return Timestamp, n, nil
		}
		return String, n, nil
	}
	c := v.Clone()
	if err := c.Compact(); err != nil {
		return "", 0, err
	}
	return Mixed, utf8.RuneCount(c), nil
}

func unquote(v jsontext.Value) (string, error) {
	s, err := jsontext.AppendUnquote(nil, v)
	return string(s), err
}

// colStats is what a column's values have shown so far.
type colStats struct {
	types    uint8 // one bit per non-empty Type seen
	nonEmpty int
	maxLen   int
}

// nonEmpty are the types a non-empty value can have, one bit each.
var nonEmpty = []Type{Boolean, Integer, Number, Timestamp, String, Mixed}

func bit(t Type) uint8 {
	for i, u := range nonEmpty {
		if u == t {
			return 1 << i
		}
	}
	return 0
}

func (c *colStats) add(t Type, length int) {
	c.maxLen = max(c.maxLen, length)
	if t == Empty {
		return
	}
	c.nonEmpty++
	c.types |= bit(t)
}

// inferred is the one type all non-empty values share; integers with
// numbers are numbers; none is empty; anything else is mixed.
func (c *colStats) inferred() Type {
	switch c.types {
	case 0:
		return Empty
	case bit(Integer) | bit(Number):
		return Number
	}
	for _, t := range nonEmpty {
		if c.types == bit(t) {
			return t
		}
	}
	return Mixed
}

func (c *colStats) column(name string, index int) Column {
	return Column{Name: name, Index: index, Inferred: c.inferred(), NonEmpty: c.nonEmpty, MaxLength: c.maxLen}
}
