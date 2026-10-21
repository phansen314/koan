package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"io/fs"
	"net/url"
	"os"
	"path/filepath"
	"strings"

	"github.com/phansen314/koan/internal/cli"
)

// Root is ~/koan-root: its paths as written, through the symlink when it
// is one, and the directory it is.
type Root struct {
	Path     string
	Tree     string
	Archive  string
	Database string
	// Dir is the directory the root is, following ~/koan-root when it is
	// a symlink and no further link.
	Dir string
}

// The names koan keeps in the root.
const (
	DatabaseName = "koan.db"
	TreeName     = "tree"
	ArchiveName  = "archived"
)

// FindRoot finds the root under home. exists is false when there is none.
func FindRoot(home string) (r Root, exists bool, e *cli.Error) {
	root := filepath.Join(home, "koan-root")
	r = Root{
		Path:     root,
		Tree:     filepath.Join(root, TreeName),
		Archive:  filepath.Join(root, ArchiveName),
		Database: filepath.Join(root, DatabaseName),
		Dir:      root,
	}
	fi, err := os.Lstat(root)
	if errors.Is(err, fs.ErrNotExist) {
		return r, false, nil
	}
	if err != nil {
		return r, false, cli.IO("could not read the root: "+err.Error(), root)
	}
	if fi.Mode()&fs.ModeSymlink == 0 {
		return r, true, nil
	}
	target, err := os.Readlink(root)
	if err != nil {
		return r, false, cli.IO("could not read the root's symlink: "+err.Error(), root)
	}
	if !filepath.IsAbs(target) {
		target = filepath.Join(home, target)
	}
	r.Dir = target
	if _, err := os.Lstat(target); errors.Is(err, fs.ErrNotExist) {
		return r, false, nil
	} else if err != nil {
		return r, false, cli.IO("could not read the root: "+err.Error(), root)
	}
	return r, true, nil
}

// List lists the root's directory. A root that isn't a directory, or that
// can't be listed, is the operating system refusing.
func (r Root) List() ([]fs.DirEntry, *cli.Error) {
	entries, err := os.ReadDir(r.Dir)
	if err != nil {
		return nil, cli.IO("could not list the root: "+err.Error(), r.Path)
	}
	return entries, nil
}

// HasDatabase reports whether the root's listing holds koan.db, byte for
// byte.
func (r Root) HasDatabase() (bool, *cli.Error) {
	entries, e := r.List()
	if e != nil {
		return false, e
	}
	for _, de := range entries {
		if de.Name() == DatabaseName {
			return true, nil
		}
	}
	return false, nil
}

// NotInitialized is the error for a root with something missing: the root
// itself, or its database.
func NotInitialized(missing string) *cli.Error {
	e := cli.NewError(cli.KindNotInitialized, missing+" does not exist. Run koan init.")
	e.Paths = []string{missing}
	return e
}

// NoDatabase is the not-initialized error for a root with no database.
// When the root holds what isn't koan's, such as a tree the original koan
// left, init would refuse it, so the message says to move it aside first.
func (r Root) NoDatabase() *cli.Error {
	entries, e := r.List()
	if e != nil {
		return e
	}
	foreign := foreignIn(r, entries)
	if len(foreign) == 0 {
		return NotInitialized(r.Database)
	}
	e = cli.NewError(cli.KindNotInitialized, r.Database+" does not exist, and "+r.Path+" holds what isn't koan's: "+
		strings.Join(foreign, ", ")+". Move it aside, or make ~/koan-root a symlink to an empty directory, then run koan init.")
	e.Paths = []string{r.Database}
	return e
}

// UnsupportedFormat is the error for a database in a format this binary
// doesn't read.
func UnsupportedFormat(database string, format int) *cli.Error {
	return cli.NewError(cli.KindUnsupportedFormat,
		fmt.Sprintf("%s is in format %d, and this koan reads only format %d.", database, format, Format))
}

// pragmas are set on every open, since SQLite doesn't store them;
// journal_mode is stored in the file by init.
var pragmas = []string{
	"busy_timeout(5000)",
	"foreign_keys(1)",
	"synchronous(FULL)",
	"fullfsync(1)",
	"checkpoint_fullfsync(1)",
}

