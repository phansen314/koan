// Package markers keeps the .koan/ markers that bind a directory outside
// the tree to a folder: finding the nearest, reading one, and writing and
// removing one whole. It knows nothing of the database; whether a marker's
// ID names a live folder is the caller's to judge.
package markers

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"syscall"
	"unicode/utf8"

	"github.com/phansen314/koan/internal/core"
)

// The names a marker keeps, every one of them koan's own.
const (
	DirName    = ".koan"
	FileName   = "marker.json"
	TempName   = "marker.json.tmp"
	IgnoreName = ".gitignore"
)

// Schema is the one marker.json format this koan reads and writes.
const Schema = 1

// Marker is marker.json, in its schema's order.
type Marker struct {
	Schema int    `json:"schema"`
	ID     int64  `json:"id"`
	Path   string `json:"path"`
	Dir    string `json:"dir"`
}

// File is a directory's marker.json.
func File(dir string) string { return filepath.Join(dir, DirName, FileName) }

// entries lists dir. A directory that is missing, or isn't one, holds
// nothing.
func entries(dir string) ([]fs.DirEntry, error) {
	list, err := os.ReadDir(dir)
	if errors.Is(err, fs.ErrNotExist) || errors.Is(err, syscall.ENOTDIR) {
		return nil, nil
	}
	return list, err
}

// entry is the entry in dir named exactly name, or nil.
func entry(dir, name string) (fs.DirEntry, error) {
	list, err := entries(dir)
	if err != nil {
		return nil, err
	}
	for _, e := range list {
		if e.Name() == name {
			return e, nil
		}
	}
	return nil, nil
}

// maxLinks is how many symlinks Real follows before it gives up, as the
// operating system does.
const maxLinks = 255

// sameFile reports whether two paths name one file, its own entry if a
// symlink. A test may replace it to act as a filesystem that ignores case.
var sameFile = func(a, b string) bool {
	ai, err := os.Lstat(a)
	if err != nil {
		return false
	}
	bi, err := os.Lstat(b)
	return err == nil && os.SameFile(ai, bi)
}

// folded is the entry in dir whose name matches name ignoring case and
// which the filesystem says is the same file as name, or nil.
func folded(dir, name string) (fs.DirEntry, error) {
	list, err := entries(dir)
	if err != nil {
		return nil, err
	}
	for _, e := range list {
		if strings.EqualFold(e.Name(), name) && sameFile(filepath.Join(dir, e.Name()), filepath.Join(dir, name)) {
			return e, nil
		}
	}
	return nil, nil
}

// Real is the real path of an absolute path: every symlink on it
// followed, and every name spelled as the listing of the directory above
// it spells it. root is the root's real path, or "" while that is what
// is being worked out. Below root a name is found byte for byte, so a
// name spelled in another case is never taken for the entry on a
// filesystem that ignores case. Elsewhere, a name not found byte for byte
// is taken in the listing's spelling when an entry matches it ignoring
// case and is the same file. From the first name not found, the rest is
// kept as written; exists reports whether all of it was found.
func Real(p, root string) (real string, exists bool, err error) {
	todo := split(p)
	cur := string(filepath.Separator)
	links := 0
	for len(todo) > 0 {
		name := todo[0]
		todo = todo[1:]
		switch name {
		case "", ".":
			continue
		case "..":
			cur = filepath.Dir(cur)
			continue
		}
		e, err := entry(cur, name)
		if err == nil && e == nil && (root == "" || !Under(cur, root)) {
			e, err = folded(cur, name)
		}
		if err != nil {
			return "", false, err
		}
		if e == nil {
			return filepath.Join(append([]string{cur, name}, todo...)...), false, nil
		}
		next := filepath.Join(cur, e.Name())
		if e.Type()&fs.ModeSymlink == 0 {
			cur = next
			continue
		}
		if links++; links > maxLinks {
			return "", false, &fs.PathError{Op: "resolve", Path: p, Err: syscall.ELOOP}
		}
		target, err := os.Readlink(next)
		if err != nil {
			return "", false, err
		}
		if filepath.IsAbs(target) {
			cur = string(filepath.Separator)
		}
		todo = append(split(target), todo...)
	}
	return cur, true, nil
}

