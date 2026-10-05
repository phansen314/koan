package jsonio

import (
	"bytes"
	"encoding/json"
)

// MarshalFile encodes v in the design spec's File format: two-space indent,
// one member or item per line, empty objects and arrays as {} and [], minimal
// string escaping, and a single trailing newline. Key order comes from struct
// field order and from *Object's stored order; nil slices encode as null, so
// callers must pass empty ones. Sets must already be sorted.
func MarshalFile(v any) ([]byte, error) {
	return marshal(v, "  ")
}

// MarshalLine encodes v compactly on one line, followed by a newline: the
// CLI's output envelope.
func MarshalLine(v any) ([]byte, error) {
	return marshal(v, "")
}

// With HTML escaping off, the encoder escapes exactly ", \, U+0000–U+001F,
// U+2028, and U+2029: the File format's list.
func marshal(v any, indent string) ([]byte, error) {
	var b bytes.Buffer
	enc := json.NewEncoder(&b)
	enc.SetEscapeHTML(false)
	if indent != "" {
		enc.SetIndent("", indent)
	}
	if err := enc.Encode(v); err != nil {
		return nil, err
	}
	return b.Bytes(), nil
}