// dsn names the database file with the pragmas. mode is rw to open only a
// database that is there, rwc to make one.
func dsn(path, mode string) string {
	q := url.Values{"mode": {mode}, "_pragma": pragmas}
	return "file:" + (&url.URL{Path: path}).EscapedPath() + "?" + q.Encode()
}

// DB is one connection to the database, as each koan process has.
type DB struct {
	path string // as written, for messages
	db   *sql.DB
	conn *sql.Conn
}

// openConn opens one connection with the pragmas set.
func openConn(file, path, mode string) (*DB, *cli.Error) {
	db, err := sql.Open("sqlite", dsn(file, mode))
	if err != nil {
		return nil, cli.NewError(cli.KindInternal, "could not open the database: "+err.Error())
	}
	db.SetMaxOpenConns(1)
	conn, err := db.Conn(context.Background())
	if err != nil {
		db.Close()
		return nil, sqliteError(err, path)
	}
	return &DB{path: path, db: db, conn: conn}, nil
}

// Close closes the connection. The last connection to close checkpoints
// the WAL.
func (d *DB) Close() {
	if d == nil {
		return
	}
	d.conn.Close()
	d.db.Close()
}

// UserVersion reads the database's format.
func (d *DB) UserVersion() (int, *cli.Error) {
	var v int
	if err := d.conn.QueryRowContext(context.Background(), "PRAGMA user_version").Scan(&v); err != nil {
		return 0, sqliteError(err, d.path)
	}
	return v, nil
}

// OpenDatabase opens a root's database, which must be there, and checks
// its format; its writes carry out their filesystem steps through steps.
// On unsupported-format, and on corrupt when the format could be read,
// format says what the database holds; it is -1 when it couldn't be read.
func OpenDatabase(r Root, steps StepsFor) (s *Store, format int, e *cli.Error) {
	if steps == nil {
		return nil, -1, cli.NewError(cli.KindInternal, "a store opened with no filesystem steps")
	}
	d, e := openConn(filepath.Join(r.Dir, DatabaseName), r.Database, "rw")
	if e != nil {
		return nil, -1, e
	}
	format, e = d.UserVersion()
	if e != nil {
		d.Close()
		return nil, -1, e
	}
	if format != Format {
		d.Close()
		return nil, format, UnsupportedFormat(r.Database, format)
	}
	return &Store{Root: r, db: d, steps: steps(r)}, format, nil
}

// Open opens the database of the root under home for a command, its
// writes carrying out their filesystem steps through steps. The root's
// state is checked here, so a missing root or database is
// not-initialized, and one in another format is unsupported-format.
func Open(home string, steps StepsFor) (*Store, *cli.Error) {
	r, exists, e := FindRoot(home)
	if e != nil {
		return nil, e
	}
	if !exists {
		return nil, NotInitialized(r.Path)
	}
	has, e := r.HasDatabase()
	if e != nil {
		return nil, e
	}
	if !has {
		return nil, r.NoDatabase()
	}
	s, _, e := OpenDatabase(r, steps)
	return s, e
}

// Stats is what info counts in a usable database.
type Stats struct {
	NextID  int64
	Tasks   int64
	Folders int64
	Claims  int64
	Pending int64
	Stuck   int64
}

// Stats counts the database in one read transaction.
func (s *Store) Stats() (Stats, *cli.Error) {
	var st Stats
	e := s.Read(func(tx *Tx) error {
		return tx.QueryRow(`SELECT
			max(coalesce((SELECT max(id) FROM issued), 0) + 1, (SELECT first_id FROM root)),
			(SELECT count(*) FROM task),
			(SELECT count(*) FROM folder),
			(SELECT count(*) FROM claim),
			(SELECT count(*) FROM pending),
			(SELECT count(*) FROM pending WHERE stuck = 1)`).
			Scan(&st.NextID, &st.Tasks, &st.Folders, &st.Claims, &st.Pending, &st.Stuck)
	})
	return st, e
}
