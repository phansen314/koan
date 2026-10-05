package store

import (
	"testing"
)

func TestLocate(t *testing.T) {
	for _, tc := range []struct {
		goos, home, xdg   string
		wantHome, wantDir string
	}{
		{"linux", "/h", "/x", "/h", "/x/koan"},
		{"linux", "/h", "", "/h", "/h/.config/koan"},
		{"linux", "/h", "rel", "/h", "/h/.config/koan"}, // a relative XDG_CONFIG_HOME is ignored
		{"linux", "", "/x", "", "/x/koan"},
		{"linux", "rel", "", "", ""}, // a relative HOME is unusable
		{"linux", "", "", "", ""},
		{"darwin", "/h", "/x", "/h", "/h/Library/Application Support/koan"},
		{"darwin", "", "/x", "", ""},
	} {
		env := map[string]string{"HOME": tc.home, "XDG_CONFIG_HOME": tc.xdg}
		home, dir := Locate(func(k string) string { return env[k] }, tc.goos)
		if home != tc.wantHome || dir != tc.wantDir {
			t.Errorf("%s HOME=%q XDG_CONFIG_HOME=%q: got %q, %q; want %q, %q", tc.goos, tc.home, tc.xdg, home, dir, tc.wantHome, tc.wantDir)
		}
	}
}

