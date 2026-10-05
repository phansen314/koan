package jsonio

import (
	"bytes"
	"encoding/json"
	"errors"
	"reflect"
	"strconv"
	"strings"
	"testing"
	"unicode/utf16"
	"unicode/utf8"
)

func failureOf(err error) Failure {
	var re *ReadError
	if errors.As(err, &re) {
		return re.Failure
	}
	return ""
}

// The edge-case table of implementation-spec.md, JSON reading.
func TestParseObjectEdgeCases(t *testing.T) {
	for _, tc := range []struct {
		name     string
		in       string
		fail     Failure
		repeated []string
	}{
		{"valid", `{"a":1,"b":[true,null,"x"]}`, "", nil},
		{"surrounding whitespace", " \n\t{}\r\n ", "", nil},
		{"repeated key", `{"a":1,"a":2}`, "", []string{"/a"}},
		{"repeated key nested", `{"extra":{"status":1,"status":2}}`, "", []string{"/extra/status"}},
		{"repeated key in array", `{"x":[{"k":1},{"k":1,"k":2}]}`, "", []string{"/x/1/k"}},
		{"repeated key reported once", `{"a":1,"a":2,"a":3}`, "", []string{"/a"}},
		{"repeated keys in order", `{"b":1,"z":{"q":1,"q":1},"b":2}`, "", []string{"/z/q", "/b"}},
		{"repeat through escapes", `{"a":1,"\u0061":2}`, "", []string{"/a"}},
		{"repeat at depth", `{"x":[0,{"y":{"k~":1,"k~":2}}]}`, "", []string{"/x/1/y/k~0"}},
		{"pointer escaping", `{"a/b~":1,"a/b~":2}`, "", []string{"/a~1b~0"}},
		{"same key in sibling objects", `{"x":{"k":1},"y":{"k":1}}`, "", nil},
		{"invalid UTF-8", "{\"a\":\"\xff\"}", FailInvalidUTF8, nil},
		{"byte-order mark", "\xEF\xBB\xBF{}", FailBOM, nil},
		{"empty", "", FailEmpty, nil},
		{"whitespace only", " \n ", FailEmpty, nil},
		{"array", `[]`, FailNotObject, nil},
		{"string", `"x"`, FailNotObject, nil},
		{"number", `2.0`, FailNotObject, nil},
		{"null", `null`, FailNotObject, nil},
		{"second value", `{} {}`, FailTrailing, nil},
		{"second value, jq -c", "{\"a\":1}\n{\"a\":2}\n", FailTrailing, nil},
		{"trailing garbage", `{}x`, FailTrailing, nil},
		{"trailing comma after value", `{},`, FailTrailing, nil},
		{"truncated", `{"a":1`, FailSyntax, nil},
		{"missing value", `{"a":}`, FailSyntax, nil},
		{"missing colon", `{"a" 1}`, FailSyntax, nil},
		{"trailing comma in object", `{"a":1,}`, FailSyntax, nil},
		{"trailing comma in array", `{"a":[1,]}`, FailSyntax, nil},
		{"missing comma in array", `{"a":[1 2]}`, FailSyntax, nil},
		{"bad literal", `{"a":tru}`, FailSyntax, nil},
		{"leading zero", `{"a":01}`, FailSyntax, nil},
		{"single quotes", `{'a':1}`, FailSyntax, nil},
		{"surrogate pair", `{"a":"\ud83d\ude00"}`, "", nil},
		{"surrogate pair, upper case", `{"a":"\uD83D\uDE00"}`, "", nil},
		{"escaped backslash, then ud800", `{"a":"\\ud800"}`, "", nil},
		{"lone high surrogate", `{"a":"\ud800"}`, FailLoneSurrogate, nil},
		{"lone low surrogate", `{"a":"\udc00"}`, FailLoneSurrogate, nil},
		{"high then high", `{"a":"\ud800\ud800"}`, FailLoneSurrogate, nil},
		{"high then text", `{"a":"\ud800x"}`, FailLoneSurrogate, nil},
		{"high then other escape", `{"a":"\ud800\n"}`, FailLoneSurrogate, nil},
		{"low then high", `{"a":"\udc00\ud800"}`, FailLoneSurrogate, nil},
		{"lone surrogate in key", `{"\ud800":1,"\udc00":2}`, FailLoneSurrogate, nil},
		{"high then escaped backslash", `{"a":"\ud800\\udc00"}`, FailLoneSurrogate, nil},
		{"escaped backslash, then high", `{"a":"\\\ud800"}`, FailLoneSurrogate, nil},
	} {
		obj, repeated, err := ParseObject([]byte(tc.in))
		if got := failureOf(err); got != tc.fail {
			t.Errorf("%s: failure %q, want %q (err %v)", tc.name, got, tc.fail, err)
			continue
		}
		if err != nil {
			if obj != nil || repeated != nil {
				t.Errorf("%s: tree or repeats returned with an error", tc.name)
			}
			continue
		}
		if !reflect.DeepEqual(repeated, tc.repeated) {
			t.Errorf("%s: repeated %q, want %q", tc.name, repeated, tc.repeated)
		}
	}
}

