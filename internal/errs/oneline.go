package errs

import (
	"strconv"
	"strings"
	"unicode"
)

// OneLine writes each control character in s as a Go escape (\n, \x1b,
// \u2028), so that none, e.g. a newline in a root path, can split the line or
// drive a terminal. Everything else is left as it is. It is the CLI's stderr
// line (cli-spec.md, Output), and pick's text shown in fzf.
func OneLine(s string) string {
	if !strings.ContainsFunc(s, IsLineControl) {
		return s
	}
	var b strings.Builder
	for _, r := range s {
		if IsLineControl(r) {
			q := strconv.QuoteRune(r)
			b.WriteString(q[1 : len(q)-1])
		} else {
			b.WriteRune(r)
		}
	}
	return b.String()
}

// IsLineControl reports whether OneLine escapes r.
func IsLineControl(r rune) bool {
	return unicode.IsControl(r) || r == '\u2028' || r == '\u2029'
}
