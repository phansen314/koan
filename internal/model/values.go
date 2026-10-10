package model

import (
	"encoding/json"
	"regexp"
	"strconv"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/phansen314/koan/internal/jsonio"
)

// ID is a task ID: 1 to IDMax (see design-spec.md, Task IDs).
type ID int64

// IDMax is the ID ceiling: 15 digits, below 2^53 so every JSON reader holds
// it exactly.
const IDMax = 999_999_999_999_999

// MigrationMax bounds koan.json's migration: the integers every JSON reader
// holds exactly.
const MigrationMax = 1<<53 - 1

// Priority bounds: the integers every JSON reader holds exactly.
const (
	PriorityMin = -(1<<53 - 1)
	PriorityMax = 1<<53 - 1
)

// Supported format versions: the one task file and koan.json version this
// binary reads and writes.
const (
	TaskSchema = 1
	RootSchema = 2
	// StateSchema is the state file's.
	StateSchema = 1
)

// ID checks v, at ptr, as a task ID.
func (p *Problems) ID(v any, ptr string) (ID, bool) {
	i, ok := p.Int(v, ptr, 1, IDMax)
	return ID(i), ok
}

// IDs checks v, at ptr, as a set of task IDs: an array of distinct IDs.
func (p *Problems) IDs(v any, ptr string) ([]ID, bool) {
	a, ok := p.Array(v, ptr)
	if !ok {
		return nil, false
	}
	ids := make([]ID, 0, len(a))
	valid := make([]bool, 0, len(a))
	for i, item := range a {
		id, ok1 := p.ID(item, jsonio.Pointer(ptr, strconv.Itoa(i)))
		ok = ok && ok1
		ids = append(ids, id)
		valid = append(valid, ok1)
	}
	return ids, Unique(p, ids, valid, ptr) && ok
}

// Priority checks v, at ptr, as a priority: an integer, or null for none.
func (p *Problems) Priority(v any, ptr string) (*int64, bool) {
	if v == nil {
		return nil, true
	}
	if _, isNum := v.(json.Number); !isNum {
		p.Add(ptr, "expected an integer or null")
		return nil, false
	}
	i, ok := p.Int(v, ptr, PriorityMin, PriorityMax)
	if !ok {
		return nil, false
	}
	return &i, true
}

// Title is a task title: trimmed, 1–200 code points, no line breaks (see
// design-spec.md, Titles).
type Title string

const titleMax = 200

// isTitleSpace is the Unicode White_Space property, which unicode.IsSpace
// implements.
func isTitleSpace(r rune) bool { return unicode.IsSpace(r) }

// TitleInput trims s, a title given as input, then validates it.
func (p *Problems) TitleInput(s, ptr string) (Title, bool) {
	t := strings.TrimFunc(s, isTitleSpace)
	return Title(t), titleBody(t, ptr, p.AddAdditional) // the input schema takes any string
}

// StoredTitle validates s, a title read from a task file: it must already be
// trimmed.
func (p *Problems) StoredTitle(s, ptr string) (Title, bool) {
	if s != strings.TrimFunc(s, isTitleSpace) {
		p.Add(ptr, "must not start or end with whitespace")
		return "", false
	}
	return Title(s), titleBody(s, ptr, p.Add) // the file schema's pattern
}

func titleBody(t, ptr string, add func(field, reason string)) bool {
	n := utf8.RuneCountInString(t)
	switch {
	case n == 0:
		add(ptr, "must not be empty")
		return false
	case n > titleMax:
		add(ptr, "must be at most 200 characters")
		return false
	case strings.ContainsFunc(t, isLineBreakOrControl):
		add(ptr, "must not contain control characters or line breaks")
		return false
	}
	return true
}

// isLineBreakOrControl is category Cc, plus the line and paragraph
// separators.
func isLineBreakOrControl(r rune) bool {
	return unicode.Is(unicode.Cc, r) || r == '\u2028' || r == '\u2029'
}

