package testrun

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"sync"

	"github.com/santhosh-tekuri/jsonschema/v6"
)

// base is where the schemas are said to live, so that each $id, such as
// "types", resolves against it and no schema is ever fetched.
const base = "https://koan.invalid/schemas/"

// Schemas is schemas/, ready to validate against.
type Schemas struct {
	compiler *jsonschema.Compiler
	docs     map[string]*Node

	mu       sync.Mutex
	compiled map[string]*jsonschema.Schema
}

// LoadDir reads every schema in dir.
func LoadDir(dir string) (*Schemas, error) {
	files, err := filepath.Glob(filepath.Join(dir, "*.json"))
	if err != nil {
		return nil, err
	}
	s := &Schemas{compiler: jsonschema.NewCompiler(), docs: map[string]*Node{}, compiled: map[string]*jsonschema.Schema{}}
	s.compiler.UseRegexpEngine(ecmaRegexp)
	for _, f := range files {
		data, err := os.ReadFile(f)
		if err != nil {
			return nil, err
		}
		doc, err := jsonschema.UnmarshalJSON(strings.NewReader(string(data)))
		if err != nil {
			return nil, fmt.Errorf("%s: %w", f, err)
		}
		node, err := Parse(string(data))
		if err != nil {
			return nil, fmt.Errorf("%s: %w", f, err)
		}
		id := node.Str("$id")
		if id+".json" != filepath.Base(f) {
			return nil, fmt.Errorf("%s: $id is %q", f, id)
		}
		if err := s.compiler.AddResource(base+id, doc); err != nil {
			return nil, fmt.Errorf("%s: %w", f, err)
		}
		s.docs[id] = node
	}
	return s, nil
}

// IDs is every schema's $id.
func (s *Schemas) IDs() []string {
	var ids []string
	for id := range s.docs {
		ids = append(ids, id)
	}
	return ids
}

// Doc is the schema with this $id, keys in order, or nil.
func (s *Schemas) Doc(id string) *Node { return s.docs[id] }

// Validate checks one JSON text against the schema at id and pointer,
// such as "task-create" and "/$defs/result".
func (s *Schemas) Validate(id, pointer, text string) error {
	sch, err := s.compile(id, pointer)
	if err != nil {
		return err
	}
	inst, err := jsonschema.UnmarshalJSON(strings.NewReader(text))
	if err != nil {
		return err
	}
	return sch.Validate(inst)
}

func (s *Schemas) compile(id, pointer string) (*jsonschema.Schema, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	url := base + id + "#" + pointer
	if sch := s.compiled[url]; sch != nil {
		return sch, nil
	}
	if s.docs[id] == nil {
		return nil, fmt.Errorf("no schema %q", id)
	}
	sch, err := s.compiler.Compile(url)
	if err != nil {
		return nil, err
	}
	s.compiled[url] = sch
	return sch, nil
}

// Order checks that every object in text lists its fields in the order
// of the schema's properties, following $ref, allOf, anyOf, oneOf and
// items into nested objects.
func (s *Schemas) Order(id, pointer, text string) error {
	v, err := Parse(text)
	if err != nil {
		return err
	}
	return s.order(id, s.docs[id].At(pointer), v, "")
}

func (s *Schemas) order(id string, sch, v *Node, at string) error {
	if sch == nil || v == nil {
		return nil
	}
	if ref := sch.Str("$ref"); ref != "" {
		rid, rptr := id, ref
		if !strings.HasPrefix(ref, "#") {
			rid, rptr, _ = strings.Cut(ref, "#")
		} else {
			rptr = ref[1:]
		}
		if doc := s.docs[rid]; doc != nil {
			if err := s.order(rid, doc.At(rptr), v, at); err != nil {
				return err
			}
		}
	}
	for _, k := range []string{"allOf", "anyOf", "oneOf"} {
		for _, branch := range sch.Get(k).List() {
			if err := s.order(id, branch, v, at); err != nil {
				return err
			}
		}
	}
	if props := sch.Get("properties"); props != nil && v.Keys != nil {
		pos := map[string]int{}
		for i, k := range props.Keys {
			pos[k] = i
		}
		last := -1
		for _, k := range v.Keys {
			p, ok := pos[k]
			if !ok {
				continue
			}
			if p < last {
				return fmt.Errorf("at %q: field %q is out of the schema's order %v", at, k, props.Keys)
			}
			last = p
			if err := s.order(id, props.Get(k), v.Get(k), at+"/"+k); err != nil {
				return err
			}
		}
	}
	if items := sch.Get("items"); items != nil {
		for i, item := range v.Items {
			if err := s.order(id, items, item, at+"/"+strconv.Itoa(i)); err != nil {
				return err
			}
		}
	}
	return nil
}

