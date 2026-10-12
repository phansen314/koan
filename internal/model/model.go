// Package model holds koan's data types and the rules they follow
// (spec/design.md, Data model). It does no I/O.
package model

import (
	"path"
	"regexp"
	"strconv"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"
)

// Format is the one format this binary reads and writes.
const Format = 1

// ID is a task ID.
type ID = int64

// MaxID is the largest ID: 15 digits.
const MaxID ID = 999999999999999

// MaxInt bounds every integer in a file: ±(2^53 − 1).
const MaxInt int64 = 1<<53 - 1

// ValidID reports whether n can be a task's ID.
func ValidID(n ID) bool { return n >= 1 && n <= MaxID }

var (
	folderName = regexp.MustCompile(`^[A-Za-z0-9](?:[A-Za-z0-9-]{0,62}[A-Za-z0-9])?$`)
	tagName    = regexp.MustCompile(`^[a-z0-9](?:[a-z0-9-]{0,62}[a-z0-9])?$`)
	taskName   = regexp.MustCompile(`^([1-9][0-9]{0,14})\.(json|md)$`)
	timestamp  = regexp.MustCompile(`^[0-9]{4}-[0-9]{2}-[0-9]{2}T[0-9]{2}:[0-9]{2}:[0-9]{2}Z$`)
)

// ValidFolderName reports whether s can name a folder.
func ValidFolderName(s string) bool { return folderName.MatchString(s) }

// ValidTag reports whether s can be a tag, or a create-batch ref.
func ValidTag(s string) bool { return tagName.MatchString(s) }

// Suffixes of a task's two files.
const (
	TaskSuffix  = ".json"
	NotesSuffix = ".md"
)

// TaskFilename splits a filename like 42.json or 42.md into its ID and
// suffix; ok is false for any other name.
func TaskFilename(name string) (id ID, suffix string, ok bool) {
	m := taskName.FindStringSubmatch(name)
	if m == nil {
		return 0, "", false
	}
	id, _ = strconv.ParseInt(m[1], 10, 64)
	return id, "." + m[2], true
}

// Folder is a folder path from the root: "/" or "/proj/travel".
type Folder string

// Root is the root folder.
const Root Folder = "/"

// ParseFolder checks s against the folder path rule.
func ParseFolder(s string) (Folder, bool) {
	if s == "/" {
		return Root, true
	}
	if !strings.HasPrefix(s, "/") {
		return "", false
	}
	for _, seg := range strings.Split(s[1:], "/") {
		if !ValidFolderName(seg) {
			return "", false
		}
	}
	return Folder(s), true
}

// Rel is the folder's path relative to the root directory: "." for the root.
func (f Folder) Rel() string {
	if f == Root {
		return "."
	}
	return string(f[1:])
}

// Parent is the folder holding f; the root is its own parent.
func (f Folder) Parent() Folder { return Folder(path.Dir(string(f))) }

// Join is the folder named name inside f.
func (f Folder) Join(name string) Folder { return Folder(path.Join(string(f), name)) }

// Holds reports whether g is f or under it.
func (f Folder) Holds(g Folder) bool {
	return f == Root || g == f || strings.HasPrefix(string(g), string(f)+"/")
}

// Segments are the names along the path; none for the root.
func (f Folder) Segments() []string {
	if f == Root {
		return nil
	}
	return strings.Split(string(f[1:]), "/")
}

// CompareFolders is tree order: a parent before its children, siblings by
// name, byte for byte.
func CompareFolders(a, b Folder) int {
	as, bs := a.Segments(), b.Segments()
	for i := 0; i < len(as) && i < len(bs); i++ {
		if c := strings.Compare(as[i], bs[i]); c != 0 {
			return c
		}
	}
	return len(as) - len(bs)
}

// MaxTitle is the longest title, in characters.
const MaxTitle = 200

// CleanTitle trims s and checks it against the title rule. reason is empty
// when the title is valid.
func CleanTitle(s string) (title, reason string) {
	title = strings.TrimFunc(s, unicode.IsSpace)
	switch n := utf8.RuneCountInString(title); {
	case !utf8.ValidString(title):
		return title, "is not valid UTF-8"
	case n == 0:
		return title, "is empty"
	case n > MaxTitle:
		return title, "is longer than 200 characters"
	}
	for _, r := range title {
		if unicode.IsControl(r) || r == ' ' || r == ' ' {
			return title, "contains a control character or line break"
		}
	}
	return title, ""
}

// TimeLayout is the form of every timestamp: UTC, to the second.
const TimeLayout = "2006-01-02T15:04:05Z"

// Stamp formats t as a timestamp.
func Stamp(t time.Time) string { return t.UTC().Format(TimeLayout) }

// ValidStamp reports whether s is a timestamp.
func ValidStamp(s string) bool {
	if !timestamp.MatchString(s) {
		return false
	}
	_, err := time.Parse(TimeLayout, s)
	return err == nil
}

// Readiness is a task's derived state.
type Readiness string

const (
	Ready   Readiness = "ready"
	Blocked Readiness = "blocked"
	Done    Readiness = "done"
)