// Tag is a tag: a name (see design-spec.md, Tags).
type Tag string

// tagPattern is the rule for tags: a folder name, lowercase only.
var tagPattern = regexp.MustCompile(`^[a-z0-9](?:[a-z0-9-]{0,62}[a-z0-9])?$`)

const (
	reasonTag        = "must be 1-64 lowercase letters, digits, and hyphens, not starting or ending with a hyphen"
	reasonFolderName = "must be 1-64 letters, digits, and hyphens, not starting or ending with a hyphen"
)

// Tag checks v, at ptr, as a tag.
func (p *Problems) Tag(v any, ptr string) (Tag, bool) {
	s, ok := p.String(v, ptr)
	if !ok {
		return "", false
	}
	if !tagPattern.MatchString(s) {
		p.Add(ptr, reasonTag)
		return "", false
	}
	return Tag(s), true
}

// Tags checks v, at ptr, as a set of tags: an array of distinct tags.
func (p *Problems) Tags(v any, ptr string) ([]Tag, bool) {
	a, ok := p.Array(v, ptr)
	if !ok {
		return nil, false
	}
	tags := make([]Tag, 0, len(a))
	valid := make([]bool, 0, len(a))
	for i, item := range a {
		t, ok1 := p.Tag(item, jsonio.Pointer(ptr, strconv.Itoa(i)))
		ok = ok && ok1
		tags = append(tags, t)
		valid = append(valid, ok1)
	}
	return tags, Unique(p, tags, valid, ptr) && ok
}

// FolderPath is a folder's path from the root: "/" or "/a/b" (see
// design-spec.md, Folder paths).
type FolderPath string

// RootFolder is the root's folder path.
const RootFolder FolderPath = "/"

var folderPathPattern = regexp.MustCompile(`^/$|^(/[A-Za-z0-9](?:[A-Za-z0-9-]{0,62}[A-Za-z0-9])?)+$`)

// FolderPath checks v, at ptr, as a folder path.
func (p *Problems) FolderPath(v any, ptr string) (FolderPath, bool) {
	s, ok := p.String(v, ptr)
	if !ok {
		return "", false
	}
	if !folderPathPattern.MatchString(s) {
		p.Add(ptr, "must be a folder path from the root, like / or /proj/travel, each segment "+reasonFolderName)
		return "", false
	}
	return FolderPath(s), true
}

// Segments returns the folder names on the path, outermost first; none for
// the root.
func (f FolderPath) Segments() []string {
	if f == RootFolder {
		return nil
	}
	return strings.Split(string(f)[1:], "/")
}

// Timestamp is a UTC time in whole seconds, "2006-01-02T15:04:05Z" (see
// design-spec.md, Timestamps).
type Timestamp string

const timestampLayout = "2006-01-02T15:04:05Z"

var timestampPattern = regexp.MustCompile(`^[0-9]{4}-[0-9]{2}-[0-9]{2}T[0-9]{2}:[0-9]{2}:[0-9]{2}Z$`)

// TimestampOf returns t, in UTC and truncated to whole seconds.
func TimestampOf(t time.Time) Timestamp {
	return Timestamp(t.UTC().Format(timestampLayout))
}

// Timestamp checks v, at ptr, as a timestamp: its shape, then that it is a
// real calendar date and time (time.Parse rejects month 13, hour 24, and a
// leap second).
func (p *Problems) Timestamp(v any, ptr string) (Timestamp, bool) {
	s, ok := p.String(v, ptr)
	if !ok {
		return "", false
	}
	if !timestampPattern.MatchString(s) {
		p.Add(ptr, "must be a UTC timestamp like 2026-09-20T18:31:51Z")
		return "", false
	}
	if _, err := time.Parse(timestampLayout, s); err != nil {
		p.AddAdditional(ptr, "is not a real date and time") // the schema checks only the shape
		return "", false
	}
	return Timestamp(s), true
}
