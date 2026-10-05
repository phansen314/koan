package jsonio

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strconv"
	"strings"
	"unicode/utf8"
)

// Failure is why bytes could not be read as one JSON value.
type Failure string

const (
	FailInvalidUTF8 Failure = "invalid-utf8"
	FailBOM         Failure = "bom"
	FailEmpty       Failure = "empty"
	FailSyntax      Failure = "syntax"
	FailNotObject   Failure = "not-object"
	FailTrailing    Failure = "trailing"
	// FailLoneSurrogate is a \u escape naming half of a UTF-16 surrogate pair
	// without the other half. The decoder would silently replace it with
	// U+FFFD, changing the string (and making distinct keys equal).
	FailLoneSurrogate Failure = "lone-surrogate"
)

// writeDepth is the standard library's nesting limit: json.Valid, and the
// encoder's checks of MarshalJSON output and of indented output, reject
// anything deeper.
const writeDepth = 10000

// maxDepth is the deepest nesting of objects and arrays read. It is below
// writeDepth so that whatever is read can be written back even inside the
// output envelope, which wraps a task in at most 3 more levels
// ({"result":{"tasks":[task]}}); the rest of the margin is spare.
const maxDepth = writeDepth - 10

// ReadError reports a Failure. Repeated keys are not a ReadError: they are
// returned alongside the tree, so a file can still be version-checked.
type ReadError struct {
	Failure Failure
	Detail  string
}

func (e *ReadError) Error() string {
	switch e.Failure {
	case FailInvalidUTF8:
		return "not valid UTF-8"
	case FailBOM:
		return "starts with a byte-order mark"
	case FailEmpty:
		return "empty"
	case FailNotObject:
		return "expected a JSON object"
	case FailTrailing:
		return "unexpected data after the JSON value"
	case FailLoneSurrogate:
		return "unpaired surrogate escape " + e.Detail
	default:
		return "not valid JSON: " + e.Detail
	}
}

var bom = []byte{0xEF, 0xBB, 0xBF}

// ParseObject reads data as exactly one JSON object; see ParseValue.
func ParseObject(data []byte) (*Object, []string, error) {
	v, repeated, err := ParseValue(data)
	if err != nil {
		return nil, nil, err
	}
	o, ok := v.(*Object)
	if !ok {
		return nil, nil, &ReadError{Failure: FailNotObject}
	}
	return o, repeated, nil
}

// ParseValue reads data as exactly one JSON value, optionally surrounded by
// whitespace, into a tree (see the package doc). repeated lists the JSON
// Pointer of each key repeated within an object, once per key and object, in
// document order; the tree keeps every member, repeats included.
func ParseValue(data []byte) (value any, repeated []string, err error) {
	return ParseValueAt(data, 0)
}

// ParseValueAt is ParseValue for a value that will sit depth levels deep in
// an input (e.g. 1 for --extra, at /extra), so the whole input stays within
// maxDepth: the value may nest only maxDepth-depth levels. Without the budget,
// a value read at the full limit would be written into a file its own reader
// then rejects.
func ParseValueAt(data []byte, depth int) (value any, repeated []string, err error) {
	// Checked before decoding: the decoder silently replaces invalid bytes
	// with U+FFFD, and yields no tokens for empty input.
	if !utf8.Valid(data) {
		return nil, nil, &ReadError{Failure: FailInvalidUTF8}
	}
	if bytes.HasPrefix(data, bom) {
		return nil, nil, &ReadError{Failure: FailBOM}
	}
	if len(bytes.TrimLeft(data, " \t\r\n")) == 0 {
		return nil, nil, &ReadError{Failure: FailEmpty}
	}

	dec := json.NewDecoder(bytes.NewReader(data))
	dec.UseNumber()
	b := builder{}
	for !b.done {
		tok, err := dec.Token()
		if err != nil {
			return nil, nil, syntaxError(err)
		}
		b.add(tok)
		if len(b.stack) > maxDepth-depth {
			return nil, nil, &ReadError{Failure: FailSyntax, Detail: fmt.Sprintf("nested deeper than %d (at byte %d)", maxDepth-depth, dec.InputOffset())}
		}
	}
	if _, err := dec.Token(); err != io.EOF {
		return nil, nil, &ReadError{Failure: FailTrailing}
	}
	if i, ok := loneSurrogate(data); ok {
		return nil, nil, &ReadError{Failure: FailLoneSurrogate, Detail: fmt.Sprintf("%s (at byte %d)", data[i:i+6], i)}
	}
	return b.root, b.repeated, nil
}

