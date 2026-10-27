package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"time"

	"github.com/phansen314/koan/internal/cli"
	"github.com/phansen314/koan/internal/core"
)

// Store is a root with its database open, as one command uses it. Its
// writes always carry out their filesystem steps: it can only be opened
// with the Steps that do.
type Store struct {
	Root Root
	// Command is the command its writes are logged as, as its name is
	// typed, such as task create.
	Command string
	db      *DB
	steps   Steps
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
	// Stuck is the steps that stuck, each with its step rewritten to
	// record what it found. Each is marked stuck with the rest of its
	// change.
	Stuck []Pending
}

// Steps carries out filesystem steps, oldest first.
type Steps interface {
	// Encode is a step as its pending row holds it, given the seq it
	// will have. It refuses anything that isn't one of its steps, so
	// nothing adds a step bypassing it.
	Encode(seq int64, step any) (json.RawMessage, error)
	// Carry carries out the live rows; stuck are set aside, and steps
	// that overlap them wait.
	Carry(live, stuck []Pending) Outcome
	// Warnings is what the rows left say once a write is done with them:
	// live are still pending, stuck are set aside.
	Warnings(live, stuck []Pending) []cli.Warning
}

// StepsFor is the Steps of a root.
type StepsFor func(Root) Steps

// Fault, when a test sets it, is called at each point where a write can
// be cut short, which point names. A test crashes the process there, or
// returns an error for the operating system refusing there: at COMMIT,
// at a filesystem step, or deleting the rows of the steps carried out.
// Past COMMIT, at committed, nothing can fail, and its error is ignored.
// Nothing but tests sets it.
var Fault func(point string) error

func fault(point string) error {
	if Fault == nil {
		return nil
	}
	return Fault(point)
}

// Change is steps 3 to 5 of a write: inside BEGIN IMMEDIATE it resolves
// every reference, checks the tree, changes the rows, adds the filesystem
// steps that bring the tree in line, and records its events, which are
// inserted once it returns. Its error refuses the change, and nothing is
// written.
type Change func(tx *Tx) (result any, warnings []cli.Warning, err error)

// Write runs a change through the steps of a write. The caller has done
// step 1, checking the input's shape, and opened the store, checking the
// root's state.
//
//  2. Take the lock, and finish the steps an earlier write left pending.
//  3. BEGIN IMMEDIATE.
//  4. and 5. The change, then the events it recorded.
//  6. COMMIT.
//  7. Carry out the steps, then delete their rows.
//  8. Release the lock.
//
// Once COMMIT succeeds the write succeeds, whatever then happens to its
// filesystem steps. Once the lock is taken, the result or the error
// carries what the steps left pending or stuck say.
func (s *Store) Write(change Change) (any, []cli.Warning, *cli.Error) {
	lock, e := TakeLock(s.Root.Dir, s.Root.Path)
	if e != nil {
		return nil, nil, e
	}
	defer lock.Release()
	result, warnings, e := s.write(change)
	return result, append(warnings, s.left()...), e
}

func (s *Store) write(change Change) (any, []cli.Warning, *cli.Error) {
	if e := s.finish(); e != nil {
		return nil, nil, e
	}
	return s.change(change)
}

// change is steps 3 to 7 of a write, the lock held and the steps an
// earlier write left pending finished.
func (s *Store) change(change Change) (any, []cli.Warning, *cli.Error) {
	tx, e := s.begin("BEGIN IMMEDIATE")
	if e != nil {
		return nil, nil, e
	}
	result, warnings, err := change(tx)
	if err == nil {
		err = tx.err
	}
	if err == nil {
		err = tx.recordMade()
	}
	if err == nil {
		err = tx.recordDerived(result)
	}
	if err == nil {
		err = tx.insert(tx.ctx, s.db.conn)
	}
	if err != nil {
		e := asError(err, s.Root.Database, tx.ArchivedEntry)
		tx.exec("ROLLBACK")
		return nil, nil, e
	}
	err = fault("commit")
	if err == nil {
		_, err = s.db.conn.ExecContext(tx.ctx, "COMMIT")
	}
	if err != nil {
		tx.exec("ROLLBACK")
		return nil, nil, sqliteError(err, s.Root.Database)
	}
	fault("committed")

	if tx.steps {
		// Past COMMIT nothing fails the write. Rows that couldn't be
		// deleted stay pending, and the next write carries them out
		// again, which is harmless.
		s.finish()
	}
	return result, warnings, nil
}

