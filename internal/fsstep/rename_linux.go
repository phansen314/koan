package fsstep

import (
	"errors"
	"io/fs"

	"golang.org/x/sys/unix"
)

func renameNoReplace(from, to string) error {
	err := unix.Renameat2(unix.AT_FDCWD, from, unix.AT_FDCWD, to, unix.RENAME_NOREPLACE)
	switch {
	case err == nil:
		return nil
	case errors.Is(err, unix.EEXIST):
		return fs.ErrExist
	case errors.Is(err, unix.EINVAL), errors.Is(err, unix.ENOSYS):
		return errNoReplace
	}
	return &fs.PathError{Op: "rename", Path: from, Err: err}
}
