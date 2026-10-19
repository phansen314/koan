// Package model holds koan's data types and the rules they follow
// (spec/design.md, Data model). It does no I/O.
package model

import (
	"path"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"
)

// Format is the one tree format this binary reads and writes: the tree's
// layout, and what @koan.json records.
const Format = 3

// The format of each kind of file this binary reads and writes. Each kind is
// numbered apart, so no kind shares a constant with another.
const (
	TaskFormat   = 1
	FolderFormat = 2
	StateFormat  = 1
)

// Mark starts the name of every entry koan owns in the tree.
const Mark = "@"

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
	taskName   = regexp.MustCompile(`^@([1-9][0-9]{0,14})\.(json|md)$`)
	idRef      = regexp.MustCompile(`^@?([1-9][0-9]{0,14})$`)
	timestamp  = regexp.MustCompile(`^[0-9]{4}-[0-9]{2}-[0-9]{2}T[0-9]{2}:[0-9]{2}:[0-9]{2}Z$`)
)

// ValidFolderName reports whether s can name a folder.
func ValidFolderName(s string) bool { return folderName.MatchString(s) }

// ValidTag reports whether s can be a tag.
func ValidTag(s string) bool { return tagName.MatchString(s) }

// ValidRef reports whether s can be a batch ref: a tag with a letter in it,
// so that no ref reads as a task ID.
func ValidRef(s string) bool {
	return ValidTag(s) && strings.ContainsAny(s, "abcdefghijklmnopqrstuvwxyz")
}

// ParseIDRef reads an ID as a reference writes it, with or without its
// `@`: "42" or "@42".
func ParseIDRef(s string) (ID, bool) {
	m := idRef.FindStringSubmatch(s)
	if m == nil {
		return 0, false
	}
	id, _ := strconv.ParseInt(m[1], 10, 64)
	return id, true
}

// IDRef is ID id as a reference writes it: "@42".
func IDRef(id ID) string { return Mark + strconv.FormatInt(id, 10) }

// ValidMaterial reports whether s can be the material part of a reference:
// a relative path with no empty, ".", or ".." segment, whose first segment
// is not named with `@`.
func ValidMaterial(s string) bool {
	if strings.HasPrefix(s, Mark) || strings.ContainsRune(s, 0) {
		return false
	}
	for _, seg := range strings.Split(s, "/") {
		if seg == "" || seg == "." || seg == ".." {
			return false
		}
	}
	return true
}

// Suffixes of a task's two files.
const (
	TaskSuffix  = ".json"
	NotesSuffix = ".md"
)

// Names of the root's metadata file and a folder's two files.
const (
	MetaName        = "@koan.json"
	FolderFileName  = "@folder.json"
	FolderNotesName = "@folder.md"
)

// TaskFilename splits a filename like @42.json or @42.md into its ID and
// suffix; ok is false for any other name.
func TaskFilename(name string) (id ID, suffix string, ok bool) {
	m := taskName.FindStringSubmatch(name)
	if m == nil {
		return 0, "", false
	}
	id, _ = strconv.ParseInt(m[1], 10, 64)
	return id, "." + m[2], true
}

// FolderDir is the name on disk of the folder named name.
func FolderDir(name string) string { return Mark + name }

// FolderName is the folder name an entry on disk stands for: ok is false
// when the entry's name is not `@` and a folder name.
func FolderName(entry string) (name string, ok bool) {
	name, ok = strings.CutPrefix(entry, Mark)
	return name, ok && ValidFolderName(name)
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

// Rel is the folder's path relative to the root directory, each name with
// its `@`: "@proj/@travel", and "." for the root.
func (f Folder) Rel() string {
	if f == Root {
		return "."
	}
	segs := f.Segments()
	for i, s := range segs {
		segs[i] = FolderDir(s)
	}
	return path.Join(segs...)
}

// TaskRel and NotesRel are the root-relative paths of a task's two files.
func TaskRel(f Folder, id ID) string {
	return path.Join(f.Rel(), Mark+ClaimKey(id)+TaskSuffix)
}

func NotesRel(f Folder, id ID) string {
	return path.Join(f.Rel(), Mark+ClaimKey(id)+NotesSuffix)
}

// FolderFileRel and FolderNotesRel are the root-relative paths of a
// folder's two files.
func FolderFileRel(f Folder) string { return path.Join(f.Rel(), FolderFileName) }

func FolderNotesRel(f Folder) string { return path.Join(f.Rel(), FolderNotesName) }

// Parent is the folder holding f; the root is its own parent.
func (f Folder) Parent() Folder { return Folder(path.Dir(string(f))) }

// Join is the folder named name inside f.
func (f Folder) Join(name string) Folder { return Folder(path.Join(string(f), name)) }

// Name is f's own name, the last part of its path; "" for the root.
func (f Folder) Name() string {
	if f == Root {
		return ""
	}
	return path.Base(string(f))
}

// SameButCase reports whether a and b differ only in the case of ASCII
// letters: two names a filesystem that ignores case takes for one. Folder
// names are ASCII, so no other folding can matter.
func SameButCase(a, b string) bool {
	if a == b || len(a) != len(b) {
		return false
	}
	for i := range len(a) {
		if lower(a[i]) != lower(b[i]) {
			return false
		}
	}
	return true
}

func lower(c byte) byte {
	if 'A' <= c && c <= 'Z' {
		return c + 'a' - 'A'
	}
	return c
}

// CaseClash finds the first of paths that differs only in case from an
// earlier one, in some part of its path: its index, and the earlier
// path's part it clashes with, as far as it goes. i is -1 when there is
// none. The paths are valid, so ASCII.
func CaseClash(paths []Folder) (i int, earlier Folder) {
	seen := map[string]Folder{}
	for i, p := range paths {
		cur := Root
		for _, seg := range p.Segments() {
			cur = cur.Join(seg)
			k := strings.ToLower(string(cur))
			if q, ok := seen[k]; ok && q != cur {
				return i, q
			} else if !ok {
				seen[k] = cur
			}
		}
	}
	return -1, ""
}

// Holds reports whether g is f or under it.
func (f Folder) Holds(g Folder) bool {
	return f == Root || g == f || strings.HasPrefix(string(g), string(f)+"/")
}

// TreeOrder compares two folders in tree order: a parent before its
// children, siblings by name, byte for byte.
func TreeOrder(a, b Folder) int {
	return slices.Compare(a.Segments(), b.Segments())
}

// Segments are the names along the path; none for the root.
func (f Folder) Segments() []string {
	if f == Root {
		return nil
	}
	return strings.Split(string(f[1:]), "/")
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
