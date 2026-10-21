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

// Only --help alone, or after a command, prints usage text instead of an
// envelope.
func TestHelpLine(t *testing.T) {
	for _, c := range []struct {
		args []string
		want bool
	}{
		{[]string{"--help"}, true},
		{[]string{"version", "--help"}, true},
		{[]string{"task", "create", "--help"}, true},
		{[]string{"task", "--help"}, false},
		{[]string{"version", "x", "--help"}, false},
		{[]string{"task", "create", "x", "--help"}, false},
		{[]string{"info", "--contents", "--help"}, false},
		{[]string{"version"}, false},
		{[]string{"nope", "--help"}, false},
		{nil, false},
	} {
		if got := helpLine(c.args); got != c.want {
			t.Errorf("%q: %v", c.args, got)
		}
	}
}
