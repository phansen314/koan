package jsonio

import (
	"encoding/json"
	"math/big"
)

// Equal reports whether a and b, values as ParseValue returns them, are equal
// as JSON values (implementation-spec.md, Comparing values in update):
// objects regardless of member order, arrays element by element, numbers by
// exact numeric value. A number big.Rat refuses — an exponent past its limit
// — equals only a number written the same way. Objects are compared by
// their first member of each key, as Get finds it; callers compare objects
// without repeated keys.
func Equal(a, b any) bool {
	switch a := a.(type) {
	case json.Number:
		b, ok := b.(json.Number)
		if !ok {
			return false
		}
		if a == b {
			return true
		}
		x, okA := new(big.Rat).SetString(string(a))
		y, okB := new(big.Rat).SetString(string(b))
		return okA && okB && x.Cmp(y) == 0
	case *Object:
		b, ok := b.(*Object)
		if !ok || a.Len() != b.Len() {
			return false
		}
		for _, m := range a.Members {
			v, ok := b.Get(m.Key)
			if !ok || !Equal(m.Value, v) {
				return false
			}
		}
		return true
	case []any:
		b, ok := b.([]any)
		if !ok || len(a) != len(b) {
			return false
		}
		for i := range a {
			if !Equal(a[i], b[i]) {
				return false
			}
		}
		return true
	case string, bool, nil:
		return a == b
	}
	return false
}
