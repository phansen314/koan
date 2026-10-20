package store

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"github.com/phansen314/koan/internal/cli"
)

// What init can make, in the order its result lists them.
const (
	MadeRoot     = "root"
	MadeTree     = "tree"
	MadeArchive  = "archive"
	MadeDatabase = "database"
)

// ownNames are koan's own names in the root, byte for byte, and whether
// each must be a directory.
var ownNames = map[string]bool{
	DatabaseName:          false,
	DatabaseName + "-wal": false,
	DatabaseName + "-shm": false,
	TreeName:              true,
	ArchiveName:           true,
}

// Init makes the root, its tree and archive, and the database, issuing IDs
// from idsFrom, or from 1 when it is nil. What is already there it leaves
// as it is, and reports. It returns what it made, in result order, and the
// root's first ID.
func Init(home string, idsFrom *int64) (r Root, created []string, firstID int64, e *cli.Error) {
	r, exists, e := FindRoot(home)
	if e != nil {
		return r, nil, 0, e
	}
	if !exists {
		// Another init may make it first; the lock then decides.
		err := os.Mkdir(r.Dir, 0o755)
		switch {
		case err == nil:
			created = append(created, MadeRoot)
		case !errors.Is(err, fs.ErrExist):
			return r, nil, 0, cli.IO("could not make the root: "+err.Error(), r.Path)
		}
	}
	if fi, err := os.Lstat(r.Dir); err != nil {
		return r, nil, 0, cli.IO("could not read the root: "+err.Error(), r.Path)
	} else if !fi.IsDir() {
		return r, nil, 0, notARoot([]string{r.Path}, r.Path+" is not a directory")
	}
	entries, e := r.List()
	if e != nil {
		return r, nil, 0, e
	}
	if e := checkOwn(r, entries); e != nil {
		return r, nil, 0, e
	}

	lock, e := TakeLock(r.Dir, r.Path)
	if e != nil {
		return r, nil, 0, e
	}
	defer lock.Release()
	// Listed again under the lock, since another init may have run.
	if entries, e = r.List(); e != nil {
		return r, nil, 0, e
	}
	if e := checkOwn(r, entries); e != nil {
		return r, nil, 0, e
	}
	have := map[string]bool{}
	for _, de := range entries {
		have[de.Name()] = true
	}
	for _, d := range []struct{ name, made, path string }{
		{TreeName, MadeTree, r.Tree},
		{ArchiveName, MadeArchive, r.Archive},
	} {
		if have[d.name] {
			continue
		}
		if err := os.Mkdir(filepath.Join(r.Dir, d.name), 0o755); err != nil {
			return r, nil, 0, cli.IO("could not make "+d.path+": "+err.Error(), d.path)
		}
		created = append(created, d.made)
	}

	file := filepath.Join(r.Dir, DatabaseName)
	first := int64(1)
	if idsFrom != nil {
		first = *idsFrom
	}
	if !have[DatabaseName] {
		if e := createDatabase(file, r.Database, first); e != nil {
			return r, nil, 0, e
		}
		created = append(created, MadeDatabase)
	} else {
		finish, got, e := existingDatabase(r, idsFrom)
		if e != nil {
			return r, nil, 0, e
		}
		if finish {
			if e := createDatabase(file, r.Database, first); e != nil {
				return r, nil, 0, e
			}
			created = append(created, MadeDatabase)
		} else {
			first = got
		}
	}
	if len(created) > 0 && created[0] == MadeRoot {
		if e := syncDir(filepath.Dir(r.Dir), filepath.Dir(r.Path)); e != nil {
			return r, nil, 0, e
		}
	}
	if len(created) > 0 {
		if e := syncDir(r.Dir, r.Path); e != nil {
			return r, nil, 0, e
		}
	}
	return r, created, first, nil
}