func TestParseKeepsRepeatedMembers(t *testing.T) {
	obj, _, err := ParseObject([]byte(`{"a":1,"b":2,"a":3}`))
	if err != nil {
		t.Fatal(err)
	}
	var keys []string
	for _, m := range obj.Members {
		keys = append(keys, m.Key)
	}
	if !reflect.DeepEqual(keys, []string{"a", "b", "a"}) {
		t.Errorf("members %q", keys)
	}
	if v, _ := obj.Get("a"); v != json.Number("1") {
		t.Errorf("Get returns the first member, got %v", v)
	}
}

func TestParseTree(t *testing.T) {
	obj, _, err := ParseObject([]byte(`{"s":"x\né","n":2.0,"e":1e400,"i":12345678901234567890,"z":-0,"t":true,"f":false,"nil":null,"a":[1,{"k":[]},[]],"o":{}}`))
	if err != nil {
		t.Fatal(err)
	}
	want := &Object{Members: []Member{
		{"s", "x\né"},
		{"n", json.Number("2.0")},
		{"e", json.Number("1e400")},
		{"i", json.Number("12345678901234567890")},
		{"z", json.Number("-0")},
		{"t", true},
		{"f", false},
		{"nil", nil},
		{"a", []any{json.Number("1"), &Object{Members: []Member{{"k", []any{}}}}, []any{}}},
		{"o", &Object{}},
	}}
	if !reflect.DeepEqual(obj, want) {
		t.Errorf("got  %#v\nwant %#v", obj, want)
	}
}

func TestParseValueAnyType(t *testing.T) {
	for in, want := range map[string]any{
		`"x"`:   "x",
		` 2.50`: json.Number("2.50"),
		`null`:  nil,
		`[]`:    []any{},
	} {
		v, _, err := ParseValue([]byte(in))
		if err != nil || !reflect.DeepEqual(v, want) {
			t.Errorf("ParseValue(%q) = %#v, %v; want %#v", in, v, err, want)
		}
	}
}

// Nesting is read to maxDepth, and what is read can be written back, even
// inside the output envelope; one level deeper is rejected.
func TestParseDeepNesting(t *testing.T) {
	nested := func(depth int) []byte { // an object holding depth-1 nested arrays
		return append(append([]byte(`{"a":`), bytes.Repeat([]byte("["), depth-1)...), append(bytes.Repeat([]byte("]"), depth-1), '}')...)
	}
	obj, _, err := ParseObject(nested(maxDepth))
	if err != nil {
		t.Fatal(err)
	}
	for _, marshal := range []func(any) ([]byte, error){MarshalLine, MarshalFile} {
		if _, err := marshal(obj); err != nil {
			t.Fatal(err)
		}
	}
	envelope := &Object{Members: []Member{{"result", &Object{Members: []Member{{"tasks", []any{obj}}}}}}}
	if _, err := MarshalLine(envelope); err != nil {
		t.Fatalf("inside the envelope: %v", err)
	}
	if _, _, err := ParseObject(nested(maxDepth + 1)); failureOf(err) != FailSyntax {
		t.Fatalf("depth %d: %v, want a syntax failure", maxDepth+1, err)
	}
}

// A value read at depth d nests at most maxDepth-d levels, so wrapped in d
// levels (as --extra is at /extra) it reads back as a whole.
func TestParseValueAtDepthBudget(t *testing.T) {
	arrays := func(depth int) []byte {
		return append(bytes.Repeat([]byte("["), depth), bytes.Repeat([]byte("]"), depth)...)
	}
	v, _, err := ParseValueAt(arrays(maxDepth-1), 1)
	if err != nil {
		t.Fatal(err)
	}
	file, err := MarshalFile(&Object{Members: []Member{{"extra", v}}})
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := ParseObject(file); err != nil {
		t.Fatalf("written back: %v", err)
	}
	if _, _, err := ParseValueAt(arrays(maxDepth), 1); failureOf(err) != FailSyntax {
		t.Fatalf("depth %d at 1: %v, want a syntax failure", maxDepth, err)
	}
}

// Many repeats deep down are reported in time linear in their pointers.
func TestParseDeepRepeats(t *testing.T) {
	const depth, n = 2000, 2000
	data := []byte(strings.Repeat(`{"a":`, depth) + "[" + strings.Repeat(`{"k":1,"k":2},`, n) + "0]" + strings.Repeat("}", depth))
	_, repeated, err := ParseObject(data)
	if err != nil || len(repeated) != n {
		t.Fatalf("%d repeats, %v", len(repeated), err)
	}
	if want := strings.Repeat("/a", depth) + "/7/k"; repeated[7] != want {
		t.Errorf("repeated[7] = %.40q..., want %.40q...", repeated[7], want)
	}
}