func TestParseConfig(t *testing.T) {
	valid := map[string]string{
		`root = "/a"`:                          "/a",
		"root = \"/a\"\n":                      "/a",
		`root="/a"`:                            "/a",
		"\t root \t=\t \"/a\" \t":              "/a",
		"# c\n\n  # indented\nroot = \"/a\"\n": "/a",
		"root = \"/a\"\r\n# c\r\n":             "/a",
		`root = "/a" # trailing`:               "/a",
		`root = "/a"#x`:                        "/a",
		`root = "/q\"b\\s\tt\nn"`:              "/q\"b\\s\tt\nn",
		`root = "/é\U0001F600"`:                "/é\U0001F600",
		"root = \"/raw\ttab é\"":               "/raw\ttab é",
		`root = ""`:                            "",
		"# comment with\ttab\nroot = \"/a\"":   "/a",
	}
	for in, want := range valid {
		got, err := parseConfig([]byte(in))
		if err != nil || got != want {
			t.Errorf("parseConfig(%q) = %q, %v; want %q", in, got, err, want)
		}
	}
	invalid := map[string]string{
		"":                             `no root = "..." line`,
		"# only a comment\n":           `no root = "..." line`,
		"\xEF\xBB\xBFroot = \"/a\"":    "starts with a byte-order mark",
		"root = \"/\xff\"":             "not valid UTF-8",
		"root = \"/a\"\nroot = \"/b\"": "line 2: root repeated (first on line 1)",
		"other = \"/a\"":               `line 1: expected root = "..."`,
		"root = \"/a\"\nother = 1":     `line 2: expected root = "..."`,
		"[table]\nroot = \"/a\"":       `line 1: expected root = "..."`,
		`"root" = "/a"`:                `line 1: expected root = "..."`,
		`roots = "/a"`:                 `line 1: expected root = "..."`,
		`root = '/a'`:                  "line 1: root must be a double-quoted string",
		`root = """/a"""`:              "line 1: unexpected text after the string",
		`root = /a`:                    "line 1: root must be a double-quoted string",
		`root "/a"`:                    `line 1: expected root = "..."`,
		`root = "/a`:                   "line 1: no closing quote",
		`root = "/a" x`:                "line 1: unexpected text after the string",
		`root = "/a" "b"`:              "line 1: unexpected text after the string",
		`root = "/a\b"`:                "line 1: invalid escape in the string",
		`root = "/a\r"`:                "line 1: invalid escape in the string",
		`root = "/a\x"`:                "line 1: invalid escape in the string",
		`root = "/a\uD800"`:            "line 1: invalid escape in the string",
		`root = "/a\U00110000"`:        "line 1: invalid escape in the string",
		`root = "/a\u00e"`:             "line 1: invalid escape in the string",
		`root = "/a\u+0e9"`:            "line 1: invalid escape in the string",
		`root = "/a\`:                  "line 1: invalid escape in the string",
		"root = \"/a\x01\"":            "line 1: control character in the string",
		"root = \"/a\x7f\"":            "line 1: control character in the string",
		"root = \"/a\rb\"":             "line 1: control character in the string",
		"root = \"/a\" # c\x01":        "line 1: control character in a comment",
		"# c\x01\nroot = \"/a\"":       "line 1: control character in a comment",
		"root = \"/a\"\r":              "line 1: unexpected text after the string",
		"\rroot = \"/a\"":              `line 1: expected root = "..."`,
		"\n\n# c\n  root = \"/a\" x\n": "line 4: unexpected text after the string",
	}
	for in, want := range invalid {
		if got, err := parseConfig([]byte(in)); err == nil || err.Error() != want {
			t.Errorf("parseConfig(%q) = %q, %v; want error %q", in, got, err, want)
		}
	}
}

func TestParseRootPath(t *testing.T) {
	for raw, want := range map[string]RootPath{
		"/":          {Path: "/"},
		"/a/b/":      {Path: "/a/b"},
		"//a/./b":    {Path: "/a/b"},
		"~/":         {UnderHome: true, Path: "."},
		"~/tasks/":   {UnderHome: true, Path: "tasks"},
		"~//tasks/x": {UnderHome: true, Path: "tasks/x"},
	} {
		got, err := ParseRootPath(raw)
		if err != nil || got != want {
			t.Errorf("ParseRootPath(%q) = %+v, %v; want %+v", raw, got, err, want)
		}
	}
	const form, dotDot = "root must be an absolute path or begin with ~/", "root must not contain a .. segment"
	const nul = "root must not contain a NUL character"
	for raw, want := range map[string]string{
		"": form, "~": form, "~user/x": form, "rel": form, "./a": form,
		"/a/../b": dotDot, "/..": dotDot, "~/..": dotDot, "~/a/../b": dotDot,
		"/tmp/x\x00y": nul, "~/x\x00": nul,
	} {
		if got, err := ParseRootPath(raw); err == nil || err.Error() != want {
			t.Errorf("ParseRootPath(%q) = %+v, %v; want error %q", raw, got, err, want)
		}
	}
}

func TestRootPathExpand(t *testing.T) {
	for _, tc := range []struct {
		raw, home, want string
		ok              bool
	}{
		{"/a", "", "/a", true},
		{"~/", "/h", "/h", true},
		{"~/tasks", "/h/", "/h/tasks", true},
		{"~/tasks", "/", "/tasks", true},
		{"~/tasks", "", "", false},
	} {
		r, _ := ParseRootPath(tc.raw)
		got, ok := r.Expand(tc.home)
		if got != tc.want || ok != tc.ok {
			t.Errorf("%q with home %q: got %q, %v; want %q, %v", tc.raw, tc.home, got, ok, tc.want, tc.ok)
		}
	}
}

func TestEncodeConfig(t *testing.T) {
	if got := string(EncodeConfig("/a/b")); got != "root = \"/a/b\"\n" {
		t.Errorf("got %q", got)
	}
	if got := string(EncodeConfig("/q\"b\\s\x01\x7f\n\té")); got != "root = \"/q\\\"b\\\\s\\u0001\\u007F\\n\\té\"\n" {
		t.Errorf("got %q", got)
	}
	for _, root := range []string{"/", "/a b", "/\"\\", "/\x00\x1f\x7f", "/\t\n", "/é😀", "/ "} {
		got, err := parseConfig(EncodeConfig(root))
		if err != nil || got != root {
			t.Errorf("round trip of %q: got %q, %v", root, got, err)
		}
	}
}
