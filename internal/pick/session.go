package pick

import (
	"crypto/rand"
	"encoding/hex"
	"errors"
	"io/fs"
	"path"
	"path/filepath"
	"strings"

	"github.com/phansen314/ftask/internal/errs"
	"github.com/phansen314/ftask/internal/fsys"
)

// SessionVar names the session directory in fzf's environment, for the
// helper (pick-spec.md, Session).
const SessionVar = "FTASK_PICK_SESSION"

// sessionPrefix begins a session directory's name: ftask-pick-<random>.
const sessionPrefix = "ftask-pick-"

// markerName is the file that makes a directory a session, and markerText
// its contents: the helper refuses a directory without it, so
// FTASK_PICK_SESSION pointed anywhere else names no session.
const (
	markerName = "session"
	markerText = "ftask pick session 1\n"
)

// Session is a session directory: private (0700), outside the root, and
// removed when pick exits. Every file in it is written atomically, since the
// helper's commands may run while another writes (pick-spec.md, What runs
// concurrently).
type Session struct {
	Dir  string
	root fsys.Root
	// base is the directory the session was made in, open, for removing it;
	// nil in the helper, which never removes the session.
	base fsys.Root
}

// sessionBase is where sessions are made: $XDG_RUNTIME_DIR if set, else
// the system temp directory, $TMPDIR or /tmp as os.TempDir has it on Unix.
func sessionBase(environ []string) string {
	if d := lookupEnv(environ, "XDG_RUNTIME_DIR"); d != "" {
		return d
	}
	if d := lookupEnv(environ, "TMPDIR"); d != "" {
		return d
	}
	return "/tmp"
}

// lookupEnv is key's value in environ (KEY=value entries), the last if it is
// repeated, as getenv has it; "" if unset.
func lookupEnv(environ []string, key string) string {
	v := ""
	for _, kv := range environ {
		if k, val, ok := strings.Cut(kv, "="); ok && k == key {
			v = val
		}
	}
	return v
}

// newSession makes a session directory under base, mode 0700, and marks it.
// A relative base is made absolute, as the helper accepts only an absolute
// session directory.
func newSession(fsy fsys.FS, base string) (*Session, *errs.Error) {
	base, err := filepath.Abs(base)
	if err != nil {
		return nil, errs.FromOS(base, err)
	}
	b, err := fsy.OpenRoot(base)
	if err != nil {
		return nil, errs.FromOS(base, err)
	}
	var name string
	for range 10 { // a name taken is a collision, or someone guessing
		name = sessionPrefix + random()
		if err = b.Mkdir(name, 0o700); !errors.Is(err, fs.ErrExist) {
			break
		}
	}
	if err != nil {
		b.Close()
		return nil, errs.FromOS(path.Join(base, name), err)
	}
	s := &Session{Dir: path.Join(base, name), base: b}
	if s.root, err = fsy.OpenRoot(s.Dir); err != nil {
		e := errs.FromOS(s.Dir, err)
		s.Remove()
		return nil, e
	}
	if e := s.Write(markerName, []byte(markerText)); e != nil {
		s.Remove()
		return nil, e
	}
	return s, nil
}

func random() string {
	b := make([]byte, 8)
	rand.Read(b)
	return hex.EncodeToString(b)
}

// openSession opens the session the helper runs in, named by
// FTASK_PICK_SESSION in environ. With none, or one that is not a session,
// the helper fails with usage (pick-spec.md, Session).
func openSession(fsy fsys.FS, environ []string) (*Session, *errs.Error) {
	dir := lookupEnv(environ, SessionVar)
	if dir == "" {
		return nil, notInSession(SessionVar + " is not set")
	}
	if !path.IsAbs(dir) {
		return nil, notInSession(SessionVar + " is not an absolute path")
	}
	r, err := fsy.OpenRoot(dir)
	if err != nil {
		return nil, notInSession(SessionVar + " names no directory")
	}
	s := &Session{Dir: dir, root: r}
	if b, err := r.ReadFile(markerName); err != nil || string(b) != markerText {
		r.Close()
		return nil, notInSession(SessionVar + " names no pick session")
	}
	return s, nil
}

func notInSession(why string) *errs.Error {
	return errs.Usage([]errs.UsageProblem{{Reason: "ftask __pick runs only inside ftask pick: " + why}})
}

// Write replaces the session file name with data, atomically: a temp file
// in the session, renamed over the old one. Nothing is synced: a session
// does not outlive a crash.
func (s *Session) Write(name string, data []byte) *errs.Error {
	f, tmp, err := s.root.CreateTemp(".")
	if err != nil {
		return errs.FromOS(s.Dir, err)
	}
	_, err = f.Write(data)
	if cerr := f.Close(); err == nil {
		err = cerr
	}
	if err == nil {
		err = s.root.Rename(tmp, name)
	}
	if err != nil {
		s.root.Remove(tmp)
		return errs.FromOS(path.Join(s.Dir, name), err)
	}
	return nil
}

// Read reads the session file name; ok is false if there is none.
func (s *Session) Read(name string) (data []byte, ok bool, e *errs.Error) {
	b, err := s.root.ReadFile(name)
	switch {
	case errors.Is(err, fs.ErrNotExist):
		return nil, false, nil
	case err != nil:
		return nil, false, errs.FromOS(path.Join(s.Dir, name), err)
	}
	return b, true, nil
}

// Delete removes the session file name; one already gone is not an error.
func (s *Session) Delete(name string) *errs.Error {
	if err := s.root.Remove(name); err != nil && !errors.Is(err, fs.ErrNotExist) {
		return errs.FromOS(path.Join(s.Dir, name), err)
	}
	return nil
}

// Close closes the session without removing it: the helper's.
func (s *Session) Close() {
	s.root.Close()
}

// Remove removes the session directory and everything in it: pick's, as it
// exits. A failure leaves it behind, as a crash would; there is nothing to
// report it to.
func (s *Session) Remove() {
	if s.root != nil {
		s.root.Close()
	}
	s.base.RemoveAll(path.Base(s.Dir))
	s.base.Close()
}
