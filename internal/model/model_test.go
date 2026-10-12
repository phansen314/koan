package model

import (
	"strings"
	"testing"

	"github.com/phansen314/koan/internal/schematest"
)

func TestFolders(t *testing.T) {
	for _, s := range []string{"/", "/proj", "/Proj/travel-2", "/a/b/c", "/" + strings.Repeat("a", 64)} {
		if _, ok := ParseFolder(s); !ok {
			t.Errorf("%q refused", s)
		}
		if err := schematest.Check(t, "folder-path", []byte(`"`+s+`"`)); err != nil {
			t.Errorf("%q: schema: %v", s, err)
		}
	}
	for _, s := range []string{"", "proj", "/proj/", "//", "/a//b", "/.", "/..", "/a b", "/-a", "/a-", "/a_b", "/" + strings.Repeat("a", 65)} {
		if _, ok := ParseFolder(s); ok {
			t.Errorf("%q accepted", s)
		}
		if err := schematest.Check(t, "folder-path", []byte(`"`+s+`"`)); err == nil {
			t.Errorf("%q: the schema accepts it", s)
		}
	}
	f := Folder("/proj/travel")
	if f.Rel() != "proj/travel" || Root.Rel() != "." || f.Parent() != "/proj" || Folder("/proj").Parent() != Root {
		t.Error("Rel or Parent")
	}
	if !Folder("/proj").Holds(f) || Folder("/pro").Holds(f) || !Root.Holds(f) || !f.Holds(f) || Folder("/proj").Holds("/proj-b") {
		t.Error("Holds")
	}
}

func TestNames(t *testing.T) {
	if !ValidTag("a-1") || ValidTag("A") || ValidTag("") || ValidTag("a b") {
		t.Error("ValidTag")
	}
	for name, want := range map[string]ID{"42.json": 42, "42.md": 42, "999999999999999.json": 999999999999999} {
		if id, _, ok := TaskFilename(name); !ok || id != want {
			t.Errorf("%s: %d %v", name, id, ok)
		}
	}
	for _, name := range []string{"042.json", "0.json", "42.txt", "42", "a.json", "1000000000000000.json", "42.json.bak"} {
		if _, _, ok := TaskFilename(name); ok {
			t.Errorf("%s accepted", name)
		}
	}
}

func TestTitles(t *testing.T) {
	if got, reason := CleanTitle("  \tBook flights\u00a0\n"); got != "Book flights" || reason != "" {
		t.Errorf("%q %q", got, reason)
	}
	for _, s := range []string{"", "   ", "a\nb", "a\tb", "a\u2028b", strings.Repeat("x", 201)} {
		if _, reason := CleanTitle(s); reason == "" {
			t.Errorf("%q accepted", s)
		}
	}
	if _, reason := CleanTitle(strings.Repeat("é", 200)); reason != "" {
		t.Errorf("200 characters refused: %s", reason)
	}
}

func TestTimestamps(t *testing.T) {
	if !ValidStamp("2026-10-10T14:02:11Z") {
		t.Error("a timestamp refused")
	}
	for _, s := range []string{"2026-10-10T14:02:11", "2026-10-10 14:02:11Z", "2026-13-10T14:02:11Z", "2026-10-10T14:02:11.5Z", "2026-10-10T14:02:11+01:00"} {
		if ValidStamp(s) {
			t.Errorf("%q accepted", s)
		}
	}
}

const goodTask = `{"schema":1,"id":42,"title":"t","priority":null,"created_at":"2026-10-10T14:02:11Z",
"completed_at":null,"updated_at":"2026-10-10T14:02:11Z","blocked_by":[17],"tags":["a"],"extra":{}}`

// The task file decoder and the task-file schema must agree on what a valid
// file is.
func TestTaskFileAgreesWithSchema(t *testing.T) {
	cases := map[string]string{
		"":                                    "",
		`"id":42`:                             `"id":0`,
		`"title":"t"`:                         `"title":" t"`,
		`"priority":null`:                     `"priority":1.5`,
		`"completed_at":null`:                 `"completed_at":"yesterday"`,
		`"blocked_by":[17]`:                   `"blocked_by":[17,17]`,
		`"tags":["a"]`:                        `"tags":["A"]`,
		`"extra":{}`:                          `"extra":[]`,
		`,"extra":{}`:                         ``,
		`"extra":{}}`:                         `"extra":{},"more":1}`,
		`"created_at":"2026-10-10T14:02:11Z"`: `"created_at":null`,
	}
	for old, new := range cases {
		data := []byte(strings.Replace(goodTask, old, new, 1))
		_, problems, err := DecodeTaskFile(data)
		usable := err == nil && len(problems) == 0
		schemaErr := schematest.Check(t, "task-file", data)
		if usable != (schemaErr == nil) || usable != (old == "") {
			t.Errorf("%s -> %s: decoder usable=%v (%v %v), schema says %v", old, new, usable, problems, err, schemaErr)
		}
	}
	// One rule the schema can't state: a task never waits on itself.
	if _, problems, _ := DecodeTaskFile([]byte(strings.Replace(goodTask, "[17]", "[42]", 1))); len(problems) != 1 {
		t.Errorf("own ID in blocked_by: %v", problems)
	}
}

func TestEncode(t *testing.T) {
	tf, _, _ := DecodeTaskFile([]byte(strings.Replace(goodTask, `"extra":{}`, `"extra":{"b":"<&>","a":1.50}`, 1)))
	tf.Normalize()
	data, err := Encode(tf)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"{\n  \"schema\": 1,\n  \"id\": 42,", "\"a\": 1.50,\n    \"b\": \"<&>\"", "}\n"} {
		if !strings.Contains(string(data), want) {
			t.Errorf("lacks %q:\n%s", want, data)
		}
	}
}
