package store

import (
	"errors"
	"fmt"

	"github.com/phansen314/koan/internal/cli"
	"github.com/phansen314/koan/internal/core"
	"modernc.org/sqlite"
	sqlite3 "modernc.org/sqlite/lib"
)

// sqliteError turns an error from SQLite into the one kind it is: busy
// once SQLite's own 5-second wait runs out, corrupt for a damaged file or
// one that isn't a database, io when the operating system refused, and
// internal for anything else, which is a bug.
func sqliteError(err error, database string) *cli.Error {
	var se *sqlite.Error
	if !errors.As(err, &se) {
		return cli.NewError(cli.KindInternal, "the database failed: "+err.Error())
	}
	switch se.Code() & 0xff {
	case sqlite3.SQLITE_BUSY, sqlite3.SQLITE_LOCKED:
		return busy("the database stayed busy for 5 seconds")
	case sqlite3.SQLITE_CORRUPT, sqlite3.SQLITE_NOTADB:
		e := cli.NewError(cli.KindCorrupt, database+" is corrupt: "+se.Error())
		e.Paths = []string{database}
		return e
	case sqlite3.SQLITE_IOERR, sqlite3.SQLITE_CANTOPEN, sqlite3.SQLITE_PERM, sqlite3.SQLITE_READONLY,
		sqlite3.SQLITE_FULL, sqlite3.SQLITE_NOLFS, sqlite3.SQLITE_AUTH:
		return cli.IO("the operating system refused the database: "+se.Error(), database)
	}
	return cli.NewError(cli.KindInternal, "the database failed: "+se.Error())
}

func busy(message string) *cli.Error {
	return cli.NewError(cli.KindBusy, message+"; it is safe to retry")
}

// asError turns whatever a change returned into the one error it reports.
// archive gives where an archived ID went, for not-found.
func asError(err error, database string, archive func(id int64) cli.Archived) *cli.Error {
	var w wrapError
	var ce *core.Error
	switch {
	case errors.As(err, &w):
		return w.e
	case errors.As(err, &ce):
		out := FromCore(ce)
		for _, id := range ce.Archived {
			out.Archived = append(out.Archived, archive(id))
		}
		return out
	}
	return sqliteError(err, database)
}

// FromCore turns a core error into the envelope's, all but where archived
// IDs went.
func FromCore(e *core.Error) *cli.Error {
	var out *cli.Error
	if e.Kind == core.KindConflict {
		out = cli.Conflict(e.Rule, e.Message)
	} else {
		out = cli.NewError(e.Kind, e.Message)
	}
	out.IDs = e.IDs
	out.Paths = e.Paths
	for _, p := range e.Problems {
		out.Problems = append(out.Problems, cli.Problem{Field: p, Reason: e.Message})
	}
	return out
}

// wrapError carries an envelope error through code that returns error.
type wrapError struct{ e *cli.Error }

func (w wrapError) Error() string { return fmt.Sprintf("%s: %s", w.e.Kind, w.e.Message) }

// Err lets a change return an envelope error as an error.
func Err(e *cli.Error) error { return wrapError{e} }
