package model

import (
	"encoding/json/jsontext"
	"encoding/json/v2"
)

// Decode unmarshals one JSON document into v, rejecting unknown members,
// duplicate member names and invalid UTF-8. It checks structure only; schema
// and semantic validation are separate layers (D-014).
func Decode(data []byte, v any) error {
	return json.Unmarshal(data, v,
		json.RejectUnknownMembers(true),
		jsontext.AllowDuplicateNames(false),
		jsontext.AllowInvalidUTF8(false))
}

// Encode marshals v without HTML escaping; it is not a canonical (JCS) form.
func Encode(v any) ([]byte, error) {
	return json.Marshal(v, jsontext.EscapeForHTML(false))
}
