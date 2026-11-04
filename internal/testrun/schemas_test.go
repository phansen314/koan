package testrun

import (
	"path/filepath"
	"testing"
)

func TestOrder(t *testing.T) {
	s, err := LoadDir(filepath.Join(Repo(), "schemas"))
	if err != nil {
		t.Fatal(err)
	}
	good := `{"version":"(devel)","commit":null,"go":"go1.25","format":1}`
	if err := s.Order("version", "/$defs/result", good); err != nil {
		t.Errorf("good order refused: %v", err)
	}
	if err := s.Validate("version", "/$defs/result", good); err != nil {
		t.Errorf("good result refused: %v", err)
	}
	if err := s.Order("version", "/$defs/result", `{"commit":null,"version":"(devel)","go":"go1.25","format":1}`); err == nil {
		t.Error("bad order taken")
	}
	if err := s.Validate("version", "/$defs/result", `{"version":"(devel)","commit":null,"go":"go1.25","format":1,"x":1}`); err == nil {
		t.Error("extra field taken")
	}
	// Nested through the envelope's oneOf and the error's $ref.
	if err := s.Order("envelope", "", `{"ok":false,"error":{"rule":null,"kind":"io","message":"m","ids":[],"paths":[],"problems":[],"archived":[]},"warnings":[]}`); err == nil {
		t.Error("bad nested order taken")
	}
}

// extra as stored holds namespaces and no null at the top; a patch can
// hold nulls, and takes the same names.
func TestExtraSchemas(t *testing.T) {
	s := Load(t)
	for _, c := range []struct {
		def, text string
		good      bool
	}{
		{"extra", `{"jira":"WEB-12","rel":{"Any.Name":null},"a":[null],"n":1,"b":true}`, true},
		{"extra", `{}`, true},
		{"extra", `{"a":null}`, false},
		{"extra", `{"Bad":1}`, false},
		{"extra", `{"koan":{}}`, false},
		{"extra", `[]`, false},
		{"extra-patch", `{"a":null,"b":{"c":null}}`, true},
		{"extra-patch", `{"Bad":null}`, false},
		{"extra-patch", `{"koan":null}`, false},
		{"namespace", `"koan-x"`, true},
		{"namespace", `"koan"`, false},
		{"namespace", `"a.b"`, false},
	} {
		if err := s.Validate("types", "/$defs/"+c.def, c.text); (err == nil) != c.good {
			t.Errorf("%s %s: %v", c.def, c.text, err)
		}
	}
	if err := s.Validate("update", "/$defs/input", `{"refs":[1],"extra_replace_all":{}}`); err == nil {
		t.Error("extra_replace_all taken")
	}
	if err := s.Validate("update", "/$defs/input", `{"refs":[1],"extra_merge":{"a":null},"extra_remove":["b"]}`); err != nil {
		t.Errorf("a patch refused: %v", err)
	}
}

func TestECMARegexp(t *testing.T) {
	re, err := ecmaRegexp(`^[^\x00-\x1f  ]+$`)
	if err != nil {
		t.Fatal(err)
	}
	if !re.MatchString("Book flights") || re.MatchString("a b") || re.MatchString("a\nb") {
		t.Errorf("%s matches wrongly", re)
	}
	re, err = ecmaRegexp(`^\\u$`)
	if err != nil || !re.MatchString(`\u`) {
		t.Errorf("escaped backslash: %v", err)
	}
}

// Help and completion, and --help or -h anywhere before --, print text
// for people instead of an envelope.
func TestHelpLine(t *testing.T) {
	for _, c := range []struct {
		args []string
		want bool
	}{
		{[]string{"--help"}, true},
		{[]string{"-h"}, true},
		{[]string{"help"}, true},
		{[]string{"completion", "bash"}, true},
		{[]string{"__complete", "task", ""}, true},
		{[]string{"version", "--help"}, true},
		{[]string{"task", "--help"}, true},
		{[]string{"version", "x", "--help"}, true},
		{[]string{"task", "create", "x", "-h"}, true},
		{[]string{"task", "create", "--", "--help"}, false},
		{[]string{"version"}, false},
		{[]string{"task", "create", "help"}, false},
		{nil, false},
	} {
		if got := helpLine(c.args); got != c.want {
			t.Errorf("%q: %v", c.args, got)
		}
	}
}
