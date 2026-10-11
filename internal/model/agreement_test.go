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
	docs := append(schematest.Mutations(t, `{"schema": 2, "migration": 1}`), `{}`, `{"migration": 0}`, `{"schema": 2, "last_id": 4, "migration": 0}`)
	for _, doc := range docs {
		agree(t, "root-file", doc, adapter)
	}
	t.Logf("%d documents", len(docs))
}

func TestStateFileAgreesWithSchema(t *testing.T) {
	adapter := func(doc string) verdict {
		obj, repeated, err := jsonio.ParseObject([]byte(doc))
		if err != nil {
			t.Fatalf("parse %s: %v", doc, err)
		}
		_, r := DecodeStateFile(obj, repeated)
		return fileVerdict(r)
	}
	docs := append(schematest.Mutations(t, `{"schema": 1, "root": "/home/u/tasks", "last_id": 42}`), `{}`, `{"root": "/r", "last_id": 0}`, `{"schema": 1, "root": "", "last_id": 0}`)
	for _, doc := range docs {
		agree(t, "state-file", doc, adapter)
	}
	t.Logf("%d documents", len(docs))
}

func TestClaimsFileAgreesWithSchema(t *testing.T) {
	adapter := func(doc string) verdict {
		obj, repeated, err := jsonio.ParseObject([]byte(doc))
		if err != nil {
			t.Fatalf("parse %s: %v", doc, err)
		}
		_, r := DecodeClaimsFile(obj, repeated)
		return fileVerdict(r)
	}
	base := `{"schema": 1, "root": "/home/u/tasks", "claims": {"42": {"claimed_at": "2026-10-10T14:02:11Z", "extra": {"agent": "a", "n": 1}}, "57": {"claimed_at": "2026-10-10T14:05:40Z", "extra": {}, "token": "3f9c0a7e5b1d48c2a6e4f0b9d7c3a185"}}}`
	docs := append(schematest.Mutations(t, base),
		`{}`,
		`{"schema": 1, "root": "/r", "claims": {}}`,
		`{"schema": 1, "root": "", "claims": {}}`,
		`{"schema": 1, "root": "/r", "claims": []}`,
		`{"schema": 1, "root": "/r", "claims": {"042": {"claimed_at": "2026-10-10T14:02:11Z", "extra": {}}}}`,
		`{"schema": 1, "root": "/r", "claims": {"0": {"claimed_at": "2026-10-10T14:02:11Z", "extra": {}}}}`,
		`{"schema": 1, "root": "/r", "claims": {"x": 1}}`,
		`{"schema": 1, "root": "/r", "claims": {"1": {"claimed_at": "2026-10-10T14:02:11Z", "extra": {}, "token": "XYZ"}}}`,
		`{"schema": 1, "root": "/r", "claims": {"1": {"claimed_at": "2026-10-10T14:02:11Z", "extra": {}, "token": ""}}}`,
		`{"schema": 1, "root": "/r", "claims": {"1": {"claimed_at": "2026-10-10T14:02:11Z"}}}`,
		`{"schema": 1, "root": "/r", "claims": {"1": {"claimed_at": "2026-10-10T14:02:11Z", "extra": {}, "holder": "a"}}}`,
		`{"schema": 2, "root": "/r", "claims": {}}`,
	)
	for _, doc := range docs {
		agree(t, "claims-file", doc, adapter)
	}
	t.Logf("%d documents", len(docs))
}

// The claims file is written in koan's file format: entries in ascending
// order of ID, each extra's keys and numbers as given, a token only when the
// claim has one.
func TestClaimsFileEncode(t *testing.T) {
	extra, _, err := jsonio.ParseObject([]byte(`{"z": 1.50, "a": {"k": [true]}}`))
	if err != nil {
		t.Fatal(err)
	}
	c := ClaimsFile{Schema: ClaimsSchema, Root: "/home/u/tasks", Claims: map[ID]ClaimEntry{
		100: {ClaimedAt: "2026-10-10T14:05:40Z", Token: "3f9c0a7e5b1d48c2a6e4f0b9d7c3a185"},
		9:   {ClaimedAt: "2026-10-10T14:02:11Z", Extra: extra},
	}}
	got, err := c.Encode()
	if err != nil {
		t.Fatal(err)
	}
	want := "{\n  \"schema\": 1,\n  \"root\": \"/home/u/tasks\",\n  \"claims\": {\n    \"9\": {\n      \"claimed_at\": \"2026-10-10T14:02:11Z\",\n      \"extra\": {\n        \"z\": 1.50,\n        \"a\": {\n          \"k\": [\n            true\n          ]\n        }\n      }\n    },\n    \"100\": {\n      \"claimed_at\": \"2026-10-10T14:05:40Z\",\n      \"extra\": {},\n      \"token\": \"3f9c0a7e5b1d48c2a6e4f0b9d7c3a185\"\n    }\n  }\n}\n"
	if string(got) != want {
		t.Errorf("got\n%s\nwant\n%s", got, want)
	}
	obj, repeated, err := jsonio.ParseObject(got)
	if err != nil {
		t.Fatal(err)
	}
	back, r := DecodeClaimsFile(obj, repeated)
	if r.Status != FileOK || len(back.Claims) != 2 || back.Claims[100].Token != c.Claims[100].Token {
		t.Errorf("read back: %+v %+v", back, r)
	}
	if again, _ := back.Encode(); string(again) != want {
		t.Errorf("a rewrite changed the file:\n%s", again)
	}
	if c, _ := (ClaimsFile{Schema: ClaimsSchema, Root: "/r"}).Encode(); string(c) != "{\n  \"schema\": 1,\n  \"root\": \"/r\",\n  \"claims\": {}\n}\n" {
		t.Errorf("empty: %q", c)
	}
}