// ecmaRegexp compiles a schema's pattern, which is ECMA-262, with Go's
// regexp: the one form the schemas use that Go lacks is \uXXXX.
func ecmaRegexp(pattern string) (jsonschema.Regexp, error) {
	var b strings.Builder
	for i := 0; i < len(pattern); i++ {
		c := pattern[i]
		if c == '\\' && i+1 < len(pattern) {
			if pattern[i+1] == 'u' && i+6 <= len(pattern) {
				b.WriteString(`\x{` + pattern[i+2:i+6] + `}`)
				i += 5
				continue
			}
			b.WriteByte(c)
			i++
			c = pattern[i]
		}
		b.WriteByte(c)
	}
	return regexp.Compile(b.String())
}

// Node is a JSON value that keeps its object keys in order.
type Node struct {
	Keys   []string         // an object's keys in order; nil unless an object
	Fields map[string]*Node // an object's fields
	Items  []*Node          // an array's items; nil unless an array
	Value  any              // a scalar: string, json.Number, bool or nil
}

// Get is an object's field, or nil.
func (n *Node) Get(key string) *Node {
	if n == nil || n.Fields == nil {
		return nil
	}
	return n.Fields[key]
}

// List is an array's items; nil for anything else, nil included.
func (n *Node) List() []*Node {
	if n == nil {
		return nil
	}
	return n.Items
}

// Str is an object's string field, or "".
func (n *Node) Str(key string) string {
	if f := n.Get(key); f != nil {
		s, _ := f.Value.(string)
		return s
	}
	return ""
}

// At follows a JSON Pointer, or gives nil.
func (n *Node) At(pointer string) *Node {
	if pointer == "" {
		return n
	}
	for _, part := range strings.Split(strings.TrimPrefix(pointer, "/"), "/") {
		part = strings.NewReplacer("~1", "/", "~0", "~").Replace(part)
		if n == nil {
			return nil
		}
		if n.Items != nil {
			i, err := strconv.Atoi(part)
			if err != nil || i < 0 || i >= len(n.Items) {
				return nil
			}
			n = n.Items[i]
			continue
		}
		n = n.Get(part)
	}
	return n
}

// IsObject is whether the node is a JSON object.
func (n *Node) IsObject() bool { return n != nil && n.Fields != nil }

// Parse reads one JSON value, keeping key order.
func Parse(text string) (*Node, error) {
	dec := json.NewDecoder(strings.NewReader(text))
	dec.UseNumber()
	n, err := parse(dec)
	if err != nil {
		return nil, err
	}
	if _, err := dec.Token(); err != io.EOF {
		return nil, fmt.Errorf("more than one JSON value")
	}
	return n, nil
}

func parse(dec *json.Decoder) (*Node, error) {
	tok, err := dec.Token()
	if err != nil {
		return nil, err
	}
	switch tok {
	case json.Delim('{'):
		n := &Node{Keys: []string{}, Fields: map[string]*Node{}}
		for dec.More() {
			k, err := dec.Token()
			if err != nil {
				return nil, err
			}
			key := k.(string)
			v, err := parse(dec)
			if err != nil {
				return nil, err
			}
			if _, dup := n.Fields[key]; dup {
				return nil, fmt.Errorf("field %q twice", key)
			}
			n.Keys = append(n.Keys, key)
			n.Fields[key] = v
		}
		_, err := dec.Token()
		return n, err
	case json.Delim('['):
		n := &Node{Items: []*Node{}}
		for dec.More() {
			v, err := parse(dec)
			if err != nil {
				return nil, err
			}
			n.Items = append(n.Items, v)
		}
		_, err := dec.Token()
		return n, err
	}
	return &Node{Value: tok}, nil
}
