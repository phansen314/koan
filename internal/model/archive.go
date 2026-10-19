package model

import (
	"bytes"
	"encoding/json"
	"fmt"
	"path"
	"slices"
	"strings"
)

// ArchiveEntry is one line of the archive's index: one item archived
// (spec/design.md, The archive). Field order is the order koan writes.
type ArchiveEntry struct {
	Schema     int64   `json:"schema"`
	ID         ID      `json:"id"`
	Kind       string  `json:"kind"`
	Title      *string `json:"title"`
	Path       Folder  `json:"path"`
	Archived   string  `json:"archived"`
	ArchivedAt *string `json:"archived_at"`
	IDs        []ID    `json:"ids"`
}

// The kinds of item archived, and the index's format.
const (
	ArchivedTask   = "task"
	ArchivedFolder = "folder"
	ArchiveFormat  = 1
)

// DecodeArchiveEntry reads one line of the index and checks every rule. A
// nil error and no problems means it is usable.
func DecodeArchiveEntry(data []byte) (*ArchiveEntry, []Problem, error) {
	m, err := Keys(data)
	if err != nil {
		return nil, nil, err
	}
	if ps := missing(m, []string{"schema", "id", "kind", "title", "path", "archived", "archived_at", "ids"}, "title", "archived_at"); ps != nil {
		return nil, ps, nil
	}
	var a ArchiveEntry
	if err := Decode(data, &a); err != nil {
		return nil, []Problem{prob("", err.Error())}, nil
	}
	var ps []Problem
	if a.Schema != ArchiveFormat {
		ps = append(ps, prob("/schema", fmt.Sprintf("is not %d", ArchiveFormat)))
	}
	if !ValidID(a.ID) {
		ps = append(ps, prob("/id", "is not an ID"))
	}
	if a.Kind != ArchivedTask && a.Kind != ArchivedFolder {
		ps = append(ps, prob("/kind", "is not task or folder"))
	}
	if f, ok := ParseFolder(string(a.Path)); !ok || f != a.Path {
		ps = append(ps, prob("/path", "is not a folder path"))
	}
	if a.Archived == "" || path.IsAbs(a.Archived) || path.Clean(a.Archived) != a.Archived ||
		a.Archived == ".." || strings.HasPrefix(a.Archived, "../") {
		ps = append(ps, prob("/archived", "is not a path within the archive"))
	}
	if a.ArchivedAt != nil && !ValidStamp(*a.ArchivedAt) {
		ps = append(ps, prob("/archived_at", "is not a timestamp"))
	}
	if len(a.IDs) == 0 || !slices.IsSorted(a.IDs) || len(slices.Compact(slices.Clone(a.IDs))) != len(a.IDs) || !slices.Contains(a.IDs, a.ID) {
		ps = append(ps, prob("/ids", "is not the IDs archived, ascending, its own included"))
	}
	for i, id := range a.IDs {
		if !ValidID(id) {
			ps = append(ps, prob(fmt.Sprintf("/ids/%d", i), "is not an ID"))
		}
	}
	return &a, ps, nil
}

// Matches reports whether a and b say the same of what is in the archive:
// all but path and archived_at, which the archive does not record
// (spec/design.md, The archive).
func (a *ArchiveEntry) Matches(b *ArchiveEntry) bool {
	return a.ID == b.ID && a.Kind == b.Kind && a.Archived == b.Archived &&
		(a.Title == nil) == (b.Title == nil) && (a.Title == nil || *a.Title == *b.Title) &&
		slices.Equal(a.IDs, b.IDs)
}

// EncodeLine writes v as one line of JSON, for the index: no HTML escaping,
// and a trailing newline.
func EncodeLine(v any) ([]byte, error) {
	var b bytes.Buffer
	enc := json.NewEncoder(&b)
	enc.SetEscapeHTML(false)
	if err := enc.Encode(v); err != nil {
		return nil, err
	}
	return b.Bytes(), nil
}

// ArchiveRel is the path within the root of a path within the archive.
func ArchiveRel(rel string) string { return path.Join(ArchiveDir, rel) }
