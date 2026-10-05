// Command schemagen extracts every JSON Schema in the specs — each ```json
// block with a top-level $id — into schemas/<$id>.json, for the tests. The
// specs stay normative; main_test.go fails if schemas/ drifts from them.
package main

//go:generate go run . -root ../../.. -out ../../../schemas

import (
	"bufio"
	"bytes"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
)

// specs are the documents schemas are extracted from, relative to the root.
var specs = []string{"design-spec.md", "operations.md", "cli-spec.md", "implementation-spec.md", "pick-spec.md"}

func main() {
	root := flag.String("root", ".", "directory holding the specs")
	out := flag.String("out", "schemas", "directory to write the schemas to")
	flag.Parse()
	if err := run(*root, *out); err != nil {
		fmt.Fprintln(os.Stderr, "schemagen:", err)
		os.Exit(1)
	}
}

func run(root, out string) error {
	schemas, err := extract(root)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(out, 0o755); err != nil {
		return err
	}
	stale, err := filepath.Glob(filepath.Join(out, "*.json"))
	if err != nil {
		return err
	}
	for _, f := range stale {
		if err := os.Remove(f); err != nil {
			return err
		}
	}
	for id, body := range schemas {
		if err := os.WriteFile(filepath.Join(out, id+".json"), body, 0o644); err != nil {
			return err
		}
	}
	return nil
}

// extract returns each schema's text, verbatim, keyed by its $id.
func extract(root string) (map[string][]byte, error) {
	schemas := map[string][]byte{}
	where := map[string]string{}
	for _, spec := range specs {
		blocks, err := jsonBlocks(filepath.Join(root, spec))
		if err != nil {
			return nil, err
		}
		for _, b := range blocks {
			at := fmt.Sprintf("%s:%d", spec, b.line)
			var top map[string]json.RawMessage
			if err := json.Unmarshal(b.body, &top); err != nil {
				return nil, fmt.Errorf("%s: not a JSON object: %v", at, err)
			}
			raw, ok := top["$id"]
			if !ok {
				continue // an example, not a schema
			}
			var id string
			if err := json.Unmarshal(raw, &id); err != nil || id == "" || strings.ContainsAny(id, "/\\.") {
				return nil, fmt.Errorf("%s: $id must be a plain name, got %s", at, raw)
			}
			if prev, dup := where[id]; dup {
				return nil, fmt.Errorf("%s: $id %q already defined at %s", at, id, prev)
			}
			schemas[id], where[id] = b.body, at
		}
	}
	return schemas, nil
}

// idLine is a line declaring a schema's $id, which must be inside a block
// that jsonBlocks recognizes.
var idLine = regexp.MustCompile(`"\$id"\s*:`)

type block struct {
	line int // of the opening fence
	body []byte
}

// jsonBlocks returns the contents of every ```json fenced block in a file. An
// $id outside one — in a fence written another way, say indented — is an
// error, so no schema is silently left out.
func jsonBlocks(path string) ([]block, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	var blocks []block
	var cur *bytes.Buffer
	start := 0
	sc := bufio.NewScanner(f)
	sc.Buffer(nil, 1<<20)
	for n := 1; sc.Scan(); n++ {
		line := sc.Text()
		switch {
		case cur == nil && line == "```json":
			cur, start = &bytes.Buffer{}, n
		case cur != nil && line == "```":
			blocks = append(blocks, block{line: start, body: cur.Bytes()})
			cur = nil
		case cur != nil:
			cur.WriteString(line)
			cur.WriteByte('\n')
		case idLine.MatchString(line):
			return nil, fmt.Errorf("%s:%d: $id outside a ```json block", path, n)
		}
	}
	if err := sc.Err(); err != nil {
		return nil, err
	}
	if cur != nil {
		return nil, fmt.Errorf("%s:%d: unterminated ```json block", path, start)
	}
	return blocks, nil
}