// loneSurrogate returns the offset of the first \u escape of a surrogate
// without its other half: a high surrogate not immediately followed by an
// escaped low one, or a low one not preceded by a high one. data must be
// valid JSON, where a backslash occurs only in a string, starting an escape,
// and a \u is always followed by four hex digits.
func loneSurrogate(data []byte) (int, bool) {
	for i := 0; ; {
		j := bytes.IndexByte(data[i:], '\\')
		if j < 0 {
			return 0, false
		}
		i += j
		if data[i+1] != 'u' {
			i += 2 // skips the escaped character, which may be a backslash
			continue
		}
		switch r := hex4(data[i+2:]); {
		case r >= 0xD800 && r < 0xDC00:
			if i+12 <= len(data) && data[i+6] == '\\' && data[i+7] == 'u' {
				if low := hex4(data[i+8:]); low >= 0xDC00 && low < 0xE000 {
					i += 12
					continue
				}
			}
			return i, true
		case r >= 0xDC00 && r < 0xE000:
			return i, true
		}
		i += 6
	}
}

// hex4 decodes the four hex digits at the start of b.
func hex4(b []byte) rune {
	var r rune
	for _, c := range b[:4] {
		r <<= 4
		switch {
		case c <= '9':
			r |= rune(c - '0')
		case c >= 'a':
			r |= rune(c - 'a' + 10)
		default:
			r |= rune(c - 'A' + 10)
		}
	}
	return r
}

func syntaxError(err error) *ReadError {
	if errors.Is(err, io.EOF) || errors.Is(err, io.ErrUnexpectedEOF) {
		return &ReadError{Failure: FailSyntax, Detail: "unexpected end of input"}
	}
	var se *json.SyntaxError
	if errors.As(err, &se) {
		return &ReadError{Failure: FailSyntax, Detail: fmt.Sprintf("%s (at byte %d)", se.Error(), se.Offset)}
	}
	return &ReadError{Failure: FailSyntax, Detail: err.Error()}
}

// frame is an open object or array. Built iteratively, so nesting depth is
// bounded by maxDepth, not the goroutine stack.
type frame struct {
	obj      *Object
	arr      []any
	parent   *frame
	token    string // this value's reference token in parent; built into a pointer only when needed
	ptr      string // this container's pointer, once built
	havePtr  bool
	key      string // the pending member's key, once read
	haveKey  bool
	seen     map[string]bool // allocated at the first key
	reported map[string]bool // allocated at the first repeat
}

type builder struct {
	stack    []*frame
	root     any
	done     bool
	repeated []string
}

func (b *builder) add(tok json.Token) {
	top := b.top()
	if top != nil && top.obj != nil && !top.haveKey {
		if tok == json.Delim('}') {
			b.close()
			return
		}
		key := tok.(string) // the decoder yields only strings in key position
		top.key, top.haveKey = key, true
		switch {
		case top.seen == nil:
			top.seen = map[string]bool{key: true}
		case !top.seen[key]:
			top.seen[key] = true
		case !top.reported[key]:
			if top.reported == nil {
				top.reported = map[string]bool{}
			}
			top.reported[key] = true
			b.repeated = append(b.repeated, Pointer(top.pointer(), key))
		}
		return
	}

	switch tok {
	case json.Delim('{'):
		b.stack = append(b.stack, &frame{obj: &Object{}, parent: b.top(), token: b.childToken()})
	case json.Delim('['):
		b.stack = append(b.stack, &frame{arr: []any{}, parent: b.top(), token: b.childToken()})
	case json.Delim(']'):
		b.close()
	default:
		b.put(tok) // string, json.Number, bool, or nil
	}
}

func (b *builder) top() *frame {
	if len(b.stack) == 0 {
		return nil
	}
	return b.stack[len(b.stack)-1]
}

// childToken is the reference token, in the open container, of the value
// about to be read.
func (b *builder) childToken() string {
	top := b.top()
	switch {
	case top == nil:
		return ""
	case top.obj != nil:
		return top.key
	default:
		return strconv.Itoa(len(top.arr))
	}
}

// pointer is the JSON Pointer of the container f, built once, in time
// linear in its length.
func (f *frame) pointer() string {
	if !f.havePtr {
		var tokens []string
		for g := f; g.parent != nil; g = g.parent {
			tokens = append(tokens, g.token)
		}
		var sb strings.Builder
		for i := len(tokens) - 1; i >= 0; i-- {
			sb.WriteString(Pointer("", tokens[i]))
		}
		f.ptr, f.havePtr = sb.String(), true
	}
	return f.ptr
}

func (b *builder) close() {
	f := b.stack[len(b.stack)-1]
	b.stack = b.stack[:len(b.stack)-1]
	if f.obj != nil {
		b.put(f.obj)
	} else {
		b.put(f.arr)
	}
}

// put places a complete value in the enclosing container, or as the root.
func (b *builder) put(v any) {
	top := b.top()
	switch {
	case top == nil:
		b.root, b.done = v, true
	case top.obj != nil:
		top.obj.Members = append(top.obj.Members, Member{Key: top.key, Value: v})
		top.key, top.haveKey = "", false
	default:
		top.arr = append(top.arr, v)
	}
}
