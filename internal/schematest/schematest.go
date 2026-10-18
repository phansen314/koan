// Package schematest compiles the JSON Schemas in schemas/ for tests, and
// checks values against them. It is imported only by tests, so the schema
// library never reaches the binary.
package schematest

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
	"sync"
	"testing"

	"github.com/santhosh-tekuri/jsonschema/v6"
)

// base is the fixed URI the schemas are loaded under; each $id resolves
// against it, so "task-file#/properties/id" finds task-file.
const base = "https://koan.invalid/schemas/"

var (
	once     sync.Once
	compiler *jsonschema.Compiler
	loadErr  error
	mu       sync.Mutex
	compiled = map[string]*jsonschema.Schema{}
)

// Dir returns the schemas/ directory.
func Dir() string {
	_, file, _, _ := runtime.Caller(0)
	return filepath.Join(filepath.Dir(file), "..", "..", "schemas")
}

func load() {
	compiler = jsonschema.NewCompiler()
	compiler.UseRegexpEngine(goRegexp)
	files, err := filepath.Glob(filepath.Join(Dir(), "*.json"))
	if err != nil {
		loadErr = err
		return
	}
	for _, f := range files {
		data, err := os.ReadFile(f)
		if err != nil {
			loadErr = err
			return
		}
		doc, err := jsonschema.UnmarshalJSON(bytes.NewReader(data))
		if err != nil {
			loadErr = err
			return
		}
		id := strings.TrimSuffix(filepath.Base(f), ".json")
		if err := compiler.AddResource(base+id, doc); err != nil {
			loadErr = err
			return
		}
	}
}

// unicodeEscape is the \uXXXX escape of the schemas' regular expressions,
// which Go's regexp package spells \x{XXXX}.
var unicodeEscape = regexp.MustCompile(`\\u([0-9A-Fa-f]{4})`)

type goRE struct{ *regexp.Regexp }

func goRegexp(s string) (jsonschema.Regexp, error) {
	re, err := regexp.Compile(unicodeEscape.ReplaceAllString(s, `\x{$1}`))
	if err != nil {
		return nil, err
	}
	return goRE{re}, nil
}

// Schema returns the compiled schema with the given $id.
func Schema(t testing.TB, id string) *jsonschema.Schema {
	t.Helper()
	once.Do(load)
	if loadErr != nil {
		t.Fatal(loadErr)
	}
	mu.Lock()
	defer mu.Unlock()
	if s, ok := compiled[id]; ok {
		return s
	}
	s, err := compiler.Compile(base + id)
	if err != nil {
		t.Fatalf("schema %s: %v", id, err)
	}
	compiled[id] = s
	return s
}

// Check reports how the JSON in data fails the schema id, nil when it
// passes.
func Check(t testing.TB, id string, data []byte) error {
	t.Helper()
	v, err := jsonschema.UnmarshalJSON(bytes.NewReader(data))
	if err != nil {
		return err
	}
	return Schema(t, id).Validate(v)
}

// Must fails the test when v, encoded as JSON, does not pass the schema id.
func Must(t testing.TB, id string, v any) {
	t.Helper()
	data, err := json.Marshal(v)
	if err != nil {
		t.Fatalf("encode for schema %s: %v", id, err)
	}
	if err := Check(t, id, data); err != nil {
		t.Errorf("does not match schema %s: %v\n%s", id, err, data)
	}
}
