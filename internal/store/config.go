package store

import (
	"bytes"
	"errors"
	"fmt"
	"path"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/phansen314/koan/internal/fsys"
	"github.com/phansen314/koan/internal/model"
)

// Env is what store needs from the environment. Home and ConfigDir are ""
// when they cannot be determined (see Locate).
type Env struct {
	FS        fsys.FS
	Home      string
	ConfigDir string
	LockWait  time.Duration // how long a write waits for a held write lock; 0 tries once
	// LegacyConfigDir is where ftask, koan's former name, kept its config;
	// "" when unknown, or when there is nothing to migrate (see Migrate).
	LegacyConfigDir string
}

// DefaultLockWait is how long a write waits for a held write lock before
// failing with busy (design-spec.md, Guarantees: Bounded wait).
const DefaultLockWait = 5 * time.Second

// ConfigName is the config's file name in the config directory.
const ConfigName = "config.toml"

// ConfigPath is the config's path; "" when the config directory is unknown.
func (e Env) ConfigPath() string {
	if e.ConfigDir == "" {
		return ""
	}
	return path.Join(e.ConfigDir, ConfigName)
}

// Locate derives the home and config directories from the environment, per
// design-spec.md, Config file — not with os.UserConfigDir, which fails on a
// relative XDG_CONFIG_HOME instead of ignoring it. HOME and XDG_CONFIG_HOME
// count only when set to an absolute path; either result is "" when it cannot
// be determined. goos is runtime.GOOS.
func Locate(getenv func(string) string, goos string) (home, configDir string) {
	if h := getenv("HOME"); strings.HasPrefix(h, "/") {
		home = h
	}
	return home, configDirNamed(getenv, goos, home, "koan")
}

// LocateLegacy is where ftask, koan's former name, kept its config: located
// as Locate does, under the name ftask; "" when it cannot be determined.
func LocateLegacy(getenv func(string) string, goos string) string {
	home, _ := Locate(getenv, goos)
	return configDirNamed(getenv, goos, home, "ftask")
}

func configDirNamed(getenv func(string) string, goos, home, name string) string {
	if goos == "darwin" {
		if home == "" {
			return ""
		}
		return path.Join(home, "Library", "Application Support", name)
	}
	switch x := getenv("XDG_CONFIG_HOME"); {
	case strings.HasPrefix(x, "/"):
		return path.Join(x, name)
	case home != "":
		return path.Join(home, ".config", name)
	}
	return ""
}

// RootPath is a config's root, in one of its two legal forms (see
// design-spec.md, Root path), cleaned: an absolute path, or a path under the
// home directory.
type RootPath struct {
	// UnderHome is true for a "~/" root; Path is then relative to the home
	// directory, "." for the home directory itself.
	UnderHome bool
	Path      string
}

// ParseRootPath checks a config's root: absolute or beginning "~/", with no
// ".." segment and no NUL. Any other form makes the config corrupt; the
// error says why.
func ParseRootPath(raw string) (r RootPath, err error) {
	if strings.ContainsRune(raw, 0) {
		return RootPath{}, errors.New("root must not contain a NUL character")
	}
	switch {
	case strings.HasPrefix(raw, "/"):
		r.Path = model.CleanPath(raw)
	case strings.HasPrefix(raw, "~/"):
		r.UnderHome = true
		r.Path = model.CleanPath(strings.TrimLeft(raw[2:], "/"))
	default:
		return RootPath{}, errors.New("root must be an absolute path or begin with ~/")
	}
	if model.HasDotDot(r.Path) {
		return RootPath{}, errors.New("root must not contain a .. segment")
	}
	return r, nil
}

// Expand returns the root as koan reports it: cleaned, with "~/" expanded
// into home. ok is false for a "~/" root with no home directory, which
// counts as a missing root.
func (r RootPath) Expand(home string) (string, bool) {
	if !r.UnderHome {
		return r.Path, true
	}
	if home == "" {
		return "", false
	}
	return model.CleanPath(home + "/" + r.Path), true
}

