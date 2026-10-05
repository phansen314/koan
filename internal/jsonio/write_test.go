package jsonio

import (
	"testing"
)

// taskFile mirrors the task file schema's key order, standing in for the
// model's type (a later step) so the File format rules are pinned here.
type taskFile struct {
	Schema      int64    `json:"schema"`
	ID          int64    `json:"id"`
	Title       string   `json:"title"`
	Priority    *int64   `json:"priority"`
	CreatedAt   string   `json:"created_at"`
	CompletedAt *string  `json:"completed_at"`
	BlockedBy   []int64  `json:"blocked_by"`
	Tags        []string `json:"tags"`
	Extra       *Object  `json:"extra"`
}

func mustParse(t *testing.T, s string) *Object {
	t.Helper()
	o, repeated, err := ParseObject([]byte(s))
	if err != nil || len(repeated) > 0 {
		t.Fatalf("parse %s: %v %v", s, err, repeated)
	}
	return o
}

// Exact bytes of a task file covering every File format rule: key order,
// nested extra in given order, empty {} and [], sets written as given
// (sorted by the caller), raw <>&/ and non-ASCII and U+007F–U+009F, escaped
// controls and U+2028/U+2029, and extra numbers unchanged.
func TestMarshalFileTaskBytes(t *testing.T) {
	extra := mustParse(t, `{"zeta":1.10,"alpha":{"b":-0,"a":[]},"big":12345678901234567890,"huge":1e400,"empty":{},"list":[1,"x",null,true],"s":"<a href=\"/x\">&amp;</a>"}`)
	prio := int64(2)
	tf := taskFile{
		Schema:    1,
		ID:        42,
		Title:     "Book flights → Zürich 🛫 <&> a/b \u007f\u0085 tab\there line\u2028para\u2029",
		Priority:  &prio,
		CreatedAt: "2026-09-20T18:31:51Z",
		BlockedBy: []int64{7, 41},
		Tags:      []string{},
		Extra:     extra,
	}
	got, err := MarshalFile(tf)
	if err != nil {
		t.Fatal(err)
	}
	want := `{
  "schema": 1,
  "id": 42,
  "title": "Book flights → Zürich 🛫 <&> a/b ` + "\u007f\u0085" + ` tab\there line\u2028para\u2029",
  "priority": 2,
  "created_at": "2026-09-20T18:31:51Z",
  "completed_at": null,
  "blocked_by": [
    7,
    41
  ],
  "tags": [],
  "extra": {
    "zeta": 1.10,
    "alpha": {
      "b": -0,
      "a": []
    },
    "big": 12345678901234567890,
    "huge": 1e400,
    "empty": {},
    "list": [
      1,
      "x",
      null,
      true
    ],
    "s": "<a href=\"/x\">&amp;</a>"
  }
}
`
	if string(got) != want {
		t.Errorf("got:\n%s\nwant:\n%s", got, want)
	}
}

func TestMarshalFileEmptyExtra(t *testing.T) {
	got, err := MarshalFile(struct {
		Extra *Object `json:"extra"`
	}{&Object{}})
	if err != nil {
		t.Fatal(err)
	}
	if want := "{\n  \"extra\": {}\n}\n"; string(got) != want {
		t.Errorf("got %q, want %q", got, want)
	}
}

func TestMarshalLine(t *testing.T) {
	v := struct {
		OK     bool    `json:"ok"`
		Result *Object `json:"result"`
	}{true, mustParse(t, `{"b": 1, "a": ["<x>", "\u2028"]}`)}
	got, err := MarshalLine(v)
	if err != nil {
		t.Fatal(err)
	}
	if want := `{"ok":true,"result":{"b":1,"a":["<x>","\u2028"]}}` + "\n"; string(got) != want {
		t.Errorf("got %q, want %q", got, want)
	}
}

// Numbers in extra round-trip character for character.
func TestNumbersRoundTrip(t *testing.T) {
	in := `{"a":1.10,"b":-0,"c":1e400,"d":12345678901234567890,"e":-0.0E+00,"f":9007199254740993}`
	got, err := MarshalLine(mustParse(t, in))
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != in+"\n" {
		t.Errorf("got %s, want %s", got, in)
	}
}

func TestObjectSetDelete(t *testing.T) {
	o := mustParse(t, `{"status":"a","owner":"me","n":1}`)
	o.Set("owner", "you") // existing key keeps its position
	o.Set("new1", 1.5)
	o.Set("new2", nil) // new keys appended in the order given
	o.Delete("status")
	o.Delete("absent")
	got, err := MarshalLine(o)
	if err != nil {
		t.Fatal(err)
	}
	if want := `{"owner":"you","n":1,"new1":1.5,"new2":null}` + "\n"; string(got) != want {
		t.Errorf("got %s, want %s", got, want)
	}
	if o.Len() != 4 {
		t.Errorf("Len %d", o.Len())
	}
}

// Set and Delete leave a shallow copy's members as they were, even when the
// members slice has spare capacity.
func TestObjectSetDeleteKeepCopy(t *testing.T) {
	members := make([]Member, 3, 8)
	copy(members, []Member{{"a", "1"}, {"b", "2"}, {"c", "3"}})
	o := &Object{Members: members}
	for _, change := range []func(){
		func() { o.Delete("a") },
		func() { o.Set("b", "x") },
		func() { o.Set("d", "4") },
	} {
		old := *o
		want, err := MarshalLine(old)
		if err != nil {
			t.Fatal(err)
		}
		change()
		got, err := MarshalLine(old)
		if err != nil {
			t.Fatal(err)
		}
		if string(got) != string(want) {
			t.Errorf("copy changed from %s to %s", want, got)
		}
	}
	if got, _ := MarshalLine(o); string(got) != `{"b":"x","c":"3","d":"4"}`+"\n" {
		t.Errorf("got %s", got)
	}
}

func TestPointer(t *testing.T) {
	for _, tc := range [][3]string{
		{"", "a", "/a"},
		{"/extra", "status", "/extra/status"},
		{"", "a/b", "/a~1b"},
		{"", "~1", "/~01"},
		{"", "", "/"},
	} {
		if got := Pointer(tc[0], tc[1]); got != tc[2] {
			t.Errorf("Pointer(%q, %q) = %q, want %q", tc[0], tc[1], got, tc[2])
		}
	}
}

// An Object stored by value encodes like a *Object; a nil *Object is null.
func TestMarshalObjectValue(t *testing.T) {
	o := &Object{Members: []Member{{"v", Object{Members: []Member{{"k", 1}}}}, {"nil", (*Object)(nil)}, {"e", Object{}}}}
	got, err := MarshalLine(o)
	if err != nil {
		t.Fatal(err)
	}
	if want := `{"v":{"k":1},"nil":null,"e":{}}` + "\n"; string(got) != want {
		t.Errorf("got %s, want %s", got, want)
	}
}