// checkOwn refuses a root holding anything that isn't koan's.
func checkOwn(r Root, entries []fs.DirEntry) *cli.Error {
	var foreign []string
	for _, de := range entries {
		dir, own := ownNames[de.Name()]
		if !own || dir && !de.IsDir() {
			foreign = append(foreign, filepath.Join(r.Path, de.Name()))
		}
	}
	if len(foreign) == 0 {
		return nil
	}
	slices.Sort(foreign)
	return notARoot(foreign, r.Path+" holds what isn't koan's: "+strings.Join(foreign, ", "))
}

func notARoot(paths []string, message string) *cli.Error {
	e := cli.Conflict(cli.RuleNotARoot, message+". Move it aside, or make ~/koan-root a symlink to an empty directory.")
	e.Paths = paths
	return e
}

// existingDatabase reads the database init found. finish is true for an
// unfinished init: format 0 and no tables at all. Otherwise it gives the
// root's first ID, refusing idsFrom with exists.
func existingDatabase(r Root, idsFrom *int64) (finish bool, firstID int64, e *cli.Error) {
	d, e := openConn(filepath.Join(r.Dir, DatabaseName), r.Database, "rw")
	if e != nil {
		return false, 0, e
	}
	defer d.Close()
	format, e := d.UserVersion()
	if e != nil {
		return false, 0, e
	}
	ctx := context.Background()
	if format == 0 {
		var n int
		if err := d.conn.QueryRowContext(ctx, "SELECT count(*) FROM sqlite_schema").Scan(&n); err != nil {
			return false, 0, sqliteError(err, r.Database)
		}
		if n == 0 {
			return true, 0, nil
		}
	}
	if format != Format {
		return false, 0, UnsupportedFormat(r.Database, format)
	}
	if idsFrom != nil {
		e := cli.Conflict(cli.RuleExists, r.Database+" already exists, so --ids-from can't set where it issues IDs from.")
		e.Paths = []string{r.Database}
		return false, 0, e
	}
	if err := d.conn.QueryRowContext(ctx, "SELECT first_id FROM root").Scan(&firstID); err != nil {
		return false, 0, sqliteError(err, r.Database)
	}
	return false, firstID, nil
}

// createDatabase makes the database in WAL mode, then its schema, format
// and root row in one transaction, so a crash leaves either none of them
// or all.
func createDatabase(file, path string, firstID int64) *cli.Error {
	d, e := openConn(file, path, "rwc")
	if e != nil {
		return e
	}
	defer d.Close()
	ctx := context.Background()
	var mode string
	if err := d.conn.QueryRowContext(ctx, "PRAGMA journal_mode = WAL").Scan(&mode); err != nil {
		return sqliteError(err, path)
	}
	if mode != "wal" {
		return cli.IO("the database could not be put in WAL mode, which needs a local filesystem", path)
	}
	if _, err := d.conn.ExecContext(ctx, "BEGIN IMMEDIATE"); err != nil {
		return sqliteError(err, path)
	}
	err := func() error {
		if _, err := d.conn.ExecContext(ctx, schema); err != nil {
			return err
		}
		if _, err := d.conn.ExecContext(ctx, fmt.Sprintf("PRAGMA user_version = %d", Format)); err != nil {
			return err
		}
		if _, err := d.conn.ExecContext(ctx, "INSERT INTO root (first_id) VALUES (?)", firstID); err != nil {
			return err
		}
		_, err := d.conn.ExecContext(ctx, "COMMIT")
		return err
	}()
	if err != nil {
		d.conn.ExecContext(ctx, "ROLLBACK")
		return sqliteError(err, path)
	}
	return nil
}

// syncDir flushes a directory, so the entries made in it last. On macOS
// Go's Sync is F_FULLFSYNC.
func syncDir(dir, path string) *cli.Error {
	f, err := os.Open(dir)
	if err == nil {
		err = f.Sync()
		if cerr := f.Close(); err == nil {
			err = cerr
		}
	}
	if err != nil && !errors.Is(err, fs.ErrInvalid) {
		return cli.IO("could not flush "+path+": "+err.Error(), path)
	}
	return nil
}