// parseConfig reads the config with koan's parser for a strict subset of
// TOML (implementation-spec.md, Config file): UTF-8 without a byte-order
// mark; blank and comment lines; and exactly one line root = "<basic
// string>". Anything else makes the config corrupt; the error says why,
// naming the line at fault if there is one.
func parseConfig(data []byte) (root string, err error) {
	if !utf8.Valid(data) {
		return "", errors.New("not valid UTF-8")
	}
	if bytes.HasPrefix(data, []byte{0xEF, 0xBB, 0xBF}) {
		return "", errors.New("starts with a byte-order mark")
	}
	found := 0 // the root line's number, once found
	lines := strings.Split(string(data), "\n")
	for i, line := range lines {
		if i < len(lines)-1 {
			line = strings.TrimSuffix(line, "\r") // a CRLF newline; a lone CR is not one
		}
		line = strings.TrimLeft(line, " \t")
		switch {
		case line == "":
			continue
		case line[0] == '#':
			if !validComment(line) {
				return "", fmt.Errorf("line %d: control character in a comment", i+1)
			}
			continue
		}
		r, why := parseRootLine(line)
		switch {
		case why != "":
			return "", fmt.Errorf("line %d: %s", i+1, why)
		case found > 0:
			return "", fmt.Errorf("line %d: root repeated (first on line %d)", i+1, found)
		}
		root, found = r, i+1
	}
	if found == 0 {
		return "", errors.New(`no root = "..." line`)
	}
	return root, nil
}

// parseRootLine parses root = "<basic string>", then optional whitespace and
// an optional comment. why is "" on success, else what is wrong.
func parseRootLine(line string) (root, why string) {
	const expected = `expected root = "..."`
	rest, ok := strings.CutPrefix(line, "root")
	if !ok {
		return "", expected
	}
	rest = strings.TrimLeft(rest, " \t")
	if rest, ok = strings.CutPrefix(rest, "="); !ok {
		return "", expected
	}
	rest = strings.TrimLeft(rest, " \t")
	if rest, ok = strings.CutPrefix(rest, `"`); !ok {
		return "", "root must be a double-quoted string"
	}
	var b strings.Builder
	for {
		r, size := utf8.DecodeRuneInString(rest)
		switch {
		case size == 0:
			return "", "no closing quote"
		case r == '"':
			rest = strings.TrimLeft(rest[1:], " \t")
			if rest != "" && (rest[0] != '#' || !validComment(rest)) {
				if rest[0] == '#' {
					return "", "control character in a comment"
				}
				return "", "unexpected text after the string"
			}
			return b.String(), ""
		case r == '\\':
			dec, n, ok := unescape(rest)
			if !ok {
				return "", "invalid escape in the string"
			}
			b.WriteRune(dec)
			rest = rest[n:]
		case isTOMLControl(r):
			return "", "control character in the string"
		default:
			b.WriteRune(r)
			rest = rest[size:]
		}
	}
}

// unescape decodes the escape at the start of s: \" \\ \t \n \uXXXX
// \UXXXXXXXX, the last two naming a Unicode scalar value. It returns the rune
// and the escape's length.
func unescape(s string) (rune, int, bool) {
	if len(s) < 2 {
		return 0, 0, false
	}
	switch s[1] {
	case '"':
		return '"', 2, true
	case '\\':
		return '\\', 2, true
	case 't':
		return '\t', 2, true
	case 'n':
		return '\n', 2, true
	case 'u', 'U':
		n := 4
		if s[1] == 'U' {
			n = 8
		}
		if len(s) < 2+n {
			return 0, 0, false
		}
		v, err := strconv.ParseUint(s[2:2+n], 16, 32)
		if err != nil || !utf8.ValidRune(rune(v)) {
			return 0, 0, false
		}
		return rune(v), 2 + n, true
	}
	return 0, 0, false
}

// isTOMLControl reports a character TOML allows in neither a basic string
// nor a comment: the control characters other than tab.
func isTOMLControl(r rune) bool {
	return (r < 0x20 && r != '\t') || r == 0x7F
}

func validComment(s string) bool {
	return !strings.ContainsFunc(s, isTOMLControl)
}

// EncodeConfig returns the config naming root: root = "<root>" and a
// newline, escaped as a TOML basic string requires.
func EncodeConfig(root string) []byte {
	var b strings.Builder
	b.WriteString(`root = "`)
	for _, r := range root {
		switch {
		case r == '"':
			b.WriteString(`\"`)
		case r == '\\':
			b.WriteString(`\\`)
		case r == '\n':
			b.WriteString(`\n`)
		case r == '\t':
			b.WriteString(`\t`)
		case isTOMLControl(r):
			fmt.Fprintf(&b, `\u%04X`, r)
		default:
			b.WriteRune(r)
		}
	}
	b.WriteString("\"\n")
	return []byte(b.String())
}
