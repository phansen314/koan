package model

import (
	"encoding/json"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/phansen314/koan/internal/errs"
)

func TestTitleInput(t *testing.T) {
	for _, tc := range []struct {
		name, in, want string
		ok             bool
	}{
		{"plain", "Book flights", "Book flights", true},
		{"trimmed", "  Book flights\t\n", "Book flights", true},
		{"no-break space trimmed", "\u00a0Book\u00a0", "Book", true},
		{"ideographic space trimmed", "\u3000Book\u2003", "Book", true},
		{"inner whitespace kept", "Book  \u00a0 flights", "Book  \u00a0 flights", true},
		{"not normalized", "e\u0301", "e\u0301", true},
		{"zero-width joiner kept", "👩\u200d💻", "👩\u200d💻", true},
		{"200 code points", strings.Repeat("é", 200), strings.Repeat("é", 200), true},
		{"201 code points", strings.Repeat("é", 201), "", false},
		{"empty", "", "", false},
		{"only whitespace", " \u00a0\t", "", false},
		{"inner newline", "a\nb", "", false},
		{"inner tab", "a\tb", "", false},
		{"inner NEL", "a\u0085b", "", false},
		{"inner DEL", "a\u007fb", "", false},
		{"inner line separator", "a\u2028b", "", false},
		{"inner paragraph separator", "a\u2029b", "", false},
	} {
		var p Problems
		got, ok := p.TitleInput(tc.in, "/title")
		if ok != tc.ok || (ok && string(got) != tc.want) {
			t.Errorf("%s: got %q, %v; want %q, %v", tc.name, got, ok, tc.want, tc.ok)
		}
		if ok == p.Failed("/title") {
			t.Errorf("%s: ok %v but Failed %v", tc.name, ok, p.Failed("/title"))
		}
	}
}

func TestStoredTitle(t *testing.T) {
	for in, ok := range map[string]bool{
		"Book flights":   true,
		" Book":          false,
		"Book\u00a0":     false,
		"":               false,
		"a\u2028b":       false,
		"trailing\u205f": false,
	} {
		var p Problems
		if _, got := p.StoredTitle(in, "/title"); got != ok {
			t.Errorf("StoredTitle(%q) = %v, want %v", in, got, ok)
		}
	}
}

func TestNames(t *testing.T) {
	long := strings.Repeat("a", 64)
	for in, ok := range map[string]bool{
		"travel": true, "a": true, "0": true, "a-b": true, "a--b": true, long: true,
		long + "a": false, "": false, "-a": false, "a-": false, "a_b": false,
		"é": false, ".": false, "..": false, ".git": false, "a b": false,
	} {
		var p Problems
		if _, got := p.Tag(in, "/tags/0"); got != ok {
			t.Errorf("Tag(%q) = %v, want %v", in, got, ok)
		}
		if in == "" {
			continue // "/" is the root, checked below
		}
		var q Problems
		if _, got := q.FolderPath("/"+in, "/folder"); got != ok {
			t.Errorf("FolderPath(%q) = %v, want %v", "/"+in, got, ok)
		}
	}
	for in, ok := range map[string]bool{
		"/": true, "/proj/travel": true, "/a/b/c": true,
		"": false, "proj": false, "/proj/": false, "//": false, "/a//b": false,
		"/./a": false, "/a/..": false, "/Proj": true, "/A/b-C/D9": true, "/Proj-": false,
	} {
		var p Problems
		if _, got := p.FolderPath(in, "/folder"); got != ok {
			t.Errorf("FolderPath(%q) = %v, want %v", in, got, ok)
		}
	}
	// Tags are lowercase; folder names may use either case.
	for _, in := range []string{"A", "Proj", "pRoj", "a-B"} {
		var p, q Problems
		if _, ok := p.Tag(in, "/tags/0"); ok {
			t.Errorf("Tag(%q) accepted", in)
		}
		if _, ok := q.FolderPath("/"+in, "/folder"); !ok {
			t.Errorf("FolderPath(%q) refused: %v", "/"+in, q.List())
		}
	}
	if got := FolderPath("/proj/travel").Segments(); !reflect.DeepEqual(got, []string{"proj", "travel"}) {
		t.Errorf("Segments = %q", got)
	}
	if got := RootFolder.Segments(); got != nil {
		t.Errorf("root Segments = %q", got)
	}
	var p Problems
	if _, ok := p.Tag(json.Number("1"), "/tags/0"); ok || p.List()[0].Reason != reasonString {
		t.Errorf("a number tag: %v", p.List())
	}
}

