package fsstep

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
)

// listed reports whether the listing of dir holds name, byte for byte. A
// missing directory holds nothing.
func listed(dir, name string) (bool, error) {
	listing, _, err := names(dir)
	return slices.Contains(listing, name), err
}

// names lists dir. ok is false when it is missing.
func names(dir string) (listing []string, ok bool, err error) {
	f, err := os.Open(dir)
	if errors.Is(err, fs.ErrNotExist) {
		return nil, false, nil
	}
	if err != nil {
		return nil, false, err
	}
	defer f.Close()
	listing, err = f.Readdirnames(-1)
	if err != nil {
		return nil, false, err
	}
	return listing, true, nil
}

// renameNew renames from to to, refusing with fs.ErrExist when to is
// there, so a hand change that got to the path first is never replaced.
func renameNew(from, to string) error {
	err := renameNoReplace(from, to)
	if errors.Is(err, errNoReplace) {
		// The filesystem can't refuse for us: look, then rename.
		if ok, lerr := listed(filepath.Dir(to), filepath.Base(to)); lerr != nil {
			return lerr
		} else if ok {
			return fs.ErrExist
		}
		return os.Rename(from, to)
	}
	return err
}

// errNoReplace is a filesystem that can't rename without replacing.
var errNoReplace = errors.New("rename without replacing is not supported")

// writeFile writes data to a new file at name, replacing one there, and
// flushes it.
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

// createEmpty makes an empty file at name, leaving one there as it is.
func createEmpty(name string) error {
	f, err := os.OpenFile(name, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o644)
	if errors.Is(err, fs.ErrExist) {
		return nil
	}
	if err != nil {
		return err
	}
	err = f.Sync()
	if cerr := f.Close(); err == nil {
		err = cerr
	}
	return err
}

// flush makes a directory's entries durable. On macOS, Sync issues
// F_FULLFSYNC. A directory that is gone has nothing to flush: its parent
// holds the change.
func flush(dir string) error {
	f, err := os.Open(dir)
	if errors.Is(err, fs.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	err = f.Sync()
	if cerr := f.Close(); err == nil {
		err = cerr
	}
	return err
}

// zeroByte reports whether fi is an empty regular file.
func zeroByte(fi fs.FileInfo) bool { return fi.Mode().IsRegular() && fi.Size() == 0 }

// emptySide reports whether one side of a move is empty: a zero-byte
// notes file, or a directory holding nothing but zero-byte notes files
// named exactly as koan names them, and any .DS_Store. Any other file is
// content, however small.
func emptySide(name string, dir bool) (bool, error) {
	fi, err := os.Lstat(name)
	if err != nil {
		return false, err
	}
	if !dir {
		return zeroByte(fi), nil
	}
	if !fi.IsDir() {
		return false, nil
	}
	entries, err := os.ReadDir(name)
	if err != nil {
		return false, err
	}
	for _, de := range entries {
		if de.Name() == ".DS_Store" {
			continue
		}
		if !IsNotesName(de.Name()) {
			return false, nil
		}
		fi, err := de.Info()
		if err != nil {
			return false, err
		}
		if !zeroByte(fi) {
			return false, nil
		}
	}
	return true, nil
}

// removeEmpty removes an empty side, checking each file again as it goes,
// so content written since it was judged is never removed.
func removeEmpty(name string, dir bool) error {
	if !dir {
		return os.Remove(name)
	}
	entries, err := os.ReadDir(name)
	if err != nil {
		return err
	}
	for _, de := range entries {
		p := filepath.Join(name, de.Name())
		if de.Name() != ".DS_Store" {
			fi, err := os.Lstat(p)
			if err != nil {
				return err
			}
			if !IsNotesName(de.Name()) || !zeroByte(fi) {
				return fmt.Errorf("%s now holds content", p)
			}
		}
		if err := os.Remove(p); err != nil {
			return err
		}
	}
	return os.Remove(name)
}
