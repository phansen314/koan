package model

import (
	"encoding/json"
	"slices"
	"strconv"
	"strings"
	"testing"

	"github.com/phansen314/koan/internal/jsonio"
	"github.com/phansen314/koan/internal/schematest"
)

// verdict is an adapter's outcome for one document. Early marks a file
// rejected at File validity steps 1–2 (its schema field), which stops every
// later check, so only that field is compared.
type verdict struct {
	ok    bool
	at    []string
	early bool
}

// agree runs doc through the schema library and the adapter: both must
// accept, or both reject at exactly the same fields (Failure.Matches).
func agree(t *testing.T, schemaID, doc string, adapter func(string) verdict) {
	t.Helper()
	libOK, lib := schematest.Check(t, schemaID, []byte(doc))
	v := adapter(doc)
	switch {
	case libOK != v.ok:
		t.Errorf("%s: schema accepts %v (%s), adapter accepts %v (%q)\n  %s", schemaID, libOK, lib, v.ok, v.at, doc)
	case v.early && !slices.Contains(lib.Fields, "/schema"):
		t.Errorf("%s: adapter stops at /schema, schema rejects at %s\n  %s", schemaID, lib, doc)
	case !libOK && !v.early && !lib.Matches(v.at):
		t.Errorf("%s: schema rejects at %s, adapter at %q\n  %s", schemaID, lib, v.at, doc)
	}
}

// fileVerdict is where a file adapter rejected, by the file's schema alone:
// a file failing step 1 or 2 fails at its schema field; one failing only
// rules beyond the schema (SchemaProblems empty) is, by the schema, valid.
func fileVerdict(r FileResult) verdict {
	switch {
	case r.Status == FileUnsupported || r.Status == FileCorrupt && !r.Versioned:
		return verdict{at: []string{"/schema"}, early: true}
	case len(r.SchemaProblems) == 0:
		return verdict{ok: true}
	}
	var at []string
	for _, p := range r.SchemaProblems {
		at = append(at, p.Field)
	}
	return verdict{at: at}
}

func taskFileAdapter(t *testing.T) func(string) verdict {
	return func(doc string) verdict {
		obj, repeated, err := jsonio.ParseObject([]byte(doc))
		if err != nil {
			t.Fatalf("parse %s: %v", doc, err)
		}
		// The filename's ID is the file's own, when it has a usable one:
		// a mismatch is outside what the schema expresses.
		filenameID := ID(1)
		if n, ok := obj.Get("id"); ok {
			if i, err := strconv.ParseInt(string(asNumber(n)), 10, 64); err == nil {
				filenameID = ID(i)
			}
		}
		_, r := DecodeTaskFile(obj, repeated, filenameID)
		return fileVerdict(r)
	}
}

func asNumber(v any) json.Number {
	n, _ := v.(json.Number)
	return n
}

func TestTaskFileAgreesWithSchema(t *testing.T) {
	adapter := taskFileAdapter(t)
	completed := exampleTask
	for _, r := range [][2]string{
		{`"completed_at": null`, `"completed_at": "2026-09-21T08:00:00Z"`},
		{`"blocked_by": []`, `"blocked_by": [3, 7]`},
		{`"priority": 2`, `"priority": null`},
	} {
		if !strings.Contains(completed, r[0]) {
			t.Fatalf("exampleTask has no %s to replace", r[0])
		}
		completed = strings.Replace(completed, r[0], r[1], 1)
	}
	docs := append(schematest.Mutations(t, exampleTask), schematest.Mutations(t, completed)...)
	docs = append(docs,
		`{}`,
		`{"schema": 1}`,
		`{"schema": 2, "id": 1}`,
		`{"schema": "1", "id": 1}`,
	)
	for _, doc := range docs {
		agree(t, "task-file", doc, adapter)
	}
	t.Logf("%d documents", len(docs))
}

func TestRootFileAgreesWithSchema(t *testing.T) {
	adapter := func(doc string) verdict {
		obj, repeated, err := jsonio.ParseObject([]byte(doc))
		if err != nil {
			t.Fatalf("parse %s: %v", doc, err)
		}
		_, r := DecodeRootFile(obj, repeated)
		return fileVerdict(r)
	}
	docs := append(schematest.Mutations(t, `{"schema": 1, "last_id": 42}`), `{}`, `{"last_id": 0}`)
	for _, doc := range docs {
		agree(t, "root-file", doc, adapter)
	}
	t.Logf("%d documents", len(docs))
}