// repeatsOf lists the repeated keys in a parsed tree, which keeps every
// member: an oracle for the reader's stream-based list.
func repeatsOf(v any, ptr string, out []string) []string {
	switch v := v.(type) {
	case *Object:
		seen, reported := map[string]bool{}, map[string]bool{}
		for _, m := range v.Members {
			if seen[m.Key] && !reported[m.Key] {
				reported[m.Key] = true
				out = append(out, Pointer(ptr, m.Key))
			}
			seen[m.Key] = true
			out = repeatsOf(m.Value, Pointer(ptr, m.Key), out)
		}
	case []any:
		for i, item := range v {
			out = repeatsOf(item, Pointer(ptr, strconv.Itoa(i)), out)
		}
	}
	return out
}

// depthOf is the deepest nesting of objects and arrays in valid JSON.
func depthOf(data []byte) int {
	depth, deepest, inString := 0, 0, false
	for i := 0; i < len(data); i++ {
		switch c := data[i]; {
		case inString && c == '\\':
			i++
		case c == '"':
			inString = !inString
		case inString:
		case c == '{' || c == '[':
			depth++
			deepest = max(deepest, depth)
		case c == '}' || c == ']':
			depth--
		}
	}
	return deepest
}

// hasLoneSurrogate reports whether valid JSON has a \u escape of a surrogate
// not paired with its other half: an oracle for loneSurrogate, walking the
// strings code unit by code unit.
func hasLoneSurrogate(data []byte) bool {
	inString, afterHigh := false, false
	for i := 0; i < len(data); i++ {
		c := data[i]
		if !inString {
			inString, afterHigh = c == '"', false
			continue
		}
		unit := rune(-1) // a code unit that is not a surrogate
		switch {
		case c == '"':
			if afterHigh {
				return true
			}
			inString = false
			continue
		case c == '\\' && data[i+1] == 'u':
			u, _ := strconv.ParseUint(string(data[i+2:i+6]), 16, 16)
			unit = rune(u)
			i += 5
		case c == '\\':
			i++
		}
		isHigh, isLow := utf16.IsSurrogate(unit) && unit < 0xDC00, utf16.IsSurrogate(unit) && unit >= 0xDC00
		if afterHigh != isLow {
			return true
		}
		afterHigh = isHigh
	}
	return false
}

// Differential check against the standard library's validator: the reader
// accepts exactly the valid UTF-8, BOM-free, single JSON objects without lone
// surrogates, nested at most maxDepth. Run with -fuzzminimizetime 1s: minimizing each new input
// otherwise stalls every worker for up to a minute, with no execs counted.
func FuzzParseObject(f *testing.F) {
	for _, s := range []string{
		`{}`, `{"a":1}`, `{"a":1,"a":2}`, `[]`, `{} {}`, `{"a":[1,{"b":null}]}`,
		"\xEF\xBB\xBF{}", "{\"a\":\"\xff\"}", `{"a":"\ud800"}`, `{"a":"\ud83d\ude00"}`, `{"\\ud800":"\udc00x"}`, ` `, `{"a":1e400}`, `{"a":-0.0e-0}`,
		`{"a":"\ud800\\udc00"}`, `{"a":"\\\ud800"}`,
	} {
		f.Add([]byte(s))
	}
	// Either side of the depth limit.
	f.Add(append(append([]byte(`{"a":`), bytes.Repeat([]byte("["), maxDepth-1)...), append(bytes.Repeat([]byte("]"), maxDepth-1), '}')...))
	f.Add(append(append([]byte(`{"a":`), bytes.Repeat([]byte("["), maxDepth)...), append(bytes.Repeat([]byte("]"), maxDepth), '}')...))
	f.Fuzz(func(t *testing.T, data []byte) {
		obj, repeated, err := ParseObject(data)
		trimmed := bytes.TrimLeft(data, " \t\r\n")
		acceptable := utf8.Valid(data) && !bytes.HasPrefix(data, bom) && json.Valid(data) &&
			len(trimmed) > 0 && trimmed[0] == '{' && !hasLoneSurrogate(data) && depthOf(data) <= maxDepth
		if (err == nil) != acceptable {
			t.Fatalf("ParseObject(%q): err %v, but acceptable=%v", data, err, acceptable)
		}
		if err != nil {
			return
		}
		if want := repeatsOf(obj, "", nil); !reflect.DeepEqual(repeated, want) {
			t.Fatalf("ParseObject(%q): repeated %q, want %q", data, repeated, want)
		}
		// What was read can be written and read back to the same tree,
		// repeats included.
		out, err := MarshalLine(obj)
		if err != nil {
			t.Fatalf("MarshalLine: %v", err)
		}
		again, repeatedAgain, err := ParseObject(out)
		if err != nil || !reflect.DeepEqual(again, obj) || !reflect.DeepEqual(repeatedAgain, repeated) {
			t.Fatalf("round trip of %q via %q: %v", data, out, err)
		}
	})
}