// Hold takes the write lock, waiting as Write does, and runs fn holding
// it, for repair and backup, which do more under one lock than one
// change. It carries out no step itself.
func (s *Store) Hold(fn func(h *Held) *cli.Error) *cli.Error {
	lock, e := TakeLock(s.Root.Dir, s.Root.Path)
	if e != nil {
		return e
	}
	defer lock.Release()
	return fn(&Held{s})
}

// Held is a store whose write lock is held.
type Held struct{ s *Store }

// Finish carries out the steps left pending, skipping stuck ones, as a
// write does before its change.
func (h *Held) Finish() *cli.Error { return h.s.finish() }

// Change runs a change as Write does once the lock is taken and the
// pending steps finished: steps 3 to 7.
func (h *Held) Change(change Change) (any, []cli.Warning, *cli.Error) { return h.s.change(change) }

// Left is what the pending rows say once the holder is done with them.
func (h *Held) Left() []cli.Warning { return h.s.left() }

// Retry sets a stuck change's rows pending again, forgetting what its
// stuck step found, then carries out the steps pending, as Finish does.
// A step that sticks again marks its change stuck again. A change that
// neither succeeds nor sticks again, the step that stuck failing or
// waiting behind another stuck change, is set aside again as it was.
func (h *Held) Retry(change int64) *cli.Error {
	var before []Pending
	_, stuck, err := pendingRows(context.Background(), h.s.db.conn)
	if err != nil {
		return sqliteError(err, h.s.Root.Database)
	}
	for _, p := range stuck {
		if p.Change == change {
			before = append(before, p)
		}
	}
	// stuck is the field of a step that records what it found.
	if e := h.s.update(func(tx *Tx) {
		tx.exec("UPDATE pending SET stuck = 0, step = json_remove(step, '$.stuck') WHERE change = ?", change)
	}); e != nil {
		return e
	}
	if e := h.s.finish(); e != nil {
		return e
	}
	live, _, err := pendingRows(context.Background(), h.s.db.conn)
	if err != nil {
		return sqliteError(err, h.s.Root.Database)
	}
	// The step that found what it stuck on is still to run.
	var again bool
	for _, p := range live {
		for _, b := range before {
			var found struct {
				Stuck string `json:"stuck"`
			}
			if b.Seq == p.Seq && json.Unmarshal(b.Step, &found) == nil && found.Stuck != "" {
				again = true
			}
		}
	}
	if !again {
		return nil
	}
	return h.s.update(func(tx *Tx) {
		for _, p := range before {
			tx.exec("UPDATE pending SET stuck = 1, step = ? WHERE seq = ?", string(p.Step), p.Seq)
		}
	})
}

// update runs statements in one write transaction.
func (s *Store) update(fn func(tx *Tx)) *cli.Error {
	tx, e := s.begin("BEGIN IMMEDIATE")
	if e != nil {
		return e
	}
	fn(tx)
	if tx.err == nil {
		tx.exec("COMMIT")
	}
	if tx.err != nil {
		e := sqliteError(tx.err, s.Root.Database)
		tx.err = nil
		tx.exec("ROLLBACK")
		return e
	}
	return nil
}

// VacuumInto writes a copy of the database to file, a new file, as one
// snapshot, and stores WAL mode in it, as init does.
func (h *Held) VacuumInto(file string) *cli.Error {
	if _, err := h.s.db.conn.ExecContext(context.Background(), "VACUUM INTO ?", file); err != nil {
		return sqliteError(err, h.s.Root.Database)
	}
	d, e := openConn(file, file, "rw")
	if e != nil {
		return e
	}
	defer d.Close()
	var mode string
	if err := d.conn.QueryRowContext(context.Background(), "PRAGMA journal_mode = WAL").Scan(&mode); err != nil {
		return sqliteError(err, file)
	}
	return nil
}

// left is what the pending rows say once a write is done with them.
func (s *Store) left() []cli.Warning {
	live, stuck, err := pendingRows(context.Background(), s.db.conn)
	if err != nil {
		return nil
	}
	return s.steps.Warnings(live, stuck)
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
		return asError(err, s.Root.Database, tx.ArchivedEntry)
	}
	return nil
}

