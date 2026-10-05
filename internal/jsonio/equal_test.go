package jsonio

import "testing"

func TestEqual(t *testing.T) {
	for _, tc := range []struct {
		a, b string
		want bool
	}{
		// Numbers, by exact value.
		{`1`, `1`, true},
		{`1`, `1.0`, true},
		{`100`, `1e2`, true},
		{`100`, `1E+2`, true},
		{`0.5`, `5e-1`, true},
		{`-0`, `0`, true},
		{`1`, `2`, false},
		{`1e400`, `2e400`, false},
		{`1e400`, `10e399`, true},
		{`12345678901234567890`, `12345678901234567891`, false},
		{`0.1`, `0.10000000000000001`, false},
		// Past big.Rat's exponent limit: equal only when written the same.
		{`1e99999999`, `1e99999999`, true},
		{`1e99999999`, `10e99999998`, false},
		{`1e99999999`, `1`, false},
		// Other scalars.
		{`"a"`, `"a"`, true},
		{`"a"`, `"b"`, false},
		{`"1"`, `1`, false},
		{`true`, `true`, true},
		{`true`, `false`, false},
		{`null`, `null`, true},
		{`null`, `false`, false},
		{`null`, `0`, false},
		// Objects, regardless of member order.
		{`{}`, `{}`, true},
		{`{"a": 1, "b": 2}`, `{"b": 2.0, "a": 1}`, true},
		{`{"a": 1}`, `{"a": 1, "b": 2}`, false},
		{`{"a": 1, "b": 2}`, `{"a": 1}`, false},
		{`{"a": 1}`, `{"b": 1}`, false},
		{`{"a": {"x": [1, {"y": null}]}}`, `{"a": {"x": [1.0, {"y": null}]}}`, true},
		{`{"a": {"x": [1, {"y": null}]}}`, `{"a": {"x": [1, {"y": false}]}}`, false},
		// Arrays, in order.
		{`[1, 2]`, `[1, 2]`, true},
		{`[1, 2]`, `[2, 1]`, false},
		{`[1]`, `[1, 1]`, false},
		{`[]`, `{}`, false},
	} {
		a, _, err := ParseValue([]byte(tc.a))
		if err != nil {
			t.Fatal(err)
		}
		b, _, err := ParseValue([]byte(tc.b))
		if err != nil {
			t.Fatal(err)
		}
		if got := Equal(a, b); got != tc.want {
			t.Errorf("Equal(%s, %s) = %v, want %v", tc.a, tc.b, got, tc.want)
		}
		if got := Equal(b, a); got != tc.want {
			t.Errorf("Equal(%s, %s) = %v, want %v", tc.b, tc.a, got, tc.want)
		}
	}
}