func split(p string) []string {
	return strings.Split(filepath.ToSlash(p), "/")
}

// Under reports whether a path is dir or lies under it. Both are real
// paths, compared byte for byte.
func Under(p, dir string) bool {
	return p == dir || strings.HasPrefix(p, strings.TrimSuffix(dir, "/")+"/")
}

// Found is the nearest marker.json at or above a path.
type Found struct {
	// Dir is the directory holding .koan/, on the path as given.
	Dir string
	// File is its marker.json, on the path as given.
	File string
}

// Look finds the nearest marker.json at or above an absolute path,
// walking up the path as given and listing each directory. It stops at
// the first marker.json, whatever its state. ok is false when there is
// none.
func Look(p string) (f Found, ok bool, err error) {
	for dir := filepath.Clean(p); ; dir = filepath.Dir(dir) {
		has, err := Has(dir)
		if err != nil {
			return Found{}, false, err
		}
		if has {
			return Found{Dir: dir, File: File(dir)}, true, nil
		}
		if dir == filepath.Dir(dir) {
			return Found{}, false, nil
		}
	}
}

// Has reports whether dir holds .koan/marker.json, each name found byte
// for byte in a listing.
func Has(dir string) (bool, error) {
	names, ok, err := Contents(dir)
	return ok && slices.Contains(names, FileName), err
}

// Contents is what dir's .koan/ holds, by name, in byte order. ok is
// false when there is no .koan directory.
func Contents(dir string) (names []string, ok bool, err error) {
	e, err := entry(dir, DirName)
	if err != nil || e == nil || !e.IsDir() {
		return nil, false, err
	}
	list, err := os.ReadDir(filepath.Join(dir, DirName))
	if err != nil {
		return nil, false, err
	}
	for _, e := range list {
		names = append(names, e.Name())
	}
	slices.Sort(names)
	return names, true, nil
}

// NotDir reports whether dir holds a .koan that isn't a directory, in
// the way of a marker.
func NotDir(dir string) (bool, error) {
	e, err := entry(dir, DirName)
	return e != nil && !e.IsDir(), err
}

// Foreign is the names among a .koan/'s that aren't koan's own.
func Foreign(names []string) []string {
	var out []string
	for _, n := range names {
		if n != FileName && n != TempName && n != IgnoreName {
			out = append(out, n)
		}
	}
	return out
}

// Checkout reports whether dir is a checkout: its .git is a directory. A
// worktree's .git is a file.
func Checkout(dir string) (bool, error) {
	e, err := entry(dir, ".git")
	return e != nil && e.IsDir(), err
}