func (s *Store) begin(stmt string) (*Tx, *cli.Error) {
	ctx := context.Background()
	if _, err := s.db.conn.ExecContext(ctx, stmt); err != nil {
		return nil, sqliteError(err, s.Root.Database)
	}
	now := core.FormatTimestamp(time.Now())
	return &Tx{conn: s.db.conn, ctx: ctx, Now: now, encode: s.steps.Encode, root: s.Root.Path,
		recorder: recorder{command: s.Command, at: now}}, nil
}

// querier is a connection or a transaction.
type querier interface {
	QueryContext(ctx context.Context, query string, args ...any) (*sql.Rows, error)
}

// pendingRows reads the pending rows, oldest first: live are still to be
// carried out, stuck are set aside.
func pendingRows(ctx context.Context, q querier) (live, stuck []Pending, err error) {
	rows, err := q.QueryContext(ctx, "SELECT seq, change, step, stuck FROM pending ORDER BY seq")
	if err != nil {
		return nil, nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var p Pending
		var step string
		var st bool
		if err := rows.Scan(&p.Seq, &p.Change, &step, &st); err != nil {
			return nil, nil, err
		}
		p.Step = json.RawMessage(step)
		if st {
			stuck = append(stuck, p)
		} else {
			live = append(live, p)
		}
	}
	return live, stuck, rows.Err()
}

// Pending reads the pending rows as the transaction sees them, oldest
// first: live are still to be carried out, stuck are set aside.
func (t *Tx) Pending() (live, stuck []Pending, err error) {
	return pendingRows(t.ctx, t.conn)
}

// finish carries out the steps left pending, skipping stuck ones, then
// deletes the rows of those done and marks the stuck changes, in one
// transaction.
func (s *Store) finish() *cli.Error {
	live, stuck, err := pendingRows(context.Background(), s.db.conn)
	if err != nil {
		return sqliteError(err, s.Root.Database)
	}
	if len(live) == 0 {
		return nil
	}
	out := s.steps.Carry(live, stuck)
	if len(out.Done) == 0 && len(out.Stuck) == 0 {
		return nil
	}
	if err := fault("delete"); err != nil {
		return cli.IO("could not delete the rows of the steps carried out: "+err.Error(), s.Root.Database)
	}
	tx, e := s.begin("BEGIN IMMEDIATE")
	if e != nil {
		return e
	}
	for _, seq := range out.Done {
		tx.exec("DELETE FROM pending WHERE seq = ?", seq)
	}
	for _, p := range out.Stuck {
		tx.exec("UPDATE pending SET step = ? WHERE seq = ?", string(p.Step), p.Seq)
		tx.exec("UPDATE pending SET stuck = 1 WHERE change = ?", p.Change)
	}
	if tx.err == nil {
		tx.exec("COMMIT")
	}
	if tx.err != nil {
		e := sqliteError(tx.err, s.Root.Database)
		tx.err = nil
		tx.exec("ROLLBACK")
		return e
	}
	return nil
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
	// encode is the store's Steps' Encode.
	encode func(seq int64, step any) (json.RawMessage, error)
	// root is the root's path as written, which the archive index's
	// paths are relative to.
	root string
	// recorder holds the events the change records, at Now.
	recorder
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

// nextSeq is the seq the next step added will have.
func (t *Tx) nextSeq() (int64, error) {
	var seq int64
	err := t.QueryRow("SELECT coalesce(max(seq), 0) + 1 FROM pending").Scan(&seq)
	return seq, err
}

// AddStep adds a filesystem step to the change, as the store's Steps
// encode it, and returns its seq.
func (t *Tx) AddStep(step any) (int64, error) {
	seq, err := t.nextSeq()
	if err != nil {
		return 0, err
	}
	b, err := t.encode(seq, step)
	if err != nil {
		return 0, err
	}
	if !t.steps {
		t.steps, t.change = true, seq
	}
	if _, err := t.Exec("INSERT INTO pending (seq, change, step) VALUES (?, ?, ?)", seq, t.change, string(b)); err != nil {
		return 0, err
	}
	return seq, nil
}
