package fsys

import "golang.org/x/sys/unix"

func renameNoReplace(ofd int, oldname string, nfd int, newname string) error {
	return unix.Renameat2(ofd, oldname, nfd, newname, unix.RENAME_NOREPLACE)
}
