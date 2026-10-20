package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"time"

	"github.com/phansen314/koan/internal/cli"
	"github.com/phansen314/koan/internal/core"
)

// Store is a root with its database open, as one command uses it.
type Store struct {
	Root Root
	db   *DB
	// Steps carries out filesystem steps. With none, steps stay pending.
	Steps Steps
}

// Close closes the database.
func (s *Store) Close() { s.db.Close() }

// Pending is a filesystem step committed with a change and not yet carried
// out. Change is the seq of its change's first step.
type Pending struct {
	Seq    int64
	Change int64
	Step   json.RawMessage
}

// Outcome is what carrying out steps came to.
type Outcome struct {
	// Done is the steps carried out, each directory they touched flushed.
	Done []int64
	// Stuck is the changes to set aside: each is marked stuck, every step
	// of it.
	Stuck    []int64
	Warnings []cli.Warning
}

// Steps carries out filesystem steps, oldest first.
type Steps interface {
	Carry(rows []Pending) Outcome
}

// Change is steps 3 to 5 of a write: inside BEGIN IMMEDIATE it resolves
// every reference, checks the tree, changes the rows and adds the
// filesystem steps that bring the tree in line. Its error refuses the
// change, and nothing is written.
type Change func(tx *Tx) (result any, warnings []cli.Warning, err error)

// Write runs a change through the steps of a write. The caller has done
// step 1, checking the input's shape, and opened the store, checking the
// root's state.
//
//  2. Take the lock, and finish the steps an earlier write left pending.
//  3. BEGIN IMMEDIATE.
//  4. and 5. The change.
//  6. COMMIT.
//  7. Carry out the steps, then delete their rows.
//  8. Release the lock.
//
// Once COMMIT succeeds the write succeeds, whatever then happens to its
// filesystem steps.
func (s *Store) Write(change Change) (any, []cli.Warning, *cli.Error) {
	lock, e := TakeLock(s.Root.Dir, s.Root.Path)
	if e != nil {
		return nil, nil, e
	}
	defer lock.Release()
	warnings, e := s.finish()
	if e != nil {
		return nil, warnings, e
	}

	tx, e := s.begin("BEGIN IMMEDIATE")
	if e != nil {
		return nil, warnings, e
	}
	result, w, err := change(tx)
	if err == nil {
		err = tx.err
	}
	if err != nil {
		e := asError(err, s.Root.Database, tx.archivePath)
		tx.exec("ROLLBACK")
		return nil, warnings, e
	}
	if _, err := s.db.conn.ExecContext(tx.ctx, "COMMIT"); err != nil {
		tx.exec("ROLLBACK")
		return nil, warnings, sqliteError(err, s.Root.Database)
	}
	warnings = append(warnings, w...)

	if tx.steps {
		// Past COMMIT nothing fails the write. Rows that couldn't be
		// deleted stay pending, and the next write carries them out
		// again, which is harmless.
		more, _ := s.finish()
		warnings = append(warnings, more...)
	}
	return result, warnings, nil
}

// Read runs fn in one read transaction, so it sees one committed state.
// It takes no lock.
func (s *Store) Read(fn func(tx *Tx) error) *cli.Error {
	tx, e := s.begin("BEGIN")
	if e != nil {
		return e
	}
	defer tx.exec("ROLLBACK")
	err := fn(tx)
	if err == nil {
		err = tx.err
	}
	if err != nil {
		return asError(err, s.Root.Database, tx.archivePath)
	}
	return nil
}

func (s *Store) begin(stmt string) (*Tx, *cli.Error) {
	ctx := context.Background()
	if _, err := s.db.conn.ExecContext(ctx, stmt); err != nil {
		return nil, sqliteError(err, s.Root.Database)
	}
	return &Tx{conn: s.db.conn, ctx: ctx, Now: core.FormatTimestamp(time.Now())}, nil
}

