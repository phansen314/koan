package core

import (
	"regexp"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"
)

// The patterns of the rules doc's Structure and Fields sections.
var (
	namePattern  = regexp.MustCompile(`^[A-Za-z0-9](?:[A-Za-z0-9-]{0,62}[A-Za-z0-9])?$`)
	tagPattern   = regexp.MustCompile(`^[a-z0-9](?:[a-z0-9-]{0,62}[a-z0-9])?$`)
	idPattern    = regexp.MustCompile(`^[1-9][0-9]{0,14}$`)
	intPattern   = regexp.MustCompile(`^-?(?:0|[1-9][0-9]*)$`)
	stampPattern = regexp.MustCompile(`^[0-9]{4}-[0-9]{2}-[0-9]{2}T[0-9]{2}:[0-9]{2}:[0-9]{2}Z$`)
)

// MaxID is the largest ID the ID pattern allows: fifteen digits.
const MaxID int64 = 999_999_999_999_999

// MaxInt is the largest integer magnitude a value may hold: 2^53 − 1.
const MaxInt int64 = 1<<53 - 1

// TimestampLayout is the one timestamp form: UTC to the second.
const TimestampLayout = "2006-01-02T15:04:05Z"

// ValidName reports whether s matches the folder pattern.
func ValidName(s string) bool { return namePattern.MatchString(s) }

// ValidTag reports whether s matches the tag pattern: the folder pattern,
// lowercase only.
func ValidTag(s string) bool { return tagPattern.MatchString(s) }

// ValidLabel reports whether s is a label: "$" and then a name.
func ValidLabel(s string) bool {
	return strings.HasPrefix(s, "$") && ValidName(s[1:])
}

// ParseID reads an ID written as digits, with no "@".
func ParseID(s string) (int64, bool) {
	if !idPattern.MatchString(s) {
		return 0, false
	}
	n, err := strconv.ParseInt(s, 10, 64)
	return n, err == nil
}

// ValidID reports whether n is within the ID pattern's range.
func ValidID(n int64) bool { return n >= 1 && n <= MaxID }

// CheckTitle trims whitespace from s and checks it as a title: 1–200 code
// points, with no control characters or line breaks. It returns the title as
// stored.
func CheckTitle(s string) (string, error) {
	if !utf8.ValidString(s) {
		return "", invalid("a title must be UTF-8")
	}
	t := strings.TrimSpace(s)
	n := utf8.RuneCountInString(t)
	if n < 1 || n > 200 {
		return "", invalid("a title holds 1 to 200 characters after trimming, not %d", n)
	}
	for _, r := range t {
		if r <= 0x1f || (r >= 0x7f && r <= 0x9f) || r == 0x2028 || r == 0x2029 {
			return "", invalid("a title holds no control characters or line breaks")
		}
	}
	return t, nil
}

// CheckInt checks an integer value: within ±(2^53 − 1).
func CheckInt(n int64) error {
	if n > MaxInt || n < -MaxInt {
		return invalid("%d is outside ±(2^53 − 1)", n)
	}
	return nil
}

// ParseInt reads an integer as written in JSON: no fraction, no exponent,
// within ±(2^53 − 1).
func ParseInt(s string) (int64, error) {
	if !intPattern.MatchString(s) {
		return 0, invalid("%q is not an integer written without a fraction or an exponent", s)
	}
	n, err := strconv.ParseInt(s, 10, 64)
	if err != nil {
		return 0, invalid("%s is outside ±(2^53 − 1)", s)
	}
	if err := CheckInt(n); err != nil {
		return 0, err
	}
	return n, nil
}

// ParseTimestamp reads a timestamp: UTC to the second, YYYY-MM-DDTHH:MM:SSZ,
// naming a real date and time.
func ParseTimestamp(s string) (time.Time, error) {
	if !stampPattern.MatchString(s) {
		return time.Time{}, invalid("%q is not a timestamp YYYY-MM-DDTHH:MM:SSZ", s)
	}
	t, err := time.Parse(TimestampLayout, s)
	if err != nil || t.Format(TimestampLayout) != s {
		return time.Time{}, invalid("%q is not a real date and time", s)
	}
	return t, nil
}

// FormatTimestamp writes t in the one timestamp form.
func FormatTimestamp(t time.Time) string { return t.UTC().Format(TimestampLayout) }

// CheckMaterial checks the material part of a reference: parts split by "/",
// none empty, "." or "..", none holding a NUL, and the first not starting
// with "@".
func CheckMaterial(s string) error {
	parts := strings.Split(s, "/")
	for i, p := range parts {
		switch {
		case p == "" || p == "." || p == "..":
			return invalid("material %q has an empty, . or .. part", s)
		case strings.IndexByte(p, 0) >= 0:
			return invalid("material %q holds a NUL", s)
		case i == 0 && p[0] == '@':
			return invalid("material %q starts with @, which is koan's", s)
		}
	}
	return nil
}