// Read reads the marker.json of the directory whose real path is real.
// why is "" when the file is a marker koan reads, bound where it is; else
// it says why it binds nothing: it can't be read as JSON, lacks a field,
// is in a format koan doesn't read, or was copied or moved. Whether its
// ID names a live folder is the caller's to judge.
func Read(file, real string) (m Marker, why string) {
	data, err := os.ReadFile(file)
	if err != nil {
		return Marker{}, "it can't be read: " + err.Error()
	}
	if !utf8.Valid(data) {
		return Marker{}, "it isn't JSON: it isn't UTF-8"
	}
	var raw map[string]any
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.UseNumber()
	if err := dec.Decode(&raw); err != nil {
		return Marker{}, "it isn't a JSON object: " + err.Error()
	}
	if raw == nil {
		return Marker{}, "it isn't a JSON object"
	}
	// A format koan doesn't read may hold other fields, so its schema
	// is judged first.
	if _, ok := raw["schema"]; !ok {
		return Marker{}, "it lacks schema"
	}
	if n, ok := raw["schema"].(json.Number); !ok || n.String() != "1" {
		return Marker{}, fmt.Sprintf("it is in schema %s, which this koan doesn't read", printed(raw["schema"]))
	}
	for _, name := range []string{"id", "path", "dir"} {
		if _, ok := raw[name]; !ok {
			return Marker{}, "it lacks " + name
		}
	}
	n, ok := raw["id"].(json.Number)
	id, valid := core.ParseID(n.String())
	if !ok || !valid {
		return Marker{}, fmt.Sprintf("its id, %s, isn't an ID", printed(raw["id"]))
	}
	m = Marker{Schema: Schema, ID: id}
	if m.Path, ok = raw["path"].(string); !ok || !folderPath(m.Path) {
		return Marker{}, fmt.Sprintf("its path, %s, isn't a folder's path", printed(raw["path"]))
	}
	if m.Dir, ok = raw["dir"].(string); !ok || !filepath.IsAbs(m.Dir) || filepath.Clean(m.Dir) != m.Dir {
		return Marker{}, fmt.Sprintf("its dir, %s, isn't a real path", printed(raw["dir"]))
	}
	if m.Dir != real {
		return Marker{}, fmt.Sprintf("its dir is %s, not %s, where it is: it was copied or moved", m.Dir, real)
	}
	return m, ""
}

// printed is a value as JSON writes it.
func printed(v any) string {
	b, _ := json.Marshal(v)
	return string(b)
}

// folderPath reports whether s is a folder's path from the root, not the
// root.
func folderPath(s string) bool {
	if !strings.HasPrefix(s, "/") || s == "/" {
		return false
	}
	for _, name := range strings.Split(s[1:], "/") {
		if !core.ValidName(name) {
			return false
		}
	}
	return true
}

// Write writes dir's marker whole: .koan/ when it is missing, its
// .gitignore, then marker.json through a temp file renamed into place.
func Write(dir string, m Marker) error {
	k := filepath.Join(dir, DirName)
	if err := os.Mkdir(k, 0o755); err != nil && !errors.Is(err, fs.ErrExist) {
		return err
	}
	if err := writeFile(filepath.Join(k, IgnoreName), []byte("*\n")); err != nil {
		return err
	}
	data, err := json.Marshal(m)
	if err != nil {
		return err
	}
	tmp := filepath.Join(k, TempName)
	if err := writeFile(tmp, append(data, '\n')); err != nil {
		return err
	}
	if err := os.Rename(tmp, filepath.Join(k, FileName)); err != nil {
		return err
	}
	return syncDir(k)
}

// Remove removes dir's marker, given what its .koan/ holds, none of it
// foreign: the temp file, marker.json, .gitignore, then .koan/ itself.
// removed reports whether there was a marker.json.
func Remove(dir string, names []string) (removed bool, err error) {
	k := filepath.Join(dir, DirName)
	for _, n := range []string{TempName, FileName, IgnoreName} {
		if !slices.Contains(names, n) {
			continue
		}
		if err := os.Remove(filepath.Join(k, n)); err != nil && !errors.Is(err, fs.ErrNotExist) {
			return false, err
		}
		removed = removed || n == FileName
	}
	if err := os.Remove(k); err != nil && !errors.Is(err, fs.ErrNotExist) {
		return false, err
	}
	return removed, nil
}

// writeFile writes data to name, replacing what is there, and flushes it.
func writeFile(name string, data []byte) error {
	f, err := os.OpenFile(name, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0o644)
	if err != nil {
		return err
	}
	_, err = f.Write(data)
	if err == nil {
		err = f.Sync()
	}
	if cerr := f.Close(); err == nil {
		err = cerr
	}
	return err
}

func syncDir(dir string) error {
	f, err := os.Open(dir)
	if err != nil {
		return err
	}
	defer f.Close()
	return f.Sync()
}