// finish carries out the steps left pending, skipping stuck ones, then
// deletes the rows of those done and marks the stuck changes, in one
// transaction.
func (s *Store) finish() ([]cli.Warning, *cli.Error) {
	if s.Steps == nil {
		return nil, nil
	}
	ctx := context.Background()
	rows, err := s.db.conn.QueryContext(ctx, "SELECT seq, change, step FROM pending WHERE stuck = 0 ORDER BY seq")
	if err != nil {
		return nil, sqliteError(err, s.Root.Database)
	}
	var pending []Pending
	for rows.Next() {
		var p Pending
		var step string
		if err := rows.Scan(&p.Seq, &p.Change, &step); err != nil {
			rows.Close()
			return nil, sqliteError(err, s.Root.Database)
		}
		p.Step = json.RawMessage(step)
		pending = append(pending, p)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return nil, sqliteError(err, s.Root.Database)
	}
	if len(pending) == 0 {
		return nil, nil
	}
	out := s.Steps.Carry(pending)
	if len(out.Done) == 0 && len(out.Stuck) == 0 {
		return out.Warnings, nil
	}
	tx, e := s.begin("BEGIN IMMEDIATE")
	if e != nil {
		return out.Warnings, e
	}
	for _, seq := range out.Done {
		tx.exec("DELETE FROM pending WHERE seq = ?", seq)
	}
	for _, change := range out.Stuck {
		tx.exec("UPDATE pending SET stuck = 1 WHERE change = ?", change)
	}
	if tx.err == nil {
		tx.exec("COMMIT")
	}
	if tx.err != nil {
		e := sqliteError(tx.err, s.Root.Database)
		tx.err = nil
		tx.exec("ROLLBACK")
		return out.Warnings, e
	}
	return out.Warnings, nil
}

// Tx is one transaction: a write's, from BEGIN IMMEDIATE to COMMIT, or a
// read's. It is a core.Tree and a core.Graph, so references resolve and
// readiness runs against the database as it stands in the transaction.
type Tx struct {
	conn *sql.Conn
	ctx  context.Context
	// Now is the change's time, the same for every row it touches.
	Now string
	// err is the first error a lookup met; the change fails with it.
	err error
	// steps is whether the change added filesystem steps; change is the
	// seq of its first.
	steps  bool
	change int64
}

// Exec runs a statement in the transaction.
func (t *Tx) Exec(query string, args ...any) (sql.Result, error) {
	return t.conn.ExecContext(t.ctx, query, args...)
}

// Query runs a query in the transaction.
func (t *Tx) Query(query string, args ...any) (*sql.Rows, error) {
	return t.conn.QueryContext(t.ctx, query, args...)
}

// QueryRow runs a query for one row in the transaction.
func (t *Tx) QueryRow(query string, args ...any) *sql.Row {
	return t.conn.QueryRowContext(t.ctx, query, args...)
}

// exec runs a statement, keeping the first error.
func (t *Tx) exec(query string, args ...any) {
	if t.err != nil && query != "ROLLBACK" {
		return
	}
	if _, err := t.conn.ExecContext(t.ctx, query, args...); err != nil && t.err == nil {
		t.err = err
	}
}

// fail keeps the first error a lookup met, and reports whether there is
// one.
func (t *Tx) fail(err error) bool {
	if err != nil && err != sql.ErrNoRows && t.err == nil {
		t.err = err
	}
	return t.err != nil
}

// AddStep adds a filesystem step to the change, and returns its seq.
func (t *Tx) AddStep(step json.RawMessage) (int64, error) {
	var seq int64
	if err := t.QueryRow("SELECT coalesce(max(seq), 0) + 1 FROM pending").Scan(&seq); err != nil {
		return 0, err
	}
	if !t.steps {
		t.steps, t.change = true, seq
	}
	if _, err := t.Exec("INSERT INTO pending (seq, change, step) VALUES (?, ?, ?)", seq, t.change, string(step)); err != nil {
		return 0, err
	}
	return seq, nil
}
