// Package jsonio reads JSON through the token stream into an ordered tree,
// configures the encoder, and defines the ordered-object type.
//
// A tree value is one of: *Object, []any (an array of tree values), string,
// json.Number (the literal's exact text), bool, or nil (null). Reading never
// uses json.Unmarshal, which loses repeated keys, the text of numbers, and key
// order (see implementation-spec.md, JSON reading).
package jsonio