func TestTimestamps(t *testing.T) {
	for in, ok := range map[string]bool{
		"2026-09-20T18:31:51Z":      true,
		"2024-02-29T00:00:00Z":      true,
		"2026-02-29T00:00:00Z":      false,
		"2026-13-01T00:00:00Z":      false,
		"2026-01-01T24:00:00Z":      false,
		"2016-12-31T23:59:60Z":      false,
		"2026-09-20T18:31:51":       false,
		"2026-09-20T18:31:51.5Z":    false,
		"2026-09-20T18:31:51+00:00": false,
		"2026-09-20 18:31:51Z":      false,
	} {
		var p Problems
		if _, got := p.Timestamp(in, "/created_at"); got != ok {
			t.Errorf("Timestamp(%q) = %v, want %v", in, got, ok)
		}
	}
	at := time.Date(2026, 9, 20, 20, 31, 51, 999_000_000, time.FixedZone("x", 2*3600))
	if got := TimestampOf(at); got != "2026-09-20T18:31:51Z" {
		t.Errorf("TimestampOf = %s", got)
	}
}

func TestIntegers(t *testing.T) {
	for _, tc := range []struct {
		in     any
		lo, hi int64
		ok     bool
		reason string
	}{
		{json.Number("42"), 1, IDMax, true, ""},
		{json.Number("999999999999999"), 1, IDMax, true, ""},
		{json.Number("1000000000000000"), 1, IDMax, false, "must be between 1 and 999999999999999"},
		{json.Number("0"), 1, IDMax, false, "must be between 1 and 999999999999999"},
		{json.Number("-0"), 0, 0, true, ""},
		{json.Number("2.0"), 1, IDMax, false, reasonLiteral},
		{json.Number("2e0"), 1, IDMax, false, reasonLiteral},
		{json.Number("12345678901234567890"), PriorityMin, PriorityMax, false, "must be between -9007199254740991 and 9007199254740991"},
		{json.Number("9007199254740991"), PriorityMin, PriorityMax, true, ""},
		{json.Number("9007199254740992"), PriorityMin, PriorityMax, false, "must be between -9007199254740991 and 9007199254740991"},
		{json.Number("-9007199254740991"), PriorityMin, PriorityMax, true, ""},
		{"42", 1, IDMax, false, reasonInteger},
		{nil, 1, IDMax, false, reasonInteger},
	} {
		var p Problems
		_, ok := p.Int(tc.in, "/x", tc.lo, tc.hi)
		if ok != tc.ok || (!ok && p.List()[0].Reason != tc.reason) {
			t.Errorf("Int(%v): ok %v, problems %v; want %v %q", tc.in, ok, p.List(), tc.ok, tc.reason)
		}
	}
}

func TestSets(t *testing.T) {
	var p Problems
	ids, ok := p.IDs([]any{json.Number("3"), json.Number("1"), json.Number("3"), "x", json.Number("1")}, "/blocked_by")
	if ok {
		t.Fatalf("IDs accepted %v", ids)
	}
	want := []errs.Problem{
		{Field: "/blocked_by/3", Reason: reasonInteger},
		{Field: "/blocked_by/2", Reason: "duplicate of item 0"},
		{Field: "/blocked_by/4", Reason: "duplicate of item 1"},
	}
	if !reflect.DeepEqual(p.List(), want) {
		t.Errorf("uniqueness is checked among the valid items: got %v", p.List())
	}

	p = Problems{}
	_, ok = p.Tags([]any{"b", "a", "b", "a", "b"}, "/tags")
	want = []errs.Problem{
		{Field: "/tags/2", Reason: "duplicate of item 0"},
		{Field: "/tags/3", Reason: "duplicate of item 1"},
		{Field: "/tags/4", Reason: "duplicate of item 0"},
	}
	if ok || !reflect.DeepEqual(p.List(), want) {
		t.Errorf("duplicates: got %v", p.List())
	}
}

func TestPriority(t *testing.T) {
	var p Problems
	if v, ok := p.Priority(nil, "/priority"); !ok || v != nil {
		t.Errorf("null: %v %v", v, ok)
	}
	if v, ok := p.Priority(json.Number("-3"), "/priority"); !ok || *v != -3 {
		t.Errorf("-3: %v %v", v, ok)
	}
	if _, ok := p.Priority("3", "/priority"); ok || p.List()[0].Reason != "expected an integer or null" {
		t.Errorf("string: %v", p.List())
	}
}
