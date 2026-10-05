package main

import (
	"bytes"
	"os"
	"path/filepath"
	"slices"
	"testing"
)

// schemas/ must be exactly what the specs say: rerun `go generate ./...`
// after editing a schema in a spec.
func TestSchemasMatchSpecs(t *testing.T) {
	want, err := extract("../../..")
	if err != nil {
		t.Fatal(err)
	}
	files, err := filepath.Glob("../../../schemas/*.json")
	if err != nil {
		t.Fatal(err)
	}
	var have []string
	for _, f := range files {
		id := filepath.Base(f[:len(f)-len(".json")])
		have = append(have, id)
		got, err := os.ReadFile(f)
		if err != nil {
			t.Fatal(err)
		}
		if body, ok := want[id]; !ok {
			t.Errorf("schemas/%s.json is in no spec: run go generate ./...", id)
		} else if !bytes.Equal(got, body) {
			t.Errorf("schemas/%s.json differs from the spec: run go generate ./...", id)
		}
	}
	for id := range want {
		if !slices.Contains(have, id) {
			t.Errorf("schema %s is missing from schemas/: run go generate ./...", id)
		}
	}
}

func TestJSONBlocks(t *testing.T) {
	for _, tc := range []struct {
		name, spec string
		want       []string // block bodies, or nil for an error
	}{
		{"blocks", "text\n```json\n{\"$id\": \"a\"}\n```\n```sh\nfor id; do x \"$id\"; done\n```\n", []string{"{\"$id\": \"a\"}\n"}},
		{"indented fence", "- item\n  ```json\n  {\n    \"$id\": \"a\"\n  }\n  ```\n", nil},
		{"other fence", "```jsonc\n{\"$id\": \"a\"}\n```\n", nil},
		{"unterminated", "```json\n{}\n", nil},
	} {
		path := filepath.Join(t.TempDir(), "spec.md")
		if err := os.WriteFile(path, []byte(tc.spec), 0o644); err != nil {
			t.Fatal(err)
		}
		blocks, err := jsonBlocks(path)
		if tc.want == nil {
			if err == nil {
				t.Errorf("%s: no error", tc.name)
			}
			continue
		}
		if err != nil {
			t.Fatalf("%s: %v", tc.name, err)
		}
		var got []string
		for _, b := range blocks {
			got = append(got, string(b.body))
		}
		if !slices.Equal(got, tc.want) {
			t.Errorf("%s: blocks %q, want %q", tc.name, got, tc.want)
		}
	}
}
